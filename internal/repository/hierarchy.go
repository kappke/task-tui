package repository

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
)

// HierarchyReader resolves local parent records for ownership validation and
// rendering. A missing parent is reported with ErrNotFound; callers should not
// infer a remote lookup from that result.
type HierarchyReader interface {
	GetSpaceForList(ctx context.Context, listID domain.ListID) (domain.Space, error)
	GetListForTask(ctx context.Context, taskID domain.TaskID) (domain.List, error)
	GetParentTask(ctx context.Context, taskID domain.TaskID) (domain.Task, error)
}
