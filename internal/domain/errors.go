package domain

import "errors"

// Sentinel errors describe stable domain-level failure categories. Callers
// should inspect them with errors.Is rather than comparing error strings.
var (
	ErrNotFound          = errors.New("not found")
	ErrInvalidID         = errors.New("invalid id")
	ErrInvalidEnum       = errors.New("invalid enum value")
	ErrProviderMismatch  = errors.New("provider mismatch")
	ErrInvalidParent     = errors.New("invalid parent")
	ErrLastTaskList      = errors.New("cannot remove a task's last list")
	ErrCrossProviderMove = errors.New("cross-provider move")
	ErrUnsupported       = errors.New("unsupported")
	ErrConflict          = errors.New("conflict")
	ErrAlreadyExists     = errors.New("already exists")
	ErrQueueEmpty        = errors.New("queue empty")
	ErrPartialTransfer   = errors.New("partial transfer")
)
