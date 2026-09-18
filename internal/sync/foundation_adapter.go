package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	stdsync "sync"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	providerpkg "github.com/kappke/task-tui/internal/provider"
	"github.com/kappke/task-tui/internal/repository"
)

// RepositoryQueue adapts the foundation repository.SyncQueue to Queue. The
// foundation queue increments attempts as part of its atomic Claim operation;
// MarkAttempt is therefore a deliberate no-op and the adapter exposes the
// pre-claim count to Worker so the provider call still observes the persisted
// post-claim attempt number.
type RepositoryQueue struct {
	queue      repository.SyncQueue
	providerID domain.ProviderID
}

// AdaptQueue adapts one provider-scoped foundation queue. A separate adapter
// is required for each worker so lifecycle calls remain bound to that provider
// instance, including when the shared queue serves multiple providers.
func AdaptQueue(queue repository.SyncQueue, providerID ProviderID) Queue {
	if isNilContract(queue) {
		return nil
	}
	return &RepositoryQueue{queue: queue, providerID: domain.ProviderID(providerID)}
}

func adaptQueueInput(value any, providerID ProviderID) (Queue, error) {
	if isNilContract(value) {
		return nil, errors.New("sync queue is nil")
	}
	switch queue := value.(type) {
	case Queue:
		return queue, nil
	case repository.SyncQueue:
		adapted := AdaptQueue(queue, providerID)
		if adapted == nil {
			return nil, errors.New("sync foundation queue is nil")
		}
		return adapted, nil
	default:
		return nil, errors.New("sync queue has an unsupported contract")
	}
}

func (q *RepositoryQueue) RecoverStale(ctx context.Context, providerID ProviderID, before time.Time) error {
	if err := q.checkProvider(providerID); err != nil {
		return err
	}
	_, err := q.queue.RequeueStale(ctx, q.providerID, before)
	return mapQueueError(err)
}

func (q *RepositoryQueue) Claim(ctx context.Context, providerID ProviderID, _ time.Time) (Operation, error) {
	if err := q.checkProvider(providerID); err != nil {
		return Operation{}, err
	}
	operation, err := q.queue.Claim(ctx, q.providerID)
	if err != nil {
		return Operation{}, mapQueueError(err)
	}
	return fromDomainOperation(operation), nil
}

func (q *RepositoryQueue) MarkAttempt(context.Context, OperationID, time.Time) error {
	// repository.SyncQueue.Claim performs this increment transactionally before
	// returning. Calling another increment here would count every attempt twice.
	return nil
}

func (q *RepositoryQueue) Complete(ctx context.Context, operationID OperationID, _ time.Time) error {
	return mapQueueError(q.queue.Complete(ctx, domain.OperationID(operationID)))
}

func (q *RepositoryQueue) Fail(ctx context.Context, operationID OperationID, failure Failure) error {
	cause := failure.Err
	if cause == nil {
		cause = errors.New(failure.Error())
	}
	if failure.RetryAt != nil {
		if scheduled, ok := q.queue.(repository.ScheduledSyncQueue); ok {
			return mapQueueError(scheduled.RetryAt(ctx, domain.OperationID(operationID), *failure.RetryAt, cause))
		}
		return mapQueueError(q.queue.Retry(ctx, domain.OperationID(operationID), cause))
	}
	return mapQueueError(q.queue.Fail(ctx, domain.OperationID(operationID), cause))
}

func (q *RepositoryQueue) Release(ctx context.Context, operationID OperationID) error {
	if releaser, ok := q.queue.(interface {
		Release(context.Context, domain.OperationID) error
	}); ok {
		return mapQueueError(releaser.Release(ctx, domain.OperationID(operationID)))
	}
	// The foundation queue contract guarantees RequeueStale. When it does not
	// expose an eager release transition, cancellation leaves the durable row
	// syncing and the next worker cycle recovers it by lease age.
	return nil
}

func (q *RepositoryQueue) checkProvider(providerID ProviderID) error {
	if providerID != ProviderID(q.providerID) {
		return fmt.Errorf("%w: queue belongs to %s, requested %s", ErrProviderMismatch, q.providerID, providerID)
	}
	return nil
}

func fromDomainOperation(operation domain.SyncOperation) Operation {
	attempts := operation.Attempts - 1
	if attempts < 0 {
		attempts = 0
	}
	converted := Operation{
		ID:            OperationID(operation.ID),
		ProviderID:    ProviderID(operation.ProviderID),
		EntityType:    EntityType(operation.EntityType),
		EntityID:      operation.EntityID,
		Operation:     OperationType(operation.Operation),
		Payload:       append([]byte(nil), operation.Payload...),
		Attempts:      attempts,
		Status:        OperationStatus(operation.Status),
		CreatedAt:     operation.CreatedAt,
		LastAttemptAt: operation.LastAttemptAt,
		NextAttemptAt: time.Time{},
	}
	if operation.NextAttemptAt != nil {
		converted.NextAttemptAt = *operation.NextAttemptAt
	}
	return converted
}

func mapQueueError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, repository.ErrQueueEmpty) || errors.Is(err, domain.ErrQueueEmpty) {
		return fmt.Errorf("%w: %w", ErrQueueEmpty, err)
	}
	return err
}

// FoundationProvider adapts internal/provider.Provider to the sync provider
// boundary. It dispatches one queue operation to the provider's normalized
// entity method and leaves retry policy entirely to Worker.
type FoundationProvider struct {
	provider providerpkg.Provider
	id       ProviderID
	caps     Capabilities
}

var _ Provider = (*FoundationProvider)(nil)
var _ Pusher = (*FoundationProvider)(nil)
var _ Puller = (*FoundationProvider)(nil)
var _ Queue = (*RepositoryQueue)(nil)

// RepositoryConflictStore adapts repository.ConflictWriter to the sync
// conflict persistence boundary. Each field is written as a provider-scoped
// Conflict with one FieldConflict entry; this keeps the adapter compatible
// with repositories that use one row per conflict identity.
type RepositoryConflictStore struct {
	Writer repository.ConflictWriter
}

// AdaptConflictWriter adapts a foundation conflict writer for MergeAndPersist.
func AdaptConflictWriter(writer repository.ConflictWriter) ConflictStore {
	if writer == nil {
		return nil
	}
	return RepositoryConflictStore{Writer: writer}
}

func (s RepositoryConflictStore) SaveConflict(ctx context.Context, conflict FieldConflict) error {
	if s.Writer == nil {
		return errors.New("sync conflict writer is nil")
	}
	base, err := json.Marshal(conflictValue(conflict.BaseValue, conflict.Base))
	if err != nil {
		return fmt.Errorf("marshal conflict %s base value: %w", conflict.ID, err)
	}
	local, err := json.Marshal(conflictValue(conflict.LocalValue, conflict.Local))
	if err != nil {
		return fmt.Errorf("marshal conflict %s local value: %w", conflict.ID, err)
	}
	remote, err := json.Marshal(conflictValue(conflict.RemoteValue, conflict.Remote))
	if err != nil {
		return fmt.Errorf("marshal conflict %s remote value: %w", conflict.ID, err)
	}
	now := conflict.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return s.Writer.Create(ctx, domain.Conflict{
		ID:         domain.ConflictID(conflict.ID),
		ProviderID: domain.ProviderID(conflict.ProviderID),
		EntityType: domain.EntityType(conflict.EntityType),
		EntityID:   conflict.EntityID,
		Fields: []domain.FieldConflict{{
			Field:       conflict.Field,
			BaseValue:   base,
			LocalValue:  local,
			RemoteValue: remote,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func conflictValue(explicit, alias any) any {
	if explicit != nil {
		return explicit
	}
	return alias
}

// AdaptProvider wraps a foundation provider instance. Local provider types are
// explicitly marked non-remote, so they cannot create a Worker.
func AdaptProvider(value providerpkg.Provider) Provider {
	if isNilContract(value) {
		return nil
	}
	id := ProviderID(value.ID())
	local := value.Type() == domain.ProviderTypeLocal
	_, canPull := value.(providerpkg.Puller)
	if !canPull {
		_, canPull = value.(providerpkg.Synchronizer)
	}
	return &FoundationProvider{
		provider: value,
		id:       id,
		caps: Capabilities{
			Remote:  !local,
			Network: !local,
			Pull:    canPull,
			Local:   local,
		},
	}
}

func adaptProviderInput(value any) (Provider, error) {
	if isNilContract(value) {
		return nil, errors.New("sync provider is nil")
	}
	switch provider := value.(type) {
	case Provider:
		if provider == nil {
			return nil, errors.New("sync provider is nil")
		}
		return provider, nil
	case providerpkg.Provider:
		adapted := AdaptProvider(provider)
		if adapted == nil {
			return nil, errors.New("sync foundation provider is nil")
		}
		return adapted, nil
	default:
		return nil, errors.New("sync provider has an unsupported contract")
	}
}

func adaptProviderInputs(value any) ([]Provider, error) {
	switch providers := value.(type) {
	case nil:
		return nil, nil
	case []Provider:
		adapted := append([]Provider(nil), providers...)
		for _, provider := range adapted {
			if isNilContract(provider) {
				return nil, errors.New("sync provider is nil")
			}
		}
		return adapted, nil
	case []providerpkg.Provider:
		adapted := make([]Provider, 0, len(providers))
		for _, provider := range providers {
			value, err := adaptProviderInput(provider)
			if err != nil {
				return nil, err
			}
			adapted = append(adapted, value)
		}
		return adapted, nil
	case Provider:
		return []Provider{providers}, nil
	case providerpkg.Provider:
		provider, err := adaptProviderInput(providers)
		if err != nil {
			return nil, err
		}
		return []Provider{provider}, nil
	case []any:
		adapted := make([]Provider, 0, len(providers))
		for _, value := range providers {
			provider, err := adaptProviderInput(value)
			if err != nil {
				return nil, err
			}
			adapted = append(adapted, provider)
		}
		return adapted, nil
	default:
		return nil, errors.New("sync providers have an unsupported contract")
	}
}

func adaptEngineQueueInput(value any, providers []Provider) (Queue, error) {
	if isNilContract(value) {
		return nil, errors.New("sync queue is nil")
	}
	switch queue := value.(type) {
	case Queue:
		return queue, nil
	case repository.SyncQueue:
		return AdaptQueueForProviders(queue, providers), nil
	default:
		return nil, errors.New("sync queue has an unsupported contract")
	}
}

func isNilContract(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (p *FoundationProvider) ID() ProviderID {
	return p.id
}

func (p *FoundationProvider) Capabilities() Capabilities {
	return p.caps
}

func (p *FoundationProvider) Push(ctx context.Context, operation Operation) error {
	if operation.ProviderID != p.id {
		return fmt.Errorf("%w: provider %s received operation for %s", ErrProviderMismatch, p.id, operation.ProviderID)
	}
	switch EntityType(operation.EntityType) {
	case EntityTask:
		var task domain.Task
		if err := decodeOperationPayload(operation, &task); err != nil {
			return err
		}
		if task.ProviderID != p.providerID() {
			return fmt.Errorf("%w: task belongs to %s, provider is %s", ErrProviderMismatch, task.ProviderID, p.providerID())
		}
		return p.pushTask(ctx, operation, task)
	case EntityList:
		var list domain.List
		if err := decodeOperationPayload(operation, &list); err != nil {
			return err
		}
		if list.ProviderID != p.providerID() {
			return fmt.Errorf("%w: list belongs to %s, provider is %s", ErrProviderMismatch, list.ProviderID, p.providerID())
		}
		return p.pushList(ctx, operation, list)
	case EntitySpace:
		var space domain.Space
		if err := decodeOperationPayload(operation, &space); err != nil {
			return err
		}
		if space.ProviderID != p.providerID() {
			return fmt.Errorf("%w: space belongs to %s, provider is %s", ErrProviderMismatch, space.ProviderID, p.providerID())
		}
		return p.pushSpace(ctx, operation, space)
	default:
		return fmt.Errorf("%w: entity type %s", ErrUnsupported, operation.EntityType)
	}
}

func (p *FoundationProvider) Pull(ctx context.Context) error {
	if puller, ok := p.provider.(providerpkg.Puller); ok {
		return puller.Pull(ctx)
	}
	if synchronizer, ok := p.provider.(providerpkg.Synchronizer); ok {
		return synchronizer.Sync(ctx)
	}
	return nil
}

func (p *FoundationProvider) providerID() domain.ProviderID {
	return domain.ProviderID(p.id)
}

func (p *FoundationProvider) pushTask(ctx context.Context, operation Operation, task domain.Task) error {
	switch operation.Type() {
	case OperationCreate:
		_, err := p.provider.CreateTask(ctx, task)
		return err
	case OperationUpdate:
		_, err := p.provider.UpdateTask(ctx, task)
		return err
	case OperationDelete:
		return p.provider.DeleteTask(ctx, task)
	default:
		return fmt.Errorf("%w: operation type %s", ErrUnsupported, operation.Type())
	}
}

func (p *FoundationProvider) pushList(ctx context.Context, operation Operation, list domain.List) error {
	writer, ok := p.provider.(providerpkg.ListWriter)
	if !ok {
		return fmt.Errorf("%w: list writes for provider %s", ErrUnsupported, p.id)
	}
	switch operation.Type() {
	case OperationCreate:
		_, err := writer.CreateList(ctx, list)
		return err
	case OperationUpdate:
		_, err := writer.UpdateList(ctx, list)
		return err
	case OperationDelete:
		return writer.DeleteList(ctx, list)
	default:
		return fmt.Errorf("%w: operation type %s", ErrUnsupported, operation.Type())
	}
}

func (p *FoundationProvider) pushSpace(ctx context.Context, operation Operation, space domain.Space) error {
	writer, ok := p.provider.(providerpkg.SpaceWriter)
	if !ok {
		return fmt.Errorf("%w: space writes for provider %s", ErrUnsupported, p.id)
	}
	switch operation.Type() {
	case OperationCreate:
		_, err := writer.CreateSpace(ctx, space)
		return err
	case OperationUpdate:
		_, err := writer.UpdateSpace(ctx, space)
		return err
	case OperationDelete:
		return writer.DeleteSpace(ctx, space)
	default:
		return fmt.Errorf("%w: operation type %s", ErrUnsupported, operation.Type())
	}
}

func decodeOperationPayload(operation Operation, target any) error {
	if len(operation.Payload) == 0 {
		return Terminal(fmt.Errorf("%w: operation %s has empty payload", ErrInvalidPayload, operation.ID))
	}
	if err := json.Unmarshal(operation.Payload, target); err != nil {
		return Terminal(fmt.Errorf("%w: operation %s payload: %v", ErrInvalidPayload, operation.ID, err))
	}
	return nil
}

// NewRepositoryWorker creates a worker using the foundation repository and
// internal/provider contracts without exporting the adapters to other layers.
func NewRepositoryWorker(queue repository.SyncQueue, value providerpkg.Provider, options WorkerOptions) (*Worker, error) {
	if isNilContract(queue) {
		return nil, errors.New("sync foundation queue is nil")
	}
	provider := AdaptProvider(value)
	if provider == nil {
		return nil, errors.New("sync foundation provider is nil")
	}
	return newWorker(AdaptQueue(queue, provider.ID()), provider, options)
}

// NewProviderWorker is a descriptive alias for NewRepositoryWorker.
func NewProviderWorker(queue repository.SyncQueue, value providerpkg.Provider, options WorkerOptions) (*Worker, error) {
	return NewRepositoryWorker(queue, value, options)
}

// NewWorkerFromRepository is an alternate constructor name for callers that
// keep repository and provider wiring in a composition root.
func NewWorkerFromRepository(queue repository.SyncQueue, value providerpkg.Provider, options WorkerOptions) (*Worker, error) {
	return NewRepositoryWorker(queue, value, options)
}

// NewRepositoryEngine constructs independent workers from the foundation
// contracts. Non-remote provider instances are skipped by NewEngine.
func NewRepositoryEngine(queue repository.SyncQueue, values []providerpkg.Provider, options EngineOptions) (*Engine, error) {
	if isNilContract(queue) {
		return nil, errors.New("sync foundation queue is nil")
	}
	providers := make([]Provider, 0, len(values))
	for _, value := range values {
		adapted := AdaptProvider(value)
		if adapted == nil {
			return nil, errors.New("sync foundation provider is nil")
		}
		providers = append(providers, adapted)
	}
	return newEngine(AdaptQueueForProviders(queue, providers), providers, options)
}

// NewEngineFromRepository is an alternate constructor name for the foundation
// repository/provider contract.
func NewEngineFromRepository(queue repository.SyncQueue, values []providerpkg.Provider, options EngineOptions) (*Engine, error) {
	return NewRepositoryEngine(queue, values, options)
}

// AdaptQueueForProviders supplies each Worker with a provider-bound queue
// adapter while preserving the small local Queue contract.
func AdaptQueueForProviders(queue repository.SyncQueue, providers []Provider) Queue {
	if isNilContract(queue) {
		return nil
	}
	// Engine creates workers with one Queue value. The provider-bound adapter
	// below routes lifecycle calls by the provider ID supplied to each method.
	return &multiRepositoryQueue{queue: queue, providers: providers}
}

type multiRepositoryQueue struct {
	queue     repository.SyncQueue
	providers []Provider
	mu        stdsync.Mutex
	claimed   map[OperationID]ProviderID
}

func (q *multiRepositoryQueue) adapter(providerID ProviderID) *RepositoryQueue {
	return &RepositoryQueue{queue: q.queue, providerID: domain.ProviderID(providerID)}
}

func (q *multiRepositoryQueue) RecoverStale(ctx context.Context, providerID ProviderID, before time.Time) error {
	return q.adapter(providerID).RecoverStale(ctx, providerID, before)
}

func (q *multiRepositoryQueue) Claim(ctx context.Context, providerID ProviderID, now time.Time) (Operation, error) {
	operation, err := q.adapter(providerID).Claim(ctx, providerID, now)
	if err != nil {
		return Operation{}, err
	}
	q.mu.Lock()
	if q.claimed == nil {
		q.claimed = make(map[OperationID]ProviderID)
	}
	q.claimed[operation.ID] = providerID
	q.mu.Unlock()
	return operation, nil
}

func (q *multiRepositoryQueue) MarkAttempt(ctx context.Context, operationID OperationID, at time.Time) error {
	return q.queueMarkAttempt(ctx, operationID, at)
}

func (q *multiRepositoryQueue) Complete(ctx context.Context, operationID OperationID, at time.Time) error {
	providerID, err := q.providerForOperation(ctx, operationID)
	if err != nil {
		return err
	}
	err = q.adapter(providerID).Complete(ctx, operationID, at)
	if err == nil {
		q.forget(operationID)
	}
	return err
}

func (q *multiRepositoryQueue) Fail(ctx context.Context, operationID OperationID, failure Failure) error {
	providerID, err := q.providerForOperation(ctx, operationID)
	if err != nil {
		return err
	}
	err = q.adapter(providerID).Fail(ctx, operationID, failure)
	if err == nil {
		q.forget(operationID)
	}
	return err
}

func (q *multiRepositoryQueue) Release(ctx context.Context, operationID OperationID) error {
	providerID, err := q.providerForOperation(ctx, operationID)
	if err != nil {
		return err
	}
	err = q.adapter(providerID).Release(ctx, operationID)
	if err == nil {
		q.forget(operationID)
	}
	return err
}

func (q *multiRepositoryQueue) queueMarkAttempt(context.Context, OperationID, time.Time) error {
	return nil
}

func (q *multiRepositoryQueue) providerForOperation(_ context.Context, operationID OperationID) (ProviderID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	providerID, ok := q.claimed[operationID]
	if ok {
		return providerID, nil
	}
	if len(q.providers) == 1 {
		return q.providers[0].ID(), nil
	}
	return "", errors.New("repository queue operation was not claimed by a provider worker")
}

func (q *multiRepositoryQueue) forget(operationID OperationID) {
	q.mu.Lock()
	delete(q.claimed, operationID)
	q.mu.Unlock()
}
