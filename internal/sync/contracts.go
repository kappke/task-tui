package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// ProviderID identifies a provider instance, not just a provider type.
type ProviderID = domain.ProviderID

// OperationID identifies a durable queue operation.
type OperationID = domain.OperationID

// EntityType identifies the normalized entity affected by an operation.
type EntityType = domain.EntityType

const (
	EntityProvider EntityType = "provider"
	EntitySpace    EntityType = domain.EntityTypeSpace
	EntityList     EntityType = domain.EntityTypeList
	EntityTask     EntityType = domain.EntityTypeTask
)

// OperationType identifies a remote mutation.
type OperationType = domain.OperationType

const (
	OperationCreate OperationType = domain.OperationTypeCreate
	OperationUpdate OperationType = domain.OperationTypeUpdate
	OperationDelete OperationType = domain.OperationTypeDelete
)

// OperationStatus is the durable state of a queue operation.
type OperationStatus = domain.SyncStatus

const (
	OperationPending   OperationStatus = domain.SyncStatusPending
	OperationSyncing   OperationStatus = domain.SyncStatusSyncing
	OperationFailed    OperationStatus = domain.SyncStatusFailed
	OperationCompleted OperationStatus = domain.SyncStatusCompleted
)

// Operation is the sync-layer view of a persisted queue row. A repository may
// use a richer domain operation and adapt it to this value inside its sync
// integration boundary.
type Operation struct {
	ID         OperationID
	ProviderID ProviderID

	EntityType EntityType
	EntityID   string
	Operation  OperationType
	// Kind is accepted by adapters that call the operation type Kind. When both
	// fields are set, Operation takes precedence.
	Kind OperationType

	Payload []byte

	Attempts       int
	Status         OperationStatus
	CreatedAt      time.Time
	LastAttemptAt  *time.Time
	NextAttemptAt  time.Time
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	CompletedAt    *time.Time
	Error          string
}

// Type returns the operation type while allowing adapters to use Kind as the
// field name in their own queue representation.
func (o Operation) Type() OperationType {
	if o.Operation != "" {
		return o.Operation
	}
	return o.Kind
}

var (
	// ErrQueueEmpty indicates that no operation for the requested provider is
	// currently eligible for claiming.
	ErrQueueEmpty = domain.ErrQueueEmpty
	// ErrProviderMismatch indicates a provider-isolation violation.
	ErrProviderMismatch = domain.ErrProviderMismatch
	// ErrNonRemoteProvider indicates that a provider cannot be assigned a
	// network worker.
	ErrNonRemoteProvider = errors.New("provider does not support remote synchronization")
	// ErrUnsupported indicates that a requested provider operation is not
	// implemented by the provider adapter.
	ErrUnsupported = domain.ErrUnsupported
	// ErrInvalidPayload marks malformed durable operation data. Retrying the
	// same bytes cannot make a provider request valid.
	ErrInvalidPayload = errors.New("invalid sync payload")
	// ErrWorkerRunning prevents two Run loops from using one worker instance.
	ErrWorkerRunning = errors.New("sync worker is already running")
	// ErrEngineStarted prevents an engine from being started twice without a
	// corresponding stop.
	ErrEngineStarted = errors.New("sync engine is already started")
	// ErrNilContext is returned instead of panicking on an invalid lifecycle
	// call.
	ErrNilContext = errors.New("sync context is nil")
)

// Capabilities describe whether a provider represents a remote service. Local
// providers should leave Remote and Network false. Pull is optional even for a
// remote provider; a provider must also implement Puller or Syncer for it to be
// invoked.
type Capabilities struct {
	Remote  bool
	Network bool
	Pull    bool
	Local   bool
}

// RequiresNetwork reports whether this provider needs a background network
// worker. Local is explicit so an adapter cannot accidentally create a worker
// for a local provider that exposes a transport-shaped implementation.
func (c Capabilities) RequiresNetwork() bool {
	return !c.Local && (c.Remote || c.Network)
}

// Provider is the identity and capability portion of the provider contract.
// Operation execution and pulling are deliberately separate optional
// interfaces so local providers and read-only providers remain small.
type Provider interface {
	ID() ProviderID
	Capabilities() Capabilities
}

// Pusher applies one durable queue operation. The sync layer owns retrying;
// implementations must make one provider request per call and must not embed
// a retry loop.
type Pusher interface {
	Push(context.Context, Operation) error
}

// OperationApplier is an alternate name for provider implementations that use
// Apply for their single-operation boundary.
type OperationApplier interface {
	Apply(context.Context, Operation) error
}

// OperationHandler is useful when an adapter names the operation boundary
// Handle. It is kept separate from Provider so providers without writes remain
// valid providers.
type OperationHandler interface {
	Handle(context.Context, Operation) error
}

// Puller optionally imports remote changes after the push queue is drained.
type Puller interface {
	Pull(context.Context) error
}

// Syncer is accepted for provider contracts whose pull boundary is named
// Sync. It is still called at most once after queued pushes in a sync cycle.
type Syncer interface {
	Sync(context.Context) error
}

// Queue is the consumer-facing durable queue contract used by Worker.
//
// Implementations must make RecoverStale, Claim, and MarkAttempt durable and
// atomic at their respective state transitions. Claim must select only the
// requested provider, preserve created-at/entity ordering, set a lease, and
// return ErrQueueEmpty when no eligible operation exists. MarkAttempt must
// persist the increment before the provider call. Complete, Fail, and Release
// must survive process termination so a crash cannot lose the queue row.
type Queue interface {
	RecoverStale(context.Context, ProviderID, time.Time) error
	Claim(context.Context, ProviderID, time.Time) (Operation, error)
	MarkAttempt(context.Context, OperationID, time.Time) error
	Complete(context.Context, ProviderID, OperationID, string, time.Time) error
	Fail(context.Context, ProviderID, OperationID, string, Failure) error
	Release(context.Context, ProviderID, OperationID, string) error
}

// SyncQueue is an explicit alias for repository packages that use that name.
type SyncQueue = Queue

// QueueFuncs is a small compile adapter for repository implementations and
// tests. Production storage should provide transactional implementations of
// these functions rather than using this type as an in-memory queue.
type QueueFuncs struct {
	RecoverStaleFunc func(context.Context, ProviderID, time.Time) error
	ClaimFunc        func(context.Context, ProviderID, time.Time) (Operation, error)
	MarkAttemptFunc  func(context.Context, OperationID, time.Time) error
	CompleteFunc     func(context.Context, ProviderID, OperationID, string, time.Time) error
	FailFunc         func(context.Context, ProviderID, OperationID, string, Failure) error
	ReleaseFunc      func(context.Context, ProviderID, OperationID, string) error
}

func (q QueueFuncs) RecoverStale(ctx context.Context, providerID ProviderID, before time.Time) error {
	if q.RecoverStaleFunc == nil {
		return nil
	}
	return q.RecoverStaleFunc(ctx, providerID, before)
}

func (q QueueFuncs) Claim(ctx context.Context, providerID ProviderID, now time.Time) (Operation, error) {
	if q.ClaimFunc == nil {
		return Operation{}, errors.New("sync queue Claim is not configured")
	}
	return q.ClaimFunc(ctx, providerID, now)
}

func (q QueueFuncs) MarkAttempt(ctx context.Context, operationID OperationID, at time.Time) error {
	if q.MarkAttemptFunc == nil {
		return errors.New("sync queue MarkAttempt is not configured")
	}
	return q.MarkAttemptFunc(ctx, operationID, at)
}

func (q QueueFuncs) Complete(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, at time.Time) error {
	if q.CompleteFunc == nil {
		return errors.New("sync queue Complete is not configured")
	}
	return q.CompleteFunc(ctx, providerID, operationID, leaseOwner, at)
}

func (q QueueFuncs) Fail(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string, failure Failure) error {
	if q.FailFunc == nil {
		return errors.New("sync queue Fail is not configured")
	}
	return q.FailFunc(ctx, providerID, operationID, leaseOwner, failure)
}

func (q QueueFuncs) Release(ctx context.Context, providerID ProviderID, operationID OperationID, leaseOwner string) error {
	if q.ReleaseFunc == nil {
		return nil
	}
	return q.ReleaseFunc(ctx, providerID, operationID, leaseOwner)
}

// ProviderAdapter turns function boundaries into a Provider, Pusher, and
// optional Puller. It is intended for the boundary between this package and
// the foundation provider.Provider contract when the latter has provider-
// specific write methods.
type ProviderAdapter struct {
	ProviderID ProviderID
	Caps       Capabilities
	PushFunc   func(context.Context, Operation) error
	PullFunc   func(context.Context) error
}

// Adapter is a short name for ProviderAdapter.
type Adapter = ProviderAdapter

func (p ProviderAdapter) ID() ProviderID {
	return p.ProviderID
}

func (p ProviderAdapter) Capabilities() Capabilities {
	return p.Caps
}

func (p ProviderAdapter) Push(ctx context.Context, operation Operation) error {
	if p.PushFunc == nil {
		return fmt.Errorf("%w: push for provider %s", ErrUnsupported, p.ProviderID)
	}
	return p.PushFunc(ctx, operation)
}

func (p ProviderAdapter) Pull(ctx context.Context) error {
	if p.PullFunc == nil {
		return fmt.Errorf("%w: pull for provider %s", ErrUnsupported, p.ProviderID)
	}
	return p.PullFunc(ctx)
}
