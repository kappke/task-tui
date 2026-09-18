package sync

import (
	"context"
	"errors"
	stdsync "sync"
)

// EngineOptions configures all provider workers and the engine event buffer.
type EngineOptions struct {
	Worker      WorkerOptions
	EventSink   EventSink
	EventBuffer int
}

// Engine owns one independent Worker per remote provider instance. It never
// uses a shared errgroup cancellation path: a provider failure is contained by
// its own Run loop and cannot stop unrelated workers.
type Engine struct {
	queue   Queue
	workers map[ProviderID]*Worker
	events  chan Event

	mu      stdsync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
	runErrs []error
}

// NewEngine creates workers for remote providers and skips local/non-remote
// providers. queue and providers may use either the sync-layer contracts or
// the foundation repository/provider contracts; this boundary adapts them
// without leaking compatibility code into other packages. Provider IDs must
// be unique among remote workers.
func NewEngine(queue any, providers any, options EngineOptions) (*Engine, error) {
	adaptedProviders, err := adaptProviderInputs(providers)
	if err != nil {
		return nil, err
	}
	adaptedQueue, err := adaptEngineQueueInput(queue, adaptedProviders)
	if err != nil {
		return nil, err
	}
	return newEngine(adaptedQueue, adaptedProviders, options)
}

func newEngine(queue Queue, providers []Provider, options EngineOptions) (*Engine, error) {
	if queue == nil {
		return nil, errors.New("sync queue is nil")
	}
	buffer := options.EventBuffer
	if buffer <= 0 {
		buffer = 64
	}
	engine := &Engine{
		queue:   queue,
		workers: make(map[ProviderID]*Worker),
		events:  make(chan Event, buffer),
	}

	for _, provider := range providers {
		if provider == nil {
			return nil, errors.New("sync provider is nil")
		}
		if !provider.Capabilities().RequiresNetwork() {
			continue
		}
		providerID := provider.ID()
		if _, exists := engine.workers[providerID]; exists {
			return nil, errors.New("duplicate sync provider ID: " + string(providerID))
		}
		workerOptions := options.Worker
		previousSink := workerOptions.EventSink
		workerOptions.EventSink = func(event Event) {
			if previousSink != nil {
				previousSink(event)
			}
			if options.EventSink != nil {
				options.EventSink(event)
			}
			select {
			case engine.events <- event:
			default:
			}
		}
		worker, err := NewWorker(queue, provider, workerOptions)
		if err != nil {
			return nil, err
		}
		engine.workers[providerID] = worker
	}
	return engine, nil
}

// Events returns a buffered, non-blocking stream of worker events. Events are
// intentionally not closed on Stop so a caller retaining a Worker cannot send
// into a closed engine channel after a restart.
func (e *Engine) Events() <-chan Event {
	return e.events
}

// Worker returns the worker for a remote provider, if one was created.
func (e *Engine) Worker(providerID ProviderID) (*Worker, bool) {
	worker, ok := e.workers[providerID]
	return worker, ok
}

// WorkerCount reports the number of network workers. Local providers are not
// counted because they never start a network goroutine.
func (e *Engine) WorkerCount() int {
	return len(e.workers)
}

// Running reports whether the engine currently owns active worker goroutines.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

// Start launches all provider workers. Each goroutine has the same lifecycle
// context but no worker can cancel that context because of its own failure.
func (e *Engine) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return ErrEngineStarted
	}
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.done = make(chan struct{})
	e.runErrs = nil
	e.started = true
	workers := make([]*Worker, 0, len(e.workers))
	for _, worker := range e.workers {
		workers = append(workers, worker)
	}
	e.mu.Unlock()

	var group stdsync.WaitGroup
	group.Add(len(workers))
	for _, worker := range workers {
		go func(worker *Worker) {
			defer group.Done()
			if err := worker.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				e.mu.Lock()
				e.runErrs = append(e.runErrs, err)
				e.mu.Unlock()
			}
		}(worker)
	}
	go func() {
		group.Wait()
		// done is closed by the goroutine that owns this run, while the
		// mutex protects Stop from observing a stale lifecycle channel.
		e.mu.Lock()
		done := e.done
		e.mu.Unlock()
		close(done)
	}()
	return nil
}

// Run starts the engine and waits for ctx cancellation, then stops all
// independent workers. It returns only lifecycle/engine errors.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return e.StopNow()
}

// Stop cancels all workers and waits for every worker goroutine until ctx is
// canceled. Pending remote work is not awaited, so durable queue rows resume
// on the next run.
func (e *Engine) Stop(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil
	}
	cancel := e.cancel
	done := e.done
	e.mu.Unlock()

	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	e.mu.Lock()
	e.started = false
	e.cancel = nil
	e.done = nil
	err := errors.Join(e.runErrs...)
	e.runErrs = nil
	e.mu.Unlock()
	return err
}

// StopNow is the unbounded-wait convenience form of Stop.
func (e *Engine) StopNow() error {
	return e.Stop(context.Background())
}

// SyncOnce runs all remote provider workers concurrently without allowing one
// provider error to cancel another. It is useful for explicit refresh actions
// and does not start long-lived Run loops.
func (e *Engine) SyncOnce(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	workers := make([]*Worker, 0, len(e.workers))
	for _, worker := range e.workers {
		workers = append(workers, worker)
	}
	errs := make(chan error, len(workers))
	var group stdsync.WaitGroup
	group.Add(len(workers))
	for _, worker := range workers {
		go func(worker *Worker) {
			defer group.Done()
			if err := worker.SyncOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errs <- err
			}
		}(worker)
	}
	group.Wait()
	close(errs)
	if err := ctx.Err(); err != nil {
		return err
	}
	var joined error
	for err := range errs {
		joined = errors.Join(joined, err)
	}
	return joined
}

// Statuses returns deterministic provider-ordered worker snapshots.
func (e *Engine) Statuses() []WorkerStatus {
	ids := make([]ProviderID, 0, len(e.workers))
	for providerID := range e.workers {
		ids = append(ids, providerID)
	}
	// Provider IDs are short and sorting keeps status displays stable without
	// exposing the mutable worker map.
	sortProviderIDs(ids)
	statuses := make([]WorkerStatus, 0, len(ids))
	for _, providerID := range ids {
		statuses = append(statuses, e.workers[providerID].Status())
	}
	return statuses
}

func sortProviderIDs(ids []ProviderID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
