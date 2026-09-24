package bootstrap

import "time"

// ProviderID identifies a configured provider instance, not only a provider
// type. This allows multiple accounts of the same provider in the future.
type ProviderID string

// SpaceID identifies a space in the local database.
type SpaceID string

// ListID identifies a list in the local database.
type ListID string

// TaskID identifies a task in the local database.
type TaskID string

// OperationID identifies a durable synchronization operation.
type OperationID string

// ProviderType identifies the adapter used by a provider instance.
type ProviderType string

const (
	ProviderTypeLocal   ProviderType = "local"
	ProviderTypeClickUp ProviderType = "clickup"
)

// SyncState describes the local synchronization state of an entity.
type SyncState string

const (
	SyncStateSynced   SyncState = "synced"
	SyncStatePending  SyncState = "pending"
	SyncStateSyncing  SyncState = "syncing"
	SyncStateConflict SyncState = "conflict"
	SyncStateFailed   SyncState = "failed"
	SyncStateLocal    SyncState = "local"
)

// EntityType identifies the entity referenced by a sync operation.
type EntityType string

const (
	EntityTypeSpace EntityType = "space"
	EntityTypeList  EntityType = "list"
	EntityTypeTask  EntityType = "task"
)

// OperationType identifies a remote mutation.
type OperationType string

const (
	OperationCreate OperationType = "create"
	OperationUpdate OperationType = "update"
	OperationDelete OperationType = "delete"
)

// OperationStatus is the durable state of a sync operation.
type OperationStatus string

const (
	OperationPending   OperationStatus = "pending"
	OperationSyncing   OperationStatus = "syncing"
	OperationFailed    OperationStatus = "failed"
	OperationCompleted OperationStatus = "completed"
)

// ProviderRecord is the persisted, non-secret description of a provider
// instance.
type ProviderRecord struct {
	ID            ProviderID
	Type          ProviderType
	Name          string
	Enabled       bool
	Configuration string
	SyncState     SyncState
	LastSyncAt    *time.Time
	SyncError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Space is the normalized application representation of a provider space.
type Space struct {
	ID              SpaceID
	ProviderID      ProviderID
	RemoteID        *string
	Name            string
	SyncState       SyncState
	RemoteUpdatedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// List is the normalized application representation of a provider list.
type List struct {
	ID              ListID
	ProviderID      ProviderID
	SpaceID         SpaceID
	RemoteID        *string
	Name            string
	SyncState       SyncState
	RemoteUpdatedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Task is the normalized application representation of a provider task.
type Task struct {
	ID              TaskID
	ProviderID      ProviderID
	ListID          ListID
	ListIDs         []ListID
	RemoteID        *string
	ParentTaskID    *TaskID
	Assignee        string
	Title           string
	Description     string
	Status          string
	Priority        string
	TimeEstimate    *time.Duration
	TimeTracked     *time.Duration
	DueAt           *time.Time
	CompletedAt     *time.Time
	SyncState       SyncState
	RemoteUpdatedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ProviderMetadata stores provider-specific data without adding provider
// fields to normalized entities.
type ProviderMetadata struct {
	EntityType EntityType
	EntityID   string
	ProviderID ProviderID
	Key        string
	Value      string
}

// SyncOperation is a durable remote mutation. Payload contains normalized JSON
// and never credentials.
type SyncOperation struct {
	ID            OperationID
	ProviderID    ProviderID
	EntityType    EntityType
	EntityID      string
	Operation     OperationType
	Payload       []byte
	Attempts      int
	Status        OperationStatus
	Error         string
	CreatedAt     time.Time
	LastAttemptAt *time.Time
}

// UIState is presentation state persisted independently from domain entities.
type UIState struct {
	ProviderID      string          `json:"provider_id,omitempty"`
	SpaceID         string          `json:"space_id,omitempty"`
	ListID          string          `json:"list_id,omitempty"`
	Panel           string          `json:"panel,omitempty"`
	Cursor          int             `json:"cursor,omitempty"`
	Filter          string          `json:"filter,omitempty"`
	GroupBy         string          `json:"group_by,omitempty"`
	CollapsedGroups []string        `json:"collapsed_groups,omitempty"`
	ListViews       []ListViewState `json:"list_views,omitempty"`
}

// ListViewState persists task filter and grouping preferences for one list.
type ListViewState struct {
	ProviderID string                 `json:"provider_id"`
	ListID     string                 `json:"list_id"`
	Filter     string                 `json:"filter,omitempty"`
	GroupBy    string                 `json:"group_by,omitempty"`
	Columns    []TaskColumnPreference `json:"columns,omitempty"`
}

// TaskColumnPreference stores visibility and width overrides for one list.
type TaskColumnPreference struct {
	ID      string `json:"id"`
	Visible bool   `json:"visible"`
	Width   int    `json:"width,omitempty"`
}

// View is the local snapshot supplied to the TUI. It is intentionally a
// snapshot so rendering does not perform I/O or synchronize providers.
type View struct {
	Providers        []ProviderRecord
	Spaces           []Space
	Lists            []List
	Tasks            []Task
	EditorOptions    []TaskEditorOptions
	TaskColumns      []TaskColumn
	TaskColumnValues []TaskColumnValueSet
	SyncErrors       map[ProviderID]string
}

// TaskColumn is a provider-neutral dynamic column attached to one list.
type TaskColumn struct {
	ProviderID ProviderID
	ListID     ListID
	ID         string
	Name       string
	Type       string
}

// TaskColumnValueSet holds display-ready dynamic values for one task.
type TaskColumnValueSet struct {
	ProviderID ProviderID
	TaskID     TaskID
	Values     map[string]string
}

// TaskEditorOptions contains provider-owned completion values for one list.
type TaskEditorOptions struct {
	ProviderID ProviderID
	SpaceID    SpaceID
	ListID     ListID
	Statuses   []string
}
