package repository

import (
	"context"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// SyncQueue is the provider-isolated durable operation lifecycle. Claim is
// atomic: it returns one eligible pending operation for providerID and marks it
// syncing before returning. If none is eligible it returns ErrQueueEmpty.
type SyncQueue interface {
	Enqueue(ctx context.Context, operation domain.SyncOperation) error
	Claim(ctx context.Context, providerID domain.ProviderID) (domain.SyncOperation, error)
	Complete(ctx context.Context, operationID domain.OperationID) error
	Retry(ctx context.Context, operationID domain.OperationID, cause error) error
	Fail(ctx context.Context, operationID domain.OperationID, cause error) error
	RequeueStale(ctx context.Context, providerID domain.ProviderID, before time.Time) (int, error)
	PendingCount(ctx context.Context, providerID domain.ProviderID) (int, error)
}

// ScheduledSyncQueue adds explicit scheduling for implementations that let
// the synchronization worker calculate backoff and jitter itself.
type ScheduledSyncQueue interface {
	SyncQueue
	RetryAt(ctx context.Context, operationID domain.OperationID, nextAttemptAt time.Time, cause error) error
}

// Queue is a concise alias for consumers that refer to the queue directly.
type Queue = SyncQueue
