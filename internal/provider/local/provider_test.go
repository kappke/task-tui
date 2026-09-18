package local

import (
	"context"
	"errors"
	"testing"

	"github.com/kappke/task-tui/internal/domain"
)

type memorySpaceRepository struct {
	spaces map[domain.SpaceID]domain.Space
}

func newMemorySpaceRepository() *memorySpaceRepository {
	return &memorySpaceRepository{spaces: make(map[domain.SpaceID]domain.Space)}
}

func (r *memorySpaceRepository) Get(_ context.Context, id domain.SpaceID) (domain.Space, error) {
	space, ok := r.spaces[id]
	if !ok {
		return domain.Space{}, errors.New("space not found")
	}
	return space, nil
}

func (r *memorySpaceRepository) ListByProvider(_ context.Context, providerID domain.ProviderID) ([]domain.Space, error) {
	spaces := make([]domain.Space, 0, len(r.spaces))
	for _, space := range r.spaces {
		if space.ProviderID == providerID {
			spaces = append(spaces, space)
		}
	}
	return spaces, nil
}

func (r *memorySpaceRepository) GetByRemoteID(_ context.Context, providerID domain.ProviderID, remoteID string) (domain.Space, error) {
	for _, space := range r.spaces {
		if space.ProviderID == providerID && space.RemoteID != nil && *space.RemoteID == remoteID {
			return space, nil
		}
	}
	return domain.Space{}, errors.New("space not found")
}

func (r *memorySpaceRepository) Create(_ context.Context, space domain.Space) (domain.Space, error) {
	r.spaces[space.ID] = space
	return space, nil
}

func (r *memorySpaceRepository) Update(_ context.Context, space domain.Space) (domain.Space, error) {
	r.spaces[space.ID] = space
	return space, nil
}

func (r *memorySpaceRepository) Delete(_ context.Context, id domain.SpaceID) error {
	delete(r.spaces, id)
	return nil
}

type memoryListRepository struct {
	lists map[domain.ListID]domain.List
}

func newMemoryListRepository() *memoryListRepository {
	return &memoryListRepository{lists: make(map[domain.ListID]domain.List)}
}

func (r *memoryListRepository) Get(_ context.Context, id domain.ListID) (domain.List, error) {
	list, ok := r.lists[id]
	if !ok {
		return domain.List{}, errors.New("list not found")
	}
	return list, nil
}

func (r *memoryListRepository) ListBySpace(_ context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	lists := make([]domain.List, 0, len(r.lists))
	for _, list := range r.lists {
		if list.SpaceID == spaceID {
			lists = append(lists, list)
		}
	}
	return lists, nil
}

func (r *memoryListRepository) ListByProvider(_ context.Context, providerID domain.ProviderID) ([]domain.List, error) {
	lists := make([]domain.List, 0, len(r.lists))
	for _, list := range r.lists {
		if list.ProviderID == providerID {
			lists = append(lists, list)
		}
	}
	return lists, nil
}

func (r *memoryListRepository) GetByRemoteID(_ context.Context, providerID domain.ProviderID, remoteID string) (domain.List, error) {
	for _, list := range r.lists {
		if list.ProviderID == providerID && list.RemoteID != nil && *list.RemoteID == remoteID {
			return list, nil
		}
	}
	return domain.List{}, errors.New("list not found")
}

func (r *memoryListRepository) Create(_ context.Context, list domain.List) (domain.List, error) {
	r.lists[list.ID] = list
	return list, nil
}

func (r *memoryListRepository) Update(_ context.Context, list domain.List) (domain.List, error) {
	r.lists[list.ID] = list
	return list, nil
}

func (r *memoryListRepository) Delete(_ context.Context, id domain.ListID) error {
	delete(r.lists, id)
	return nil
}

type memoryTaskRepository struct {
	tasks map[domain.TaskID]domain.Task
}

func newMemoryTaskRepository() *memoryTaskRepository {
	return &memoryTaskRepository{tasks: make(map[domain.TaskID]domain.Task)}
}

func (r *memoryTaskRepository) Get(_ context.Context, id domain.TaskID) (domain.Task, error) {
	task, ok := r.tasks[id]
	if !ok {
		return domain.Task{}, errors.New("task not found")
	}
	return task, nil
}

func (r *memoryTaskRepository) ListByList(_ context.Context, listID domain.ListID) ([]domain.Task, error) {
	tasks := make([]domain.Task, 0, len(r.tasks))
	for _, task := range r.tasks {
		if task.ListID == listID {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

func (r *memoryTaskRepository) ListByProvider(_ context.Context, providerID domain.ProviderID) ([]domain.Task, error) {
	tasks := make([]domain.Task, 0, len(r.tasks))
	for _, task := range r.tasks {
		if task.ProviderID == providerID {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

func (r *memoryTaskRepository) GetByRemoteID(_ context.Context, providerID domain.ProviderID, remoteID string) (domain.Task, error) {
	for _, task := range r.tasks {
		if task.ProviderID == providerID && task.RemoteID != nil && *task.RemoteID == remoteID {
			return task, nil
		}
	}
	return domain.Task{}, errors.New("task not found")
}

func (r *memoryTaskRepository) Create(_ context.Context, task domain.Task) (domain.Task, error) {
	r.tasks[task.ID] = task
	return task, nil
}

func (r *memoryTaskRepository) Update(_ context.Context, task domain.Task) (domain.Task, error) {
	r.tasks[task.ID] = task
	return task, nil
}

func (r *memoryTaskRepository) Delete(_ context.Context, id domain.TaskID) error {
	delete(r.tasks, id)
	return nil
}

func newProvider() (*Local, *memorySpaceRepository, *memoryListRepository, *memoryTaskRepository) {
	spaces := newMemorySpaceRepository()
	lists := newMemoryListRepository()
	tasks := newMemoryTaskRepository()
	provider := New(Repositories{
		Spaces:      spaces,
		SpaceWriter: spaces,
		Lists:       lists,
		ListWriter:  lists,
		Tasks:       tasks,
		TaskWriter:  tasks,
	})
	return provider, spaces, lists, tasks
}

func TestLocalProviderWorksWithoutNetwork(t *testing.T) {
	provider, _, _, _ := newProvider()
	ctx := context.Background()

	if provider.ID() != DefaultProviderID {
		t.Fatalf("default provider ID = %q, want %q", provider.ID(), DefaultProviderID)
	}
	if provider.Type() != domain.ProviderTypeLocal {
		t.Fatalf("provider type = %q, want %q", provider.Type(), domain.ProviderTypeLocal)
	}
	capabilities := provider.Capabilities()
	if !capabilities.FetchSpaces || !capabilities.FetchLists || !capabilities.FetchTasks || !capabilities.FetchTask {
		t.Fatalf("local provider read capabilities are incomplete: %+v", capabilities)
	}
	if !capabilities.CreateSpace || !capabilities.UpdateSpace || !capabilities.DeleteSpace ||
		!capabilities.CreateList || !capabilities.UpdateList || !capabilities.DeleteList ||
		!capabilities.CreateTask || !capabilities.UpdateTask || !capabilities.DeleteTask {
		t.Fatalf("local provider write capabilities are incomplete: %+v", capabilities)
	}

	if err := provider.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := provider.Authenticate(ctx); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	space, err := provider.CreateSpace(ctx, domain.Space{Name: "Personal"})
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	list, err := provider.CreateList(ctx, domain.List{SpaceID: space.ID, Name: "Today"})
	if err != nil {
		t.Fatalf("create list: %v", err)
	}
	task, err := provider.CreateTask(ctx, domain.Task{ListID: list.ID, Title: "Work offline"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := provider.FetchTask(ctx, task.ID); err != nil {
		t.Fatalf("fetch task: %v", err)
	}
	task.Title = "Updated offline"
	updated, err := provider.UpdateTask(ctx, task)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if updated.Title != "Updated offline" || updated.ProviderID != provider.ID() {
		t.Fatalf("updated task = %+v", updated)
	}
	if err := provider.DeleteTask(ctx, updated); err != nil {
		t.Fatalf("delete task: %v", err)
	}
}

func TestLocalProviderPreservesConfiguredProviderIDAndLocalState(t *testing.T) {
	spaces := newMemorySpaceRepository()
	lists := newMemoryListRepository()
	tasks := newMemoryTaskRepository()
	provider := NewWithConfig(Config{ProviderID: "personal-local"}, Repositories{
		Spaces:      spaces,
		SpaceWriter: spaces,
		Lists:       lists,
		ListWriter:  lists,
		Tasks:       tasks,
		TaskWriter:  tasks,
	})
	ctx := context.Background()

	space, err := provider.CreateSpace(ctx, domain.Space{RemoteID: stringPointer("remote-space")})
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	list, err := provider.CreateList(ctx, domain.List{SpaceID: space.ID, RemoteID: stringPointer("remote-list")})
	if err != nil {
		t.Fatalf("create list: %v", err)
	}
	task, err := provider.CreateTask(ctx, domain.Task{ListID: list.ID, RemoteID: stringPointer("remote-task")})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if got := provider.ID(); got != domain.ProviderID("personal-local") {
		t.Fatalf("provider ID = %q, want personal-local", got)
	}
	for name, entity := range map[string]struct {
		providerID domain.ProviderID
		remoteID   *string
		syncState  domain.SyncState
	}{
		"space": {space.ProviderID, space.RemoteID, space.SyncState},
		"list":  {list.ProviderID, list.RemoteID, list.SyncState},
		"task":  {task.ProviderID, task.RemoteID, task.SyncState},
	} {
		if entity.providerID != provider.ID() {
			t.Errorf("%s provider ID = %q, want %q", name, entity.providerID, provider.ID())
		}
		if entity.remoteID != nil {
			t.Errorf("%s remote ID = %q, want nil", name, *entity.remoteID)
		}
		if entity.syncState != domain.SyncStateLocal {
			t.Errorf("%s sync state = %q, want local", name, entity.syncState)
		}
	}
}

func TestLocalProviderRejectsForeignHierarchyEntities(t *testing.T) {
	provider, spaces, lists, tasks := newProvider()
	ctx := context.Background()
	foreignProvider := domain.ProviderID("clickup-work")

	foreignSpace := domain.Space{ID: "foreign-space", ProviderID: foreignProvider, Name: "Work"}
	if _, err := spaces.Create(ctx, foreignSpace); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.CreateList(ctx, domain.List{SpaceID: foreignSpace.ID, Name: "Should fail"}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("create list with foreign space error = %v, want provider mismatch", err)
	}
	if _, err := provider.FetchLists(ctx, foreignSpace.ID); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("fetch foreign space error = %v, want provider mismatch", err)
	}

	localSpace, err := provider.CreateSpace(ctx, domain.Space{Name: "Personal"})
	if err != nil {
		t.Fatal(err)
	}
	localList, err := provider.CreateList(ctx, domain.List{SpaceID: localSpace.ID, Name: "Today"})
	if err != nil {
		t.Fatal(err)
	}
	foreignParent := domain.Task{ID: "foreign-parent", ProviderID: foreignProvider, ListID: localList.ID, Title: "Work"}
	if _, err := tasks.Create(ctx, foreignParent); err != nil {
		t.Fatal(err)
	}
	parentID := foreignParent.ID
	if _, err := provider.CreateTask(ctx, domain.Task{ListID: localList.ID, ParentTaskID: &parentID}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("create task with foreign parent error = %v, want provider mismatch", err)
	}

	foreignList := domain.List{ID: "foreign-list", ProviderID: foreignProvider, SpaceID: localSpace.ID, Name: "Work"}
	if _, err := lists.Create(ctx, foreignList); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.CreateTask(ctx, domain.Task{ListID: foreignList.ID, Title: "Should fail"}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("create task with foreign list error = %v, want provider mismatch", err)
	}
	if _, err := provider.FetchTasks(ctx, foreignList.ID); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("fetch foreign list error = %v, want provider mismatch", err)
	}

	foreignTask := domain.Task{ID: "foreign-task", ProviderID: foreignProvider, ListID: foreignList.ID, Title: "Work"}
	if _, err := tasks.Create(ctx, foreignTask); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpdateTask(ctx, foreignTask); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("update foreign task error = %v, want provider mismatch", err)
	}
	if err := provider.DeleteTask(ctx, foreignTask); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("delete foreign task error = %v, want provider mismatch", err)
	}
}

func stringPointer(value string) *string {
	return &value
}
