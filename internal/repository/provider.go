package repository

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
)

// ProviderReader reads configured provider instances. List returns all
// configured instances, including disabled ones, so callers can render and
// manage provider state explicitly.
type ProviderReader interface {
	Get(ctx context.Context, id domain.ProviderID) (domain.Provider, error)
	List(ctx context.Context) ([]domain.Provider, error)
}

// ProviderWriter persists provider instances. Create must reject an existing
// ID with ErrAlreadyExists; Delete removes configuration and provider-owned
// state only according to the application's explicit deletion policy.
type ProviderWriter interface {
	Create(ctx context.Context, provider domain.Provider) (domain.Provider, error)
	Update(ctx context.Context, provider domain.Provider) (domain.Provider, error)
	Delete(ctx context.Context, id domain.ProviderID) error
}

// ProviderStore is the composed provider read/write contract.
type ProviderStore interface {
	ProviderReader
	ProviderWriter
}

// ProviderTypeReader is an optional query contract for callers that need to
// discover provider instances by adapter type.
type ProviderTypeReader interface {
	ListByType(ctx context.Context, providerType domain.ProviderType) ([]domain.Provider, error)
}
