package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type fakeRepository struct {
	spaces map[SpaceID]Space
	lists  map[ListID]List
	tasks  map[TaskID]Task

	intents   []*SyncIntent
	mutations []string

	createTaskErr error
	deleteTaskErr error
	searchResults []TaskSearchResult
	filterResults []TaskSearchResult
	searchCalls   int
	filterCalls   int
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		spaces: make(map[SpaceID]Space),
		lists:  make(map[ListID]List),
		tasks:  make(map[TaskID]Task),
	}
}

func (r *fakeRepository) GetSpace(_ context.Context, id SpaceID) (Space, error) {
	space, ok := r.spaces[id]
	if !ok {
		return Space{}, fmt.Errorf("space %s: %w", id, ErrNotFound)
	}
	return cloneSpace(space), nil
}

func (r *fakeRepository) GetList(_ context.Context, id ListID) (List, error) {
	list, ok := r.lists[id]
	if !ok {
		return List{}, fmt.Errorf("list %s: %w", id, ErrNotFound)
	}
	return cloneList(list), nil
}

func (r *fakeRepository) GetTask(_ context.Context, id TaskID) (Task, error) {
	task, ok := r.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	return cloneTask(task), nil
}

func (r *fakeRepository) ListTasks(_ context.Context, listID ListID) ([]Task, error) {
	var tasks []Task
	for _, task := range r.tasks {
		if task.ListID == listID {
			tasks = append(tasks, cloneTask(task))
		}
	}
	return tasks, nil
}

func (r *fakeRepository) SearchTasks(_ context.Context, _ string) ([]TaskSearchResult, error) {
	r.searchCalls++
	return cloneSearchResults(r.searchResults), nil
}

func (r *fakeRepository) FilterTasks(_ context.Context, _ TaskFilter) ([]TaskSearchResult, error) {
	r.filterCalls++
	return cloneSearchResults(r.filterResults), nil
}

func (r *fakeRepository) CreateSpace(_ context.Context, space Space, intent *SyncIntent) (Space, error) {
	r.mutations = append(r.mutations, "create_space")
	r.recordIntent(intent)
	r.spaces[space.ID] = cloneSpace(space)
	return cloneSpace(space), nil
}

func (r *fakeRepository) CreateList(_ context.Context, list List, intent *SyncIntent) (List, error) {
	r.mutations = append(r.mutations, "create_list")
	r.recordIntent(intent)
	r.lists[list.ID] = cloneList(list)
	return cloneList(list), nil
}

func (r *fakeRepository) CreateTask(_ context.Context, task Task, intent *SyncIntent) (Task, error) {
	if r.createTaskErr != nil {
		return Task{}, r.createTaskErr
	}
	r.mutations = append(r.mutations, "create_task")
	r.recordIntent(intent)
	r.tasks[task.ID] = cloneTask(task)
	return cloneTask(task), nil
}

func (r *fakeRepository) UpdateTask(_ context.Context, task Task, intent *SyncIntent) (Task, error) {
	r.mutations = append(r.mutations, "update_task")
	r.recordIntent(intent)
	r.tasks[task.ID] = cloneTask(task)
	return cloneTask(task), nil
}

func (r *fakeRepository) MoveTask(_ context.Context, task Task, intent *SyncIntent) (Task, error) {
	r.mutations = append(r.mutations, "move_task")
	r.recordIntent(intent)
	r.tasks[task.ID] = cloneTask(task)
	return cloneTask(task), nil
}

func (r *fakeRepository) DeleteTask(_ context.Context, task Task, intent *SyncIntent) error {
	if r.deleteTaskErr != nil {
		return r.deleteTaskErr
	}
	r.mutations = append(r.mutations, "delete_task")
	r.recordIntent(intent)
	delete(r.tasks, task.ID)
	return nil
}

func (r *fakeRepository) recordIntent(intent *SyncIntent) {
	if intent == nil {
		return
	}
	copy := *intent
	copy.Payload = append([]byte(nil), intent.Payload...)
	r.intents = append(r.intents, &copy)
}

type fakeIDs struct {
	ids   []string
	index int
}

func (g *fakeIDs) NewID(_ EntityType) (string, error) {
	if g.index >= len(g.ids) {
		return "", errors.New("no test identity available")
	}
	id := g.ids[g.index]
	g.index++
	return id, nil
}

type fakeRegistry struct {
	providers map[ProviderID]Provider
	getCalls  int
}

type fakeProvider struct {
	id    ProviderID
	typ   ProviderType
	caps  Capabilities
	calls int
}

func (p *fakeProvider) ID() ProviderID { return p.id }

func (p *fakeProvider) Type() ProviderType { return p.typ }

func (p *fakeProvider) Capabilities() Capabilities { return p.caps }

func (p *fakeProvider) FetchSpaces(context.Context) ([]Space, error) {
	p.calls++
	return nil, errors.New("provider fetch must not be called")
}

func (p *fakeProvider) FetchLists(context.Context, SpaceID) ([]List, error) {
	p.calls++
	return nil, errors.New("provider fetch must not be called")
}

func (p *fakeProvider) FetchTasks(context.Context, ListID) ([]Task, error) {
	p.calls++
	return nil, errors.New("provider fetch must not be called")
}

func (p *fakeProvider) FetchTask(context.Context, TaskID) (Task, error) {
	p.calls++
	return Task{}, errors.New("provider fetch must not be called")
}

func (p *fakeProvider) CreateTask(context.Context, Task) (Task, error) {
	p.calls++
	return Task{}, errors.New("provider mutation must not be called")
}

func (p *fakeProvider) UpdateTask(context.Context, Task) (Task, error) {
	p.calls++
	return Task{}, errors.New("provider mutation must not be called")
}

func (p *fakeProvider) DeleteTask(context.Context, Task) error {
	p.calls++
	return errors.New("provider mutation must not be called")
}

func (r *fakeRegistry) Get(id ProviderID) (Provider, bool) {
	r.getCalls++
	provider, ok := r.providers[id]
	return provider, ok
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

func fullProvider(id ProviderID, providerType ProviderType) Provider {
	return &fakeProvider{id: id, typ: providerType, caps: AllCapabilities()}
}

func addHierarchy(r *fakeRepository, providerID ProviderID, spaceID SpaceID, listID ListID) {
	r.spaces[spaceID] = Space{ID: spaceID, ProviderID: providerID, Name: string(spaceID), SyncState: SyncStateSynced}
	r.lists[listID] = List{ID: listID, ProviderID: providerID, SpaceID: spaceID, Name: string(listID), SyncState: SyncStateSynced}
}

func newServiceForTest(r *fakeRepository, providers ...Provider) *Service {
	registry := NewRegistry(providers...)
	return NewService(r, registry,
		WithClock(fixedClock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}),
		WithIDGenerator(&fakeIDs{ids: []string{"generated-space", "generated-list", "generated-task", "generated-copy"}}),
	)
}

func TestCreateRemoteTaskPersistsAtomicQueueIntent(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "remote", "space-remote", "list-remote")
	s := newServiceForTest(r, fullProvider("remote", "clickup"))

	task, err := s.CreateTask(context.Background(), CreateTaskInput{
		ProviderID:  "remote",
		ListID:      "list-remote",
		Title:       "write tests",
		Description: "keep the operation local-first",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if task.SyncState != SyncStatePending {
		t.Fatalf("SyncState = %q, want %q", task.SyncState, SyncStatePending)
	}
	if len(r.intents) != 1 {
		t.Fatalf("recorded %d intents, want 1", len(r.intents))
	}
	intent := r.intents[0]
	if intent.ProviderID != "remote" || intent.EntityType != EntityTypeTask || intent.EntityID != string(task.ID) || intent.Operation != OperationCreate {
		t.Fatalf("unexpected intent: %+v", intent)
	}
	var payload TaskCreatePayload
	if err := json.Unmarshal(intent.Payload, &payload); err != nil {
		t.Fatalf("decode intent payload: %v", err)
	}
	if payload.Title != task.Title || payload.ListID != task.ListID {
		t.Fatalf("unexpected task payload: %+v", payload)
	}
	if got := r.mutations; !reflect.DeepEqual(got, []string{"create_task"}) {
		t.Fatalf("mutations = %v, want one atomic create", got)
	}
}

func TestLocalMutationHasNoQueueIntentAndQueriesStayLocal(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "local", "space-local", "list-local")
	localProvider := &fakeProvider{id: "local", typ: ProviderTypeLocal, caps: AllCapabilities()}
	registry := &fakeRegistry{providers: map[ProviderID]Provider{
		"local": localProvider,
	}}
	s := NewService(r, registry,
		WithClock(fixedClock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}),
		WithIDGenerator(&fakeIDs{ids: []string{"local-task"}}),
	)

	task, err := s.CreateTask(context.Background(), CreateTaskInput{
		ProviderID: "local",
		ListID:     "list-local",
		Title:      "work offline",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if task.SyncState != SyncStateLocal {
		t.Fatalf("SyncState = %q, want %q", task.SyncState, SyncStateLocal)
	}
	if len(r.intents) != 0 {
		t.Fatalf("local create recorded %d queue intents", len(r.intents))
	}

	r.searchResults = []TaskSearchResult{{Task: task}}
	if _, err := s.SearchTasks(context.Background(), "offline"); err != nil {
		t.Fatalf("SearchTasks() error = %v", err)
	}
	if _, err := s.FilterTasks(context.Background(), TaskFilter{ListID: ptr(ListID("list-local"))}); err != nil {
		t.Fatalf("FilterTasks() error = %v", err)
	}
	if registry.getCalls != 1 {
		t.Fatalf("provider lookups = %d after local mutation and local queries, want 1", registry.getCalls)
	}
	if localProvider.calls != 0 {
		t.Fatalf("local provider API calls = %d, want 0", localProvider.calls)
	}
	if r.searchCalls != 1 || r.filterCalls != 1 {
		t.Fatalf("query calls = (%d, %d), want (1, 1)", r.searchCalls, r.filterCalls)
	}
}

func TestProviderIsolationRejectsCrossProviderHierarchyAndMove(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "provider-a", "space-a", "list-a")
	addHierarchy(r, "provider-b", "space-b", "list-b")
	r.tasks["task-a"] = Task{
		ID:         "task-a",
		ProviderID: "provider-a",
		ListID:     "list-a",
		Title:      "owned by a",
		Status:     StatusTodo,
	}
	r.tasks["parent-b"] = Task{
		ID:         "parent-b",
		ProviderID: "provider-b",
		ListID:     "list-b",
		Title:      "owned by b",
		Status:     StatusTodo,
	}
	s := newServiceForTest(r, fullProvider("provider-a", "clickup"), fullProvider("provider-b", "clickup"))

	if _, err := s.CreateList(context.Background(), CreateListInput{ProviderID: "provider-b", SpaceID: "space-a", Name: "invalid"}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("CreateList() error = %v, want provider mismatch", err)
	}
	if _, err := s.CreateTask(context.Background(), CreateTaskInput{ProviderID: "provider-b", ListID: "list-a", Title: "invalid"}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("CreateTask() error = %v, want provider mismatch", err)
	}
	if _, err := s.CreateTask(context.Background(), CreateTaskInput{ProviderID: "provider-a", ListID: "list-a", ParentTaskID: ptr(TaskID("parent-b")), Title: "invalid"}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("CreateTask(parent) error = %v, want provider mismatch", err)
	}
	if _, err := s.MoveTask(context.Background(), "task-a", "list-b"); !errors.Is(err, ErrCrossProviderMove) {
		t.Fatalf("MoveTask() error = %v, want explicit cross-provider rejection", err)
	}
	if len(r.mutations) != 0 {
		t.Fatalf("cross-provider attempts mutated repository: %v", r.mutations)
	}
	if got := r.tasks["task-a"]; got.ProviderID != "provider-a" || got.ListID != "list-a" {
		t.Fatalf("source task changed after rejected move: %+v", got)
	}
}

func TestMoveTaskUsesLocalMutationForSameProvider(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "provider", "space", "source-list")
	r.spaces["space-2"] = Space{ID: "space-2", ProviderID: "provider", Name: "second space", SyncState: SyncStateSynced}
	r.lists["destination-list"] = List{ID: "destination-list", ProviderID: "provider", SpaceID: "space-2", Name: "destination", SyncState: SyncStateSynced}
	r.tasks["task"] = Task{ID: "task", ProviderID: "provider", ListID: "source-list", Title: "move me", Status: StatusTodo, Priority: PriorityNormal, SyncState: SyncStateSynced}
	provider := &fakeProvider{id: "provider", typ: ProviderTypeLocal, caps: AllCapabilities()}
	s := newServiceForTest(r, provider)

	moved, err := s.MoveTask(context.Background(), "task", "destination-list")
	if err != nil {
		t.Fatalf("MoveTask() error = %v", err)
	}
	if moved.ProviderID != "provider" || moved.ListID != "destination-list" || moved.SyncState != SyncStateLocal {
		t.Fatalf("moved task = %+v", moved)
	}
	if len(r.intents) != 0 {
		t.Fatalf("local move recorded queue intents: %+v", r.intents)
	}
	if !reflect.DeepEqual(r.mutations, []string{"move_task"}) {
		t.Fatalf("mutations = %v, want one local move", r.mutations)
	}
	if provider.calls != 0 {
		t.Fatalf("provider API calls = %d, want 0", provider.calls)
	}
}

func TestCopyTaskCreatesIndependentDestinationIdentity(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "source-provider", "source-space", "source-list")
	addHierarchy(r, "destination-provider", "destination-space", "destination-list")
	remoteID := "remote-task-1"
	parentID := TaskID("parent-source")
	remoteUpdated := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	r.tasks["parent-source"] = Task{ID: parentID, ProviderID: "source-provider", ListID: "source-list", Title: "parent", Status: StatusTodo}
	r.tasks["source-task"] = Task{
		ID:              "source-task",
		ProviderID:      "source-provider",
		ListID:          "source-list",
		RemoteID:        &remoteID,
		ParentTaskID:    &parentID,
		Title:           "copy me",
		Description:     "normalized fields only",
		Status:          StatusDone,
		Priority:        PriorityHigh,
		CompletedAt:     &remoteUpdated,
		SyncState:       SyncStateSynced,
		RemoteUpdatedAt: &remoteUpdated,
	}
	s := newServiceForTest(r, fullProvider("source-provider", "clickup"), fullProvider("destination-provider", "local"))

	copied, err := s.CopyTask(context.Background(), "source-task", "destination-list")
	if err != nil {
		t.Fatalf("CopyTask() error = %v", err)
	}
	if copied.ID != "generated-space" {
		t.Fatalf("destination ID = %q, want generated identity", copied.ID)
	}
	if copied.ProviderID != "destination-provider" || copied.ListID != "destination-list" {
		t.Fatalf("destination ownership = (%q, %q)", copied.ProviderID, copied.ListID)
	}
	if copied.RemoteID != nil || copied.ParentTaskID != nil || copied.RemoteUpdatedAt != nil {
		t.Fatalf("destination copied provider-owned state: %+v", copied)
	}
	if copied.SyncState != SyncStateLocal {
		t.Fatalf("destination SyncState = %q, want local", copied.SyncState)
	}
	if copied.Title != "copy me" || copied.Status != StatusDone || copied.Priority != PriorityHigh {
		t.Fatalf("compatible fields were not copied: %+v", copied)
	}
	source := r.tasks["source-task"]
	if source.ProviderID != "source-provider" || source.RemoteID == nil || source.ParentTaskID == nil {
		t.Fatalf("source changed during copy: %+v", source)
	}
}

func TestTransferTaskRetainsDestinationOnSourceDeleteFailure(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "source-provider", "source-space", "source-list")
	addHierarchy(r, "destination-provider", "destination-space", "destination-list")
	r.tasks["source-task"] = Task{
		ID:           "source-task",
		ProviderID:   "source-provider",
		ListID:       "source-list",
		RemoteID:     ptrString("source-remote"),
		ParentTaskID: ptr(TaskID("source-parent")),
		Title:        "transfer me",
		Status:       StatusTodo,
	}
	r.tasks["source-parent"] = Task{ID: "source-parent", ProviderID: "source-provider", ListID: "source-list", Title: "parent", Status: StatusTodo}
	r.deleteTaskErr = errors.New("source delete unavailable")
	s := newServiceForTest(r, fullProvider("source-provider", "clickup"), fullProvider("destination-provider", "local"))

	destination, err := s.TransferTask(context.Background(), "source-task", "destination-list")
	if !errors.Is(err, ErrPartialTransfer) {
		t.Fatalf("TransferTask() error = %v, want partial transfer", err)
	}
	if destination.ID != "generated-space" {
		t.Fatalf("returned destination = %+v, want created destination", destination)
	}
	if _, ok := r.tasks[destination.ID]; !ok {
		t.Fatalf("destination was not retained after source delete failure")
	}
	source := r.tasks["source-task"]
	if source.ProviderID != "source-provider" || source.ListID != "source-list" {
		t.Fatalf("source ownership changed after partial transfer: %+v", source)
	}
	if len(r.intents) != 0 {
		t.Fatalf("local destination unexpectedly queued intents: %d", len(r.intents))
	}
}

func TestPatchTaskPreservesOmittedFieldsAndQueuesPatch(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "remote", "space-remote", "list-remote")
	due := time.Date(2026, time.January, 3, 0, 0, 0, 0, time.UTC)
	parentID := TaskID("parent")
	remoteID := "remote-id"
	r.tasks[parentID] = Task{ID: parentID, ProviderID: "remote", ListID: "list-remote", Title: "parent", Status: StatusTodo}
	r.tasks["task"] = Task{
		ID:           "task",
		ProviderID:   "remote",
		ListID:       "list-remote",
		RemoteID:     &remoteID,
		ParentTaskID: &parentID,
		Title:        "old title",
		Description:  "old description",
		Status:       StatusTodo,
		Priority:     PriorityHigh,
		DueAt:        &due,
		SyncState:    SyncStateSynced,
	}
	s := newServiceForTest(r, fullProvider("remote", "clickup"))
	newTitle := "new title"

	updated, err := s.PatchTask(context.Background(), "task", TaskPatch{Title: &newTitle})
	if err != nil {
		t.Fatalf("PatchTask() error = %v", err)
	}
	if updated.Title != newTitle || updated.Description != "old description" || updated.Status != StatusTodo || updated.Priority != PriorityHigh {
		t.Fatalf("omitted fields changed: %+v", updated)
	}
	if updated.DueAt == nil || !updated.DueAt.Equal(due) || updated.ParentTaskID == nil || *updated.ParentTaskID != parentID {
		t.Fatalf("nullable omitted fields changed: %+v", updated)
	}
	if updated.RemoteID == nil || *updated.RemoteID != remoteID || updated.SyncState != SyncStatePending {
		t.Fatalf("provider-owned state changed incorrectly: %+v", updated)
	}
	if len(r.intents) != 1 || r.intents[0].Operation != OperationUpdate {
		t.Fatalf("patch intents = %+v, want one update intent", r.intents)
	}
	var payload TaskUpdatePayload
	if err := json.Unmarshal(r.intents[0].Payload, &payload); err != nil {
		t.Fatalf("decode patch payload: %v", err)
	}
	if payload.Title == nil || *payload.Title != newTitle || payload.Description != nil || payload.Priority != nil {
		t.Fatalf("patch payload included omitted fields: %+v", payload)
	}

	r.intents = nil
	updated, err = s.PatchTask(context.Background(), "task", TaskPatch{ClearDueAt: true, ClearParentTask: true})
	if err != nil {
		t.Fatalf("PatchTask(clear) error = %v", err)
	}
	if updated.DueAt != nil || updated.ParentTaskID != nil {
		t.Fatalf("explicit clears were ignored: %+v", updated)
	}
}

func TestCompleteAndDeleteUseRemoteQueueWithoutProviderCalls(t *testing.T) {
	r := newFakeRepository()
	addHierarchy(r, "remote", "space-remote", "list-remote")
	r.tasks["task"] = Task{ID: "task", ProviderID: "remote", ListID: "list-remote", Title: "complete", Status: StatusTodo}
	registry := &fakeRegistry{providers: map[ProviderID]Provider{"remote": fullProvider("remote", "clickup")}}
	s := NewService(r, registry, WithClock(fixedClock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}))

	completed, err := s.CompleteTask(context.Background(), "task")
	if err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	if completed.Status != StatusDone || completed.CompletedAt == nil || completed.SyncState != SyncStatePending {
		t.Fatalf("completion = %+v", completed)
	}
	if err := s.DeleteTask(context.Background(), "task"); err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if _, ok := r.tasks["task"]; ok {
		t.Fatalf("task still exists after local delete mutation")
	}
	if len(r.intents) != 2 || r.intents[0].Operation != OperationUpdate || r.intents[1].Operation != OperationDelete {
		t.Fatalf("queue intents = %+v, want update and delete", r.intents)
	}
	if registry.getCalls != 2 {
		t.Fatalf("provider lookups = %d, want metadata-only lookups", registry.getCalls)
	}
}

func ptr[T any](value T) *T { return &value }

func ptrString(value string) *string { return &value }
