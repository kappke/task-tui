package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Message is the small message contract used by Model.Update. A Bubble Tea
// adapter can convert its tea.Msg values to these messages at the boundary.
type Message interface{}

// Cmd follows the shape of Bubble Tea's command function without importing a
// terminal framework. Commands are inert until the application event loop
// evaluates them.
type Cmd func() Message

// CommandKind identifies an application/core operation requested by the TUI.
type CommandKind string

const (
	CommandLoadCached          CommandKind = "load_cached"
	CommandFetchLists          CommandKind = "fetch_lists"
	CommandFetchTasks          CommandKind = "fetch_tasks"
	CommandFetchTask           CommandKind = "fetch_task"
	CommandCreateSpace         CommandKind = "create_space"
	CommandCreateList          CommandKind = "create_list"
	CommandCreateTask          CommandKind = "create_task"
	CommandUpdateTask          CommandKind = "update_task"
	CommandCompleteTask        CommandKind = "complete_task"
	CommandDeleteTask          CommandKind = "delete_task"
	CommandMoveTask            CommandKind = "move_task"
	CommandAddTaskToList       CommandKind = "add_task_to_list"
	CommandRemoveTaskFromList  CommandKind = "remove_task_from_list"
	CommandSearch              CommandKind = "search_tasks"
	CommandFilter              CommandKind = "filter_tasks"
	CommandSort                CommandKind = "sort_tasks"
	CommandGroup               CommandKind = "group_tasks"
	CommandConfigureColumns    CommandKind = "configure_columns"
	CommandStartTracking       CommandKind = "start_tracking"
	CommandStopTracking        CommandKind = "stop_tracking"
	CommandPollTracking        CommandKind = "poll_tracking"
	CommandLoadTrackingHistory CommandKind = "load_tracking_history"
	CommandSwitchProvider      CommandKind = "switch_provider"
	CommandRefresh             CommandKind = "refresh"
	CommandQuit                CommandKind = "quit"
	CommandHelp                CommandKind = "help"
)

// Short aliases make command construction pleasant for small adapters while
// retaining the explicit MVP names above.
const (
	CommandCreate   = CommandCreateTask
	CommandSpace    = CommandCreateSpace
	CommandList     = CommandCreateList
	CommandEdit     = CommandUpdateTask
	CommandComplete = CommandCompleteTask
	CommandDelete   = CommandDeleteTask
)

// AppCommand is the normalized contract between presentation and application
// layers. ProviderID is carried on every provider-owned task operation.
type AppCommand struct {
	Kind              CommandKind
	ProviderID        ProviderID
	SpaceID           SpaceID
	ListID            ListID
	DestinationListID ListID
	TaskID            TaskID
	Title             string
	Description       string
	Assignee          string
	Priority          Priority
	DueAt             *time.Time
	ClearDueAt        bool
	Query             string
	Filter            Filter
	Sort              []SortCriterion
	GroupBy           TaskGroupMode
	Completed         bool
	Status            string
	Raw               string
	EditAllFields     bool
	FetchRemote       bool
}

// Command is a concise alias for the presentation-to-application contract.
type Command = AppCommand

// CommandMsg is returned by Cmd values emitted from input handling.
type CommandMsg struct {
	Command AppCommand
}

// CommandResultMsg lets the application report completion without making the
// TUI wait for local persistence or remote synchronization.
type CommandResultMsg struct {
	Command AppCommand
	Err     error
	Text    string
}

// Init emits only an optional cache-load request. New(data) already has
// cached state and therefore renders immediately without doing work.
func (m Model) Init() Cmd {
	if !m.Options.RequestCache && hasSnapshotData(m.Data) {
		return nil
	}
	return m.emit(AppCommand{Kind: CommandLoadCached})
}

func hasSnapshotData(data Snapshot) bool {
	return len(data.Providers) > 0 || len(data.Spaces) > 0 || len(data.Lists) > 0 || len(data.Tasks) > 0
}

func (m Model) emit(command AppCommand) Cmd {
	sink := m.Options.OnCommand
	return func() Message {
		if sink != nil {
			sink(command)
		}
		return CommandMsg{Command: command}
	}
}

// ParseCommand parses the command-palette grammar. Selection-dependent fields
// are filled by Model when the command is submitted.
func ParseCommand(input string) (AppCommand, error) {
	raw := strings.TrimSpace(input)
	raw = strings.TrimPrefix(raw, ":")
	if raw == "" {
		return AppCommand{}, errors.New("enter a command")
	}

	fields := strings.Fields(raw)
	name := normalize(fields[0])
	args := fields[1:]
	if name == "task" {
		if len(args) == 0 {
			return AppCommand{}, errors.New("task command requires create, edit, complete, delete, move, add-list, or remove-list")
		}
		name = normalize(args[0])
		args = args[1:]
	}
	if name == "space" || name == "list" {
		if len(args) == 0 || (normalize(args[0]) != "create" && normalize(args[0]) != "new") {
			return AppCommand{}, fmt.Errorf("%s command requires create", name)
		}
		command := AppCommand{Raw: raw}
		if name == "space" {
			command.Kind = CommandCreateSpace
		} else {
			command.Kind = CommandCreateList
		}
		command.Title = strings.TrimSpace(strings.Join(args[1:], " "))
		return command, nil
	}
	if name == "provider" {
		if len(args) > 0 && (normalize(args[0]) == "switch" || normalize(args[0]) == "select") {
			args = args[1:]
		}
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return AppCommand{}, errors.New("provider command requires a provider id or name")
		}
		return AppCommand{Kind: CommandSwitchProvider, ProviderID: ProviderID(args[0]), Raw: raw}, nil
	}

	command := AppCommand{Raw: raw}
	switch name {
	case "create", "new":
		command.Kind = CommandCreateTask
		command.Title = strings.TrimSpace(strings.Join(args, " "))
	case "edit", "update":
		command.Kind = CommandUpdateTask
		command.Title = strings.TrimSpace(strings.Join(args, " "))
	case "complete", "done", "toggle":
		command.Kind = CommandCompleteTask
	case "delete", "remove":
		command.Kind = CommandDeleteTask
	case "move", "add-list", "add-to-list", "remove-list", "remove-from-list":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return AppCommand{}, fmt.Errorf("task %s requires a destination list id", name)
		}
		command.DestinationListID = ListID(strings.TrimSpace(args[0]))
		switch name {
		case "move":
			command.Kind = CommandMoveTask
		case "add-list", "add-to-list":
			command.Kind = CommandAddTaskToList
		default:
			command.Kind = CommandRemoveTaskFromList
		}
	case "search", "find":
		command.Kind = CommandSearch
		command.Query = strings.TrimSpace(strings.Join(args, " "))
		if command.Query == "" {
			return AppCommand{}, errors.New("search query cannot be empty")
		}
	case "filter":
		command.Kind = CommandFilter
		filterInput := strings.TrimSpace(strings.Join(args, " "))
		filter, err := ParseFilter(filterInput)
		if err != nil {
			return AppCommand{}, err
		}
		command.Filter = filter
	case "sort", "order":
		command.Kind = CommandSort
		criteria, err := ParseSort(strings.TrimSpace(strings.Join(args, " ")))
		if err != nil {
			return AppCommand{}, err
		}
		command.Sort = criteria
	case "group", "groupby":
		command.Kind = CommandGroup
		modeInput := strings.TrimSpace(strings.Join(args, " "))
		mode, err := ParseTaskGroupMode(modeInput)
		if err != nil {
			return AppCommand{}, err
		}
		command.GroupBy = mode
	case "columns", "column":
		command.Kind = CommandConfigureColumns
	case "ungroup", "ungrouped":
		command.Kind = CommandGroup
		command.GroupBy = TaskGroupNone
	case "refresh", "sync", "r":
		command.Kind = CommandRefresh
	case "quit", "exit", "q":
		command.Kind = CommandQuit
	case "help", "?":
		command.Kind = CommandHelp
	default:
		return AppCommand{}, fmt.Errorf("unknown command %q", fields[0])
	}
	return command, nil
}

// ParseTaskGroupMode accepts the command-palette names for the supported task
// arrangements. "tasks" and "subtasks" both select the parent-child view.
func ParseTaskGroupMode(input string) (TaskGroupMode, error) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(input)))
	if len(fields) == 2 && fields[0] == "by" {
		fields = fields[1:]
	}
	if len(fields) != 1 {
		return TaskGroupNone, errors.New("group requires status, assignee, priority, tasks, or none")
	}
	switch fields[0] {
	case "none", "off", "clear", "ungroup":
		return TaskGroupNone, nil
	case "status", "statuses", "state":
		return TaskGroupStatus, nil
	case "assignee", "assignees", "owner":
		return TaskGroupAssignee, nil
	case "priority", "priorities":
		return TaskGroupPriority, nil
	case "task", "tasks", "subtask", "subtasks", "hierarchy", "tasks/subtasks", "task/subtask", "tasks_subtasks":
		return TaskGroupTasksSubtasks, nil
	default:
		return TaskGroupNone, fmt.Errorf("unknown task group %q", fields[0])
	}
}
