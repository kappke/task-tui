// Package provider defines the provider boundary used by synchronization and
// application code. Provider-specific API models must remain below this
// boundary.
package provider

import (
	"context"

	"github.com/kappke/task-tui/internal/domain"
)

// Capabilities is re-exported at the provider boundary for callers that do
// not otherwise need to name the domain package.
type Capabilities = domain.Capabilities

// Provider is the common adapter contract for local and remote provider
// instances. Implementations must reject entities whose ProviderID does not
// equal ID(); ownership is part of the provider boundary, not a UI concern.
type Provider interface {
	ID() domain.ProviderID
	Type() domain.ProviderType
	Capabilities() domain.Capabilities

	FetchSpaces(ctx context.Context) ([]domain.Space, error)
	FetchLists(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error)
	FetchTasks(ctx context.Context, listID domain.ListID) ([]domain.Task, error)
	FetchTask(ctx context.Context, taskID domain.TaskID) (domain.Task, error)

	CreateTask(ctx context.Context, task domain.Task) (domain.Task, error)
	UpdateTask(ctx context.Context, task domain.Task) (domain.Task, error)
	DeleteTask(ctx context.Context, task domain.Task) error
}

// ListWriter is an optional provider capability for list mutations.
type ListWriter interface {
	CreateList(ctx context.Context, list domain.List) (domain.List, error)
	UpdateList(ctx context.Context, list domain.List) (domain.List, error)
	DeleteList(ctx context.Context, list domain.List) error
}

// SpaceWriter is an optional provider capability for space mutations.
type SpaceWriter interface {
	CreateSpace(ctx context.Context, space domain.Space) (domain.Space, error)
	UpdateSpace(ctx context.Context, space domain.Space) (domain.Space, error)
	DeleteSpace(ctx context.Context, space domain.Space) error
}

// Puller is an optional provider capability for pulling remote changes into
// local storage. Persistence and conflict handling remain sync-layer concerns.
type Puller interface {
	Pull(ctx context.Context) error
}

// Synchronizer is an optional provider capability for providers that expose a
// complete provider-owned synchronization operation.
type Synchronizer interface {
	Sync(ctx context.Context) error
}

// PullProvider and SyncProvider are concise aliases for the optional
// lifecycle contracts.
type PullProvider = Puller

type SyncProvider = Synchronizer

// Authenticator is an optional provider capability for providers requiring an
// authentication or session check.
type Authenticator interface {
	Authenticate(ctx context.Context) error
}

// Authentication is a descriptive alias for Authenticator.
type Authentication = Authenticator

// AuthProvider is a concise alias for the optional authentication contract.
type AuthProvider = Authenticator

// StatusMetadataProvider exposes provider-owned editor options without adding
// provider-specific fields to the core hierarchy models.
type StatusMetadataProvider interface {
	SpaceStatusMetadata(domain.Space) []domain.ProviderMetadata
	ListStatusMetadata(domain.List) []domain.ProviderMetadata
}

// TaskColumnMetadataProvider exposes provider-neutral dynamic column metadata
// and display values without adding provider-specific fields to core entities.
type TaskColumnMetadataProvider interface {
	ListTaskColumns(domain.List) []domain.ProviderMetadata
	TaskColumnValues(domain.Task) []domain.ProviderMetadata
}
