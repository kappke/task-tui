package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/repository"
)

func (s *Store) ApplyTaskMutation(ctx context.Context, mutation repository.TaskMutation) (domain.Task, error) {
	if err := mutation.Operation.Validate(); err != nil {
		return domain.Task{}, err
	}
	if err := repository.ValidateTaskMutationPayload(mutation.Payload); err != nil {
		return domain.Task{}, err
	}
	task := mutation.Task
	if task.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return domain.Task{}, fmt.Errorf("sqlite: generate task mutation id: %w", err)
		}
		task.ID = domain.TaskID(id)
	}
	if task.SyncState.IsZero() {
		task.SyncState = domain.SyncStateLocal
	}
	if task.Priority.IsZero() {
		task.Priority = domain.PriorityNormal
	}
	if task.Status == "" {
		task.Status = "todo"
	}
	if err := task.Validate(); err != nil {
		return domain.Task{}, err
	}
	record, err := taskRecord(task)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.validateTaskParents(ctx, record); err != nil {
		return domain.Task{}, err
	}
	if mutation.Operation == domain.OperationTypeDelete {
		now := time.Now().UTC()
		record.IsDeleted = true
		record.DeletedAt = &now
	}
	record.SyncState = SyncStatePending
	intent := SyncOperation{
		ProviderID: string(task.ProviderID),
		EntityType: EntityTask,
		EntityID:   task.ID.String(),
		Operation:  OperationType(mutation.Operation),
		Payload:    append([]byte(nil), mutation.Payload...),
		Status:     QueueStatusPending,
	}
	if mutation.Operation == domain.OperationTypeCreate {
		created, err := s.createTaskWithQueue(ctx, record, &intent)
		if err != nil {
			return domain.Task{}, adaptError(err, false)
		}
		return domainTask(created), nil
	}
	updated, err := s.mutateTask(ctx, record, &intent)
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	return domainTask(updated), nil
}

func (s *Store) MutateTask(ctx context.Context, mutation repository.TaskMutation) (domain.Task, error) {
	return s.ApplyTaskMutation(ctx, mutation)
}

func (s *Store) UpdateTaskWithQueue(ctx context.Context, task domain.Task, operation *domain.SyncOperation) (domain.Task, error) {
	if operation == nil {
		return s.UpdateTask(ctx, task)
	}
	if operation.ProviderID != "" && operation.ProviderID != task.ProviderID {
		return domain.Task{}, domain.ErrProviderMismatch
	}
	if operation.EntityType != "" && operation.EntityType != domain.EntityTypeTask {
		return domain.Task{}, domain.ErrProviderMismatch
	}
	if operation.EntityID != "" && operation.EntityID != task.ID.String() {
		return domain.Task{}, domain.ErrInvalidParent
	}
	return s.ApplyTaskMutation(ctx, repository.TaskMutation{
		Task:      task,
		Operation: operation.Operation,
		Payload:   operation.Payload,
	})
}

func (s *Store) CreateTaskWithQueue(ctx context.Context, task domain.Task, operation *domain.SyncOperation) (domain.Task, error) {
	if operation == nil {
		return s.CreateTask(ctx, task)
	}
	return s.ApplyTaskMutation(ctx, repository.TaskMutation{
		Task:      task,
		Operation: domain.OperationTypeCreate,
		Payload:   operation.Payload,
	})
}

func operationRecord(operation domain.SyncOperation) (SyncOperation, error) {
	if operation.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return SyncOperation{}, fmt.Errorf("sqlite: generate operation id: %w", err)
		}
		operation.ID = domain.OperationID(id)
	}
	if operation.Status.IsZero() {
		operation.Status = domain.SyncStatusPending
	}
	if err := operation.Validate(); err != nil {
		return SyncOperation{}, err
	}
	operation = operation.NormalizeUTC()
	var errorValue *string
	if operation.Error != "" {
		value := operation.Error
		errorValue = &value
	}
	nextAttemptAt := time.Time{}
	if operation.NextAttemptAt != nil {
		nextAttemptAt = operation.NextAttemptAt.UTC()
	}
	return SyncOperation{
		ID:            operation.ID.String(),
		ProviderID:    operation.ProviderID.String(),
		EntityType:    EntityType(operation.EntityType),
		EntityID:      operation.EntityID,
		Operation:     OperationType(operation.Operation),
		Payload:       append([]byte(nil), operation.Payload...),
		Attempts:      operation.Attempts,
		Status:        QueueStatus(operation.Status),
		Error:         errorValue,
		CreatedAt:     operation.CreatedAt,
		LastAttemptAt: operation.LastAttemptAt,
		NextAttemptAt: nextAttemptAt,
	}, nil
}

func domainOperation(operation SyncOperation) domain.SyncOperation {
	leaseOwner := ""
	if operation.LeaseOwner != nil {
		leaseOwner = *operation.LeaseOwner
	}
	result := domain.SyncOperation{
		ID:             domain.OperationID(operation.ID),
		ProviderID:     domain.ProviderID(operation.ProviderID),
		EntityType:     domain.EntityType(operation.EntityType),
		EntityID:       operation.EntityID,
		Operation:      domain.OperationType(operation.Operation),
		Payload:        append([]byte(nil), operation.Payload...),
		Attempts:       operation.Attempts,
		Status:         domain.SyncStatus(operation.Status),
		CreatedAt:      operation.CreatedAt,
		LastAttemptAt:  operation.LastAttemptAt,
		LeaseOwner:     leaseOwner,
		LeaseExpiresAt: operation.LeaseExpiresAt,
	}
	if operation.Error != nil {
		result.Error = *operation.Error
	}
	if !operation.NextAttemptAt.IsZero() {
		next := operation.NextAttemptAt
		result.NextAttemptAt = &next
	}
	return result
}

func (s *Store) Enqueue(ctx context.Context, operation domain.SyncOperation) error {
	record, err := operationRecord(operation)
	if err != nil {
		return err
	}
	if err := s.validateOperationTarget(ctx, record); err != nil {
		return err
	}
	if err := enqueueExec(ctx, s.db, record); err != nil {
		return adaptError(err, true)
	}
	return nil
}

func (s *Store) validateOperationTarget(ctx context.Context, operation SyncOperation) error {
	switch operation.EntityType {
	case EntitySpace:
		space, err := s.getSpaceByID(ctx, operation.EntityID)
		if err != nil {
			return adaptError(err, false)
		}
		if space.ProviderID != operation.ProviderID {
			return domain.ErrProviderMismatch
		}
	case EntityList:
		list, err := s.getListByID(ctx, operation.EntityID)
		if err != nil {
			return adaptError(err, false)
		}
		if list.ProviderID != operation.ProviderID {
			return domain.ErrProviderMismatch
		}
	case EntityTask:
		task, err := s.getTaskByID(ctx, operation.EntityID)
		if err != nil {
			return adaptError(err, false)
		}
		if task.ProviderID != operation.ProviderID {
			return domain.ErrProviderMismatch
		}
	default:
		return domain.ErrInvalidEnum
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, providerID domain.ProviderID) (domain.SyncOperation, error) {
	operation, err := s.claimOperation(ctx, providerID.String(), s.workerID, defaultLease)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.SyncOperation{}, domain.ErrQueueEmpty
		}
		return domain.SyncOperation{}, err
	}
	return domainOperation(operation), nil
}

func (s *Store) Complete(ctx context.Context, operationID domain.OperationID) error {
	operation, err := s.getOperationByID(ctx, operationID.String())
	if err != nil {
		return adaptError(err, false)
	}
	return s.completeOperation(ctx, operation.ProviderID, operation.ID, s.workerID)
}

func (s *Store) CompleteForProvider(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID) error {
	return s.completeOperation(ctx, providerID.String(), operationID.String(), s.workerID)
}

func (s *Store) CompleteForProviderWithLease(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, leaseOwner string) error {
	if err := validateLeaseOwner(leaseOwner); err != nil {
		return err
	}
	return s.completeOperation(ctx, providerID.String(), operationID.String(), leaseOwner)
}

func (s *Store) Retry(ctx context.Context, operationID domain.OperationID, cause error) error {
	operation, err := s.getOperationByID(ctx, operationID.String())
	if err != nil {
		return adaptError(err, false)
	}
	return s.retryOperation(ctx, operation.ProviderID, operation.ID, s.workerID, time.Now().UTC(), cause)
}

func (s *Store) RetryForProvider(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, cause error) error {
	return s.retryOperation(ctx, providerID.String(), operationID.String(), s.workerID, time.Now().UTC(), cause)
}

func (s *Store) RetryForProviderWithLease(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, leaseOwner string, cause error) error {
	if err := validateLeaseOwner(leaseOwner); err != nil {
		return err
	}
	return s.retryOperation(ctx, providerID.String(), operationID.String(), leaseOwner, time.Now().UTC(), cause)
}

func (s *Store) RetryAt(ctx context.Context, operationID domain.OperationID, nextAttemptAt time.Time, cause error) error {
	operation, err := s.getOperationByID(ctx, operationID.String())
	if err != nil {
		return adaptError(err, false)
	}
	return s.retryOperation(ctx, operation.ProviderID, operation.ID, s.workerID, nextAttemptAt.UTC(), cause)
}

func (s *Store) RetryAtForProvider(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, nextAttemptAt time.Time, cause error) error {
	return s.retryOperation(ctx, providerID.String(), operationID.String(), s.workerID, nextAttemptAt.UTC(), cause)
}

func (s *Store) RetryAtForProviderWithLease(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, leaseOwner string, nextAttemptAt time.Time, cause error) error {
	if err := validateLeaseOwner(leaseOwner); err != nil {
		return err
	}
	return s.retryOperation(ctx, providerID.String(), operationID.String(), leaseOwner, nextAttemptAt.UTC(), cause)
}

func (s *Store) Release(ctx context.Context, operationID domain.OperationID) error {
	operation, err := s.getOperationByID(ctx, operationID.String())
	if err != nil {
		return adaptError(err, false)
	}
	return s.releaseOperation(ctx, operation.ProviderID, operation.ID, s.workerID)
}

func (s *Store) ReleaseForProvider(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID) error {
	return s.releaseOperation(ctx, providerID.String(), operationID.String(), s.workerID)
}

func (s *Store) ReleaseForProviderWithLease(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, leaseOwner string) error {
	if err := validateLeaseOwner(leaseOwner); err != nil {
		return err
	}
	return s.releaseOperation(ctx, providerID.String(), operationID.String(), leaseOwner)
}

func (s *Store) Fail(ctx context.Context, operationID domain.OperationID, cause error) error {
	operation, err := s.getOperationByID(ctx, operationID.String())
	if err != nil {
		return adaptError(err, false)
	}
	return s.failOperation(ctx, operation.ProviderID, operation.ID, s.workerID, cause)
}

func (s *Store) FailForProvider(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, cause error) error {
	return s.failOperation(ctx, providerID.String(), operationID.String(), s.workerID, cause)
}

func (s *Store) FailForProviderWithLease(ctx context.Context, providerID domain.ProviderID, operationID domain.OperationID, leaseOwner string, cause error) error {
	if err := validateLeaseOwner(leaseOwner); err != nil {
		return err
	}
	return s.failOperation(ctx, providerID.String(), operationID.String(), leaseOwner, cause)
}

func validateLeaseOwner(leaseOwner string) error {
	if leaseOwner == "" {
		return errors.New("sqlite: lease owner is required")
	}
	return nil
}

func (s *Store) RequeueStale(ctx context.Context, providerID domain.ProviderID, before time.Time) (int, error) {
	count, err := s.requeueStaleOperations(ctx, providerID.String(), before.UTC())
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *Store) PendingCount(ctx context.Context, providerID domain.ProviderID) (int, error) {
	return s.pendingOperationCount(ctx, providerID.String())
}

func (s *Store) UpdateProviderSyncState(ctx context.Context, providerID domain.ProviderID, state domain.SyncState, cursor *string, syncErr error, lastSyncAt *time.Time) error {
	if err := state.Validate(); err != nil {
		return err
	}
	existing, err := s.getProviderSyncState(ctx, providerID.String())
	if err != nil {
		return adaptError(err, false)
	}
	if cursor == nil {
		cursor = copyStringPointer(existing.Cursor)
	}
	if lastSyncAt == nil {
		lastSyncAt = cloneTime(existing.LastSyncAt)
	}
	var message *string
	if syncErr != nil {
		value := syncErr.Error()
		message = &value
	} else if state != domain.SyncStateSynced {
		message = copyStringPointer(existing.Error)
	}
	_, err = s.setProviderSyncState(ctx, ProviderSyncState{
		ProviderID: providerID.String(),
		State:      SyncState(state),
		Cursor:     cursor,
		Error:      message,
		LastSyncAt: lastSyncAt,
	})
	return adaptError(err, false)
}

func (s *Store) PutMetadata(ctx context.Context, metadata domain.ProviderMetadata) error {
	record, err := metadataRecord(metadata)
	if err != nil {
		return err
	}
	_, err = s.putMetadata(ctx, record)
	return adaptError(err, false)
}

func (s *Store) GetMetadata(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID, key string) (domain.ProviderMetadata, error) {
	metadata, err := s.getMetadata(ctx, providerID.String(), EntityType(entityType), entityID, key)
	if err != nil {
		return domain.ProviderMetadata{}, adaptError(err, false)
	}
	return domainMetadata(metadata), nil
}

func (s *Store) ListMetadata(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID string) ([]domain.ProviderMetadata, error) {
	metadata, err := s.listMetadata(ctx, providerID.String(), EntityType(entityType), entityID)
	if err != nil {
		return nil, err
	}
	result := make([]domain.ProviderMetadata, 0, len(metadata))
	for _, item := range metadata {
		result = append(result, domainMetadata(item))
	}
	return result, nil
}

// ListMetadataForEntities loads metadata for a provider-scoped entity batch.
func (s *Store) ListMetadataForEntities(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityIDs []string) (map[string][]domain.ProviderMetadata, error) {
	metadata, err := s.listMetadataForEntities(ctx, providerID.String(), EntityType(entityType), entityIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]domain.ProviderMetadata, len(metadata))
	for entityID, items := range metadata {
		values := make([]domain.ProviderMetadata, 0, len(items))
		for _, item := range items {
			values = append(values, domainMetadata(item))
		}
		result[entityID] = values
	}
	return result, nil
}

func (s *Store) UpsertMetadata(ctx context.Context, metadata domain.ProviderMetadata) error {
	return s.PutMetadata(ctx, metadata)
}

func (s *Store) DeleteMetadata(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID, key string) error {
	return adaptError(s.deleteMetadata(ctx, providerID.String(), EntityType(entityType), entityID, key), false)
}

func metadataRecord(metadata domain.ProviderMetadata) (Metadata, error) {
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	return Metadata{
		ProviderID: metadata.ProviderID.String(),
		EntityType: EntityType(metadata.EntityType),
		EntityID:   metadata.EntityID,
		Key:        metadata.Key,
		Value:      []byte(metadata.Value),
	}, nil
}

func domainMetadata(metadata Metadata) domain.ProviderMetadata {
	return domain.ProviderMetadata{
		EntityType: domain.EntityType(metadata.EntityType),
		EntityID:   metadata.EntityID,
		ProviderID: domain.ProviderID(metadata.ProviderID),
		Key:        metadata.Key,
		Value:      string(metadata.Value),
	}
}

func (s *Store) SaveBase(ctx context.Context, base domain.SyncBase) error {
	record, err := syncBaseRecord(base)
	if err != nil {
		return err
	}
	_, err = s.putSyncBase(ctx, record)
	return adaptError(err, false)
}

func (s *Store) GetBase(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID string) (domain.SyncBase, error) {
	base, err := s.getSyncBase(ctx, providerID.String(), EntityType(entityType), entityID)
	if err != nil {
		return domain.SyncBase{}, adaptError(err, false)
	}
	return domainSyncBase(base), nil
}

func (s *Store) DeleteBase(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID string) error {
	return adaptError(s.deleteSyncBase(ctx, providerID.String(), EntityType(entityType), entityID), false)
}

func (s *Store) SaveSyncBase(ctx context.Context, base domain.SyncBase) error {
	return s.SaveBase(ctx, base)
}

func (s *Store) GetSyncBase(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID string) (domain.SyncBase, error) {
	return s.GetBase(ctx, providerID, entityType, entityID)
}

func (s *Store) DeleteSyncBase(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID string) error {
	return s.DeleteBase(ctx, providerID, entityType, entityID)
}

func syncBaseRecord(base domain.SyncBase) (SyncBase, error) {
	if base.SyncState.IsZero() {
		base.SyncState = domain.SyncStateLocal
	}
	if err := base.Validate(); err != nil {
		return SyncBase{}, err
	}
	if base.EntityType.IsZero() || base.EntityID == "" {
		return SyncBase{}, fmt.Errorf("%w: sync base entity identity is required", domain.ErrInvalidID)
	}
	capturedAt := base.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = base.CreatedAt
	}
	createdAt := base.CreatedAt
	if createdAt.IsZero() {
		createdAt = capturedAt
	}
	return SyncBase{
		ProviderID:      base.ProviderID.String(),
		EntityType:      EntityType(base.EntityType),
		EntityID:        base.EntityID,
		RemoteID:        copyStringPointer(base.RemoteID),
		SyncState:       SyncState(base.SyncState),
		RemoteUpdatedAt: base.RemoteUpdatedAt,
		Payload:         append([]byte(nil), base.Payload...),
		RemoteVersion:   copyStringPointer(base.RemoteVersion),
		CapturedAt:      capturedAt,
		CreatedAt:       createdAt,
		UpdatedAt:       base.UpdatedAt,
	}, nil
}

func domainSyncBase(base SyncBase) domain.SyncBase {
	return domain.SyncBase{
		ProviderID:      baseProviderID(base.ProviderID),
		EntityType:      domain.EntityType(base.EntityType),
		EntityID:        base.EntityID,
		RemoteID:        copyStringPointer(base.RemoteID),
		SyncState:       domain.SyncState(base.SyncState),
		RemoteUpdatedAt: base.RemoteUpdatedAt,
		Payload:         append(json.RawMessage(nil), base.Payload...),
		CreatedAt:       base.CreatedAt,
		UpdatedAt:       base.UpdatedAt,
		CapturedAt:      base.CapturedAt,
		RemoteVersion:   copyStringPointer(base.RemoteVersion),
	}
}

func baseProviderID(id string) domain.ProviderID { return domain.ProviderID(id) }

func (s *Store) CreateConflict(ctx context.Context, conflict domain.Conflict) error {
	record, err := conflictRecord(conflict)
	if err != nil {
		return err
	}
	_, err = s.createConflict(ctx, record)
	return adaptError(err, true)
}

func (s *Store) UpdateConflict(ctx context.Context, conflict domain.Conflict) error {
	record, err := conflictRecord(conflict)
	if err != nil {
		return err
	}
	_, err = s.upsertConflict(ctx, record)
	return adaptError(err, false)
}

func (s *Store) GetConflict(ctx context.Context, id domain.ConflictID) (domain.Conflict, error) {
	conflict, err := s.getConflictByID(ctx, id.String())
	if err != nil {
		return domain.Conflict{}, adaptError(err, false)
	}
	return domainConflict(conflict)
}

func (s *Store) ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Conflict, error) {
	conflicts, err := s.listConflicts(ctx, providerID.String(), true)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		item, err := domainConflict(conflict)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) ListConflicts(ctx context.Context, providerID domain.ProviderID) ([]domain.Conflict, error) {
	return s.ListByProvider(ctx, providerID)
}

func (s *Store) DeleteConflict(ctx context.Context, id domain.ConflictID) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM conflicts WHERE id = ?", id.String())
	if err != nil {
		return err
	}
	return adaptError(requireAffected(result, "delete conflict", id.String()), false)
}

func (s *Store) Resolve(ctx context.Context, id domain.ConflictID, resolution string, resolvedAt time.Time) error {
	conflict, err := s.getConflictByID(ctx, id.String())
	if err != nil {
		return adaptError(err, false)
	}
	resolvedAt = resolvedAt.UTC()
	_, err = s.db.ExecContext(ctx, `
		UPDATE conflicts SET status = 'resolved', resolution = ?, resolved_at = ?, updated_at = ?
		WHERE id = ?`, resolution, formatTime(resolvedAt), formatTime(time.Now()), conflict.ID)
	return err
}

func conflictRecord(conflict domain.Conflict) (Conflict, error) {
	if conflict.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return Conflict{}, fmt.Errorf("sqlite: generate conflict id: %w", err)
		}
		conflict.ID = domain.ConflictID(id)
	}
	if err := conflict.Validate(); err != nil {
		return Conflict{}, err
	}
	fields, err := json.Marshal(conflict.Fields)
	if err != nil {
		return Conflict{}, fmt.Errorf("sqlite: marshal conflict fields: %w", err)
	}
	baseValue, err := marshalConflictValues(conflict.Fields, func(field domain.FieldConflict) json.RawMessage {
		return field.BaseValue
	})
	if err != nil {
		return Conflict{}, fmt.Errorf("sqlite: marshal conflict base values: %w", err)
	}
	remoteValue, err := marshalConflictValues(conflict.Fields, func(field domain.FieldConflict) json.RawMessage {
		return field.RemoteValue
	})
	if err != nil {
		return Conflict{}, fmt.Errorf("sqlite: marshal conflict remote values: %w", err)
	}
	status := "open"
	if conflict.ResolvedAt != nil {
		status = "resolved"
	}
	return Conflict{
		ID:          conflict.ID.String(),
		ProviderID:  conflict.ProviderID.String(),
		EntityType:  EntityType(conflict.EntityType),
		EntityID:    conflict.EntityID,
		BaseValue:   baseValue,
		LocalValue:  fields,
		RemoteValue: remoteValue,
		Status:      status,
		Resolution:  []byte(conflict.Resolution),
		CreatedAt:   conflict.CreatedAt,
		UpdatedAt:   conflict.UpdatedAt,
		ResolvedAt:  conflict.ResolvedAt,
	}, nil
}

func marshalConflictValues(fields []domain.FieldConflict, value func(domain.FieldConflict) json.RawMessage) ([]byte, error) {
	values := make(map[string]json.RawMessage)
	for _, field := range fields {
		if current := value(field); len(current) > 0 {
			values[field.Field] = append(json.RawMessage(nil), current...)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return json.Marshal(values)
}

func domainConflict(conflict Conflict) (domain.Conflict, error) {
	var fields []domain.FieldConflict
	if len(conflict.LocalValue) > 0 {
		if err := json.Unmarshal(conflict.LocalValue, &fields); err != nil {
			return domain.Conflict{}, fmt.Errorf("sqlite: decode conflict fields: %w", err)
		}
	}
	return domain.Conflict{
		ID:         domain.ConflictID(conflict.ID),
		ProviderID: domain.ProviderID(conflict.ProviderID),
		EntityType: domain.EntityType(conflict.EntityType),
		EntityID:   conflict.EntityID,
		Fields:     fields,
		CreatedAt:  conflict.CreatedAt,
		UpdatedAt:  conflict.UpdatedAt,
		ResolvedAt: conflict.ResolvedAt,
		Resolution: string(conflict.Resolution),
	}, nil
}

func (s *Store) Load(ctx context.Context, key string) (domain.AppState, error) {
	state, err := s.GetAppState(ctx, key)
	if err != nil {
		return domain.AppState{}, adaptError(err, false)
	}
	return domain.AppState{
		Key:       state.Key,
		Value:     append(json.RawMessage(nil), state.Value...),
		UpdatedAt: state.UpdatedAt,
	}, nil
}

func (s *Store) Save(ctx context.Context, state domain.AppState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	_, err := s.SetAppState(ctx, AppState{
		Key:       state.Key,
		Value:     append([]byte(nil), state.Value...),
		UpdatedAt: state.UpdatedAt,
	})
	return err
}

func (s *Store) Delete(ctx context.Context, key string) error {
	return adaptError(s.DeleteAppState(ctx, key), false)
}
