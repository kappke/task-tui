// Package tui contains the presentation model for the task manager.
//
// The package deliberately owns no persistence or provider implementation. It
// consumes normalized snapshots and emits application commands, which keeps
// the terminal event loop local-first and easy to adapt to a terminal
// framework.
package tui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// The domain aliases keep provider ownership and entity identity consistent
// with the application foundation while allowing UI projections to remain
// local to this package.
type (
	ProviderID   = domain.ProviderID
	SpaceID      = domain.SpaceID
	ListID       = domain.ListID
	TaskID       = domain.TaskID
	ProviderType = domain.ProviderType
	Priority     = domain.Priority
	SyncState    = domain.SyncState
	Provider     = domain.Provider
	Space        = domain.Space
	List         = domain.List
	Task         = domain.Task
)

const (
	ProviderTypeLocal   = domain.ProviderTypeLocal
	ProviderTypeClickUp = domain.ProviderTypeClickUp
)

const (
	PriorityNone   = domain.PriorityNone
	PriorityLow    = domain.PriorityLow
	PriorityNormal = domain.PriorityNormal
	PriorityHigh   = domain.PriorityHigh
	PriorityUrgent = domain.PriorityUrgent

	SyncStateUnknown  SyncState = "unknown"
	SyncStateSynced             = domain.SyncStateSynced
	SyncStatePending            = domain.SyncStatePending
	SyncStateSyncing            = domain.SyncStateSyncing
	SyncStateConflict           = domain.SyncStateConflict
	SyncStateFailed             = domain.SyncStateFailed
	SyncStateLocal              = domain.SyncStateLocal
)

// Snapshot is the cached, normalized data supplied by the application layer.
// The TUI treats it as input state and never performs persistence itself.
type Snapshot struct {
	Providers []Provider
	Spaces    []Space
	Lists     []List
	Tasks     []Task
}

// Data is a readable alias for integrations that call their local cache data
// "data" rather than a snapshot.
type Data = Snapshot

// TreeNodeKind identifies one level in Provider -> Space -> List.
type TreeNodeKind string

const (
	TreeNodeProvider TreeNodeKind = "provider"
	TreeNodeSpace    TreeNodeKind = "space"
	TreeNodeList     TreeNodeKind = "list"
)

// TreeNodeRef is a stable UI selection key. IDs are scoped by ProviderID so
// equal IDs from different providers cannot alias in the interface.
type TreeNodeRef struct {
	Kind       TreeNodeKind
	ProviderID ProviderID
	SpaceID    SpaceID
	ListID     ListID
}

// TreeNode is a presentation projection of a hierarchy entity.
type TreeNode struct {
	Ref          TreeNodeRef
	Name         string
	ProviderID   ProviderID
	ProviderName string
	SyncState    SyncState
	SyncError    string
	Depth        int
	Expanded     bool
}

// TaskRef is a provider-scoped task selection key.
type TaskRef struct {
	ProviderID ProviderID
	TaskID     TaskID
}

// TaskRow enriches a normalized task with local hierarchy labels for
// rendering. It is intentionally a projection, not a replacement domain
// object.
type TaskRow struct {
	Task         Task
	ProviderID   ProviderID
	ProviderName string
	SpaceID      SpaceID
	SpaceName    string
	ListID       ListID
	ListName     string
	SearchResult bool
}

// Panel is the focused navigation area.
type Panel string

const (
	PanelHierarchy Panel = "hierarchy"
	PanelTasks     Panel = "tasks"
)

// Mode identifies a transient input/action mode.
type Mode string

const (
	ModeBrowse     Mode = "browse"
	ModeSearch     Mode = "search"
	ModeFilter     Mode = "filter"
	ModeCommand    Mode = "command"
	ModeCreateTask Mode = "create_task"
	ModeEditTask   Mode = "edit_task"
	ModeConfirm    Mode = "confirm"
)

// UIState contains only presentation state. Domain objects remain in Data.
type UIState struct {
	Focus                   Panel
	Mode                    Mode
	TreeCursor              int
	TaskCursor              int
	TreeOffset              int
	TaskOffset              int
	TreeHorizontalOffset    int
	TaskHorizontalOffset    int
	SelectedNode            TreeNodeRef
	SelectedTask            TaskRef
	ExpandedNodes           map[TreeNodeRef]bool
	SearchActive            bool
	SearchQuery             string
	FilterActive            bool
	Filter                  Filter
	Input                   string
	InputCursor             int
	InputOrigin             string
	InputOriginSearchActive bool
	PendingCommand          AppCommand
	HasPending              bool
	ConfirmPrompt           string
	Width                   int
	Height                  int
	Quitting                bool
}

// StatusLevel controls how the status line is presented.
type StatusLevel string

const (
	StatusInfo    StatusLevel = "info"
	StatusSuccess StatusLevel = "success"
	StatusWarning StatusLevel = "warning"
	StatusError   StatusLevel = "error"
)

// Status is a user-visible, local UI notice. It is never used as a hidden
// control path for application operations.
type Status struct {
	Level StatusLevel
	Text  string
}

// CommandSink is an optional integration hook. It receives normalized app
// commands only; the TUI never calls a provider, database, or SQL client.
type CommandSink func(AppCommand)

// Options configures the model without coupling it to a terminal framework.
type Options struct {
	KeyMap       KeyMap
	OnCommand    CommandSink
	RequestCache bool
}

// Model is the complete TUI model. Data is the local cached domain snapshot;
// UI and Status are independent presentation state.
type Model struct {
	Data    Snapshot
	UI      UIState
	Status  Status
	KeyMap  KeyMap
	Options Options
}

// New creates a model with cached data ready to render immediately.
func New(data Snapshot) Model {
	return NewWithOptions(data, Options{})
}

// NewModel is an explicit constructor alias for callers that prefer the
// longer name.
func NewModel(data Snapshot) Model {
	return New(data)
}

// NewWithOptions creates a model and preserves the same immediate-render
// startup behavior as New.
func NewWithOptions(data Snapshot, options Options) Model {
	keyMap := options.KeyMap
	if len(keyMap.Bindings) == 0 {
		keyMap = DefaultKeyMap()
	}

	m := Model{
		Data:    cloneSnapshot(data),
		KeyMap:  keyMap,
		Options: options,
		UI: UIState{
			Focus:         PanelHierarchy,
			Mode:          ModeBrowse,
			ExpandedNodes: make(map[TreeNodeRef]bool),
			Width:         100,
			Height:        24,
		},
	}
	m.initializeSelection()
	return m
}

// NewEmpty creates a model that asks its application owner for cached data
// when Init is run. It still renders a useful empty shell synchronously.
func NewEmpty(options Options) Model {
	options.RequestCache = true
	model := NewWithOptions(Snapshot{}, options)
	model.Status = Status{Level: StatusInfo, Text: "Loading cached data..."}
	return model
}

// SnapshotData returns a copy suitable for inspection by an adapter.
func (m Model) SnapshotData() Snapshot {
	return cloneSnapshot(m.Data)
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Providers = append([]Provider(nil), in.Providers...)
	out.Spaces = append([]Space(nil), in.Spaces...)
	out.Lists = append([]List(nil), in.Lists...)
	out.Tasks = append([]Task(nil), in.Tasks...)

	for i := range out.Providers {
		out.Providers[i].Configuration = cloneRawMessage(in.Providers[i].Configuration)
		out.Providers[i].SyncCursor = cloneString(in.Providers[i].SyncCursor)
		out.Providers[i].LastSyncAt = cloneTime(in.Providers[i].LastSyncAt)
	}
	for i := range out.Spaces {
		out.Spaces[i].RemoteID = cloneString(in.Spaces[i].RemoteID)
		out.Spaces[i].RemoteUpdatedAt = cloneTime(in.Spaces[i].RemoteUpdatedAt)
	}
	for i := range out.Lists {
		out.Lists[i].RemoteID = cloneString(in.Lists[i].RemoteID)
		out.Lists[i].RemoteUpdatedAt = cloneTime(in.Lists[i].RemoteUpdatedAt)
	}
	for i := range out.Tasks {
		out.Tasks[i] = cloneTask(in.Tasks[i])
	}
	return out
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	out := make(json.RawMessage, len(value))
	copy(out, value)
	return out
}

func cloneTask(in Task) Task {
	out := in
	out.RemoteID = cloneString(in.RemoteID)
	out.ParentTaskID = cloneTaskID(in.ParentTaskID)
	out.DueAt = cloneTime(in.DueAt)
	out.CompletedAt = cloneTime(in.CompletedAt)
	out.RemoteUpdatedAt = cloneTime(in.RemoteUpdatedAt)
	return out
}

func cloneTaskID(value *TaskID) *TaskID {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneExpanded(in map[TreeNodeRef]bool) map[TreeNodeRef]bool {
	if len(in) == 0 {
		return make(map[TreeNodeRef]bool)
	}
	out := make(map[TreeNodeRef]bool, len(in))
	for ref, expanded := range in {
		out[ref] = expanded
	}
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isTaskComplete(task Task) bool {
	switch normalize(task.Status) {
	case "done", "complete", "completed", "closed", "cancelled", "canceled":
		return true
	default:
		return task.CompletedAt != nil
	}
}
