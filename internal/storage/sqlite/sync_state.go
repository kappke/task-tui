package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) putMetadata(ctx context.Context, metadata Metadata) (Metadata, error) {
	metadata, err := prepareMetadata(metadata)
	if err != nil {
		return Metadata{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO provider_metadata (
			provider_id, entity_type, entity_id, key, value, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider_id, entity_type, entity_id, key) DO UPDATE SET
			value = excluded.value, updated_at = excluded.updated_at`,
		metadata.ProviderID,
		metadata.EntityType,
		metadata.EntityID,
		metadata.Key,
		metadata.Value,
		formatTime(metadata.CreatedAt),
		formatTime(metadata.UpdatedAt),
	)
	if err != nil {
		return Metadata{}, fmt.Errorf("sqlite: put metadata %q: %w", metadata.Key, err)
	}
	return s.getMetadata(ctx, metadata.ProviderID, metadata.EntityType, metadata.EntityID, metadata.Key)
}

func (s *Store) upsertMetadata(ctx context.Context, metadata Metadata) (Metadata, error) {
	return s.putMetadata(ctx, metadata)
}

func (s *Store) getMetadata(ctx context.Context, providerID string, entityType EntityType, entityID, key string) (Metadata, error) {
	metadata, err := scanMetadata(s.db.QueryRowContext(ctx, `
		SELECT provider_id, entity_type, entity_id, key, value, created_at, updated_at
		FROM provider_metadata
		WHERE provider_id = ? AND entity_type = ? AND entity_id = ? AND key = ?`,
		providerID, entityType, entityID, key))
	if err != nil {
		return Metadata{}, queryError("get metadata", providerID+"/"+entityID+"/"+key, err)
	}
	return metadata, nil
}

func (s *Store) listMetadata(ctx context.Context, providerID string, entityType EntityType, entityID string) ([]Metadata, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider_id, entity_type, entity_id, key, value, created_at, updated_at
		FROM provider_metadata
		WHERE provider_id = ? AND entity_type = ? AND entity_id = ?
		ORDER BY key`, providerID, entityType, entityID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list metadata: %w", err)
	}
	defer rows.Close()
	items := make([]Metadata, 0)
	for rows.Next() {
		item, err := scanMetadata(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan metadata: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list metadata: %w", err)
	}
	return items, nil
}

func (s *Store) deleteMetadata(ctx context.Context, providerID string, entityType EntityType, entityID, key string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM provider_metadata
		WHERE provider_id = ? AND entity_type = ? AND entity_id = ? AND key = ?`,
		providerID, entityType, entityID, key)
	if err != nil {
		return fmt.Errorf("sqlite: delete metadata %q: %w", key, err)
	}
	return requireAffected(result, "delete metadata", key)
}

func prepareMetadata(metadata Metadata) (Metadata, error) {
	if metadata.ProviderID == "" || metadata.EntityType == "" || metadata.EntityID == "" || metadata.Key == "" {
		return Metadata{}, errors.New("sqlite: metadata provider, entity, and key are required")
	}
	if metadata.Value == nil {
		metadata.Value = []byte{}
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = time.Now().UTC()
	}
	if metadata.UpdatedAt.IsZero() {
		metadata.UpdatedAt = metadata.CreatedAt
	}
	return metadata, nil
}

func scanMetadata(row rowScanner) (Metadata, error) {
	var (
		metadata             Metadata
		entityType           string
		createdAt, updatedAt sql.NullString
	)
	if err := row.Scan(
		&metadata.ProviderID, &entityType, &metadata.EntityID, &metadata.Key,
		&metadata.Value, &createdAt, &updatedAt,
	); err != nil {
		return Metadata{}, err
	}
	metadata.EntityType = EntityType(entityType)
	metadata.Value = append([]byte(nil), metadata.Value...)
	var err error
	if metadata.CreatedAt, err = parseNullableRequiredTime(createdAt); err != nil {
		return Metadata{}, err
	}
	if metadata.UpdatedAt, err = parseNullableRequiredTime(updatedAt); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func (s *Store) putSyncBase(ctx context.Context, base SyncBase) (SyncBase, error) {
	base, err := prepareSyncBase(base)
	if err != nil {
		return SyncBase{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sync_bases (
			provider_id, entity_type, entity_id, remote_id, sync_state, remote_updated_at,
			payload, remote_version, captured_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider_id, entity_type, entity_id) DO UPDATE SET
			remote_id = excluded.remote_id, sync_state = excluded.sync_state,
			remote_updated_at = excluded.remote_updated_at, payload = excluded.payload,
			remote_version = excluded.remote_version,
			captured_at = excluded.captured_at, updated_at = excluded.updated_at`,
		base.ProviderID,
		base.EntityType,
		base.EntityID,
		remoteIDValue(base.RemoteID),
		base.SyncState,
		nullableTime(base.RemoteUpdatedAt),
		base.Payload,
		base.RemoteVersion,
		formatTime(base.CapturedAt),
		formatTime(base.UpdatedAt),
	)
	if err != nil {
		return SyncBase{}, fmt.Errorf("sqlite: put sync base %q: %w", base.EntityID, err)
	}
	return s.getSyncBase(ctx, base.ProviderID, base.EntityType, base.EntityID)
}

func (s *Store) upsertSyncBase(ctx context.Context, base SyncBase) (SyncBase, error) {
	return s.putSyncBase(ctx, base)
}

func (s *Store) getSyncBase(ctx context.Context, providerID string, entityType EntityType, entityID string) (SyncBase, error) {
	base, err := scanSyncBase(s.db.QueryRowContext(ctx, `
		SELECT provider_id, entity_type, entity_id, remote_id, sync_state, remote_updated_at,
			payload, remote_version, captured_at, updated_at
		FROM sync_bases WHERE provider_id = ? AND entity_type = ? AND entity_id = ?`,
		providerID, entityType, entityID))
	if err != nil {
		return SyncBase{}, queryError("get sync base", providerID+"/"+entityID, err)
	}
	return base, nil
}

func (s *Store) listSyncBases(ctx context.Context, providerID string) ([]SyncBase, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider_id, entity_type, entity_id, remote_id, sync_state, remote_updated_at,
			payload, remote_version, captured_at, updated_at
		FROM sync_bases WHERE provider_id = ? ORDER BY entity_type, entity_id`, providerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list sync bases: %w", err)
	}
	defer rows.Close()
	bases := make([]SyncBase, 0)
	for rows.Next() {
		base, err := scanSyncBase(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan sync base: %w", err)
		}
		bases = append(bases, base)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list sync bases: %w", err)
	}
	return bases, nil
}

func (s *Store) deleteSyncBase(ctx context.Context, providerID string, entityType EntityType, entityID string) error {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM sync_bases WHERE provider_id = ? AND entity_type = ? AND entity_id = ?",
		providerID, entityType, entityID)
	if err != nil {
		return fmt.Errorf("sqlite: delete sync base %q: %w", entityID, err)
	}
	return requireAffected(result, "delete sync base", entityID)
}

func prepareSyncBase(base SyncBase) (SyncBase, error) {
	if base.ProviderID == "" || base.EntityType == "" || base.EntityID == "" {
		return SyncBase{}, errors.New("sqlite: sync base provider and entity are required")
	}
	if base.Payload == nil {
		base.Payload = []byte{}
	}
	base.RemoteID = normalizeRemoteID(base.RemoteID)
	if base.SyncState == "" {
		base.SyncState = SyncStateLocal
	}
	if base.CapturedAt.IsZero() {
		base.CapturedAt = time.Now().UTC()
	}
	if base.UpdatedAt.IsZero() {
		base.UpdatedAt = base.CapturedAt
	}
	return base, nil
}

func scanSyncBase(row rowScanner) (SyncBase, error) {
	var (
		base                      SyncBase
		entityType, syncState     string
		remoteID, remoteUpdatedAt sql.NullString
		remoteVersion             sql.NullString
		capturedAt, updatedAt     sql.NullString
	)
	if err := row.Scan(&base.ProviderID, &entityType, &base.EntityID, &remoteID, &syncState,
		&remoteUpdatedAt, &base.Payload, &remoteVersion, &capturedAt, &updatedAt); err != nil {
		return SyncBase{}, err
	}
	base.EntityType = EntityType(entityType)
	base.RemoteID = nullableString(remoteID)
	base.SyncState = SyncState(syncState)
	var err error
	if base.RemoteUpdatedAt, err = scanNullableTime(remoteUpdatedAt); err != nil {
		return SyncBase{}, err
	}
	base.Payload = append([]byte(nil), base.Payload...)
	base.RemoteVersion = nullableString(remoteVersion)
	if base.CapturedAt, err = parseNullableRequiredTime(capturedAt); err != nil {
		return SyncBase{}, err
	}
	if base.UpdatedAt, err = parseNullableRequiredTime(updatedAt); err != nil {
		return SyncBase{}, err
	}
	return base, nil
}

func (s *Store) createConflict(ctx context.Context, conflict Conflict) (Conflict, error) {
	conflict, err := prepareConflict(conflict)
	if err != nil {
		return Conflict{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO conflicts (
			id, provider_id, entity_type, entity_id, base_value, local_value, remote_value,
			status, resolution, created_at, updated_at, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		conflict.ID, conflict.ProviderID, conflict.EntityType, conflict.EntityID,
		conflict.BaseValue, conflict.LocalValue, conflict.RemoteValue, conflict.Status,
		conflict.Resolution, formatTime(conflict.CreatedAt), formatTime(conflict.UpdatedAt),
		nullableTime(conflict.ResolvedAt))
	if err != nil {
		return Conflict{}, fmt.Errorf("sqlite: create conflict %q: %w", conflict.ID, err)
	}
	return s.getConflict(ctx, conflict.ProviderID, conflict.ID)
}

func (s *Store) upsertConflict(ctx context.Context, conflict Conflict) (Conflict, error) {
	conflict, err := prepareConflict(conflict)
	if err != nil {
		return Conflict{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO conflicts (
			id, provider_id, entity_type, entity_id, base_value, local_value, remote_value,
			status, resolution, created_at, updated_at, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			provider_id = excluded.provider_id, entity_type = excluded.entity_type,
			entity_id = excluded.entity_id, base_value = excluded.base_value,
			local_value = excluded.local_value, remote_value = excluded.remote_value,
			status = excluded.status, resolution = excluded.resolution,
			updated_at = excluded.updated_at, resolved_at = excluded.resolved_at`,
		conflict.ID, conflict.ProviderID, conflict.EntityType, conflict.EntityID,
		conflict.BaseValue, conflict.LocalValue, conflict.RemoteValue, conflict.Status,
		conflict.Resolution, formatTime(conflict.CreatedAt), formatTime(conflict.UpdatedAt),
		nullableTime(conflict.ResolvedAt))
	if err != nil {
		return Conflict{}, fmt.Errorf("sqlite: upsert conflict %q: %w", conflict.ID, err)
	}
	return s.getConflict(ctx, conflict.ProviderID, conflict.ID)
}

func (s *Store) getConflict(ctx context.Context, providerID, id string) (Conflict, error) {
	conflict, err := scanConflict(s.db.QueryRowContext(ctx, `
		SELECT id, provider_id, entity_type, entity_id, base_value, local_value, remote_value,
			status, resolution, created_at, updated_at, resolved_at
		FROM conflicts WHERE provider_id = ? AND id = ?`, providerID, id))
	if err != nil {
		return Conflict{}, queryError("get conflict", providerID+"/"+id, err)
	}
	return conflict, nil
}

func (s *Store) getConflictByID(ctx context.Context, id string) (Conflict, error) {
	conflict, err := scanConflict(s.db.QueryRowContext(ctx, `
		SELECT id, provider_id, entity_type, entity_id, base_value, local_value, remote_value,
			status, resolution, created_at, updated_at, resolved_at
		FROM conflicts WHERE id = ?`, id))
	if err != nil {
		return Conflict{}, queryError("get conflict", id, err)
	}
	return conflict, nil
}

func (s *Store) listConflicts(ctx context.Context, providerID string, includeResolved bool) ([]Conflict, error) {
	query := `
		SELECT id, provider_id, entity_type, entity_id, base_value, local_value, remote_value,
			status, resolution, created_at, updated_at, resolved_at
		FROM conflicts WHERE provider_id = ?`
	if !includeResolved {
		query += " AND status = 'open'"
	}
	query += " ORDER BY updated_at DESC, id"
	rows, err := s.db.QueryContext(ctx, query, providerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list conflicts: %w", err)
	}
	defer rows.Close()
	conflicts := make([]Conflict, 0)
	for rows.Next() {
		conflict, err := scanConflict(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan conflict: %w", err)
		}
		conflicts = append(conflicts, conflict)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list conflicts: %w", err)
	}
	return conflicts, nil
}

func (s *Store) resolveConflict(ctx context.Context, providerID, id string, resolution []byte) (Conflict, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE conflicts SET status = 'resolved', resolution = ?, resolved_at = ?, updated_at = ?
		WHERE provider_id = ? AND id = ?`, resolution, formatTime(now), formatTime(now), providerID, id)
	if err != nil {
		return Conflict{}, fmt.Errorf("sqlite: resolve conflict %q: %w", id, err)
	}
	if err := requireAffected(result, "resolve conflict", id); err != nil {
		return Conflict{}, err
	}
	return s.getConflict(ctx, providerID, id)
}

func prepareConflict(conflict Conflict) (Conflict, error) {
	if conflict.ID == "" {
		id, err := newID()
		if err != nil {
			return Conflict{}, fmt.Errorf("sqlite: generate conflict id: %w", err)
		}
		conflict.ID = id
	}
	if conflict.ProviderID == "" || conflict.EntityType == "" || conflict.EntityID == "" {
		return Conflict{}, errors.New("sqlite: conflict provider and entity are required")
	}
	if conflict.LocalValue == nil {
		conflict.LocalValue = []byte{}
	}
	if conflict.RemoteValue == nil {
		conflict.RemoteValue = []byte{}
	}
	if conflict.Status == "" {
		conflict.Status = "open"
	}
	if conflict.CreatedAt.IsZero() {
		conflict.CreatedAt = time.Now().UTC()
	}
	if conflict.UpdatedAt.IsZero() {
		conflict.UpdatedAt = conflict.CreatedAt
	}
	return conflict, nil
}

func scanConflict(row rowScanner) (Conflict, error) {
	var (
		conflict                         Conflict
		entityType, status               string
		baseValue, resolution            []byte
		createdAt, updatedAt, resolvedAt sql.NullString
	)
	if err := row.Scan(&conflict.ID, &conflict.ProviderID, &entityType, &conflict.EntityID,
		&baseValue, &conflict.LocalValue, &conflict.RemoteValue, &status, &resolution,
		&createdAt, &updatedAt, &resolvedAt); err != nil {
		return Conflict{}, err
	}
	conflict.EntityType, conflict.Status = EntityType(entityType), status
	conflict.BaseValue, conflict.Resolution = append([]byte(nil), baseValue...), append([]byte(nil), resolution...)
	conflict.LocalValue, conflict.RemoteValue = append([]byte(nil), conflict.LocalValue...), append([]byte(nil), conflict.RemoteValue...)
	var err error
	if conflict.CreatedAt, err = parseNullableRequiredTime(createdAt); err != nil {
		return Conflict{}, err
	}
	if conflict.UpdatedAt, err = parseNullableRequiredTime(updatedAt); err != nil {
		return Conflict{}, err
	}
	if conflict.ResolvedAt, err = scanNullableTime(resolvedAt); err != nil {
		return Conflict{}, err
	}
	return conflict, nil
}

func (s *Store) SetAppState(ctx context.Context, state AppState) (AppState, error) {
	if state.Key == "" {
		return AppState{}, errors.New("sqlite: app state key is empty")
	}
	if state.Value == nil {
		state.Value = []byte{}
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_state (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		state.Key, state.Value, formatTime(state.UpdatedAt))
	if err != nil {
		return AppState{}, fmt.Errorf("sqlite: set app state %q: %w", state.Key, err)
	}
	return s.GetAppState(ctx, state.Key)
}

func (s *Store) PutAppState(ctx context.Context, state AppState) (AppState, error) {
	return s.SetAppState(ctx, state)
}

func (s *Store) GetAppState(ctx context.Context, key string) (AppState, error) {
	var state AppState
	var updatedAt sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT key, value, updated_at FROM app_state WHERE key = ?", key).
		Scan(&state.Key, &state.Value, &updatedAt)
	if err != nil {
		return AppState{}, queryError("get app state", key, err)
	}
	state.Value = append([]byte(nil), state.Value...)
	state.UpdatedAt, err = parseNullableRequiredTime(updatedAt)
	if err != nil {
		return AppState{}, err
	}
	return state, nil
}

func (s *Store) ListAppState(ctx context.Context) ([]AppState, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value, updated_at FROM app_state ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list app state: %w", err)
	}
	defer rows.Close()
	states := make([]AppState, 0)
	for rows.Next() {
		var state AppState
		var updatedAt sql.NullString
		if err := rows.Scan(&state.Key, &state.Value, &updatedAt); err != nil {
			return nil, err
		}
		state.Value = append([]byte(nil), state.Value...)
		state.UpdatedAt, err = parseNullableRequiredTime(updatedAt)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list app state: %w", err)
	}
	return states, nil
}

func (s *Store) DeleteAppState(ctx context.Context, key string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM app_state WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("sqlite: delete app state %q: %w", key, err)
	}
	return requireAffected(result, "delete app state", key)
}
