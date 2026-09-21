package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// TaskPatch contains only mutable task fields. Pointer fields distinguish an
// omitted update from an update to a zero value and therefore marshal safely
// as a JSON patch.
type TaskPatch struct {
	ListID           *ListID    `json:"list_id,omitempty"`
	ParentTaskID     *TaskID    `json:"parent_task_id,omitempty"`
	Assignee         *string    `json:"assignee,omitempty"`
	Title            *string    `json:"title,omitempty"`
	Description      *string    `json:"description,omitempty"`
	Status           *string    `json:"status,omitempty"`
	Priority         *Priority  `json:"priority,omitempty"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	ClearParentTask  bool       `json:"clear_parent_task,omitempty"`
	ClearDueAt       bool       `json:"clear_due_at,omitempty"`
	ClearCompletedAt bool       `json:"clear_completed_at,omitempty"`
}

// TaskPayload contains the provider-neutral mutable fields needed to create a
// task. Provider and remote identity are deliberately omitted because they are
// assigned by the operation and destination provider.
type TaskPayload struct {
	ListID       ListID     `json:"list_id"`
	ParentTaskID *TaskID    `json:"parent_task_id,omitempty"`
	Assignee     string     `json:"assignee,omitempty"`
	Title        string     `json:"title"`
	Description  string     `json:"description,omitempty"`
	Status       string     `json:"status,omitempty"`
	Priority     Priority   `json:"priority"`
	DueAt        *time.Time `json:"due_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// TaskCreatePayload is the explicit name used by create operations.
type TaskCreatePayload = TaskPayload

// TaskUpdatePayload is the explicit name used by update operations.
type TaskUpdatePayload = TaskPatch

// TaskDeletePayload identifies the task affected by a delete operation.
type TaskDeletePayload struct {
	TaskID TaskID `json:"task_id"`
}

// SpaceMutationPayload is the versioned queue payload for space writes.
type SpaceMutationPayload struct {
	Version    int        `json:"version"`
	ProviderID ProviderID `json:"provider_id"`
	SpaceID    SpaceID    `json:"space_id"`
	Snapshot   *Space     `json:"snapshot"`
}

// ListMutationPayload is the versioned queue payload for list writes.
type ListMutationPayload struct {
	Version    int        `json:"version"`
	ProviderID ProviderID `json:"provider_id"`
	ListID     ListID     `json:"list_id"`
	Snapshot   *List      `json:"snapshot"`
}

const HierarchyMutationPayloadVersion = 1

const (
	SpaceMutationPayloadVersion = HierarchyMutationPayloadVersion
	ListMutationPayloadVersion  = HierarchyMutationPayloadVersion
)

func NewSpaceCreateMutationPayload(space Space) SpaceMutationPayload {
	return SpaceMutationPayload{
		Version: HierarchyMutationPayloadVersion, ProviderID: space.ProviderID,
		SpaceID: space.ID, Snapshot: cloneSpaceSnapshot(space),
	}
}

func NewListCreateMutationPayload(list List) ListMutationPayload {
	return ListMutationPayload{
		Version: HierarchyMutationPayloadVersion, ProviderID: list.ProviderID,
		ListID: list.ID, Snapshot: cloneListSnapshot(list),
	}
}

func cloneSpaceSnapshot(space Space) *Space {
	space.RemoteID = cloneString(space.RemoteID)
	space.RemoteUpdatedAt = cloneTime(space.RemoteUpdatedAt)
	return &space
}

func cloneListSnapshot(list List) *List {
	list.RemoteID = cloneString(list.RemoteID)
	list.RemoteUpdatedAt = cloneTime(list.RemoteUpdatedAt)
	return &list
}

// TaskMutationPayloadVersion identifies the normalized durable task payload.
const TaskMutationPayloadVersion = 1

// TaskMutationPayload is the queue contract shared by application, storage,
// and synchronization. Snapshot is the provider-ready task state; Patch keeps
// the update semantics explicit and lets consumers distinguish an update from
// a replacement. Delete payloads retain Snapshot so a deleted task can still
// be sent when its local row is gone.
type TaskMutationPayload struct {
	Version    int        `json:"version"`
	ProviderID ProviderID `json:"provider_id"`
	TaskID     TaskID     `json:"task_id"`
	Snapshot   *Task      `json:"snapshot,omitempty"`
	RemoteID   *string    `json:"remote_id,omitempty"`
	*TaskPayload
	*TaskPatch
}

func NewTaskCreateMutationPayload(task Task) TaskMutationPayload {
	payload := NewTaskPayload(task)
	return TaskMutationPayload{
		Version:     TaskMutationPayloadVersion,
		ProviderID:  task.ProviderID,
		TaskID:      task.ID,
		Snapshot:    cloneTaskPayloadSnapshot(task),
		TaskPayload: &payload,
	}
}

func NewTaskUpdateMutationPayload(task Task, patch TaskPatch) TaskMutationPayload {
	return TaskMutationPayload{
		Version:    TaskMutationPayloadVersion,
		ProviderID: task.ProviderID,
		TaskID:     task.ID,
		Snapshot:   cloneTaskPayloadSnapshot(task),
		TaskPatch:  &patch,
	}
}

func NewTaskDeleteMutationPayload(task Task) TaskMutationPayload {
	remoteID := cloneString(task.RemoteID)
	return TaskMutationPayload{
		Version:    TaskMutationPayloadVersion,
		ProviderID: task.ProviderID,
		TaskID:     task.ID,
		Snapshot:   cloneTaskPayloadSnapshot(task),
		RemoteID:   remoteID,
	}
}

// MarshalJSON keeps the normalized metadata alongside the compact create or
// patch fields. Flattening the operation fields makes the wire format useful
// to simple payload inspectors without creating a second contract.
func (p TaskMutationPayload) MarshalJSON() ([]byte, error) {
	value := map[string]any{
		"version":     p.Version,
		"provider_id": p.ProviderID,
		"task_id":     p.TaskID,
	}
	if p.Snapshot != nil {
		value["snapshot"] = p.Snapshot
	}
	if p.RemoteID != nil {
		value["remote_id"] = p.RemoteID
	}
	if p.TaskPayload != nil {
		mergeJSONFields(value, p.TaskPayload)
	}
	if p.TaskPatch != nil {
		mergeJSONFields(value, p.TaskPatch)
	}
	return json.Marshal(value)
}

func (p *TaskMutationPayload) UnmarshalJSON(data []byte) error {
	type metadata struct {
		Version    int        `json:"version"`
		ProviderID ProviderID `json:"provider_id"`
		TaskID     TaskID     `json:"task_id"`
		Snapshot   *Task      `json:"snapshot,omitempty"`
		RemoteID   *string    `json:"remote_id,omitempty"`
	}
	var header metadata
	if err := json.Unmarshal(data, &header); err != nil {
		return err
	}
	*p = TaskMutationPayload{
		Version: header.Version, ProviderID: header.ProviderID, TaskID: header.TaskID,
		Snapshot: header.Snapshot, RemoteID: header.RemoteID,
	}
	var fields TaskPatch
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields.ListID != nil || fields.ParentTaskID != nil || fields.Assignee != nil || fields.Title != nil ||
		fields.Description != nil || fields.Status != nil || fields.Priority != nil || fields.DueAt != nil ||
		fields.CompletedAt != nil || fields.ClearParentTask || fields.ClearDueAt || fields.ClearCompletedAt {
		p.TaskPatch = &fields
	}
	return nil
}

func mergeJSONFields(target map[string]any, fields any) {
	data, err := json.Marshal(fields)
	if err != nil {
		return
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return
	}
	for key, value := range values {
		target[key] = value
	}
}

func cloneTaskPayloadSnapshot(task Task) *Task {
	snapshot := cloneTaskSnapshot(task)
	return &snapshot
}

func cloneTaskSnapshot(task Task) Task {
	task.RemoteID = cloneString(task.RemoteID)
	task.ParentTaskID = cloneTaskID(task.ParentTaskID)
	task.DueAt = cloneTime(task.DueAt)
	task.CompletedAt = cloneTime(task.CompletedAt)
	return task
}

func cloneString(value *string) *string {
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

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

// NewTaskPayload creates a provider-neutral create payload from a task.
func NewTaskPayload(task Task) TaskPayload {
	return TaskPayload{
		ListID:       task.ListID,
		ParentTaskID: task.ParentTaskID,
		Assignee:     task.Assignee,
		Title:        task.Title,
		Description:  task.Description,
		Status:       task.Status,
		Priority:     task.Priority,
		DueAt:        task.DueAt,
		CompletedAt:  task.CompletedAt,
	}
}

// Validate checks fields that carry domain identity or enum values.
func (p TaskPayload) Validate() error {
	if err := p.ListID.Validate(); err != nil {
		return err
	}
	if p.ParentTaskID != nil {
		if err := p.ParentTaskID.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidParent, err)
		}
	}
	return p.Priority.Validate()
}

// Validate checks fields that carry domain identity or enum values. Empty
// strings remain valid patch values because clearing a description or status
// can be a meaningful provider-supported mutation.
func (p TaskPatch) Validate() error {
	if p.ListID != nil {
		if err := p.ListID.Validate(); err != nil {
			return err
		}
	}
	if p.ParentTaskID != nil {
		if err := p.ParentTaskID.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidParent, err)
		}
	}
	if p.Priority != nil {
		if err := p.Priority.Validate(); err != nil {
			return err
		}
	}
	if p.ClearParentTask && p.ParentTaskID != nil {
		return fmt.Errorf("%w: parent task cannot be set and cleared together", ErrInvalidParent)
	}
	if p.ClearDueAt && p.DueAt != nil {
		return fmt.Errorf("%w: due date cannot be set and cleared together", ErrInvalidParent)
	}
	if p.ClearCompletedAt && p.CompletedAt != nil {
		return fmt.Errorf("%w: completion time cannot be set and cleared together", ErrInvalidParent)
	}
	return nil
}

// Apply returns a copy of task with the patch applied. Provider ownership is
// never a patchable field; callers must validate a changed ListID against its
// destination hierarchy.
func (p TaskPatch) Apply(task Task) (Task, error) {
	if err := p.Validate(); err != nil {
		return Task{}, err
	}
	if p.ListID != nil {
		task.ListID = *p.ListID
	}
	if p.ClearParentTask {
		task.ParentTaskID = nil
	} else if p.ParentTaskID != nil {
		parentID := *p.ParentTaskID
		task.ParentTaskID = &parentID
	}
	if p.Assignee != nil {
		task.Assignee = *p.Assignee
	}
	if p.Title != nil {
		task.Title = *p.Title
	}
	if p.Description != nil {
		task.Description = *p.Description
	}
	if p.Status != nil {
		task.Status = *p.Status
	}
	if p.Priority != nil {
		task.Priority = *p.Priority
	}
	if p.ClearDueAt {
		task.DueAt = nil
	} else if p.DueAt != nil {
		dueAt := p.DueAt.UTC()
		task.DueAt = &dueAt
	}
	if p.ClearCompletedAt {
		task.CompletedAt = nil
	} else if p.CompletedAt != nil {
		completedAt := p.CompletedAt.UTC()
		task.CompletedAt = &completedAt
	}
	return task, nil
}

// MarshalTaskPayload encodes a typed task payload for a queue operation.
func MarshalTaskPayload(payload any) (json.RawMessage, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal task payload: %w", err)
	}
	return json.RawMessage(data), nil
}
