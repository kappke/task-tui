package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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
	"github.com/kappke/task-tui/internal/storage/sqlite"
	foundationsync "github.com/kappke/task-tui/internal/sync"
	foundationtui "github.com/kappke/task-tui/internal/tui"
)

const foundationUIStateKey = "main"
const foundationTaskPageSize = 200

// foundationDataStore adapts the authoritative SQLite store to the small
// lifecycle persistence contract. All snapshots are assembled from local rows.
type foundationDataStore struct {
	store *sqlite.Store
}

type foundationTaskScope struct {
	providerID ProviderID
	spaceID    SpaceID
	listID     ListID
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
	uiState, err := s.LoadUIState(ctx)
	if err != nil {
		return View{}, err
	}
	return s.snapshotWithTaskScope(ctx, providers, uiState.ProviderID, uiState.ListID)
}

func (s *foundationDataStore) snapshotWithTaskScope(ctx context.Context, providers []domain.Provider, providerID, listID string) (View, error) {
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
		var providerTasks []domain.Task
		if listID != "" && providerID == provider.ID.String() {
			providerTasks, err = s.store.ListTasksByListPage(ctx, provider.ID, domain.ListID(listID), 0, 0)
		} else if listID == "" {
			providerTasks, err = s.store.ListTasksByProviderPage(ctx, provider.ID, foundationTaskPageSize, 0)
		}
		if err != nil {
			return View{}, fmt.Errorf("list task page for provider %s: %w", provider.ID, err)
		}
		tasks = append(tasks, providerTasks...)
	}
	view := viewFromDomain(providers, spaces, lists, tasks)
	options, err := s.editorOptions(ctx, spaces, lists)
	if err != nil {
		return View{}, err
	}
	view.EditorOptions = options
	return view, nil
}

func (s *foundationDataStore) editorOptions(ctx context.Context, spaces []domain.Space, lists []domain.List) ([]TaskEditorOptions, error) {
	spaceStatuses := make(map[domain.SpaceID][]string, len(spaces))
	for _, space := range spaces {
		metadata, err := s.store.ListMetadata(ctx, space.ProviderID, domain.EntityTypeSpace, string(space.ID))
		if err != nil {
			return nil, fmt.Errorf("list editor metadata for space %s: %w", space.ID, err)
		}
		spaceStatuses[space.ID], _ = statusNames(metadata)
	}
	options := make([]TaskEditorOptions, 0, len(lists))
	for _, list := range lists {
		metadata, err := s.store.ListMetadata(ctx, list.ProviderID, domain.EntityTypeList, string(list.ID))
		if err != nil {
			return nil, fmt.Errorf("list editor metadata for list %s: %w", list.ID, err)
		}
		statuses, hasListMetadata := statusNames(metadata)
		if !hasListMetadata {
			statuses = append([]string(nil), spaceStatuses[list.SpaceID]...)
		}
		options = append(options, TaskEditorOptions{
			ProviderID: ProviderID(list.ProviderID), SpaceID: SpaceID(list.SpaceID), ListID: ListID(list.ID), Statuses: statuses,
		})
	}
	return options, nil
}

func statusNames(metadata []domain.ProviderMetadata) ([]string, bool) {
	for _, item := range metadata {
		if item.Key != "clickup.statuses" {
			continue
		}
		var value struct {
			Statuses []struct {
				Name string `json:"name"`
			} `json:"statuses"`
		}
		if err := json.Unmarshal([]byte(item.Value), &value); err != nil {
			return nil, false
		}
		result := make([]string, 0, len(value.Statuses))
		for _, status := range value.Statuses {
			if strings.TrimSpace(status.Name) != "" {
				result = append(result, status.Name)
			}
		}
		return result, true
	}
	return nil, false
}

func (s *foundationDataStore) SnapshotForList(ctx context.Context, providerID ProviderID, listID ListID) (View, error) {
	if s == nil || s.store == nil {
		return View{}, errors.New("snapshot list data: store unavailable")
	}
	if ctx == nil {
		return View{}, errors.New("snapshot list data: nil context")
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
	return s.snapshotWithTaskScope(ctx, providers, string(providerID), string(listID))
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
	return s.normalizeUIState(ctx, result)
}

func (s *foundationDataStore) normalizeUIState(ctx context.Context, state UIState) (UIState, error) {
	original := state
	persist := func() error {
		if original.ProviderID == state.ProviderID && original.SpaceID == state.SpaceID && original.ListID == state.ListID {
			return nil
		}
		return s.SaveUIState(ctx, state)
	}
	if strings.TrimSpace(state.ProviderID) == "" {
		return state, nil
	}
	if _, err := s.store.GetProvider(ctx, domain.ProviderID(state.ProviderID)); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			state.ProviderID, state.SpaceID, state.ListID = "", "", ""
			return state, persist()
		}
		return UIState{}, fmt.Errorf("resolve UI provider %s: %w", state.ProviderID, err)
	}
	if strings.TrimSpace(state.ListID) == "" {
		if strings.TrimSpace(state.SpaceID) != "" {
			space, spaceErr := s.store.GetSpace(ctx, domain.SpaceID(state.SpaceID))
			if spaceErr != nil || space.ProviderID != domain.ProviderID(state.ProviderID) {
				space, spaceErr = s.store.GetSpaceByRemoteID(ctx, domain.ProviderID(state.ProviderID), state.SpaceID)
			}
			if spaceErr != nil {
				if !errors.Is(spaceErr, domain.ErrNotFound) {
					return UIState{}, fmt.Errorf("resolve UI space %s: %w", state.SpaceID, spaceErr)
				}
				state.SpaceID = ""
				return state, persist()
			}
			state.SpaceID = space.ID.String()
		}
		return state, persist()
	}
	list, err := s.store.GetList(ctx, domain.ListID(state.ListID))
	if err != nil || list.ProviderID != domain.ProviderID(state.ProviderID) {
		list, err = s.store.GetListByRemoteID(ctx, domain.ProviderID(state.ProviderID), state.ListID)
	}
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			state.SpaceID = ""
			state.ListID = ""
			return state, persist()
		}
		return UIState{}, fmt.Errorf("resolve UI list %s: %w", state.ListID, err)
	}
	if list.ProviderID != domain.ProviderID(state.ProviderID) {
		state.SpaceID = ""
		state.ListID = ""
		return state, persist()
	}
	state.ListID = list.ID.String()
	state.SpaceID = list.SpaceID.String()
	return state, persist()
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
			Assignee:        task.Assignee,
			Title:           task.Title,
			Description:     task.Description,
			Status:          task.Status,
			Priority:        string(task.Priority),
			TimeEstimate:    cloneDuration(task.TimeEstimate),
			TimeTracked:     cloneDuration(task.TimeTracked),
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

func cloneDuration(value *time.Duration) *time.Duration {
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
	store      *sqlite.Store
	logger     *slog.Logger
	pullMu     sync.Mutex
	activeMu   sync.RWMutex
	activeList domain.ListID
	scopeSet   bool
	progressMu sync.RWMutex
	progress   func(string)
}

var _ providerpkg.Provider = (*cachedProvider)(nil)
var _ providerpkg.Puller = (*cachedProvider)(nil)

func newCachedProvider(value providerpkg.Provider, store *sqlite.Store, loggers ...*slog.Logger) *cachedProvider {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &cachedProvider{Provider: value, store: store, logger: logger}
}

func (p *cachedProvider) SetActiveTaskList(listID domain.ListID) {
	requestedID := strings.TrimSpace(string(listID))
	resolvedID := listID
	if requestedID != "" {
		ctx := context.Background()
		if list, err := p.store.GetList(ctx, listID); err == nil && list.ProviderID == p.ID() {
			resolvedID = list.ID
		} else if list, remoteErr := p.store.GetListByRemoteID(ctx, p.ID(), requestedID); remoteErr == nil {
			resolvedID = list.ID
		} else {
			p.logger.Warn("could not resolve active ClickUp list", "requested_list_id", requestedID, "error", remoteErr)
		}
	}
	p.activeMu.Lock()
	p.activeList = resolvedID
	p.scopeSet = requestedID != ""
	p.activeMu.Unlock()
	p.logger.Info("active ClickUp task list set", "requested_list_id", requestedID, "local_list_id", resolvedID)
}

func (p *cachedProvider) activeTaskList() domain.ListID {
	p.activeMu.RLock()
	defer p.activeMu.RUnlock()
	return p.activeList
}

func (p *cachedProvider) hasActiveTaskScope() bool {
	p.activeMu.RLock()
	defer p.activeMu.RUnlock()
	return p.scopeSet
}

func (p *cachedProvider) SetSyncProgress(callback func(string)) {
	p.progressMu.Lock()
	p.progress = callback
	p.progressMu.Unlock()
}

func (p *cachedProvider) reportProgress(message string) {
	p.progressMu.RLock()
	callback := p.progress
	p.progressMu.RUnlock()
	if callback != nil {
		callback(message)
	}
}

func (p *cachedProvider) Pull(ctx context.Context) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	p.pullMu.Lock()
	defer p.pullMu.Unlock()
	return p.runPull(ctx, func() error { return p.pull(ctx) })
}

func (p *cachedProvider) FetchListsIntoCache(ctx context.Context, requestedSpaceID domain.SpaceID) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	p.pullMu.Lock()
	defer p.pullMu.Unlock()
	return p.runPull(ctx, func() error { return p.pullLists(ctx, requestedSpaceID) })
}

func (p *cachedProvider) FetchTasksIntoCache(ctx context.Context, requestedListID domain.ListID) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	p.pullMu.Lock()
	defer p.pullMu.Unlock()
	return p.runPull(ctx, func() error { return p.pullTasks(ctx, requestedListID) })
}

func (p *cachedProvider) FetchTaskIntoCache(ctx context.Context, requestedTaskID domain.TaskID) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	p.pullMu.Lock()
	defer p.pullMu.Unlock()
	return p.runPull(ctx, func() error { return p.pullTask(ctx, requestedTaskID) })
}

func (p *cachedProvider) runPull(ctx context.Context, pull func() error) error {
	if err := p.store.UpdateProviderSyncState(ctx, p.ID(), domain.SyncStateSyncing, nil, nil, nil); err != nil {
		return fmt.Errorf("mark provider %s syncing: %w", p.ID(), err)
	}
	if err := pull(); err != nil {
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

func (p *cachedProvider) pullLists(ctx context.Context, requestedSpaceID domain.SpaceID) error {
	var spaces []domain.Space
	var seenSpaces map[string]struct{}
	if requestedSpaceID != "" {
		space, err := p.store.GetSpaceByProvider(ctx, p.ID(), requestedSpaceID)
		if err != nil {
			return fmt.Errorf("resolve space %s: %w", requestedSpaceID, err)
		}
		spaces = append(spaces, space)
	} else {
		var err error
		spaces, seenSpaces, err = p.pullSpaces(ctx)
		if err != nil {
			return err
		}
	}

	for _, space := range spaces {
		_, seenLists, err := p.pullListsForSpace(ctx, space)
		if err != nil {
			return err
		}
		if err := p.reconcileMissingLists(ctx, space.ID, seenLists); err != nil {
			return err
		}
	}
	if requestedSpaceID == "" {
		if err := p.reconcileMissingSpaces(ctx, seenSpaces); err != nil {
			return err
		}
	}
	return nil
}

func (p *cachedProvider) pullSpaces(ctx context.Context) ([]domain.Space, map[string]struct{}, error) {
	p.reportProgress("fetching spaces")
	remoteSpaces, err := p.Provider.FetchSpaces(ctx)
	if err != nil {
		p.logger.Error("ClickUp space fetch failed", "provider_id", p.ID(), "error", SafeErrorText(err))
		return nil, nil, err
	}
	p.logger.Info("ClickUp spaces fetched", "provider_id", p.ID(), "spaces", len(remoteSpaces))
	spaces := make([]domain.Space, 0, len(remoteSpaces))
	seen := make(map[string]struct{}, len(remoteSpaces))
	for _, space := range remoteSpaces {
		if space.ProviderID != p.ID() {
			return nil, nil, fmt.Errorf("%w: space %s belongs to %s, provider is %s", domain.ErrProviderMismatch, space.ID, space.ProviderID, p.ID())
		}
		stored, conflict, err := p.reconcileSpace(ctx, space)
		if err != nil {
			return nil, nil, fmt.Errorf("store space %s: %w", space.ID, err)
		}
		spaces = append(spaces, stored)
		if space.RemoteID != nil {
			seen[*space.RemoteID] = struct{}{}
		}
		if metadata, ok := p.Provider.(providerpkg.StatusMetadataProvider); ok && !conflict {
			for _, item := range metadata.SpaceStatusMetadata(stored) {
				if err := p.store.UpsertMetadata(ctx, item); err != nil {
					return nil, nil, fmt.Errorf("store status metadata for space %s: %w", stored.ID, err)
				}
			}
		}
	}
	return spaces, seen, nil
}

func (p *cachedProvider) pullListsForSpace(ctx context.Context, space domain.Space) ([]domain.List, map[string]struct{}, error) {
	p.reportProgress("fetching lists from space " + space.ID.String())
	p.logger.Info("fetching ClickUp lists", "provider_id", p.ID(), "space_id", space.ID)
	lists, err := p.Provider.FetchLists(ctx, space.ID)
	if err != nil {
		p.logger.Error("ClickUp list fetch failed", "provider_id", p.ID(), "space_id", space.ID, "error", SafeErrorText(err))
		return nil, nil, fmt.Errorf("fetch lists for space %s: %w", space.ID, err)
	}
	p.logger.Info("ClickUp lists fetched", "provider_id", p.ID(), "space_id", space.ID, "lists", len(lists))
	storedLists := make([]domain.List, 0, len(lists))
	seen := make(map[string]struct{}, len(lists))
	for _, list := range lists {
		if list.ProviderID != p.ID() || list.SpaceID != space.ID {
			return nil, nil, fmt.Errorf("%w: list %s has invalid provider or parent", domain.ErrProviderMismatch, list.ID)
		}
		stored, conflict, err := p.reconcileList(ctx, list)
		if err != nil {
			return nil, nil, fmt.Errorf("store list %s: %w", list.ID, err)
		}
		storedLists = append(storedLists, stored)
		if list.RemoteID != nil {
			seen[*list.RemoteID] = struct{}{}
		}
		if metadata, ok := p.Provider.(providerpkg.StatusMetadataProvider); ok && !conflict {
			for _, item := range metadata.ListStatusMetadata(stored) {
				if err := p.store.UpsertMetadata(ctx, item); err != nil {
					return nil, nil, fmt.Errorf("store status metadata for list %s: %w", stored.ID, err)
				}
			}
		}
	}
	return storedLists, seen, nil
}

func (p *cachedProvider) pullTasks(ctx context.Context, requestedListID domain.ListID) error {
	list, err := p.store.GetListByProvider(ctx, p.ID(), requestedListID)
	if err != nil {
		return fmt.Errorf("resolve list %s: %w", requestedListID, err)
	}
	return p.pullTasksForList(ctx, list)
}

func (p *cachedProvider) pullTasksForList(ctx context.Context, list domain.List) error {
	p.reportProgress("fetching tasks from list " + list.ID.String())
	tasks, err := p.Provider.FetchTasks(ctx, list.ID)
	if err != nil {
		return fmt.Errorf("fetch tasks for list %s: %w", list.ID, err)
	}
	seenTasks := make(map[string]struct{}, len(tasks))
	if err := p.reconcileTasks(ctx, list.ID, tasks, seenTasks); err != nil {
		return err
	}
	return p.reconcileMissingTasks(ctx, list.ID, seenTasks)
}

func (p *cachedProvider) pullTask(ctx context.Context, requestedTaskID domain.TaskID) error {
	task, err := p.store.GetTaskByProvider(ctx, p.ID(), requestedTaskID)
	if err != nil {
		return fmt.Errorf("resolve task %s: %w", requestedTaskID, err)
	}
	remote, err := p.Provider.FetchTask(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("fetch task %s: %w", task.ID, err)
	}
	if remote.ID != task.ID || remote.ProviderID != p.ID() || remote.ListID != task.ListID {
		return fmt.Errorf("%w: fetched task %s has invalid identity or parent", domain.ErrProviderMismatch, task.ID)
	}
	if _, _, err := p.reconcileTask(ctx, remote); err != nil {
		return fmt.Errorf("store task %s: %w", task.ID, err)
	}
	return nil
}

func (p *cachedProvider) pull(ctx context.Context) error {
	p.logger.Info("ClickUp pull started", "provider_id", p.ID())
	spaces, seenSpaces, err := p.pullSpaces(ctx)
	if err != nil {
		return err
	}
	for _, space := range spaces {
		lists, seenLists, err := p.pullListsForSpace(ctx, space)
		if err != nil {
			return err
		}
		for _, list := range lists {
			activeList := p.activeTaskList()
			scopeSet := p.hasActiveTaskScope()
			p.logger.Debug("evaluating ClickUp task scope", "active_list_id", activeList, "stored_list_id", list.ID, "remote_list_id", remoteIDText(list.RemoteID))
			if scopeSet && list.ID != activeList {
				continue
			}
			p.logger.Info("fetching ClickUp tasks", "list_id", list.ID, "remote_list_id", remoteIDText(list.RemoteID))
			if err := p.pullTasksForList(ctx, list); err != nil {
				return err
			}
		}
		if err := p.reconcileMissingLists(ctx, space.ID, seenLists); err != nil {
			return err
		}
	}
	if err := p.reconcileMissingSpaces(ctx, seenSpaces); err != nil {
		return err
	}
	return nil
}

func remoteIDText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func remoteTaskIDs(tasks []domain.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.RemoteID != nil {
			ids = append(ids, *task.RemoteID)
		}
	}
	return ids
}

func (p *cachedProvider) reconcileTasks(ctx context.Context, listID domain.ListID, tasks []domain.Task, seen map[string]struct{}) error {
	// ClickUp's mapper may use batch-local IDs for out-of-order parents. Resolve
	// those placeholders to remote identities before saving either task so a
	// later import cannot leave a child pointing at an old mapper ID.
	remoteByMappedID := make(map[domain.TaskID]string, len(tasks))
	for _, task := range tasks {
		if task.RemoteID != nil {
			remoteByMappedID[task.ID] = *task.RemoteID
		}
	}
	pending := append([]domain.Task(nil), tasks...)
	for len(pending) > 0 {
		progress := false
		next := pending[:0]
		for _, task := range pending {
			if task.ProviderID != p.ID() || task.ListID != listID {
				return fmt.Errorf("%w: task %s has invalid provider or parent", domain.ErrProviderMismatch, task.ID)
			}
			task.ListID = listID
			if task.ParentTaskID != nil {
				parentRemoteID := remoteByMappedID[*task.ParentTaskID]
				if parentRemoteID != "" {
					parent, err := p.store.GetTaskByRemoteID(ctx, p.ID(), parentRemoteID)
					if errors.Is(err, domain.ErrNotFound) {
						next = append(next, task)
						continue
					}
					if err != nil {
						return fmt.Errorf("resolve parent task %s: %w", parentRemoteID, err)
					}
					task.ParentTaskID = &parent.ID
				}
			}
			if task.RemoteID != nil {
				if existing, lookupErr := p.store.GetTaskByRemoteID(ctx, p.ID(), *task.RemoteID); lookupErr == nil && hasLocalChanges(existing.SyncState) {
					progress = true
					seen[*task.RemoteID] = struct{}{}
					continue
				}
			}
			_, _, err := p.reconcileTask(ctx, task)
			if err != nil {
				return fmt.Errorf("store task %s: %w", task.ID, err)
			}
			if task.RemoteID != nil {
				seen[*task.RemoteID] = struct{}{}
			}
			progress = true
		}
		if !progress {
			return fmt.Errorf("reconcile tasks: unresolved parent dependency")
		}
		pending = next
	}
	return nil
}

func (p *cachedProvider) CreateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := p.check(ctx); err != nil {
		return domain.Task{}, err
	}
	p.logger.Info("ClickUp task create started", "provider_id", p.ID(), "task_id", task.ID, "remote_task_id", remoteIDText(task.RemoteID))
	created, err := p.Provider.CreateTask(ctx, task)
	if err != nil {
		p.logger.Error("ClickUp task create failed", "provider_id", p.ID(), "task_id", task.ID, "error", SafeErrorText(err))
		return domain.Task{}, err
	}
	created = pushedTask(task, created)
	if _, err := p.store.UpsertTask(ctx, created); err != nil {
		return domain.Task{}, fmt.Errorf("store created task %s: %w", task.ID, err)
	}
	if err := p.savePushBase(ctx, created); err != nil {
		return domain.Task{}, fmt.Errorf("save created task %s sync base: %w", task.ID, err)
	}
	p.logger.Info("ClickUp task create completed", "provider_id", p.ID(), "task_id", task.ID, "remote_task_id", remoteIDText(created.RemoteID))
	return created, nil
}

func (p *cachedProvider) UpdateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := p.check(ctx); err != nil {
		return domain.Task{}, err
	}
	task = p.persistedTaskIdentity(ctx, task)
	p.logger.Info("ClickUp task update started", "provider_id", p.ID(), "task_id", task.ID, "remote_task_id", remoteIDText(task.RemoteID))
	updated, err := p.Provider.UpdateTask(ctx, task)
	if err != nil {
		p.logger.Error("ClickUp task update failed", "provider_id", p.ID(), "task_id", task.ID, "remote_task_id", remoteIDText(task.RemoteID), "error", SafeErrorText(err))
		return domain.Task{}, err
	}
	updated = pushedTask(task, updated)
	if _, err := p.store.UpsertTask(ctx, updated); err != nil {
		return domain.Task{}, fmt.Errorf("store updated task %s: %w", task.ID, err)
	}
	if err := p.savePushBase(ctx, updated); err != nil {
		return domain.Task{}, fmt.Errorf("save updated task %s sync base: %w", task.ID, err)
	}
	p.logger.Info("ClickUp task update completed", "provider_id", p.ID(), "task_id", task.ID, "remote_task_id", remoteIDText(updated.RemoteID))
	return updated, nil
}

func (p *cachedProvider) savePushBase(ctx context.Context, task domain.Task) error {
	payload, err := json.Marshal(entityFields(task))
	if err != nil {
		return err
	}
	base := task.SyncBase()
	base.Payload = payload
	base.SyncState = domain.SyncStateSynced
	base.CapturedAt = time.Now().UTC()
	base.UpdatedAt = base.CapturedAt
	return p.store.SaveBase(ctx, base)
}

func (p *cachedProvider) DeleteTask(ctx context.Context, task domain.Task) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	task = p.persistedTaskIdentity(ctx, task)
	return p.Provider.DeleteTask(ctx, task)
}

func (p *cachedProvider) persistedTaskIdentity(ctx context.Context, task domain.Task) domain.Task {
	if task.RemoteID != nil || task.ID.IsZero() {
		return task
	}
	persisted, err := p.store.GetTaskByProvider(ctx, p.ID(), task.ID)
	if err == nil && persisted.RemoteID != nil {
		task.RemoteID = cloneString(persisted.RemoteID)
	}
	return task
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

func (p *cachedProvider) reconcileSpace(ctx context.Context, remote domain.Space) (domain.Space, bool, error) {
	value, conflict, err := reconcileEntity(ctx, p, remote, remote.RemoteID, domain.EntityTypeSpace, func() (any, error) {
		return p.store.GetSpaceByRemoteID(ctx, p.ID(), *remote.RemoteID)
	}, func(existing any, merged map[string]any, state domain.SyncState) (any, error) {
		local := existing.(domain.Space)
		mapped, err := mapEntity(merged, remote)
		if err != nil {
			return domain.Space{}, err
		}
		value := mapped.(domain.Space)
		value.ID, value.ProviderID, value.SyncState = local.ID, p.ID(), state
		return p.store.UpsertSpace(ctx, value)
	}, func(value any) domain.SyncBase { return value.(domain.Space).SyncBase() })
	return value.(domain.Space), conflict, err
}

func (p *cachedProvider) reconcileList(ctx context.Context, remote domain.List) (domain.List, bool, error) {
	value, conflict, err := reconcileEntity(ctx, p, remote, remote.RemoteID, domain.EntityTypeList, func() (any, error) {
		return p.store.GetListByRemoteID(ctx, p.ID(), *remote.RemoteID)
	}, func(existing any, merged map[string]any, state domain.SyncState) (any, error) {
		local := existing.(domain.List)
		mapped, err := mapEntity(merged, remote)
		if err != nil {
			return domain.List{}, err
		}
		value := mapped.(domain.List)
		value.ID, value.ProviderID, value.SpaceID, value.SyncState = local.ID, p.ID(), local.SpaceID, state
		return p.store.UpsertList(ctx, value)
	}, func(value any) domain.SyncBase { return value.(domain.List).SyncBase() })
	return value.(domain.List), conflict, err
}

func (p *cachedProvider) reconcileTask(ctx context.Context, remote domain.Task) (domain.Task, bool, error) {
	value, conflict, err := reconcileEntity(ctx, p, remote, remote.RemoteID, domain.EntityTypeTask, func() (any, error) {
		return p.store.GetTaskByRemoteID(ctx, p.ID(), *remote.RemoteID)
	}, func(existing any, merged map[string]any, state domain.SyncState) (any, error) {
		local := existing.(domain.Task)
		mapped, err := mapEntity(merged, remote)
		if err != nil {
			return domain.Task{}, err
		}
		value := mapped.(domain.Task)
		value.ID, value.ProviderID, value.ListID, value.SyncState = local.ID, p.ID(), local.ListID, state
		return p.store.UpsertTask(ctx, value)
	}, func(value any) domain.SyncBase { return value.(domain.Task).SyncBase() })
	return value.(domain.Task), conflict, err
}

// reconcileEntity is deliberately local to the bootstrap pull boundary. The
// provider returns complete snapshots, while pending local mutations remain
// authoritative until their queue operation succeeds.
func reconcileEntity(ctx context.Context, p *cachedProvider, remote any, remoteID *string, entityType domain.EntityType,
	lookup func() (any, error), save func(any, map[string]any, domain.SyncState) (any, error), baseOf func(any) domain.SyncBase) (any, bool, error) {
	if remoteID == nil || strings.TrimSpace(*remoteID) == "" {
		return nil, false, errors.New("pull entity has no remote ID")
	}
	existing, err := lookup()
	if errors.Is(err, domain.ErrNotFound) {
		value, err := save(remote, entityFields(remote), domain.SyncStateSynced)
		if err != nil {
			return nil, false, err
		}
		if err := p.savePullBase(ctx, baseOf(value), value); err != nil {
			return nil, false, err
		}
		return value, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	localFields := entityFields(existing)
	remoteFields := entityFields(remote)
	baseFields := make(map[string]any)
	entityID := entityID(existing)
	base, baseErr := p.store.GetBase(ctx, p.ID(), entityType, entityID)
	if baseErr == nil && len(base.Payload) > 0 {
		if err := json.Unmarshal(base.Payload, &baseFields); err != nil {
			return nil, false, fmt.Errorf("decode sync base for %s/%s: %w", entityType, entityID, err)
		}
		if baseFields == nil {
			return nil, false, fmt.Errorf("decode sync base for %s/%s: expected object", entityType, entityID)
		}
	} else if baseErr != nil && !errors.Is(baseErr, domain.ErrNotFound) {
		return nil, false, fmt.Errorf("load sync base for %s/%s: %w", entityType, entityID, baseErr)
	}
	if entityDeleted(existing) {
		return existing, false, nil
	}
	result, err := foundationsync.MergeFields(foundationsync.MergeInput{
		ProviderID: foundationsync.ProviderID(p.ID()), EntityType: foundationsync.EntityType(entityType), EntityID: entityID,
		Base: baseFields, Local: localFields, Remote: remoteFields,
	})
	if err != nil {
		return nil, false, err
	}
	state := domain.SyncStateSynced
	if entitySyncState(existing) != domain.SyncStateSynced {
		state = entitySyncState(existing)
	}
	if len(result.Conflicts) > 0 {
		state = domain.SyncStateConflict
	}
	mergedFields := result.Fields
	if hasLocalChanges(entitySyncState(existing)) {
		// Do not let a provider snapshot replace the retained local object while
		// a queued local mutation is outstanding. Remote changes are fetched on
		// the next pull after the push resolves the pending state.
		mergedFields = localFields
	}
	value, err := save(existing, mergedFields, state)
	if err != nil {
		return nil, false, err
	}
	if len(result.Conflicts) > 0 {
		for _, conflict := range result.Conflicts {
			if err := p.store.UpdateConflict(ctx, domain.Conflict{ID: domain.ConflictID(conflict.ID), ProviderID: p.ID(), EntityType: entityType, EntityID: conflict.EntityID, Fields: []domain.FieldConflict{{Field: conflict.Field, BaseValue: jsonValue(conflict.BaseValue), LocalValue: jsonValue(conflict.LocalValue), RemoteValue: jsonValue(conflict.RemoteValue)}}, CreatedAt: conflict.CreatedAt, UpdatedAt: conflict.CreatedAt}); err != nil {
				return nil, true, err
			}
		}
		return value, true, nil
	}
	if state == domain.SyncStateSynced {
		if err := p.savePullBase(ctx, baseOf(value), value); err != nil {
			return nil, false, err
		}
	}
	return value, false, nil
}

func (p *cachedProvider) savePullBase(ctx context.Context, base domain.SyncBase, value any) error {
	payload, err := json.Marshal(entityFields(value))
	if err != nil {
		return err
	}
	base.Payload = payload
	base.SyncState = domain.SyncStateSynced
	base.CapturedAt = time.Now().UTC()
	base.UpdatedAt = base.CapturedAt
	return p.store.SaveBase(ctx, base)
}

func entityFields(value any) map[string]any {
	payload, _ := json.Marshal(value)
	fields := make(map[string]any)
	_ = json.Unmarshal(payload, &fields)
	for _, key := range []string{"id", "provider_id", "space_id", "list_id", "created_at", "updated_at", "sync_state", "remote_updated_at", "is_deleted", "deleted_at"} {
		delete(fields, key)
	}
	return fields
}

func entityDeleted(value any) bool {
	switch value := value.(type) {
	case domain.Space:
		return value.IsDeleted
	case domain.List:
		return value.IsDeleted
	case domain.Task:
		return value.IsDeleted
	default:
		return false
	}
}

func mapEntity(fields map[string]any, template any) (any, error) {
	payload, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	switch template.(type) {
	case domain.Space:
		var value domain.Space
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		return value, nil
	case domain.List:
		var value domain.List
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		return value, nil
	case domain.Task:
		var value domain.Task
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported pull entity template %T", template)
	}
}

func entityID(value any) string {
	switch value := value.(type) {
	case domain.Space:
		return value.ID.String()
	case domain.List:
		return value.ID.String()
	case domain.Task:
		return value.ID.String()
	default:
		return ""
	}
}

func entitySyncState(value any) domain.SyncState {
	switch value := value.(type) {
	case domain.Space:
		return value.SyncState
	case domain.List:
		return value.SyncState
	case domain.Task:
		return value.SyncState
	default:
		return domain.SyncStateLocal
	}
}

func jsonValue(value any) []byte {
	if value == nil {
		return nil
	}
	payload, _ := json.Marshal(value)
	return payload
}

func (p *cachedProvider) reconcileMissingSpaces(ctx context.Context, seen map[string]struct{}) error {
	spaces, err := p.store.ListSpaces(ctx, p.ID())
	if err != nil {
		return fmt.Errorf("list cached spaces for deletion reconciliation: %w", err)
	}
	for _, space := range spaces {
		if space.RemoteID == nil || hasRemote(seen, *space.RemoteID) || hasLocalChanges(space.SyncState) {
			continue
		}
		if err := p.store.DeleteSpace(ctx, space.ID); err != nil {
			return fmt.Errorf("tombstone missing space %s: %w", space.ID, err)
		}
	}
	return nil
}

func (p *cachedProvider) reconcileMissingLists(ctx context.Context, spaceID domain.SpaceID, seen map[string]struct{}) error {
	lists, err := p.store.ListBySpace(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("list cached lists for deletion reconciliation: %w", err)
	}
	for _, list := range lists {
		if list.RemoteID == nil || hasRemote(seen, *list.RemoteID) || hasLocalChanges(list.SyncState) {
			continue
		}
		if err := p.store.DeleteList(ctx, list.ID); err != nil {
			return fmt.Errorf("tombstone missing list %s: %w", list.ID, err)
		}
	}
	return nil
}

func (p *cachedProvider) reconcileMissingTasks(ctx context.Context, listID domain.ListID, seen map[string]struct{}) error {
	tasks, err := p.store.ListByList(ctx, listID)
	if err != nil {
		return fmt.Errorf("list cached tasks for deletion reconciliation: %w", err)
	}
	for _, task := range tasks {
		if task.RemoteID == nil || hasRemote(seen, *task.RemoteID) || hasLocalChanges(task.SyncState) {
			continue
		}
		if err := p.store.DeleteTask(ctx, task.ID); err != nil {
			return fmt.Errorf("tombstone missing task %s: %w", task.ID, err)
		}
	}
	return nil
}

func hasRemote(seen map[string]struct{}, remoteID string) bool {
	_, ok := seen[remoteID]
	return ok
}

func hasLocalChanges(state domain.SyncState) bool {
	switch state {
	case domain.SyncStatePending, domain.SyncStateSyncing, domain.SyncStateFailed, domain.SyncStateConflict:
		return true
	default:
		return false
	}
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
	if result.Assignee == "" {
		result.Assignee = original.Assignee
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

// foundationSyncController runs only explicitly requested remote operations.
type foundationSyncController struct {
	engine   *foundationsync.Engine
	enabled  bool
	fetchers map[ProviderID]*cachedProvider
	events   chan foundationsync.Event

	mu        sync.Mutex
	closing   bool
	active    sync.WaitGroup
	cancelers map[uint64]context.CancelFunc
	nextID    uint64
}

var _ SyncController = (*foundationSyncController)(nil)

func newFoundationSyncController(engine *foundationsync.Engine, enabled bool, fetchers map[ProviderID]*cachedProvider, events chan foundationsync.Event) *foundationSyncController {
	if events == nil {
		events = make(chan foundationsync.Event, 64)
	}
	return &foundationSyncController{
		engine:    engine,
		enabled:   enabled,
		fetchers:  fetchers,
		events:    events,
		cancelers: make(map[uint64]context.CancelFunc),
	}
}

func (s *foundationSyncController) Events() <-chan foundationsync.Event {
	if s == nil {
		return nil
	}
	return s.events
}

func (s *foundationSyncController) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("stop foundation sync requests: nil context")
	}
	s.mu.Lock()
	s.closing = true
	cancelers := make([]context.CancelFunc, 0, len(s.cancelers))
	for _, cancel := range s.cancelers {
		cancelers = append(cancelers, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancelers {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop foundation sync requests: %w", ctx.Err())
	}
}

func (s *foundationSyncController) Trigger(ctx context.Context, providerID ProviderID, listID domain.ListID) error {
	if s == nil || s.engine == nil || !s.enabled {
		return nil
	}
	if ctx == nil {
		return errors.New("request provider sync: nil context")
	}
	if providerID != "" {
		worker, ok := s.engine.Worker(domain.ProviderID(providerID))
		if !ok {
			return nil
		}
		if fetcher := s.fetchers[providerID]; fetcher != nil {
			fetcher.SetActiveTaskList(listID)
		}
		return s.schedule(ctx, providerID, worker.SyncOnce, false)
	}
	for _, status := range s.engine.Statuses() {
		worker, ok := s.engine.Worker(status.ProviderID)
		if !ok {
			continue
		}
		if fetcher := s.fetchers[ProviderID(status.ProviderID)]; fetcher != nil {
			fetcher.SetActiveTaskList("")
		}
		if err := s.schedule(ctx, ProviderID(status.ProviderID), worker.SyncOnce, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *foundationSyncController) Fetch(ctx context.Context, input command.Command) error {
	if s == nil || !s.enabled {
		return nil
	}
	if ctx == nil {
		return errors.New("request scoped fetch: nil context")
	}
	fetcher := s.fetchers[ProviderID(input.ProviderID)]
	if fetcher == nil {
		return nil
	}
	var fetch func(context.Context) error
	switch input.Kind {
	case command.KindFetchLists:
		spaceID := domain.SpaceID(input.SpaceID)
		fetch = func(ctx context.Context) error { return fetcher.FetchListsIntoCache(ctx, spaceID) }
	case command.KindFetchTasks:
		listID := domain.ListID(input.ListID)
		fetch = func(ctx context.Context) error { return fetcher.FetchTasksIntoCache(ctx, listID) }
	case command.KindFetchTask:
		taskID := domain.TaskID(input.TaskID)
		fetch = func(ctx context.Context) error { return fetcher.FetchTaskIntoCache(ctx, taskID) }
	default:
		return fmt.Errorf("request scoped fetch: unsupported command %q", input.Kind)
	}
	return s.schedule(ctx, ProviderID(input.ProviderID), fetch, true)
}

func (s *foundationSyncController) schedule(ctx context.Context, providerID ProviderID, run func(context.Context) error, report bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return errors.New("sync requests are shutting down")
	}
	s.nextID++
	id := s.nextID
	runCtx, cancel := context.WithCancel(ctx)
	s.cancelers[id] = cancel
	s.active.Add(1)
	s.mu.Unlock()

	if report {
		s.emit(foundationsync.Event{ProviderID: domain.ProviderID(providerID), Kind: foundationsync.EventSyncStarted, State: foundationsync.WorkerRunning, At: time.Now().UTC()})
	}
	go func() {
		defer s.active.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.cancelers, id)
			s.mu.Unlock()
		}()

		err := run(runCtx)
		if !report || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			s.emit(foundationsync.Event{ProviderID: domain.ProviderID(providerID), Kind: foundationsync.EventSyncFailed, State: foundationsync.WorkerFailed, Err: err, At: time.Now().UTC()})
			return
		}
		s.emit(foundationsync.Event{ProviderID: domain.ProviderID(providerID), Kind: foundationsync.EventSyncCompleted, State: foundationsync.WorkerSynced, At: time.Now().UTC()})
	}()
	return nil
}

func (s *foundationSyncController) emit(event foundationsync.Event) {
	if s == nil || s.events == nil {
		return
	}
	select {
	case s.events <- event:
	default:
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
	if input.Kind == command.KindFetchLists || input.Kind == command.KindFetchTasks || input.Kind == command.KindFetchTask {
		if h.sync != nil {
			if err := h.sync.Fetch(ctx, input); err != nil {
				return command.Event{}, err
			}
		}
		return command.Event{Kind: command.EventRefresh}, nil
	}
	if input.Kind == command.KindRefresh {
		if h.sync != nil {
			if err := h.sync.Trigger(ctx, ProviderID(input.ProviderID), domain.ListID(input.ListID)); err != nil {
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
		if input.Title != "" || input.EditAllFields {
			value := input.Title
			patch.Title = &value
		}
		if input.Description != "" || input.EditAllFields {
			value := input.Description
			patch.Description = &value
		}
		if input.EditAllFields {
			value := input.Assignee
			patch.Assignee = &value
		}
		if input.Status != "" || input.EditAllFields {
			value := input.Status
			patch.Status = &value
		}
		if input.Priority != "" || input.EditAllFields {
			value := domain.Priority(input.Priority)
			patch.Priority = &value
		}
		if input.DueAt != nil {
			patch.DueAt = cloneTime(input.DueAt)
		}
		patch.ClearDueAt = input.ClearDueAt
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
// the model accepts input; passive navigation uses cached local data.
type foundationUIController struct {
	terminal Terminal
	handler  command.Handler
	load     ViewLoader
	loadList func(context.Context, ProviderID, ListID) (View, error)
	events   <-chan foundationsync.Event

	mu          sync.RWMutex
	model       foundationtui.Model
	initialized bool
	accepting   bool
	eventCancel context.CancelFunc
	eventDone   chan struct{}

	program    *tea.Program
	charm      *foundationtui.CharmModel
	running    bool
	activeList *foundationTaskScope

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

func newFoundationUIControllerWithListLoader(terminal Terminal, handler command.Handler, load ViewLoader, loadList func(context.Context, ProviderID, ListID) (View, error), events <-chan foundationsync.Event) (*foundationUIController, error) {
	u, err := newFoundationUIController(terminal, handler, load, events)
	if err != nil {
		return nil, err
	}
	u.loadList = loadList
	return u, nil
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
	u.model.UI.ActiveProviderID = foundationtui.ProviderID(state.ProviderID)
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
	if state.ProviderID != "" && state.ListID != "" {
		u.activeList = &foundationTaskScope{
			providerID: ProviderID(state.ProviderID),
			spaceID:    SpaceID(state.SpaceID),
			listID:     ListID(state.ListID),
		}
	} else {
		u.activeList = nil
	}
	u.model.UI.Filter = foundationtui.Filter{}
	u.model.UI.FilterActive = false
	if strings.TrimSpace(state.Filter) != "" {
		filter, err := foundationtui.ParseFilter(state.Filter)
		if err == nil {
			u.model.UI.Filter = filter
			u.model.UI.FilterActive = true
		}
	}
	u.model.UI.CollapsedGroups = make(map[string]bool, len(state.CollapsedGroups))
	for _, key := range state.CollapsedGroups {
		if key = strings.TrimSpace(key); key != "" {
			u.model.UI.CollapsedGroups[key] = true
		}
	}
	u.model.UI.FocusedGroup = ""
	u.model.UI.TaskGroupCursor = 0
	u.model.UI.TaskHeaderSelected = false
	u.model.UI.TaskHeaderTask = foundationtui.TaskRef{}
	u.model.UI.GroupBy = foundationtui.TaskGroupNone
	if strings.TrimSpace(state.GroupBy) != "" {
		if group, err := foundationtui.ParseTaskGroupMode(state.GroupBy); err == nil {
			u.model.UI.GroupBy = group
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
	if u.activeList != nil {
		state.ProviderID = string(u.activeList.providerID)
		state.SpaceID = string(u.activeList.spaceID)
		state.ListID = string(u.activeList.listID)
	}
	if model.UI.FilterActive {
		state.Filter = model.UI.Filter.String()
	}
	state.GroupBy = string(model.UI.GroupBy)
	for key, collapsed := range model.UI.CollapsedGroups {
		if collapsed && strings.TrimSpace(key) != "" {
			state.CollapsedGroups = append(state.CollapsedGroups, key)
		}
	}
	sort.Strings(state.CollapsedGroups)
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
	u.mu.RLock()
	activeList := u.activeList
	u.mu.RUnlock()
	var view View
	var err error
	if activeList != nil && u.loadList != nil {
		view, err = u.loadList(ctx, activeList.providerID, activeList.listID)
	} else {
		view, err = u.load(ctx)
	}
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
			if input.FetchRemote {
				_, err := u.handler.Handle(ctx, command.Command{
					Kind:       command.KindFetchTasks,
					ProviderID: string(input.ProviderID),
					ListID:     string(input.ListID),
				})
				if err != nil {
					return foundationtui.ErrorMsg{Err: err, Text: SafeErrorText(err)}
				}
			}
			var view View
			var err error
			if input.ListID != "" {
				u.mu.Lock()
				u.activeList = &foundationTaskScope{providerID: ProviderID(input.ProviderID), spaceID: SpaceID(input.SpaceID), listID: ListID(input.ListID)}
				u.mu.Unlock()
				if u.loadList != nil {
					view, err = u.loadList(ctx, ProviderID(input.ProviderID), ListID(input.ListID))
				} else {
					view, err = u.load(ctx)
				}
			} else {
				view, err = u.load(ctx)
			}
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
	case foundationsync.EventProgress:
		message.State = foundationtui.SyncStateSyncing
		message.Message = event.Message
		return message, false, true
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
	return message, false, true
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
	case foundationtui.CommandCreateSpace:
		return command.Command{Kind: command.KindCreateSpace, ProviderID: string(input.ProviderID), Title: input.Title}, true, nil
	case foundationtui.CommandCreateList:
		return command.Command{Kind: command.KindCreateList, ProviderID: string(input.ProviderID), SpaceID: string(input.SpaceID), Title: input.Title}, true, nil
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
			Kind:          command.KindUpdateTask,
			ProviderID:    string(input.ProviderID),
			ListID:        string(input.ListID),
			TaskID:        string(input.TaskID),
			Title:         input.Title,
			Description:   input.Description,
			Assignee:      input.Assignee,
			Status:        input.Status,
			Priority:      string(input.Priority),
			DueAt:         cloneTime(input.DueAt),
			ClearDueAt:    input.ClearDueAt,
			EditAllFields: input.EditAllFields,
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
	case foundationtui.CommandFetchLists:
		return command.Command{Kind: command.KindFetchLists, ProviderID: string(input.ProviderID), SpaceID: string(input.SpaceID)}, true, nil
	case foundationtui.CommandFetchTasks:
		return command.Command{Kind: command.KindFetchTasks, ProviderID: string(input.ProviderID), ListID: string(input.ListID)}, true, nil
	case foundationtui.CommandFetchTask:
		return command.Command{Kind: command.KindFetchTask, ProviderID: string(input.ProviderID), ListID: string(input.ListID), TaskID: string(input.TaskID)}, true, nil
	case foundationtui.CommandRefresh:
		return command.Command{Kind: command.KindRefresh, ProviderID: string(input.ProviderID), ListID: string(input.ListID)}, true, nil
	case foundationtui.CommandQuit:
		return command.Command{Kind: command.KindQuit}, true, nil
	case foundationtui.CommandFilter, foundationtui.CommandGroup, foundationtui.CommandHelp:
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
			Assignee:        value.Assignee,
			Title:           value.Title,
			Description:     value.Description,
			Status:          value.Status,
			Priority:        domain.Priority(value.Priority),
			TimeEstimate:    cloneDuration(value.TimeEstimate),
			TimeTracked:     cloneDuration(value.TimeTracked),
			DueAt:           cloneTime(value.DueAt),
			CompletedAt:     cloneTime(value.CompletedAt),
			SyncState:       domain.SyncState(value.SyncState),
			RemoteUpdatedAt: cloneTime(value.RemoteUpdatedAt),
			CreatedAt:       value.CreatedAt,
			UpdatedAt:       value.UpdatedAt,
		})
	}
	snapshot := foundationtui.SnapshotFromDomain(foundationtui.DomainSnapshot{
		Providers: providers,
		Spaces:    spaces,
		Lists:     lists,
		Tasks:     tasks,
	})
	for _, option := range view.EditorOptions {
		snapshot.EditorOptions = append(snapshot.EditorOptions, foundationtui.TaskEditorOptions{
			ProviderID: foundationtui.ProviderID(option.ProviderID),
			SpaceID:    foundationtui.SpaceID(option.SpaceID),
			ListID:     foundationtui.ListID(option.ListID),
			Statuses:   append([]string(nil), option.Statuses...),
		})
	}
	return snapshot
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

	cliStore     *sqlite.Store
	cliProviders *app.Registry
	cliService   *app.Service
	cliEngine    *foundationsync.Engine
}

func buildFoundationGraph(ctx context.Context, cfg Config, terminal Terminal, logger *slog.Logger) (*foundationGraph, error) {
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
	fetchers := make(map[ProviderID]*cachedProvider)
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
				ResponseLogger: func(method, endpoint string, statusCode int, body []byte) {
					logger.Info("ClickUp response", "method", method, "endpoint", endpoint, "status_code", statusCode, "body", string(body))
				},
			}),
			ParentResolver:      foundationParentResolver(store),
			RemoteListResolver:  foundationRemoteListResolver(store),
			RemoteSpaceResolver: foundationRemoteSpaceResolver(store),
			LocalListResolver:   foundationLocalListResolver(store, clickupProviderID),
			RemoteTaskResolver:  foundationRemoteTaskResolver(store),
		})
		cached := newCachedProvider(clickupProvider, store, logger)
		providers = append(providers, cached)
		fetchers[ProviderID(clickupProvider.ID())] = cached
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
	mutationRepository := app.NewFoundationMutationAdapter(
		spaceRepository,
		listRepository,
		taskRepository,
		store,
		nil,
		nil,
	)
	service := app.NewService(mutationRepository, providerRegistry)
	workerOptions := foundationsync.DefaultWorkerOptions()
	workerOptions.Interval = cfg.Sync.Interval
	if !cfg.Sync.RetryFailed {
		workerOptions.RetryPolicy.MaxAttempts = 1
	}
	workerOptions.EventSink = func(event foundationsync.Event) {
		level := slog.LevelDebug
		if event.Err != nil || event.Kind == foundationsync.EventSyncFailed || event.Kind == foundationsync.EventOperationFailed {
			level = slog.LevelWarn
		}
		args := []any{
			"provider_id", event.ProviderID,
			"event", event.Kind,
			"state", event.State,
			"operation_id", event.OperationID,
			"attempts", event.Attempts,
		}
		if event.Err != nil {
			args = append(args, "error", SafeErrorText(event.Err))
		}
		logContext(ctx, logger, level, "sync event", args...)
	}
	syncEvents := make(chan foundationsync.Event, 64)
	engine, err := foundationsync.NewEngine(store, providers, foundationsync.EngineOptions{
		Worker: workerOptions,
		EventSink: func(event foundationsync.Event) {
			select {
			case syncEvents <- event:
			default:
			}
		},
	})
	if err != nil {
		closeStore()
		return nil, fmt.Errorf("create foundation sync engine: %w", err)
	}
	syncController := newFoundationSyncController(engine, cfg.Sync.Enabled, fetchers, syncEvents)
	handler := newFoundationHandler(service, syncController)
	ui, err := newFoundationUIControllerWithListLoader(terminal, handler, func(ctx context.Context) (View, error) {
		return newFoundationDataStore(store).Snapshot(ctx)
	}, func(ctx context.Context, providerID ProviderID, listID ListID) (View, error) {
		return newFoundationDataStore(store).SnapshotForList(ctx, providerID, listID)
	}, syncController.Events())
	if err != nil {
		closeStore()
		return nil, err
	}
	return &foundationGraph{
		store:        newFoundationDataStore(store),
		handler:      handler,
		sync:         syncController,
		ui:           ui,
		cliStore:     store,
		cliProviders: providerRegistry,
		cliService:   service,
		cliEngine:    engine,
	}, nil
}

func foundationLocalListResolver(store *sqlite.Store, providerID domain.ProviderID) func(context.Context, string) (domain.ListID, error) {
	return func(ctx context.Context, remoteID string) (domain.ListID, error) {
		if store == nil {
			return "", errors.New("resolve local list ID: store unavailable")
		}
		if ctx == nil {
			return "", errors.New("resolve local list ID: nil context")
		}
		list, err := store.GetListByRemoteID(ctx, providerID, remoteID)
		if err != nil {
			return "", err
		}
		return list.ID, nil
	}
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

func foundationRemoteSpaceResolver(store *sqlite.Store) func(context.Context, domain.ProviderID, domain.SpaceID) (string, error) {
	return func(ctx context.Context, providerID domain.ProviderID, spaceID domain.SpaceID) (string, error) {
		if store == nil {
			return "", errors.New("resolve remote space ID: store unavailable")
		}
		if ctx == nil {
			return "", errors.New("resolve remote space ID: nil context")
		}
		space, err := store.GetSpaceByProvider(ctx, providerID, spaceID)
		if err != nil {
			return "", err
		}
		if space.RemoteID == nil || strings.TrimSpace(*space.RemoteID) == "" {
			return "", clickuppkg.ErrRemoteIDMissing
		}
		return strings.TrimSpace(*space.RemoteID), nil
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
