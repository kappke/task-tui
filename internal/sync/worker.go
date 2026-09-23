package sync

import (
	"context"
	"errors"
	"fmt"
	stdsync "sync"
	"time"
)

const (
	defaultSyncInterval = time.Minute
	defaultLeaseTTL     = 5 * time.Minute
	defaultCleanupWait  = 5 * time.Second
)

// WorkerState is the observable lifecycle state for one provider worker.
type WorkerState string

const (
	WorkerIdle    WorkerState = "idle"
	WorkerRunning WorkerState = "running"
	WorkerSynced  WorkerState = "synced"
	WorkerBackoff WorkerState = "backoff"
	WorkerFailed  WorkerState = "failed"
	WorkerStopped WorkerState = "stopped"
)

// EventKind identifies useful worker lifecycle transitions.
type EventKind string

const (
	EventWorkerStarted      EventKind = "worker_started"
	EventWorkerStopped      EventKind = "worker_stopped"
	EventSyncStarted        EventKind = "sync_started"
	EventOperationClaimed   EventKind = "operation_claimed"
	EventOperationAttempted EventKind = "operation_attempted"
	EventOperationCompleted EventKind = "operation_completed"
	EventOperationFailed    EventKind = "operation_failed"
	EventRetryScheduled     EventKind = "retry_scheduled"
	EventPullCompleted      EventKind = "pull_completed"
	EventSyncCompleted      EventKind = "sync_completed"
	EventSyncFailed         EventKind = "sync_failed"
	EventProgress           EventKind = "sync_progress"
)

// Event is delivered to an optional non-blocking event sink.
type Event struct {
	At          time.Time
	ProviderID  ProviderID
	Kind        EventKind
	State       WorkerState
	OperationID OperationID
	Attempts    int
	Failure     FailureKind
	Decision    RetryDecision
	Err         error
	Message     string
}

// EventSink receives worker status events. Sinks should be quick; the Worker
// calls them synchronously after state changes.
type EventSink func(Event)

// WorkerStatus is a race-safe snapshot returned by Status.
type WorkerStatus struct {
	ProviderID      ProviderID
	State           WorkerState
	LastSyncAt      time.Time
	NextRetryAt     time.Time
	LastError       error
	Failure         FailureKind
	Decision        RetryDecision
	Operations      uint64
	Attempts        uint64
	FailureAttempts int
}

// WorkerOptions controls timing and observability. Zero values select the
// documented production defaults.
type WorkerOptions struct {
	Clock          Clock
	RetryPolicy    RetryPolicy
	Interval       time.Duration
	LeaseTTL       time.Duration
	CleanupTimeout time.Duration
	EventSink      EventSink
}

// DefaultWorkerOptions returns production timing defaults with deterministic
// backoff; jitter can be supplied by replacing RetryPolicy.Jitter.
func DefaultWorkerOptions() WorkerOptions {
	return WorkerOptions{
		Clock:          RealClock{},
		RetryPolicy:    DefaultRetryPolicy(),
		Interval:       defaultSyncInterval,
		LeaseTTL:       defaultLeaseTTL,
		CleanupTimeout: defaultCleanupWait,
	}
}

// Worker owns exactly one provider instance and serializes its sync cycles.
// It never stores a context; callers control its lifecycle through Run or
// SyncOnce contexts.
type Worker struct {
	queue    Queue
	provider Provider
	options  WorkerOptions

	once chan struct{}

	lifecycleMu stdsync.Mutex
	running     bool

	statusMu stdsync.RWMutex
	status   WorkerStatus
}

// NewWorker constructs a worker only for a provider with remote capabilities.
// queue and provider may be the sync-layer contracts or the foundation
// repository.SyncQueue/internal/provider.Provider contracts; adaptation stays
// inside this package. Local and other non-remote providers return
// ErrNonRemoteProvider and cannot accidentally acquire a network loop.
func NewWorker(queue any, provider any, options WorkerOptions) (*Worker, error) {
	adaptedProvider, err := adaptProviderInput(provider)
	if err != nil {
		return nil, err
	}
	adaptedQueue, err := adaptQueueInput(queue, adaptedProvider.ID())
	if err != nil {
		return nil, err
	}
	return newWorker(adaptedQueue, adaptedProvider, options)
}

func newWorker(queue Queue, provider Provider, options WorkerOptions) (*Worker, error) {
	if queue == nil {
		return nil, errors.New("sync queue is nil")
	}
	if provider == nil {
		return nil, errors.New("sync provider is nil")
	}
	if !provider.Capabilities().RequiresNetwork() {
		return nil, ErrNonRemoteProvider
	}
	providerID := provider.ID()
	if providerID == "" {
		return nil, errors.New("sync provider ID is empty")
	}
	options = normalizeWorkerOptions(options)
	worker := &Worker{
		queue:    queue,
		provider: provider,
		options:  options,
		once:     make(chan struct{}, 1),
		status: WorkerStatus{
			ProviderID: providerID,
			State:      WorkerIdle,
		},
	}
	if reporter, ok := provider.(interface{ SetSyncProgress(func(string)) }); ok {
		reporter.SetSyncProgress(func(message string) {
			worker.emit(context.Background(), Event{Kind: EventProgress, State: WorkerRunning, Message: message})
		})
	}
	return worker, nil
}

func normalizeWorkerOptions(options WorkerOptions) WorkerOptions {
	defaults := DefaultWorkerOptions()
	if options.Clock == nil {
		options.Clock = defaults.Clock
	}
	if len(options.RetryPolicy.Schedule) == 0 {
		options.RetryPolicy.Schedule = defaults.RetryPolicy.Schedule
	}
	if options.Interval <= 0 {
		options.Interval = defaults.Interval
	}
	if options.LeaseTTL <= 0 {
		options.LeaseTTL = defaults.LeaseTTL
	}
	if options.CleanupTimeout <= 0 {
		options.CleanupTimeout = defaults.CleanupTimeout
	}
	return options
}

// ProviderID returns the provider instance owned by this worker.
func (w *Worker) ProviderID() ProviderID {
	return w.provider.ID()
}

// Running reports whether this worker owns an active Run loop.
func (w *Worker) Running() bool {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	return w.running
}

// Status returns a copy of the latest observable worker state.
func (w *Worker) Status() WorkerStatus {
	w.statusMu.RLock()
	defer w.statusMu.RUnlock()
	return w.status
}

// SyncOnce recovers this provider's stale leases, drains currently eligible
// queue operations in repository order, and then performs an optional pull.
// A provider failure is persisted and returned; Run treats it as an isolated
// retry condition rather than cancelling other workers.
func (w *Worker) SyncOnce(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		w.setState(WorkerStopped)
		return err
	}
	if err := acquire(ctx, w.once); err != nil {
		return err
	}
	defer release(w.once)

	w.setStatus(WorkerRunning, nil, "", RetryDecision{Kind: DecisionNone})
	w.emit(ctx, Event{Kind: EventSyncStarted, State: WorkerRunning})

	now := w.options.Clock.Now()
	if err := w.queue.RecoverStale(ctx, w.ProviderID(), now.Add(-w.options.LeaseTTL)); err != nil {
		return w.infrastructureFailure(ctx, EventSyncFailed, "recover stale sync leases", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return w.cancelClaim(ctx, "before claiming sync operation", err, Operation{})
		}

		operation, err := w.queue.Claim(ctx, w.ProviderID(), w.options.Clock.Now())
		if errors.Is(err, ErrQueueEmpty) || (err == nil && operation.ID == "") {
			break
		}
		if err != nil {
			return w.infrastructureFailure(ctx, EventSyncFailed, "claim sync operation", err)
		}
		w.emit(ctx, Event{Kind: EventOperationClaimed, State: WorkerRunning, OperationID: operation.ID})

		if operation.ProviderID != w.ProviderID() {
			err := fmt.Errorf("%w: worker %s claimed operation %s for %s", ErrProviderMismatch,
				w.ProviderID(), operation.ID, operation.ProviderID)
			return w.isolationFailure(ctx, operation, err)
		}
		if err := ctx.Err(); err != nil {
			return w.cancelClaim(ctx, "before attempting sync operation", err, operation)
		}

		attemptAt := w.options.Clock.Now()
		if err := w.queue.MarkAttempt(ctx, operation.ID, attemptAt); err != nil {
			if releaseErr := w.releaseClaim(ctx, operation.ProviderID, operation.ID, operation.LeaseOwner); releaseErr != nil {
				err = errors.Join(err, releaseErr)
			}
			return w.infrastructureFailure(ctx, EventSyncFailed, "persist sync attempt", err)
		}
		operation.Attempts++
		w.incrementAttempts()
		w.emit(ctx, Event{
			Kind:        EventOperationAttempted,
			State:       WorkerRunning,
			OperationID: operation.ID,
			Attempts:    operation.Attempts,
		})

		providerErr := w.push(ctx, operation)
		if providerErr != nil {
			if ctx.Err() != nil {
				return w.cancelClaim(ctx, "cancel sync operation", ctx.Err(), operation)
			}
			decision := w.options.RetryPolicy.Decide(providerErr, operation.Attempts)
			if decision.Kind == DecisionCanceled {
				return w.cancelClaim(ctx, "provider canceled sync operation", providerErr, operation)
			}
			if err := w.persistFailure(ctx, operation, providerErr, decision); err != nil {
				return err
			}
			return providerErr
		}

		completeAt := w.options.Clock.Now()
		persistCtx, cancel := w.persistenceContext(ctx)
		completeErr := w.queue.Complete(persistCtx, operation.ProviderID, operation.ID, operation.LeaseOwner, completeAt)
		cancel()
		if completeErr != nil {
			return w.infrastructureFailure(ctx, EventSyncFailed, "complete sync operation", completeErr)
		}
		w.incrementOperations()
		w.emit(ctx, Event{
			Kind:        EventOperationCompleted,
			State:       WorkerRunning,
			OperationID: operation.ID,
			Attempts:    operation.Attempts,
		})
	}

	if w.provider.Capabilities().Pull {
		if err := w.pull(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			decision := w.providerFailureDecision(err)
			w.setFailure(WorkerFailed, err, decision)
			w.emit(ctx, Event{Kind: EventSyncFailed, State: WorkerFailed, Failure: decision.Failure, Decision: decision, Err: err})
			if decision.ShouldRetry() {
				w.emit(ctx, Event{Kind: EventRetryScheduled, State: WorkerBackoff, Failure: decision.Failure, Decision: decision, Err: err})
			}
			return err
		}
		w.emit(ctx, Event{Kind: EventPullCompleted, State: WorkerRunning})
	}

	completedAt := w.options.Clock.Now()
	w.setSynced(completedAt)
	w.emit(ctx, Event{Kind: EventSyncCompleted, State: WorkerSynced})
	return nil
}

// Run starts an owned loop that responds to ctx cancellation. Provider and
// queue failures are reported through status/events and retried independently;
// only lifecycle or wait errors end this worker.
func (w *Worker) Run(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	if !w.beginRun() {
		return ErrWorkerRunning
	}
	defer w.endRun()

	w.emit(ctx, Event{Kind: EventWorkerStarted, State: WorkerRunning})
	for {
		if err := ctx.Err(); err != nil {
			w.setState(WorkerStopped)
			w.emit(context.Background(), Event{Kind: EventWorkerStopped, State: WorkerStopped, Err: err})
			return err
		}

		err := w.SyncOnce(ctx)
		if ctx.Err() != nil {
			stopErr := ctx.Err()
			w.setState(WorkerStopped)
			w.emit(context.Background(), Event{Kind: EventWorkerStopped, State: WorkerStopped, Err: stopErr})
			return stopErr
		}

		delay := w.nextDelay(err)
		if err != nil {
			w.setState(WorkerBackoff)
		} else {
			w.setState(WorkerIdle)
		}
		if err := w.options.Clock.Wait(ctx, delay); err != nil {
			w.setState(WorkerStopped)
			w.emit(context.Background(), Event{Kind: EventWorkerStopped, State: WorkerStopped, Err: err})
			return err
		}
	}
}

func (w *Worker) push(ctx context.Context, operation Operation) error {
	switch provider := w.provider.(type) {
	case Pusher:
		return provider.Push(ctx, operation)
	case OperationApplier:
		return provider.Apply(ctx, operation)
	case OperationHandler:
		return provider.Handle(ctx, operation)
	default:
		return fmt.Errorf("%w: provider %s has no operation handler", ErrUnsupported, w.ProviderID())
	}
}

func (w *Worker) pull(ctx context.Context) error {
	switch provider := w.provider.(type) {
	case Puller:
		return provider.Pull(ctx)
	case Syncer:
		return provider.Sync(ctx)
	default:
		// Pull is optional at the provider boundary. A remote write-only
		// provider must still be able to drain its durable push queue.
		return nil
	}
}

func (w *Worker) persistFailure(ctx context.Context, operation Operation, providerErr error, decision RetryDecision) error {
	failure := Failure{
		Kind:     decision.Failure,
		Err:      providerErr,
		Attempts: operation.Attempts,
	}
	if decision.ShouldRetry() {
		next := w.options.Clock.Now().Add(decision.Delay)
		failure.RetryAt = &next
	}

	persistCtx, cancel := w.persistenceContext(ctx)
	err := w.queue.Fail(persistCtx, operation.ProviderID, operation.ID, operation.LeaseOwner, failure)
	cancel()
	if err != nil {
		return w.infrastructureFailure(ctx, EventSyncFailed, "persist sync failure", err)
	}

	w.setFailure(WorkerFailed, providerErr, decision)
	w.emit(ctx, Event{
		Kind:        EventOperationFailed,
		State:       WorkerFailed,
		OperationID: operation.ID,
		Attempts:    operation.Attempts,
		Failure:     decision.Failure,
		Decision:    decision,
		Err:         providerErr,
	})
	if decision.ShouldRetry() {
		w.emit(ctx, Event{Kind: EventRetryScheduled, State: WorkerBackoff, OperationID: operation.ID, Attempts: operation.Attempts, Failure: decision.Failure, Decision: decision, Err: providerErr})
	}
	return nil
}

func (w *Worker) isolationFailure(ctx context.Context, operation Operation, err error) error {
	releaseErr := w.releaseClaim(ctx, operation.ProviderID, operation.ID, operation.LeaseOwner)
	if releaseErr != nil {
		err = errors.Join(err, releaseErr)
	}
	decision := RetryDecision{
		Kind:     DecisionTerminal,
		Failure:  FailureTerminal,
		Attempts: operation.Attempts,
		Err:      err,
	}
	w.setFailure(WorkerFailed, err, decision)
	w.emit(ctx, Event{Kind: EventSyncFailed, State: WorkerFailed, Failure: FailureTerminal, Decision: decision, Err: err})
	return err
}

func (w *Worker) infrastructureFailure(ctx context.Context, kind EventKind, action string, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	w.setFailure(WorkerFailed, err, w.providerFailureDecision(err))
	w.emit(ctx, Event{Kind: kind, State: WorkerFailed, Failure: ClassifyFailure(err), Decision: w.Status().Decision, Err: fmt.Errorf("%s: %w", action, err)})
	return fmt.Errorf("%s: %w", action, err)
}

func (w *Worker) cancelClaim(ctx context.Context, action string, err error, operation Operation) error {
	if operation.ID != "" {
		if releaseErr := w.releaseClaim(ctx, operation.ProviderID, operation.ID, operation.LeaseOwner); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}
	w.setState(WorkerStopped)
	w.emit(context.Background(), Event{Kind: EventWorkerStopped, State: WorkerStopped, OperationID: operation.ID, Err: fmt.Errorf("%s: %w", action, err)})
	return err
}

func (w *Worker) releaseClaim(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string) error {
	persistCtx, cancel := w.persistenceContext(ctx)
	err := w.queue.Release(persistCtx, providerID, operationID, leaseOwner)
	cancel()
	return err
}

func (w *Worker) persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), w.options.CleanupTimeout)
}

func (w *Worker) nextDelay(err error) time.Duration {
	if err == nil {
		return w.options.Interval
	}
	status := w.Status()
	if status.Decision.Delay > 0 {
		return status.Decision.Delay
	}
	if status.Decision.Kind == DecisionTerminal {
		return w.options.Interval
	}
	return w.options.RetryPolicy.Decide(err, 1).Delay
}

func (w *Worker) beginRun() bool {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if w.running {
		return false
	}
	w.running = true
	return true
}

func (w *Worker) endRun() {
	w.lifecycleMu.Lock()
	w.running = false
	w.lifecycleMu.Unlock()
}

func acquire(ctx context.Context, channel chan struct{}) error {
	select {
	case channel <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(channel chan struct{}) {
	<-channel
}

func (w *Worker) incrementAttempts() {
	w.statusMu.Lock()
	w.status.Attempts++
	w.statusMu.Unlock()
}

func (w *Worker) incrementOperations() {
	w.statusMu.Lock()
	w.status.Operations++
	w.statusMu.Unlock()
}

func (w *Worker) setSynced(at time.Time) {
	w.statusMu.Lock()
	w.status.State = WorkerSynced
	w.status.LastSyncAt = at
	w.status.LastError = nil
	w.status.Failure = ""
	w.status.Decision = RetryDecision{Kind: DecisionNone}
	w.status.NextRetryAt = time.Time{}
	w.status.FailureAttempts = 0
	w.statusMu.Unlock()
}

func (w *Worker) providerFailureDecision(err error) RetryDecision {
	w.statusMu.Lock()
	w.status.FailureAttempts++
	attempts := w.status.FailureAttempts
	w.statusMu.Unlock()
	return w.options.RetryPolicy.Decide(err, attempts)
}

func (w *Worker) setFailure(state WorkerState, err error, decision RetryDecision) {
	w.statusMu.Lock()
	w.status.State = state
	w.status.LastError = err
	w.status.Failure = decision.Failure
	w.status.Decision = decision
	if decision.ShouldRetry() {
		w.status.NextRetryAt = w.options.Clock.Now().Add(decision.Delay)
	} else {
		w.status.NextRetryAt = time.Time{}
	}
	w.statusMu.Unlock()
}

func (w *Worker) setStatus(state WorkerState, err error, failure FailureKind, decision RetryDecision) {
	w.statusMu.Lock()
	w.status.State = state
	w.status.LastError = err
	w.status.Failure = failure
	w.status.Decision = decision
	if decision.ShouldRetry() {
		w.status.NextRetryAt = w.options.Clock.Now().Add(decision.Delay)
	} else {
		w.status.NextRetryAt = time.Time{}
	}
	w.statusMu.Unlock()
}

func (w *Worker) setState(state WorkerState) {
	w.statusMu.Lock()
	w.status.State = state
	w.statusMu.Unlock()
}

func (w *Worker) emit(ctx context.Context, event Event) {
	if w.options.EventSink == nil {
		return
	}
	event.ProviderID = w.ProviderID()
	event.At = w.options.Clock.Now()
	w.options.EventSink(event)
}
