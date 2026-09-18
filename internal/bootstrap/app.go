package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kappke/task-tui/internal/command"
)

// Application handles normalized commands against local state. It never calls
// a remote provider directly; remote work is represented by queue rows and
// consumed by SyncEngine.
type Application struct {
	repo            *Repository
	providers       *Registry
	sync            *SyncEngine
	defaultProvider ProviderID

	mu        sync.RWMutex
	accepting bool
}

// NewApplication constructs a command handler.
func NewApplication(repo *Repository, providers *Registry, syncEngine *SyncEngine, defaultProvider ProviderID) (*Application, error) {
	if repo == nil {
		return nil, errors.New("new application: nil repository")
	}
	if providers == nil {
		return nil, errors.New("new application: nil provider registry")
	}
	if strings.TrimSpace(string(defaultProvider)) == "" {
		defaultProvider = ProviderID("local")
	}
	if _, ok := providers.Get(defaultProvider); !ok {
		return nil, fmt.Errorf("new application: default provider %s: %w", defaultProvider, ErrNotFound)
	}
	return &Application{repo: repo, providers: providers, sync: syncEngine, defaultProvider: defaultProvider, accepting: true}, nil
}

// Handle executes a command using local persistence.
func (a *Application) Handle(ctx context.Context, c command.Command) (command.Event, error) {
	if ctx == nil {
		return command.Event{}, errors.New("handle command: nil context")
	}
	a.mu.RLock()
	accepting := a.accepting
	a.mu.RUnlock()
	if !accepting && c.Kind != command.KindQuit {
		return command.Event{}, errors.New("application is shutting down")
	}
	if err := c.Validate(); err != nil {
		return command.Event{}, err
	}
	if err := ctx.Err(); err != nil {
		return command.Event{}, err
	}
	switch c.Kind {
	case command.KindQuit:
		return command.Event{Kind: command.EventQuit}, nil
	case command.KindRefresh:
		if a.sync != nil {
			for _, provider := range a.providers.All() {
				if provider.Capabilities().RemoteSync {
					if err := a.sync.Trigger(provider.ID()); err != nil && !errors.Is(err, ErrNotFound) {
						return command.Event{}, fmt.Errorf("refresh provider %s: %w", provider.ID(), err)
					}
				}
			}
		}
		return command.Event{Kind: command.EventRefresh}, nil
	case command.KindSearch:
		if _, err := a.repo.SearchTasks(ctx, c.Query); err != nil {
			return command.Event{}, err
		}
		return command.Event{Kind: command.EventSearchResults, Query: c.Query}, nil
	case command.KindCreateSpace:
		return command.Event{Kind: command.EventChanged}, a.createSpace(ctx, c)
	case command.KindCreateList:
		return command.Event{Kind: command.EventChanged}, a.createList(ctx, c)
	case command.KindCreateTask:
		return command.Event{Kind: command.EventChanged}, a.createTask(ctx, c)
	case command.KindUpdateTask:
		return command.Event{Kind: command.EventChanged}, a.updateTask(ctx, c)
	case command.KindCompleteTask:
		return command.Event{Kind: command.EventChanged}, a.completeTask(ctx, c)
	case command.KindDeleteTask:
		return command.Event{Kind: command.EventChanged}, a.deleteTask(ctx, c)
	case command.KindMoveTask:
		return command.Event{Kind: command.EventChanged}, a.moveTask(ctx, c)
	case command.KindTransferTask:
		return command.Event{}, fmt.Errorf("transfer task: %w", ErrUnsupported)
	default:
		return command.Event{}, fmt.Errorf("handle command: unknown kind %q", c.Kind)
	}
}

// StopAccepting prevents new mutations while shutdown persists UI state.
func (a *Application) StopAccepting() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.accepting = false
	a.mu.Unlock()
}

// View returns the current local snapshot for the TUI.
func (a *Application) View(ctx context.Context) (View, error) {
	if a == nil || a.repo == nil {
		return View{}, errors.New("application is unavailable")
	}
	return a.repo.Snapshot(ctx)
}

func (a *Application) provider(id string) (Provider, error) {
	providerID := ProviderID(id)
	if strings.TrimSpace(id) == "" {
		providerID = a.defaultProvider
	}
	provider, ok := a.providers.Get(providerID)
	if !ok {
		return nil, fmt.Errorf("provider %s: %w", providerID, ErrNotFound)
	}
	if !provider.Record().Enabled {
		return nil, fmt.Errorf("provider %s is disabled", providerID)
	}
	return provider, nil
}

func (a *Application) createSpace(ctx context.Context, c command.Command) error {
	provider, err := a.provider(c.ProviderID)
	if err != nil {
		return err
	}
	if provider.Type() != ProviderTypeLocal {
		return fmt.Errorf("create space: provider %s: %w", provider.ID(), ErrUnsupported)
	}
	now := time.Now().UTC()
	return a.repo.CreateSpace(ctx, Space{
		ID:         SpaceID(newID("space")),
		ProviderID: provider.ID(),
		Name:       strings.TrimSpace(c.Title),
		SyncState:  SyncStateLocal,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

func (a *Application) createList(ctx context.Context, c command.Command) error {
	provider, err := a.provider(c.ProviderID)
	if err != nil {
		return err
	}
	if provider.Type() != ProviderTypeLocal {
		return fmt.Errorf("create list: provider %s: %w", provider.ID(), ErrUnsupported)
	}
	now := time.Now().UTC()
	return a.repo.CreateList(ctx, List{
		ID:         ListID(newID("list")),
		ProviderID: provider.ID(),
		SpaceID:    SpaceID(c.SpaceID),
		Name:       strings.TrimSpace(c.Title),
		SyncState:  SyncStateLocal,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

func (a *Application) createTask(ctx context.Context, c command.Command) error {
	provider, err := a.provider(c.ProviderID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	task := Task{
		ID:          TaskID(newID("task")),
		ProviderID:  provider.ID(),
		ListID:      ListID(c.ListID),
		Title:       strings.TrimSpace(c.Title),
		Description: c.Description,
		Status:      c.Status,
		Priority:    c.Priority,
		DueAt:       c.DueAt,
		SyncState:   SyncStateLocal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if task.Status == "" {
		task.Status = "open"
	}
	var operation *SyncOperation
	if provider.Capabilities().RemoteSync {
		task.SyncState = SyncStatePending
		operation, err = taskOperation(task, OperationCreate)
		if err != nil {
			return err
		}
	}
	return a.repo.MutateTask(ctx, task, operation)
}

func (a *Application) updateTask(ctx context.Context, c command.Command) error {
	provider, err := a.provider(c.ProviderID)
	if err != nil {
		return err
	}
	task, err := a.repo.GetTask(ctx, provider.ID(), TaskID(c.TaskID))
	if err != nil {
		return err
	}
	if c.Title != "" {
		task.Title = c.Title
	}
	if c.Description != "" {
		task.Description = c.Description
	}
	if c.Status != "" {
		task.Status = c.Status
	}
	if c.Priority != "" {
		task.Priority = c.Priority
	}
	if c.DueAt != nil {
		task.DueAt = c.DueAt
	}
	task.UpdatedAt = time.Now().UTC()
	var operation *SyncOperation
	if provider.Capabilities().RemoteSync {
		task.SyncState = SyncStatePending
		operation, err = taskOperation(task, OperationUpdate)
		if err != nil {
			return err
		}
	}
	return a.repo.MutateTask(ctx, task, operation)
}

func (a *Application) completeTask(ctx context.Context, c command.Command) error {
	provider, err := a.provider(c.ProviderID)
	if err != nil {
		return err
	}
	task, err := a.repo.GetTask(ctx, provider.ID(), TaskID(c.TaskID))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if task.CompletedAt == nil {
		task.Status = "done"
		task.CompletedAt = &now
	} else {
		task.Status = "open"
		task.CompletedAt = nil
	}
	task.UpdatedAt = now
	var operation *SyncOperation
	if provider.Capabilities().RemoteSync {
		task.SyncState = SyncStatePending
		operation, err = taskOperation(task, OperationUpdate)
		if err != nil {
			return err
		}
	}
	return a.repo.MutateTask(ctx, task, operation)
}

func (a *Application) deleteTask(ctx context.Context, c command.Command) error {
	provider, err := a.provider(c.ProviderID)
	if err != nil {
		return err
	}
	task, err := a.repo.GetTask(ctx, provider.ID(), TaskID(c.TaskID))
	if err != nil {
		return err
	}
	var operation *SyncOperation
	if provider.Capabilities().RemoteSync {
		operation, err = taskOperation(task, OperationDelete)
		if err != nil {
			return err
		}
	}
	return a.repo.DeleteTaskAndEnqueue(ctx, provider.ID(), task.ID, operation)
}

func (a *Application) moveTask(ctx context.Context, c command.Command) error {
	provider, err := a.provider(c.ProviderID)
	if err != nil {
		return err
	}
	task, err := a.repo.GetTask(ctx, provider.ID(), TaskID(c.TaskID))
	if err != nil {
		return err
	}
	task.ListID = ListID(c.DestinationListID)
	task.UpdatedAt = time.Now().UTC()
	var operation *SyncOperation
	if provider.Capabilities().RemoteSync {
		task.SyncState = SyncStatePending
		operation, err = taskOperation(task, OperationUpdate)
		if err != nil {
			return err
		}
	}
	return a.repo.MutateTask(ctx, task, operation)
}

func taskOperation(task Task, operationType OperationType) (*SyncOperation, error) {
	payload, err := json.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("encode task operation: %w", err)
	}
	return &SyncOperation{
		ID:         OperationID(newID("op")),
		ProviderID: task.ProviderID,
		EntityType: EntityTypeTask,
		EntityID:   string(task.ID),
		Operation:  operationType,
		Payload:    payload,
		Status:     OperationPending,
		CreatedAt:  time.Now().UTC(),
	}, nil
}
