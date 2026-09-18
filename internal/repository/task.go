package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// TaskReader reads tasks from local state. ListByList is intended for normal
// local-first navigation and must not contact a remote provider.
type TaskReader interface {
	Get(ctx context.Context, id domain.TaskID) (domain.Task, error)
	ListByList(ctx context.Context, listID domain.ListID) ([]domain.Task, error)
	ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Task, error)
	GetByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.Task, error)
}

// TaskWriter persists local task state. Delete is addressed by the local task
// ID; provider ownership remains immutable and is checked by the
// implementation before deletion.
type TaskWriter interface {
	Create(ctx context.Context, task domain.Task) (domain.Task, error)
	Update(ctx context.Context, task domain.Task) (domain.Task, error)
	Delete(ctx context.Context, id domain.TaskID) error
}

// TaskStore is the composed task read/write contract.
type TaskStore interface {
	TaskReader
	TaskWriter
}

// TaskFilter describes local task search and filtering. A nil pointer means
// that criterion is not applied. Status is a provider-neutral string because
// providers may expose different status vocabularies.
type TaskFilter struct {
	Query       string
	ProviderID  *domain.ProviderID
	ProviderIDs []domain.ProviderID
	SpaceID     *domain.SpaceID
	ListID      *domain.ListID

	Status   string
	Statuses []string

	Priority    *domain.Priority
	MinPriority *domain.Priority
	MaxPriority *domain.Priority

	DueBefore *time.Time
	DueAfter  *time.Time
	Completed *bool
	SyncState *domain.SyncState

	Limit  int
	Offset int
}

// Validate checks IDs, enum values, and pagination constraints before a query
// reaches storage.
func (f TaskFilter) Validate() error {
	if f.ProviderID != nil {
		if err := f.ProviderID.Validate(); err != nil {
			return err
		}
	}
	for _, providerID := range f.ProviderIDs {
		if err := providerID.Validate(); err != nil {
			return err
		}
	}
	if f.SpaceID != nil {
		if err := f.SpaceID.Validate(); err != nil {
			return err
		}
	}
	if f.ListID != nil {
		if err := f.ListID.Validate(); err != nil {
			return err
		}
	}
	if f.Priority != nil {
		if err := f.Priority.Validate(); err != nil {
			return err
		}
	}
	if f.MinPriority != nil {
		if err := f.MinPriority.Validate(); err != nil {
			return err
		}
	}
	if f.MaxPriority != nil {
		if err := f.MaxPriority.Validate(); err != nil {
			return err
		}
	}
	if f.MinPriority != nil && f.MaxPriority != nil &&
		domain.ComparePriority(*f.MinPriority, *f.MaxPriority) > 0 {
		return fmt.Errorf("%w: minimum priority exceeds maximum priority", ErrInvalidFilter)
	}
	if f.SyncState != nil {
		if err := f.SyncState.Validate(); err != nil {
			return err
		}
	}
	if f.Limit < 0 || f.Offset < 0 {
		return fmt.Errorf("%w: limit and offset cannot be negative", ErrInvalidFilter)
	}
	return nil
}

// NormalizeUTC returns a copy whose time boundaries use UTC.
func (f TaskFilter) NormalizeUTC() TaskFilter {
	if f.DueBefore != nil {
		dueBefore := f.DueBefore.UTC()
		f.DueBefore = &dueBefore
	}
	if f.DueAfter != nil {
		dueAfter := f.DueAfter.UTC()
		f.DueAfter = &dueAfter
	}
	return f
}

// TaskView is a local search result with denormalized hierarchy labels. Task
// remains the authoritative entity and retains provider identity even when
// results span multiple providers.
type TaskView struct {
	Task domain.Task

	ProviderID   domain.ProviderID
	ProviderName string
	SpaceID      domain.SpaceID
	SpaceName    string
	ListID       domain.ListID
	ListName     string
}

// TaskSearcher searches only the local repository and never requires a
// provider API request.
type TaskSearcher interface {
	Search(ctx context.Context, filter TaskFilter) ([]TaskView, error)
}

// TaskSearchReader is a descriptive alias for consumers that use searching as
// a read-only repository capability.
type TaskSearchReader = TaskSearcher
