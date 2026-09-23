// Package command contains application commands emitted by the presentation
// layer. Commands deliberately contain normalized values rather than provider
// specific request types.
package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind identifies an application command.
type Kind string

const (
	KindQuit         Kind = "quit"
	KindRefresh      Kind = "refresh"
	KindFetchLists   Kind = "fetch_lists"
	KindFetchTasks   Kind = "fetch_tasks"
	KindFetchTask    Kind = "fetch_task"
	KindSearch       Kind = "search"
	KindCreateSpace  Kind = "create_space"
	KindCreateList   Kind = "create_list"
	KindCreateTask   Kind = "create_task"
	KindUpdateTask   Kind = "update_task"
	KindCompleteTask Kind = "complete_task"
	KindDeleteTask   Kind = "delete_task"
	KindMoveTask     Kind = "move_task"
	KindTransferTask Kind = "transfer_task"
)

// Command is a normalized request from the UI to the application layer.
type Command struct {
	Kind Kind

	ProviderID string
	SpaceID    string
	ListID     string
	TaskID     string

	Title         string
	Description   string
	Assignee      string
	Status        string
	Priority      string
	DueAt         *time.Time
	ClearDueAt    bool
	EditAllFields bool
	Completed     *bool

	// DestinationListID is used by explicit move and transfer commands. A
	// normal move is only valid within the source provider.
	DestinationProviderID string
	DestinationSpaceID    string
	DestinationListID     string

	Query string
}

// EventKind identifies an application event returned after handling a command.
type EventKind string

const (
	EventNone          EventKind = "none"
	EventQuit          EventKind = "quit"
	EventChanged       EventKind = "changed"
	EventRefresh       EventKind = "refresh"
	EventSearchResults EventKind = "search_results"
)

// Event is the small, presentation-neutral result of a command.
type Event struct {
	Kind  EventKind
	Query string
	Error error
}

// Handler is implemented by the application layer.
type Handler interface {
	Handle(context.Context, Command) (Event, error)
}

// ErrInvalid is returned when a command cannot be interpreted safely.
var ErrInvalid = errors.New("invalid command")

// Validate checks command fields that are common to all handlers. Domain
// validation remains the responsibility of the application and repository.
func (c Command) Validate() error {
	switch c.Kind {
	case KindQuit, KindRefresh:
		return nil
	case KindFetchLists:
		if strings.TrimSpace(c.ProviderID) == "" {
			return fmt.Errorf("%w: provider is required to fetch lists", ErrInvalid)
		}
	case KindFetchTasks:
		if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.ListID) == "" {
			return fmt.Errorf("%w: provider and list are required to fetch tasks", ErrInvalid)
		}
	case KindFetchTask:
		if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.TaskID) == "" {
			return fmt.Errorf("%w: provider and task are required to fetch a task", ErrInvalid)
		}
	case KindSearch:
		if strings.TrimSpace(c.Query) == "" {
			return fmt.Errorf("%w: search query is empty", ErrInvalid)
		}
	case KindCreateSpace:
		if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("%w: provider and space title are required", ErrInvalid)
		}
	case KindCreateList:
		if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.SpaceID) == "" || strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("%w: provider, space, and list title are required", ErrInvalid)
		}
	case KindCreateTask:
		if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.ListID) == "" || strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("%w: provider, list, and task title are required", ErrInvalid)
		}
	case KindUpdateTask, KindCompleteTask, KindDeleteTask:
		if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.TaskID) == "" {
			return fmt.Errorf("%w: provider and task are required", ErrInvalid)
		}
	case KindMoveTask, KindTransferTask:
		if strings.TrimSpace(c.ProviderID) == "" || strings.TrimSpace(c.TaskID) == "" || strings.TrimSpace(c.DestinationListID) == "" {
			return fmt.Errorf("%w: provider, task, and destination list are required", ErrInvalid)
		}
		if c.Kind == KindTransferTask && strings.TrimSpace(c.DestinationProviderID) == "" {
			return fmt.Errorf("%w: destination provider is required for transfer", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, c.Kind)
	}
	return nil
}
