package bootstrap

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kappke/task-tui/internal/command"
	"github.com/kappke/task-tui/internal/domain"
	providerpkg "github.com/kappke/task-tui/internal/provider"
	"github.com/kappke/task-tui/internal/storage/sqlite"
	foundationsync "github.com/kappke/task-tui/internal/sync"
	foundationtui "github.com/kappke/task-tui/internal/tui"
)

func TestBuildUsesFoundationGraphAndRendersCachedProviders(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Database.Path = filepath.Join(t.TempDir(), "tasktui.db")
	cfg.Logging.Path = filepath.Join(t.TempDir(), "tasktui.log")
	cfg.ClickUp.Enabled = true
	cfg.Sync.Enabled = false

	var output bytes.Buffer
	runtime, err := Build(context.Background(), Options{
		Config:   cfg,
		Headless: true,
		Input:    bytes.NewReader(nil),
		Output:   &output,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("Runtime.Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "ClickUp") || strings.Contains(output.String(), "Local") {
		t.Fatalf("initial frame does not contain only the active provider: %q", output.String())
	}
	if count := strings.Count(output.String(), "TASK MANAGER"); count != 1 {
		t.Fatalf("headless render count = %d, want one frame", count)
	}
}

func TestCurrentSQLiteRunnerUpgradesLegacyBootstrapDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(sqliteMigrations[0].sql); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL); INSERT INTO schema_migrations VALUES (1, 'initial_schema', '2026-01-01T00:00:00Z'); INSERT INTO providers(id, type, name, enabled, configuration, last_sync_at, sync_error, created_at, updated_at) VALUES ('legacy', 'local', 'Legacy', 1, '', NULL, '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.GetProvider(context.Background(), "legacy")
	if err != nil || provider.Name != "Legacy" {
		t.Fatalf("legacy provider = %#v, %v", provider, err)
	}
	for _, table := range []string{"sync_bases", "conflicts", "app_state"} {
		var count int
		if err := store.SQLDB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("legacy upgrade table %s: count=%d err=%v", table, count, err)
		}
	}
	for _, object := range []struct {
		kind string
		name string
	}{
		{"index", "spaces_provider_remote_id"},
		{"index", "sync_operations_claim_idx"},
		{"trigger", "spaces_provider_immutable"},
		{"trigger", "provider_metadata_validate_insert"},
		{"trigger", "sync_bases_validate_insert"},
	} {
		var count int
		if err := store.SQLDB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", object.kind, object.name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("legacy upgrade %s %s: count=%d err=%v", object.kind, object.name, count, err)
		}
	}
}

func TestFoundationUIRunsBubbleTeaOnInteractiveStreams(t *testing.T) {
	var output bytes.Buffer
	terminal := NewStreamTerminal(bytes.NewBufferString("q"), &output, false)
	ui, err := newFoundationUIController(terminal, foundationNoopHandler{}, nil, nil)
	if err != nil {
		t.Fatalf("newFoundationUIController() error = %v", err)
	}
	if err := ui.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := ui.Render(context.Background(), View{}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if err := ui.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "TASK MANAGER") {
		t.Fatalf("Bubble Tea output does not contain the rendered frame: %q", output.String())
	}
}

func TestFoundationUIStateRoundTripsCollapsedGroups(t *testing.T) {
	terminal := NewStreamTerminal(bytes.NewBuffer(nil), &bytes.Buffer{}, false)
	ui, err := newFoundationUIController(terminal, foundationNoopHandler{}, nil, nil)
	if err != nil {
		t.Fatalf("newFoundationUIController() error = %v", err)
	}
	ui.model.UI.Focus = foundationtui.PanelTasks
	ui.model.UI.GroupBy = foundationtui.TaskGroupStatus
	ui.model.UI.CollapsedGroups = map[string]bool{
		"status:done":    true,
		"assignee:alice": true,
		"status:open":    false,
	}

	state := ui.State()
	if len(state.CollapsedGroups) != 2 || state.CollapsedGroups[0] != "assignee:alice" || state.CollapsedGroups[1] != "status:done" {
		t.Fatalf("saved collapsed groups = %#v, want sorted active keys", state.CollapsedGroups)
	}

	ui.SetState(UIState{
		Panel:           string(foundationtui.PanelTasks),
		ProviderID:      "work",
		SpaceID:         "engineering",
		ListID:          "backend",
		GroupBy:         string(foundationtui.TaskGroupStatus),
		CollapsedGroups: []string{" status:done ", "status:done", "assignee:alice"},
	})
	if ui.model.UI.ActiveProviderID != "work" || ui.model.UI.SelectedNode.ListID != "backend" {
		t.Fatalf("restored selection = %#v, active provider = %q", ui.model.UI.SelectedNode, ui.model.UI.ActiveProviderID)
	}
	state = ui.State()
	if state.ProviderID != "work" || state.SpaceID != "engineering" || state.ListID != "backend" {
		t.Fatalf("restored opened list state = %#v, want work/engineering/backend", state)
	}
	if !ui.model.UI.CollapsedGroups["status:done"] || !ui.model.UI.CollapsedGroups["assignee:alice"] || len(ui.model.UI.CollapsedGroups) != 2 {
		t.Fatalf("restored collapsed groups = %#v, want two unique keys", ui.model.UI.CollapsedGroups)
	}
}

func TestFoundationUILoadsTheRequestedListInsteadOfPersistedList(t *testing.T) {
	oldView := View{Tasks: []Task{{ID: "old-task", ProviderID: "work", ListID: "old-list", Title: "Old"}}}
	newView := View{Tasks: []Task{{ID: "new-task", ProviderID: "work", ListID: "new-list", Title: "New"}}}
	loaderCalls := 0
	listLoaderCalls := 0
	ui, err := newFoundationUIControllerWithListLoader(
		NewStreamTerminal(bytes.NewBuffer(nil), &bytes.Buffer{}, false),
		foundationNoopHandler{},
		func(context.Context) (View, error) {
			loaderCalls++
			return oldView, nil
		},
		func(_ context.Context, providerID ProviderID, listID ListID) (View, error) {
			listLoaderCalls++
			if providerID != "work" || listID != "new-list" {
				t.Fatalf("requested list scope = %s/%s", providerID, listID)
			}
			return newView, nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("newFoundationUIControllerWithListLoader() error = %v", err)
	}

	message := ui.teaCommand(context.Background(), foundationtui.AppCommand{
		Kind:       foundationtui.CommandLoadCached,
		ProviderID: "work",
		ListID:     "new-list",
	})()
	snapshot, ok := message.(foundationtui.SnapshotMsg)
	if !ok || len(snapshot.Data.Tasks) != 1 || snapshot.Data.Tasks[0].ID != "new-task" {
		t.Fatalf("loaded message = %#v, want new-list task", message)
	}
	if loaderCalls != 0 || listLoaderCalls != 1 {
		t.Fatalf("loader calls = generic %d, scoped %d", loaderCalls, listLoaderCalls)
	}
}

func TestFoundationHandlerPersistsLocalTaskThroughComposedGraph(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Database.Path = filepath.Join(t.TempDir(), "tasktui.db")
	cfg.Logging.Path = filepath.Join(t.TempDir(), "tasktui.log")
	cfg.Sync.Enabled = false

	runtime, err := Build(context.Background(), Options{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer func() { _ = runtime.Shutdown(context.Background()) }()

	ctx := context.Background()
	if _, err := runtime.handler.Handle(ctx, command.Command{
		Kind:       command.KindCreateSpace,
		ProviderID: "local",
		Title:      "Personal",
	}); err != nil {
		t.Fatalf("create space error = %v", err)
	}
	view, err := runtime.store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot after space error = %v", err)
	}
	if len(view.Spaces) != 1 {
		t.Fatalf("space count = %d, want 1", len(view.Spaces))
	}
	spaceID := string(view.Spaces[0].ID)
	if _, err := runtime.handler.Handle(ctx, command.Command{
		Kind:       command.KindCreateList,
		ProviderID: "local",
		SpaceID:    spaceID,
		Title:      "Today",
	}); err != nil {
		t.Fatalf("create list error = %v", err)
	}
	view, err = runtime.store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot after list error = %v", err)
	}
	if len(view.Lists) != 1 {
		t.Fatalf("list count = %d, want 1", len(view.Lists))
	}
	if _, err := runtime.handler.Handle(ctx, command.Command{
		Kind:       command.KindCreateTask,
		ProviderID: "local",
		ListID:     string(view.Lists[0].ID),
		Title:      "Remember the cache",
	}); err != nil {
		t.Fatalf("create task error = %v", err)
	}
	view, err = runtime.store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot after task error = %v", err)
	}
	if len(view.Tasks) != 1 || view.Tasks[0].Title != "Remember the cache" {
		t.Fatalf("tasks = %#v, want one local task", view.Tasks)
	}
}

func TestFoundationUICommandsCreateFirstHierarchy(t *testing.T) {
	space, dispatch, err := foundationUICommand(foundationtui.AppCommand{
		Kind:       foundationtui.CommandCreateSpace,
		ProviderID: "local",
		Title:      "Personal",
	})
	if err != nil || !dispatch || space.Kind != command.KindCreateSpace || space.ProviderID != "local" {
		t.Fatalf("space translation = %#v, dispatch=%v, err=%v", space, dispatch, err)
	}
	list, dispatch, err := foundationUICommand(foundationtui.AppCommand{
		Kind:       foundationtui.CommandCreateList,
		ProviderID: "local",
		SpaceID:    "space-1",
		Title:      "Today",
	})
	if err != nil || !dispatch || list.Kind != command.KindCreateList || list.SpaceID != "space-1" {
		t.Fatalf("list translation = %#v, dispatch=%v, err=%v", list, dispatch, err)
	}
}

func TestFoundationUICommandRefreshPreservesListID(t *testing.T) {
	translated, dispatch, err := foundationUICommand(foundationtui.AppCommand{
		Kind:       foundationtui.CommandRefresh,
		ProviderID: "clickup",
		ListID:     "list-local",
	})
	if err != nil || !dispatch {
		t.Fatalf("refresh translation failed: dispatch=%v err=%v", dispatch, err)
	}
	if translated.Kind != command.KindRefresh || translated.ProviderID != "clickup" || translated.ListID != "list-local" {
		t.Fatalf("refresh translation = %#v", translated)
	}
}

func TestCachedProviderReconcilesRemoteIDsWithLocalIDs(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	providerID := domain.ProviderID("clickup-work")
	if _, err := store.UpsertProvider(ctx, domain.Provider{
		ID:        providerID,
		Type:      domain.ProviderTypeClickUp,
		Name:      "Work",
		Enabled:   true,
		SyncState: domain.SyncStatePending,
	}); err != nil {
		t.Fatalf("insert provider error = %v", err)
	}
	now := time.Now().UTC()
	spaceRemoteID := "space-remote"
	space, err := store.UpsertSpace(ctx, domain.Space{
		ID:         "space-local",
		ProviderID: providerID,
		RemoteID:   &spaceRemoteID,
		Name:       "Work",
		SyncState:  domain.SyncStateSynced,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("insert space error = %v", err)
	}
	listRemoteID := "list-remote"
	list, err := store.UpsertList(ctx, domain.List{
		ID:         "list-local",
		ProviderID: providerID,
		SpaceID:    space.ID,
		RemoteID:   &listRemoteID,
		Name:       "Inbox",
		SyncState:  domain.SyncStateSynced,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("insert list error = %v", err)
	}
	taskRemoteID := "task-remote"
	fake := &foundationTestProvider{
		id: providerID,
		created: domain.Task{
			ID:         "task-remote-result",
			ProviderID: providerID,
			ListID:     list.ID,
			RemoteID:   &taskRemoteID,
			Title:      "Remote task",
			Status:     "todo",
			Priority:   domain.PriorityNormal,
			SyncState:  domain.SyncStateSynced,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}
	cached := newCachedProvider(fake, store)
	cached.SetActiveTaskList(domain.ListID(listRemoteID))
	if got := cached.activeTaskList(); got != list.ID {
		t.Fatalf("active list = %q, want local list %q", got, list.ID)
	}
	localTask, err := cached.CreateTask(ctx, domain.Task{
		ID:         "task-local",
		ProviderID: providerID,
		ListID:     list.ID,
		Title:      "Remote task",
		Status:     "todo",
		Priority:   domain.PriorityNormal,
		SyncState:  domain.SyncStatePending,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("cached CreateTask() error = %v", err)
	}
	if localTask.ID != "task-local" || localTask.RemoteID == nil || *localTask.RemoteID != taskRemoteID {
		t.Fatalf("created task identity = %#v", localTask)
	}
	remoteList, err := foundationRemoteListResolver(store)(ctx, providerID, list.ID)
	if err != nil || remoteList != listRemoteID {
		t.Fatalf("remote list resolver = %q, %v; want %q", remoteList, err, listRemoteID)
	}
	remoteSpace, err := foundationRemoteSpaceResolver(store)(ctx, providerID, space.ID)
	if err != nil || remoteSpace != spaceRemoteID {
		t.Fatalf("remote space resolver = %q, %v; want %q", remoteSpace, err, spaceRemoteID)
	}
	remoteTask, err := foundationRemoteTaskResolver(store)(ctx, providerID, localTask.ID)
	if err != nil || remoteTask != taskRemoteID {
		t.Fatalf("remote task resolver = %q, %v; want %q", remoteTask, err, taskRemoteID)
	}
	parentTask, ok := foundationParentResolver(store)(ctx, providerID, taskRemoteID)
	if !ok || parentTask != localTask.ID {
		t.Fatalf("parent resolver = %q, %v; want %q, true", parentTask, ok, localTask.ID)
	}

	fetchedSpaceID := domain.SpaceID("space-fetched")
	fetchedListID := domain.ListID("list-fetched")
	fake.spaces = []domain.Space{{
		ID:              fetchedSpaceID,
		ProviderID:      providerID,
		RemoteID:        &spaceRemoteID,
		Name:            "Work",
		SyncState:       domain.SyncStateSynced,
		RemoteUpdatedAt: &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}}
	fake.lists = map[domain.SpaceID][]domain.List{
		fetchedSpaceID: {{
			ID:              fetchedListID,
			ProviderID:      providerID,
			SpaceID:         fetchedSpaceID,
			RemoteID:        &listRemoteID,
			Name:            "Inbox",
			SyncState:       domain.SyncStateSynced,
			RemoteUpdatedAt: &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		}},
	}
	fake.lists[space.ID] = append([]domain.List(nil), fake.lists[fetchedSpaceID]...)
	fake.lists[space.ID][0].SpaceID = space.ID
	fake.tasks = map[domain.ListID][]domain.Task{
		list.ID: {{
			ID:              domain.TaskID("task-fetched"),
			ProviderID:      providerID,
			ListID:          list.ID,
			RemoteID:        &taskRemoteID,
			Title:           "Remote task",
			Status:          "todo",
			Priority:        domain.PriorityNormal,
			SyncState:       domain.SyncStateSynced,
			RemoteUpdatedAt: &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		}},
	}
	if err := cached.Pull(ctx); err != nil {
		t.Fatalf("cached Pull() error = %v", err)
	}
	tasks, err := store.ListTasksByProvider(ctx, providerID)
	if err != nil {
		t.Fatalf("list tasks error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "task-local" {
		t.Fatalf("cached tasks = %#v, want one task with local identity", tasks)
	}
	state, err := store.GetProviderSyncState(ctx, providerID.String())
	if err != nil {
		t.Fatalf("get sync state error = %v", err)
	}
	if state.State != sqlite.SyncStateSynced || state.LastSyncAt == nil {
		t.Fatalf("provider sync state = %#v, want synced", state)
	}

	local, err := store.GetTask(ctx, localTask.ID)
	if err != nil {
		t.Fatalf("get task before concurrent local edit: %v", err)
	}
	local.Title = "Local edit"
	local.SyncState = domain.SyncStatePending
	local.UpdatedAt = now.Add(time.Minute)
	if _, err := store.UpdateTask(ctx, local); err != nil {
		t.Fatalf("persist local edit: %v", err)
	}
	fake.tasks[list.ID][0].Title = "Remote overwrite"
	if err := cached.Pull(ctx); err != nil {
		t.Fatalf("cached Pull() after local edit: %v", err)
	}
	local, err = store.GetTask(ctx, localTask.ID)
	if err != nil {
		t.Fatalf("get task after concurrent local edit: %v", err)
	}
	if local.Title != "Local edit" || local.SyncState != domain.SyncStatePending {
		t.Fatalf("local task after pull = %#v, want pending local edit", local)
	}

	// A successful push makes the pushed value the new merge base. If ClickUp
	// is then changed back to the previous value, remote reconciliation applies it.
	fake.created = local
	if _, err := cached.UpdateTask(ctx, local); err != nil {
		t.Fatalf("push local edit: %v", err)
	}
	fake.tasks[list.ID][0].Title = "Remote task"
	if err := cached.Pull(ctx); err != nil {
		t.Fatalf("cached Pull() after remote reversion: %v", err)
	}
	local, err = store.GetTask(ctx, localTask.ID)
	if err != nil {
		t.Fatalf("get task after remote reversion: %v", err)
	}
	if local.Title != "Remote task" || local.SyncState != domain.SyncStateSynced {
		t.Fatalf("task after remote reversion = %#v, want reverted synced task", local)
	}
}

func TestUIStateNormalizesRemoteListAndRestartRefreshFindsSharedTasks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasktui.db")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	providerID := domain.ProviderID("clickup-work")
	if _, err := store.UpsertProvider(ctx, domain.Provider{ID: providerID, Type: domain.ProviderTypeClickUp, Name: "Work", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	spaceRemote, selectedRemote, otherRemote := "space-remote", "list-remote-selected", "list-remote-other"
	space, err := store.UpsertSpace(ctx, domain.Space{ID: "space-local", ProviderID: providerID, RemoteID: &spaceRemote, Name: "Work", SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := store.UpsertList(ctx, domain.List{ID: "list-local-selected", ProviderID: providerID, SpaceID: space.ID, RemoteID: &selectedRemote, Name: "Selected", SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.UpsertList(ctx, domain.List{ID: "list-local-other", ProviderID: providerID, SpaceID: space.ID, RemoteID: &otherRemote, Name: "Other", SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	data := newFoundationDataStore(store)
	if err := data.SaveUIState(ctx, UIState{ProviderID: string(providerID), SpaceID: string(space.ID), ListID: selectedRemote}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	data = newFoundationDataStore(store)
	state, err := data.LoadUIState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.ListID != string(selected.ID) || state.SpaceID != string(space.ID) {
		t.Fatalf("normalized UI state = %#v, want local scope %s/%s", state, space.ID, selected.ID)
	}

	selectedTaskRemote, otherTaskRemote := "task-selected", "task-other"
	fake := &foundationTestProvider{
		id:     providerID,
		spaces: []domain.Space{{ID: "fetched-space", ProviderID: providerID, RemoteID: &spaceRemote, Name: "Work", SyncState: domain.SyncStateSynced}},
		lists:  map[domain.SpaceID][]domain.List{space.ID: {{ID: "mapped-selected", ProviderID: providerID, SpaceID: space.ID, RemoteID: &selectedRemote, Name: "Selected", SyncState: domain.SyncStateSynced}, {ID: "mapped-other", ProviderID: providerID, SpaceID: space.ID, RemoteID: &otherRemote, Name: "Other", SyncState: domain.SyncStateSynced}}},
		tasks: map[domain.ListID][]domain.Task{
			selected.ID: {{ID: "mapped-selected-task", ProviderID: providerID, ListID: selected.ID, ListIDs: []domain.ListID{selected.ID}, RemoteID: &selectedTaskRemote, Title: "Selected task", Status: "todo", Priority: domain.PriorityNormal, SyncState: domain.SyncStateSynced}},
			other.ID:    {{ID: "mapped-other-task", ProviderID: providerID, ListID: other.ID, ListIDs: []domain.ListID{other.ID, selected.ID}, RemoteID: &otherTaskRemote, Title: "Other task", Status: "todo", Priority: domain.PriorityNormal, SyncState: domain.SyncStateSynced}},
		},
	}
	cached := newCachedProvider(fake, store)
	cached.SetActiveTaskList(domain.ListID(state.ListID))
	if err := cached.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasksByProvider(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("restart refresh tasks = %#v, want tasks from each home list", tasks)
	}
	selectedTasks, err := store.ListByList(ctx, selected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(selectedTasks) != 2 {
		t.Fatalf("selected list tasks = %#v, want shared task from other home list", selectedTasks)
	}
	var shared domain.Task
	for _, task := range selectedTasks {
		if task.Title == "Other task" {
			shared = task
		}
	}
	if shared.ID == "" || shared.ListID != other.ID || !shared.HasList(selected.ID) {
		t.Fatalf("shared task = %#v, want other home list and selected membership", shared)
	}
}

func TestCachedProviderPreservesParentsAcrossOutOfOrderRepeatedPulls(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	providerID := domain.ProviderID("clickup-work")
	if _, err := store.UpsertProvider(ctx, domain.Provider{ID: providerID, Type: domain.ProviderTypeClickUp, Name: "Work", Enabled: true, SyncState: domain.SyncStatePending}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	spaceRemoteID, listRemoteID := "space-remote", "list-remote"
	space, err := store.UpsertSpace(ctx, domain.Space{ID: "space-local", ProviderID: providerID, RemoteID: &spaceRemoteID, Name: "Work", SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.UpsertList(ctx, domain.List{ID: "list-local", ProviderID: providerID, SpaceID: space.ID, RemoteID: &listRemoteID, Name: "Inbox", SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}

	parentRemoteID, childRemoteID := "parent-remote", "child-remote"
	fake := &foundationTestProvider{
		id:     providerID,
		spaces: []domain.Space{{ID: "fetched-space", ProviderID: providerID, RemoteID: &spaceRemoteID, Name: "Work", SyncState: domain.SyncStateSynced}},
		lists:  map[domain.SpaceID][]domain.List{space.ID: {{ID: "fetched-list", ProviderID: providerID, SpaceID: space.ID, RemoteID: &listRemoteID, Name: "Inbox", SyncState: domain.SyncStateSynced}}},
		tasks: map[domain.ListID][]domain.Task{
			list.ID: {
				{ID: "mapped-child-1", ProviderID: providerID, ListID: list.ID, RemoteID: &childRemoteID, ParentTaskID: taskIDPointer("mapped-parent-1"), Title: "Child", Status: "todo", Priority: domain.PriorityNormal, SyncState: domain.SyncStateSynced},
				{ID: "mapped-parent-1", ProviderID: providerID, ListID: list.ID, RemoteID: &parentRemoteID, Title: "Parent", Status: "todo", Priority: domain.PriorityNormal, SyncState: domain.SyncStateSynced},
			},
		},
	}
	cached := newCachedProvider(fake, store)
	cached.SetActiveTaskList(list.ID)
	if err := cached.Pull(ctx); err != nil {
		t.Fatalf("first Pull() error = %v", err)
	}

	parent, err := store.GetTaskByRemoteID(ctx, providerID, parentRemoteID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.GetTaskByRemoteID(ctx, providerID, childRemoteID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentTaskID == nil || *child.ParentTaskID != parent.ID {
		t.Fatalf("first child parent = %v, want %q", child.ParentTaskID, parent.ID)
	}

	fake.tasks[list.ID] = []domain.Task{
		{ID: "mapped-parent-2", ProviderID: providerID, ListID: list.ID, RemoteID: &parentRemoteID, Title: "Parent", Status: "todo", Priority: domain.PriorityNormal, SyncState: domain.SyncStateSynced},
		{ID: "mapped-child-2", ProviderID: providerID, ListID: list.ID, RemoteID: &childRemoteID, ParentTaskID: taskIDPointer("mapped-parent-2"), Title: "Child", Status: "todo", Priority: domain.PriorityNormal, SyncState: domain.SyncStateSynced},
	}
	if err := cached.Pull(ctx); err != nil {
		t.Fatalf("second Pull() error = %v", err)
	}
	parent, err = store.GetTaskByRemoteID(ctx, providerID, parentRemoteID)
	if err != nil {
		t.Fatal(err)
	}
	child, err = store.GetTaskByRemoteID(ctx, providerID, childRemoteID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentTaskID == nil || *child.ParentTaskID != parent.ID {
		t.Fatalf("second child parent = %v, want stable local ID %q", child.ParentTaskID, parent.ID)
	}
}

func taskIDPointer(value string) *domain.TaskID {
	id := domain.TaskID(value)
	return &id
}

func TestFoundationSyncMessageKeepsOperationFailureVisible(t *testing.T) {
	message, refresh, ok := foundationSyncMessage(foundationsync.Event{
		ProviderID: "clickup-work",
		Kind:       foundationsync.EventOperationFailed,
		Err:        domain.ErrProviderMismatch,
	})
	if !ok || refresh || message.State != foundationtui.SyncStateFailed || message.Error == "" {
		t.Fatalf("operation failure message = %#v, refresh=%v, ok=%v", message, refresh, ok)
	}
}

type foundationTestProvider struct {
	id           domain.ProviderID
	spaces       []domain.Space
	lists        map[domain.SpaceID][]domain.List
	tasks        map[domain.ListID][]domain.Task
	fetchedTasks map[domain.TaskID]domain.Task
	created      domain.Task
}

type foundationNoopHandler struct{}

func (foundationNoopHandler) Handle(context.Context, command.Command) (command.Event, error) {
	return command.Event{}, nil
}

var _ providerpkg.Provider = (*foundationTestProvider)(nil)

func (p *foundationTestProvider) ID() domain.ProviderID { return p.id }

func (p *foundationTestProvider) Type() domain.ProviderType { return domain.ProviderTypeClickUp }

func (p *foundationTestProvider) Capabilities() domain.Capabilities {
	return domain.Capabilities{
		RemoteSync:  true,
		FetchSpaces: true,
		FetchLists:  true,
		FetchTasks:  true,
		FetchTask:   true,
		CreateTask:  true,
		UpdateTask:  true,
		DeleteTask:  true,
	}
}

func (p *foundationTestProvider) FetchSpaces(context.Context) ([]domain.Space, error) {
	return p.spaces, nil
}

func (p *foundationTestProvider) FetchLists(_ context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	return p.lists[spaceID], nil
}

func (p *foundationTestProvider) FetchTasks(_ context.Context, listID domain.ListID) ([]domain.Task, error) {
	return p.tasks[listID], nil
}

func (p *foundationTestProvider) FetchTask(_ context.Context, taskID domain.TaskID) (domain.Task, error) {
	if task, ok := p.fetchedTasks[taskID]; ok {
		return task, nil
	}
	for _, tasks := range p.tasks {
		for _, task := range tasks {
			if task.ID == taskID {
				return task, nil
			}
		}
	}
	return domain.Task{}, domain.ErrNotFound
}

func (p *foundationTestProvider) CreateTask(context.Context, domain.Task) (domain.Task, error) {
	return p.created, nil
}

func (p *foundationTestProvider) UpdateTask(context.Context, domain.Task) (domain.Task, error) {
	return p.created, nil
}

func (p *foundationTestProvider) DeleteTask(context.Context, domain.Task) error { return nil }

func TestScopedRefreshKeepsTaskThatMovedOutOfCurrentList(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	providerID := domain.ProviderID("clickup-work")
	if _, err := store.CreateProvider(ctx, domain.Provider{ID: providerID, Type: domain.ProviderTypeClickUp, Name: "Work", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	space, err := store.CreateSpace(ctx, domain.Space{ID: "space", ProviderID: providerID, Name: "Work", SyncState: domain.SyncStateSynced})
	if err != nil {
		t.Fatal(err)
	}
	currentList, err := store.CreateList(ctx, domain.List{ID: "current-list", ProviderID: providerID, SpaceID: space.ID, RemoteID: bootstrapStringPointer("remote-current"), Name: "Current", SyncState: domain.SyncStateSynced})
	if err != nil {
		t.Fatal(err)
	}
	homeList, err := store.CreateList(ctx, domain.List{ID: "home-list", ProviderID: providerID, SpaceID: space.ID, RemoteID: bootstrapStringPointer("remote-home"), Name: "Home", SyncState: domain.SyncStateSynced})
	if err != nil {
		t.Fatal(err)
	}
	remoteTaskID := "remote-task"
	task, err := store.CreateTask(ctx, domain.Task{
		ID: "task", ProviderID: providerID, ListID: currentList.ID, ListIDs: []domain.ListID{currentList.ID},
		RemoteID: &remoteTaskID, Title: "Move me", Status: "todo", Priority: domain.PriorityNormal,
		SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &foundationTestProvider{
		id:     providerID,
		spaces: []domain.Space{{ID: space.ID, ProviderID: providerID, RemoteID: bootstrapStringPointer("remote-space"), Name: space.Name, SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now}},
		lists:  map[domain.SpaceID][]domain.List{space.ID: {currentList, homeList}},
		tasks: map[domain.ListID][]domain.Task{
			currentList.ID: {{
				ID: task.ID, ProviderID: providerID, ListID: currentList.ID, ListIDs: []domain.ListID{currentList.ID},
				RemoteID: &remoteTaskID, Title: task.Title, Status: task.Status, Priority: task.Priority,
				SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now,
			}},
		},
	}
	cached := newCachedProvider(fake, store)
	cached.SetActiveTaskList(currentList.ID)
	if err := cached.Pull(ctx); err != nil {
		t.Fatalf("initial Pull() error = %v", err)
	}

	fake.tasks[currentList.ID] = nil
	fake.fetchedTasks = map[domain.TaskID]domain.Task{
		task.ID: {
			ID: task.ID, ProviderID: providerID, ListID: homeList.ID, ListIDs: []domain.ListID{homeList.ID},
			RemoteID: &remoteTaskID, Title: task.Title, Status: task.Status, Priority: task.Priority,
			SyncState: domain.SyncStateSynced, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := cached.Pull(ctx); err != nil {
		t.Fatalf("scoped Pull() after move error = %v", err)
	}
	updated, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.IsDeleted || updated.ListID != homeList.ID || !updated.HasList(homeList.ID) {
		t.Fatalf("task after scoped refresh = %#v, want active in home list", updated)
	}
}

func bootstrapStringPointer(value string) *string { return &value }
