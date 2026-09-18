package local

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/provider"
	"github.com/kappke/task-tui/internal/repository"
)

const (
	ProviderType = "local"
	Type         = ProviderType

	DefaultProviderID = domain.ProviderID(ProviderType)
)

var (
	// ErrProviderMismatch is returned when an operation would cross a provider
	// boundary. It aliases the domain sentinel so callers can inspect either
	// package's error category.
	ErrProviderMismatch = domain.ErrProviderMismatch

	ErrRepositoryUnavailable = errors.New("local repository unavailable")
)

// Config controls the identity of a local provider instance.
//
// ProviderID is intentionally independent from Type. Multiple local provider
// instances can therefore have independent provider-owned hierarchies.
type Config struct {
	ProviderID domain.ProviderID
	ID         domain.ProviderID
}

// ProviderConfig is a descriptive alias used by provider registration code.
type ProviderConfig = Config

// Repositories contains the small persistence contracts used by Local. The
// reader and writer fields are separate so applications can compose different
// implementations; NewWithStores is convenient when one store implements both
// sides of each entity contract.
type Repositories struct {
	Spaces      repository.SpaceReader
	SpaceWriter repository.SpaceWriter
	Lists       repository.ListReader
	ListWriter  repository.ListWriter
	Tasks       repository.TaskReader
	TaskWriter  repository.TaskWriter
}

// Local is the offline-only provider implementation.
type Local struct {
	providerID domain.ProviderID
	spaces     repository.SpaceReader
	spaceWrite repository.SpaceWriter
	lists      repository.ListReader
	listWrite  repository.ListWriter
	tasks      repository.TaskReader
	taskWrite  repository.TaskWriter
	sequence   atomic.Uint64
}

// Provider is an alias for callers that prefer the implementation name to
// match the provider package's other adapters.
type Provider = Local

var (
	_ provider.Provider      = (*Local)(nil)
	_ provider.SpaceWriter   = (*Local)(nil)
	_ provider.ListWriter    = (*Local)(nil)
	_ provider.Synchronizer  = (*Local)(nil)
	_ provider.Puller        = (*Local)(nil)
	_ provider.Authenticator = (*Local)(nil)
)

// New constructs a local provider using injected repository contracts. It
// accepts a Repositories value, three SpaceStore/ListStore/TaskStore values,
// or a Config value together with either repository form. An omitted provider
// ID defaults to "local".
func New(args ...any) *Local {
	config, repos := parseArguments(args)
	return NewWithConfig(config, repos)
}

// NewWithConfig constructs a local provider with an explicit stable identity.
func NewWithConfig(config Config, repos Repositories) *Local {
	providerID := config.ProviderID
	if providerID.IsZero() {
		providerID = config.ID
	}
	if providerID.IsZero() {
		providerID = DefaultProviderID
	}
	if repos.SpaceWriter == nil {
		if writer, ok := repos.Spaces.(repository.SpaceWriter); ok {
			repos.SpaceWriter = writer
		}
	}
	if repos.ListWriter == nil {
		if writer, ok := repos.Lists.(repository.ListWriter); ok {
			repos.ListWriter = writer
		}
	}
	if repos.TaskWriter == nil {
		if writer, ok := repos.Tasks.(repository.TaskWriter); ok {
			repos.TaskWriter = writer
		}
	}

	return &Local{
		providerID: providerID,
		spaces:     repos.Spaces,
		spaceWrite: repos.SpaceWriter,
		lists:      repos.Lists,
		listWrite:  repos.ListWriter,
		tasks:      repos.Tasks,
		taskWrite:  repos.TaskWriter,
	}
}

// NewWithStores constructs a local provider when one store implements both
// reader and writer interfaces for each hierarchy entity.
func NewWithStores(spaces repository.SpaceStore, lists repository.ListStore, tasks repository.TaskStore) *Local {
	return NewWithConfig(Config{}, Repositories{
		Spaces:      spaces,
		SpaceWriter: spaces,
		Lists:       lists,
		ListWriter:  lists,
		Tasks:       tasks,
		TaskWriter:  tasks,
	})
}

// NewWithProviderID constructs a local provider with a stable custom identity
// and composed stores.
func NewWithProviderID(id domain.ProviderID, spaces repository.SpaceStore, lists repository.ListStore, tasks repository.TaskStore) *Local {
	return NewWithConfig(Config{ProviderID: id}, Repositories{
		Spaces:      spaces,
		SpaceWriter: spaces,
		Lists:       lists,
		ListWriter:  lists,
		Tasks:       tasks,
		TaskWriter:  tasks,
	})
}

func parseArguments(args []any) (Config, Repositories) {
	var (
		config Config
		repos  Repositories
		stores []any
	)

	for _, arg := range args {
		switch value := arg.(type) {
		case Config:
			config = value
		case *Config:
			if value != nil {
				config = *value
			}
		case domain.ProviderID:
			config.ProviderID = value
		case string:
			config.ProviderID = domain.ProviderID(value)
		case Repositories:
			repos = value
		case *Repositories:
			if value != nil {
				repos = *value
			}
		case repository.SpaceStore:
			repos.Spaces = value
			repos.SpaceWriter = value
			stores = append(stores, value)
		case repository.ListStore:
			repos.Lists = value
			repos.ListWriter = value
			stores = append(stores, value)
		case repository.TaskStore:
			repos.Tasks = value
			repos.TaskWriter = value
			stores = append(stores, value)
		case repository.SpaceReader:
			repos.Spaces = value
		case repository.SpaceWriter:
			repos.SpaceWriter = value
		case repository.ListReader:
			repos.Lists = value
		case repository.ListWriter:
			repos.ListWriter = value
		case repository.TaskReader:
			repos.Tasks = value
		case repository.TaskWriter:
			repos.TaskWriter = value
		default:
			continue
		}
	}

	if len(stores) == 3 {
		return config, Repositories{
			Spaces:      stores[0].(repository.SpaceStore),
			SpaceWriter: stores[0].(repository.SpaceStore),
			Lists:       stores[1].(repository.ListStore),
			ListWriter:  stores[1].(repository.ListStore),
			Tasks:       stores[2].(repository.TaskStore),
			TaskWriter:  stores[2].(repository.TaskStore),
		}
	}

	return config, repos
}

// ID returns the provider instance identity.
func (p *Local) ID() domain.ProviderID {
	return p.providerID
}

// Type returns the local provider type.
func (p *Local) Type() domain.ProviderType {
	return Type
}

// Capabilities describes all local hierarchy operations supported by the MVP.
func (p *Local) Capabilities() domain.Capabilities {
	return domain.Capabilities{
		FetchSpaces: true,
		FetchLists:  true,
		FetchTasks:  true,
		FetchTask:   true,

		CreateTask:  true,
		UpdateTask:  true,
		DeleteTask:  true,
		CreateList:  true,
		UpdateList:  true,
		DeleteList:  true,
		CreateSpace: true,
		UpdateSpace: true,
		DeleteSpace: true,

		DueDates: true,
		Subtasks: true,
	}
}

// FetchSpaces reads this provider's spaces from local persistence.
func (p *Local) FetchSpaces(ctx context.Context) ([]domain.Space, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if p.spaces == nil {
		return nil, ErrRepositoryUnavailable
	}

	spaces, err := p.spaces.ListByProvider(ctx, p.ID())
	if err != nil {
		return nil, fmt.Errorf("list local spaces: %w", err)
	}
	result := make([]domain.Space, 0, len(spaces))
	for _, space := range spaces {
		space, err = p.ownedSpace(space)
		if err != nil {
			return nil, err
		}
		result = append(result, localSpace(space))
	}
	return result, nil
}

// FetchLists reads lists beneath an owned space from local persistence.
func (p *Local) FetchLists(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if _, err := p.getOwnedSpace(ctx, spaceID); err != nil {
		return nil, fmt.Errorf("check local space %s: %w", spaceID, err)
	}
	if p.lists == nil {
		return nil, ErrRepositoryUnavailable
	}

	lists, err := p.lists.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("list local lists for space %s: %w", spaceID, err)
	}
	result := make([]domain.List, 0, len(lists))
	for _, list := range lists {
		if list.SpaceID != spaceID {
			return nil, fmt.Errorf("list %s has unexpected parent space %s: %w", list.ID, list.SpaceID, domain.ErrInvalidParent)
		}
		list, err = p.ownedList(list)
		if err != nil {
			return nil, err
		}
		result = append(result, localList(list))
	}
	return result, nil
}

// FetchTasks reads tasks beneath an owned list from local persistence.
func (p *Local) FetchTasks(ctx context.Context, listID domain.ListID) ([]domain.Task, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if _, err := p.getOwnedList(ctx, listID); err != nil {
		return nil, fmt.Errorf("check local list %s: %w", listID, err)
	}
	if p.tasks == nil {
		return nil, ErrRepositoryUnavailable
	}

	tasks, err := p.tasks.ListByList(ctx, listID)
	if err != nil {
		return nil, fmt.Errorf("list local tasks for list %s: %w", listID, err)
	}
	result := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.ListID != listID {
			return nil, fmt.Errorf("task %s has unexpected parent list %s: %w", task.ID, task.ListID, domain.ErrInvalidParent)
		}
		task, err = p.ownedTask(task)
		if err != nil {
			return nil, err
		}
		if err := p.checkParentTask(ctx, task.ID, task.ParentTaskID); err != nil {
			return nil, err
		}
		result = append(result, localTask(task))
	}
	return result, nil
}

// FetchTask reads one task from local persistence and validates its hierarchy.
func (p *Local) FetchTask(ctx context.Context, taskID domain.TaskID) (domain.Task, error) {
	if err := contextError(ctx); err != nil {
		return domain.Task{}, err
	}
	task, err := p.getOwnedTask(ctx, taskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("get local task %s: %w", taskID, err)
	}
	return localTask(task), nil
}

// CreateSpace persists a local space.
func (p *Local) CreateSpace(ctx context.Context, space domain.Space) (domain.Space, error) {
	if err := contextError(ctx); err != nil {
		return domain.Space{}, err
	}
	if err := p.acceptCreateProvider(space.ProviderID); err != nil {
		return domain.Space{}, err
	}
	if p.spaceWrite == nil {
		return domain.Space{}, ErrRepositoryUnavailable
	}

	space.ProviderID = p.ID()
	space = localSpace(space)
	if space.ID.IsZero() {
		space.ID = domain.SpaceID(p.newID("space"))
	}
	setCreateTimes(&space.CreatedAt, &space.UpdatedAt)

	created, err := p.spaceWrite.Create(ctx, space)
	if err != nil {
		return domain.Space{}, fmt.Errorf("create local space: %w", err)
	}
	return p.createdSpace(space, created)
}

// UpdateSpace updates an owned local space.
func (p *Local) UpdateSpace(ctx context.Context, space domain.Space) (domain.Space, error) {
	if err := contextError(ctx); err != nil {
		return domain.Space{}, err
	}
	if space.ID.IsZero() {
		return domain.Space{}, fmt.Errorf("update local space: space ID is required")
	}
	if !space.ProviderID.IsZero() {
		if err := p.requireProvider(space.ProviderID); err != nil {
			return domain.Space{}, err
		}
	}
	if p.spaceWrite == nil {
		return domain.Space{}, ErrRepositoryUnavailable
	}

	existing, err := p.getOwnedSpace(ctx, space.ID)
	if err != nil {
		return domain.Space{}, fmt.Errorf("check local space %s: %w", space.ID, err)
	}
	if space.ProviderID.IsZero() {
		space.ProviderID = existing.ProviderID
	}
	if err := p.requireProvider(space.ProviderID); err != nil {
		return domain.Space{}, err
	}
	space.CreatedAt = existing.CreatedAt
	space = localSpace(space)
	space.UpdatedAt = time.Now().UTC()

	updated, err := p.spaceWrite.Update(ctx, space)
	if err != nil {
		return domain.Space{}, fmt.Errorf("update local space %s: %w", space.ID, err)
	}
	return p.updatedSpace(space, updated)
}

// DeleteSpace deletes an owned local space.
func (p *Local) DeleteSpace(ctx context.Context, space domain.Space) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := p.requireDeleteSpace(ctx, space); err != nil {
		return err
	}
	if p.spaceWrite == nil {
		return ErrRepositoryUnavailable
	}
	if err := p.spaceWrite.Delete(ctx, space.ID); err != nil {
		return fmt.Errorf("delete local space %s: %w", space.ID, err)
	}
	return nil
}

// CreateList persists a local list beneath an owned space.
func (p *Local) CreateList(ctx context.Context, list domain.List) (domain.List, error) {
	if err := contextError(ctx); err != nil {
		return domain.List{}, err
	}
	if err := p.acceptCreateProvider(list.ProviderID); err != nil {
		return domain.List{}, err
	}
	if _, err := p.getOwnedSpace(ctx, list.SpaceID); err != nil {
		return domain.List{}, fmt.Errorf("check parent space for local list: %w", err)
	}
	if p.listWrite == nil {
		return domain.List{}, ErrRepositoryUnavailable
	}

	list.ProviderID = p.ID()
	list = localList(list)
	if list.ID.IsZero() {
		list.ID = domain.ListID(p.newID("list"))
	}
	setCreateTimes(&list.CreatedAt, &list.UpdatedAt)

	created, err := p.listWrite.Create(ctx, list)
	if err != nil {
		return domain.List{}, fmt.Errorf("create local list: %w", err)
	}
	return p.createdList(ctx, list, created)
}

// UpdateList updates an owned local list. Changing its space is allowed only
// when the destination space is also owned by this provider.
func (p *Local) UpdateList(ctx context.Context, list domain.List) (domain.List, error) {
	if err := contextError(ctx); err != nil {
		return domain.List{}, err
	}
	if list.ID.IsZero() {
		return domain.List{}, fmt.Errorf("update local list: list ID is required")
	}
	if !list.ProviderID.IsZero() {
		if err := p.requireProvider(list.ProviderID); err != nil {
			return domain.List{}, err
		}
	}
	if p.listWrite == nil {
		return domain.List{}, ErrRepositoryUnavailable
	}

	existing, err := p.getOwnedList(ctx, list.ID)
	if err != nil {
		return domain.List{}, fmt.Errorf("check local list %s: %w", list.ID, err)
	}
	if list.ProviderID.IsZero() {
		list.ProviderID = existing.ProviderID
	}
	if err := p.requireProvider(list.ProviderID); err != nil {
		return domain.List{}, err
	}
	if list.SpaceID.IsZero() {
		list.SpaceID = existing.SpaceID
	}
	if _, err := p.getOwnedSpace(ctx, list.SpaceID); err != nil {
		return domain.List{}, fmt.Errorf("check parent space for local list %s: %w", list.ID, err)
	}
	list.CreatedAt = existing.CreatedAt
	list = localList(list)
	list.UpdatedAt = time.Now().UTC()

	updated, err := p.listWrite.Update(ctx, list)
	if err != nil {
		return domain.List{}, fmt.Errorf("update local list %s: %w", list.ID, err)
	}
	return p.updatedList(ctx, list, updated)
}

// DeleteList deletes an owned local list.
func (p *Local) DeleteList(ctx context.Context, list domain.List) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := p.requireDeleteList(ctx, list); err != nil {
		return err
	}
	if p.listWrite == nil {
		return ErrRepositoryUnavailable
	}
	if err := p.listWrite.Delete(ctx, list.ID); err != nil {
		return fmt.Errorf("delete local list %s: %w", list.ID, err)
	}
	return nil
}

// CreateTask persists a local task beneath an owned list.
func (p *Local) CreateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := contextError(ctx); err != nil {
		return domain.Task{}, err
	}
	if err := p.acceptCreateProvider(task.ProviderID); err != nil {
		return domain.Task{}, err
	}

	task.ProviderID = p.ID()
	if task.ID.IsZero() {
		task.ID = domain.TaskID(p.newID("task"))
	}
	if _, err := p.getOwnedList(ctx, task.ListID); err != nil {
		return domain.Task{}, fmt.Errorf("check parent list for local task: %w", err)
	}
	if err := p.checkParentTask(ctx, task.ID, task.ParentTaskID); err != nil {
		return domain.Task{}, err
	}
	if p.taskWrite == nil {
		return domain.Task{}, ErrRepositoryUnavailable
	}

	task = localTask(task)
	setTaskDefaults(&task)
	setCreateTimes(&task.CreatedAt, &task.UpdatedAt)

	created, err := p.taskWrite.Create(ctx, task)
	if err != nil {
		return domain.Task{}, fmt.Errorf("create local task: %w", err)
	}
	return p.createdTask(ctx, task, created)
}

// UpdateTask updates an owned local task and validates its resulting parent
// hierarchy.
func (p *Local) UpdateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := contextError(ctx); err != nil {
		return domain.Task{}, err
	}
	if task.ID.IsZero() {
		return domain.Task{}, fmt.Errorf("update local task: task ID is required")
	}
	if !task.ProviderID.IsZero() {
		if err := p.requireProvider(task.ProviderID); err != nil {
			return domain.Task{}, err
		}
	}
	if p.taskWrite == nil {
		return domain.Task{}, ErrRepositoryUnavailable
	}

	existing, err := p.getOwnedTask(ctx, task.ID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("check local task %s: %w", task.ID, err)
	}
	if task.ProviderID.IsZero() {
		task.ProviderID = existing.ProviderID
	}
	if err := p.requireProvider(task.ProviderID); err != nil {
		return domain.Task{}, err
	}
	if task.ListID.IsZero() {
		task.ListID = existing.ListID
	}
	if _, err := p.getOwnedList(ctx, task.ListID); err != nil {
		return domain.Task{}, fmt.Errorf("check parent list for local task %s: %w", task.ID, err)
	}
	if err := p.checkParentTask(ctx, task.ID, task.ParentTaskID); err != nil {
		return domain.Task{}, err
	}
	task.CreatedAt = existing.CreatedAt
	task = localTask(task)
	setTaskDefaults(&task)
	task.UpdatedAt = time.Now().UTC()

	updated, err := p.taskWrite.Update(ctx, task)
	if err != nil {
		return domain.Task{}, fmt.Errorf("update local task %s: %w", task.ID, err)
	}
	return p.updatedTask(ctx, task, updated)
}

// DeleteTask deletes an owned local task.
func (p *Local) DeleteTask(ctx context.Context, task domain.Task) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := p.requireDeleteTask(ctx, task); err != nil {
		return err
	}
	if p.taskWrite == nil {
		return ErrRepositoryUnavailable
	}
	if err := p.taskWrite.Delete(ctx, task.ID); err != nil {
		return fmt.Errorf("delete local task %s: %w", task.ID, err)
	}
	return nil
}

// Sync is a context-aware no-op. Local state is already authoritative and no
// remote service is associated with this provider.
func (p *Local) Sync(ctx context.Context) error {
	return contextError(ctx)
}

// Pull is a context-aware no-op for callers that use the optional Puller
// capability. There is no remote state to pull for a local provider.
func (p *Local) Pull(ctx context.Context) error {
	return contextError(ctx)
}

// Authenticate is a context-aware no-op because local access needs no account
// or network authentication.
func (p *Local) Authenticate(ctx context.Context) error {
	return contextError(ctx)
}

func (p *Local) acceptCreateProvider(id domain.ProviderID) error {
	if !id.IsZero() {
		return p.requireProvider(id)
	}
	return nil
}

func (p *Local) requireProvider(id domain.ProviderID) error {
	if id != p.ID() {
		return fmt.Errorf("entity belongs to provider %s, local provider is %s: %w", id, p.ID(), ErrProviderMismatch)
	}
	return nil
}

func (p *Local) ownedSpace(space domain.Space) (domain.Space, error) {
	if err := p.requireProvider(space.ProviderID); err != nil {
		return domain.Space{}, fmt.Errorf("space %s: %w", space.ID, err)
	}
	return localSpace(space), nil
}

func (p *Local) ownedList(list domain.List) (domain.List, error) {
	if err := p.requireProvider(list.ProviderID); err != nil {
		return domain.List{}, fmt.Errorf("list %s: %w", list.ID, err)
	}
	return localList(list), nil
}

func (p *Local) ownedTask(task domain.Task) (domain.Task, error) {
	if err := p.requireProvider(task.ProviderID); err != nil {
		return domain.Task{}, fmt.Errorf("task %s: %w", task.ID, err)
	}
	return localTask(task), nil
}

func (p *Local) getOwnedSpace(ctx context.Context, id domain.SpaceID) (domain.Space, error) {
	if p.spaces == nil {
		return domain.Space{}, ErrRepositoryUnavailable
	}
	space, err := p.spaces.Get(ctx, id)
	if err != nil {
		return domain.Space{}, err
	}
	return p.ownedSpace(space)
}

func (p *Local) getOwnedList(ctx context.Context, id domain.ListID) (domain.List, error) {
	if p.lists == nil {
		return domain.List{}, ErrRepositoryUnavailable
	}
	list, err := p.lists.Get(ctx, id)
	if err != nil {
		return domain.List{}, err
	}
	list, err = p.ownedList(list)
	if err != nil {
		return domain.List{}, err
	}
	if _, err := p.getOwnedSpace(ctx, list.SpaceID); err != nil {
		return domain.List{}, fmt.Errorf("list %s parent: %w", id, err)
	}
	return list, nil
}

func (p *Local) getOwnedTask(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	if p.tasks == nil {
		return domain.Task{}, ErrRepositoryUnavailable
	}
	task, err := p.tasks.Get(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	task, err = p.ownedTask(task)
	if err != nil {
		return domain.Task{}, err
	}
	if _, err := p.getOwnedList(ctx, task.ListID); err != nil {
		return domain.Task{}, fmt.Errorf("task %s parent: %w", id, err)
	}
	if err := p.checkParentTask(ctx, task.ID, task.ParentTaskID); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func (p *Local) getOwnedParentTask(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	if p.tasks == nil {
		return domain.Task{}, ErrRepositoryUnavailable
	}
	parent, err := p.tasks.Get(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	parent, err = p.ownedTask(parent)
	if err != nil {
		return domain.Task{}, err
	}
	if _, err := p.getOwnedList(ctx, parent.ListID); err != nil {
		return domain.Task{}, fmt.Errorf("parent task %s parent list: %w", id, err)
	}
	return parent, nil
}

func (p *Local) checkParentTask(ctx context.Context, childID domain.TaskID, parentID *domain.TaskID) error {
	if parentID == nil {
		return nil
	}
	if childID == *parentID {
		return fmt.Errorf("task %s cannot parent itself: %w", childID, domain.ErrInvalidParent)
	}
	if _, err := p.getOwnedParentTask(ctx, *parentID); err != nil {
		return fmt.Errorf("check parent task %s: %w", parentID, err)
	}
	return nil
}

func (p *Local) requireDeleteSpace(ctx context.Context, space domain.Space) error {
	if !space.ProviderID.IsZero() {
		if err := p.requireProvider(space.ProviderID); err != nil {
			return err
		}
	}
	if space.ID.IsZero() {
		return fmt.Errorf("delete local space: space ID is required")
	}
	_, err := p.getOwnedSpace(ctx, space.ID)
	return err
}

func (p *Local) requireDeleteList(ctx context.Context, list domain.List) error {
	if !list.ProviderID.IsZero() {
		if err := p.requireProvider(list.ProviderID); err != nil {
			return err
		}
	}
	if list.ID.IsZero() {
		return fmt.Errorf("delete local list: list ID is required")
	}
	_, err := p.getOwnedList(ctx, list.ID)
	return err
}

func (p *Local) requireDeleteTask(ctx context.Context, task domain.Task) error {
	if !task.ProviderID.IsZero() {
		if err := p.requireProvider(task.ProviderID); err != nil {
			return err
		}
	}
	if task.ID.IsZero() {
		return fmt.Errorf("delete local task: task ID is required")
	}
	_, err := p.getOwnedTask(ctx, task.ID)
	return err
}

func (p *Local) createdSpace(input, output domain.Space) (domain.Space, error) {
	if output.ID.IsZero() {
		output = input
	}
	return p.ownedSpace(output)
}

func (p *Local) updatedSpace(input, output domain.Space) (domain.Space, error) {
	if output.ID.IsZero() {
		output = input
	}
	return p.ownedSpace(output)
}

func (p *Local) createdList(ctx context.Context, input, output domain.List) (domain.List, error) {
	if output.ID.IsZero() {
		output = input
	}
	list, err := p.ownedList(output)
	if err != nil {
		return domain.List{}, err
	}
	if list.SpaceID != input.SpaceID {
		return domain.List{}, fmt.Errorf("list %s has unexpected parent space %s: %w", list.ID, list.SpaceID, domain.ErrInvalidParent)
	}
	if _, err := p.getOwnedSpace(ctx, list.SpaceID); err != nil {
		return domain.List{}, fmt.Errorf("check created local list %s parent: %w", list.ID, err)
	}
	return list, nil
}

func (p *Local) updatedList(ctx context.Context, input, output domain.List) (domain.List, error) {
	if output.ID.IsZero() {
		output = input
	}
	return p.createdList(ctx, input, output)
}

func (p *Local) createdTask(ctx context.Context, input, output domain.Task) (domain.Task, error) {
	if output.ID.IsZero() {
		output = input
	}
	task, err := p.ownedTask(output)
	if err != nil {
		return domain.Task{}, err
	}
	if task.ListID != input.ListID {
		return domain.Task{}, fmt.Errorf("task %s has unexpected parent list %s: %w", task.ID, task.ListID, domain.ErrInvalidParent)
	}
	if _, err := p.getOwnedList(ctx, task.ListID); err != nil {
		return domain.Task{}, fmt.Errorf("check created local task %s parent: %w", task.ID, err)
	}
	if err := p.checkParentTask(ctx, task.ID, task.ParentTaskID); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func (p *Local) updatedTask(ctx context.Context, input, output domain.Task) (domain.Task, error) {
	if output.ID.IsZero() {
		output = input
	}
	return p.createdTask(ctx, input, output)
}

func (p *Local) newID(kind string) string {
	sequence := p.sequence.Add(1)
	return fmt.Sprintf("local-%s-%d-%d", kind, time.Now().UTC().UnixNano(), sequence)
}

func localSpace(space domain.Space) domain.Space {
	space.RemoteID = nil
	space.RemoteUpdatedAt = nil
	space.SyncState = domain.SyncStateLocal
	return space.NormalizeUTC()
}

func localList(list domain.List) domain.List {
	list.RemoteID = nil
	list.RemoteUpdatedAt = nil
	list.SyncState = domain.SyncStateLocal
	return list.NormalizeUTC()
}

func localTask(task domain.Task) domain.Task {
	task.RemoteID = nil
	task.RemoteUpdatedAt = nil
	task.SyncState = domain.SyncStateLocal
	return task.NormalizeUTC()
}

func setTaskDefaults(task *domain.Task) {
	if task.Status == "" {
		task.Status = "todo"
	}
	if task.Priority.IsZero() {
		task.Priority = domain.PriorityNormal
	}
}

func setCreateTimes(createdAt, updatedAt *time.Time) {
	now := time.Now().UTC()
	if createdAt.IsZero() {
		*createdAt = now
	}
	*updatedAt = now
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
