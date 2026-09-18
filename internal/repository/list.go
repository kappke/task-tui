package repository

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
)

// ListReader reads lists by their hierarchy or provider-scoped remote ID.
type ListReader interface {
	Get(ctx context.Context, id domain.ListID) (domain.List, error)
	ListBySpace(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error)
	ListByProvider(ctx context.Context, providerID domain.ProviderID) ([]domain.List, error)
	GetByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.List, error)
}

// ListWriter persists lists and must preserve the list-to-space provider
// invariant on every write.
type ListWriter interface {
	Create(ctx context.Context, list domain.List) (domain.List, error)
	Update(ctx context.Context, list domain.List) (domain.List, error)
	Delete(ctx context.Context, id domain.ListID) error
}

// ListStore is the composed list read/write contract.
type ListStore interface {
	ListReader
	ListWriter
}
