package app

import (
	"fmt"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/repository"
)

// Application code uses the foundation domain types directly. These aliases
// keep the app API convenient without introducing a second representation that
// would need conversion at every TUI or storage boundary.
type ProviderID = domain.ProviderID
type SpaceID = domain.SpaceID
type ListID = domain.ListID
type TaskID = domain.TaskID
type OperationID = domain.OperationID

type ProviderType = domain.ProviderType
type EntityType = domain.EntityType
type SyncState = domain.SyncState
type OperationType = domain.OperationType
type Priority = domain.Priority

const (
	ProviderTypeLocal   = domain.ProviderTypeLocal
	ProviderTypeClickUp = domain.ProviderTypeClickUp

	EntityTypeSpace = domain.EntityTypeSpace
	EntityTypeList  = domain.EntityTypeList
	EntityTypeTask  = domain.EntityTypeTask

	SyncStateSynced   = domain.SyncStateSynced
	SyncStatePending  = domain.SyncStatePending
	SyncStateSyncing  = domain.SyncStateSyncing
	SyncStateConflict = domain.SyncStateConflict
	SyncStateFailed   = domain.SyncStateFailed
	SyncStateLocal    = domain.SyncStateLocal

	OperationCreate                 = domain.OperationCreate
	OperationUpdate                 = domain.OperationUpdate
	OperationDelete                 = domain.OperationDelete
	OperationComplete OperationType = domain.OperationType("update")
	OperationMove     OperationType = domain.OperationType("update")

	PriorityNone   = domain.PriorityNone
	PriorityLow    = domain.PriorityLow
	PriorityNormal = domain.PriorityNormal
	PriorityHigh   = domain.PriorityHigh
	PriorityUrgent = domain.PriorityUrgent

	StatusTodo = "todo"
	StatusDone = "done"
)

type ProviderInfo = domain.Provider
type Space = domain.Space
type List = domain.List
type Task = domain.Task
type TaskPatch = domain.TaskPatch
type TaskCreatePayload = domain.TaskCreatePayload
type TaskUpdatePayload = domain.TaskUpdatePayload

// SyncIntent is the small app-to-repository queue intent. The repository owns
// assigning queue row identity and lifecycle fields; the app supplies the
// provider-scoped mutation that must be committed with the local write.
type SyncIntent struct {
	ProviderID ProviderID    `json:"provider_id"`
	EntityType EntityType    `json:"entity_type"`
	EntityID   string        `json:"entity_id"`
	Operation  OperationType `json:"operation"`
	Payload    []byte        `json:"payload,omitempty"`
}

type QueueIntent = SyncIntent

type SyncOperation = domain.SyncOperation

// OperationRecord adapts an app intent to the foundation queue row shape. A
// repository should call it inside the same transaction as the local write and
// provide its own durable operation ID.
func (intent SyncIntent) OperationRecord(id OperationID, now time.Time) domain.SyncOperation {
	now = now.UTC()
	return domain.SyncOperation{
		ID:            id,
		ProviderID:    intent.ProviderID,
		EntityType:    intent.EntityType,
		EntityID:      intent.EntityID,
		Operation:     intent.Operation,
		Payload:       append([]byte(nil), intent.Payload...),
		Status:        domain.SyncStatusPending,
		CreatedAt:     now,
		NextAttemptAt: &now,
	}
}

type CreateSpaceInput struct {
	ProviderID ProviderID
	Name       string
}

type CreateListInput struct {
	ProviderID ProviderID
	SpaceID    SpaceID
	Name       string
}

type CreateTaskInput struct {
	ProviderID   ProviderID
	ListID       ListID
	ParentTaskID *TaskID
	Title        string
	Description  string
	Status       string
	Priority     Priority
	DueAt        *time.Time
	CompletedAt  *time.Time
}

// TaskFilter and TaskSearchResult are the local repository's normalized query
// contracts. TaskView retains provider identity for aggregated results.
type TaskFilter = repository.TaskFilter
type TaskSearchResult = repository.TaskView

type TransferResult struct {
	Source        Task
	Destination   Task
	SourceDeleted bool
}

func ValidateSpace(space Space) error {
	if space.ID.IsZero() {
		return fmt.Errorf("%w: space id is empty", ErrInvalidEntity)
	}
	if space.ProviderID.IsZero() {
		return fmt.Errorf("%w: space %s has no provider", ErrInvalidEntity, space.ID)
	}
	return nil
}

func ValidateList(list List) error {
	if list.ID.IsZero() {
		return fmt.Errorf("%w: list id is empty", ErrInvalidEntity)
	}
	if list.ProviderID.IsZero() {
		return fmt.Errorf("%w: list %s has no provider", ErrInvalidEntity, list.ID)
	}
	if list.SpaceID.IsZero() {
		return fmt.Errorf("%w: list %s has no space", ErrInvalidEntity, list.ID)
	}
	return nil
}

func ValidateTask(task Task) error {
	if task.ID.IsZero() {
		return fmt.Errorf("%w: task id is empty", ErrInvalidEntity)
	}
	if task.ProviderID.IsZero() {
		return fmt.Errorf("%w: task %s has no provider", ErrInvalidEntity, task.ID)
	}
	if task.ListID.IsZero() {
		return fmt.Errorf("%w: task %s has no list", ErrInvalidEntity, task.ID)
	}
	return nil
}

func ValidateListProvider(list List, space Space) error {
	if err := domain.ValidateListProvider(list, space); err != nil {
		return err
	}
	return nil
}

func ValidateTaskProvider(task Task, list List) error {
	if err := domain.ValidateTaskProvider(task, list); err != nil {
		return err
	}
	return nil
}

func ValidateParentProvider(task Task, parent Task) error {
	if task.ProviderID != parent.ProviderID {
		return fmt.Errorf("%w: %w: task %s belongs to %s, parent %s belongs to %s", ErrInvalidParent, ErrProviderMismatch, task.ID, task.ProviderID, parent.ID, parent.ProviderID)
	}
	if task.ID != "" && task.ID == parent.ID {
		return fmt.Errorf("%w: task %s cannot be its own parent", ErrInvalidParent, task.ID)
	}
	return nil
}

func taskPatchHasChanges(patch TaskPatch) bool {
	return patch.ListID != nil || patch.ParentTaskID != nil || patch.Title != nil ||
		patch.Description != nil || patch.Status != nil || patch.Priority != nil ||
		patch.DueAt != nil || patch.CompletedAt != nil || patch.ClearParentTask ||
		patch.ClearDueAt || patch.ClearCompletedAt
}

func taskPatchPayloadFrom(patch TaskPatch) TaskUpdatePayload {
	payload := patch
	payload.ListID = cloneListID(patch.ListID)
	payload.ParentTaskID = cloneTaskID(patch.ParentTaskID)
	payload.Title = cloneString(patch.Title)
	payload.Description = cloneString(patch.Description)
	payload.Status = cloneString(patch.Status)
	payload.Priority = clonePriority(patch.Priority)
	payload.DueAt = cloneTime(patch.DueAt)
	payload.CompletedAt = cloneTime(patch.CompletedAt)
	return payload
}

func taskCreatePayloadFrom(task Task) TaskCreatePayload {
	payload := domain.NewTaskPayload(task)
	payload.ListID = task.ListID
	payload.ParentTaskID = cloneTaskID(task.ParentTaskID)
	payload.DueAt = cloneTime(task.DueAt)
	payload.CompletedAt = cloneTime(task.CompletedAt)
	return payload
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func clonePriority(value *Priority) *Priority {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTaskID(value *TaskID) *TaskID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneListID(value *ListID) *ListID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func cloneSpace(space Space) Space {
	space.RemoteID = cloneString(space.RemoteID)
	space.RemoteUpdatedAt = cloneTime(space.RemoteUpdatedAt)
	return space
}

func cloneList(list List) List {
	list.RemoteID = cloneString(list.RemoteID)
	list.RemoteUpdatedAt = cloneTime(list.RemoteUpdatedAt)
	return list
}

func cloneTask(task Task) Task {
	task.RemoteID = cloneString(task.RemoteID)
	task.ParentTaskID = cloneTaskID(task.ParentTaskID)
	task.DueAt = cloneTime(task.DueAt)
	task.CompletedAt = cloneTime(task.CompletedAt)
	task.RemoteUpdatedAt = cloneTime(task.RemoteUpdatedAt)
	return task
}

func cloneTasks(tasks []Task) []Task {
	if tasks == nil {
		return nil
	}
	cloned := make([]Task, len(tasks))
	for i, task := range tasks {
		cloned[i] = cloneTask(task)
	}
	return cloned
}

func cloneSearchResults(results []TaskSearchResult) []TaskSearchResult {
	if results == nil {
		return nil
	}
	cloned := make([]TaskSearchResult, len(results))
	for i, result := range results {
		cloned[i] = result
		cloned[i].Task = cloneTask(result.Task)
	}
	return cloned
}
