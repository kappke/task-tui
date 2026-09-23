package tui

import (
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// WindowSizeMsg updates the available terminal dimensions.
type WindowSizeMsg struct {
	Width  int
	Height int
}

// ResizeMsg is an adapter-friendly alias for WindowSizeMsg.
type ResizeMsg = WindowSizeMsg

// SnapshotMsg replaces the local cached snapshot. It is safe to deliver this
// asynchronously after the initial model has already rendered.
type SnapshotMsg struct {
	Data Snapshot
}

// CachedDataMsg and DataLoadedMsg are descriptive aliases for the same event.
type CachedDataMsg = SnapshotMsg
type DataLoadedMsg = SnapshotMsg

// NewSnapshotMsg creates an asynchronous snapshot event.
func NewSnapshotMsg(data Snapshot) SnapshotMsg {
	return SnapshotMsg{Data: data}
}

// TasksLoadedMsg updates cached tasks for one provider/list scope.
type TasksLoadedMsg struct {
	ProviderID ProviderID
	ListID     ListID
	Tasks      []Task
	Replace    bool
}

// EntityType identifies the owner of a sync update. It aliases the foundation
// entity type so domain events can cross the adapter without conversion.
type EntityType = domain.EntityType

const (
	EntityProvider EntityType = "provider"
	EntitySpace    EntityType = domain.EntityTypeSpace
	EntityList     EntityType = domain.EntityTypeList
	EntityTask     EntityType = domain.EntityTypeTask
)

// SyncStateMsg reports asynchronous synchronization progress or failure.
type SyncStateMsg struct {
	ProviderID ProviderID
	EntityType EntityType
	EntityID   string
	State      SyncState
	Error      string
	Message    string
	LastSyncAt *time.Time
}

// SyncUpdatedMsg is an alias useful to event-oriented application adapters.
type SyncUpdatedMsg = SyncStateMsg

// ErrorMsg displays an application error without crashing or blocking the UI.
type ErrorMsg struct {
	Err  error
	Text string
}

// StatusMsg displays a non-error application notice.
type StatusMsg struct {
	Level StatusLevel
	Text  string
}

// QuitMsg lets an outer lifecycle owner close the model cleanly.
type QuitMsg struct{}
