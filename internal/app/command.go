package app

import (
	"context"
	"fmt"
	"time"
)

// Command is the boundary between TUI input and application behavior.
type Command interface {
	isCommand()
}

type CreateSpaceCommand struct {
	ProviderID ProviderID
	Name       string
}

type CreateListCommand struct {
	ProviderID ProviderID
	SpaceID    SpaceID
	Name       string
}

type CreateTaskCommand struct {
	ProviderID   ProviderID
	ListID       ListID
	ParentTaskID *TaskID
	Title        string
	Description  string
	Status       string
	Priority     Priority
	DueAt        *time.Time
	CompletedAt  *time.Time
}

type PatchTaskCommand struct {
	TaskID TaskID
	Patch  TaskPatch
}

type CompleteTaskCommand struct {
	TaskID TaskID
}

type DeleteTaskCommand struct {
	TaskID TaskID
}

type MoveTaskCommand struct {
	TaskID            TaskID
	DestinationListID ListID
}

type CopyTaskCommand struct {
	SourceTaskID      TaskID
	DestinationListID ListID
}

type TransferTaskCommand struct {
	SourceTaskID      TaskID
	DestinationListID ListID
}

type SearchTasksCommand struct {
	Query string
}

type FilterTasksCommand struct {
	Filter TaskFilter
}

func (CreateSpaceCommand) isCommand()  {}
func (CreateListCommand) isCommand()   {}
func (CreateTaskCommand) isCommand()   {}
func (PatchTaskCommand) isCommand()    {}
func (CompleteTaskCommand) isCommand() {}
func (DeleteTaskCommand) isCommand()   {}
func (MoveTaskCommand) isCommand()     {}
func (CopyTaskCommand) isCommand()     {}
func (TransferTaskCommand) isCommand() {}
func (SearchTasksCommand) isCommand()  {}
func (FilterTasksCommand) isCommand()  {}

type UpdateTaskCommand = PatchTaskCommand

type Event interface {
	isEvent()
}

type SpaceCreated struct {
	Space Space
}

type ListCreated struct {
	List List
}

type TaskCreated struct {
	Task Task
}

type TaskUpdated struct {
	Task Task
}

type TaskCompleted struct {
	Task Task
}

type TaskDeleted struct {
	Task Task
}

type TaskMoved struct {
	Task Task
}

type TaskCopied struct {
	Source      Task
	Destination Task
}

type TaskTransferred struct {
	Source        Task
	Destination   Task
	SourceDeleted bool
}

type TasksSearched struct {
	Query   string
	Results []TaskSearchResult
}

type TasksFiltered struct {
	Filter  TaskFilter
	Results []TaskSearchResult
}

func (SpaceCreated) isEvent()    {}
func (ListCreated) isEvent()     {}
func (TaskCreated) isEvent()     {}
func (TaskUpdated) isEvent()     {}
func (TaskCompleted) isEvent()   {}
func (TaskDeleted) isEvent()     {}
func (TaskMoved) isEvent()       {}
func (TaskCopied) isEvent()      {}
func (TaskTransferred) isEvent() {}
func (TasksSearched) isEvent()   {}
func (TasksFiltered) isEvent()   {}

type TaskPatched = TaskUpdated

func (s *Service) Execute(ctx context.Context, command Command) (Event, error) {
	if command == nil {
		return nil, fmt.Errorf("execute command: %w: command is nil", ErrInvalidInput)
	}
	switch command := command.(type) {
	case CreateSpaceCommand:
		space, err := s.CreateSpace(ctx, CreateSpaceInput{ProviderID: command.ProviderID, Name: command.Name})
		if err != nil {
			return nil, fmt.Errorf("execute create space command: %w", err)
		}
		return SpaceCreated{Space: space}, nil
	case CreateListCommand:
		list, err := s.CreateList(ctx, CreateListInput{ProviderID: command.ProviderID, SpaceID: command.SpaceID, Name: command.Name})
		if err != nil {
			return nil, fmt.Errorf("execute create list command: %w", err)
		}
		return ListCreated{List: list}, nil
	case CreateTaskCommand:
		task, err := s.CreateTask(ctx, CreateTaskInput{
			ProviderID:   command.ProviderID,
			ListID:       command.ListID,
			ParentTaskID: command.ParentTaskID,
			Title:        command.Title,
			Description:  command.Description,
			Status:       command.Status,
			Priority:     command.Priority,
			DueAt:        command.DueAt,
			CompletedAt:  command.CompletedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("execute create task command: %w", err)
		}
		return TaskCreated{Task: task}, nil
	case PatchTaskCommand:
		task, err := s.PatchTask(ctx, command.TaskID, command.Patch)
		if err != nil {
			return nil, fmt.Errorf("execute patch task command: %w", err)
		}
		return TaskUpdated{Task: task}, nil
	case CompleteTaskCommand:
		task, err := s.CompleteTask(ctx, command.TaskID)
		if err != nil {
			return nil, fmt.Errorf("execute complete task command: %w", err)
		}
		return TaskCompleted{Task: task}, nil
	case DeleteTaskCommand:
		task, err := s.GetTask(ctx, command.TaskID)
		if err != nil {
			return nil, fmt.Errorf("execute delete task command load task: %w", err)
		}
		if err := s.DeleteTask(ctx, command.TaskID); err != nil {
			return nil, fmt.Errorf("execute delete task command: %w", err)
		}
		return TaskDeleted{Task: task}, nil
	case MoveTaskCommand:
		task, err := s.MoveTask(ctx, command.TaskID, command.DestinationListID)
		if err != nil {
			return nil, fmt.Errorf("execute move task command: %w", err)
		}
		return TaskMoved{Task: task}, nil
	case CopyTaskCommand:
		source, err := s.GetTask(ctx, command.SourceTaskID)
		if err != nil {
			return nil, fmt.Errorf("execute copy task command load source: %w", err)
		}
		task, err := s.CopyTask(ctx, command.SourceTaskID, command.DestinationListID)
		if err != nil {
			return nil, fmt.Errorf("execute copy task command: %w", err)
		}
		return TaskCopied{Source: source, Destination: task}, nil
	case TransferTaskCommand:
		result, err := s.TransferTaskResult(ctx, command.SourceTaskID, command.DestinationListID)
		if result.Destination.ID == "" {
			if err != nil {
				return nil, fmt.Errorf("execute transfer task command: %w", err)
			}
			return nil, fmt.Errorf("execute transfer task command: %w", ErrInvalidEntity)
		}
		event := TaskTransferred{Source: result.Source, Destination: result.Destination, SourceDeleted: result.SourceDeleted}
		if err != nil {
			return event, fmt.Errorf("execute transfer task command: %w", err)
		}
		return event, nil
	case SearchTasksCommand:
		results, err := s.SearchTasks(ctx, command.Query)
		if err != nil {
			return nil, fmt.Errorf("execute search tasks command: %w", err)
		}
		return TasksSearched{Query: command.Query, Results: results}, nil
	case FilterTasksCommand:
		results, err := s.FilterTasks(ctx, command.Filter)
		if err != nil {
			return nil, fmt.Errorf("execute filter tasks command: %w", err)
		}
		return TasksFiltered{Filter: command.Filter, Results: results}, nil
	default:
		return nil, fmt.Errorf("execute command: %w: %T", ErrInvalidInput, command)
	}
}

func (s *Service) Handle(ctx context.Context, command Command) (Event, error) {
	return s.Execute(ctx, command)
}
