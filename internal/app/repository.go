package app

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
	repositorypkg "github.com/kappke/task-tui/internal/repository"
)

// These app-facing read contracts use distinct method names because the
// foundation repository package intentionally keeps each entity store small
// with a generic Get method. The adapter below bridges those stores without
// making the service know about a concrete storage implementation.
type SpaceReader interface {
	GetSpace(ctx context.Context, id domain.SpaceID) (domain.Space, error)
}

type ListReader interface {
	GetList(ctx context.Context, id domain.ListID) (domain.List, error)
}

type TaskReader interface {
	GetTask(ctx context.Context, id domain.TaskID) (domain.Task, error)
	ListTasks(ctx context.Context, listID domain.ListID) ([]domain.Task, error)
}

// TaskQueryRepository is the local-only search contract.
type TaskQueryRepository interface {
	SearchTasks(ctx context.Context, query string) ([]TaskSearchResult, error)
	FilterTasks(ctx context.Context, filter TaskFilter) ([]TaskSearchResult, error)
}

const DefaultSearchLimit = 100

// FoundationReaderAdapter adapts the foundation repository read contracts to
// the app's distinct entity-reader names. It only delegates to local stores.
type FoundationReaderAdapter struct {
	Spaces  repositorypkg.SpaceReader
	Lists   repositorypkg.ListReader
	Tasks   repositorypkg.TaskReader
	Queries repositorypkg.TaskSearcher
}

func (a FoundationReaderAdapter) GetSpace(ctx context.Context, id domain.SpaceID) (domain.Space, error) {
	if a.Spaces == nil {
		return domain.Space{}, ErrRepositoryUnavailable
	}
	return a.Spaces.Get(ctx, id)
}

func (a FoundationReaderAdapter) GetList(ctx context.Context, id domain.ListID) (domain.List, error) {
	if a.Lists == nil {
		return domain.List{}, ErrRepositoryUnavailable
	}
	return a.Lists.Get(ctx, id)
}

func (a FoundationReaderAdapter) GetTask(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	if a.Tasks == nil {
		return domain.Task{}, ErrRepositoryUnavailable
	}
	return a.Tasks.Get(ctx, id)
}

func (a FoundationReaderAdapter) ListTasks(ctx context.Context, listID domain.ListID) ([]domain.Task, error) {
	if a.Tasks == nil {
		return nil, ErrRepositoryUnavailable
	}
	return a.Tasks.ListByList(ctx, listID)
}

func (a FoundationReaderAdapter) SearchTasks(ctx context.Context, query string) ([]TaskSearchResult, error) {
	if a.Queries == nil {
		return nil, ErrQueryUnavailable
	}
	return a.Queries.Search(ctx, repositorypkg.TaskFilter{Query: query, Limit: DefaultSearchLimit})
}

func (a FoundationReaderAdapter) FilterTasks(ctx context.Context, filter TaskFilter) ([]TaskSearchResult, error) {
	if a.Queries == nil {
		return nil, ErrQueryUnavailable
	}
	return a.Queries.Search(ctx, filter)
}

// MutationRepository is the app's atomic write facade. The foundation entity
// writers intentionally stay small; an implementation of this facade adapts
// them to a transaction that also inserts the optional queue intent.
type MutationRepository interface {
	CreateSpace(ctx context.Context, space domain.Space, intent *SyncIntent) (domain.Space, error)
	CreateList(ctx context.Context, list domain.List, intent *SyncIntent) (domain.List, error)
	CreateTask(ctx context.Context, task domain.Task, intent *SyncIntent) (domain.Task, error)
	UpdateTask(ctx context.Context, task domain.Task, intent *SyncIntent) (domain.Task, error)
	MoveTask(ctx context.Context, task domain.Task, intent *SyncIntent) (domain.Task, error)
	DeleteTask(ctx context.Context, task domain.Task, intent *SyncIntent) error
}

type CoreRepository interface {
	SpaceReader
	ListReader
	TaskReader
	MutationRepository
}

type Repository interface {
	CoreRepository
	TaskQueryRepository
}
