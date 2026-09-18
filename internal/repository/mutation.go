package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kappke/task-tui/internal/domain"
)

// TaskMutation is the complete local change that must be committed together
// with its durable synchronization operation.
type TaskMutation struct {
	Task      domain.Task
	Operation domain.OperationType
	Payload   json.RawMessage
}

// Validate checks that a mutation addresses a task and carries a valid task
// model. Payload may be nil when the operation has no additional fields.
func (m TaskMutation) Validate() error {
	if err := m.Task.Validate(); err != nil {
		return err
	}
	if err := m.Operation.Validate(); err != nil {
		return err
	}
	return ValidateTaskMutationPayload(m.Payload)
}

// TaskMutationStore atomically applies a local task mutation and records its
// remote synchronization operation. An implementation must roll back both
// effects if either part fails, so a local change cannot exist without its
// durable queue entry.
type TaskMutationStore interface {
	ApplyTaskMutation(ctx context.Context, mutation TaskMutation) (domain.Task, error)
}

// ValidateTaskMutationPayload is a small helper for storage implementations
// that want to reject malformed JSON before opening a transaction.
func ValidateTaskMutationPayload(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return fmt.Errorf("%w: invalid task mutation payload: %v", domain.ErrInvalidEnum, err)
	}
	return nil
}
