package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/repository"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasktui.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func testProvider(id string) domain.Provider {
	return domain.Provider{
		ID:      domain.ProviderID(id),
		Type:    domain.ProviderTypeLocal,
		Name:    id,
		Enabled: true,
	}
}

func createProvider(t *testing.T, store *Store, id string) domain.Provider {
	t.Helper()
	provider, err := store.CreateProvider(context.Background(), testProvider(id))
	if err != nil {
		t.Fatalf("create provider %s: %v", id, err)
	}
	return provider
}

type hierarchy struct {
	provider domain.Provider
	space    domain.Space
	list     domain.List
	task     domain.Task
}

func createHierarchy(t *testing.T, store *Store, providerID string) hierarchy {
	t.Helper()
	provider := createProvider(t, store, providerID)
	space, err := store.CreateSpace(context.Background(), domain.Space{
		ID:         domain.SpaceID(providerID + "-space"),
		ProviderID: provider.ID,
		Name:       providerID + " space",
		SyncState:  domain.SyncStateLocal,
	})
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	list, err := store.CreateList(context.Background(), domain.List{
		ID:         domain.ListID(providerID + "-list"),
		ProviderID: provider.ID,
		SpaceID:    space.ID,
		Name:       providerID + " list",
		SyncState:  domain.SyncStateLocal,
	})
	if err != nil {
		t.Fatalf("create list: %v", err)
	}
	task, err := store.CreateTask(context.Background(), domain.Task{
		ID:         domain.TaskID(providerID + "-task"),
		ProviderID: provider.ID,
		ListID:     list.ID,
		Title:      providerID + " task",
		Status:     "todo",
		Priority:   domain.PriorityNormal,
		SyncState:  domain.SyncStateLocal,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return hierarchy{provider: provider, space: space, list: list, task: task}
}

func TestOpenRunsInitialMigrationAndConfiguresSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	var foreignKeys int
	if err := store.SQLDB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var journalMode string
	if err := store.SQLDB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode pragma: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var busyTimeout int
	if err := store.SQLDB().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout pragma: %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("busy_timeout = %d, want at least 5000", busyTimeout)
	}

	for _, table := range []string{
		"providers", "spaces", "lists", "tasks", "provider_metadata", "sync_operations",
		"sync_bases", "conflicts", "app_state", "schema_migrations",
	} {
		var exists int
		if err := store.SQLDB().QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if exists != 1 {
			t.Fatalf("table %s is missing", table)
		}
	}
	var migrationCount int
	if err := store.SQLDB().QueryRow("SELECT count(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("migration count: %v", err)
	}
	if migrationCount != 6 {
		t.Fatalf("migration count = %d, want 6", migrationCount)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if err := reopened.SQLDB().QueryRow("SELECT count(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("reopen migration count: %v", err)
	}
	if migrationCount != 6 {
		t.Fatalf("reopen migration count = %d, want 6", migrationCount)
	}
}

func TestOpenMemoryUsesAnIndependentMigratedDatabase(t *testing.T) {
	first, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	createProvider(t, first, "memory-first")
	var count int
	if err := second.SQLDB().QueryRow("SELECT count(*) FROM providers").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("independent memory database has %d providers", count)
	}
}

func TestPersistenceAndSecureIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := store.CreateProvider(context.Background(), domain.Provider{
		Type:    domain.ProviderTypeLocal,
		Name:    "generated",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create generated provider: %v", err)
	}
	if provider.ID.IsZero() {
		t.Fatal("provider ID was not generated")
	}
	space, err := store.CreateSpace(context.Background(), domain.Space{
		ProviderID: provider.ID,
		Name:       "generated space",
		SyncState:  domain.SyncStateLocal,
	})
	if err != nil {
		t.Fatalf("create generated space: %v", err)
	}
	list, err := store.CreateList(context.Background(), domain.List{
		ProviderID: provider.ID,
		SpaceID:    space.ID,
		Name:       "generated list",
		SyncState:  domain.SyncStateLocal,
	})
	if err != nil {
		t.Fatalf("create generated list: %v", err)
	}
	task, err := store.CreateTask(context.Background(), domain.Task{
		ProviderID: provider.ID,
		ListID:     list.ID,
		Title:      "generated task",
		Assignee:   "owner",
		Priority:   domain.PriorityNormal,
		SyncState:  domain.SyncStateLocal,
	})
	if err != nil {
		t.Fatalf("create generated task: %v", err)
	}
	for name, id := range map[string]string{
		"provider": provider.ID.String(),
		"space":    space.ID.String(),
		"list":     list.ID.String(),
		"task":     task.ID.String(),
	} {
		if id == "" || strings.ContainsAny(id, " \t\n") {
			t.Errorf("%s ID = %q", name, id)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get persisted task: %v", err)
	}
	if got.Title != task.Title || got.ProviderID != provider.ID || got.Assignee != task.Assignee {
		t.Fatalf("persisted task = %#v", got)
	}
}

func TestCompositeProviderIsolationAndImmutableOwnership(t *testing.T) {
	store := openTestStore(t)
	first := createHierarchy(t, store, "provider-a")
	second := createHierarchy(t, store, "provider-b")

	_, err := store.CreateList(context.Background(), domain.List{
		ID:         "cross-list",
		ProviderID: first.provider.ID,
		SpaceID:    second.space.ID,
		Name:       "invalid",
		SyncState:  domain.SyncStateLocal,
	})
	if err == nil {
		t.Fatal("cross-provider list was accepted")
	}
	_, err = store.CreateTask(context.Background(), domain.Task{
		ID:           "cross-task",
		ProviderID:   first.provider.ID,
		ListID:       second.list.ID,
		ParentTaskID: &second.task.ID,
		Title:        "invalid",
		Priority:     domain.PriorityNormal,
		SyncState:    domain.SyncStateLocal,
	})
	if err == nil {
		t.Fatal("cross-provider task was accepted")
	}

	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"UPDATE spaces SET provider_id = ? WHERE id = ?", []any{second.provider.ID, first.space.ID}},
		{"UPDATE lists SET provider_id = ? WHERE id = ?", []any{second.provider.ID, first.list.ID}},
		{"UPDATE tasks SET provider_id = ? WHERE id = ?", []any{second.provider.ID, first.task.ID}},
	} {
		if _, err := store.SQLDB().Exec(statement.query, statement.args...); err == nil {
			t.Fatalf("immutable provider update succeeded: %s", statement.query)
		}
	}
}

func TestRemoteIDsAreProviderScopedAndTombstonesRemain(t *testing.T) {
	store := openTestStore(t)
	first := createHierarchy(t, store, "remote-a")
	second := createHierarchy(t, store, "remote-b")
	remote := "same-remote-id"

	firstSpace, err := store.CreateSpace(context.Background(), domain.Space{
		ID:         "remote-space-a",
		ProviderID: first.provider.ID,
		RemoteID:   &remote,
		Name:       "remote a",
		SyncState:  domain.SyncStateSynced,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondSpace, err := store.CreateSpace(context.Background(), domain.Space{
		ID:         "remote-space-b",
		ProviderID: second.provider.ID,
		RemoteID:   &remote,
		Name:       "remote b",
		SyncState:  domain.SyncStateSynced,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSpaceByRemoteID(context.Background(), first.provider.ID, remote)
	if err != nil || got.ID != firstSpace.ID {
		t.Fatalf("first provider remote lookup = %#v, %v", got, err)
	}
	got, err = store.GetSpaceByRemoteID(context.Background(), second.provider.ID, remote)
	if err != nil || got.ID != secondSpace.ID {
		t.Fatalf("second provider remote lookup = %#v, %v", got, err)
	}
	_, err = store.CreateSpace(context.Background(), domain.Space{
		ID:         "duplicate-remote",
		ProviderID: first.provider.ID,
		RemoteID:   &remote,
		Name:       "duplicate",
		SyncState:  domain.SyncStateSynced,
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate remote error = %v, want ErrAlreadyExists", err)
	}

	if err := store.DeleteTask(context.Background(), first.task.ID); err != nil {
		t.Fatal(err)
	}
	var deleted int
	var storedRemote sql.NullString
	if err := store.SQLDB().QueryRow(
		"SELECT is_deleted, remote_id FROM tasks WHERE id = ?", first.task.ID,
	).Scan(&deleted, &storedRemote); err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("task is_deleted = %d, want 1", deleted)
	}
	if storedRemote.Valid {
		t.Fatalf("unexpected task remote ID %q", storedRemote.String)
	}
	if _, err := store.GetTask(context.Background(), first.task.ID); err != nil {
		t.Fatalf("tombstone get: %v", err)
	}
	visible, err := store.ListByList(context.Background(), first.list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 0 {
		t.Fatalf("tombstone appeared in normal task list: %#v", visible)
	}
}

func TestTaskMutationAndQueueAreAtomic(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "atomic")
	original := h.task
	record, err := taskRecord(original)
	if err != nil {
		t.Fatal(err)
	}
	record.Title = "must roll back"
	_, err = store.updateTaskWithQueue(context.Background(), record, &SyncOperation{
		ProviderID: h.provider.ID.String() + "-wrong",
		EntityType: EntityTask,
		EntityID:   record.ID,
		Operation:  OperationUpdate,
	})
	if err == nil {
		t.Fatal("invalid queue intent succeeded")
	}
	got, err := store.GetTask(context.Background(), h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != original.Title {
		t.Fatalf("task changed despite rollback: %q", got.Title)
	}
	count, err := store.PendingCount(context.Background(), h.provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("pending count after rollback = %d, want 0", count)
	}

	updated := original
	updated.Title = "committed"
	updated.UpdatedAt = time.Now().UTC()
	updatedTask, err := store.ApplyTaskMutation(context.Background(), repository.TaskMutation{
		Task:      updated,
		Operation: domain.OperationTypeUpdate,
		Payload:   json.RawMessage(`{"title":"committed"}`),
	})
	if err != nil {
		t.Fatalf("apply mutation: %v", err)
	}
	if updatedTask.Title != "committed" {
		t.Fatalf("updated task = %#v", updatedTask)
	}
	count, err = store.PendingCount(context.Background(), h.provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pending count after mutation = %d, want 1", count)
	}
}

func TestRemoteTaskCreationUsesThePersistedTaskIDInItsQueue(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "remote-create")
	task, err := store.ApplyTaskMutation(context.Background(), repository.TaskMutation{
		Task: domain.Task{
			ProviderID: h.provider.ID,
			ListID:     h.list.ID,
			Title:      "remote task",
		},
		Operation: domain.OperationTypeCreate,
		Payload:   json.RawMessage(`{"title":"remote task"}`),
	})
	if err != nil {
		t.Fatalf("create task mutation: %v", err)
	}
	if task.ID.IsZero() {
		t.Fatal("created task has no ID")
	}
	var entityID string
	if err := store.SQLDB().QueryRow(
		"SELECT entity_id FROM sync_operations WHERE provider_id = ? AND entity_type = 'task' AND operation = 'create'",
		h.provider.ID,
	).Scan(&entityID); err != nil {
		t.Fatalf("load queued task: %v", err)
	}
	if entityID != task.ID.String() {
		t.Fatalf("queued entity ID = %q, persisted task ID = %q", entityID, task.ID)
	}
	if _, err := store.GetTask(context.Background(), task.ID); err != nil {
		t.Fatalf("load created task: %v", err)
	}
}

func TestSearchAndFilterUseJoinedProviderScopedData(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "search-a")
	other := createHierarchy(t, store, "search-b")
	due := time.Now().UTC().Add(24 * time.Hour)
	searchTask := h.task
	searchTask.Title = "Fix authentication"
	searchTask.Description = "Searchable description"
	searchTask.Status = "in_progress"
	searchTask.Priority = domain.PriorityHigh
	searchTask.DueAt = &due
	searchTask.UpdatedAt = time.Now().UTC()
	if _, err := store.UpdateTask(context.Background(), searchTask); err != nil {
		t.Fatal(err)
	}
	otherTask := other.task
	otherTask.Title = "Unrelated"
	otherTask.Status = "done"
	otherTask.Priority = domain.PriorityLow
	otherTask.UpdatedAt = time.Now().UTC()
	if _, err := store.UpdateTask(context.Background(), otherTask); err != nil {
		t.Fatal(err)
	}

	views, err := store.Search(context.Background(), repository.TaskFilter{Query: "authentication"})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Task.ID != searchTask.ID {
		t.Fatalf("search views = %#v", views)
	}
	if views[0].ProviderName != h.provider.Name || views[0].SpaceName != h.space.Name || views[0].ListName != h.list.Name {
		t.Fatalf("joined labels = %#v", views[0])
	}

	providerIDs := []domain.ProviderID{h.provider.ID}
	minPriority := domain.PriorityHigh
	filtered, err := store.Search(context.Background(), repository.TaskFilter{
		ProviderIDs: providerIDs,
		Statuses:    []string{"in_progress"},
		MinPriority: &minPriority,
		Completed:   boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Task.ID != searchTask.ID {
		t.Fatalf("filtered views = %#v", filtered)
	}
	injection, err := store.Search(context.Background(), repository.TaskFilter{Query: "' OR 1=1 --"})
	if err != nil {
		t.Fatal(err)
	}
	if len(injection) != 0 {
		t.Fatalf("search injection returned %d rows", len(injection))
	}
	wildcard := searchTask
	wildcard.ID = "wildcard-task"
	wildcard.Title = "literal 100% value"
	if _, err := store.CreateTask(context.Background(), wildcard); err != nil {
		t.Fatal(err)
	}
	matched, err := store.Search(context.Background(), repository.TaskFilter{Query: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].Task.ID != wildcard.ID {
		t.Fatalf("wildcard search results = %#v", matched)
	}
}

func TestConstraintErrorsKeepTheirCategories(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "constraint-errors")
	if _, err := store.CreateProvider(context.Background(), testProvider(h.provider.ID.String())); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate provider error = %v, want ErrAlreadyExists", err)
	}
	foreignErr := adaptError(func() error {
		_, err := store.SQLDB().Exec("INSERT INTO tasks (id, provider_id, list_id, title, status, priority) VALUES (?, ?, ?, ?, ?, ?)", "bad-task", h.provider.ID, "missing-list", "bad", "todo", "normal")
		return err
	}(), false)
	if !errors.Is(foreignErr, ErrForeignKeyConstraint) || errors.Is(foreignErr, domain.ErrAlreadyExists) {
		t.Fatalf("foreign-key error = %v", foreignErr)
	}
	checkErr := adaptError(func() error {
		_, err := store.SQLDB().Exec("INSERT INTO tasks (id, provider_id, list_id, title, status, priority) VALUES (?, ?, ?, ?, ?, ?)", "bad-task", h.provider.ID, h.list.ID, "", "todo", "normal")
		return err
	}(), false)
	if !errors.Is(checkErr, ErrCheckConstraint) || errors.Is(checkErr, domain.ErrAlreadyExists) {
		t.Fatalf("check error = %v", checkErr)
	}
	immutableErr := adaptError(func() error {
		_, err := store.SQLDB().Exec("UPDATE tasks SET provider_id = ? WHERE id = ?", "other-provider", h.task.ID)
		return err
	}(), false)
	if !errors.Is(immutableErr, ErrImmutableProvider) || !errors.Is(immutableErr, domain.ErrProviderMismatch) {
		t.Fatalf("immutable-provider error = %v", immutableErr)
	}
}

func TestUpsertProviderRejectsIdentityCollisionsWithoutReplacingState(t *testing.T) {
	store := openTestStore(t)
	provider := createProvider(t, store, "persistent-provider")
	configuration := json.RawMessage(`{"account":"one"}`)
	provider.Configuration = configuration
	if _, err := store.UpdateProvider(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	provider.Type = domain.ProviderTypeClickUp
	provider.Configuration = json.RawMessage(`{"account":"two"}`)
	if _, err := store.UpsertProvider(context.Background(), provider); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("colliding provider error = %v, want ErrAlreadyExists", err)
	}
	got, err := store.GetProvider(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != domain.ProviderTypeLocal || string(got.Configuration) != string(configuration) {
		t.Fatalf("provider was replaced: %#v", got)
	}
}

func TestQueueOrderingLeasesAndProviderScope(t *testing.T) {
	store := openTestStore(t)
	first := createHierarchy(t, store, "queue-a")
	second := createHierarchy(t, store, "queue-b")
	base := time.Now().UTC().Add(-time.Minute)
	firstOperation := domain.SyncOperation{
		ID:            "queue-op-a1",
		ProviderID:    first.provider.ID,
		EntityType:    domain.EntityTypeTask,
		EntityID:      first.task.ID.String(),
		Operation:     domain.OperationTypeUpdate,
		Payload:       json.RawMessage(`{"step":1}`),
		CreatedAt:     base,
		NextAttemptAt: timePointer(base),
	}
	secondOperation := firstOperation
	secondOperation.ID = "queue-op-a2"
	secondOperation.Payload = json.RawMessage(`{"step":2}`)
	secondOperation.CreatedAt = base.Add(time.Second)
	otherOperation := firstOperation
	otherOperation.ID = "queue-op-b1"
	otherOperation.ProviderID = second.provider.ID
	otherOperation.EntityID = second.task.ID.String()
	if err := store.Enqueue(context.Background(), firstOperation); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(context.Background(), secondOperation); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(context.Background(), otherOperation); err != nil {
		t.Fatal(err)
	}

	claimed, err := store.Claim(context.Background(), first.provider.ID)
	if err != nil || claimed.ID != firstOperation.ID {
		t.Fatalf("first claim = %#v, %v", claimed, err)
	}
	claimedTask, err := store.GetTask(context.Background(), first.task.ID)
	if err != nil || claimedTask.SyncState != domain.SyncStateSyncing {
		t.Fatalf("claimed task state = %#v, %v", claimedTask, err)
	}
	if _, err := store.Claim(context.Background(), first.provider.ID); !errors.Is(err, domain.ErrQueueEmpty) {
		t.Fatalf("same-entity second claim = %v, want queue empty", err)
	}
	otherClaim, err := store.Claim(context.Background(), second.provider.ID)
	if err != nil || otherClaim.ID != otherOperation.ID {
		t.Fatalf("other provider claim = %#v, %v", otherClaim, err)
	}
	if err := store.Complete(context.Background(), otherClaim.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(context.Background(), claimed.ID); err != nil {
		t.Fatal(err)
	}
	completedTask, err := store.GetTask(context.Background(), first.task.ID)
	if err != nil || completedTask.SyncState != domain.SyncStateSynced {
		t.Fatalf("completed task state = %#v, %v", completedTask, err)
	}
	claimed, err = store.Claim(context.Background(), first.provider.ID)
	if err != nil || claimed.ID != secondOperation.ID {
		t.Fatalf("second claim = %#v, %v", claimed, err)
	}
	future := time.Now().UTC().Add(time.Hour)
	if err := store.RetryAt(context.Background(), claimed.ID, future, errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), first.provider.ID); !errors.Is(err, domain.ErrQueueEmpty) {
		t.Fatalf("future retry claim = %v, want queue empty", err)
	}
	if _, err := store.SQLDB().Exec(
		"UPDATE sync_operations SET next_attempt_at = ? WHERE id = ?",
		formatTime(time.Now().UTC()), secondOperation.ID,
	); err != nil {
		t.Fatal(err)
	}
	ready, err := store.Claim(context.Background(), first.provider.ID)
	if err != nil || ready.ID != secondOperation.ID {
		t.Fatalf("ready retry claim = %#v, %v", ready, err)
	}
	if err := store.Complete(context.Background(), ready.ID); err != nil {
		t.Fatal(err)
	}

	// Use the internal clocked claim to make lease expiry deterministic.
	stale, err := store.enqueueOperation(context.Background(), SyncOperation{
		ID:            "queue-op-a3",
		ProviderID:    first.provider.ID.String(),
		EntityType:    EntityTask,
		EntityID:      first.task.ID.String(),
		Operation:     OperationUpdate,
		CreatedAt:     base.Add(2 * time.Second),
		NextAttemptAt: base.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	claimedRaw, err := store.claimOperationAt(context.Background(), first.provider.ID.String(), "test-worker", time.Minute, clock)
	if err != nil || claimedRaw.ID != stale.ID {
		t.Fatalf("clocked claim = %#v, %v", claimedRaw, err)
	}
	if _, err := store.requeueStaleOperations(context.Background(), first.provider.ID.String(), clock.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.completeOperation(context.Background(), first.provider.ID.String(), stale.ID, "test-worker"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired completion = %v, want ErrLeaseLost", err)
	}
}

func TestTerminalQueueFailureIsNotClaimable(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "terminal-failure")
	operation := domain.SyncOperation{
		ID:         "terminal-failure-op",
		ProviderID: h.provider.ID,
		EntityType: domain.EntityTypeTask,
		EntityID:   h.task.ID.String(),
		Operation:  domain.OperationTypeUpdate,
		Payload:    json.RawMessage(`{"title":"invalid"}`),
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Enqueue(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), h.provider.ID)
	if err != nil {
		t.Fatalf("claim operation: %v", err)
	}
	if err := store.Fail(context.Background(), claimed.ID, errors.New("permanent validation failure")); err != nil {
		t.Fatalf("fail operation: %v", err)
	}

	failed, err := store.GetOperation(context.Background(), h.provider.ID.String(), claimed.ID.String())
	if err != nil {
		t.Fatalf("get failed operation: %v", err)
	}
	if failed.Status != QueueStatusFailed {
		t.Fatalf("failed operation status = %q, want failed", failed.Status)
	}
	if _, err := store.Claim(context.Background(), h.provider.ID); !errors.Is(err, domain.ErrQueueEmpty) {
		t.Fatalf("terminal failure claim = %v, want queue empty", err)
	}
}

func TestTerminalFailureDoesNotBlockLaterEntityOperation(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "terminal-order")
	base := time.Now().UTC().Add(-time.Second)
	for _, operation := range []domain.SyncOperation{
		{ID: "terminal-order-create", ProviderID: h.provider.ID, EntityType: domain.EntityTypeTask, EntityID: h.task.ID.String(), Operation: domain.OperationTypeCreate, CreatedAt: base},
		{ID: "terminal-order-update", ProviderID: h.provider.ID, EntityType: domain.EntityTypeTask, EntityID: h.task.ID.String(), Operation: domain.OperationTypeUpdate, CreatedAt: base.Add(time.Second)},
	} {
		if err := store.Enqueue(context.Background(), operation); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.Claim(context.Background(), h.provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Fail(context.Background(), first.ID, errors.New("permanent")); err != nil {
		t.Fatal(err)
	}
	failed, err := store.GetOperation(context.Background(), h.provider.ID.String(), first.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if failed.Retryable {
		t.Fatal("terminal failure remained retryable")
	}
	second, err := store.Claim(context.Background(), h.provider.ID)
	if err != nil {
		t.Fatalf("later operation remained blocked: %v", err)
	}
	if second.ID != "terminal-order-update" {
		t.Fatalf("claimed operation = %q, want later update", second.ID)
	}
}

func TestQueueLifecycleRejectsForeignProvider(t *testing.T) {
	store := openTestStore(t)
	first := createHierarchy(t, store, "lifecycle-first")
	second := createHierarchy(t, store, "lifecycle-second")
	if err := store.Enqueue(context.Background(), domain.SyncOperation{
		ID: "foreign-lifecycle-op", ProviderID: first.provider.ID, EntityType: domain.EntityTypeTask,
		EntityID: first.task.ID.String(), Operation: domain.OperationTypeUpdate,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), first.provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteForProvider(context.Background(), second.provider.ID, claimed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign completion error = %v, want ErrNotFound", err)
	}
	if err := store.CompleteForProvider(context.Background(), first.provider.ID, claimed.ID); err != nil {
		t.Fatalf("owner completion = %v", err)
	}
}

func TestReleasedQueueOperationIsImmediatelyClaimable(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "released-operation")
	operation := domain.SyncOperation{
		ID:         "released-operation-op",
		ProviderID: h.provider.ID,
		EntityType: domain.EntityTypeTask,
		EntityID:   h.task.ID.String(),
		Operation:  domain.OperationTypeUpdate,
		Payload:    json.RawMessage(`{"title":"released"}`),
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Enqueue(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), h.provider.ID)
	if err != nil {
		t.Fatalf("claim operation: %v", err)
	}
	if err := store.Release(context.Background(), claimed.ID); err != nil {
		t.Fatalf("release operation: %v", err)
	}

	released, err := store.GetOperation(context.Background(), h.provider.ID.String(), claimed.ID.String())
	if err != nil {
		t.Fatalf("get released operation: %v", err)
	}
	if released.Status != QueueStatusPending || released.LeaseOwner != nil || released.LeaseExpiresAt != nil {
		t.Fatalf("released operation = %#v, want pending without lease", released)
	}
	task, err := store.GetTask(context.Background(), h.task.ID)
	if err != nil {
		t.Fatalf("get released task: %v", err)
	}
	if task.SyncState != domain.SyncStatePending {
		t.Fatalf("released task state = %q, want pending", task.SyncState)
	}

	reclaimed, err := store.Claim(context.Background(), h.provider.ID)
	if err != nil || reclaimed.ID != claimed.ID {
		t.Fatalf("reclaimed operation = %#v, %v", reclaimed, err)
	}
}

func TestTaskTimeTrackingRoundTrips(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "time-tracking")
	estimate := 90 * time.Minute
	tracked := 45 * time.Minute
	h.task.TimeEstimate = &estimate
	h.task.TimeTracked = &tracked
	updated, err := store.UpdateTask(context.Background(), h.task)
	if err != nil {
		t.Fatalf("update task with time tracking: %v", err)
	}
	if updated.TimeEstimate == nil || *updated.TimeEstimate != estimate {
		t.Fatalf("updated estimate = %v, want %v", updated.TimeEstimate, estimate)
	}
	if updated.TimeTracked == nil || *updated.TimeTracked != tracked {
		t.Fatalf("updated tracked time = %v, want %v", updated.TimeTracked, tracked)
	}

	loaded, err := store.GetTask(context.Background(), h.task.ID)
	if err != nil {
		t.Fatalf("reload task with time tracking: %v", err)
	}
	if loaded.TimeEstimate == nil || *loaded.TimeEstimate != estimate {
		t.Fatalf("loaded estimate = %v, want %v", loaded.TimeEstimate, estimate)
	}
	if loaded.TimeTracked == nil || *loaded.TimeTracked != tracked {
		t.Fatalf("loaded tracked time = %v, want %v", loaded.TimeTracked, tracked)
	}
}

func TestMetadataSyncBasesConflictsAndAppState(t *testing.T) {
	store := openTestStore(t)
	h := createHierarchy(t, store, "state")
	cursor := "cursor-1"
	syncAt := time.Now().UTC()
	if _, err := store.SetProviderSyncState(context.Background(), ProviderSyncState{
		ProviderID: h.provider.ID.String(),
		State:      SyncStatePending,
		Cursor:     &cursor,
		LastSyncAt: &syncAt,
	}); err != nil {
		t.Fatal(err)
	}
	providerSync, err := store.GetProviderSyncState(context.Background(), h.provider.ID.String())
	if err != nil || providerSync.State != SyncStatePending || providerSync.Cursor == nil || *providerSync.Cursor != cursor {
		t.Fatalf("provider sync state = %#v, %v", providerSync, err)
	}
	if err := store.PutMetadata(context.Background(), domain.ProviderMetadata{
		ProviderID: h.provider.ID,
		EntityType: domain.EntityTypeTask,
		EntityID:   h.task.ID.String(),
		Key:        "provider_key",
		Value:      "value",
	}); err != nil {
		t.Fatal(err)
	}
	metadata, err := store.GetMetadata(context.Background(), h.provider.ID, domain.EntityTypeTask, h.task.ID.String(), "provider_key")
	if err != nil || metadata.Value != "value" {
		t.Fatalf("metadata = %#v, %v", metadata, err)
	}
	if _, err := store.SQLDB().Exec(`
		INSERT INTO provider_metadata (provider_id, entity_type, entity_id, key, value, created_at, updated_at)
		VALUES (?, 'task', ?, 'bad', 'value', ?, ?)`,
		"state-other", h.task.ID.String(), formatTime(time.Now()), formatTime(time.Now())); err == nil {
		t.Fatal("cross-provider metadata was accepted")
	}

	remoteID := "remote-task"
	remoteVersion := "version-7"
	remoteUpdated := time.Now().UTC()
	createdAt := time.Now().UTC().Add(-time.Hour)
	capturedAt := time.Now().UTC().Add(-time.Minute)
	base := domain.SyncBase{
		ProviderID:      h.provider.ID,
		RemoteID:        &remoteID,
		SyncState:       domain.SyncStateSynced,
		RemoteUpdatedAt: &remoteUpdated,
		EntityType:      domain.EntityTypeTask,
		EntityID:        h.task.ID.String(),
		Payload:         json.RawMessage(`{"title":"base"}`),
		RemoteVersion:   &remoteVersion,
		CreatedAt:       createdAt,
		CapturedAt:      capturedAt,
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.SaveBase(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	gotBase, err := store.GetBase(context.Background(), h.provider.ID, domain.EntityTypeTask, h.task.ID.String())
	if err != nil || string(gotBase.Payload) != string(base.Payload) || gotBase.RemoteID == nil || *gotBase.RemoteID != remoteID || gotBase.RemoteVersion == nil || *gotBase.RemoteVersion != remoteVersion || !gotBase.CreatedAt.Equal(createdAt) || !gotBase.UpdatedAt.Equal(base.UpdatedAt) || !gotBase.CapturedAt.Equal(capturedAt) {
		t.Fatalf("sync base = %#v, %v", gotBase, err)
	}

	conflict := domain.Conflict{
		ID:         "conflict-state",
		ProviderID: h.provider.ID,
		EntityType: domain.EntityTypeTask,
		EntityID:   h.task.ID.String(),
		Fields: []domain.FieldConflict{{
			Field:       "title",
			LocalValue:  json.RawMessage(`"local"`),
			RemoteValue: json.RawMessage(`"remote"`),
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateConflict(context.Background(), conflict); err != nil {
		t.Fatal(err)
	}
	gotConflict, err := store.GetConflict(context.Background(), conflict.ID)
	if err != nil || len(gotConflict.Fields) != 1 || gotConflict.Fields[0].Field != "title" {
		t.Fatalf("conflict = %#v, %v", gotConflict, err)
	}
	resolvedAt := time.Now().UTC()
	if err := store.Resolve(context.Background(), conflict.ID, "local", resolvedAt); err != nil {
		t.Fatal(err)
	}
	gotConflict, err = store.GetConflict(context.Background(), conflict.ID)
	if err != nil || gotConflict.ResolvedAt == nil || gotConflict.Resolution != "local" {
		t.Fatalf("resolved conflict = %#v, %v", gotConflict, err)
	}

	state := domain.AppState{Key: "ui", Value: json.RawMessage(`{"list":"state-list"}`), UpdatedAt: time.Now().UTC()}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), "ui")
	if err != nil || string(loaded.Value) != string(state.Value) {
		t.Fatalf("app state = %#v, %v", loaded, err)
	}
}

func TestProviderSyncMetadataSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider-state.db")
	ctx := context.Background()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	providerID := domain.ProviderID("provider-state")
	if _, err := store.UpsertProvider(ctx, domain.Provider{ID: providerID, Type: domain.ProviderTypeClickUp, Name: "State", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cursor := "cursor-9"
	lastSync := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateProviderSyncState(ctx, providerID, domain.SyncStateFailed, &cursor, errors.New("rate limited"), &lastSync); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.GetProvider(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if provider.SyncState != domain.SyncStateFailed || provider.SyncCursor == nil || *provider.SyncCursor != cursor || provider.SyncError != "rate limited" || provider.LastSyncAt == nil || !provider.LastSyncAt.Equal(lastSync) {
		t.Fatalf("provider sync metadata = %#v", provider)
	}
	if err := store.UpdateProviderSyncState(ctx, providerID, domain.SyncStateSynced, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	provider, err = store.GetProvider(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if provider.SyncState != domain.SyncStateSynced || provider.SyncError != "" {
		t.Fatalf("successful provider sync metadata = %#v, want cleared error", provider)
	}
}

func boolPointer(value bool) *bool { return &value }

func timePointer(value time.Time) *time.Time { return &value }
