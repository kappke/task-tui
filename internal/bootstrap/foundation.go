package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kappke/task-tui/internal/app"
	"github.com/kappke/task-tui/internal/command"
	"github.com/kappke/task-tui/internal/domain"
	providerpkg "github.com/kappke/task-tui/internal/provider"
	clickuppkg "github.com/kappke/task-tui/internal/provider/clickup"
	localpkg "github.com/kappke/task-tui/internal/provider/local"
	repositorypkg "github.com/kappke/task-tui/internal/repository"
	"github.com/kappke/task-tui/internal/storage/sqlite"
	foundationsync "github.com/kappke/task-tui/internal/sync"
	foundationtui "github.com/kappke/task-tui/internal/tui"
)

const foundationUIStateKey = "main"

// foundationDataStore adapts the authoritative SQLite store to the small
// lifecycle persistence contract. All snapshots are assembled from local rows.
type foundationDataStore struct {
	store *sqlite.Store
}

var _ DataStore = (*foundationDataStore)(nil)

func newFoundationDataStore(store *sqlite.Store) *foundationDataStore {
	return &foundationDataStore{store: store}
}

func (s *foundationDataStore) Snapshot(ctx context.Context) (View, error) {
	if s == nil || s.store == nil {
		return View{}, errors.New("snapshot local data: store unavailable")
	}
	if ctx == nil {
		return View{}, errors.New("snapshot local data: nil context")
	}
	providers, err := s.store.ListProviders(ctx)
	if err != nil {
		return View{}, fmt.Errorf("list providers: %w", err)
	}
	for index := range providers {
		state, err := s.store.GetProviderSyncState(ctx, providers[index].ID.String())
		if err != nil {
			return View{}, fmt.Errorf("load sync state for provider %s: %w", providers[index].ID, err)
		}
		providers[index].SyncState = domain.SyncState(state.State)
		providers[index].SyncError = errorText(state.Error)
		providers[index].LastSyncAt = state.LastSyncAt
	}
	spaces := make([]domain.Space, 0)
	lists := make([]domain.List, 0)
	tasks := make([]domain.Task, 0)
	for _, provider := range providers {
		providerSpaces, err := s.store.ListSpaces(ctx, provider.ID)
		if err != nil {
			return View{}, fmt.Errorf("list spaces for provider %s: %w", provider.ID, err)
		}
		spaces = append(spaces, providerSpaces...)
		providerLists, err := s.store.ListListsByProvider(ctx, provider.ID)
		if err != nil {
			return View{}, fmt.Errorf("list lists for provider %s: %w", provider.ID, err)
		}
		lists = append(lists, providerLists...)
		providerTasks, err := s.store.ListTasksByProvider(ctx, provider.ID)
		if err != nil {
			return View{}, fmt.Errorf("list tasks for provider %s: %w", provider.ID, err)
		}
		tasks = append(tasks, providerTasks...)
	}
	return viewFromDomain(providers, spaces, lists, tasks), nil
}

func errorText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *foundationDataStore) LoadUIState(ctx context.Context) (UIState, error) {
	if s == nil || s.store == nil {
		return UIState{}, errors.New("load UI state: store unavailable")
	}
	if ctx == nil {
		return UIState{}, errors.New("load UI state: nil context")
	}
	state, err := s.store.Load(ctx, foundationUIStateKey)
	if errors.Is(err, domain.ErrNotFound) {
		return UIState{}, nil
	}
	if err != nil {
		return UIState{}, fmt.Errorf("load application state: %w", err)
	}
	if len(state.Value) == 0 {
		return UIState{}, nil
	}
	var result UIState
	if err := json.Unmarshal(state.Value, &result); err != nil {
		return UIState{}, fmt.Errorf("decode UI state: %w", err)
	}
	return result, nil
}

func (s *foundationDataStore) SaveUIState(ctx context.Context, state UIState) error {
	if s == nil || s.store == nil {
		return errors.New("save UI state: store unavailable")
	}
	if ctx == nil {
		return errors.New("save UI state: nil context")
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode UI state: %w", err)
	}
	if err := s.store.Save(ctx, domain.AppState{
		Key:       foundationUIStateKey,
		Value:     payload,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("save application state: %w", err)
	}
	return nil
}

func (s *foundationDataStore) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Close()
}

func viewFromDomain(
	providers []domain.Provider,
	spaces []domain.Space,
	lists []domain.List,
	tasks []domain.Task,
) View {
	view := View{
		Providers:  make([]ProviderRecord, 0, len(providers)),
		Spaces:     make([]Space, 0, len(spaces)),
		Lists:      make([]List, 0, len(lists)),
		Tasks:      make([]Task, 0, len(tasks)),
		SyncErrors: make(map[ProviderID]string),
	}
	for _, provider := range providers {
		view.Providers = append(view.Providers, ProviderRecord{
			ID:            ProviderID(provider.ID),
			Type:          ProviderType(provider.Type),
			Name:          provider.Name,
			Enabled:       provider.Enabled,
			Configuration: string(provider.Configuration),
			SyncState:     SyncState(provider.SyncState),
			LastSyncAt:    cloneTime(provider.LastSyncAt),
			SyncError:     provider.SyncError,
			CreatedAt:     provider.CreatedAt,
			UpdatedAt:     provider.UpdatedAt,
		})
		if provider.SyncError != "" {
			view.SyncErrors[ProviderID(provider.ID)] = provider.SyncError
		}
	}
	for _, space := range spaces {
		view.Spaces = append(view.Spaces, Space{
			ID:              SpaceID(space.ID),
			ProviderID:      ProviderID(space.ProviderID),
			RemoteID:        cloneString(space.RemoteID),
			Name:            space.Name,
			SyncState:       SyncState(space.SyncState),
			RemoteUpdatedAt: cloneTime(space.RemoteUpdatedAt),
			CreatedAt:       space.CreatedAt,
			UpdatedAt:       space.UpdatedAt,
		})
	}
	for _, list := range lists {
		view.Lists = append(view.Lists, List{
			ID:              ListID(list.ID),
			ProviderID:      ProviderID(list.ProviderID),
			SpaceID:         SpaceID(list.SpaceID),
			RemoteID:        cloneString(list.RemoteID),
			Name:            list.Name,
			SyncState:       SyncState(list.SyncState),
			RemoteUpdatedAt: cloneTime(list.RemoteUpdatedAt),
			CreatedAt:       list.CreatedAt,
			UpdatedAt:       list.UpdatedAt,
		})
	}
	for _, task := range tasks {
		view.Tasks = append(view.Tasks, Task{
			ID:              TaskID(task.ID),
			ProviderID:      ProviderID(task.ProviderID),
			ListID:          ListID(task.ListID),
			RemoteID:        cloneString(task.RemoteID),
			ParentTaskID:    cloneTaskID(task.ParentTaskID),
			Title:           task.Title,
			Description:     task.Description,
			Status:          task.Status,
			Priority:        string(task.Priority),
			DueAt:           cloneTime(task.DueAt),
			CompletedAt:     cloneTime(task.CompletedAt),
			SyncState:       SyncState(task.SyncState),
			RemoteUpdatedAt: cloneTime(task.RemoteUpdatedAt),
			CreatedAt:       task.CreatedAt,
			UpdatedAt:       task.UpdatedAt,
		})
	}
	return view
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTaskID(value *domain.TaskID) *TaskID {
	if value == nil {
		return nil
	}
	copy := TaskID(*value)
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// cachedProvider adds the missing pull-to-storage boundary to a provider. The
// provider itself remains responsible for network I/O; sync owns when Pull is
// invoked and serializes it per provider instance.
type cachedProvider struct {
	providerpkg.Provider
	store *sqlite.Store
}

var _ providerpkg.Provider = (*cachedProvider)(nil)
var _ providerpkg.Puller = (*cachedProvider)(nil)

func newCachedProvider(value providerpkg.Provider, store *sqlite.Store) *cachedProvider {
	return &cachedProvider{Provider: value, store: store}
}

func (p *cachedProvider) Pull(ctx context.Context) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	if err := p.store.UpdateProviderSyncState(ctx, p.ID(), domain.SyncStateSyncing, nil, nil, nil); err != nil {
		return fmt.Errorf("mark provider %s syncing: %w", p.ID(), err)
	}
	if err := p.pull(ctx); err != nil {
		stateCtx, cancel := persistenceContext(ctx)
		stateErr := p.store.UpdateProviderSyncState(stateCtx, p.ID(), domain.SyncStateFailed, nil, err, nil)
		cancel()
		if stateErr != nil {
			return errors.Join(err, fmt.Errorf("record provider %s failure: %w", p.ID(), stateErr))
		}
		return err
	}
	now := time.Now().UTC()
	if err := p.store.UpdateProviderSyncState(ctx, p.ID(), domain.SyncStateSynced, nil, nil, &now); err != nil {
		return fmt.Errorf("record provider %s sync: %w", p.ID(), err)
	}
	return nil
}

func (p *cachedProvider) pull(ctx context.Context) error {
	spaces, err := p.Provider.FetchSpaces(ctx)
	if err != nil {
		return err
	}
	for _, space := range spaces {
		if space.ProviderID != p.ID() {
			return fmt.Errorf("%w: space %s belongs to %s, provider is %s", domain.ErrProviderMismatch, space.ID, space.ProviderID, p.ID())
		}
		storedSpace, err := p.upsertSpace(ctx, space)
		if err != nil {
			return fmt.Errorf("store space %s: %w", space.ID, err)
		}
		lists, err := p.Provider.FetchLists(ctx, space.ID)
		if err != nil {
			return fmt.Errorf("fetch lists for space %s: %w", space.ID, err)
		}
		for _, list := range lists {
			if list.ProviderID != p.ID() || list.SpaceID != space.ID {
				return fmt.Errorf("%w: list %s has invalid provider or parent", domain.ErrProviderMismatch, list.ID)
			}
			list.SpaceID = storedSpace.ID
			storedList, err := p.upsertList(ctx, list)
			if err != nil {
				return fmt.Errorf("store list %s: %w", list.ID, err)
			}
			tasks, err := p.Provider.FetchTasks(ctx, list.ID)
			if err != nil {
				return fmt.Errorf("fetch tasks for list %s: %w", list.ID, err)
			}
			for _, task := range tasks {
				if task.ProviderID != p.ID() || task.ListID != list.ID {
					return fmt.Errorf("%w: task %s has invalid provider or parent", domain.ErrProviderMismatch, task.ID)
				}
				task.ListID = storedList.ID
				if _, err := p.upsertTask(ctx, task); err != nil {
					return fmt.Errorf("store task %s: %w", task.ID, err)
				}
			}
		}
	}
	return nil
}

func (p *cachedProvider) CreateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := p.check(ctx); err != nil {
		return domain.Task{}, err
	}
	created, err := p.Provider.CreateTask(ctx, task)
	if err != nil {
		return domain.Task{}, err
	}
	created = pushedTask(task, created)
	if _, err := p.store.UpsertTask(ctx, created); err != nil {
		return domain.Task{}, fmt.Errorf("store created task %s: %w", task.ID, err)
	}
	return created, nil
}

func (p *cachedProvider) UpdateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := p.check(ctx); err != nil {
		return domain.Task{}, err
	}
	updated, err := p.Provider.UpdateTask(ctx, task)
	if err != nil {
		return domain.Task{}, err
	}
	updated = pushedTask(task, updated)
	if _, err := p.store.UpsertTask(ctx, updated); err != nil {
		return domain.Task{}, fmt.Errorf("store updated task %s: %w", task.ID, err)
	}
	return updated, nil
}

func (p *cachedProvider) DeleteTask(ctx context.Context, task domain.Task) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	return p.Provider.DeleteTask(ctx, task)
}

func (p *cachedProvider) check(ctx context.Context) error {
	if p == nil || p.Provider == nil || p.store == nil {
		return errors.New("cached provider: unavailable")
	}
	if ctx == nil {
		return errors.New("cached provider: nil context")
	}
	return nil
}

func (p *cachedProvider) upsertSpace(ctx context.Context, space domain.Space) (domain.Space, error) {
	if space.RemoteID != nil {
		existing, err := p.store.GetSpaceByRemoteID(ctx, p.ID(), *space.RemoteID)
		if err == nil {
			space.ID = existing.ID
			if existing.SyncState != domain.SyncStateSynced {
				return existing, nil
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Space{}, err
		}
	}
	return p.store.UpsertSpace(ctx, space)
}

func (p *cachedProvider) upsertList(ctx context.Context, list domain.List) (domain.List, error) {
	if list.RemoteID != nil {
		existing, err := p.store.GetListByRemoteID(ctx, p.ID(), *list.RemoteID)
		if err == nil {
			list.ID = existing.ID
			if existing.SyncState != domain.SyncStateSynced {
				return existing, nil
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.List{}, err
		}
	}
	return p.store.UpsertList(ctx, list)
}

func (p *cachedProvider) upsertTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if task.RemoteID != nil {
		existing, err := p.store.GetTaskByRemoteID(ctx, p.ID(), *task.RemoteID)
		if err == nil {
			task.ID = existing.ID
			if existing.SyncState != domain.SyncStateSynced {
				return existing, nil
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Task{}, err
		}
	}
	return p.store.UpsertTask(ctx, task)
}

func pushedTask(original, result domain.Task) domain.Task {
	result.ID = original.ID
	result.ProviderID = original.ProviderID
	result.ListID = original.ListID
	if result.RemoteID == nil {
		result.RemoteID = cloneString(original.RemoteID)
	}
	if result.ParentTaskID == nil {
		result.ParentTaskID = cloneDomainTaskID(original.ParentTaskID)
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = original.CreatedAt
	}
	if result.UpdatedAt.IsZero() {
		result.UpdatedAt = time.Now().UTC()
	}
	result.SyncState = domain.SyncStateSynced
	return result
}

func cloneDomainTaskID(value *domain.TaskID) *domain.TaskID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), time.Second)
}

// foundationSyncController owns the foundation engine and a tracked refresh
// loop. Refresh requests never run network I/O on the terminal goroutine.
type foundationSyncController struct {
	engine  *foundationsync.Engine
	enabled bool

	mu       sync.Mutex
	started  bool
	cancel   context.CancelFunc
	requests chan domain.ProviderID
	done     chan struct{}
}

var _ SyncController = (*foundationSyncController)(nil)

func newFoundationSyncController(engine *foundationsync.Engine, enabled bool) *foundationSyncController {
	return &foundationSyncController{
		engine:  engine,
		enabled: enabled,
	}
}

func (s *foundationSyncController) Events() <-chan foundationsync.Event {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.Events()
}

func (s *foundationSyncController) Start(ctx context.Context) error {
	if s == nil || s.engine == nil || !s.enabled {
		return nil
	}
	if ctx == nil {
		return errors.New("foundation sync controller: nil context")
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("foundation sync controller is already started")
	}
	s.mu.Unlock()
	if err := s.engine.Start(ctx); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		cancel()
		return errors.New("foundation sync controller is already started")
	}
	s.started = true
	s.cancel = cancel
	s.requests = make(chan domain.ProviderID, 16)
	s.done = make(chan struct{})
	requests := s.requests
	done := s.done
	s.mu.Unlock()
	go s.runRefreshLoop(runCtx, requests, done)
	return nil
}

func (s *foundationSyncController) Stop(ctx context.Context) error {
	if s == nil || s.engine == nil || !s.enabled {
		return nil
	}
	if ctx == nil {
		return errors.New("foundation sync controller: nil context")
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	stopErr := s.engine.Stop(ctx)
	if stopErr != nil && ctx.Err() != nil && errors.Is(stopErr, ctx.Err()) {
		return stopErr
	}
	s.mu.Lock()
	s.started = false
	s.cancel = nil
	s.done = nil
	s.requests = nil
	s.mu.Unlock()
	return stopErr
}

func (s *foundationSyncController) Trigger(providerID ProviderID) error {
	if s == nil || !s.enabled {
		return nil
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	requests := s.requests
	s.mu.Unlock()
	if providerID != "" {
		if _, ok := s.engine.Worker(domain.ProviderID(providerID)); !ok {
			return nil
		}
		select {
		case requests <- domain.ProviderID(providerID):
		default:
		}
		return nil
	}
	for _, status := range s.engine.Statuses() {
		select {
		case requests <- status.ProviderID:
		default:
			return nil
		}
	}
	return nil
}

func (s *foundationSyncController) runRefreshLoop(ctx context.Context, requests <-chan domain.ProviderID, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case providerID := <-requests:
			worker, ok := s.engine.Worker(providerID)
			if ok {
				_ = worker.SyncOnce(ctx)
			}
		}
	}
}

// foundationHandler translates the stable command package into the typed app
// command API. It is the only default path from the TUI into app.Service.
type foundationHandler struct {
	service *app.Service
	sync    *foundationSyncController

	mu        sync.RWMutex
	accepting bool
}

var _ command.Handler = (*foundationHandler)(nil)

func newFoundationHandler(service *app.Service, syncController *foundationSyncController) *foundationHandler {
	return &foundationHandler{service: service, sync: syncController, accepting: true}
}

func (h *foundationHandler) Handle(ctx context.Context, input command.Command) (command.Event, error) {
	if h == nil || h.service == nil {
		return command.Event{}, errors.New("handle command: application unavailable")
	}
	if err := input.Validate(); err != nil {
		return command.Event{}, err
	}
	h.mu.RLock()
	accepting := h.accepting
	h.mu.RUnlock()
	if !accepting && input.Kind != command.KindQuit {
		return command.Event{}, errors.New("application is shutting down")
	}
	if input.Kind == command.KindQuit {
		return command.Event{Kind: command.EventQuit}, nil
	}
	if input.Kind == command.KindRefresh {
		if h.sync != nil {
			if err := h.sync.Trigger(ProviderID(input.ProviderID)); err != nil {
				return command.Event{}, err
			}
		}
		return command.Event{Kind: command.EventRefresh}, nil
	}
	appCommand, err := foundationCommand(input)
	if err != nil {
		return command.Event{}, err
	}
	event, err := h.service.Execute(ctx, appCommand)
	if err != nil {
		return command.Event{}, err
	}
	return commandEvent(event), nil
}

func (h *foundationHandler) StopAccepting() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.accepting = false
	h.mu.Unlock()
}

// fullTaskMutationStore keeps the queued payload self-contained. The app
// service uses compact create/patch payloads, while the sync provider boundary
// needs the provider-scoped task that was optimistically persisted.
type fullTaskMutationStore struct {
	store repositorypkg.TaskMutationStore
}

var _ repositorypkg.TaskMutationStore = (*fullTaskMutationStore)(nil)

func (s *fullTaskMutationStore) ApplyTaskMutation(ctx context.Context, mutation repositorypkg.TaskMutation) (domain.Task, error) {
	if s == nil || s.store == nil {
		return domain.Task{}, errors.New("apply task mutation: store unavailable")
	}
	payload, err := json.Marshal(mutation.Task)
	if err != nil {
		return domain.Task{}, fmt.Errorf("encode queued task mutation: %w", err)
	}
	mutation.Payload = payload
	return s.store.ApplyTaskMutation(ctx, mutation)
}

func foundationCommand(input command.Command) (app.Command, error) {
	switch input.Kind {
	case command.KindCreateSpace:
		return app.CreateSpaceCommand{ProviderID: domain.ProviderID(input.ProviderID), Name: input.Title}, nil
	case command.KindCreateList:
		return app.CreateListCommand{
			ProviderID: domain.ProviderID(input.ProviderID),
			SpaceID:    domain.SpaceID(input.SpaceID),
			Name:       input.Title,
		}, nil
	case command.KindCreateTask:
		return app.CreateTaskCommand{
			ProviderID:  domain.ProviderID(input.ProviderID),
			ListID:      domain.ListID(input.ListID),
			Title:       input.Title,
			Description: input.Description,
			Status:      input.Status,
			Priority:    domain.Priority(input.Priority),
			DueAt:       cloneTime(input.DueAt),
		}, nil
	case command.KindUpdateTask:
		patch := domain.TaskPatch{}
		if input.Title != "" {
			value := input.Title
			patch.Title = &value
		}
		if input.Description != "" {
			value := input.Description
			patch.Description = &value
		}
		if input.Status != "" {
			value := input.Status
			patch.Status = &value
		}
		if input.Priority != "" {
			value := domain.Priority(input.Priority)
			patch.Priority = &value
		}
		if input.DueAt != nil {
			patch.DueAt = cloneTime(input.DueAt)
		}
		return app.PatchTaskCommand{TaskID: domain.TaskID(input.TaskID), Patch: patch}, nil
	case command.KindCompleteTask:
		if input.Completed != nil && !*input.Completed {
			status := "todo"
			return app.PatchTaskCommand{
				TaskID: domain.TaskID(input.TaskID),
				Patch: domain.TaskPatch{
					Status:           &status,
					ClearCompletedAt: true,
				},
			}, nil
		}
		return app.CompleteTaskCommand{TaskID: domain.TaskID(input.TaskID)}, nil
	case command.KindDeleteTask:
		return app.DeleteTaskCommand{TaskID: domain.TaskID(input.TaskID)}, nil
	case command.KindMoveTask:
		return app.MoveTaskCommand{
			TaskID:            domain.TaskID(input.TaskID),
			DestinationListID: domain.ListID(input.DestinationListID),
		}, nil
	case command.KindTransferTask:
		return app.TransferTaskCommand{
			SourceTaskID:      domain.TaskID(input.TaskID),
			DestinationListID: domain.ListID(input.DestinationListID),
		}, nil
	case command.KindSearch:
		return app.SearchTasksCommand{Query: input.Query}, nil
	default:
		return nil, fmt.Errorf("translate command %q: unsupported command", input.Kind)
	}
}

func commandEvent(event app.Event) command.Event {
	switch value := event.(type) {
	case app.TasksSearched:
		return command.Event{Kind: command.EventSearchResults, Query: value.Query}
	case app.TasksFiltered:
		return command.Event{Kind: command.EventSearchResults}
	default:
		return command.Event{Kind: command.EventChanged}
	}
}

// foundationUIController adapts the framework-neutral presentation model to
// the bootstrap terminal lifecycle. It evaluates emitted commands only after
// the model has accepted the input, so local navigation never performs I/O.
type foundationUIController struct {
	terminal Terminal
	handler  command.Handler
	load     ViewLoader
	events   <-chan foundationsync.Event

	mu          sync.RWMutex
	model       foundationtui.Model
	initialized bool
	accepting   bool
	eventCancel context.CancelFunc
	eventDone   chan struct{}

	program *tea.Program
	charm   *foundationtui.CharmModel
	running bool

	headlessRendered bool
}

var _ UIController = (*foundationUIController)(nil)

func newFoundationUIController(terminal Terminal, handler command.Handler, load ViewLoader, events <-chan foundationsync.Event) (*foundationUIController, error) {
	if terminal == nil {
		return nil, errors.New("new foundation UI: nil terminal")
	}
	if handler == nil {
		return nil, errors.New("new foundation UI: nil command handler")
	}
	return &foundationUIController{
		terminal:  terminal,
		handler:   handler,
		load:      load,
		events:    events,
		model:     foundationtui.New(foundationtui.Snapshot{}),
		accepting: true,
	}, nil
}

func (u *foundationUIController) Initialize(ctx context.Context) error {
	if u == nil || u.terminal == nil {
		return errors.New("initialize foundation UI: unavailable")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	// Bubble Tea owns raw-mode input, the alternate screen, and terminal
	// restoration when the concrete terminal exposes its streams. Legacy
	// terminal fakes still use the lifecycle methods directly.
	if _, ok := u.terminal.(TerminalStreams); !ok {
		if err := u.terminal.Initialize(ctx); err != nil {
			return err
		}
	}
	u.mu.Lock()
	u.initialized = true
	u.mu.Unlock()
	return nil
}

func (u *foundationUIController) SetState(state UIState) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.model.UI.Focus = foundationPanel(state.Panel)
	if u.model.UI.Focus == "" {
		u.model.UI.Focus = foundationtui.PanelHierarchy
	}
	u.model.UI.TreeCursor = 0
	u.model.UI.TaskCursor = 0
	if u.model.UI.Focus == foundationtui.PanelTasks {
		u.model.UI.TaskCursor = max(0, state.Cursor)
	} else {
		u.model.UI.TreeCursor = max(0, state.Cursor)
	}
	u.model.UI.SelectedNode = foundationNodeRef(state)
	u.model.UI.Filter = foundationtui.Filter{}
	u.model.UI.FilterActive = false
	if strings.TrimSpace(state.Filter) != "" {
		filter, err := foundationtui.ParseFilter(state.Filter)
		if err == nil {
			u.model.UI.Filter = filter
			u.model.UI.FilterActive = true
		}
	}
}

func (u *foundationUIController) Render(ctx context.Context, view View) error {
	if u == nil || u.terminal == nil {
		return errors.New("render foundation UI: unavailable")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	snapshot := foundationSnapshot(view)
	u.mu.Lock()
	program := u.program
	running := u.running
	var headlessFrame string
	var writeHeadless bool
	if !running {
		model, _ := u.model.Update(foundationtui.NewSnapshotMsg(snapshot))
		u.model = model
		if u.terminal.Headless() {
			headlessFrame = foundationtui.NewCharmModel(model, foundationtui.CharmOptions{}).View()
			writeHeadless = true
		}
	}
	u.mu.Unlock()
	if running && program != nil {
		program.Send(foundationtui.NewSnapshotMsg(snapshot))
	}
	if writeHeadless {
		if err := u.writeHeadlessFrame(ctx, headlessFrame); err != nil {
			return err
		}
		u.mu.Lock()
		u.headlessRendered = true
		u.mu.Unlock()
	}
	return nil
}

func (u *foundationUIController) Run(ctx context.Context) error {
	if u == nil || u.terminal == nil {
		return errors.New("run foundation UI: unavailable")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	u.mu.RLock()
	initialized := u.initialized
	u.mu.RUnlock()
	if !initialized {
		return errors.New("run foundation UI: not initialized")
	}
	u.mu.RLock()
	model := u.model
	u.mu.RUnlock()
	charm := foundationtui.NewCharmModel(model, foundationtui.CharmOptions{
		OnCommand: func(input foundationtui.AppCommand) tea.Cmd {
			return u.teaCommand(ctx, input)
		},
	})
	if u.terminal.Headless() {
		u.mu.Lock()
		alreadyRendered := u.headlessRendered
		u.headlessRendered = false
		u.mu.Unlock()
		if alreadyRendered {
			return nil
		}
		return u.writeHeadlessFrame(ctx, charm.View())
	}

	input, output, cleanup, err := u.programStreams(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	program := tea.NewProgram(
		charm,
		tea.WithContext(ctx),
		tea.WithInput(input),
		tea.WithOutput(output),
		tea.WithAltScreen(),
		tea.WithoutSignalHandler(),
	)
	u.mu.Lock()
	u.program = program
	u.charm = charm
	u.running = true
	u.mu.Unlock()

	eventCancel := u.startSyncEvents(ctx)
	finalModel, runErr := program.Run()
	if eventCancel != nil {
		eventCancel()
	}
	u.mu.RLock()
	eventDone := u.eventDone
	u.mu.RUnlock()
	if eventDone != nil {
		<-eventDone
	}
	u.mu.Lock()
	if finalCharm, ok := finalModel.(*foundationtui.CharmModel); ok {
		u.model = finalCharm.CoreModel()
	}
	u.running = false
	u.program = nil
	u.charm = nil
	u.mu.Unlock()
	if errors.Is(runErr, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return runErr
}

func (u *foundationUIController) State() UIState {
	if u == nil {
		return UIState{}
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	model := u.model
	state := UIState{
		Panel:  string(model.UI.Focus),
		Cursor: model.UI.TaskCursor,
	}
	if model.UI.Focus != foundationtui.PanelTasks {
		state.Cursor = model.UI.TreeCursor
	}
	ref := model.UI.SelectedNode
	state.ProviderID = string(ref.ProviderID)
	state.SpaceID = string(ref.SpaceID)
	state.ListID = string(ref.ListID)
	if model.UI.FilterActive {
		state.Filter = model.UI.Filter.String()
	}
	return state
}

func (u *foundationUIController) StopAccepting() {
	if u == nil {
		return
	}
	u.mu.Lock()
	u.accepting = false
	u.model.UI.Quitting = true
	eventCancel := u.eventCancel
	eventDone := u.eventDone
	program := u.program
	u.eventCancel = nil
	u.eventDone = nil
	u.mu.Unlock()
	if program != nil {
		program.Quit()
	}
	if eventCancel != nil {
		eventCancel()
	}
	if eventDone != nil {
		<-eventDone
	}
}

func (u *foundationUIController) Restore(ctx context.Context) error {
	if u == nil || u.terminal == nil {
		return nil
	}
	if _, ok := u.terminal.(TerminalStreams); ok {
		// Bubble Tea restores stream-backed terminals when Program.Run returns.
		return nil
	}
	return u.terminal.Restore(ctx)
}

func (u *foundationUIController) reload(ctx context.Context) error {
	if u.load == nil {
		return nil
	}
	view, err := u.load(ctx)
	if err != nil {
		return err
	}
	snapshot := foundationSnapshot(view)
	u.mu.Lock()
	program := u.program
	running := u.running
	if !running {
		model, _ := u.model.Update(foundationtui.NewSnapshotMsg(snapshot))
		u.model = model
	}
	u.mu.Unlock()
	if running && program != nil {
		program.Send(foundationtui.NewSnapshotMsg(snapshot))
	}
	return nil
}

func (u *foundationUIController) setError(err error) {
	u.mu.Lock()
	program := u.program
	if !u.running {
		model, _ := u.model.Update(foundationtui.ErrorMsg{Err: err, Text: SafeErrorText(err)})
		u.model = model
	}
	u.mu.Unlock()
	if program != nil {
		program.Send(foundationtui.ErrorMsg{Err: err, Text: SafeErrorText(err)})
	}
}

func (u *foundationUIController) renderModel(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	// The Bubble Tea renderer redraws after every state message. This method is
	// retained for lifecycle callers that request a frame before Run starts.
	u.mu.RLock()
	running := u.running
	model := u.model
	u.mu.RUnlock()
	if running {
		return nil
	}
	return u.writeHeadlessFrame(ctx, foundationtui.NewCharmModel(model, foundationtui.CharmOptions{}).View())
}

func (u *foundationUIController) startSyncEvents(ctx context.Context) context.CancelFunc {
	if u == nil || u.events == nil {
		return nil
	}
	eventCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	u.mu.Lock()
	u.eventCancel = cancel
	u.eventDone = done
	u.mu.Unlock()
	go func() {
		defer close(done)
		u.runSyncEvents(eventCtx)
	}()
	return cancel
}

func (u *foundationUIController) runSyncEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-u.events:
			if !ok {
				return
			}
			message, refresh, ok := foundationSyncMessage(event)
			if !ok {
				continue
			}
			if refresh {
				if err := u.reload(ctx); err != nil {
					u.setError(err)
				}
			}
			u.mu.RLock()
			program := u.program
			u.mu.RUnlock()
			if program != nil {
				program.Send(message)
			}
		}
	}
}

func (u *foundationUIController) teaCommand(ctx context.Context, input foundationtui.AppCommand) tea.Cmd {
	return func() tea.Msg {
		if input.Kind == foundationtui.CommandLoadCached {
			if u.load == nil {
				return nil
			}
			view, err := u.load(ctx)
			if err != nil {
				return foundationtui.ErrorMsg{Err: err, Text: SafeErrorText(err)}
			}
			return foundationtui.NewSnapshotMsg(foundationSnapshot(view))
		}
		appCommand, dispatch, err := foundationUICommand(input)
		if err != nil {
			return foundationtui.ErrorMsg{Err: err, Text: SafeErrorText(err)}
		}
		if !dispatch {
			return nil
		}
		event, err := u.handler.Handle(ctx, appCommand)
		if err != nil {
			return foundationtui.ErrorMsg{Err: err, Text: SafeErrorText(err)}
		}
		if event.Kind == command.EventChanged && u.load != nil {
			view, err := u.load(ctx)
			if err != nil {
				return foundationtui.ErrorMsg{Err: err, Text: SafeErrorText(err)}
			}
			return foundationtui.NewSnapshotMsg(foundationSnapshot(view))
		}
		return foundationtui.CommandResultMsg{Command: input}
	}
}

func (u *foundationUIController) writeHeadlessFrame(ctx context.Context, frame string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	data := []byte(frame)
	if streams, ok := u.terminal.(TerminalStreams); ok && streams.Output() != nil {
		output := streams.Output()
		written, err := output.Write(data)
		if err != nil {
			return err
		}
		if written != len(data) {
			return io.ErrShortWrite
		}
		return nil
	}
	return u.terminal.Write(ctx, data)
}

func (u *foundationUIController) programStreams(ctx context.Context) (io.Reader, io.Writer, func(), error) {
	var input io.Reader
	var output io.Writer
	if streams, ok := u.terminal.(TerminalStreams); ok {
		input = streams.Input()
		output = streams.Output()
	}
	if output == nil {
		output = terminalOutputWriter{terminal: u.terminal, ctx: ctx}
	}
	if input != nil {
		return input, output, func() {}, nil
	}

	readCtx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		for {
			key, err := u.terminal.ReadKey(readCtx)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
					return
				}
				_ = writer.CloseWithError(err)
				return
			}
			encoded := encodeTerminalKey(key)
			if encoded == "" {
				continue
			}
			if _, err := io.WriteString(writer, encoded); err != nil {
				return
			}
		}
	}()
	cleanup := func() {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		<-done
	}
	return reader, output, cleanup, nil
}

type terminalOutputWriter struct {
	terminal Terminal
	ctx      context.Context
}

func (w terminalOutputWriter) Write(data []byte) (int, error) {
	if err := w.terminal.Write(w.ctx, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func encodeTerminalKey(key Key) string {
	switch key.Name {
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "right":
		return "\x1b[C"
	case "left":
		return "\x1b[D"
	case "enter":
		return "\r"
	case "escape", "esc":
		return "\x1b"
	case "backspace":
		return "\x7f"
	case "quit", "ctrl+c":
		return "\x03"
	default:
		if key.Name != "" {
			return key.Name
		}
		if key.Rune != 0 {
			return string(key.Rune)
		}
		return ""
	}
}

func foundationSyncMessage(event foundationsync.Event) (foundationtui.SyncStateMsg, bool, bool) {
	message := foundationtui.SyncStateMsg{
		ProviderID: foundationtui.ProviderID(event.ProviderID),
		EntityType: foundationtui.EntityProvider,
	}
	if event.ProviderID == "" {
		return foundationtui.SyncStateMsg{}, false, false
	}
	switch event.Kind {
	case foundationsync.EventWorkerStarted, foundationsync.EventSyncStarted,
		foundationsync.EventOperationClaimed, foundationsync.EventOperationAttempted:
		message.State = foundationtui.SyncStateSyncing
	case foundationsync.EventOperationCompleted, foundationsync.EventPullCompleted:
		message.State = foundationtui.SyncStateSyncing
	case foundationsync.EventRetryScheduled, foundationsync.EventOperationFailed:
		message.State = foundationtui.SyncStateFailed
		message.Error = SafeErrorText(event.Err)
		return message, false, true
	case foundationsync.EventSyncFailed:
		message.State = foundationtui.SyncStateFailed
		message.Error = SafeErrorText(event.Err)
		return message, true, true
	case foundationsync.EventSyncCompleted:
		message.State = foundationtui.SyncStateSynced
		at := event.At
		message.LastSyncAt = &at
		return message, true, true
	default:
		return foundationtui.SyncStateMsg{}, false, false
	}
	return message, true, false
}

func foundationPanel(value string) foundationtui.Panel {
	switch value {
	case string(foundationtui.PanelTasks):
		return foundationtui.PanelTasks
	default:
		return foundationtui.PanelHierarchy
	}
}

func foundationNodeRef(state UIState) foundationtui.TreeNodeRef {
	ref := foundationtui.TreeNodeRef{ProviderID: foundationtui.ProviderID(state.ProviderID)}
	switch {
	case state.ListID != "":
		ref.Kind = foundationtui.TreeNodeList
		ref.SpaceID = foundationtui.SpaceID(state.SpaceID)
		ref.ListID = foundationtui.ListID(state.ListID)
	case state.SpaceID != "":
		ref.Kind = foundationtui.TreeNodeSpace
		ref.SpaceID = foundationtui.SpaceID(state.SpaceID)
	case state.ProviderID != "":
		ref.Kind = foundationtui.TreeNodeProvider
	}
	return ref
}

func foundationUICommand(input foundationtui.AppCommand) (command.Command, bool, error) {
	switch input.Kind {
	case foundationtui.CommandCreateTask:
		return command.Command{
			Kind:        command.KindCreateTask,
			ProviderID:  string(input.ProviderID),
			SpaceID:     string(input.SpaceID),
			ListID:      string(input.ListID),
			Title:       input.Title,
			Description: input.Description,
			Status:      input.Status,
			Priority:    string(input.Priority),
			DueAt:       cloneTime(input.DueAt),
		}, true, nil
	case foundationtui.CommandUpdateTask:
		return command.Command{
			Kind:        command.KindUpdateTask,
			ProviderID:  string(input.ProviderID),
			ListID:      string(input.ListID),
			TaskID:      string(input.TaskID),
			Title:       input.Title,
			Description: input.Description,
			Status:      input.Status,
			Priority:    string(input.Priority),
			DueAt:       cloneTime(input.DueAt),
		}, true, nil
	case foundationtui.CommandCompleteTask:
		completed := input.Completed
		return command.Command{
			Kind:       command.KindCompleteTask,
			ProviderID: string(input.ProviderID),
			TaskID:     string(input.TaskID),
			Status:     input.Status,
			Completed:  &completed,
		}, true, nil
	case foundationtui.CommandDeleteTask:
		return command.Command{
			Kind:       command.KindDeleteTask,
			ProviderID: string(input.ProviderID),
			TaskID:     string(input.TaskID),
		}, true, nil
	case foundationtui.CommandSearch:
		return command.Command{Kind: command.KindSearch, Query: input.Query}, true, nil
	case foundationtui.CommandRefresh:
		return command.Command{Kind: command.KindRefresh, ProviderID: string(input.ProviderID)}, true, nil
	case foundationtui.CommandQuit:
		return command.Command{Kind: command.KindQuit}, true, nil
	case foundationtui.CommandFilter, foundationtui.CommandHelp:
		// Filtering and help are presentation-local operations.
		return command.Command{}, false, nil
	default:
		return command.Command{}, false, fmt.Errorf("translate UI command %q: unsupported command", input.Kind)
	}
}

func foundationSnapshot(view View) foundationtui.Snapshot {
	providers := make([]domain.Provider, 0, len(view.Providers))
	for _, value := range view.Providers {
		state := domain.SyncState(value.SyncState)
		if state == "" {
			state = domain.SyncStatePending
			switch {
			case value.Type == ProviderTypeLocal:
				state = domain.SyncStateLocal
			case value.SyncError != "":
				state = domain.SyncStateFailed
			case value.LastSyncAt != nil:
				state = domain.SyncStateSynced
			}
		}
		providers = append(providers, domain.Provider{
			ID:            domain.ProviderID(value.ID),
			Type:          domain.ProviderType(value.Type),
			Name:          value.Name,
			Enabled:       value.Enabled,
			Configuration: json.RawMessage(value.Configuration),
			SyncState:     state,
			SyncError:     value.SyncError,
			LastSyncAt:    cloneTime(value.LastSyncAt),
			CreatedAt:     value.CreatedAt,
			UpdatedAt:     value.UpdatedAt,
		})
	}
	spaces := make([]domain.Space, 0, len(view.Spaces))
	for _, value := range view.Spaces {
		spaces = append(spaces, domain.Space{
			ID:              domain.SpaceID(value.ID),
			ProviderID:      domain.ProviderID(value.ProviderID),
			RemoteID:        cloneString(value.RemoteID),
			Name:            value.Name,
			SyncState:       domain.SyncState(value.SyncState),
			RemoteUpdatedAt: cloneTime(value.RemoteUpdatedAt),
			CreatedAt:       value.CreatedAt,
			UpdatedAt:       value.UpdatedAt,
		})
	}
	lists := make([]domain.List, 0, len(view.Lists))
	for _, value := range view.Lists {
		lists = append(lists, domain.List{
			ID:              domain.ListID(value.ID),
			ProviderID:      domain.ProviderID(value.ProviderID),
			SpaceID:         domain.SpaceID(value.SpaceID),
			RemoteID:        cloneString(value.RemoteID),
			Name:            value.Name,
			SyncState:       domain.SyncState(value.SyncState),
			RemoteUpdatedAt: cloneTime(value.RemoteUpdatedAt),
			CreatedAt:       value.CreatedAt,
			UpdatedAt:       value.UpdatedAt,
		})
	}
	tasks := make([]domain.Task, 0, len(view.Tasks))
	for _, value := range view.Tasks {
		var parent *domain.TaskID
		if value.ParentTaskID != nil {
			parentID := domain.TaskID(*value.ParentTaskID)
			parent = &parentID
		}
		tasks = append(tasks, domain.Task{
			ID:              domain.TaskID(value.ID),
			ProviderID:      domain.ProviderID(value.ProviderID),
			ListID:          domain.ListID(value.ListID),
			RemoteID:        cloneString(value.RemoteID),
			ParentTaskID:    parent,
			Title:           value.Title,
			Description:     value.Description,
			Status:          value.Status,
			Priority:        domain.Priority(value.Priority),
			DueAt:           cloneTime(value.DueAt),
			CompletedAt:     cloneTime(value.CompletedAt),
			SyncState:       domain.SyncState(value.SyncState),
			RemoteUpdatedAt: cloneTime(value.RemoteUpdatedAt),
			CreatedAt:       value.CreatedAt,
			UpdatedAt:       value.UpdatedAt,
		})
	}
	return foundationtui.SnapshotFromDomain(foundationtui.DomainSnapshot{
		Providers: providers,
		Spaces:    spaces,
		Lists:     lists,
		Tasks:     tasks,
	})
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

type foundationGraph struct {
	store   DataStore
	handler command.Handler
	sync    SyncController
	ui      UIController
}

func buildFoundationGraph(ctx context.Context, cfg Config, terminal Terminal) (*foundationGraph, error) {
	store, err := openFoundationStore(cfg.Database)
	if err != nil {
		return nil, err
	}
	closeStore := func() {
		_ = store.Close()
	}

	spaceRepository := sqlite.NewSpaceRepository(store)
	listRepository := sqlite.NewListRepository(store)
	taskRepository := sqlite.NewTaskRepository(store)
	localProvider := localpkg.NewWithStores(spaceRepository, listRepository, taskRepository)
	providers := make([]providerpkg.Provider, 0, 2)
	providers = append(providers, localProvider)
	if err := upsertFoundationProvider(ctx, store, domain.Provider{
		ID:        localProvider.ID(),
		Type:      localProvider.Type(),
		Name:      "Local",
		Enabled:   true,
		SyncState: domain.SyncStateLocal,
	}); err != nil {
		closeStore()
		return nil, fmt.Errorf("register local provider: %w", err)
	}

	if cfg.ClickUp.Enabled {
		clickupProviderID := domain.ProviderID(cfg.ClickUp.ID)
		clickupProvider := clickuppkg.NewWithConfig(clickuppkg.ProviderConfig{
			ProviderID: clickupProviderID,
			Client: clickuppkg.NewClient(clickuppkg.ClientConfig{
				BaseURL:     cfg.ClickUp.BaseURL,
				TeamID:      cfg.ClickUp.WorkspaceID,
				TokenSource: foundationTokenSource(cfg.ClickUp.TokenEnv),
			}),
			ParentResolver:     foundationParentResolver(store),
			RemoteListResolver: foundationRemoteListResolver(store),
			RemoteTaskResolver: foundationRemoteTaskResolver(store),
		})
		cached := newCachedProvider(clickupProvider, store)
		providers = append(providers, cached)
		configuration, marshalErr := json.Marshal(struct {
			BaseURL     string `json:"base_url"`
			WorkspaceID string `json:"workspace_id,omitempty"`
		}{BaseURL: cfg.ClickUp.BaseURL, WorkspaceID: cfg.ClickUp.WorkspaceID})
		if marshalErr != nil {
			closeStore()
			return nil, fmt.Errorf("encode ClickUp provider configuration: %w", marshalErr)
		}
		if err := upsertFoundationProvider(ctx, store, domain.Provider{
			ID:            clickupProvider.ID(),
			Type:          clickupProvider.Type(),
			Name:          cfg.ClickUp.Name,
			Enabled:       true,
			SyncState:     domain.SyncStatePending,
			Configuration: configuration,
		}); err != nil {
			closeStore()
			return nil, fmt.Errorf("register ClickUp provider: %w", err)
		}
	}

	providerRegistry := app.NewProviderRegistry(providers...)
	if _, ok := providerRegistry.Get(app.ProviderID(cfg.App.DefaultProvider)); !ok {
		closeStore()
		return nil, fmt.Errorf("default provider %s is not registered", cfg.App.DefaultProvider)
	}
	mutationStore := &fullTaskMutationStore{store: store}
	mutationRepository := app.NewFoundationMutationAdapter(
		spaceRepository,
		listRepository,
		taskRepository,
		mutationStore,
		nil,
		nil,
	)
	service := app.NewService(mutationRepository, providerRegistry)
	workerOptions := foundationsync.DefaultWorkerOptions()
	workerOptions.Interval = cfg.Sync.Interval
	if !cfg.Sync.RetryFailed {
		workerOptions.RetryPolicy.MaxAttempts = 1
	}
	engine, err := foundationsync.NewEngine(store, providers, foundationsync.EngineOptions{Worker: workerOptions})
	if err != nil {
		closeStore()
		return nil, fmt.Errorf("create foundation sync engine: %w", err)
	}
	syncController := newFoundationSyncController(engine, cfg.Sync.Enabled)
	handler := newFoundationHandler(service, syncController)
	ui, err := newFoundationUIController(terminal, handler, func(ctx context.Context) (View, error) {
		return newFoundationDataStore(store).Snapshot(ctx)
	}, syncController.Events())
	if err != nil {
		closeStore()
		return nil, err
	}
	return &foundationGraph{
		store:   newFoundationDataStore(store),
		handler: handler,
		sync:    syncController,
		ui:      ui,
	}, nil
}

func foundationParentResolver(store *sqlite.Store) clickuppkg.ContextParentResolverFunc {
	return func(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.TaskID, bool) {
		if store == nil || ctx == nil {
			return "", false
		}
		task, err := store.GetTaskByRemoteID(ctx, providerID, remoteID)
		if err != nil {
			return "", false
		}
		return task.ID, true
	}
}

func foundationRemoteListResolver(store *sqlite.Store) func(context.Context, domain.ProviderID, domain.ListID) (string, error) {
	return func(ctx context.Context, providerID domain.ProviderID, listID domain.ListID) (string, error) {
		if store == nil {
			return "", errors.New("resolve remote list ID: store unavailable")
		}
		if ctx == nil {
			return "", errors.New("resolve remote list ID: nil context")
		}
		list, err := store.GetList(ctx, listID)
		if err != nil {
			return "", err
		}
		if list.ProviderID != providerID {
			return "", fmt.Errorf("%w: list %s belongs to %s, provider is %s", domain.ErrProviderMismatch, listID, list.ProviderID, providerID)
		}
		if list.RemoteID == nil || strings.TrimSpace(*list.RemoteID) == "" {
			return "", clickuppkg.ErrRemoteIDMissing
		}
		return strings.TrimSpace(*list.RemoteID), nil
	}
}

func foundationRemoteTaskResolver(store *sqlite.Store) func(context.Context, domain.ProviderID, domain.TaskID) (string, error) {
	return func(ctx context.Context, providerID domain.ProviderID, taskID domain.TaskID) (string, error) {
		if store == nil {
			return "", errors.New("resolve remote task ID: store unavailable")
		}
		if ctx == nil {
			return "", errors.New("resolve remote task ID: nil context")
		}
		task, err := store.GetTask(ctx, taskID)
		if err != nil {
			return "", err
		}
		if task.ProviderID != providerID {
			return "", fmt.Errorf("%w: task %s belongs to %s, provider is %s", domain.ErrProviderMismatch, taskID, task.ProviderID, providerID)
		}
		if task.RemoteID == nil || strings.TrimSpace(*task.RemoteID) == "" {
			return "", clickuppkg.ErrRemoteIDMissing
		}
		return strings.TrimSpace(*task.RemoteID), nil
	}
}

func upsertFoundationProvider(ctx context.Context, store *sqlite.Store, provider domain.Provider) error {
	if store == nil {
		return errors.New("provider store is unavailable")
	}
	if _, err := store.UpsertProvider(ctx, provider); err != nil {
		return err
	}
	if provider.SyncState != "" {
		if err := store.UpdateProviderSyncState(ctx, provider.ID, provider.SyncState, nil, nil, provider.LastSyncAt); err != nil {
			return err
		}
	}
	return nil
}

func foundationTokenSource(name string) clickuppkg.TokenSourceFunc {
	return func(ctx context.Context) (string, error) {
		if ctx == nil {
			return "", errors.New("nil context")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		value, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(value) == "" {
			return "", clickuppkg.ErrMissingTokenSource
		}
		return value, nil
	}
}

func openFoundationStore(cfg DatabaseConfig) (*sqlite.Store, error) {
	path := strings.TrimSpace(cfg.Path)
	if err := prepareDatabasePath(path); err != nil {
		return nil, err
	}
	store, err := sqlite.Open(path)
	if err != nil {
		return nil, err
	}
	if cfg.BusyTimeout > 0 {
		if _, err := store.SQLDB().Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.BusyTimeout.Milliseconds())); err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
		}
	}
	return store, nil
}

func prepareDatabasePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file::memory:") || strings.HasPrefix(path, "file:") {
		return nil
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set database permissions: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close database file: %w", err)
	}
	return nil
}
