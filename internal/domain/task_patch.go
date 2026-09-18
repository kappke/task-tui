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
