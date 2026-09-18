package repository

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
)

// SpaceReader reads spaces with provider-scoped remote lookups.
type SpaceReader interface {
	Get(ctx context.Context, id domain.SpaceID) (domain.Space, error)
	ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.Space, error)
	GetByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.Space, error)
}

// SpaceWriter persists spaces. Implementations must reject a space whose
// ProviderID does not identify an existing provider.
type SpaceWriter interface {
	Create(ctx context.Context, space domain.Space) (domain.Space, error)
	Update(ctx context.Context, space domain.Space) (domain.Space, error)
	Delete(ctx context.Context, id domain.SpaceID) error
}

// SpaceStore is the composed space read/write contract.
type SpaceStore interface {
	SpaceReader
	SpaceWriter
}
