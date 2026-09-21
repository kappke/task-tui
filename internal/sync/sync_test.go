package sync

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	providerpkg "github.com/kappke/task-tui/internal/provider"
	"github.com/kappke/task-tui/internal/repository"
)

type testQueue struct {
	mu  sync.Mutex
	ops []*testOperation

	claimedProviders []ProviderID
	recovered        []ProviderID
	attempts         []OperationID
	completed        []OperationID
	failures         map[OperationID]Failure
	released         []OperationID
}

type testOperation struct {
	operation Operation
	leaseAt   time.Time
}

func (q *testQueue) RecoverStale(_ context.Context, providerID ProviderID, before time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recovered = append(q.recovered, providerID)
	for _, item := range q.ops {
		if item.operation.ProviderID == providerID && item.operation.Status == OperationSyncing && item.leaseAt.Before(before) {
			item.operation.Status = OperationPending
		}
	}
	return nil
}

func (q *testQueue) Claim(_ context.Context, providerID ProviderID, now time.Time) (Operation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.claimedProviders = append(q.claimedProviders, providerID)
	for _, item := range q.ops {
		operation := &item.operation
		if operation.ProviderID != providerID {
			continue
		}
		if operation.Status != OperationPending && operation.Status != OperationFailed {
			continue
		}
		if !operation.NextAttemptAt.IsZero() && operation.NextAttemptAt.After(now) {
			continue
		}
		operation.Status = OperationSyncing
		item.leaseAt = now
		return *operation, nil
	}
	return Operation{}, ErrQueueEmpty
}

func (q *testQueue) MarkAttempt(_ context.Context, operationID OperationID, at time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.ops {
		if item.operation.ID != operationID {
			continue
		}
		item.operation.Attempts++
		item.operation.LastAttemptAt = &at
		q.attempts = append(q.attempts, operationID)
		return nil
	}
	return errors.New("operation not found")
}

func (q *testQueue) Complete(_ context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, at time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.ops {
		if item.operation.ID != operationID {
			continue
		}
		if item.operation.ProviderID != providerID || item.operation.LeaseOwner != leaseOwner {
			return errors.New("invalid lease")
		}
		item.operation.Status = OperationCompleted
		item.operation.CompletedAt = &at
		q.completed = append(q.completed, operationID)
		return nil
	}
	return errors.New("operation not found")
}

func (q *testQueue) Fail(_ context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, failure Failure) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.ops {
		if item.operation.ID != operationID {
			continue
		}
		if item.operation.ProviderID != providerID || item.operation.LeaseOwner != leaseOwner {
			return errors.New("invalid lease")
		}
		item.operation.Status = OperationFailed
		item.operation.Error = failure.Error()
		if failure.RetryAt != nil {
			item.operation.NextAttemptAt = *failure.RetryAt
		}
		q.failures = ensureFailureMap(q.failures)
		q.failures[operationID] = failure
		return nil
	}
	return errors.New("operation not found")
}

func (q *testQueue) Release(_ context.Context, providerID ProviderID, operationID OperationID, leaseOwner string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.ops {
		if item.operation.ID != operationID {
			continue
		}
		if item.operation.ProviderID != providerID || item.operation.LeaseOwner != leaseOwner {
			return errors.New("invalid lease")
		}
		item.operation.Status = OperationPending
		q.released = append(q.released, operationID)
		return nil
	}
	return errors.New("operation not found")
}

func ensureFailureMap(failures map[OperationID]Failure) map[OperationID]Failure {
	if failures == nil {
		return make(map[OperationID]Failure)
	}
	return failures
}

func (q *testQueue) operation(id OperationID) (Operation, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.ops {
		if item.operation.ID == id {
			return item.operation, true
		}
	}
	return Operation{}, false
}

type testProvider struct {
	id         ProviderID
	caps       Capabilities
	mu         sync.Mutex
	pushed     []Operation
	pullCount  int
	pushErrors []error
	push       func(context.Context, Operation) error
	pull       func(context.Context) error
}

func (p *testProvider) ID() ProviderID { return p.id }

func (p *testProvider) Capabilities() Capabilities { return p.caps }

func (p *testProvider) Push(ctx context.Context, operation Operation) error {
	p.mu.Lock()
	p.pushed = append(p.pushed, operation)
	var err error
	if len(p.pushErrors) > 0 {
		err = p.pushErrors[0]
		p.pushErrors = p.pushErrors[1:]
	}
	push := p.push
	p.mu.Unlock()
	if push != nil {
		return push(ctx, operation)
	}
	return err
}

func (p *testProvider) Pull(ctx context.Context) error {
	p.mu.Lock()
	p.pullCount++
	pull := p.pull
	p.mu.Unlock()
	if pull != nil {
		return pull(ctx)
	}
	return nil
}

func (p *testProvider) pushedIDs() []OperationID {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]OperationID, 0, len(p.pushed))
	for _, operation := range p.pushed {
		ids = append(ids, operation.ID)
	}
	return ids
}

type testClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  []time.Duration
	waitFn func(context.Context, time.Duration) error
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Wait(ctx context.Context, delay time.Duration) error {
	c.mu.Lock()
	c.waits = append(c.waits, delay)
	waitFn := c.waitFn
	c.mu.Unlock()
	if waitFn != nil {
		return waitFn(ctx, delay)
	}
	return nil
}

func newTestWorker(t *testing.T, queue Queue, provider *testProvider, clock *testClock) *Worker {
	t.Helper()
	worker, err := NewWorker(queue, provider, WorkerOptions{
		Clock:       clock,
		RetryPolicy: DefaultRetryPolicy(),
		Interval:    time.Hour,
		LeaseTTL:    time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func queued(id OperationID, providerID ProviderID, createdAt time.Time) *testOperation {
	return &testOperation{operation: Operation{
		ID:         id,
		ProviderID: providerID,
		EntityType: EntityTask,
		EntityID:   string(id),
		Operation:  OperationUpdate,
		Status:     OperationPending,
		CreatedAt:  createdAt,
	}}
}

func TestWorkerRoutesOnlyItsProviderAndKeepsQueueIsolated(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	queue := &testQueue{ops: []*testOperation{
		queued("a-1", "work", now),
		queued("p-1", "personal", now.Add(time.Second)),
	}}
	provider := &testProvider{id: "work", caps: Capabilities{Remote: true}}
	worker := newTestWorker(t, queue, provider, &testClock{now: now})

	if err := worker.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := provider.pushedIDs(), []OperationID{"a-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pushed IDs = %v, want %v", got, want)
	}
	if got, want := queue.claimedProviders, []ProviderID{"work", "work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claimed providers = %v, want %v", got, want)
	}
	if operation, ok := queue.operation("p-1"); !ok || operation.Status != OperationPending {
		t.Fatalf("foreign operation was changed: %+v", operation)
	}
}

func TestWorkerPushesInQueueOrderBeforePull(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	queue := &testQueue{ops: []*testOperation{
		queued("create", "work", now),
		queued("update", "work", now.Add(time.Second)),
	}}
	var sequence []string
	provider := &testProvider{
		id:   "work",
		caps: Capabilities{Remote: true, Pull: true},
		push: func(_ context.Context, operation Operation) error {
			sequence = append(sequence, string(operation.ID))
			return nil
		},
		pull: func(context.Context) error {
			sequence = append(sequence, "pull")
			return nil
		},
	}
	worker := newTestWorker(t, queue, provider, &testClock{now: now})

	if err := worker.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"create", "update", "pull"}; !reflect.DeepEqual(sequence, want) {
		t.Fatalf("provider sequence = %v, want %v", sequence, want)
	}
}

func TestRetryPolicyUsesDocumentedScheduleAndRateLimitFloor(t *testing.T) {
	policy := DefaultRetryPolicy()
	want := []time.Duration{
		time.Second,
		5 * time.Second,
		15 * time.Second,
		30 * time.Second,
		time.Minute,
		5 * time.Minute,
		5 * time.Minute,
	}
	for attempt, expected := range want {
		if got := policy.Delay(attempt + 1); got != expected {
			t.Errorf("attempt %d delay = %s, want %s", attempt+1, got, expected)
		}
	}

	decision := policy.Decide(RateLimited(errors.New("slow down"), 10*time.Minute), 1)
	if decision.Kind != DecisionRateLimited || decision.Delay != 10*time.Minute || !decision.ShouldRetry() {
		t.Fatalf("rate-limit decision = %+v", decision)
	}
	terminal := policy.Decide(Terminal(errors.New("invalid")), 1)
	if terminal.Kind != DecisionTerminal || terminal.ShouldRetry() {
		t.Fatalf("terminal decision = %+v", terminal)
	}
	foreignRateLimit := policy.Decide(foreignRateLimitError{RetryAfter: 2 * time.Minute}, 1)
	if foreignRateLimit.Kind != DecisionRateLimited || foreignRateLimit.Delay != 2*time.Minute {
		t.Fatalf("foreign rate-limit decision = %+v", foreignRateLimit)
	}
}

type foreignRateLimitError struct {
	RetryAfter time.Duration
}

func (e foreignRateLimitError) Error() string { return "foreign rate limit" }

func TestWorkerPersistsAttemptBeforeRetryAndDistinguishesTerminalFailure(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	queue := &testQueue{ops: []*testOperation{queued("retry", "work", now)}}
	retryErr := errors.New("offline")
	provider := &testProvider{
		id:         "work",
		caps:       Capabilities{Remote: true},
		pushErrors: []error{Retryable(retryErr)},
	}
	worker := newTestWorker(t, queue, provider, &testClock{now: now})

	err := worker.SyncOnce(context.Background())
	if !errors.Is(err, retryErr) {
		t.Fatalf("retryable provider error = %v", err)
	}
	operation, ok := queue.operation("retry")
	if !ok {
		t.Fatal("retry operation disappeared")
	}
	if operation.Attempts != 1 || len(queue.attempts) != 1 || len(provider.pushed) != 1 {
		t.Fatalf("attempt was not persisted before call: operation=%+v attempts=%v pushes=%v", operation, queue.attempts, provider.pushed)
	}
	failure := queue.failures["retry"]
	if failure.Kind != FailureRetryable || failure.RetryAt == nil || !failure.RetryAt.Equal(now.Add(time.Second)) {
		t.Fatalf("retry failure = %+v", failure)
	}

	terminalQueue := &testQueue{ops: []*testOperation{queued("terminal", "work", now)}}
	terminalProvider := &testProvider{
		id:         "work",
		caps:       Capabilities{Remote: true},
		pushErrors: []error{Terminal(errors.New("invalid payload"))},
	}
	terminalWorker := newTestWorker(t, terminalQueue, terminalProvider, &testClock{now: now})
	if err := terminalWorker.SyncOnce(context.Background()); err == nil {
		t.Fatal("terminal provider error was not returned")
	}
	terminalFailure := terminalQueue.failures["terminal"]
	if terminalFailure.Kind != FailureTerminal || terminalFailure.RetryAt != nil {
		t.Fatalf("terminal failure = %+v", terminalFailure)
	}
}

func TestWorkerCancellationReleasesClaimAndRecoversStaleLease(t *testing.T) {
	now := time.Unix(400, 0).UTC()
	queue := &testQueue{ops: []*testOperation{queued("cancel", "work", now)}}
	started := make(chan struct{})
	provider := &testProvider{
		id:   "work",
		caps: Capabilities{Remote: true},
		push: func(ctx context.Context, _ Operation) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	worker := newTestWorker(t, queue, provider, &testClock{now: now})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.SyncOnce(ctx) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	operation, ok := queue.operation("cancel")
	if !ok || operation.Status != OperationPending || operation.Attempts != 1 {
		t.Fatalf("canceled operation = %+v", operation)
	}
	if len(queue.released) != 1 || len(queue.completed) != 0 || len(queue.failures) != 0 {
		t.Fatalf("cancellation persistence: released=%v completed=%v failures=%v", queue.released, queue.completed, queue.failures)
	}

	stale := queued("stale", "work", now)
	stale.operation.Status = OperationSyncing
	stale.leaseAt = now.Add(-2 * time.Minute)
	staleQueue := &testQueue{ops: []*testOperation{stale}}
	staleProvider := &testProvider{id: "work", caps: Capabilities{Remote: true}}
	staleWorker := newTestWorker(t, staleQueue, staleProvider, &testClock{now: now})
	if err := staleWorker.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(staleQueue.recovered) != 1 || len(staleQueue.completed) != 1 {
		t.Fatalf("stale lease was not recovered: recovered=%v completed=%v", staleQueue.recovered, staleQueue.completed)
	}
}

func TestWorkerRunStopsWithContextCancellation(t *testing.T) {
	now := time.Unix(450, 0).UTC()
	queue := &testQueue{}
	clock := &testClock{
		now: now,
		waitFn: func(ctx context.Context, _ time.Duration) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	worker := newTestWorker(t, queue, &testProvider{id: "work", caps: Capabilities{Remote: true}}, clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	// The empty queue cycle completes before Run enters its injectable wait.
	deadline := time.After(time.Second)
	for {
		if worker.Running() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("worker did not reach its wait")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if !worker.Running() {
		t.Fatal("worker did not report its active Run loop")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run cancellation error = %v", err)
	}
	if worker.Running() {
		t.Fatal("worker reported a Run loop after cancellation")
	}
}

func TestCanceledContextsAreRejectedBeforeQueueWork(t *testing.T) {
	now := time.Unix(475, 0).UTC()
	queue := &testQueue{ops: []*testOperation{queued("cancelled", "work", now)}}
	worker := newTestWorker(t, queue, &testProvider{id: "work", caps: Capabilities{Remote: true}}, &testClock{now: now})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.SyncOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncOnce cancellation error = %v", err)
	}
	if len(queue.recovered) != 0 || len(queue.claimedProviders) != 0 {
		t.Fatalf("canceled SyncOnce touched queue: recovered=%v claimed=%v", queue.recovered, queue.claimedProviders)
	}
	if worker.Status().State != WorkerStopped {
		t.Fatalf("worker state = %q, want %q", worker.Status().State, WorkerStopped)
	}

	engine, err := NewEngine(queue, []Provider{
		&testProvider{id: "work", caps: Capabilities{Remote: true}},
	}, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start cancellation error = %v", err)
	}
	if engine.Running() {
		t.Fatal("canceled engine started")
	}
}

func TestEngineKeepsProviderFailuresIndependentAndSkipsLocal(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	queue := &testQueue{ops: []*testOperation{
		queued("bad", "bad", now),
		queued("good", "good", now.Add(time.Second)),
	}}
	bad := &testProvider{
		id:         "bad",
		caps:       Capabilities{Remote: true},
		pushErrors: []error{Terminal(errors.New("bad provider"))},
	}
	good := &testProvider{id: "good", caps: Capabilities{Remote: true}}
	local := &testProvider{id: "local", caps: Capabilities{Local: true}}
	engine, err := NewEngine(queue, []Provider{bad, good, local}, EngineOptions{
		Worker: WorkerOptions{Clock: &testClock{now: now}, Interval: time.Hour, LeaseTTL: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.WorkerCount() != 2 {
		t.Fatalf("worker count = %d, want 2", engine.WorkerCount())
	}
	if _, ok := engine.Worker("local"); ok {
		t.Fatal("local provider received a network worker")
	}
	if err := engine.SyncOnce(context.Background()); err == nil {
		t.Fatal("engine hid provider failure")
	}
	if goodIDs := good.pushedIDs(); !reflect.DeepEqual(goodIDs, []OperationID{"good"}) {
		t.Fatalf("healthy provider calls = %v", goodIDs)
	}
}

func TestEngineStartsAndStopsEachRemoteWorkerIndependently(t *testing.T) {
	now := time.Unix(550, 0).UTC()
	queue := &testQueue{}
	started := make(chan ProviderID, 2)
	clock := &testClock{
		now: now,
		waitFn: func(ctx context.Context, _ time.Duration) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	engine, err := NewEngine(queue, []Provider{
		&testProvider{id: "a", caps: Capabilities{Remote: true}},
		&testProvider{id: "b", caps: Capabilities{Remote: true}},
	}, EngineOptions{
		Worker: WorkerOptions{Clock: clock, Interval: time.Hour, LeaseTTL: time.Minute},
		EventSink: func(event Event) {
			if event.Kind == EventWorkerStarted {
				started <- event.ProviderID
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	seen := make(map[ProviderID]bool)
	for range 2 {
		select {
		case providerID := <-started:
			seen[providerID] = true
		case <-time.After(time.Second):
			t.Fatal("not all provider workers started")
		}
	}
	if len(seen) != 2 || !engine.Running() {
		t.Fatalf("started workers = %v, running = %v", seen, engine.Running())
	}
	cancel()
	if err := engine.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.Running() {
		t.Fatal("engine remained running after Stop")
	}
}

type testConflictStore struct {
	conflicts []FieldConflict
	marked    []string
}

func (s *testConflictStore) SaveConflict(_ context.Context, conflict FieldConflict) error {
	s.conflicts = append(s.conflicts, conflict)
	return nil
}

func (s *testConflictStore) MarkConflict(_ context.Context, providerID ProviderID, entityType EntityType, entityID string) error {
	s.marked = append(s.marked, string(providerID)+":"+string(entityType)+":"+entityID)
	return nil
}

func TestThreeWayMergeRetainsLocalAndPersistsProviderScopedConflict(t *testing.T) {
	now := time.Unix(600, 0).UTC()
	store := &testConflictStore{}
	result, err := MergeAndPersist(context.Background(), MergeInput{
		ProviderID: "work",
		EntityType: EntityTask,
		EntityID:   "task-1",
		Base:       map[string]any{"title": "base", "status": "open", "obsolete": "yes"},
		Local:      map[string]any{"title": "local", "status": "open"},
		Remote:     map[string]any{"title": "remote", "status": "done"},
		Now:        now,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != SyncStateConflict || result.Fields["title"] != "local" || result.Fields["status"] != "done" {
		t.Fatalf("merge result = %+v", result)
	}
	if _, exists := result.Fields["obsolete"]; exists {
		t.Fatal("unchanged local deletion was not retained")
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", result.Conflicts)
	}
	conflict := result.Conflicts[0]
	if conflict.ProviderID != "work" || conflict.BaseValue != "base" || conflict.LocalValue != "local" || conflict.RemoteValue != "remote" || !conflict.CreatedAt.Equal(now) {
		t.Fatalf("field conflict = %+v", conflict)
	}
	if len(store.conflicts) != 1 || len(store.marked) != 1 || store.marked[0] != "work:task:task-1" {
		t.Fatalf("conflict persistence = conflicts=%v marked=%v", store.conflicts, store.marked)
	}

	if _, err := MergeFields(MergeInput{
		ProviderID:       "work",
		LocalProviderID:  "work",
		RemoteProviderID: "personal",
		Base:             map[string]any{"title": "base"},
		Local:            map[string]any{"title": "local"},
		Remote:           map[string]any{"title": "remote"},
	}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("cross-provider merge error = %v", err)
	}
}

func TestThreeWayMergeWithMissingBaseDoesNotAssumeLocalBase(t *testing.T) {
	result, err := MergeFields(MergeInput{
		ProviderID: "work",
		EntityType: EntityTask,
		EntityID:   "task-1",
		Base:       nil,
		Local:      map[string]any{"title": "local"},
		Remote:     map[string]any{"title": "remote"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != SyncStateConflict || len(result.Conflicts) != 1 {
		t.Fatalf("missing-base merge = %+v, want one conflict", result)
	}
}

type foundationTestQueue struct {
	operation domain.SyncOperation
	claimed   bool
	complete  bool
}

func (q *foundationTestQueue) Enqueue(context.Context, domain.SyncOperation) error { return nil }

func (q *foundationTestQueue) Claim(_ context.Context, providerID domain.ProviderID) (domain.SyncOperation, error) {
	if q.claimed || q.operation.ProviderID != providerID {
		return domain.SyncOperation{}, repository.ErrQueueEmpty
	}
	q.claimed = true
	q.operation.Status = domain.SyncStatusSyncing
	q.operation.Attempts++
	q.operation.LeaseOwner = "foundation-worker"
	return q.operation, nil
}

func (q *foundationTestQueue) Complete(_ context.Context, operationID domain.OperationID) error {
	if operationID != q.operation.ID {
		return errors.New("unexpected operation")
	}
	q.complete = true
	q.operation.Status = domain.SyncStatusCompleted
	return nil
}

func (q *foundationTestQueue) CompleteForProviderWithLease(_ context.Context, providerID domain.ProviderID, operationID domain.OperationID, leaseOwner string) error {
	if providerID != q.operation.ProviderID || leaseOwner != q.operation.LeaseOwner {
		return errors.New("unexpected lease")
	}
	return q.Complete(context.Background(), operationID)
}

func (q *foundationTestQueue) Retry(context.Context, domain.OperationID, error) error { return nil }

func (q *foundationTestQueue) Fail(context.Context, domain.OperationID, error) error { return nil }

func (q *foundationTestQueue) FailForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string, error) error {
	return nil
}

func (q *foundationTestQueue) RetryForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string, error) error {
	return nil
}

func (q *foundationTestQueue) RetryAtForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string, time.Time, error) error {
	return nil
}

func (q *foundationTestQueue) ReleaseForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string) error {
	return nil
}

func (q *foundationTestQueue) RequeueStale(context.Context, domain.ProviderID, time.Time) (int, error) {
	return 0, nil
}

func (q *foundationTestQueue) PendingCount(context.Context, domain.ProviderID) (int, error) {
	if q.complete {
		return 0, nil
	}
	return 1, nil
}

type foundationTestProvider struct {
	id      domain.ProviderID
	created int
	remote  string
	updated []domain.Task
}

func (p *foundationTestProvider) ID() domain.ProviderID { return p.id }

func (p *foundationTestProvider) Type() domain.ProviderType { return domain.ProviderTypeClickUp }

func (p *foundationTestProvider) Capabilities() domain.Capabilities {
	return domain.Capabilities{CreateTask: true}
}

func (p *foundationTestProvider) FetchSpaces(context.Context) ([]domain.Space, error) {
	return nil, nil
}

func (p *foundationTestProvider) FetchLists(context.Context, domain.SpaceID) ([]domain.List, error) {
	return nil, nil
}

func (p *foundationTestProvider) FetchTasks(context.Context, domain.ListID) ([]domain.Task, error) {
	return nil, nil
}

func (p *foundationTestProvider) FetchTask(context.Context, domain.TaskID) (domain.Task, error) {
	return domain.Task{}, nil
}

func (p *foundationTestProvider) CreateTask(_ context.Context, task domain.Task) (domain.Task, error) {
	p.created++
	if p.remote != "" {
		task.RemoteID = &p.remote
	}
	return task, nil
}

func (p *foundationTestProvider) UpdateTask(_ context.Context, task domain.Task) (domain.Task, error) {
	p.updated = append(p.updated, task)
	return task, nil
}

func (p *foundationTestProvider) DeleteTask(context.Context, domain.Task) error { return nil }

var _ providerpkg.Provider = (*foundationTestProvider)(nil)

func TestNewWorkerAcceptsFoundationQueueAndProviderContracts(t *testing.T) {
	now := time.Unix(700, 0).UTC()
	queue := &foundationTestQueue{operation: domain.SyncOperation{
		ID:         "operation-1",
		ProviderID: "work",
		EntityType: domain.EntityTypeTask,
		EntityID:   "task-1",
		Operation:  domain.OperationTypeCreate,
		Payload:    []byte(`{"id":"task-1","provider_id":"work","list_id":"list-1","title":"task","status":"open","priority":"normal","sync_state":"pending"}`),
		Status:     domain.SyncStatusPending,
	}}
	provider := &foundationTestProvider{id: "work"}
	worker, err := NewWorker(queue, provider, WorkerOptions{Clock: &testClock{now: now}})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.created != 1 || !queue.complete || queue.operation.Attempts != 1 {
		t.Fatalf("foundation adapter state: created=%d complete=%v operation=%+v", provider.created, queue.complete, queue.operation)
	}
}

func TestFoundationProviderPropagatesCreatedRemoteIdentity(t *testing.T) {
	provider := &foundationTestProvider{id: "work", remote: "remote-task-1"}
	adapted := AdaptProvider(provider).(*FoundationProvider)
	payload, err := json.Marshal(domain.Task{ID: "task-1", ProviderID: "work", ListID: "list-1", Title: "task", Status: "open", Priority: domain.PriorityNormal})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapted.Push(context.Background(), Operation{ID: "create", ProviderID: "work", EntityType: EntityTask, EntityID: "task-1", Operation: OperationCreate, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := adapted.Push(context.Background(), Operation{ID: "update", ProviderID: "work", EntityType: EntityTask, EntityID: "task-1", Operation: OperationUpdate, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(provider.updated) != 1 || provider.updated[0].RemoteID == nil || *provider.updated[0].RemoteID != "remote-task-1" {
		t.Fatalf("updated task remote identity = %+v, want remote-task-1", provider.updated)
	}
}
