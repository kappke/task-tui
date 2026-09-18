package sqlite

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/repository"
)

// ProviderRepository adapts Store to the consumer-facing provider interfaces.
type ProviderRepository struct{ store *Store }

func NewProviderRepository(store *Store) *ProviderRepository {
	return &ProviderRepository{store: store}
}

func NewProviderStore(store *Store) *ProviderRepository { return NewProviderRepository(store) }

func (s *Store) Providers() *ProviderRepository { return NewProviderRepository(s) }

func (r *ProviderRepository) Get(ctx context.Context, id domain.ProviderID) (domain.Provider, error) {
	return r.store.GetProvider(ctx, id)
}

func (r *ProviderRepository) List(ctx context.Context) ([]domain.Provider, error) {
	return r.store.ListProviders(ctx)
}

func (r *ProviderRepository) ListByType(ctx context.Context, providerType domain.ProviderType) ([]domain.Provider, error) {
	return r.store.ListByType(ctx, providerType)
}

func (r *ProviderRepository) Create(ctx context.Context, provider domain.Provider) (domain.Provider, error) {
	return r.store.CreateProvider(ctx, provider)
}

func (r *ProviderRepository) Update(ctx context.Context, provider domain.Provider) (domain.Provider, error) {
	return r.store.UpdateProvider(ctx, provider)
}

func (r *ProviderRepository) Delete(ctx context.Context, id domain.ProviderID) error {
	return r.store.DeleteProvider(ctx, id)
}

// SpaceRepository adapts Store to the consumer-facing space interfaces.
type SpaceRepository struct{ store *Store }

func NewSpaceRepository(store *Store) *SpaceRepository { return &SpaceRepository{store: store} }

func NewSpaceStore(store *Store) *SpaceRepository { return NewSpaceRepository(store) }

func (s *Store) Spaces() *SpaceRepository { return NewSpaceRepository(s) }

func (r *SpaceRepository) Get(ctx context.Context, id domain.SpaceID) (domain.Space, error) {
	return r.store.GetSpace(ctx, id)
}

func (r *SpaceRepository) ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Space, error) {
	return r.store.ListSpacesByProvider(ctx, providerID)
}

func (r *SpaceRepository) GetByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.Space, error) {
	return r.store.GetSpaceByRemoteID(ctx, providerID, remoteID)
}

func (r *SpaceRepository) Create(ctx context.Context, space domain.Space) (domain.Space, error) {
	return r.store.CreateSpace(ctx, space)
}

func (r *SpaceRepository) Update(ctx context.Context, space domain.Space) (domain.Space, error) {
	return r.store.UpdateSpace(ctx, space)
}

func (r *SpaceRepository) Delete(ctx context.Context, id domain.SpaceID) error {
	return r.store.DeleteSpace(ctx, id)
}

// ListRepository adapts Store to the consumer-facing list interfaces.
type ListRepository struct{ store *Store }

func NewListRepository(store *Store) *ListRepository { return &ListRepository{store: store} }

func NewListStore(store *Store) *ListRepository { return NewListRepository(store) }

func (s *Store) Lists() *ListRepository { return NewListRepository(s) }

func (r *ListRepository) Get(ctx context.Context, id domain.ListID) (domain.List, error) {
	return r.store.GetList(ctx, id)
}

func (r *ListRepository) ListBySpace(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	return r.store.ListBySpace(ctx, spaceID)
}

func (r *ListRepository) ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.List, error) {
	return r.store.ListListsByProvider(ctx, providerID)
}

func (r *ListRepository) GetByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.List, error) {
	return r.store.GetListByRemoteID(ctx, providerID, remoteID)
}

func (r *ListRepository) Create(ctx context.Context, list domain.List) (domain.List, error) {
	return r.store.CreateList(ctx, list)
}

func (r *ListRepository) Update(ctx context.Context, list domain.List) (domain.List, error) {
	return r.store.UpdateList(ctx, list)
}

func (r *ListRepository) Delete(ctx context.Context, id domain.ListID) error {
	return r.store.DeleteList(ctx, id)
}

// TaskRepository adapts Store to the consumer-facing task interfaces.
type TaskRepository struct{ store *Store }

func NewTaskRepository(store *Store) *TaskRepository { return &TaskRepository{store: store} }

func NewTaskStore(store *Store) *TaskRepository { return NewTaskRepository(store) }

func (s *Store) Tasks() *TaskRepository { return NewTaskRepository(s) }

func (r *TaskRepository) Get(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	return r.store.GetTask(ctx, id)
}

func (r *TaskRepository) ListByList(ctx context.Context, listID domain.ListID) ([]domain.Task, error) {
	return r.store.ListByList(ctx, listID)
}

func (r *TaskRepository) ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Task, error) {
	return r.store.ListTasksByProvider(ctx, providerID)
}

func (r *TaskRepository) GetByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.Task, error) {
	return r.store.GetTaskByRemoteID(ctx, providerID, remoteID)
}

func (r *TaskRepository) Create(ctx context.Context, task domain.Task) (domain.Task, error) {
	return r.store.CreateTask(ctx, task)
}

func (r *TaskRepository) Update(ctx context.Context, task domain.Task) (domain.Task, error) {
	return r.store.UpdateTask(ctx, task)
}

func (r *TaskRepository) Delete(ctx context.Context, id domain.TaskID) error {
	return r.store.DeleteTask(ctx, id)
}

func (r *TaskRepository) Search(ctx context.Context, filter repository.TaskFilter) ([]repository.TaskView, error) {
	return r.store.Search(ctx, filter)
}

// ConflictRepository adapts Store to the conflict interfaces.
type ConflictRepository struct{ store *Store }

func NewConflictRepository(store *Store) *ConflictRepository {
	return &ConflictRepository{store: store}
}

func NewConflictStore(store *Store) *ConflictRepository { return NewConflictRepository(store) }

func (s *Store) Conflicts() *ConflictRepository { return NewConflictRepository(s) }

func (r *ConflictRepository) Get(ctx context.Context, id domain.ConflictID) (domain.Conflict, error) {
	return r.store.GetConflict(ctx, id)
}

func (r *ConflictRepository) ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Conflict, error) {
	return r.store.ListByProvider(ctx, providerID)
}

func (r *ConflictRepository) Create(ctx context.Context, conflict domain.Conflict) error {
	return r.store.CreateConflict(ctx, conflict)
}

func (r *ConflictRepository) Update(ctx context.Context, conflict domain.Conflict) error {
	return r.store.UpdateConflict(ctx, conflict)
}

func (r *ConflictRepository) Delete(ctx context.Context, id domain.ConflictID) error {
	return r.store.DeleteConflict(ctx, id)
}

// Store is intentionally used directly for these small cross-cutting
// contracts; they do not have colliding generic method names.
var (
	_ repository.ProviderStore      = (*ProviderRepository)(nil)
	_ repository.ProviderTypeReader = (*ProviderRepository)(nil)
	_ repository.SpaceStore         = (*SpaceRepository)(nil)
	_ repository.ListStore          = (*ListRepository)(nil)
	_ repository.TaskStore          = (*TaskRepository)(nil)
	_ repository.TaskSearcher       = (*TaskRepository)(nil)
	_ repository.TaskSearcher       = (*Store)(nil)
	_ repository.HierarchyReader    = (*Store)(nil)
	_ repository.SyncQueue          = (*Store)(nil)
	_ repository.ScheduledSyncQueue = (*Store)(nil)
	_ repository.TaskMutationStore  = (*Store)(nil)
	_ repository.AppStateStore      = (*Store)(nil)
	_ repository.ConflictStore      = (*ConflictRepository)(nil)
	_ repository.ConflictResolver   = (*Store)(nil)
	_ repository.SyncBaseStore      = (*Store)(nil)
)
