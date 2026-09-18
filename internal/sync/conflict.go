package sync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// SyncState is the local synchronization state returned by a merge.
type SyncState = domain.SyncState

const (
	SyncStateSynced   SyncState = "synced"
	SyncStatePending  SyncState = "pending"
	SyncStateSyncing  SyncState = "syncing"
	SyncStateConflict SyncState = "conflict"
	SyncStateFailed   SyncState = "failed"
	SyncStateLocal    SyncState = "local"
)

// FieldConflict preserves all three values needed for a later user decision.
// A false Present flag represents a deleted or previously absent field; the
// corresponding Value is nil in that case. Base/Local/Remote aliases are
// populated as well as the explicit Value fields so persistence adapters can
// use either vocabulary without losing data.
type FieldConflict struct {
	ID         string     `json:"id"`
	ProviderID ProviderID `json:"provider_id"`
	EntityType EntityType `json:"entity_type"`
	EntityID   string     `json:"entity_id"`
	Field      string     `json:"field"`

	BaseValue     any  `json:"base_value,omitempty"`
	LocalValue    any  `json:"local_value,omitempty"`
	RemoteValue   any  `json:"remote_value,omitempty"`
	BasePresent   bool `json:"base_present"`
	LocalPresent  bool `json:"local_present"`
	RemotePresent bool `json:"remote_present"`

	Base   any `json:"-"`
	Local  any `json:"-"`
	Remote any `json:"-"`

	State     SyncState `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// MergeInput supplies the previous synchronized base and the current local
// and remote representations. Provider IDs are repeated intentionally: a
// repository can pass the IDs it loaded and the merge rejects cross-provider
// input before producing conflicts.
type MergeInput struct {
	ProviderID       ProviderID
	BaseProviderID   ProviderID
	LocalProviderID  ProviderID
	RemoteProviderID ProviderID
	EntityType       EntityType
	EntityID         string
	Base             map[string]any
	Local            map[string]any
	Remote           map[string]any
	Now              time.Time
}

// MergeResult retains local state for every conflicting field and marks the
// resulting entity as conflict when one or more fields overlap.
type MergeResult struct {
	ProviderID ProviderID
	Fields     map[string]any
	State      SyncState
	Conflicts  []FieldConflict
}

// ConflictStore persists individual field conflicts. Implementations should
// use a transaction when saving conflicts together with the entity state.
type ConflictStore interface {
	SaveConflict(context.Context, FieldConflict) error
}

// ConflictBatchStore is an optional transactional form of ConflictStore.
type ConflictBatchStore interface {
	SaveConflicts(context.Context, []FieldConflict) error
}

// ConflictStateStore is an optional repository hook that marks the retained
// local entity as conflict after its FieldConflict rows are persisted.
type ConflictStateStore interface {
	MarkConflict(context.Context, ProviderID, EntityType, string) error
}

// MergeFields performs a three-way field merge. Non-overlapping changes are
// applied automatically. When both local and remote changed a field from the
// base to different values, the local value wins and a FieldConflict records
// base, local, and remote values.
func MergeFields(input MergeInput) (MergeResult, error) {
	providerID, err := mergeProviderID(input)
	if err != nil {
		return MergeResult{}, err
	}

	result := MergeResult{
		ProviderID: providerID,
		Fields:     cloneFields(input.Local),
		State:      SyncStateSynced,
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	keys := make(map[string]struct{}, len(input.Base)+len(input.Local)+len(input.Remote))
	for field := range input.Base {
		keys[field] = struct{}{}
	}
	for field := range input.Local {
		keys[field] = struct{}{}
	}
	for field := range input.Remote {
		keys[field] = struct{}{}
	}
	fields := make([]string, 0, len(keys))
	for field := range keys {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	for _, field := range fields {
		base, basePresent := input.Base[field]
		local, localPresent := input.Local[field]
		remote, remotePresent := input.Remote[field]

		localMatchesBase := sameValue(local, localPresent, base, basePresent)
		remoteMatchesBase := sameValue(remote, remotePresent, base, basePresent)
		localMatchesRemote := sameValue(local, localPresent, remote, remotePresent)

		switch {
		case localMatchesBase && !remoteMatchesBase:
			setField(result.Fields, field, remote, remotePresent)
		case remoteMatchesBase:
			setField(result.Fields, field, local, localPresent)
		case localMatchesRemote:
			setField(result.Fields, field, local, localPresent)
		default:
			conflict := newFieldConflict(providerID, input.EntityType, input.EntityID, field,
				base, basePresent, local, localPresent, remote, remotePresent, now)
			result.Conflicts = append(result.Conflicts, conflict)
			// The local value is deliberately retained in result.Fields.
		}
	}

	if len(result.Conflicts) > 0 {
		result.State = SyncStateConflict
	}
	return result, nil
}

// ThreeWayMerge is a concise wrapper for callers that have one provider ID.
func ThreeWayMerge(providerID ProviderID, entityType EntityType, entityID string, base, local, remote map[string]any) (MergeResult, error) {
	return MergeFields(MergeInput{
		ProviderID: providerID,
		EntityType: entityType,
		EntityID:   entityID,
		Base:       base,
		Local:      local,
		Remote:     remote,
	})
}

// MergeAndPersist merges fields and persists every generated conflict. If the
// repository supports a batch method it is used so all conflict rows can be
// committed atomically; otherwise SaveConflict is called in deterministic
// field order.
func MergeAndPersist(ctx context.Context, input MergeInput, store ConflictStore) (MergeResult, error) {
	if ctx == nil {
		return MergeResult{}, ErrNilContext
	}
	if store == nil {
		return MergeResult{}, errors.New("sync conflict store is nil")
	}
	result, err := MergeFields(input)
	if err != nil {
		return MergeResult{}, err
	}
	if len(result.Conflicts) == 0 {
		return result, nil
	}

	if batch, ok := store.(ConflictBatchStore); ok {
		if err := batch.SaveConflicts(ctx, result.Conflicts); err != nil {
			return MergeResult{}, fmt.Errorf("persist field conflicts: %w", err)
		}
	} else {
		for _, conflict := range result.Conflicts {
			if err := store.SaveConflict(ctx, conflict); err != nil {
				return MergeResult{}, fmt.Errorf("persist field conflict %s: %w", conflict.ID, err)
			}
		}
	}
	if marker, ok := store.(ConflictStateStore); ok {
		if err := marker.MarkConflict(ctx, result.ProviderID, input.EntityType, input.EntityID); err != nil {
			return MergeResult{}, fmt.Errorf("mark entity conflict: %w", err)
		}
	}
	return result, nil
}

func mergeProviderID(input MergeInput) (ProviderID, error) {
	ids := []ProviderID{
		input.ProviderID,
		input.BaseProviderID,
		input.LocalProviderID,
		input.RemoteProviderID,
	}
	var providerID ProviderID
	for _, id := range ids {
		if id == "" {
			continue
		}
		if providerID == "" {
			providerID = id
			continue
		}
		if providerID != id {
			return "", fmt.Errorf("%w: merge values belong to %s and %s", ErrProviderMismatch, providerID, id)
		}
	}
	if providerID == "" {
		return "", errors.New("sync merge provider ID is empty")
	}
	return providerID, nil
}

func newFieldConflict(providerID ProviderID, entityType EntityType, entityID, field string,
	base any, basePresent bool, local any, localPresent bool, remote any, remotePresent bool,
	now time.Time) FieldConflict {
	return FieldConflict{
		ID:            conflictID(providerID, entityType, entityID, field),
		ProviderID:    providerID,
		EntityType:    entityType,
		EntityID:      entityID,
		Field:         field,
		BaseValue:     base,
		LocalValue:    local,
		RemoteValue:   remote,
		BasePresent:   basePresent,
		LocalPresent:  localPresent,
		RemotePresent: remotePresent,
		Base:          base,
		Local:         local,
		Remote:        remote,
		State:         SyncStateConflict,
		CreatedAt:     now,
	}
}

func conflictID(providerID ProviderID, entityType EntityType, entityID, field string) string {
	return fmt.Sprintf("%s:%s:%s:%s", providerID, entityType, entityID, field)
}

func sameValue(left any, leftPresent bool, right any, rightPresent bool) bool {
	return leftPresent == rightPresent && reflect.DeepEqual(left, right)
}

func setField(fields map[string]any, field string, value any, present bool) {
	if present {
		fields[field] = value
		return
	}
	delete(fields, field)
}

func cloneFields(fields map[string]any) map[string]any {
	clone := make(map[string]any, len(fields))
	for field, value := range fields {
		clone[field] = value
	}
	return clone
}
