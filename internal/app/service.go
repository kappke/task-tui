package app

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type IDGenerator interface {
	NewID(kind EntityType) (string, error)
}

type RandomIDGenerator struct{}

func (RandomIDGenerator) NewID(_ EntityType) (string, error) {
	var raw [16]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate identity: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

type Option func(*Service)

func WithClock(clock Clock) Option {
	return func(service *Service) {
		if clock != nil {
			service.clock = clock
		}
	}
}

func WithIDGenerator(generator IDGenerator) Option {
	return func(service *Service) {
		if generator != nil {
			service.ids = generator
		}
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Service contains application behavior only. In particular, providers are
// represented here by local metadata; remote adapters are used by a separate
// synchronization worker and are never called by these synchronous methods.
type Service struct {
	repo      CoreRepository
	providers ProviderRegistry
	clock     Clock
	ids       IDGenerator
}

type TaskService = Service

func NewService(repo CoreRepository, providers ProviderRegistry, options ...Option) *Service {
	service := &Service{
		repo:      repo,
		providers: providers,
		clock:     systemClock{},
		ids:       RandomIDGenerator{},
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func NewTaskService(repo CoreRepository, providers ProviderRegistry, options ...Option) *Service {
	return NewService(repo, providers, options...)
}

func (s *Service) GetSpace(ctx context.Context, id SpaceID) (Space, error) {
	if err := checkContext(ctx); err != nil {
		return Space{}, fmt.Errorf("get space %s: %w", id, err)
	}
	if err := s.requireRepository(); err != nil {
		return Space{}, fmt.Errorf("get space %s: %w", id, err)
	}
	space, err := s.repo.GetSpace(ctx, id)
	if err != nil {
		return Space{}, fmt.Errorf("get space %s: %w", id, err)
	}
	if space.ID != id {
		return Space{}, fmt.Errorf("get space %s returned %s: %w", id, space.ID, ErrInvalidEntity)
	}
	return cloneSpace(space), nil
}

func (s *Service) GetList(ctx context.Context, id ListID) (List, error) {
	if err := checkContext(ctx); err != nil {
		return List{}, fmt.Errorf("get list %s: %w", id, err)
	}
	if err := s.requireRepository(); err != nil {
		return List{}, fmt.Errorf("get list %s: %w", id, err)
	}
	list, err := s.repo.GetList(ctx, id)
	if err != nil {
		return List{}, fmt.Errorf("get list %s: %w", id, err)
	}
	if list.ID != id {
		return List{}, fmt.Errorf("get list %s returned %s: %w", id, list.ID, ErrInvalidEntity)
	}
	return cloneList(list), nil
}

func (s *Service) GetTask(ctx context.Context, id TaskID) (Task, error) {
	if err := checkContext(ctx); err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	if err := s.requireRepository(); err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	task, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	if task.ID != id {
		return Task{}, fmt.Errorf("get task %s returned %s: %w", id, task.ID, ErrInvalidEntity)
	}
	return cloneTask(task), nil
}

func (s *Service) ListTasks(ctx context.Context, listID ListID) ([]Task, error) {
	if err := checkContext(ctx); err != nil {
		return nil, fmt.Errorf("list tasks for list %s: %w", listID, err)
	}
	if err := s.requireRepository(); err != nil {
		return nil, fmt.Errorf("list tasks for list %s: %w", listID, err)
	}
	tasks, err := s.repo.ListTasks(ctx, listID)
	if err != nil {
		return nil, fmt.Errorf("list tasks for list %s: %w", listID, err)
	}
	return cloneTasks(tasks), nil
}

func (s *Service) CreateSpace(ctx context.Context, input CreateSpaceInput) (Space, error) {
	if err := checkContext(ctx); err != nil {
		return Space{}, fmt.Errorf("create space: %w", err)
	}
	if err := s.requireRepository(); err != nil {
		return Space{}, fmt.Errorf("create space: %w", err)
	}
	if input.ProviderID == "" {
		return Space{}, fmt.Errorf("create space: %w: provider id is empty", ErrInvalidInput)
	}
	if strings.TrimSpace(input.Name) == "" {
		return Space{}, fmt.Errorf("create space: %w: name is empty", ErrInvalidInput)
	}
	provider, err := s.provider(ctx, input.ProviderID)
	if err != nil {
		return Space{}, fmt.Errorf("create space for provider %s: %w", input.ProviderID, err)
	}
	if err := requireCapability(provider, CapabilityCreateSpace); err != nil {
		return Space{}, fmt.Errorf("create space for provider %s: %w", input.ProviderID, err)
	}
	id, err := s.newID(EntityTypeSpace)
	if err != nil {
		return Space{}, fmt.Errorf("create space: %w", err)
	}
	now := s.now()
	space := Space{
		ID:         SpaceID(id),
		ProviderID: input.ProviderID,
		Name:       input.Name,
		SyncState:  stateFor(provider),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	intent, err := makeIntent(provider, EntityTypeSpace, id, OperationCreate, domain.NewSpaceCreateMutationPayload(space))
	if err != nil {
		return Space{}, fmt.Errorf("create space %s: %w", id, err)
	}
	saved, err := s.repo.CreateSpace(ctx, space, intent)
	if err != nil {
		return Space{}, fmt.Errorf("persist space %s: %w", id, err)
	}
	if err := validateSavedSpace(space, saved); err != nil {
		return Space{}, fmt.Errorf("persist space %s: %w", id, err)
	}
	return cloneSpace(saved), nil
}

func (s *Service) CreateList(ctx context.Context, input CreateListInput) (List, error) {
	if err := checkContext(ctx); err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	if err := s.requireRepository(); err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	if input.ProviderID == "" || input.SpaceID == "" {
		return List{}, fmt.Errorf("create list: %w: provider and space are required", ErrInvalidInput)
	}
	if strings.TrimSpace(input.Name) == "" {
		return List{}, fmt.Errorf("create list: %w: name is empty", ErrInvalidInput)
	}
	provider, err := s.provider(ctx, input.ProviderID)
	if err != nil {
		return List{}, fmt.Errorf("create list for provider %s: %w", input.ProviderID, err)
	}
	if err := requireCapability(provider, CapabilityCreateList); err != nil {
		return List{}, fmt.Errorf("create list for provider %s: %w", input.ProviderID, err)
	}
	space, err := s.loadSpace(ctx, input.SpaceID)
	if err != nil {
		return List{}, fmt.Errorf("create list in space %s: %w", input.SpaceID, err)
	}
	if space.ProviderID != input.ProviderID {
		return List{}, fmt.Errorf("create list in space %s: list provider %s, space provider %s: %w", input.SpaceID, input.ProviderID, space.ProviderID, ErrProviderMismatch)
	}
	id, err := s.newID(EntityTypeList)
	if err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	now := s.now()
	list := List{
		ID:         ListID(id),
		ProviderID: input.ProviderID,
		SpaceID:    input.SpaceID,
		Name:       input.Name,
		SyncState:  stateFor(provider),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	intent, err := makeIntent(provider, EntityTypeList, id, OperationCreate, domain.NewListCreateMutationPayload(list))
	if err != nil {
		return List{}, fmt.Errorf("create list %s: %w", id, err)
	}
	saved, err := s.repo.CreateList(ctx, list, intent)
	if err != nil {
		return List{}, fmt.Errorf("persist list %s: %w", id, err)
	}
	if err := validateSavedList(list, saved); err != nil {
		return List{}, fmt.Errorf("persist list %s: %w", id, err)
	}
	return cloneList(saved), nil
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (Task, error) {
	if err := checkContext(ctx); err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	if err := s.requireRepository(); err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	if input.ProviderID == "" || input.ListID == "" {
		return Task{}, fmt.Errorf("create task: %w: provider and list are required", ErrInvalidInput)
	}
	if strings.TrimSpace(input.Title) == "" {
		return Task{}, fmt.Errorf("create task: %w: title is empty", ErrInvalidInput)
	}
	provider, err := s.provider(ctx, input.ProviderID)
	if err != nil {
		return Task{}, fmt.Errorf("create task for provider %s: %w", input.ProviderID, err)
	}
	if err := requireCapability(provider, CapabilityCreateTask); err != nil {
		return Task{}, fmt.Errorf("create task for provider %s: %w", input.ProviderID, err)
	}
	if input.Priority != "" {
		if err := input.Priority.Validate(); err != nil {
			return Task{}, fmt.Errorf("create task for provider %s: %w", input.ProviderID, err)
		}
	}
	if err := requireTaskFeatures(provider, input.DueAt != nil, input.ParentTaskID != nil); err != nil {
		return Task{}, fmt.Errorf("create task for provider %s: %w", input.ProviderID, err)
	}
	list, err := s.loadList(ctx, input.ListID)
	if err != nil {
		return Task{}, fmt.Errorf("create task in list %s: %w", input.ListID, err)
	}
	if list.ProviderID != input.ProviderID {
		return Task{}, fmt.Errorf("create task in list %s: task provider %s, list provider %s: %w", input.ListID, input.ProviderID, list.ProviderID, ErrProviderMismatch)
	}
	parentID := cloneTaskID(input.ParentTaskID)
	if parentID != nil {
		if *parentID == "" {
			return Task{}, fmt.Errorf("create task: %w: parent id is empty", ErrInvalidParent)
		}
		parent, err := s.loadTask(ctx, *parentID)
		if err != nil {
			return Task{}, fmt.Errorf("create task with parent %s: %w", *parentID, err)
		}
		candidate := Task{ProviderID: input.ProviderID}
		if err := ValidateParentProvider(candidate, parent); err != nil {
			return Task{}, fmt.Errorf("create task with parent %s: %w", *parentID, err)
		}
	}
	id, err := s.newID(EntityTypeTask)
	if err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	now := s.now()
	status := input.Status
	if status == "" {
		status = StatusTodo
	}
	priority := input.Priority
	if priority == "" {
		priority = PriorityNormal
	}
	task := Task{
		ID:           TaskID(id),
		ProviderID:   input.ProviderID,
		ListID:       input.ListID,
		ParentTaskID: parentID,
		Title:        input.Title,
		Description:  input.Description,
		Status:       status,
		Priority:     priority,
		DueAt:        cloneTime(input.DueAt),
		CompletedAt:  cloneTime(input.CompletedAt),
		SyncState:    stateFor(provider),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	intent, err := makeIntent(provider, EntityTypeTask, id, OperationCreate, domain.NewTaskCreateMutationPayload(task))
	if err != nil {
		return Task{}, fmt.Errorf("create task %s: %w", id, err)
	}
	saved, err := s.repo.CreateTask(ctx, task, intent)
	if err != nil {
		return Task{}, fmt.Errorf("persist task %s: %w", id, err)
	}
	if err := validateSavedTask(task, saved); err != nil {
		return Task{}, fmt.Errorf("persist task %s: %w", id, err)
	}
	return cloneTask(saved), nil
}

func (s *Service) PatchTask(ctx context.Context, id TaskID, patch TaskPatch) (Task, error) {
	if err := checkContext(ctx); err != nil {
		return Task{}, fmt.Errorf("patch task %s: %w", id, err)
	}
	if err := s.requireRepository(); err != nil {
		return Task{}, fmt.Errorf("patch task %s: %w", id, err)
	}
	task, err := s.loadTask(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("patch task %s: %w", id, err)
	}
	if !taskPatchHasChanges(patch) {
		return task, nil
	}
	provider, err := s.provider(ctx, task.ProviderID)
	if err != nil {
		return Task{}, fmt.Errorf("patch task %s: %w", id, err)
	}
	if err := requireCapability(provider, CapabilityUpdateTask); err != nil {
		return Task{}, fmt.Errorf("patch task %s: %w", id, err)
	}
	if err := requireTaskFeatures(provider, patch.DueAt != nil || patch.ClearDueAt, patch.ParentTaskID != nil || patch.ClearParentTask); err != nil {
		return Task{}, fmt.Errorf("patch task %s: %w", id, err)
	}
	if patch.ParentTaskID != nil {
		if *patch.ParentTaskID == "" {
			return Task{}, fmt.Errorf("patch task %s: %w: parent id is empty", id, ErrInvalidParent)
		}
		parent, err := s.loadTask(ctx, *patch.ParentTaskID)
		if err != nil {
			return Task{}, fmt.Errorf("patch task %s parent %s: %w", id, *patch.ParentTaskID, err)
		}
		candidate := task
		if err := ValidateParentProvider(candidate, parent); err != nil {
			return Task{}, fmt.Errorf("patch task %s parent %s: %w", id, *patch.ParentTaskID, err)
		}
	}
	now := s.now()
	if patch.ListID != nil {
		destination, err := s.loadList(ctx, *patch.ListID)
		if err != nil {
			return Task{}, fmt.Errorf("patch task %s destination list %s: %w", id, *patch.ListID, err)
		}
		if destination.ProviderID != task.ProviderID {
			return Task{}, fmt.Errorf("patch task %s from provider %s to list %s owned by provider %s: %w: %w", id, task.ProviderID, *patch.ListID, destination.ProviderID, ErrCrossProviderMove, ErrProviderMismatch)
		}
	}
	updated, err := ApplyTaskPatch(task, patch, now)
	if err != nil {
		return Task{}, fmt.Errorf("apply patch to task %s: %w", id, err)
	}
	updated.SyncState = stateFor(provider)
	intent, err := makeIntent(provider, EntityTypeTask, string(id), OperationUpdate, domain.NewTaskUpdateMutationPayload(updated, patch))
	if err != nil {
		return Task{}, fmt.Errorf("patch task %s: %w", id, err)
	}
	saved, err := s.repo.UpdateTask(ctx, updated, intent)
	if err != nil {
		return Task{}, fmt.Errorf("persist patch for task %s: %w", id, err)
	}
	if err := validateSavedTask(updated, saved); err != nil {
		return Task{}, fmt.Errorf("persist patch for task %s: %w", id, err)
	}
	return cloneTask(saved), nil
}

func (s *Service) UpdateTask(ctx context.Context, id TaskID, patch TaskPatch) (Task, error) {
	return s.PatchTask(ctx, id, patch)
}

func (s *Service) CompleteTask(ctx context.Context, id TaskID) (Task, error) {
	if err := checkContext(ctx); err != nil {
		return Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	if err := s.requireRepository(); err != nil {
		return Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	task, err := s.loadTask(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	provider, err := s.provider(ctx, task.ProviderID)
	if err != nil {
		return Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	if err := requireCapability(provider, CapabilityCompleteTask); err != nil {
		return Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	now := s.now()
	status := StatusDone
	patch := TaskPatch{Status: &status, CompletedAt: &now}
	updated, err := ApplyTaskPatch(task, patch, now)
	if err != nil {
		return Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	updated.SyncState = stateFor(provider)
	intent, err := makeIntent(provider, EntityTypeTask, string(id), OperationComplete, domain.NewTaskUpdateMutationPayload(updated, patch))
	if err != nil {
		return Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	saved, err := s.repo.UpdateTask(ctx, updated, intent)
	if err != nil {
		return Task{}, fmt.Errorf("persist completion for task %s: %w", id, err)
	}
	if err := validateSavedTask(updated, saved); err != nil {
		return Task{}, fmt.Errorf("persist completion for task %s: %w", id, err)
	}
	return cloneTask(saved), nil
}

func (s *Service) DeleteTask(ctx context.Context, id TaskID) error {
	if err := checkContext(ctx); err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	if err := s.requireRepository(); err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	task, err := s.loadTask(ctx, id)
	if err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	provider, err := s.provider(ctx, task.ProviderID)
	if err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	if err := requireCapability(provider, CapabilityDeleteTask); err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	if err := s.deleteLoadedTask(ctx, task, provider); err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	return nil
}

func (s *Service) MoveTask(ctx context.Context, id TaskID, destinationListID ListID) (Task, error) {
	if err := checkContext(ctx); err != nil {
		return Task{}, fmt.Errorf("move task %s: %w", id, err)
	}
	if err := s.requireRepository(); err != nil {
		return Task{}, fmt.Errorf("move task %s: %w", id, err)
	}
	task, err := s.loadTask(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("move task %s: %w", id, err)
	}
	destination, err := s.loadList(ctx, destinationListID)
	if err != nil {
		return Task{}, fmt.Errorf("move task %s to list %s: %w", id, destinationListID, err)
	}
	if task.ProviderID != destination.ProviderID {
		return Task{}, fmt.Errorf("move task %s from provider %s to list %s owned by provider %s: %w: %w", id, task.ProviderID, destinationListID, destination.ProviderID, ErrCrossProviderMove, ErrProviderMismatch)
	}
	if task.ListID == destinationListID {
		return task, nil
	}
	provider, err := s.provider(ctx, task.ProviderID)
	if err != nil {
		return Task{}, fmt.Errorf("move task %s: %w", id, err)
	}
	if err := requireCapability(provider, CapabilityMoveTask); err != nil {
		return Task{}, fmt.Errorf("move task %s: %w", id, err)
	}
	moved := cloneTask(task)
	moved.ListID = destinationListID
	moved.UpdatedAt = s.now()
	moved.SyncState = stateFor(provider)
	patch := TaskPatch{ListID: &destinationListID}
	intent, err := makeIntent(provider, EntityTypeTask, string(id), OperationMove, domain.NewTaskUpdateMutationPayload(moved, patch))
	if err != nil {
		return Task{}, fmt.Errorf("move task %s: %w", id, err)
	}
	saved, err := s.repo.MoveTask(ctx, moved, intent)
	if err != nil {
		return Task{}, fmt.Errorf("persist move for task %s: %w", id, err)
	}
	if err := validateSavedTask(moved, saved); err != nil {
		return Task{}, fmt.Errorf("persist move for task %s: %w", id, err)
	}
	return cloneTask(saved), nil
}

func (s *Service) CopyTask(ctx context.Context, sourceID TaskID, destinationListID ListID) (Task, error) {
	if err := checkContext(ctx); err != nil {
		return Task{}, fmt.Errorf("copy task %s: %w", sourceID, err)
	}
	if err := s.requireRepository(); err != nil {
		return Task{}, fmt.Errorf("copy task %s: %w", sourceID, err)
	}
	source, err := s.loadTask(ctx, sourceID)
	if err != nil {
		return Task{}, fmt.Errorf("copy task %s: %w", sourceID, err)
	}
	destination, err := s.loadList(ctx, destinationListID)
	if err != nil {
		return Task{}, fmt.Errorf("copy task %s to list %s: %w", sourceID, destinationListID, err)
	}
	copied, err := s.copyLoadedTask(ctx, source, destination)
	if err != nil {
		return Task{}, fmt.Errorf("copy task %s to list %s: %w", sourceID, destinationListID, err)
	}
	return copied, nil
}

// TransferTask copies the source into a new destination identity and then
// deletes the source. On a delete failure it returns the created destination
// together with PartialTransferError; the source is never rewritten to the
// destination provider.
func (s *Service) TransferTask(ctx context.Context, sourceID TaskID, destinationListID ListID) (Task, error) {
	result, err := s.TransferTaskResult(ctx, sourceID, destinationListID)
	return result.Destination, err
}

func (s *Service) TransferTaskResult(ctx context.Context, sourceID TaskID, destinationListID ListID) (TransferResult, error) {
	if err := checkContext(ctx); err != nil {
		return TransferResult{}, fmt.Errorf("transfer task %s: %w", sourceID, err)
	}
	if err := s.requireRepository(); err != nil {
		return TransferResult{}, fmt.Errorf("transfer task %s: %w", sourceID, err)
	}
	source, err := s.loadTask(ctx, sourceID)
	if err != nil {
		return TransferResult{}, fmt.Errorf("transfer task %s: %w", sourceID, err)
	}
	destinationList, err := s.loadList(ctx, destinationListID)
	if err != nil {
		return TransferResult{Source: source}, fmt.Errorf("transfer task %s to list %s: %w", sourceID, destinationListID, err)
	}
	sourceProvider, err := s.provider(ctx, source.ProviderID)
	if err != nil {
		return TransferResult{Source: source}, fmt.Errorf("transfer task %s: %w", sourceID, err)
	}
	if err := requireCapability(sourceProvider, CapabilityDeleteTask); err != nil {
		return TransferResult{Source: source}, fmt.Errorf("transfer task %s: %w", sourceID, err)
	}
	copied, err := s.copyLoadedTask(ctx, source, destinationList)
	if err != nil {
		return TransferResult{Source: source}, fmt.Errorf("transfer task %s destination: %w", sourceID, err)
	}
	result := TransferResult{Source: source, Destination: copied}
	if err := checkContext(ctx); err != nil {
		return result, &PartialTransferError{Destination: copied, Err: err}
	}
	if err := s.deleteLoadedTask(ctx, source, sourceProvider); err != nil {
		return result, &PartialTransferError{Destination: copied, Err: fmt.Errorf("delete source task %s: %w", sourceID, err)}
	}
	result.SourceDeleted = true
	return result, nil
}

func (s *Service) SearchTasks(ctx context.Context, query string) ([]TaskSearchResult, error) {
	if err := checkContext(ctx); err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	if err := s.requireRepository(); err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	queries, ok := s.repo.(TaskQueryRepository)
	if !ok {
		return nil, fmt.Errorf("search tasks: %w", ErrQueryUnavailable)
	}
	results, err := queries.SearchTasks(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	return cloneSearchResults(results), nil
}

func (s *Service) FilterTasks(ctx context.Context, filter TaskFilter) ([]TaskSearchResult, error) {
	if err := checkContext(ctx); err != nil {
		return nil, fmt.Errorf("filter tasks: %w", err)
	}
	if err := s.requireRepository(); err != nil {
		return nil, fmt.Errorf("filter tasks: %w", err)
	}
	if err := filter.Validate(); err != nil {
		return nil, fmt.Errorf("filter tasks: %w", err)
	}
	filter = filter.NormalizeUTC()
	queries, ok := s.repo.(TaskQueryRepository)
	if !ok {
		return nil, fmt.Errorf("filter tasks: %w", ErrQueryUnavailable)
	}
	results, err := queries.FilterTasks(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("filter tasks: %w", err)
	}
	return cloneSearchResults(results), nil
}

func ApplyTaskPatch(task Task, patch TaskPatch, now time.Time) (Task, error) {
	if patch.ClearDueAt && patch.DueAt != nil {
		return Task{}, fmt.Errorf("%w: due date cannot be set and cleared in one patch", ErrInvalidInput)
	}
	if patch.ClearParentTask && patch.ParentTaskID != nil {
		return Task{}, fmt.Errorf("%w: parent cannot be set and cleared in one patch", ErrInvalidInput)
	}
	if patch.ClearCompletedAt && patch.CompletedAt != nil {
		return Task{}, fmt.Errorf("%w: completion cannot be set and cleared in one patch", ErrInvalidInput)
	}
	updated, err := patch.Apply(cloneTask(task))
	if err != nil {
		return Task{}, fmt.Errorf("apply task patch: %w", err)
	}
	updated.UpdatedAt = now.UTC()
	return updated, nil
}

type createSpacePayload struct {
	Name string `json:"name"`
}

type createListPayload struct {
	SpaceID SpaceID `json:"space_id"`
	Name    string  `json:"name"`
}

type moveTaskPayload struct {
	ListID ListID `json:"list_id"`
}

type deleteTaskPayload struct {
	RemoteID *string `json:"remote_id,omitempty"`
}

func (s *Service) copyLoadedTask(ctx context.Context, source Task, destinationList List) (Task, error) {
	if err := checkContext(ctx); err != nil {
		return Task{}, err
	}
	provider, err := s.provider(ctx, destinationList.ProviderID)
	if err != nil {
		return Task{}, err
	}
	if err := requireCapability(provider, CapabilityCreateTask); err != nil {
		return Task{}, err
	}
	if err := requireTaskFeatures(provider, source.DueAt != nil, false); err != nil {
		return Task{}, err
	}
	priority := source.Priority
	if priority == "" {
		priority = PriorityNormal
	}
	if err := priority.Validate(); err != nil {
		return Task{}, fmt.Errorf("copy task %s priority: %w", source.ID, err)
	}
	id, err := s.newID(EntityTypeTask)
	if err != nil {
		return Task{}, err
	}
	if TaskID(id) == source.ID {
		return Task{}, fmt.Errorf("copy task %s: %w", source.ID, ErrIDCollision)
	}
	now := s.now()
	copy := Task{
		ID:          TaskID(id),
		ProviderID:  destinationList.ProviderID,
		ListID:      destinationList.ID,
		Title:       source.Title,
		Description: source.Description,
		Status:      source.Status,
		Priority:    priority,
		DueAt:       cloneTime(source.DueAt),
		CompletedAt: cloneTime(source.CompletedAt),
		SyncState:   stateFor(provider),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	intent, err := makeIntent(provider, EntityTypeTask, id, OperationCreate, domain.NewTaskCreateMutationPayload(copy))
	if err != nil {
		return Task{}, err
	}
	saved, err := s.repo.CreateTask(ctx, copy, intent)
	if err != nil {
		return Task{}, err
	}
	if err := validateSavedTask(copy, saved); err != nil {
		return Task{}, err
	}
	if saved.RemoteID != nil || saved.ParentTaskID != nil || saved.RemoteUpdatedAt != nil {
		return Task{}, fmt.Errorf("copy task %s: %w: destination retained source-owned state", source.ID, ErrInvalidEntity)
	}
	return cloneTask(saved), nil
}

func (s *Service) deleteLoadedTask(ctx context.Context, task Task, provider Provider) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	intent, err := makeIntent(provider, EntityTypeTask, string(task.ID), OperationDelete, domain.NewTaskDeleteMutationPayload(task))
	if err != nil {
		return err
	}
	if err := s.repo.DeleteTask(ctx, task, intent); err != nil {
		return err
	}
	return nil
}

func (s *Service) loadSpace(ctx context.Context, id SpaceID) (Space, error) {
	space, err := s.repo.GetSpace(ctx, id)
	if err != nil {
		return Space{}, fmt.Errorf("load space %s: %w", id, err)
	}
	if space.ID != id {
		return Space{}, fmt.Errorf("load space %s returned %s: %w", id, space.ID, ErrInvalidEntity)
	}
	if err := ValidateSpace(space); err != nil {
		return Space{}, fmt.Errorf("load space %s: %w", id, err)
	}
	return cloneSpace(space), nil
}

func (s *Service) loadList(ctx context.Context, id ListID) (List, error) {
	list, err := s.repo.GetList(ctx, id)
	if err != nil {
		return List{}, fmt.Errorf("load list %s: %w", id, err)
	}
	if list.ID != id {
		return List{}, fmt.Errorf("load list %s returned %s: %w", id, list.ID, ErrInvalidEntity)
	}
	if err := ValidateList(list); err != nil {
		return List{}, fmt.Errorf("load list %s: %w", id, err)
	}
	space, err := s.loadSpace(ctx, list.SpaceID)
	if err != nil {
		return List{}, fmt.Errorf("load list %s space %s: %w", id, list.SpaceID, err)
	}
	if err := ValidateListProvider(list, space); err != nil {
		return List{}, fmt.Errorf("load list %s: %w", id, err)
	}
	return cloneList(list), nil
}

func (s *Service) loadTask(ctx context.Context, id TaskID) (Task, error) {
	task, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("load task %s: %w", id, err)
	}
	if task.ID != id {
		return Task{}, fmt.Errorf("load task %s returned %s: %w", id, task.ID, ErrInvalidEntity)
	}
	if err := ValidateTask(task); err != nil {
		return Task{}, fmt.Errorf("load task %s: %w", id, err)
	}
	list, err := s.loadList(ctx, task.ListID)
	if err != nil {
		return Task{}, fmt.Errorf("load task %s list %s: %w", id, task.ListID, err)
	}
	if err := ValidateTaskProvider(task, list); err != nil {
		return Task{}, fmt.Errorf("load task %s: %w", id, err)
	}
	if task.ParentTaskID != nil {
		if *task.ParentTaskID == "" {
			return Task{}, fmt.Errorf("load task %s: %w: parent id is empty", id, ErrInvalidParent)
		}
		parent, err := s.repo.GetTask(ctx, *task.ParentTaskID)
		if err != nil {
			return Task{}, fmt.Errorf("load task %s parent %s: %w", id, *task.ParentTaskID, err)
		}
		if parent.ID != *task.ParentTaskID {
			return Task{}, fmt.Errorf("load task %s parent %s returned %s: %w", id, *task.ParentTaskID, parent.ID, ErrInvalidEntity)
		}
		if err := ValidateTask(parent); err != nil {
			return Task{}, fmt.Errorf("load task %s parent %s: %w", id, *task.ParentTaskID, err)
		}
		parentList, err := s.loadList(ctx, parent.ListID)
		if err != nil {
			return Task{}, fmt.Errorf("load task %s parent %s list %s: %w", id, *task.ParentTaskID, parent.ListID, err)
		}
		if err := ValidateTaskProvider(parent, parentList); err != nil {
			return Task{}, fmt.Errorf("load task %s parent %s: %w", id, *task.ParentTaskID, err)
		}
		if err := ValidateParentProvider(task, parent); err != nil {
			return Task{}, fmt.Errorf("load task %s parent %s: %w", id, *task.ParentTaskID, err)
		}
	}
	return cloneTask(task), nil
}

func (s *Service) provider(ctx context.Context, id ProviderID) (Provider, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.providers == nil {
		return nil, ErrProviderRegistryUnavailable
	}
	provider, ok := s.providers.Get(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	if provider == nil {
		return nil, fmt.Errorf("provider registry returned nil for %s: %w", id, ErrInvalidEntity)
	}
	if provider.ID() != id {
		return nil, fmt.Errorf("provider registry returned %s for %s: %w", provider.ID(), id, ErrInvalidEntity)
	}
	return provider, nil
}

func (s *Service) requireRepository() error {
	if s == nil || s.repo == nil {
		return ErrRepositoryUnavailable
	}
	return nil
}

func (s *Service) newID(kind EntityType) (string, error) {
	if s == nil || s.ids == nil {
		return "", ErrInvalidEntity
	}
	id, err := s.ids.NewID(kind)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("%w: generated %s id is empty", ErrInvalidEntity, kind)
	}
	return id, nil
}

func (s *Service) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func requireCapability(provider Provider, capability Capability) error {
	if provider == nil {
		return &UnsupportedError{Capability: capability}
	}
	if supportsCapability(provider, capability) {
		return nil
	}
	return &UnsupportedError{ProviderID: provider.ID(), Capability: capability}
}

func requireTaskFeatures(provider Provider, dueDates, subtasks bool) error {
	if dueDates {
		if err := requireCapability(provider, CapabilityDueDates); err != nil {
			return err
		}
	}
	if subtasks {
		if err := requireCapability(provider, CapabilitySubtasks); err != nil {
			return err
		}
	}
	return nil
}

func stateFor(provider Provider) SyncState {
	if provider.Type() == ProviderTypeLocal {
		return SyncStateLocal
	}
	return SyncStatePending
}

func makeIntent(provider Provider, entityType EntityType, entityID string, operation OperationType, payload any) (*SyncIntent, error) {
	if provider.Type() == ProviderTypeLocal {
		return nil, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s sync intent: %w", entityType, err)
	}
	return &SyncIntent{
		ProviderID: provider.ID(),
		EntityType: entityType,
		EntityID:   entityID,
		Operation:  operation,
		Payload:    append([]byte(nil), data...),
	}, nil
}

func validateSavedSpace(expected, saved Space) error {
	if saved.ID != expected.ID || saved.ProviderID != expected.ProviderID {
		return fmt.Errorf("%w: repository changed space identity or ownership", ErrInvalidEntity)
	}
	return nil
}

func validateSavedList(expected, saved List) error {
	if saved.ID != expected.ID || saved.ProviderID != expected.ProviderID || saved.SpaceID != expected.SpaceID {
		return fmt.Errorf("%w: repository changed list identity, ownership, or parent", ErrInvalidEntity)
	}
	return nil
}

func validateSavedTask(expected, saved Task) error {
	if saved.ID != expected.ID || saved.ProviderID != expected.ProviderID || saved.ListID != expected.ListID {
		return fmt.Errorf("%w: repository changed task identity, ownership, or parent", ErrInvalidEntity)
	}
	return nil
}
