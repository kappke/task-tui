package app

import (
	"errors"
	"fmt"

	"github.com/kappke/task-tui/internal/domain"
)

var (
	ErrInvalidInput                = errors.New("invalid input")
	ErrInvalidEntity               = errors.New("invalid entity")
	ErrNotFound                    = domain.ErrNotFound
	ErrProviderMismatch            = domain.ErrProviderMismatch
	ErrInvalidParent               = domain.ErrInvalidParent
	ErrCrossProviderMove           = domain.ErrCrossProviderMove
	ErrUnsupported                 = domain.ErrUnsupported
	ErrProviderNotFound            = errors.New("provider not found")
	ErrProviderRegistryUnavailable = errors.New("provider registry unavailable")
	ErrRepositoryUnavailable       = errors.New("repository unavailable")
	ErrNilContext                  = errors.New("nil context")
	ErrIDCollision                 = errors.New("generated identity collides with an existing identity")
	ErrQueryUnavailable            = errors.New("local query repository unavailable")
	ErrAtomicMutationUnavailable   = errors.New("atomic mutation repository unavailable")
	ErrPartialTransfer             = domain.ErrPartialTransfer
	ErrProviderAlreadyRegistered   = errors.New("provider already registered")
)

// UnsupportedError identifies the provider capability that was requested.
type UnsupportedError struct {
	ProviderID ProviderID
	Capability Capability
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("provider %s does not support %s", e.ProviderID, e.Capability)
}

func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }

// PartialTransferError reports a destination that was created before deleting
// the source failed. The destination is intentionally retained; rolling it
// back would make a successful remote create impossible to reason about.
type PartialTransferError struct {
	Destination Task
	Err         error
}

func (e *PartialTransferError) Error() string {
	if e.Err == nil {
		return ErrPartialTransfer.Error()
	}
	return fmt.Sprintf("%s: %v", ErrPartialTransfer, e.Err)
}

func (e *PartialTransferError) Unwrap() error { return e.Err }

func (e *PartialTransferError) Is(target error) bool {
	return target == ErrPartialTransfer || (e.Err != nil && errors.Is(e.Err, target))
}
