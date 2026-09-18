package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// SyncController is the lifecycle boundary used by Runtime.
type SyncController interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// SyncEngine runs one independently cancellable worker per enabled remote
// provider. It pushes durable operations before pulling remote cache changes.
type SyncEngine struct {
	repo     *Repository
	registry *Registry
	logger   *slog.Logger
	interval time.Duration
	retry    bool
	now      func() time.Time

	mu      sync.Mutex
	workers map[ProviderID]*syncWorker
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

type syncWorker struct {
	provider Provider
	wake     chan struct{}
}

// SyncEngineOptions configures a synchronization engine.
type SyncEngineOptions struct {
	Interval    time.Duration
	RetryFailed bool
	Logger      *slog.Logger
	Now         func() time.Time
}

// NewSyncEngine constructs a stopped synchronization engine.
func NewSyncEngine(repo *Repository, registry *Registry, options SyncEngineOptions) (*SyncEngine, error) {
	if repo == nil {
		return nil, errors.New("new sync engine: nil repository")
	}
	if registry == nil {
		return nil, errors.New("new sync engine: nil provider registry")
	}
	if options.Interval <= 0 {
		options.Interval = time.Minute
	}
	if options.Logger == nil {
		options.Logger = DiscardLogger()
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &SyncEngine{
		repo:     repo,
		registry: registry,
		logger:   options.Logger,
		interval: options.Interval,
		retry:    options.RetryFailed,
		now:      options.Now,
		workers:  make(map[ProviderID]*syncWorker),
	}, nil
}

// Start recovers interrupted queue rows and starts remote workers. Starting a
// worker is asynchronous, so this method never waits for network I/O.
func (e *SyncEngine) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("start sync engine: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return errors.New("sync engine is already started")
	}
	e.mu.Unlock()
	if err := e.repo.RecoverInFlight(ctx); err != nil {
		return fmt.Errorf("start sync engine: %w", err)
	}
	child, cancel := context.WithCancel(ctx)
	providers := e.registry.All()
	workers := make(map[ProviderID]*syncWorker)
	for _, provider := range providers {
		record := provider.Record()
		if !record.Enabled || !provider.Capabilities().RemoteSync {
			continue
		}
		workers[provider.ID()] = &syncWorker{provider: provider, wake: make(chan struct{}, 1)}
	}
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		cancel()
		return errors.New("sync engine is already started")
	}
	e.cancel = cancel
	e.done = make(chan struct{})
	e.workers = workers
	e.started = true
	e.mu.Unlock()
	var group sync.WaitGroup
	for _, worker := range workers {
		worker := worker
		group.Add(1)
		go func() {
			defer group.Done()
			e.runWorker(child, worker)
		}()
	}
	go func() {
		group.Wait()
		close(e.done)
	}()
	return nil
}

// Stop cancels workers and waits for every provider worker to finish. The
// caller must supply a live cleanup context when the root context is already
// canceled.
func (e *SyncEngine) Stop(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("stop sync engine: nil context")
	}
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil
	}
	cancel := e.cancel
	done := e.done
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
		e.mu.Lock()
		e.started = false
		e.cancel = nil
		e.done = nil
		e.mu.Unlock()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop sync engine: %w", ctx.Err())
	}
}

// Wait waits for workers to finish because their parent context was canceled.
func (e *SyncEngine) Wait(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("wait sync engine: nil context")
	}
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Trigger asks one provider to run a cycle before its normal interval.
func (e *SyncEngine) Trigger(providerID ProviderID) error {
	if e == nil {
		return errors.New("trigger sync: nil engine")
	}
	e.mu.Lock()
	worker, ok := e.workers[providerID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("trigger sync %s: %w", providerID, ErrNotFound)
	}
	select {
	case worker.wake <- struct{}{}:
	default:
	}
	return nil
}

// Running reports whether the engine owns active workers.
func (e *SyncEngine) Running() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

func (e *SyncEngine) runWorker(ctx context.Context, worker *syncWorker) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		err := e.runCycle(ctx, worker.provider)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logContext(ctx, e.logger, slog.LevelWarn, "provider sync failed", "provider_id", worker.provider.ID(), "error", SafeErrorText(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-worker.wake:
		}
	}
}

func (e *SyncEngine) runCycle(ctx context.Context, provider Provider) error {
	if err := e.push(ctx, provider); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = e.repo.SetProviderSyncState(cleanupCtx, provider.ID(), nil, err)
		cancel()
		return err
	}
	if err := e.pull(ctx, provider); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = e.repo.SetProviderSyncState(cleanupCtx, provider.ID(), nil, err)
		cancel()
		return err
	}
	now := e.now().UTC()
	if err := e.repo.SetProviderSyncState(ctx, provider.ID(), &now, nil); err != nil {
		return fmt.Errorf("record provider %s sync: %w", provider.ID(), err)
	}
	return nil
}

func (e *SyncEngine) push(ctx context.Context, provider Provider) error {
	var retryDelay func(int) time.Duration
	if e.retry {
		retryDelay = RetryDelay
	}
	for {
		operation, err := e.repo.ClaimNext(ctx, provider.ID(), e.now().UTC(), retryDelay)
		if errors.Is(err, ErrQueueEmpty) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim %s operation: %w", provider.ID(), err)
		}
		if err := e.applyOperation(ctx, provider, operation); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				releaseErr := e.repo.Release(releaseCtx, operation.ID, operation.ProviderID)
				cancel()
				if releaseErr != nil {
					return fmt.Errorf("release interrupted operation %s: %w", operation.ID, releaseErr)
				}
				return err
			}
			failCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			failErr := e.repo.Fail(failCtx, operation.ID, operation.ProviderID, err)
			cancel()
			if failErr != nil {
				return fmt.Errorf("record operation %s failure: %w", operation.ID, failErr)
			}
			logContext(ctx, e.logger, slog.LevelWarn, "sync operation failed", "provider_id", provider.ID(), "operation_id", operation.ID, "error", SafeErrorText(err))
			return nil
		}
		if err := e.repo.Complete(ctx, operation.ID, operation.ProviderID); err != nil {
			return fmt.Errorf("complete operation %s: %w", operation.ID, err)
		}
	}
}

func (e *SyncEngine) applyOperation(ctx context.Context, provider Provider, operation SyncOperation) error {
	if operation.ProviderID != provider.ID() {
		return ErrProviderMismatch
	}
	if operation.EntityType != EntityTypeTask {
		return ErrUnsupported
	}
	var task Task
	if err := json.Unmarshal(operation.Payload, &task); err != nil {
		return fmt.Errorf("decode task operation %s: %w", operation.ID, err)
	}
	if task.ProviderID != provider.ID() {
		return ErrProviderMismatch
	}
	switch operation.Operation {
	case OperationCreate:
		created, err := provider.CreateTask(ctx, task)
		if err != nil {
			return err
		}
		return e.storeSyncedTask(ctx, task, created)
	case OperationUpdate:
		updated, err := provider.UpdateTask(ctx, task)
		if err != nil {
			return err
		}
		return e.storeSyncedTask(ctx, task, updated)
	case OperationDelete:
		return provider.DeleteTask(ctx, task)
	default:
		return fmt.Errorf("operation %s: unknown operation %q", operation.ID, operation.Operation)
	}
}

func (e *SyncEngine) storeSyncedTask(ctx context.Context, original, returned Task) error {
	if returned.ID == "" {
		returned.ID = original.ID
	}
	returned.ProviderID = original.ProviderID
	returned.ListID = original.ListID
	if returned.Title == "" {
		returned.Title = original.Title
	}
	if returned.Description == "" {
		returned.Description = original.Description
	}
	if returned.CreatedAt.IsZero() {
		returned.CreatedAt = original.CreatedAt
	}
	returned.SyncState = SyncStateSynced
	if returned.UpdatedAt.IsZero() {
		returned.UpdatedAt = e.now().UTC()
	}
	return e.repo.UpsertRemoteTask(ctx, returned)
}

func (e *SyncEngine) pull(ctx context.Context, provider Provider) error {
	spaces, err := provider.FetchSpaces(ctx)
	if err != nil {
		return err
	}
	for _, space := range spaces {
		if space.ProviderID != provider.ID() {
			return ErrProviderMismatch
		}
		if err := e.repo.UpsertRemoteSpace(ctx, space); err != nil {
			return fmt.Errorf("store space %s: %w", space.ID, err)
		}
		lists, err := provider.FetchLists(ctx, space.ID)
		if err != nil {
			return fmt.Errorf("fetch lists for space %s: %w", space.ID, err)
		}
		for _, list := range lists {
			if list.ProviderID != provider.ID() || list.SpaceID != space.ID {
				return ErrProviderMismatch
			}
			if err := e.repo.UpsertRemoteList(ctx, list); err != nil {
				return fmt.Errorf("store list %s: %w", list.ID, err)
			}
			tasks, err := provider.FetchTasks(ctx, list.ID)
			if err != nil {
				return fmt.Errorf("fetch tasks for list %s: %w", list.ID, err)
			}
			for _, task := range tasks {
				if task.ProviderID != provider.ID() || task.ListID != list.ID {
					return ErrProviderMismatch
				}
				if err := e.repo.UpsertRemoteTask(ctx, task); err != nil {
					return fmt.Errorf("store task %s: %w", task.ID, err)
				}
			}
		}
	}
	return nil
}

// RetryDelay returns the bounded exponential retry delay for an attempt count.
func RetryDelay(attempts int) time.Duration {
	if attempts <= 1 {
		return time.Second
	}
	delays := [...]time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}
	if attempts > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempts-1]
}
