package domain

import "fmt"

// ProviderType identifies the adapter kind used by a provider instance.
type ProviderType string

const (
	ProviderTypeLocal   ProviderType = "local"
	ProviderTypeClickUp ProviderType = "clickup"
	ProviderTypeGitHub  ProviderType = "github"
	ProviderTypeLinear  ProviderType = "linear"
	ProviderTypeJira    ProviderType = "jira"
)

// Short provider constant names are aliases for the type-prefixed names.
const (
	ProviderLocal   = ProviderTypeLocal
	ProviderClickUp = ProviderTypeClickUp
	ProviderGitHub  = ProviderTypeGitHub
	ProviderLinear  = ProviderTypeLinear
	ProviderJira    = ProviderTypeJira
)

// SyncState is the synchronization state displayed for a cached entity.
type SyncState string

const (
	SyncStateSynced   SyncState = "synced"
	SyncStatePending  SyncState = "pending"
	SyncStateSyncing  SyncState = "syncing"
	SyncStateConflict SyncState = "conflict"
	SyncStateFailed   SyncState = "failed"
	SyncStateLocal    SyncState = "local"
)

// EntityType identifies the hierarchy level affected by a synchronization
// operation or conflict.
type EntityType string

const (
	EntityTypeSpace EntityType = "space"
	EntityTypeList  EntityType = "list"
	EntityTypeTask  EntityType = "task"
)

// Short entity constant names are aliases for the type-prefixed names.
const (
	EntitySpace = EntityTypeSpace
	EntityList  = EntityTypeList
	EntityTask  = EntityTypeTask
)

// OperationType identifies a remote-affecting mutation.
type OperationType string

const (
	OperationTypeCreate OperationType = "create"
	OperationTypeUpdate OperationType = "update"
	OperationTypeDelete OperationType = "delete"
)

// Short operation constant names are aliases for the type-prefixed names.
const (
	OperationCreate = OperationTypeCreate
	OperationUpdate = OperationTypeUpdate
	OperationDelete = OperationTypeDelete
)

// SyncStatus identifies the lifecycle state of a queued operation.
type SyncStatus string

const (
	SyncStatusPending   SyncStatus = "pending"
	SyncStatusSyncing   SyncStatus = "syncing"
	SyncStatusFailed    SyncStatus = "failed"
	SyncStatusCompleted SyncStatus = "completed"
)

// Priority is an ordered task priority. None is intentionally lower than
// every explicit priority.
type Priority string

const (
	PriorityNone   Priority = "none"
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

func (v ProviderType) IsZero() bool { return v == "" }

func (v ProviderType) String() string { return string(v) }

func (v ProviderType) IsValid() bool {
	switch v {
	case ProviderTypeLocal, ProviderTypeClickUp, ProviderTypeGitHub, ProviderTypeLinear, ProviderTypeJira:
		return true
	default:
		return false
	}
}

func (v ProviderType) Validate() error { return validateEnum("provider type", v.IsValid(), v) }

// ValidateProviderType validates a provider adapter type.
func ValidateProviderType(v ProviderType) error { return v.Validate() }

func (v SyncState) IsZero() bool { return v == "" }

func (v SyncState) String() string { return string(v) }

func (v SyncState) IsValid() bool {
	switch v {
	case SyncStateSynced, SyncStatePending, SyncStateSyncing, SyncStateConflict, SyncStateFailed, SyncStateLocal:
		return true
	default:
		return false
	}
}

func (v SyncState) Validate() error { return validateEnum("sync state", v.IsValid(), v) }

// ValidateSyncState validates an entity synchronization state.
func ValidateSyncState(v SyncState) error { return v.Validate() }

func (v EntityType) IsZero() bool { return v == "" }

func (v EntityType) String() string { return string(v) }

func (v EntityType) IsValid() bool {
	switch v {
	case EntityTypeSpace, EntityTypeList, EntityTypeTask:
		return true
	default:
		return false
	}
}

func (v EntityType) Validate() error { return validateEnum("entity type", v.IsValid(), v) }

// ValidateEntityType validates a synchronization entity type.
func ValidateEntityType(v EntityType) error { return v.Validate() }

func (v OperationType) IsZero() bool { return v == "" }

func (v OperationType) String() string { return string(v) }

func (v OperationType) IsValid() bool {
	switch v {
	case OperationTypeCreate, OperationTypeUpdate, OperationTypeDelete:
		return true
	default:
		return false
	}
}

func (v OperationType) Validate() error { return validateEnum("operation type", v.IsValid(), v) }

// ValidateOperationType validates a queued operation type.
func ValidateOperationType(v OperationType) error { return v.Validate() }

func (v SyncStatus) IsZero() bool { return v == "" }

func (v SyncStatus) String() string { return string(v) }

func (v SyncStatus) IsValid() bool {
	switch v {
	case SyncStatusPending, SyncStatusSyncing, SyncStatusFailed, SyncStatusCompleted:
		return true
	default:
		return false
	}
}

func (v SyncStatus) Validate() error { return validateEnum("sync status", v.IsValid(), v) }

// ValidateSyncStatus validates a queued operation status.
func ValidateSyncStatus(v SyncStatus) error { return v.Validate() }

func (v Priority) IsZero() bool { return v == "" }

func (v Priority) String() string { return string(v) }

func (v Priority) IsValid() bool {
	switch v {
	case PriorityNone, PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent:
		return true
	default:
		return false
	}
}

func (v Priority) Validate() error { return validateEnum("priority", v.IsValid(), v) }

// ValidatePriority validates a task priority.
func ValidatePriority(v Priority) error { return v.Validate() }

// Rank returns the sortable rank of a priority. Invalid priorities return -1
// so they sort below valid values while still failing Validate.
func (v Priority) Rank() int {
	switch v {
	case PriorityNone:
		return 0
	case PriorityLow:
		return 1
	case PriorityNormal:
		return 2
	case PriorityHigh:
		return 3
	case PriorityUrgent:
		return 4
	default:
		return -1
	}
}

// Compare compares priorities by domain order and returns -1, 0, or 1.
func (v Priority) Compare(other Priority) int { return ComparePriority(v, other) }

// Less reports whether v has a lower priority than other.
func (v Priority) Less(other Priority) bool { return ComparePriority(v, other) < 0 }

// AtLeast reports whether v is at least as important as other. Invalid
// priorities never satisfy the comparison.
func (v Priority) AtLeast(other Priority) bool {
	return v.IsValid() && other.IsValid() && ComparePriority(v, other) >= 0
}

// ComparePriority compares priorities using none < low < normal < high <
// urgent. Callers should validate values received from external boundaries.
func ComparePriority(left, right Priority) int {
	lrank, rrank := left.Rank(), right.Rank()
	switch {
	case lrank < rrank:
		return -1
	case lrank > rrank:
		return 1
	default:
		return 0
	}
}

// Supports reports whether a capability covers a write operation for an
// entity type. Invalid enum values return false.
func (c Capabilities) Supports(entity EntityType, operation OperationType) bool {
	switch entity {
	case EntityTypeSpace:
		switch operation {
		case OperationTypeCreate:
			return c.CreateSpace
		case OperationTypeUpdate:
			return c.UpdateSpace
		case OperationTypeDelete:
			return c.DeleteSpace
		}
	case EntityTypeList:
		switch operation {
		case OperationTypeCreate:
			return c.CreateList
		case OperationTypeUpdate:
			return c.UpdateList
		case OperationTypeDelete:
			return c.DeleteList
		}
	case EntityTypeTask:
		switch operation {
		case OperationTypeCreate:
			return c.CreateTask
		case OperationTypeUpdate:
			return c.UpdateTask
		case OperationTypeDelete:
			return c.DeleteTask
		}
	}
	return false
}

func validateEnum(kind string, valid bool, value fmt.Stringer) error {
	if !valid {
		return fmt.Errorf("%w: invalid %s %q", ErrInvalidEnum, kind, value.String())
	}
	return nil
}
