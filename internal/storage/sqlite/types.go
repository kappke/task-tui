package sqlite

import (
	"errors"
	"time"
)

var (
	ErrNotFound  = errors.New("sqlite: not found")
	ErrLeaseLost = errors.New("sqlite: queue lease lost")
)

type SyncState string

const (
	SyncStateSynced   SyncState = "synced"
	SyncStatePending  SyncState = "pending"
	SyncStateSyncing  SyncState = "syncing"
	SyncStateConflict SyncState = "conflict"
	SyncStateFailed   SyncState = "failed"
	SyncStateLocal    SyncState = "local"
)

type QueueStatus string

const (
	QueueStatusPending   QueueStatus = "pending"
	QueueStatusSyncing   QueueStatus = "syncing"
	QueueStatusFailed    QueueStatus = "failed"
	QueueStatusCompleted QueueStatus = "completed"
)

type EntityType string

const (
	EntityProvider EntityType = "provider"
	EntitySpace    EntityType = "space"
	EntityList     EntityType = "list"
	EntityTask     EntityType = "task"
)

type OperationType string

const (
	OperationCreate OperationType = "create"
	OperationUpdate OperationType = "update"
	OperationDelete OperationType = "delete"
)

type Provider struct {
	ID            string
	Type          string
	Name          string
	Enabled       bool
	Configuration []byte
	SyncState     SyncState
	SyncCursor    *string
	SyncError     *string
	LastSyncAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Space struct {
	ID              string
	ProviderID      string
	RemoteID        *string
	Name            string
	SyncState       SyncState
	RemoteUpdatedAt *time.Time
	IsDeleted       bool
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type List struct {
	ID              string
	ProviderID      string
	SpaceID         string
	RemoteID        *string
	Name            string
	SyncState       SyncState
	RemoteUpdatedAt *time.Time
	IsDeleted       bool
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Task struct {
	ID              string
	ProviderID      string
	ListID          string
	RemoteID        *string
	ParentTaskID    *string
	Assignee        string
	Title           string
	Description     string
	Status          string
	Priority        string
	DueAt           *time.Time
	CompletedAt     *time.Time
	SyncState       SyncState
	RemoteUpdatedAt *time.Time
	IsDeleted       bool
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type TaskFilter struct {
	Query          string
	ProviderID     string
	ProviderIDs    []string
	SpaceID        string
	ListID         string
	Status         string
	Statuses       []string
	Priority       string
	MinPriority    *int
	MaxPriority    *int
	SyncState      SyncState
	DueBefore      *time.Time
	DueAfter       *time.Time
	Completed      *bool
	IncludeDeleted bool
}

type TaskSearch struct {
	Query  string
	Filter TaskFilter
	Limit  int
	Offset int
}

type TaskResult struct {
	Task     Task
	List     List
	Space    Space
	Provider Provider
}

type SyncOperation struct {
	ID             string
	ProviderID     string
	EntityType     EntityType
	EntityID       string
	Operation      OperationType
	Payload        []byte
	Attempts       int
	Status         QueueStatus
	Error          *string
	CreatedAt      time.Time
	LastAttemptAt  *time.Time
	NextAttemptAt  time.Time
	LeaseOwner     *string
	LeaseExpiresAt *time.Time
	CompletedAt    *time.Time
}

type SyncBase struct {
	ProviderID      string
	EntityType      EntityType
	EntityID        string
	RemoteID        *string
	SyncState       SyncState
	RemoteUpdatedAt *time.Time
	Payload         []byte
	RemoteVersion   *string
	CapturedAt      time.Time
	UpdatedAt       time.Time
}

type Conflict struct {
	ID          string
	ProviderID  string
	EntityType  EntityType
	EntityID    string
	BaseValue   []byte
	LocalValue  []byte
	RemoteValue []byte
	Status      string
	Resolution  []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ResolvedAt  *time.Time
}

type ProviderSyncState struct {
	ProviderID string
	State      SyncState
	Cursor     *string
	Error      *string
	LastSyncAt *time.Time
}

type Metadata struct {
	ProviderID string
	EntityType EntityType
	EntityID   string
	Key        string
	Value      []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type AppState struct {
	Key       string
	Value     []byte
	UpdatedAt time.Time
}
