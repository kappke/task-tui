package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrNotFound is returned when a requested local entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrProviderMismatch means an operation attempted to connect entities from
	// different provider instances.
	ErrProviderMismatch = errors.New("provider mismatch")
	// ErrInvalidEntity means required identity or hierarchy fields are invalid.
	ErrInvalidEntity = errors.New("invalid entity")
	// ErrQueueEmpty means no eligible operation is currently available.
	ErrQueueEmpty = errors.New("sync queue is empty")
)

// DataStore is the portion of persistence needed by the lifecycle runtime.
// Keeping this interface small also makes startup and shutdown ordering
// directly testable without a real database.
type DataStore interface {
	Snapshot(context.Context) (View, error)
	LoadUIState(context.Context) (UIState, error)
	SaveUIState(context.Context, UIState) error
	Close() error
}

// Repository is the SQLite-backed local repository and durable sync queue.
type Repository struct {
	db  *sql.DB
	now func() time.Time
}

// NewRepository constructs a repository over an already migrated database.
func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("new repository: nil database")
	}
	return &Repository{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

// DB exposes the underlying connection for diagnostics and migration tests.
// Callers must not close it before Repository.Close.
func (r *Repository) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// Close closes the underlying database.
func (r *Repository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// EnsureProvider inserts or updates a provider record without storing secrets.
func (r *Repository) EnsureProvider(ctx context.Context, provider ProviderRecord) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(string(provider.ID)) == "" || strings.TrimSpace(string(provider.Type)) == "" || strings.TrimSpace(provider.Name) == "" {
		return fmt.Errorf("ensure provider: %w", ErrInvalidEntity)
	}
	now := r.now()
	created := provider.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := provider.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO providers(id, type, name, enabled, configuration, last_sync_at, sync_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
 type = excluded.type,
 name = excluded.name,
 enabled = excluded.enabled,
 configuration = excluded.configuration,
 updated_at = excluded.updated_at`,
		string(provider.ID), string(provider.Type), provider.Name, boolInt(provider.Enabled), provider.Configuration,
		nullableTime(provider.LastSyncAt), provider.SyncError, formatTime(created), formatTime(updated))
	if err != nil {
		return fmt.Errorf("ensure provider %s: %w", provider.ID, err)
	}
	return nil
}

// Provider returns a provider record by instance ID.
func (r *Repository) Provider(ctx context.Context, id ProviderID) (ProviderRecord, error) {
	if err := r.check(ctx); err != nil {
		return ProviderRecord{}, err
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, type, name, enabled, configuration, last_sync_at, sync_error, created_at, updated_at FROM providers WHERE id = ?`, string(id))
	provider, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderRecord{}, fmt.Errorf("provider %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return ProviderRecord{}, fmt.Errorf("get provider %s: %w", id, err)
	}
	return provider, nil
}

// ListProviders returns providers in stable display order.
func (r *Repository) ListProviders(ctx context.Context) ([]ProviderRecord, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, type, name, enabled, configuration, last_sync_at, sync_error, created_at, updated_at FROM providers ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	var providers []ProviderRecord
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate providers: %w", err)
	}
	return providers, nil
}

// Snapshot loads the local cached data used for the initial render.
func (r *Repository) Snapshot(ctx context.Context) (View, error) {
	if err := r.check(ctx); err != nil {
		return View{}, err
	}
	providers, err := r.ListProviders(ctx)
	if err != nil {
		return View{}, err
	}
	spaces, err := r.listSpaces(ctx)
	if err != nil {
		return View{}, err
	}
	lists, err := r.listLists(ctx)
	if err != nil {
		return View{}, err
	}
	tasks, err := r.listAllTasks(ctx)
	if err != nil {
		return View{}, err
	}
	view := View{Providers: providers, Spaces: spaces, Lists: lists, Tasks: tasks}
	view.SyncErrors = make(map[ProviderID]string)
	for _, provider := range providers {
		if provider.SyncError != "" {
			view.SyncErrors[provider.ID] = provider.SyncError
		}
	}
	return view, nil
}

// LoadUIState loads the single persisted UI state record.
func (r *Repository) LoadUIState(ctx context.Context) (UIState, error) {
	if err := r.check(ctx); err != nil {
		return UIState{}, err
	}
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT value FROM ui_state WHERE key = 'main'`).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return UIState{}, nil
	}
	if err != nil {
		return UIState{}, fmt.Errorf("load UI state: %w", err)
	}
	var state UIState
	if err := json.Unmarshal(payload, &state); err != nil {
		return UIState{}, fmt.Errorf("decode UI state: %w", err)
	}
	return state, nil
}

// SaveUIState atomically persists presentation state without changing domain
// entities or queue rows.
func (r *Repository) SaveUIState(ctx context.Context, state UIState) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode UI state: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO ui_state(key, value, updated_at) VALUES ('main', ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, payload, formatTime(r.now()))
	if err != nil {
		return fmt.Errorf("save UI state: %w", err)
	}
	return nil
}

// CreateSpace writes a space after validating its provider.
func (r *Repository) CreateSpace(ctx context.Context, space Space) error {
	return r.putSpace(ctx, space, nil)
}

// CreateList writes a list after validating its parent provider.
func (r *Repository) CreateList(ctx context.Context, list List) error {
	return r.putList(ctx, list, nil)
}

// CreateTask writes a task after validating its parent provider.
func (r *Repository) CreateTask(ctx context.Context, task Task) error {
	return r.putTask(ctx, task, nil)
}

// UpdateTask updates a task without creating a remote operation. Use
// MutateTask when the change must be synchronized.
func (r *Repository) UpdateTask(ctx context.Context, task Task) error {
	return r.putTask(ctx, task, nil)
}

// UpsertRemoteSpace stores provider data without enqueueing a local mutation.
func (r *Repository) UpsertRemoteSpace(ctx context.Context, space Space) error {
	return r.putSpace(ctx, space, nil)
}

// UpsertRemoteList stores provider data without enqueueing a local mutation.
func (r *Repository) UpsertRemoteList(ctx context.Context, list List) error {
	return r.putList(ctx, list, nil)
}

// UpsertRemoteTask stores provider data without enqueueing a local mutation.
func (r *Repository) UpsertRemoteTask(ctx context.Context, task Task) error {
	return r.putTask(ctx, task, nil)
}

// DeleteTask deletes a task only when it belongs to providerID.
func (r *Repository) DeleteTask(ctx context.Context, providerID ProviderID, taskID TaskID) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ? AND provider_id = ?`, string(taskID), string(providerID))
	if err != nil {
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("delete task %s rows affected: %w", taskID, err)
	} else if count == 0 {
		return fmt.Errorf("delete task %s: %w", taskID, ErrNotFound)
	}
	return nil
}

// MutateTask persists a task and its optional sync operation in one transaction.
// This is the central local-first write invariant.
func (r *Repository) MutateTask(ctx context.Context, task Task, operation *SyncOperation) error {
	return r.putTask(ctx, task, operation)
}

// DeleteTaskAndEnqueue deletes a task and records the remote delete atomically.
func (r *Repository) DeleteTaskAndEnqueue(ctx context.Context, providerID ProviderID, taskID TaskID, operation *SyncOperation) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete task: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ? AND provider_id = ?`, string(taskID), string(providerID))
	if err != nil {
		rollback(tx, err)
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}
	if count, err := result.RowsAffected(); err != nil {
		rollback(tx, err)
		return fmt.Errorf("delete task %s rows affected: %w", taskID, err)
	} else if count == 0 {
		rollback(tx, ErrNotFound)
		return fmt.Errorf("delete task %s: %w", taskID, ErrNotFound)
	}
	if operation != nil {
		if err := insertOperation(ctx, tx, *operation); err != nil {
			rollback(tx, err)
			return fmt.Errorf("queue delete task %s: %w", taskID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete task %s: %w", taskID, err)
	}
	return nil
}

// GetTask returns a task scoped by provider and ID.
func (r *Repository) GetTask(ctx context.Context, providerID ProviderID, taskID TaskID) (Task, error) {
	if err := r.check(ctx); err != nil {
		return Task{}, err
	}
	row := r.db.QueryRowContext(ctx, taskSelect+` WHERE id = ? AND provider_id = ?`, string(taskID), string(providerID))
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", taskID, err)
	}
	return task, nil
}

// ListTasks returns tasks in one list, scoped by provider.
func (r *Repository) ListTasks(ctx context.Context, providerID ProviderID, listID ListID) ([]Task, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	return r.listTasks(ctx, ` WHERE provider_id = ? AND list_id = ? ORDER BY created_at, id`, string(providerID), string(listID))
}

// SearchTasks searches only local cached fields and never calls a provider.
func (r *Repository) SearchTasks(ctx context.Context, query string) ([]Task, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search tasks: %w", ErrInvalidEntity)
	}
	pattern := "%" + query + "%"
	return r.listTasks(ctx, `
 JOIN lists ON lists.id = tasks.list_id AND lists.provider_id = tasks.provider_id
 JOIN spaces ON spaces.id = lists.space_id AND spaces.provider_id = lists.provider_id
 WHERE tasks.title LIKE ? COLLATE NOCASE
    OR tasks.description LIKE ? COLLATE NOCASE
    OR lists.name LIKE ? COLLATE NOCASE
    OR spaces.name LIKE ? COLLATE NOCASE
 ORDER BY tasks.updated_at DESC, tasks.id`, pattern, pattern, pattern, pattern)
}

// Enqueue stores an operation durably. MutateTask is preferred when changing a
// task because it combines both writes in one transaction.
func (r *Repository) Enqueue(ctx context.Context, operation SyncOperation) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if operation.ID == "" {
		operation.ID = OperationID(newID("op"))
	}
	if operation.Status == "" {
		operation.Status = OperationPending
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = r.now()
	}
	if err := validateOperation(operation); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `
INSERT INTO sync_operations(id, provider_id, entity_type, entity_id, operation, payload, attempts, status, error, created_at, last_attempt_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(operation.ID), string(operation.ProviderID), string(operation.EntityType), operation.EntityID, string(operation.Operation), operation.Payload,
		operation.Attempts, string(operation.Status), operation.Error, formatTime(operation.CreatedAt), nullableTime(operation.LastAttemptAt)); err != nil {
		return fmt.Errorf("enqueue operation %s: %w", operation.ID, err)
	}
	return nil
}

// ClaimNext claims the oldest eligible operation for one provider. Only the
// worker owning providerID can claim its operations.
func (r *Repository) ClaimNext(ctx context.Context, providerID ProviderID, now time.Time, retryDelay func(int) time.Duration) (SyncOperation, error) {
	if err := r.check(ctx); err != nil {
		return SyncOperation{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncOperation{}, fmt.Errorf("begin claim operation: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id, provider_id, entity_type, entity_id, operation, payload, attempts, status, error, created_at, last_attempt_at
FROM sync_operations
WHERE provider_id = ? AND status IN ('pending', 'failed')
ORDER BY created_at, id`, string(providerID))
	if err != nil {
		rollback(tx, err)
		return SyncOperation{}, fmt.Errorf("query operation queue for %s: %w", providerID, err)
	}
	var selected SyncOperation
	for rows.Next() {
		candidate, scanErr := scanOperation(rows)
		if scanErr != nil {
			rows.Close()
			rollback(tx, scanErr)
			return SyncOperation{}, fmt.Errorf("scan operation: %w", scanErr)
		}
		if candidate.Status == OperationFailed {
			if retryDelay == nil {
				continue
			}
			if candidate.LastAttemptAt != nil && now.Sub(*candidate.LastAttemptAt) < retryDelay(candidate.Attempts) {
				continue
			}
		}
		selected = candidate
		break
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		rollback(tx, rowsErr)
		return SyncOperation{}, fmt.Errorf("iterate operation queue: %w", rowsErr)
	}
	if selected.ID == "" {
		rollback(tx, nil)
		return SyncOperation{}, ErrQueueEmpty
	}
	attempts := selected.Attempts + 1
	lastAttempt := now.UTC()
	result, err := tx.ExecContext(ctx, `
UPDATE sync_operations SET status = 'syncing', attempts = ?, last_attempt_at = ?, error = ''
WHERE id = ? AND provider_id = ? AND status IN ('pending', 'failed')`, attempts, formatTime(lastAttempt), string(selected.ID), string(providerID))
	if err != nil {
		rollback(tx, err)
		return SyncOperation{}, fmt.Errorf("claim operation %s: %w", selected.ID, err)
	}
	if count, err := result.RowsAffected(); err != nil {
		rollback(tx, err)
		return SyncOperation{}, fmt.Errorf("claim operation %s rows affected: %w", selected.ID, err)
	} else if count != 1 {
		rollback(tx, errors.New("operation was claimed by another worker"))
		return SyncOperation{}, fmt.Errorf("claim operation %s: queue changed", selected.ID)
	}
	if err := tx.Commit(); err != nil {
		return SyncOperation{}, fmt.Errorf("commit claim operation %s: %w", selected.ID, err)
	}
	selected.Status = OperationSyncing
	selected.Attempts = attempts
	selected.LastAttemptAt = &lastAttempt
	selected.Error = ""
	return selected, nil
}

// Complete marks an operation complete after its provider call succeeds.
func (r *Repository) Complete(ctx context.Context, operationID OperationID, providerID ProviderID) error {
	return r.updateOperationStatus(ctx, operationID, providerID, OperationCompleted, "")
}

// Fail records a provider error while retaining the operation for retry.
func (r *Repository) Fail(ctx context.Context, operationID OperationID, providerID ProviderID, providerErr error) error {
	message := SafeErrorText(providerErr)
	return r.updateOperationStatus(ctx, operationID, providerID, OperationFailed, message)
}

// Release returns an in-flight operation to pending when shutdown interrupts
// its provider call. Attempts and payload remain durable.
func (r *Repository) Release(ctx context.Context, operationID OperationID, providerID ProviderID) error {
	return r.updateOperationStatus(ctx, operationID, providerID, OperationPending, "")
}

// Operations returns durable queue rows for diagnostics and restart tests.
func (r *Repository) Operations(ctx context.Context, providerID ProviderID) ([]SyncOperation, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id, provider_id, entity_type, entity_id, operation, payload, attempts, status, error, created_at, last_attempt_at
FROM sync_operations WHERE provider_id = ? ORDER BY created_at, id`, string(providerID))
	if err != nil {
		return nil, fmt.Errorf("list operations for %s: %w", providerID, err)
	}
	defer rows.Close()
	var operations []SyncOperation
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan operation: %w", err)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operations: %w", err)
	}
	return operations, nil
}

// RecoverInFlight makes operations interrupted by a process crash eligible on
// the next launch. The payload and attempt count are retained.
func (r *Repository) RecoverInFlight(ctx context.Context) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE sync_operations SET status = 'pending', error = 'interrupted by previous shutdown' WHERE status = 'syncing'`); err != nil {
		return fmt.Errorf("recover in-flight operations: %w", err)
	}
	return nil
}

// SetProviderSyncState records provider-specific sync status without touching
// another provider's state.
func (r *Repository) SetProviderSyncState(ctx context.Context, providerID ProviderID, syncedAt *time.Time, syncErr error) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	message := ""
	if syncErr != nil {
		message = SafeErrorText(syncErr)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE providers SET last_sync_at = ?, sync_error = ?, updated_at = ? WHERE id = ?`, nullableTime(syncedAt), message, formatTime(r.now()), string(providerID))
	if err != nil {
		return fmt.Errorf("set sync state for %s: %w", providerID, err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("set sync state for %s rows affected: %w", providerID, err)
	} else if count == 0 {
		return fmt.Errorf("set sync state for %s: %w", providerID, ErrNotFound)
	}
	return nil
}

func (r *Repository) putSpace(ctx context.Context, space Space, operation *SyncOperation) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(string(space.ID)) == "" || strings.TrimSpace(string(space.ProviderID)) == "" || strings.TrimSpace(space.Name) == "" {
		return fmt.Errorf("put space: %w", ErrInvalidEntity)
	}
	if err := r.verifyExistingProvider(ctx, "spaces", space.ID, space.ProviderID); err != nil {
		return fmt.Errorf("put space %s: %w", space.ID, err)
	}
	now := r.now()
	created := space.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := space.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	if space.SyncState == "" {
		space.SyncState = SyncStateLocal
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin put space: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO spaces(id, provider_id, remote_id, name, sync_state, remote_updated_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
 provider_id = excluded.provider_id, remote_id = excluded.remote_id, name = excluded.name,
 sync_state = excluded.sync_state, remote_updated_at = excluded.remote_updated_at, updated_at = excluded.updated_at`,
		string(space.ID), string(space.ProviderID), nullableString(space.RemoteID), space.Name, string(space.SyncState), nullableTime(space.RemoteUpdatedAt), formatTime(created), formatTime(updated))
	if err != nil {
		rollback(tx, err)
		return fmt.Errorf("put space %s: %w", space.ID, err)
	}
	if operation != nil {
		if err := insertOperation(ctx, tx, *operation); err != nil {
			rollback(tx, err)
			return fmt.Errorf("queue space %s: %w", space.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit space %s: %w", space.ID, err)
	}
	return nil
}

func (r *Repository) putList(ctx context.Context, list List, operation *SyncOperation) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(string(list.ID)) == "" || strings.TrimSpace(string(list.ProviderID)) == "" || strings.TrimSpace(string(list.SpaceID)) == "" || strings.TrimSpace(list.Name) == "" {
		return fmt.Errorf("put list: %w", ErrInvalidEntity)
	}
	if err := r.verifyExistingProvider(ctx, "lists", list.ID, list.ProviderID); err != nil {
		return fmt.Errorf("put list %s: %w", list.ID, err)
	}
	if err := r.verifyParentProvider(ctx, "spaces", list.SpaceID, list.ProviderID); err != nil {
		return fmt.Errorf("put list %s: %w", list.ID, err)
	}
	now := r.now()
	created := list.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := list.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	if list.SyncState == "" {
		list.SyncState = SyncStateLocal
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin put list: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO lists(id, provider_id, space_id, remote_id, name, sync_state, remote_updated_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
 provider_id = excluded.provider_id, space_id = excluded.space_id, remote_id = excluded.remote_id, name = excluded.name,
 sync_state = excluded.sync_state, remote_updated_at = excluded.remote_updated_at, updated_at = excluded.updated_at`,
		string(list.ID), string(list.ProviderID), string(list.SpaceID), nullableString(list.RemoteID), list.Name, string(list.SyncState), nullableTime(list.RemoteUpdatedAt), formatTime(created), formatTime(updated))
	if err != nil {
		rollback(tx, err)
		return fmt.Errorf("put list %s: %w", list.ID, err)
	}
	if operation != nil {
		if err := insertOperation(ctx, tx, *operation); err != nil {
			rollback(tx, err)
			return fmt.Errorf("queue list %s: %w", list.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit list %s: %w", list.ID, err)
	}
	return nil
}

func (r *Repository) putTask(ctx context.Context, task Task, operation *SyncOperation) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(string(task.ID)) == "" || strings.TrimSpace(string(task.ProviderID)) == "" || strings.TrimSpace(string(task.ListID)) == "" || strings.TrimSpace(task.Title) == "" {
		return fmt.Errorf("put task: %w", ErrInvalidEntity)
	}
	if err := r.verifyExistingProvider(ctx, "tasks", task.ID, task.ProviderID); err != nil {
		return fmt.Errorf("put task %s: %w", task.ID, err)
	}
	if operation != nil && (operation.ProviderID != task.ProviderID || operation.EntityType != EntityTypeTask || operation.EntityID != string(task.ID)) {
		return fmt.Errorf("put task %s: %w", task.ID, ErrProviderMismatch)
	}
	if err := r.verifyParentProvider(ctx, "lists", task.ListID, task.ProviderID); err != nil {
		return fmt.Errorf("put task %s: %w", task.ID, err)
	}
	if task.ParentTaskID != nil {
		if err := r.verifyParentProvider(ctx, "tasks", *task.ParentTaskID, task.ProviderID); err != nil {
			return fmt.Errorf("put task %s parent: %w", task.ID, err)
		}
		if *task.ParentTaskID == task.ID {
			return fmt.Errorf("put task %s: %w", task.ID, ErrInvalidEntity)
		}
	}
	now := r.now()
	created := task.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := task.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	if task.SyncState == "" {
		task.SyncState = SyncStateLocal
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin put task: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO tasks(id, provider_id, list_id, remote_id, parent_task_id, title, description, status, priority, due_at, completed_at, sync_state, remote_updated_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
 provider_id = excluded.provider_id, list_id = excluded.list_id, remote_id = excluded.remote_id,
 parent_task_id = excluded.parent_task_id, title = excluded.title, description = excluded.description,
 status = excluded.status, priority = excluded.priority, due_at = excluded.due_at, completed_at = excluded.completed_at,
 sync_state = excluded.sync_state, remote_updated_at = excluded.remote_updated_at, updated_at = excluded.updated_at`,
		string(task.ID), string(task.ProviderID), string(task.ListID), nullableString(task.RemoteID), nullableTaskID(task.ParentTaskID), task.Title, task.Description, task.Status, task.Priority,
		nullableTime(task.DueAt), nullableTime(task.CompletedAt), string(task.SyncState), nullableTime(task.RemoteUpdatedAt), formatTime(created), formatTime(updated))
	if err != nil {
		rollback(tx, err)
		return fmt.Errorf("put task %s: %w", task.ID, err)
	}
	if operation != nil {
		if err := insertOperation(ctx, tx, *operation); err != nil {
			rollback(tx, err)
			return fmt.Errorf("queue task %s: %w", task.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task %s: %w", task.ID, err)
	}
	return nil
}

func (r *Repository) verifyParentProvider(ctx context.Context, table string, id interface{}, providerID ProviderID) error {
	if table != "spaces" && table != "lists" && table != "tasks" {
		return fmt.Errorf("verify parent: %w", ErrInvalidEntity)
	}
	row := r.db.QueryRowContext(ctx, "SELECT provider_id FROM "+table+" WHERE id = ?", idString(id))
	var actual string
	if err := row.Scan(&actual); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if actual != string(providerID) {
		return ErrProviderMismatch
	}
	return nil
}

func (r *Repository) verifyExistingProvider(ctx context.Context, table string, id interface{}, providerID ProviderID) error {
	if table != "spaces" && table != "lists" && table != "tasks" {
		return fmt.Errorf("verify existing entity: %w", ErrInvalidEntity)
	}
	row := r.db.QueryRowContext(ctx, "SELECT provider_id FROM "+table+" WHERE id = ?", idString(id))
	var actual string
	if err := row.Scan(&actual); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if actual != string(providerID) {
		return ErrProviderMismatch
	}
	return nil
}

func (r *Repository) listSpaces(ctx context.Context) ([]Space, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, provider_id, remote_id, name, sync_state, remote_updated_at, created_at, updated_at FROM spaces ORDER BY provider_id, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}
	defer rows.Close()
	var spaces []Space
	for rows.Next() {
		space, err := scanSpace(rows)
		if err != nil {
			return nil, fmt.Errorf("scan space: %w", err)
		}
		spaces = append(spaces, space)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate spaces: %w", err)
	}
	return spaces, nil
}

func (r *Repository) listLists(ctx context.Context) ([]List, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, provider_id, space_id, remote_id, name, sync_state, remote_updated_at, created_at, updated_at FROM lists ORDER BY provider_id, space_id, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list lists: %w", err)
	}
	defer rows.Close()
	var lists []List
	for rows.Next() {
		list, err := scanList(rows)
		if err != nil {
			return nil, fmt.Errorf("scan list: %w", err)
		}
		lists = append(lists, list)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lists: %w", err)
	}
	return lists, nil
}

func (r *Repository) listAllTasks(ctx context.Context) ([]Task, error) {
	return r.listTasks(ctx, ` ORDER BY provider_id, list_id, created_at, id`)
}

const taskSelect = `SELECT id, provider_id, list_id, remote_id, parent_task_id, title, description, status, priority, due_at, completed_at, sync_state, remote_updated_at, created_at, updated_at FROM tasks`

func (r *Repository) listTasks(ctx context.Context, suffix string, args ...any) ([]Task, error) {
	rows, err := r.db.QueryContext(ctx, taskSelect+suffix, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	return tasks, nil
}

func (r *Repository) updateOperationStatus(ctx context.Context, operationID OperationID, providerID ProviderID, status OperationStatus, message string) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE sync_operations SET status = ?, error = ? WHERE id = ? AND provider_id = ?`, string(status), message, string(operationID), string(providerID))
	if err != nil {
		return fmt.Errorf("update operation %s: %w", operationID, err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("update operation %s rows affected: %w", operationID, err)
	} else if count == 0 {
		return fmt.Errorf("update operation %s: %w", operationID, ErrNotFound)
	}
	return nil
}

func insertOperation(ctx context.Context, tx *sql.Tx, operation SyncOperation) error {
	if operation.ID == "" {
		operation.ID = OperationID(newID("op"))
	}
	if operation.Status == "" {
		operation.Status = OperationPending
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = time.Now().UTC()
	}
	if err := validateOperation(operation); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO sync_operations(id, provider_id, entity_type, entity_id, operation, payload, attempts, status, error, created_at, last_attempt_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(operation.ID), string(operation.ProviderID), string(operation.EntityType), operation.EntityID, string(operation.Operation), operation.Payload,
		operation.Attempts, string(operation.Status), operation.Error, formatTime(operation.CreatedAt), nullableTime(operation.LastAttemptAt))
	return err
}

func validateOperation(operation SyncOperation) error {
	if strings.TrimSpace(string(operation.ProviderID)) == "" || strings.TrimSpace(operation.EntityID) == "" || len(operation.Payload) == 0 {
		return fmt.Errorf("enqueue operation: %w", ErrInvalidEntity)
	}
	switch operation.EntityType {
	case EntityTypeSpace, EntityTypeList, EntityTypeTask:
	default:
		return fmt.Errorf("enqueue operation: unknown entity type %q", operation.EntityType)
	}
	switch operation.Operation {
	case OperationCreate, OperationUpdate, OperationDelete:
	default:
		return fmt.Errorf("enqueue operation: unknown operation %q", operation.Operation)
	}
	switch operation.Status {
	case OperationPending, OperationSyncing, OperationFailed, OperationCompleted:
	default:
		return fmt.Errorf("enqueue operation: unknown status %q", operation.Status)
	}
	return nil
}

func (r *Repository) check(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("repository is closed")
	}
	if ctx == nil {
		return errors.New("repository: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func rollback(tx *sql.Tx, original error) {
	if tx == nil {
		return
	}
	_ = tx.Rollback()
	_ = original
}

type scanner interface {
	Scan(...any) error
}

func scanProvider(row scanner) (ProviderRecord, error) {
	var provider ProviderRecord
	var id, typ, lastSync, created, updated sql.NullString
	var enabled int
	if err := row.Scan(&id, &typ, &provider.Name, &enabled, &provider.Configuration, &lastSync, &provider.SyncError, &created, &updated); err != nil {
		return ProviderRecord{}, err
	}
	provider.ID = ProviderID(id.String)
	provider.Type = ProviderType(typ.String)
	provider.Enabled = enabled != 0
	var err error
	if provider.LastSyncAt, err = parseTime(lastSync); err != nil {
		return ProviderRecord{}, err
	}
	createdAt, err := parseTime(created)
	if err != nil || createdAt == nil {
		return ProviderRecord{}, err
	}
	provider.CreatedAt = *createdAt
	updatedAt, err := parseTime(updated)
	if err != nil || updatedAt == nil {
		return ProviderRecord{}, err
	}
	provider.UpdatedAt = *updatedAt
	return provider, nil
}

func scanSpace(row scanner) (Space, error) {
	var space Space
	var id, providerID, remoteID, state, remoteUpdated, created, updated sql.NullString
	if err := row.Scan(&id, &providerID, &remoteID, &space.Name, &state, &remoteUpdated, &created, &updated); err != nil {
		return Space{}, err
	}
	space.ID = SpaceID(id.String)
	space.ProviderID = ProviderID(providerID.String)
	space.RemoteID = nullStringPtr(remoteID)
	space.SyncState = SyncState(state.String)
	var err error
	if space.RemoteUpdatedAt, err = parseTime(remoteUpdated); err != nil {
		return Space{}, err
	}
	createdAt, err := parseTime(created)
	if err != nil || createdAt == nil {
		return Space{}, err
	}
	space.CreatedAt = *createdAt
	updatedAt, err := parseTime(updated)
	if err != nil || updatedAt == nil {
		return Space{}, err
	}
	space.UpdatedAt = *updatedAt
	return space, nil
}

func scanList(row scanner) (List, error) {
	var list List
	var id, providerID, spaceID, remoteID, state, remoteUpdated, created, updated sql.NullString
	if err := row.Scan(&id, &providerID, &spaceID, &remoteID, &list.Name, &state, &remoteUpdated, &created, &updated); err != nil {
		return List{}, err
	}
	list.ID = ListID(id.String)
	list.ProviderID = ProviderID(providerID.String)
	list.SpaceID = SpaceID(spaceID.String)
	list.RemoteID = nullStringPtr(remoteID)
	list.SyncState = SyncState(state.String)
	var err error
	if list.RemoteUpdatedAt, err = parseTime(remoteUpdated); err != nil {
		return List{}, err
	}
	createdAt, err := parseTime(created)
	if err != nil || createdAt == nil {
		return List{}, err
	}
	list.CreatedAt = *createdAt
	updatedAt, err := parseTime(updated)
	if err != nil || updatedAt == nil {
		return List{}, err
	}
	list.UpdatedAt = *updatedAt
	return list, nil
}

func scanTask(row scanner) (Task, error) {
	var task Task
	var id, providerID, listID, remoteID, parentID, dueAt, completedAt, state, remoteUpdated, created, updated sql.NullString
	if err := row.Scan(&id, &providerID, &listID, &remoteID, &parentID, &task.Title, &task.Description, &task.Status, &task.Priority, &dueAt, &completedAt, &state, &remoteUpdated, &created, &updated); err != nil {
		return Task{}, err
	}
	task.ID = TaskID(id.String)
	task.ProviderID = ProviderID(providerID.String)
	task.ListID = ListID(listID.String)
	task.RemoteID = nullStringPtr(remoteID)
	if parentID.Valid && parentID.String != "" {
		parent := TaskID(parentID.String)
		task.ParentTaskID = &parent
	}
	task.SyncState = SyncState(state.String)
	var err error
	if task.DueAt, err = parseTime(dueAt); err != nil {
		return Task{}, err
	}
	if task.CompletedAt, err = parseTime(completedAt); err != nil {
		return Task{}, err
	}
	if task.RemoteUpdatedAt, err = parseTime(remoteUpdated); err != nil {
		return Task{}, err
	}
	createdAt, err := parseTime(created)
	if err != nil || createdAt == nil {
		return Task{}, err
	}
	task.CreatedAt = *createdAt
	updatedAt, err := parseTime(updated)
	if err != nil || updatedAt == nil {
		return Task{}, err
	}
	task.UpdatedAt = *updatedAt
	return task, nil
}

func scanOperation(row scanner) (SyncOperation, error) {
	var operation SyncOperation
	var id, providerID, entityType, entityID, op, status, created, lastAttempt sql.NullString
	if err := row.Scan(&id, &providerID, &entityType, &entityID, &op, &operation.Payload, &operation.Attempts, &status, &operation.Error, &created, &lastAttempt); err != nil {
		return SyncOperation{}, err
	}
	operation.ID = OperationID(id.String)
	operation.ProviderID = ProviderID(providerID.String)
	operation.EntityType = EntityType(entityType.String)
	operation.EntityID = entityID.String
	operation.Operation = OperationType(op.String)
	operation.Status = OperationStatus(status.String)
	var err error
	createdAt, err := parseTime(created)
	if err != nil || createdAt == nil {
		return SyncOperation{}, err
	}
	operation.CreatedAt = *createdAt
	if operation.LastAttemptAt, err = parseTime(lastAttempt); err != nil {
		return SyncOperation{}, err
	}
	return operation, nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	result := value.String
	return &result
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
