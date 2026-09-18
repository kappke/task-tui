package repository

import (
	"errors"

	"github.com/kappke/task-tui/internal/domain"
)

// Repository aliases the domain error categories so callers can inspect
// errors without importing implementation packages.
var (
	ErrNotFound          = domain.ErrNotFound
	ErrInvalidID         = domain.ErrInvalidID
	ErrInvalidEnum       = domain.ErrInvalidEnum
	ErrProviderMismatch  = domain.ErrProviderMismatch
	ErrInvalidParent     = domain.ErrInvalidParent
	ErrCrossProviderMove = domain.ErrCrossProviderMove
	ErrUnsupported       = domain.ErrUnsupported
	ErrConflict          = domain.ErrConflict
	ErrAlreadyExists     = domain.ErrAlreadyExists
	ErrQueueEmpty        = domain.ErrQueueEmpty
	ErrPartialTransfer   = domain.ErrPartialTransfer

	ErrInvalidFilter = errors.New("invalid task filter")
)
