package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const spaceSelect = `
	SELECT id, provider_id, remote_id, name, sync_state, remote_updated_at,
		is_deleted, deleted_at, created_at, updated_at
	FROM spaces`

func (s *Store) createSpace(ctx context.Context, space Space) (Space, error) {
	space, err := prepareSpace(space)
	if err != nil {
		return Space{}, err
	}
	if space.ID == "" {
		if err := assignSpaceID(&space); err != nil {
			return Space{}, err
		}
	}
	if err := s.insertSpace(ctx, nil, space); err != nil {
		return Space{}, fmt.Errorf("sqlite: create space %q: %w", space.ID, err)
	}
	return s.getSpace(ctx, s.db, space.ID, "")
}

func (s *Store) getSpaceByID(ctx context.Context, id string) (Space, error) {
	return s.getSpace(ctx, s.db, id, "")
}

func (s *Store) getSpaceByProvider(ctx context.Context, providerID, id string) (Space, error) {
	return s.getSpace(ctx, s.db, id, providerID)
}

func (s *Store) getSpaceByRemoteID(ctx context.Context, providerID, remoteID string) (Space, error) {
	if remoteID == "" {
		return Space{}, fmt.Errorf("sqlite: get space by remote id: empty remote id")
	}
	row := s.db.QueryRowContext(ctx, spaceSelect+" WHERE provider_id = ? AND remote_id = ?", providerID, remoteID)
	space, err := scanSpace(row)
	if err != nil {
		return Space{}, queryError("get space by remote id", providerID+"/"+remoteID, err)
	}
	return space, nil
}

func (s *Store) listSpacesByProvider(ctx context.Context, providerID string) ([]Space, error) {
	return s.listSpaces(ctx, s.db, providerID, false)
}

func (s *Store) listAllSpaces(ctx context.Context, providerID string) ([]Space, error) {
	return s.listSpaces(ctx, s.db, providerID, true)
}

func (s *Store) updateSpace(ctx context.Context, space Space) (Space, error) {
	if space.ID == "" || space.ProviderID == "" {
		return Space{}, fmt.Errorf("sqlite: update space: provider and id are required")
	}
	existing, err := s.getSpaceByProvider(ctx, space.ProviderID, space.ID)
	if err != nil {
		return Space{}, err
	}
	space = mergeSpaceTimes(space, existing)
	if space.SyncState == "" {
		space.SyncState = existing.SyncState
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE spaces SET
			remote_id = ?, name = ?, sync_state = ?, remote_updated_at = ?,
			is_deleted = ?, deleted_at = ?, updated_at = ?
		WHERE provider_id = ? AND id = ?`,
		remoteIDValue(space.RemoteID),
		space.Name,
		space.SyncState,
		nullableTime(space.RemoteUpdatedAt),
		boolInt(space.IsDeleted),
		nullableTime(space.DeletedAt),
		formatTime(space.UpdatedAt),
		space.ProviderID,
		space.ID,
	); err != nil {
		return Space{}, fmt.Errorf("sqlite: update space %q: %w", space.ID, err)
	}
	return s.getSpaceByProvider(ctx, space.ProviderID, space.ID)
}

func (s *Store) upsertSpace(ctx context.Context, space Space) (Space, error) {
	space, err := prepareSpace(space)
	if err != nil {
		return Space{}, err
	}
	if err := s.resolveSpaceID(ctx, &space); err != nil {
		return Space{}, err
	}
	if space.ID == "" {
		if err := assignSpaceID(&space); err != nil {
			return Space{}, err
		}
	}
	if err := s.insertSpace(ctx, nil, space); err != nil {
		if !isConstraintError(err) {
			return Space{}, fmt.Errorf("sqlite: upsert space %q: %w", space.ID, err)
		}
		_, updateErr := s.db.ExecContext(ctx, `
			UPDATE spaces SET remote_id = ?, name = ?, sync_state = ?, remote_updated_at = ?,
				is_deleted = ?, deleted_at = ?, updated_at = ?
			WHERE provider_id = ? AND id = ?`,
			remoteIDValue(space.RemoteID),
			space.Name,
			space.SyncState,
			nullableTime(space.RemoteUpdatedAt),
			boolInt(space.IsDeleted),
			nullableTime(space.DeletedAt),
			formatTime(space.UpdatedAt),
			space.ProviderID,
			space.ID,
		)
		if updateErr != nil {
			return Space{}, fmt.Errorf("sqlite: upsert space %q: %w", space.ID, updateErr)
		}
	}
	return s.getSpaceByProvider(ctx, space.ProviderID, space.ID)
}

func (s *Store) deleteSpaceByProvider(ctx context.Context, providerID, id string) error {
	return s.tombstoneSpace(ctx, providerID, id, nil)
}

func (s *Store) tombstoneSpace(ctx context.Context, providerID, id string, deletedAt *time.Time) error {
	if deletedAt == nil {
		now := time.Now().UTC()
		deletedAt = &now
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE spaces SET is_deleted = 1, deleted_at = ?, sync_state = ?, updated_at = ?
		WHERE provider_id = ? AND id = ?`,
		formatTime(*deletedAt),
		SyncStateSynced,
		formatTime(time.Now()),
		providerID,
		id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: tombstone space %q: %w", id, err)
	}
	return requireAffected(result, "tombstone space", id)
}

func (s *Store) insertSpace(ctx context.Context, tx *sql.Tx, space Space) error {
	exec := execerFor(s.db, tx)
	_, err := exec.ExecContext(ctx, `
		INSERT INTO spaces (
			id, provider_id, remote_id, name, sync_state, remote_updated_at,
			is_deleted, deleted_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		space.ID,
		space.ProviderID,
		remoteIDValue(space.RemoteID),
		space.Name,
		space.SyncState,
		nullableTime(space.RemoteUpdatedAt),
		boolInt(space.IsDeleted),
		nullableTime(space.DeletedAt),
		formatTime(space.CreatedAt),
		formatTime(space.UpdatedAt),
	)
	return err
}

func (s *Store) resolveSpaceID(ctx context.Context, space *Space) error {
	if space.ID != "" {
		return nil
	}
	if space.RemoteID != nil && *space.RemoteID != "" {
		var id string
		err := s.db.QueryRowContext(ctx,
			"SELECT id FROM spaces WHERE provider_id = ? AND remote_id = ?",
			space.ProviderID,
			*space.RemoteID,
		).Scan(&id)
		if err == nil {
			space.ID = id
			return nil
		}
		if !isNoRows(err) {
			return fmt.Errorf("sqlite: resolve space remote id: %w", err)
		}
	}
	id, err := newID()
	if err != nil {
		return fmt.Errorf("sqlite: generate space id: %w", err)
	}
	space.ID = id
	return nil
}

func (s *Store) getSpace(ctx context.Context, queryer queryer, id, providerID string) (Space, error) {
	query := spaceSelect + " WHERE id = ?"
	args := []any{id}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, providerID)
	}
	space, err := scanSpace(queryer.QueryRowContext(ctx, query, args...))
	if err != nil {
		return Space{}, queryError("get space", id, err)
	}
	return space, nil
}

func (s *Store) listSpaces(ctx context.Context, queryer queryer, providerID string, includeDeleted bool) ([]Space, error) {
	query := spaceSelect + " WHERE provider_id = ?"
	args := []any{providerID}
	if !includeDeleted {
		query += " AND is_deleted = 0"
	}
	query += " ORDER BY name, id"
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list spaces for provider %q: %w", providerID, err)
	}
	defer rows.Close()
	spaces := make([]Space, 0)
	for rows.Next() {
		space, err := scanSpace(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan space: %w", err)
		}
		spaces = append(spaces, space)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list spaces: %w", err)
	}
	return spaces, nil
}

func prepareSpace(space Space) (Space, error) {
	if space.ProviderID == "" {
		return Space{}, fmt.Errorf("sqlite: space provider id is empty")
	}
	space.RemoteID = normalizeRemoteID(space.RemoteID)
	if space.CreatedAt.IsZero() {
		space.CreatedAt = time.Now().UTC()
	}
	if space.UpdatedAt.IsZero() {
		space.UpdatedAt = space.CreatedAt
	}
	if space.SyncState == "" {
		space.SyncState = SyncStateLocal
	}
	return space, nil
}

func assignSpaceID(space *Space) error {
	id, err := newID()
	if err != nil {
		return fmt.Errorf("sqlite: generate space id: %w", err)
	}
	space.ID = id
	return nil
}

func mergeSpaceTimes(space, existing Space) Space {
	if space.CreatedAt.IsZero() {
		space.CreatedAt = existing.CreatedAt
	}
	if space.UpdatedAt.IsZero() {
		space.UpdatedAt = time.Now().UTC()
	}
	return space
}

func scanSpace(row rowScanner) (Space, error) {
	var (
		space                      Space
		remoteID                   sql.NullString
		syncState                  string
		remoteUpdatedAt, deletedAt sql.NullString
		isDeleted                  int
		createdAt, updatedAt       sql.NullString
	)
	if err := row.Scan(
		&space.ID,
		&space.ProviderID,
		&remoteID,
		&space.Name,
		&syncState,
		&remoteUpdatedAt,
		&isDeleted,
		&deletedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Space{}, err
	}
	space.RemoteID = nullableString(remoteID)
	space.SyncState = SyncState(syncState)
	space.IsDeleted = isDeleted != 0
	var err error
	if space.RemoteUpdatedAt, err = scanNullableTime(remoteUpdatedAt); err != nil {
		return Space{}, fmt.Errorf("remote updated timestamp: %w", err)
	}
	if space.DeletedAt, err = scanNullableTime(deletedAt); err != nil {
		return Space{}, fmt.Errorf("deleted timestamp: %w", err)
	}
	if space.CreatedAt, err = parseNullableRequiredTime(createdAt); err != nil {
		return Space{}, fmt.Errorf("created timestamp: %w", err)
	}
	if space.UpdatedAt, err = parseNullableRequiredTime(updatedAt); err != nil {
		return Space{}, fmt.Errorf("updated timestamp: %w", err)
	}
	return space, nil
}
