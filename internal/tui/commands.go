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
	CommandLoadCached   CommandKind = "load_cached"
	CommandCreateTask   CommandKind = "create_task"
	CommandUpdateTask   CommandKind = "update_task"
	CommandCompleteTask CommandKind = "complete_task"
	CommandDeleteTask   CommandKind = "delete_task"
	CommandSearch       CommandKind = "search_tasks"
	CommandFilter       CommandKind = "filter_tasks"
	CommandRefresh      CommandKind = "refresh"
	CommandQuit         CommandKind = "quit"
	CommandHelp         CommandKind = "help"
)

// Short aliases make command construction pleasant for small adapters while
// retaining the explicit MVP names above.
const (
	CommandCreate   = CommandCreateTask
	CommandEdit     = CommandUpdateTask
	CommandComplete = CommandCompleteTask
	CommandDelete   = CommandDeleteTask
)

// AppCommand is the normalized contract between presentation and application
// layers. ProviderID is carried on every provider-owned task operation.
type AppCommand struct {
	Kind        CommandKind
	ProviderID  ProviderID
	SpaceID     SpaceID
	ListID      ListID
	TaskID      TaskID
	Title       string
	Description string
	Priority    Priority
	DueAt       *time.Time
	Query       string
	Filter      Filter
	Completed   bool
	Status      string
	Raw         string
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
			return AppCommand{}, errors.New("task command requires create, edit, complete, or delete")
		}
		name = normalize(args[0])
		args = args[1:]
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
