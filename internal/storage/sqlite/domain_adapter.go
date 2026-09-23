package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/repository"
)

const defaultLease = 5 * time.Minute

func adaptError(err error, duplicate bool) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if kind, ok := constraintKind(err); ok {
		switch kind {
		case constraintUnique:
			return fmt.Errorf("%w: %v", domain.ErrAlreadyExists, err)
		case constraintForeignKey:
			return fmt.Errorf("%w: %v", ErrForeignKeyConstraint, err)
		case constraintCheck:
			return fmt.Errorf("%w: %v", ErrCheckConstraint, err)
		case constraintImmutableProvider:
			return fmt.Errorf("%w: %w: %v", ErrImmutableProvider, domain.ErrProviderMismatch, err)
		}
	}
	return err
}

func ensureID(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	return newID()
}

func (s *Store) CreateProvider(ctx context.Context, provider domain.Provider) (domain.Provider, error) {
	record, err := providerRecord(provider)
	if err != nil {
		return domain.Provider{}, err
	}
	created, err := s.createProvider(ctx, record)
	if err != nil {
		return domain.Provider{}, adaptError(err, true)
	}
	return domainProvider(created), nil
}

func (s *Store) GetProvider(ctx context.Context, id domain.ProviderID) (domain.Provider, error) {
	provider, err := s.getProvider(ctx, id.String())
	if err != nil {
		return domain.Provider{}, adaptError(err, false)
	}
	return domainProvider(provider), nil
}

func (s *Store) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	providers, err := s.listProviders(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Provider, 0, len(providers))
	for _, provider := range providers {
		result = append(result, domainProvider(provider))
	}
	return result, nil
}

func (s *Store) ListByType(ctx context.Context, providerType domain.ProviderType) ([]domain.Provider, error) {
	if err := providerType.Validate(); err != nil {
		return nil, err
	}
	providers, err := s.listProviders(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Provider, 0)
	for _, provider := range providers {
		if provider.Type == string(providerType) {
			result = append(result, domainProvider(provider))
		}
	}
	return result, nil
}

func (s *Store) UpdateProvider(ctx context.Context, provider domain.Provider) (domain.Provider, error) {
	record, err := providerRecord(provider)
	if err != nil {
		return domain.Provider{}, err
	}
	updated, err := s.updateProvider(ctx, record)
	if err != nil {
		return domain.Provider{}, adaptError(err, false)
	}
	return domainProvider(updated), nil
}

func (s *Store) UpsertProvider(ctx context.Context, provider domain.Provider) (domain.Provider, error) {
	if existing, err := s.getProvider(ctx, provider.ID.String()); err == nil {
		if provider.SyncState.IsZero() {
			provider.SyncState = domain.SyncState(existing.SyncState)
		}
		if provider.SyncCursor == nil {
			provider.SyncCursor = copyStringPointer(existing.SyncCursor)
		}
		if provider.SyncError == "" {
			provider.SyncError = stringOrEmpty(existing.SyncError)
		}
		if provider.LastSyncAt == nil {
			provider.LastSyncAt = cloneTime(existing.LastSyncAt)
		}
	} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, sql.ErrNoRows) {
		return domain.Provider{}, adaptError(err, false)
	}
	record, err := providerRecord(provider)
	if err != nil {
		return domain.Provider{}, err
	}
	if existing, lookupErr := s.getProvider(ctx, record.ID); lookupErr == nil {
		if existing.Type != record.Type || string(existing.Configuration) != string(record.Configuration) {
			return domain.Provider{}, domain.ErrAlreadyExists
		}
	}
	upserted, err := s.upsertProvider(ctx, record)
	if err != nil {
		return domain.Provider{}, adaptError(err, false)
	}
	return domainProvider(upserted), nil
}

func (s *Store) DeleteProvider(ctx context.Context, id domain.ProviderID) error {
	return adaptError(s.deleteProvider(ctx, id.String()), false)
}

func providerRecord(provider domain.Provider) (Provider, error) {
	if provider.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return Provider{}, fmt.Errorf("sqlite: generate provider id: %w", err)
		}
		provider.ID = domain.ProviderID(id)
	}
	provider = provider.NormalizeUTC()
	if err := provider.Validate(); err != nil {
		return Provider{}, err
	}
	return Provider{
		ID:            provider.ID.String(),
		Type:          provider.Type.String(),
		Name:          provider.Name,
		Enabled:       provider.Enabled,
		Configuration: append([]byte(nil), provider.Configuration...),
		SyncState:     SyncState(provider.SyncState),
		SyncCursor:    copyStringPointer(provider.SyncCursor),
		SyncError:     stringPointer(provider.SyncError),
		LastSyncAt:    provider.LastSyncAt,
		CreatedAt:     provider.CreatedAt,
		UpdatedAt:     provider.UpdatedAt,
	}, nil
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func domainProvider(provider Provider) domain.Provider {
	return domain.Provider{
		ID:            domain.ProviderID(provider.ID),
		Type:          domain.ProviderType(provider.Type),
		Name:          provider.Name,
		Enabled:       provider.Enabled,
		Configuration: append(json.RawMessage(nil), provider.Configuration...),
		SyncState:     domain.SyncState(provider.SyncState),
		SyncCursor:    copyStringPointer(provider.SyncCursor),
		SyncError:     stringOrEmpty(provider.SyncError),
		LastSyncAt:    provider.LastSyncAt,
		CreatedAt:     provider.CreatedAt,
		UpdatedAt:     provider.UpdatedAt,
	}
}

func (s *Store) CreateSpace(ctx context.Context, space domain.Space) (domain.Space, error) {
	record, err := spaceRecord(space)
	if err != nil {
		return domain.Space{}, err
	}
	if err := s.validateProvider(ctx, record.ProviderID); err != nil {
		return domain.Space{}, err
	}
	created, err := s.createSpace(ctx, record)
	if err != nil {
		return domain.Space{}, adaptError(err, true)
	}
	return domainSpace(created), nil
}

func (s *Store) GetSpace(ctx context.Context, id domain.SpaceID) (domain.Space, error) {
	space, err := s.getSpaceByID(ctx, id.String())
	if err != nil {
		return domain.Space{}, adaptError(err, false)
	}
	return domainSpace(space), nil
}

func (s *Store) GetSpaceByProvider(ctx context.Context, providerID domain.ProviderID, id domain.SpaceID) (domain.Space, error) {
	space, err := s.getSpaceByProvider(ctx, providerID.String(), id.String())
	if err != nil {
		return domain.Space{}, adaptError(err, false)
	}
	return domainSpace(space), nil
}

func (s *Store) ListSpacesByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Space, error) {
	spaces, err := s.listSpacesByProvider(ctx, providerID.String())
	if err != nil {
		return nil, err
	}
	result := make([]domain.Space, 0, len(spaces))
	for _, space := range spaces {
		result = append(result, domainSpace(space))
	}
	return result, nil
}

func (s *Store) ListSpaces(ctx context.Context, providerID domain.ProviderID) ([]domain.Space, error) {
	return s.ListSpacesByProvider(ctx, providerID)
}

func (s *Store) GetByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.Space, error) {
	space, err := s.getSpaceByRemoteID(ctx, providerID.String(), remoteID)
	if err != nil {
		return domain.Space{}, adaptError(err, false)
	}
	return domainSpace(space), nil
}

func (s *Store) GetSpaceByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.Space, error) {
	return s.GetByRemoteID(ctx, providerID, remoteID)
}

func (s *Store) UpdateSpace(ctx context.Context, space domain.Space) (domain.Space, error) {
	record, err := s.spaceRecordForUpdate(ctx, space)
	if err != nil {
		return domain.Space{}, err
	}
	updated, err := s.updateSpace(ctx, record)
	if err != nil {
		return domain.Space{}, adaptError(err, false)
	}
	return domainSpace(updated), nil
}

func (s *Store) UpsertSpace(ctx context.Context, space domain.Space) (domain.Space, error) {
	if space.ID.IsZero() && space.RemoteID != nil {
		if existing, err := s.getSpaceByRemoteID(ctx, space.ProviderID.String(), *space.RemoteID); err == nil {
			space.ID = domain.SpaceID(existing.ID)
		} else if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrNotFound) {
			return domain.Space{}, adaptError(err, false)
		}
	}
	record, err := spaceRecord(space)
	if err != nil {
		return domain.Space{}, err
	}
	if err := s.validateProvider(ctx, record.ProviderID); err != nil {
		return domain.Space{}, err
	}
	upserted, err := s.upsertSpace(ctx, record)
	if err != nil {
		return domain.Space{}, adaptError(err, false)
	}
	return domainSpace(upserted), nil
}

func (s *Store) DeleteSpace(ctx context.Context, id domain.SpaceID) error {
	return s.tombstoneByID(ctx, "spaces", id.String())
}

func spaceRecord(space domain.Space) (Space, error) {
	if space.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return Space{}, fmt.Errorf("sqlite: generate space id: %w", err)
		}
		space.ID = domain.SpaceID(id)
	}
	if space.SyncState.IsZero() {
		space.SyncState = domain.SyncStateLocal
	}
	space = space.NormalizeUTC()
	if err := space.Validate(); err != nil {
		return Space{}, err
	}
	return Space{
		ID:              space.ID.String(),
		ProviderID:      space.ProviderID.String(),
		RemoteID:        copyStringPointer(space.RemoteID),
		Name:            space.Name,
		SyncState:       SyncState(space.SyncState),
		RemoteUpdatedAt: space.RemoteUpdatedAt,
		CreatedAt:       space.CreatedAt,
		UpdatedAt:       space.UpdatedAt,
	}, nil
}

func (s *Store) spaceRecordForUpdate(ctx context.Context, space domain.Space) (Space, error) {
	record, err := spaceRecord(space)
	if err != nil {
		return Space{}, err
	}
	if err := s.validateProvider(ctx, record.ProviderID); err != nil {
		return Space{}, err
	}
	existing, err := s.getSpaceByID(ctx, record.ID)
	if err != nil {
		return Space{}, adaptError(err, false)
	}
	if existing.ProviderID != record.ProviderID {
		return Space{}, fmt.Errorf("%w: space provider is immutable", domain.ErrProviderMismatch)
	}
	record.IsDeleted = existing.IsDeleted
	record.DeletedAt = existing.DeletedAt
	return record, nil
}

func domainSpace(space Space) domain.Space {
	return domain.Space{
		ID:              domain.SpaceID(space.ID),
		ProviderID:      domain.ProviderID(space.ProviderID),
		RemoteID:        copyStringPointer(space.RemoteID),
		Name:            space.Name,
		SyncState:       domain.SyncState(space.SyncState),
		RemoteUpdatedAt: space.RemoteUpdatedAt,
		IsDeleted:       space.IsDeleted,
		DeletedAt:       space.DeletedAt,
		CreatedAt:       space.CreatedAt,
		UpdatedAt:       space.UpdatedAt,
	}
}

func (s *Store) CreateList(ctx context.Context, list domain.List) (domain.List, error) {
	record, err := listRecord(list)
	if err != nil {
		return domain.List{}, err
	}
	if err := s.validateListParent(ctx, record); err != nil {
		return domain.List{}, err
	}
	created, err := s.createList(ctx, record)
	if err != nil {
		return domain.List{}, adaptError(err, true)
	}
	return domainList(created), nil
}

func (s *Store) GetList(ctx context.Context, id domain.ListID) (domain.List, error) {
	list, err := s.getListByID(ctx, id.String())
	if err != nil {
		return domain.List{}, adaptError(err, false)
	}
	return domainList(list), nil
}

func (s *Store) GetListByProvider(ctx context.Context, providerID domain.ProviderID, id domain.ListID) (domain.List, error) {
	list, err := s.getListByProvider(ctx, providerID.String(), id.String())
	if err != nil {
		return domain.List{}, adaptError(err, false)
	}
	return domainList(list), nil
}

func (s *Store) ListBySpace(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	lists, err := s.listListsBySpaceID(ctx, spaceID.String())
	if err != nil {
		return nil, err
	}
	result := make([]domain.List, 0, len(lists))
	for _, list := range lists {
		result = append(result, domainList(list))
	}
	return result, nil
}

func (s *Store) ListListsByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.List, error) {
	lists, err := s.listAllLists(ctx, providerID.String())
	if err != nil {
		return nil, err
	}
	result := make([]domain.List, 0, len(lists))
	for _, list := range lists {
		result = append(result, domainList(list))
	}
	return result, nil
}

func (s *Store) ListLists(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	return s.ListBySpace(ctx, spaceID)
}

func (s *Store) GetListByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.List, error) {
	list, err := s.getListByRemoteID(ctx, providerID.String(), remoteID)
	if err != nil {
		return domain.List{}, adaptError(err, false)
	}
	return domainList(list), nil
}

func (s *Store) UpdateList(ctx context.Context, list domain.List) (domain.List, error) {
	record, err := s.listRecordForUpdate(ctx, list)
	if err != nil {
		return domain.List{}, err
	}
	updated, err := s.updateList(ctx, record)
	if err != nil {
		return domain.List{}, adaptError(err, false)
	}
	return domainList(updated), nil
}

func (s *Store) UpsertList(ctx context.Context, list domain.List) (domain.List, error) {
	if list.ID.IsZero() && list.RemoteID != nil {
		if existing, err := s.getListByRemoteID(ctx, list.ProviderID.String(), *list.RemoteID); err == nil {
			list.ID = domain.ListID(existing.ID)
		} else if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrNotFound) {
			return domain.List{}, adaptError(err, false)
		}
	}
	record, err := listRecord(list)
	if err != nil {
		return domain.List{}, err
	}
	if err := s.validateListParent(ctx, record); err != nil {
		return domain.List{}, err
	}
	upserted, err := s.upsertList(ctx, record)
	if err != nil {
		return domain.List{}, adaptError(err, false)
	}
	return domainList(upserted), nil
}

func (s *Store) DeleteList(ctx context.Context, id domain.ListID) error {
	return s.tombstoneByID(ctx, "lists", id.String())
}

func listRecord(list domain.List) (List, error) {
	if list.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return List{}, fmt.Errorf("sqlite: generate list id: %w", err)
		}
		list.ID = domain.ListID(id)
	}
	if list.SyncState.IsZero() {
		list.SyncState = domain.SyncStateLocal
	}
	list = list.NormalizeUTC()
	if err := list.Validate(); err != nil {
		return List{}, err
	}
	return List{
		ID:              list.ID.String(),
		ProviderID:      list.ProviderID.String(),
		SpaceID:         list.SpaceID.String(),
		RemoteID:        copyStringPointer(list.RemoteID),
		Name:            list.Name,
		SyncState:       SyncState(list.SyncState),
		RemoteUpdatedAt: list.RemoteUpdatedAt,
		CreatedAt:       list.CreatedAt,
		UpdatedAt:       list.UpdatedAt,
	}, nil
}

func (s *Store) listRecordForUpdate(ctx context.Context, list domain.List) (List, error) {
	record, err := listRecord(list)
	if err != nil {
		return List{}, err
	}
	if err := s.validateListParent(ctx, record); err != nil {
		return List{}, err
	}
	existing, err := s.getListByID(ctx, record.ID)
	if err != nil {
		return List{}, adaptError(err, false)
	}
	if existing.ProviderID != record.ProviderID {
		return List{}, fmt.Errorf("%w: list provider is immutable", domain.ErrProviderMismatch)
	}
	record.IsDeleted = existing.IsDeleted
	record.DeletedAt = existing.DeletedAt
	return record, nil
}

func domainList(list List) domain.List {
	return domain.List{
		ID:              domain.ListID(list.ID),
		ProviderID:      domain.ProviderID(list.ProviderID),
		SpaceID:         domain.SpaceID(list.SpaceID),
		RemoteID:        copyStringPointer(list.RemoteID),
		Name:            list.Name,
		SyncState:       domain.SyncState(list.SyncState),
		RemoteUpdatedAt: list.RemoteUpdatedAt,
		IsDeleted:       list.IsDeleted,
		DeletedAt:       list.DeletedAt,
		CreatedAt:       list.CreatedAt,
		UpdatedAt:       list.UpdatedAt,
	}
}

func (s *Store) CreateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	record, err := taskRecord(task)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.validateTaskParents(ctx, record); err != nil {
		return domain.Task{}, err
	}
	created, err := s.createTask(ctx, record)
	if err != nil {
		return domain.Task{}, adaptError(err, true)
	}
	return domainTask(created), nil
}

func (s *Store) GetTask(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	task, err := s.getTaskByID(ctx, id.String())
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	return domainTask(task), nil
}

func (s *Store) GetTaskByProvider(ctx context.Context, providerID domain.ProviderID, id domain.TaskID) (domain.Task, error) {
	task, err := s.getTaskByProvider(ctx, providerID.String(), id.String())
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	return domainTask(task), nil
}

func (s *Store) ListByList(ctx context.Context, listID domain.ListID) ([]domain.Task, error) {
	tasks, err := s.listTasksByListID(ctx, listID.String())
	if err != nil {
		return nil, err
	}
	result := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, domainTask(task))
	}
	return result, nil
}

func (s *Store) ListTasksByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Task, error) {
	tasks, err := s.listAllTasks(ctx, providerID.String())
	if err != nil {
		return nil, err
	}
	result := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, domainTask(task))
	}
	return result, nil
}

// ListTasksByListPage returns a local task page for one provider-owned list. A
// non-positive limit returns all matching tasks.
func (s *Store) ListTasksByListPage(ctx context.Context, providerID domain.ProviderID, listID domain.ListID, limit, offset int) ([]domain.Task, error) {
	tasks, err := s.listTasksPage(ctx, providerID.String(), listID.String(), limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, domainTask(task))
	}
	return result, nil
}

// ListTasksByProviderPage returns a local task page for one provider. A
// non-positive limit returns all matching tasks.
func (s *Store) ListTasksByProviderPage(ctx context.Context, providerID domain.ProviderID, limit, offset int) ([]domain.Task, error) {
	tasks, err := s.listAllTasksPage(ctx, providerID.String(), limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, domainTask(task))
	}
	return result, nil
}

func (s *Store) ListTasks(ctx context.Context, listID domain.ListID) ([]domain.Task, error) {
	return s.ListByList(ctx, listID)
}

func (s *Store) GetTaskByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.Task, error) {
	task, err := s.getTaskByRemoteID(ctx, providerID.String(), remoteID)
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	return domainTask(task), nil
}

func (s *Store) UpdateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	record, err := s.taskRecordForUpdate(ctx, task)
	if err != nil {
		return domain.Task{}, err
	}
	updated, err := s.updateTask(ctx, record)
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	return domainTask(updated), nil
}

func (s *Store) UpsertTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if task.ID.IsZero() && task.RemoteID != nil {
		if existing, err := s.getTaskByRemoteID(ctx, task.ProviderID.String(), *task.RemoteID); err == nil {
			task.ID = domain.TaskID(existing.ID)
		} else if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrNotFound) {
			return domain.Task{}, adaptError(err, false)
		}
	}
	record, err := taskRecord(task)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.validateTaskParents(ctx, record); err != nil {
		return domain.Task{}, err
	}
	upserted, err := s.upsertTask(ctx, record)
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	task.ID = domain.TaskID(upserted.ID)
	return domainTask(upserted), nil
}

func (s *Store) DeleteTask(ctx context.Context, id domain.TaskID) error {
	return s.tombstoneByID(ctx, "tasks", id.String())
}

func taskRecord(task domain.Task) (Task, error) {
	if task.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return Task{}, fmt.Errorf("sqlite: generate task id: %w", err)
		}
		task.ID = domain.TaskID(id)
	}
	if task.SyncState.IsZero() {
		task.SyncState = domain.SyncStateLocal
	}
	if task.Priority.IsZero() {
		task.Priority = domain.PriorityNormal
	}
	if task.Status == "" {
		task.Status = "todo"
	}
	var err error
	task, err = task.NormalizeListMemberships()
	if err != nil {
		return Task{}, err
	}
	task = task.NormalizeUTC()
	if err := task.Validate(); err != nil {
		return Task{}, err
	}
	var parentTaskID *string
	if task.ParentTaskID != nil {
		value := task.ParentTaskID.String()
		parentTaskID = &value
	}
	return Task{
		ID:              task.ID.String(),
		ProviderID:      task.ProviderID.String(),
		ListID:          task.ListID.String(),
		ListIDs:         listIDsToStrings(task.ListIDs),
		RemoteID:        copyStringPointer(task.RemoteID),
		ParentTaskID:    parentTaskID,
		Assignee:        task.Assignee,
		Title:           task.Title,
		Description:     task.Description,
		Status:          task.Status,
		Priority:        task.Priority.String(),
		TimeEstimate:    copyDurationPointer(task.TimeEstimate),
		TimeTracked:     copyDurationPointer(task.TimeTracked),
		DueAt:           task.DueAt,
		CompletedAt:     task.CompletedAt,
		SyncState:       SyncState(task.SyncState),
		RemoteUpdatedAt: task.RemoteUpdatedAt,
		CreatedAt:       task.CreatedAt,
		UpdatedAt:       task.UpdatedAt,
	}, nil
}

func (s *Store) taskRecordForUpdate(ctx context.Context, task domain.Task) (Task, error) {
	existing, err := s.getTaskByID(ctx, task.ID.String())
	if err != nil {
		return Task{}, adaptError(err, false)
	}
	if existing.ProviderID != task.ProviderID.String() {
		return Task{}, fmt.Errorf("%w: task provider is immutable", domain.ErrProviderMismatch)
	}
	if len(task.ListIDs) == 0 {
		task.ListIDs = stringListIDsToDomain(existing.ListIDs)
	}
	record, err := taskRecord(task)
	if err != nil {
		return Task{}, err
	}
	if err := s.validateTaskParents(ctx, record); err != nil {
		return Task{}, err
	}
	if existing.IsDeleted {
		record.IsDeleted = true
		record.DeletedAt = existing.DeletedAt
	}
	return record, nil
}

func domainTask(task Task) domain.Task {
	var parentTaskID *domain.TaskID
	if task.ParentTaskID != nil {
		value := domain.TaskID(*task.ParentTaskID)
		parentTaskID = &value
	}
	return domain.Task{
		ID:              domain.TaskID(task.ID),
		ProviderID:      domain.ProviderID(task.ProviderID),
		ListID:          domain.ListID(task.ListID),
		ListIDs:         stringListIDsToDomain(task.ListIDs),
		RemoteID:        copyStringPointer(task.RemoteID),
		ParentTaskID:    parentTaskID,
		Assignee:        task.Assignee,
		Title:           task.Title,
		Description:     task.Description,
		Status:          task.Status,
		Priority:        domain.Priority(task.Priority),
		TimeEstimate:    copyDurationPointer(task.TimeEstimate),
		TimeTracked:     copyDurationPointer(task.TimeTracked),
		DueAt:           task.DueAt,
		CompletedAt:     task.CompletedAt,
		SyncState:       domain.SyncState(task.SyncState),
		RemoteUpdatedAt: task.RemoteUpdatedAt,
		IsDeleted:       task.IsDeleted,
		DeletedAt:       task.DeletedAt,
		CreatedAt:       task.CreatedAt,
		UpdatedAt:       task.UpdatedAt,
	}
}

func copyStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyDurationPointer(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Store) Search(ctx context.Context, filter repository.TaskFilter) ([]repository.TaskView, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	filter = filter.NormalizeUTC()
	spaceID, listID := "", ""
	if filter.SpaceID != nil {
		spaceID = filter.SpaceID.String()
	}
	if filter.ListID != nil {
		listID = filter.ListID.String()
	}
	rawFilter := TaskFilter{
		Query:     filter.Query,
		SpaceID:   spaceID,
		ListID:    listID,
		Status:    filter.Status,
		Statuses:  append([]string(nil), filter.Statuses...),
		DueBefore: filter.DueBefore,
		DueAfter:  filter.DueAfter,
		Completed: filter.Completed,
	}
	if filter.ProviderID != nil {
		rawFilter.ProviderID = filter.ProviderID.String()
	}
	for _, providerID := range filter.ProviderIDs {
		rawFilter.ProviderIDs = append(rawFilter.ProviderIDs, providerID.String())
	}
	if filter.Priority != nil {
		rawFilter.Priority = filter.Priority.String()
	}
	if filter.MinPriority != nil {
		rank := filter.MinPriority.Rank()
		rawFilter.MinPriority = &rank
	}
	if filter.MaxPriority != nil {
		rank := filter.MaxPriority.Rank()
		rawFilter.MaxPriority = &rank
	}
	if filter.SyncState != nil {
		rawFilter.SyncState = SyncState(filter.SyncState.String())
	}
	results, err := s.searchTaskRecords(ctx, TaskSearch{
		Query:  filter.Query,
		Filter: rawFilter,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	})
	if err != nil {
		return nil, err
	}
	views := make([]repository.TaskView, 0, len(results))
	for _, result := range results {
		views = append(views, repository.TaskView{
			Task:         domainTask(result.Task),
			ProviderID:   domain.ProviderID(result.Provider.ID),
			ProviderName: result.Provider.Name,
			SpaceID:      domain.SpaceID(result.Space.ID),
			SpaceName:    result.Space.Name,
			ListID:       domain.ListID(result.List.ID),
			ListName:     result.List.Name,
		})
	}
	return views, nil
}

func (s *Store) Filter(ctx context.Context, filter repository.TaskFilter) ([]domain.Task, error) {
	views, err := s.Search(ctx, filter)
	if err != nil {
		return nil, err
	}
	tasks := make([]domain.Task, 0, len(views))
	for _, view := range views {
		tasks = append(tasks, view.Task)
	}
	return tasks, nil
}

func (s *Store) SearchTasks(ctx context.Context, filter repository.TaskFilter) ([]repository.TaskView, error) {
	return s.Search(ctx, filter)
}

func (s *Store) FilterTasks(ctx context.Context, filter repository.TaskFilter) ([]domain.Task, error) {
	return s.Filter(ctx, filter)
}

func (s *Store) GetSpaceForList(ctx context.Context, listID domain.ListID) (domain.Space, error) {
	list, err := s.getListByID(ctx, listID.String())
	if err != nil {
		return domain.Space{}, adaptError(err, false)
	}
	space, err := s.getSpaceByProvider(ctx, list.ProviderID, list.SpaceID)
	if err != nil {
		return domain.Space{}, adaptError(err, false)
	}
	return domainSpace(space), nil
}

func (s *Store) GetListForTask(ctx context.Context, taskID domain.TaskID) (domain.List, error) {
	task, err := s.getTaskByID(ctx, taskID.String())
	if err != nil {
		return domain.List{}, adaptError(err, false)
	}
	list, err := s.getListByProvider(ctx, task.ProviderID, task.ListID)
	if err != nil {
		return domain.List{}, adaptError(err, false)
	}
	return domainList(list), nil
}

func (s *Store) GetParentTask(ctx context.Context, taskID domain.TaskID) (domain.Task, error) {
	task, err := s.getTaskByID(ctx, taskID.String())
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	if task.ParentTaskID == nil {
		return domain.Task{}, domain.ErrNotFound
	}
	parent, err := s.getTaskByProvider(ctx, task.ProviderID, *task.ParentTaskID)
	if err != nil {
		return domain.Task{}, adaptError(err, false)
	}
	return domainTask(parent), nil
}

func (s *Store) validateProvider(ctx context.Context, providerID string) error {
	if _, err := s.getProvider(ctx, providerID); err != nil {
		return adaptError(err, false)
	}
	return nil
}

func (s *Store) validateListParent(ctx context.Context, list List) error {
	space, err := s.getSpaceByID(ctx, list.SpaceID)
	if err != nil {
		return adaptError(err, false)
	}
	if space.ProviderID != list.ProviderID {
		return fmt.Errorf("%w: list %s and space %s", domain.ErrProviderMismatch, list.ID, list.SpaceID)
	}
	return nil
}

func (s *Store) validateTaskParents(ctx context.Context, task Task) error {
	for _, listID := range task.ListIDs {
		list, err := s.getListByID(ctx, listID)
		if err != nil {
			return adaptError(err, false)
		}
		if list.ProviderID != task.ProviderID {
			return fmt.Errorf("%w: task %s and list %s", domain.ErrProviderMismatch, task.ID, listID)
		}
	}
	if task.ParentTaskID == nil {
		return nil
	}
	if *task.ParentTaskID == task.ID {
		return domain.ErrInvalidParent
	}
	parent, err := s.getTaskByID(ctx, *task.ParentTaskID)
	if err != nil {
		return adaptError(err, false)
	}
	if parent.ProviderID != task.ProviderID {
		return fmt.Errorf("%w: task %s and parent %s", domain.ErrProviderMismatch, task.ID, *task.ParentTaskID)
	}
	return nil
}

func listIDsToStrings(values []domain.ListID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func stringListIDsToDomain(values []string) []domain.ListID {
	result := make([]domain.ListID, len(values))
	for index, value := range values {
		result[index] = domain.ListID(value)
	}
	return result
}

func (s *Store) tombstoneByID(ctx context.Context, table, id string) error {
	if id == "" {
		return domain.ErrNotFound
	}
	now := time.Now().UTC()
	query := fmt.Sprintf("UPDATE %s SET is_deleted = 1, deleted_at = ?, sync_state = ?, updated_at = ? WHERE id = ?", table)
	result, err := s.db.ExecContext(ctx, query, formatTime(now), SyncStateSynced, formatTime(now), id)
	if err != nil {
		return adaptError(err, false)
	}
	return adaptError(requireAffected(result, "tombstone entity", id), false)
}
