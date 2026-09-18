package domain

import (
	"fmt"
	"strings"
)

// ProviderID identifies a configured provider instance, not just a provider
// type. Two accounts of the same provider type therefore have different IDs.
type ProviderID string

// SpaceID identifies a space in the local domain model.
type SpaceID string

// ListID identifies a list in the local domain model.
type ListID string

// TaskID identifies a task in the local domain model.
type TaskID string

// OperationID identifies a durable synchronization operation.
type OperationID string

// ConflictID identifies a synchronization conflict.
type ConflictID string

func (id ProviderID) IsZero() bool { return id == "" }

func (id ProviderID) String() string { return string(id) }

func (id ProviderID) Validate() error { return validateID("provider", string(id)) }

// IsValid reports whether the provider ID is non-zero and well formed.
func (id ProviderID) IsValid() bool { return id.Validate() == nil }

// ValidateProviderID validates a provider instance identifier.
func ValidateProviderID(id ProviderID) error { return id.Validate() }

func (id SpaceID) IsZero() bool { return id == "" }

func (id SpaceID) String() string { return string(id) }

func (id SpaceID) Validate() error { return validateID("space", string(id)) }

// IsValid reports whether the space ID is non-zero and well formed.
func (id SpaceID) IsValid() bool { return id.Validate() == nil }

// ValidateSpaceID validates a space identifier.
func ValidateSpaceID(id SpaceID) error { return id.Validate() }

func (id ListID) IsZero() bool { return id == "" }

func (id ListID) String() string { return string(id) }

func (id ListID) Validate() error { return validateID("list", string(id)) }

// IsValid reports whether the list ID is non-zero and well formed.
func (id ListID) IsValid() bool { return id.Validate() == nil }

// ValidateListID validates a list identifier.
func ValidateListID(id ListID) error { return id.Validate() }

func (id TaskID) IsZero() bool { return id == "" }

func (id TaskID) String() string { return string(id) }

func (id TaskID) Validate() error { return validateID("task", string(id)) }

// IsValid reports whether the task ID is non-zero and well formed.
func (id TaskID) IsValid() bool { return id.Validate() == nil }

// ValidateTaskID validates a task identifier.
func ValidateTaskID(id TaskID) error { return id.Validate() }

func (id OperationID) IsZero() bool { return id == "" }

func (id OperationID) String() string { return string(id) }

func (id OperationID) Validate() error { return validateID("operation", string(id)) }

// IsValid reports whether the operation ID is non-zero and well formed.
func (id OperationID) IsValid() bool { return id.Validate() == nil }

// ValidateOperationID validates a synchronization operation identifier.
func ValidateOperationID(id OperationID) error { return id.Validate() }

func (id ConflictID) IsZero() bool { return id == "" }

func (id ConflictID) String() string { return string(id) }

func (id ConflictID) Validate() error { return validateID("conflict", string(id)) }

// IsValid reports whether the conflict ID is non-zero and well formed.
func (id ConflictID) IsValid() bool { return id.Validate() == nil }

// ValidateConflictID validates a synchronization conflict identifier.
func ValidateConflictID(id ConflictID) error { return id.Validate() }

func validateID(kind, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s identifier must be non-empty and trimmed", ErrInvalidID, kind)
	}
	return nil
}

func validateEntityID(kind, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s entity identifier must be non-empty and trimmed", ErrInvalidID, kind)
	}
	return nil
}

func validateRemoteID(remoteID *string) error {
	if remoteID == nil {
		return nil
	}
	if strings.TrimSpace(*remoteID) != *remoteID || *remoteID == "" {
		return fmt.Errorf("%w: remote identifier must be non-empty and trimmed", ErrInvalidID)
	}
	return nil
}

// ValidateRemoteID validates a nullable provider remote identifier.
func ValidateRemoteID(remoteID *string) error { return validateRemoteID(remoteID) }
