package repository

import (
	"context"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// ConflictReader reads unresolved and historical provider-scoped conflicts.
type ConflictReader interface {
	Get(ctx context.Context, id domain.ConflictID) (domain.Conflict, error)
	ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Conflict, error)
}

// ConflictWriter persists conflict records without merging values implicitly.
type ConflictWriter interface {
	Create(ctx context.Context, conflict domain.Conflict) error
	Update(ctx context.Context, conflict domain.Conflict) error
	Delete(ctx context.Context, id domain.ConflictID) error
}

// ConflictResolver is optional for storage implementations that provide a
// dedicated resolution transition.
type ConflictResolver interface {
	Resolve(ctx context.Context, id domain.ConflictID, resolution string, resolvedAt time.Time) error
}

// ConflictStore is the composed conflict persistence contract.
type ConflictStore interface {
	ConflictReader
	ConflictWriter
}

// SyncBaseReader reads the last synchronized baseline for one provider-owned
// entity. The provider ID is required because remote IDs are provider scoped.
type SyncBaseReader interface {
	GetBase(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID string) (domain.SyncBase, error)
}

// SyncBaseWriter persists or removes synchronization baselines.
type SyncBaseWriter interface {
	SaveBase(ctx context.Context, base domain.SyncBase) error
	DeleteBase(ctx context.Context, providerID domain.ProviderID, entityType domain.EntityType, entityID string) error
}

// SyncBaseStore is the composed baseline persistence contract.
type SyncBaseStore interface {
	SyncBaseReader
	SyncBaseWriter
}

// BaseReader is a concise alias for the baseline reader contract.
type BaseReader = SyncBaseReader

// BaseWriter is a concise alias for the baseline writer contract.
type BaseWriter = SyncBaseWriter

// BaseStore is a concise alias for the baseline store contract.
type BaseStore = SyncBaseStore
