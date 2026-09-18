package repository

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
)

// AppStateReader loads persisted application state by stable key.
type AppStateReader interface {
	Load(ctx context.Context, key string) (domain.AppState, error)
}

// AppStateWriter saves or removes persisted application state. Save should be
// an atomic replacement for the supplied key.
type AppStateWriter interface {
	Save(ctx context.Context, state domain.AppState) error
	Delete(ctx context.Context, key string) error
}

// AppStateStore is the composed application-state persistence contract.
type AppStateStore interface {
	AppStateReader
	AppStateWriter
}
