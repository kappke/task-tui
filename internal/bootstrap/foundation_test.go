package bootstrap

import (
	"bytes"
	"context"
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
	if !strings.Contains(output.String(), "ClickUp") || !strings.Contains(output.String(), "Local") {
		t.Fatalf("initial frame does not contain both providers: %q", output.String())
	}
	if count := strings.Count(output.String(), "TASK MANAGER"); count != 1 {
		t.Fatalf("headless render count = %d, want one frame", count)
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
	fake.tasks = map[domain.ListID][]domain.Task{
		fetchedListID: {{
			ID:              domain.TaskID("task-fetched"),
			ProviderID:      providerID,
			ListID:          fetchedListID,
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
	fake.tasks[fetchedListID][0].Title = "Remote overwrite"
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
	id      domain.ProviderID
	spaces  []domain.Space
	lists   map[domain.SpaceID][]domain.List
	tasks   map[domain.ListID][]domain.Task
	created domain.Task
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
