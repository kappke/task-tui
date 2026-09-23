package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

type providerScopedQueue interface {
	CompleteForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string) error
	RetryForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string, error) error
	RetryAtForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string, time.Time, error) error
	FailForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string, error) error
	ReleaseForProviderWithLease(context.Context, domain.ProviderID, domain.OperationID, string) error
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
	converted := fromDomainOperation(operation)
	if err := requireLease(converted.LeaseOwner); err != nil {
		return Operation{}, err
	}
	return converted, nil
}

func (q *RepositoryQueue) MarkAttempt(context.Context, OperationID, time.Time) error {
	// repository.SyncQueue.Claim performs this increment transactionally before
	// returning. Calling another increment here would count every attempt twice.
	return nil
}

func (q *RepositoryQueue) Complete(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, _ time.Time) error {
	if err := q.checkProvider(providerID); err != nil {
		return err
	}
	if err := requireLease(leaseOwner); err != nil {
		return err
	}
	if scoped, ok := q.queue.(providerScopedQueue); ok {
		return mapQueueError(scoped.CompleteForProviderWithLease(ctx, q.providerID, domain.OperationID(operationID), leaseOwner))
	}
	return errors.New("sync foundation queue does not support scoped completion")
}

func (q *RepositoryQueue) Fail(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, failure Failure) error {
	if err := q.checkProvider(providerID); err != nil {
		return err
	}
	if err := requireLease(leaseOwner); err != nil {
		return err
	}
	cause := failure.Err
	if cause == nil {
		cause = errors.New(failure.Error())
	}
	if failure.RetryAt != nil {
		if scoped, ok := q.queue.(providerScopedQueue); ok {
			return mapQueueError(scoped.RetryAtForProviderWithLease(ctx, q.providerID, domain.OperationID(operationID), leaseOwner, *failure.RetryAt, cause))
		}
		return errors.New("sync foundation queue does not support scoped retry")
	}
	if scoped, ok := q.queue.(providerScopedQueue); ok {
		return mapQueueError(scoped.FailForProviderWithLease(ctx, q.providerID, domain.OperationID(operationID), leaseOwner, cause))
	}
	return errors.New("sync foundation queue does not support scoped failure")
}

func (q *RepositoryQueue) Release(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string) error {
	if err := q.checkProvider(providerID); err != nil {
		return err
	}
	if err := requireLease(leaseOwner); err != nil {
		return err
	}
	if scoped, ok := q.queue.(providerScopedQueue); ok {
		return mapQueueError(scoped.ReleaseForProviderWithLease(ctx, q.providerID, domain.OperationID(operationID), leaseOwner))
	}
	return errors.New("sync foundation queue does not support scoped release")
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
		LeaseOwner:    operation.LeaseOwner,
	}
	if operation.NextAttemptAt != nil {
		converted.NextAttemptAt = *operation.NextAttemptAt
	}
	if operation.LeaseExpiresAt != nil {
		converted.LeaseExpiresAt = operation.LeaseExpiresAt
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
	provider   providerpkg.Provider
	id         ProviderID
	caps       Capabilities
	identityMu stdsync.Mutex
	remoteIDs  map[string]string
}

func (p *FoundationProvider) SetSyncProgress(callback func(string)) {
	if reporter, ok := p.provider.(interface{ SetSyncProgress(func(string)) }); ok {
		reporter.SetSyncProgress(callback)
	}
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
		provider:  value,
		id:        id,
		remoteIDs: make(map[string]string),
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
		task, err := decodeTaskMutationPayload(operation)
		if err != nil {
			return err
		}
		if task.ProviderID != p.providerID() {
			return fmt.Errorf("%w: task belongs to %s, provider is %s", ErrProviderMismatch, task.ProviderID, p.providerID())
		}
		return p.pushTask(ctx, operation, task)
	case EntityList:
		list, err := decodeListMutationPayload(operation)
		if err != nil {
			return err
		}
		if list.ProviderID != p.providerID() {
			return fmt.Errorf("%w: list belongs to %s, provider is %s", ErrProviderMismatch, list.ProviderID, p.providerID())
		}
		return p.pushList(ctx, operation, list)
	case EntitySpace:
		space, err := decodeSpaceMutationPayload(operation)
		if err != nil {
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
	if operation.Type() != OperationCreate {
		p.identityMu.Lock()
		if task.RemoteID == nil {
			if remoteID := p.remoteIDs[task.ID.String()]; remoteID != "" {
				task.RemoteID = &remoteID
			}
		}
		p.identityMu.Unlock()
	}
	switch operation.Type() {
	case OperationCreate:
		p.identityMu.Lock()
		remoteID := p.remoteIDs[task.ID.String()]
		p.identityMu.Unlock()
		if remoteID != "" {
			task.RemoteID = &remoteID
			_, err := p.provider.UpdateTask(ctx, task)
			return err
		}
		created, err := p.provider.CreateTask(ctx, task)
		if created.RemoteID != nil && *created.RemoteID != "" {
			p.identityMu.Lock()
			p.remoteIDs[task.ID.String()] = *created.RemoteID
			p.identityMu.Unlock()
		}
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

func decodeTaskMutationPayload(operation Operation) (domain.Task, error) {
	if len(operation.Payload) == 0 {
		return domain.Task{}, Terminal(fmt.Errorf("%w: operation %s has empty payload", ErrInvalidPayload, operation.ID))
	}
	var payload domain.TaskMutationPayload
	if err := json.Unmarshal(operation.Payload, &payload); err != nil {
		return domain.Task{}, Terminal(fmt.Errorf("%w: operation %s payload: %v", ErrInvalidPayload, operation.ID, err))
	}
	if payload.Version == 0 {
		// Queue rows written before the normalized contract are still readable;
		// newly created rows always use the versioned form below.
		var legacy domain.Task
		if err := json.Unmarshal(operation.Payload, &legacy); err != nil || legacy.ID.IsZero() {
			return domain.Task{}, Terminal(fmt.Errorf("%w: operation %s has unsupported task payload", ErrInvalidPayload, operation.ID))
		}
		return legacy, nil
	}
	if payload.Version != domain.TaskMutationPayloadVersion || payload.ProviderID != domain.ProviderID(operation.ProviderID) || payload.TaskID != domain.TaskID(operation.EntityID) {
		return domain.Task{}, Terminal(fmt.Errorf("%w: operation %s has invalid task identity or version", ErrInvalidPayload, operation.ID))
	}
	if payload.Snapshot == nil {
		return domain.Task{}, Terminal(fmt.Errorf("%w: operation %s has no task snapshot", ErrInvalidPayload, operation.ID))
	}
	task := *payload.Snapshot
	if task.ProviderID != payload.ProviderID || task.ID != payload.TaskID {
		return domain.Task{}, Terminal(fmt.Errorf("%w: operation %s snapshot identity mismatch", ErrInvalidPayload, operation.ID))
	}
	if payload.TaskPatch != nil && operation.Type() != OperationDelete {
		updated, err := payload.TaskPatch.Apply(task)
		if err != nil {
			return domain.Task{}, Terminal(fmt.Errorf("%w: operation %s patch: %v", ErrInvalidPayload, operation.ID, err))
		}
		task = updated
	}
	return task, nil
}

func decodeSpaceMutationPayload(operation Operation) (domain.Space, error) {
	if len(operation.Payload) == 0 {
		return domain.Space{}, Terminal(fmt.Errorf("%w: operation %s has empty payload", ErrInvalidPayload, operation.ID))
	}
	var payload domain.SpaceMutationPayload
	if err := json.Unmarshal(operation.Payload, &payload); err != nil {
		return domain.Space{}, Terminal(fmt.Errorf("%w: operation %s payload: %v", ErrInvalidPayload, operation.ID, err))
	}
	if payload.Version != 0 {
		if payload.Version != domain.HierarchyMutationPayloadVersion || payload.ProviderID != domain.ProviderID(operation.ProviderID) || payload.SpaceID != domain.SpaceID(operation.EntityID) || payload.Snapshot == nil {
			return domain.Space{}, Terminal(fmt.Errorf("%w: operation %s has invalid space identity or version", ErrInvalidPayload, operation.ID))
		}
		space := *payload.Snapshot
		if space.ProviderID != payload.ProviderID || space.ID != payload.SpaceID {
			return domain.Space{}, Terminal(fmt.Errorf("%w: operation %s space snapshot identity mismatch", ErrInvalidPayload, operation.ID))
		}
		return space, nil
	}
	var legacy domain.Space
	if err := json.Unmarshal(operation.Payload, &legacy); err != nil || legacy.ID.IsZero() || legacy.ProviderID != domain.ProviderID(operation.ProviderID) || legacy.ID != domain.SpaceID(operation.EntityID) {
		return domain.Space{}, Terminal(fmt.Errorf("%w: operation %s has unsupported space payload", ErrInvalidPayload, operation.ID))
	}
	return legacy, nil
}

func decodeListMutationPayload(operation Operation) (domain.List, error) {
	if len(operation.Payload) == 0 {
		return domain.List{}, Terminal(fmt.Errorf("%w: operation %s has empty payload", ErrInvalidPayload, operation.ID))
	}
	var payload domain.ListMutationPayload
	if err := json.Unmarshal(operation.Payload, &payload); err != nil {
		return domain.List{}, Terminal(fmt.Errorf("%w: operation %s payload: %v", ErrInvalidPayload, operation.ID, err))
	}
	if payload.Version != 0 {
		if payload.Version != domain.HierarchyMutationPayloadVersion || payload.ProviderID != domain.ProviderID(operation.ProviderID) || payload.ListID != domain.ListID(operation.EntityID) || payload.Snapshot == nil {
			return domain.List{}, Terminal(fmt.Errorf("%w: operation %s has invalid list identity or version", ErrInvalidPayload, operation.ID))
		}
		list := *payload.Snapshot
		if list.ProviderID != payload.ProviderID || list.ID != payload.ListID {
			return domain.List{}, Terminal(fmt.Errorf("%w: operation %s list snapshot identity mismatch", ErrInvalidPayload, operation.ID))
		}
		return list, nil
	}
	var legacy domain.List
	if err := json.Unmarshal(operation.Payload, &legacy); err != nil || legacy.ID.IsZero() || legacy.ProviderID != domain.ProviderID(operation.ProviderID) || legacy.ID != domain.ListID(operation.EntityID) {
		return domain.List{}, Terminal(fmt.Errorf("%w: operation %s has unsupported list payload", ErrInvalidPayload, operation.ID))
	}
	return legacy, nil
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
	claimed   map[OperationID]queueClaim
}

type queueClaim struct {
	providerID ProviderID
	leaseOwner string
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
		q.claimed = make(map[OperationID]queueClaim)
	}
	q.claimed[operation.ID] = queueClaim{providerID: providerID, leaseOwner: operation.LeaseOwner}
	q.mu.Unlock()
	return operation, nil
}

func (q *multiRepositoryQueue) MarkAttempt(ctx context.Context, operationID OperationID, at time.Time) error {
	return q.queueMarkAttempt(ctx, operationID, at)
}

func (q *multiRepositoryQueue) Complete(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, at time.Time) error {
	if err := q.checkClaim(providerID, operationID, leaseOwner); err != nil {
		return err
	}
	err := q.adapter(providerID).Complete(ctx, providerID, operationID, leaseOwner, at)
	if err == nil {
		q.forget(operationID)
	}
	return err
}

func (q *multiRepositoryQueue) Fail(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, failure Failure) error {
	if err := q.checkClaim(providerID, operationID, leaseOwner); err != nil {
		return err
	}
	err := q.adapter(providerID).Fail(ctx, providerID, operationID, leaseOwner, failure)
	if err == nil {
		q.forget(operationID)
	}
	return err
}

func (q *multiRepositoryQueue) Release(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string) error {
	if err := q.checkClaim(providerID, operationID, leaseOwner); err != nil {
		return err
	}
	err := q.adapter(providerID).Release(ctx, providerID, operationID, leaseOwner)
	if err == nil {
		q.forget(operationID)
	}
	return err
}

func (q *multiRepositoryQueue) checkClaim(providerID ProviderID, operationID OperationID, leaseOwner string) error {
	if err := requireLease(leaseOwner); err != nil {
		return err
	}
	q.mu.Lock()
	claim, ok := q.claimed[operationID]
	q.mu.Unlock()
	if !ok || claim.providerID != providerID || claim.leaseOwner != leaseOwner {
		return fmt.Errorf("sync operation %s was not claimed for provider %s", operationID, providerID)
	}
	return nil
}

func requireLease(leaseOwner string) error {
	if strings.TrimSpace(leaseOwner) == "" {
		return errors.New("sync operation lease owner is required")
	}
	return nil
}

func (q *multiRepositoryQueue) queueMarkAttempt(context.Context, OperationID, time.Time) error {
	return nil
}

func (q *multiRepositoryQueue) forget(operationID OperationID) {
	q.mu.Lock()
	delete(q.claimed, operationID)
	q.mu.Unlock()
}
