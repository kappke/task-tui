package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kappke/task-tui/internal/app"
	"github.com/kappke/task-tui/internal/domain"
	foundationsync "github.com/kappke/task-tui/internal/sync"
)

func (r *Runtime) providerID(id domain.ProviderID) domain.ProviderID {
	if strings.TrimSpace(string(id)) != "" {
		return id
	}
	return domain.ProviderID(r.config.App.DefaultProvider)
}

// ListProviders returns the configured provider instances.
func (r *Runtime) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	if r == nil || r.cliStore == nil {
		return nil, errors.New("list providers: runtime is not CLI-capable")
	}
	return r.cliStore.ListProviders(ctx)
}

// ListSpaces returns cached spaces owned by one provider instance.
func (r *Runtime) ListSpaces(ctx context.Context, providerID domain.ProviderID) ([]domain.Space, error) {
	if r == nil || r.cliStore == nil {
		return nil, errors.New("list spaces: runtime is not CLI-capable")
	}
	return r.cliStore.ListSpacesByProvider(ctx, r.providerID(providerID))
}

// ListLists returns cached lists owned by one provider, optionally beneath a
// specific space.
func (r *Runtime) ListLists(ctx context.Context, providerID domain.ProviderID, spaceID domain.SpaceID) ([]domain.List, error) {
	if r == nil || r.cliStore == nil {
		return nil, errors.New("list lists: runtime is not CLI-capable")
	}
	providerID = r.providerID(providerID)
	if spaceID == "" {
		return r.cliStore.ListListsByProvider(ctx, providerID)
	}
	space, err := r.resolveSpace(ctx, providerID, spaceID)
	if err != nil {
		return nil, err
	}
	return r.cliStore.ListBySpace(ctx, space.ID)
}

// ListTasks returns cached tasks in one provider-owned list.
func (r *Runtime) ListTasks(ctx context.Context, providerID domain.ProviderID, listID domain.ListID) ([]domain.Task, error) {
	if r == nil || r.cliStore == nil {
		return nil, errors.New("list tasks: runtime is not CLI-capable")
	}
	list, err := r.resolveList(ctx, r.providerID(providerID), listID)
	if err != nil {
		return nil, err
	}
	return r.cliStore.ListByList(ctx, list.ID)
}

// GetTask returns a cached task by local ID or provider-scoped remote ID.
func (r *Runtime) GetTask(ctx context.Context, providerID domain.ProviderID, taskID string) (domain.Task, error) {
	if r == nil || r.cliStore == nil {
		return domain.Task{}, errors.New("get task: runtime is not CLI-capable")
	}
	providerID = r.providerID(providerID)
	task, err := r.cliStore.GetTaskByProvider(ctx, providerID, domain.TaskID(taskID))
	if err == nil {
		return task, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Task{}, err
	}
	return r.cliStore.GetTaskByRemoteID(ctx, providerID, taskID)
}

// CreateTask creates local state and, for remote providers, queues the remote
// create atomically. It never performs network I/O itself.
func (r *Runtime) CreateTask(ctx context.Context, input app.CreateTaskInput) (domain.Task, error) {
	if r == nil || r.cliService == nil || r.cliStore == nil {
		return domain.Task{}, errors.New("create task: runtime is not CLI-capable")
	}
	input.ProviderID = r.providerID(input.ProviderID)
	list, err := r.resolveList(ctx, input.ProviderID, input.ListID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("resolve task list: %w", err)
	}
	input.ListID = list.ID
	return r.cliService.CreateTask(ctx, input)
}

// SyncOnce performs one bounded synchronization cycle. It does not start a
// persistent worker loop. A provider ID limits the cycle to that provider.
func (r *Runtime) SyncOnce(ctx context.Context, providerID domain.ProviderID) error {
	if r == nil || r.cliEngine == nil || r.cliProviders == nil {
		return errors.New("sync: runtime is not CLI-capable")
	}
	if providerID != "" {
		configured, ok := r.cliProviders.Get(providerID)
		if !ok {
			return fmt.Errorf("sync provider %s: %w", providerID, domain.ErrNotFound)
		}
		if configured.Type() == domain.ProviderTypeLocal {
			return nil
		}
		worker, ok := r.cliEngine.Worker(foundationsync.ProviderID(providerID))
		if !ok {
			return fmt.Errorf("sync provider %s: %w", providerID, domain.ErrNotFound)
		}
		return worker.SyncOnce(ctx)
	}
	return r.cliEngine.SyncOnce(ctx)
}

func (r *Runtime) resolveSpace(ctx context.Context, providerID domain.ProviderID, id domain.SpaceID) (domain.Space, error) {
	space, err := r.cliStore.GetSpaceByProvider(ctx, providerID, id)
	if err == nil {
		return space, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Space{}, err
	}
	return r.cliStore.GetSpaceByRemoteID(ctx, providerID, string(id))
}

func (r *Runtime) resolveList(ctx context.Context, providerID domain.ProviderID, id domain.ListID) (domain.List, error) {
	list, err := r.cliStore.GetListByProvider(ctx, providerID, id)
	if err == nil {
		return list, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.List{}, err
	}
	return r.cliStore.GetListByRemoteID(ctx, providerID, string(id))
}
