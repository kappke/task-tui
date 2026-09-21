package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const operationSelect = `
	SELECT id, provider_id, entity_type, entity_id, operation, payload, attempts,
		status, retryable, error, created_at, last_attempt_at, next_attempt_at, lease_owner,
		lease_expires_at, completed_at
	FROM sync_operations`

func (s *Store) enqueueOperation(ctx context.Context, operation SyncOperation) (SyncOperation, error) {
	operation, err := prepareOperation(operation)
	if err != nil {
		return SyncOperation{}, err
	}
	if err := enqueueExec(ctx, s.db, operation); err != nil {
		return SyncOperation{}, fmt.Errorf("sqlite: enqueue operation %q: %w", operation.ID, err)
	}
	return s.GetOperation(ctx, operation.ProviderID, operation.ID)
}

func (s *Store) enqueueOperationWithResult(ctx context.Context, operation SyncOperation) (SyncOperation, error) {
	return s.enqueueOperation(ctx, operation)
}

func (s *Store) GetOperation(ctx context.Context, providerID, id string) (SyncOperation, error) {
	operation, err := scanOperation(s.db.QueryRowContext(ctx,
		operationSelect+" WHERE provider_id = ? AND id = ?", providerID, id))
	if err != nil {
		return SyncOperation{}, queryError("get sync operation", providerID+"/"+id, err)
	}
	return operation, nil
}

func (s *Store) getOperationByID(ctx context.Context, id string) (SyncOperation, error) {
	operation, err := scanOperation(s.db.QueryRowContext(ctx,
		operationSelect+" WHERE id = ?", id))
	if err != nil {
		return SyncOperation{}, queryError("get sync operation", id, err)
	}
	return operation, nil
}

func (s *Store) GetSyncOperation(ctx context.Context, providerID, id string) (SyncOperation, error) {
	return s.GetOperation(ctx, providerID, id)
}

func (s *Store) ListOperations(ctx context.Context, providerID string) ([]SyncOperation, error) {
	rows, err := s.db.QueryContext(ctx,
		operationSelect+" WHERE provider_id = ? ORDER BY created_at, id", providerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list sync operations for provider %q: %w", providerID, err)
	}
	defer rows.Close()
	operations := make([]SyncOperation, 0)
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan sync operation: %w", err)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list sync operations: %w", err)
	}
	return operations, nil
}

func (s *Store) claimOperation(ctx context.Context, providerID, workerID string, lease time.Duration) (SyncOperation, error) {
	return s.claimOperationAt(ctx, providerID, workerID, lease, time.Now().UTC())
}

func (s *Store) claimNextOperation(ctx context.Context, providerID, workerID string, lease time.Duration) (SyncOperation, error) {
	return s.claimOperation(ctx, providerID, workerID, lease)
}

func (s *Store) claimOperationAt(ctx context.Context, providerID, workerID string, lease time.Duration, now time.Time) (SyncOperation, error) {
	if providerID == "" || workerID == "" {
		return SyncOperation{}, fmt.Errorf("sqlite: claim operation: provider and worker are required")
	}
	if lease <= 0 {
		return SyncOperation{}, fmt.Errorf("sqlite: claim operation: lease must be positive")
	}
	now = now.UTC()
	leaseUntil := now.Add(lease)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncOperation{}, fmt.Errorf("sqlite: begin queue claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.QueryContext(ctx, `
		UPDATE sync_operations AS candidate
		SET status = 'syncing', attempts = attempts + 1, last_attempt_at = ?,
			lease_owner = ?, lease_expires_at = ?, error = NULL, completed_at = NULL
		WHERE candidate.provider_id = ?
		  AND candidate.id = (
			SELECT eligible.id
			FROM sync_operations AS eligible
			WHERE eligible.provider_id = ?
			  AND (
				(eligible.status = 'pending' AND eligible.next_attempt_at <= ?)
				OR (eligible.status = 'failed' AND eligible.retryable = 1 AND eligible.next_attempt_at <= ?)
				OR (eligible.status = 'syncing' AND eligible.lease_expires_at IS NOT NULL AND eligible.lease_expires_at <= ?)
			  )
			  AND NOT EXISTS (
				SELECT 1
				FROM sync_operations AS prior
				WHERE prior.provider_id = eligible.provider_id
				  AND prior.entity_type = eligible.entity_type
				  AND prior.entity_id = eligible.entity_id
				  AND prior.status <> 'completed'
				  AND (prior.status <> 'failed' OR prior.retryable = 1)
				  AND (
					prior.created_at < eligible.created_at
					OR (prior.created_at = eligible.created_at AND prior.id < eligible.id)
				  )
			  )
			ORDER BY eligible.created_at, eligible.id
			LIMIT 1
		  )
		RETURNING id, provider_id, entity_type, entity_id, operation, payload, attempts,
			status, retryable, error, created_at, last_attempt_at, next_attempt_at, lease_owner,
			lease_expires_at, completed_at`,
		formatTime(now),
		workerID,
		formatTime(leaseUntil),
		providerID,
		providerID,
		formatTime(now),
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return SyncOperation{}, fmt.Errorf("sqlite: claim operation for provider %q: %w", providerID, err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return SyncOperation{}, fmt.Errorf("sqlite: claim operation for provider %q: %w", providerID, err)
		}
		_ = rows.Close()
		return SyncOperation{}, fmt.Errorf("%w: claim operation", ErrNotFound)
	}
	operation, err := scanOperation(rows)
	if closeErr := rows.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return SyncOperation{}, fmt.Errorf("sqlite: scan claimed operation: %w", err)
	}
	if err := updateEntitySyncStateTx(ctx, tx, providerID, operation.ID, EntitySyncing); err != nil {
		return SyncOperation{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncOperation{}, fmt.Errorf("sqlite: commit queue claim %q: %w", operation.ID, err)
	}
	committed = true
	return operation, nil
}

func (s *Store) completeOperation(ctx context.Context, providerID, operationID, workerID string) error {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin queue completion: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var entityType, entityID string
	if err := tx.QueryRowContext(ctx,
		"SELECT entity_type, entity_id FROM sync_operations WHERE provider_id = ? AND id = ?", providerID, operationID).
		Scan(&entityType, &entityID); err != nil {
		return queryError("complete sync operation", providerID+"/"+operationID, err)
	}
	query := `
		UPDATE sync_operations
		SET status = 'completed', error = NULL, lease_owner = NULL, lease_expires_at = NULL,
			completed_at = ?, next_attempt_at = ?
		WHERE provider_id = ? AND id = ? AND status = 'syncing'`
	args := []any{formatTime(now), formatTime(now), providerID, operationID}
	if workerID != "" {
		query += " AND lease_owner = ?"
		args = append(args, workerID)
	}
	query += " AND (lease_expires_at IS NULL OR lease_expires_at > ?)"
	args = append(args, formatTime(now))
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite: complete sync operation %q: %w", operationID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: complete sync operation %q rows affected: %w", operationID, err)
	} else if affected != 1 {
		return fmt.Errorf("%w: complete sync operation %q", ErrLeaseLost, operationID)
	}
	if err := setEntitySyncStateTx(ctx, tx, providerID, EntityType(entityType), entityID, SyncStateSynced); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit sync completion %q: %w", operationID, err)
	}
	committed = true
	return nil
}

func (s *Store) completeOperationByProvider(ctx context.Context, providerID, operationID, workerID string) error {
	return s.completeOperation(ctx, providerID, operationID, workerID)
}

func (s *Store) retryOperation(ctx context.Context, providerID, operationID, workerID string, nextAttemptAt time.Time, cause error) error {
	return s.finishRetry(ctx, providerID, operationID, workerID, nextAttemptAt, cause, QueueStatusPending)
}

func (s *Store) retryOperationAt(ctx context.Context, providerID, operationID, workerID string, nextAttemptAt time.Time, cause error) error {
	return s.retryOperation(ctx, providerID, operationID, workerID, nextAttemptAt, cause)
}

func (s *Store) releaseOperation(ctx context.Context, providerID, operationID, workerID string) error {
	return s.finishRetry(ctx, providerID, operationID, workerID, time.Now().UTC(), nil, QueueStatusPending)
}

func (s *Store) failOperation(ctx context.Context, providerID, operationID, workerID string, cause error) error {
	return s.finishRetry(ctx, providerID, operationID, workerID, time.Now().UTC(), cause, QueueStatusFailed)
}

func (s *Store) failOperationByProvider(ctx context.Context, providerID, operationID, workerID string, cause error) error {
	return s.failOperation(ctx, providerID, operationID, workerID, cause)
}

func (s *Store) finishRetry(ctx context.Context, providerID, operationID, workerID string, nextAttemptAt time.Time, cause error, status QueueStatus) error {
	now := time.Now().UTC()
	message := nullableError(cause)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin queue %s: %w", status, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var entityType, entityID string
	if err := tx.QueryRowContext(ctx,
		"SELECT entity_type, entity_id FROM sync_operations WHERE provider_id = ? AND id = ?", providerID, operationID).
		Scan(&entityType, &entityID); err != nil {
		return queryError("retry sync operation", providerID+"/"+operationID, err)
	}
	query := `
		UPDATE sync_operations
		SET status = ?, retryable = ?, error = ?, lease_owner = NULL, lease_expires_at = NULL,
			next_attempt_at = ?, completed_at = NULL
		WHERE provider_id = ? AND id = ? AND status = 'syncing'`
	retryable := 0
	if status == QueueStatusPending {
		retryable = 1
	}
	args := []any{status, retryable, message, formatTime(nextAttemptAt), providerID, operationID}
	if workerID != "" {
		query += " AND lease_owner = ?"
		args = append(args, workerID)
	}
	query += " AND (lease_expires_at IS NULL OR lease_expires_at > ?)"
	args = append(args, formatTime(now))
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite: %s sync operation %q: %w", status, operationID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: %s sync operation %q rows affected: %w", status, operationID, err)
	} else if affected != 1 {
		return fmt.Errorf("%w: %s sync operation %q", ErrLeaseLost, status, operationID)
	}
	state := SyncStateFailed
	if status == QueueStatusPending {
		state = SyncStatePending
	}
	if err := setEntitySyncStateTx(ctx, tx, providerID, EntityType(entityType), entityID, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit queue %s %q: %w", status, operationID, err)
	}
	committed = true
	return nil
}

func (s *Store) requeueStaleOperations(ctx context.Context, providerID string, now time.Time) (int64, error) {
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlite: begin stale queue requeue: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.QueryContext(ctx, `
		SELECT entity_type, entity_id
		FROM sync_operations
		WHERE provider_id = ? AND status = 'syncing'
		  AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?`, providerID, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("sqlite: find stale queue operations: %w", err)
	}
	entities := make([][2]string, 0)
	for rows.Next() {
		var entity [2]string
		if err := rows.Scan(&entity[0], &entity[1]); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("sqlite: scan stale queue operation: %w", err)
		}
		entities = append(entities, entity)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("sqlite: close stale queue rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("sqlite: read stale queue operations: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sync_operations
		SET status = 'pending', error = 'lease expired', lease_owner = NULL,
			lease_expires_at = NULL, next_attempt_at = ?
		WHERE provider_id = ? AND status = 'syncing'
		  AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?`,
		formatTime(now), providerID, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("sqlite: requeue stale operations for provider %q: %w", providerID, err)
	}
	for _, entity := range entities {
		if err := setEntitySyncStateTx(ctx, tx, providerID, EntityType(entity[0]), entity[1], SyncStatePending); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlite: commit stale queue requeue: %w", err)
	}
	committed = true
	return result.RowsAffected()
}

func (s *Store) requeueStale(ctx context.Context, providerID string, now time.Time) (int64, error) {
	return s.requeueStaleOperations(ctx, providerID, now)
}

func (s *Store) pendingOperationCount(ctx context.Context, providerID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM sync_operations
		WHERE provider_id = ? AND status <> 'completed'`, providerID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count pending operations for provider %q: %w", providerID, err)
	}
	return count, nil
}

func (s *Store) pendingCount(ctx context.Context, providerID string) (int, error) {
	return s.pendingOperationCount(ctx, providerID)
}

func enqueueExec(ctx context.Context, exec execer, operation SyncOperation) error {
	prepared, err := prepareOperation(operation)
	if err != nil {
		return err
	}
	operation = prepared
	_, err = exec.ExecContext(ctx, `
		INSERT INTO sync_operations (
			id, provider_id, entity_type, entity_id, operation, payload, attempts, status, retryable,
			error, created_at, last_attempt_at, next_attempt_at, lease_owner,
			lease_expires_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.ID,
		operation.ProviderID,
		operation.EntityType,
		operation.EntityID,
		operation.Operation,
		operation.Payload,
		operation.Attempts,
		operation.Status,
		operation.Retryable,
		nullableError(operationError(operation.Error)),
		formatTime(operation.CreatedAt),
		nullableTime(operation.LastAttemptAt),
		formatTime(operation.NextAttemptAt),
		operation.LeaseOwner,
		nullableTime(operation.LeaseExpiresAt),
		nullableTime(operation.CompletedAt),
	)
	return err
}

func prepareOperation(operation SyncOperation) (SyncOperation, error) {
	if operation.ID == "" {
		id, err := newID()
		if err != nil {
			return SyncOperation{}, fmt.Errorf("sqlite: generate operation id: %w", err)
		}
		operation.ID = id
	}
	if operation.ProviderID == "" || operation.EntityType == "" || operation.EntityID == "" {
		return SyncOperation{}, errors.New("sqlite: sync operation provider, entity type, and entity id are required")
	}
	if operation.Operation == "" {
		return SyncOperation{}, errors.New("sqlite: sync operation type is empty")
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = time.Now().UTC()
	}
	if operation.NextAttemptAt.IsZero() {
		operation.NextAttemptAt = operation.CreatedAt
	}
	if operation.Status == "" {
		operation.Status = QueueStatusPending
	}
	if operation.Status != QueueStatusFailed {
		operation.Retryable = true
	}
	if operation.Attempts < 0 {
		return SyncOperation{}, errors.New("sqlite: sync operation attempts cannot be negative")
	}
	return operation, nil
}

func scanOperation(row rowScanner) (SyncOperation, error) {
	var (
		operation                                 SyncOperation
		entityType, operationType, status         string
		payload                                   []byte
		errorValue, createdAt, lastAttemptAt      sql.NullString
		nextAttemptAt, leaseOwner, leaseExpiresAt sql.NullString
		completedAt                               sql.NullString
	)
	if err := row.Scan(
		&operation.ID, &operation.ProviderID, &entityType, &operation.EntityID,
		&operationType, &payload, &operation.Attempts, &status, &operation.Retryable, &errorValue,
		&createdAt, &lastAttemptAt, &nextAttemptAt, &leaseOwner, &leaseExpiresAt,
		&completedAt,
	); err != nil {
		return SyncOperation{}, err
	}
	operation.EntityType = EntityType(entityType)
	operation.Operation = OperationType(operationType)
	operation.Status = QueueStatus(status)
	operation.Payload = append([]byte(nil), payload...)
	operation.Error = nullableString(errorValue)
	operation.LeaseOwner = nullableString(leaseOwner)
	var err error
	if operation.CreatedAt, err = parseNullableRequiredTime(createdAt); err != nil {
		return SyncOperation{}, err
	}
	if operation.LastAttemptAt, err = scanNullableTime(lastAttemptAt); err != nil {
		return SyncOperation{}, err
	}
	if operation.NextAttemptAt, err = parseNullableRequiredTime(nextAttemptAt); err != nil {
		return SyncOperation{}, err
	}
	if operation.LeaseExpiresAt, err = scanNullableTime(leaseExpiresAt); err != nil {
		return SyncOperation{}, err
	}
	if operation.CompletedAt, err = scanNullableTime(completedAt); err != nil {
		return SyncOperation{}, err
	}
	return operation, nil
}

func operationError(value *string) error {
	if value == nil {
		return nil
	}
	return errors.New(*value)
}

func nullableError(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

const (
	EntitySyncing = SyncState("syncing")
)

func updateEntitySyncStateTx(ctx context.Context, tx *sql.Tx, providerID, operationID string, state SyncState) error {
	var entityType, entityID string
	if err := tx.QueryRowContext(ctx,
		"SELECT entity_type, entity_id FROM sync_operations WHERE provider_id = ? AND id = ?",
		providerID, operationID).Scan(&entityType, &entityID); err != nil {
		return fmt.Errorf("sqlite: load operation entity %q: %w", operationID, err)
	}
	return setEntitySyncStateTx(ctx, tx, providerID, EntityType(entityType), entityID, state)
}

func setEntitySyncStateTx(ctx context.Context, tx *sql.Tx, providerID string, entityType EntityType, entityID string, state SyncState) error {
	var query string
	switch entityType {
	case EntityProvider:
		query = "UPDATE providers SET sync_state = ?, updated_at = ? WHERE id = ?"
	case EntitySpace:
		query = "UPDATE spaces SET sync_state = ?, updated_at = ? WHERE provider_id = ? AND id = ?"
	case EntityList:
		query = "UPDATE lists SET sync_state = ?, updated_at = ? WHERE provider_id = ? AND id = ?"
	case EntityTask:
		query = "UPDATE tasks SET sync_state = ?, updated_at = ? WHERE provider_id = ? AND id = ?"
	default:
		return fmt.Errorf("sqlite: unsupported sync entity type %q", entityType)
	}
	updatedAt := formatTime(time.Now())
	var (
		result sql.Result
		err    error
	)
	if entityType == EntityProvider {
		result, err = tx.ExecContext(ctx, query, state, updatedAt, entityID)
	} else {
		result, err = tx.ExecContext(ctx, query, state, updatedAt, providerID, entityID)
	}
	if err != nil {
		return fmt.Errorf("sqlite: update %s sync state %q: %w", entityType, entityID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: update %s sync state %q rows affected: %w", entityType, entityID, err)
	} else if affected == 0 {
		return fmt.Errorf("%w: sync entity %s/%s", ErrNotFound, entityType, entityID)
	}
	return nil
}
