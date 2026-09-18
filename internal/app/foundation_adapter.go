package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kappke/task-tui/internal/domain"
	repositorypkg "github.com/kappke/task-tui/internal/repository"
)

// AtomicSpaceMutation and AtomicListMutation are optional extensions for a
// storage adapter. A remote create must use one of these extensions rather
// than falling back to a separate writer and queue call.
type AtomicSpaceMutation interface {
	CreateSpaceWithQueue(context.Context, domain.Space, *SyncIntent) (domain.Space, error)
}

type AtomicListMutation interface {
	CreateListWithQueue(context.Context, domain.List, *SyncIntent) (domain.List, error)
}

// FoundationMutationAdapter combines the small foundation stores with the
// atomic app mutation contract. Local writes use the ordinary store; remote
// task writes use repository.TaskMutationStore, and remote space/list writes
// require the explicit atomic extensions above.
type FoundationMutationAdapter struct {
	FoundationReaderAdapter
	Spaces repositorypkg.SpaceStore
	Lists  repositorypkg.ListStore
	Tasks  repositorypkg.TaskStore

	TaskMutations repositorypkg.TaskMutationStore
	SpaceAtomic   AtomicSpaceMutation
	ListAtomic    AtomicListMutation
}

func NewFoundationMutationAdapter(
	spaces repositorypkg.SpaceStore,
	lists repositorypkg.ListStore,
	tasks repositorypkg.TaskStore,
	taskMutations repositorypkg.TaskMutationStore,
	spaceAtomic AtomicSpaceMutation,
	listAtomic AtomicListMutation,
) *FoundationMutationAdapter {
	reader := FoundationReaderAdapter{
		Spaces: spaces,
		Lists:  lists,
		Tasks:  tasks,
	}
	if queries, ok := tasks.(repositorypkg.TaskSearcher); ok {
		reader.Queries = queries
	}
	return &FoundationMutationAdapter{
		FoundationReaderAdapter: reader,
		Spaces:                  spaces,
		Lists:                   lists,
		Tasks:                   tasks,
		TaskMutations:           taskMutations,
		SpaceAtomic:             spaceAtomic,
		ListAtomic:              listAtomic,
	}
}

func (a *FoundationMutationAdapter) CreateSpace(ctx context.Context, space domain.Space, intent *SyncIntent) (domain.Space, error) {
	if intent != nil {
		if a.SpaceAtomic == nil {
			return domain.Space{}, ErrAtomicMutationUnavailable
		}
		return a.SpaceAtomic.CreateSpaceWithQueue(ctx, space, intent)
	}
	if a.Spaces == nil {
		return domain.Space{}, ErrRepositoryUnavailable
	}
	return a.Spaces.Create(ctx, space)
}

func (a *FoundationMutationAdapter) CreateList(ctx context.Context, list domain.List, intent *SyncIntent) (domain.List, error) {
	if intent != nil {
		if a.ListAtomic == nil {
			return domain.List{}, ErrAtomicMutationUnavailable
		}
		return a.ListAtomic.CreateListWithQueue(ctx, list, intent)
	}
	if a.Lists == nil {
		return domain.List{}, ErrRepositoryUnavailable
	}
	return a.Lists.Create(ctx, list)
}

func (a *FoundationMutationAdapter) CreateTask(ctx context.Context, task domain.Task, intent *SyncIntent) (domain.Task, error) {
	if intent == nil {
		if a.Tasks == nil {
			return domain.Task{}, ErrRepositoryUnavailable
		}
		return a.Tasks.Create(ctx, task)
	}
	return a.applyTaskMutation(ctx, task, intent)
}

func (a *FoundationMutationAdapter) UpdateTask(ctx context.Context, task domain.Task, intent *SyncIntent) (domain.Task, error) {
	if intent == nil {
		if a.Tasks == nil {
			return domain.Task{}, ErrRepositoryUnavailable
		}
		return a.Tasks.Update(ctx, task)
	}
	return a.applyTaskMutation(ctx, task, intent)
}

func (a *FoundationMutationAdapter) MoveTask(ctx context.Context, task domain.Task, intent *SyncIntent) (domain.Task, error) {
	return a.UpdateTask(ctx, task, intent)
}

func (a *FoundationMutationAdapter) DeleteTask(ctx context.Context, task domain.Task, intent *SyncIntent) error {
	if intent == nil {
		if a.Tasks == nil {
			return ErrRepositoryUnavailable
		}
		if err := a.Tasks.Delete(ctx, task.ID); err != nil {
			return fmt.Errorf("delete task %s: %w", task.ID, err)
		}
		return nil
	}
	if _, err := a.applyTaskMutation(ctx, task, intent); err != nil {
		return fmt.Errorf("delete task %s with queue: %w", task.ID, err)
	}
	return nil
}

func (a *FoundationMutationAdapter) applyTaskMutation(ctx context.Context, task domain.Task, intent *SyncIntent) (domain.Task, error) {
	if a.TaskMutations == nil {
		return domain.Task{}, ErrAtomicMutationUnavailable
	}
	mutation := repositorypkg.TaskMutation{
		Task:      task,
		Operation: intent.Operation,
		Payload:   json.RawMessage(append([]byte(nil), intent.Payload...)),
	}
	updated, err := a.TaskMutations.ApplyTaskMutation(ctx, mutation)
	if err != nil {
		return domain.Task{}, fmt.Errorf("apply task mutation %s/%s: %w", task.ProviderID, task.ID, err)
	}
	return updated, nil
}

var _ CoreRepository = (*FoundationMutationAdapter)(nil)
