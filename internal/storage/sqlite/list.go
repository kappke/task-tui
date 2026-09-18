package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const listSelect = `
	SELECT id, provider_id, space_id, remote_id, name, sync_state, remote_updated_at,
		is_deleted, deleted_at, created_at, updated_at
	FROM lists`

func (s *Store) createList(ctx context.Context, list List) (List, error) {
	list, err := prepareList(list)
	if err != nil {
		return List{}, err
	}
	if list.ID == "" {
		if err := assignListID(&list); err != nil {
			return List{}, err
		}
	}
	if err := s.insertList(ctx, nil, list); err != nil {
		return List{}, fmt.Errorf("sqlite: create list %q: %w", list.ID, err)
	}
	return s.getList(ctx, s.db, list.ID, list.ProviderID)
}

func (s *Store) getListByID(ctx context.Context, id string) (List, error) {
	return s.getList(ctx, s.db, id, "")
}

func (s *Store) getListByProvider(ctx context.Context, providerID, id string) (List, error) {
	return s.getList(ctx, s.db, id, providerID)
}

func (s *Store) getListByRemoteID(ctx context.Context, providerID, remoteID string) (List, error) {
	if remoteID == "" {
		return List{}, fmt.Errorf("sqlite: get list by remote id: empty remote id")
	}
	list, err := scanList(s.db.QueryRowContext(ctx,
		listSelect+" WHERE provider_id = ? AND remote_id = ?", providerID, remoteID))
	if err != nil {
		return List{}, queryError("get list by remote id", providerID+"/"+remoteID, err)
	}
	return list, nil
}

func (s *Store) listLists(ctx context.Context, providerID, spaceID string) ([]List, error) {
	query := listSelect + " WHERE provider_id = ?"
	args := []any{providerID}
	if spaceID != "" {
		query += " AND space_id = ?"
		args = append(args, spaceID)
	}
	query += " AND is_deleted = 0 ORDER BY name, id"
	return listQuery(ctx, s.db, query, args...)
}

func (s *Store) listListsBySpace(ctx context.Context, providerID, spaceID string) ([]List, error) {
	return s.listLists(ctx, providerID, spaceID)
}

func (s *Store) listListsBySpaceID(ctx context.Context, spaceID string) ([]List, error) {
	return listQuery(ctx, s.db,
		listSelect+" WHERE space_id = ? AND is_deleted = 0 ORDER BY name, id", spaceID)
}

func (s *Store) listAllLists(ctx context.Context, providerID string) ([]List, error) {
	return listQuery(ctx, s.db,
		listSelect+" WHERE provider_id = ? AND is_deleted = 0 ORDER BY name, id", providerID)
}

func (s *Store) updateList(ctx context.Context, list List) (List, error) {
	if list.ID == "" || list.ProviderID == "" {
		return List{}, fmt.Errorf("sqlite: update list: provider and id are required")
	}
	existing, err := s.getListByProvider(ctx, list.ProviderID, list.ID)
	if err != nil {
		return List{}, err
	}
	list = mergeListTimes(list, existing)
	if list.SyncState == "" {
		list.SyncState = existing.SyncState
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE lists SET
			space_id = ?, remote_id = ?, name = ?, sync_state = ?, remote_updated_at = ?,
			is_deleted = ?, deleted_at = ?, updated_at = ?
		WHERE provider_id = ? AND id = ?`,
		list.SpaceID,
		remoteIDValue(list.RemoteID),
		list.Name,
		list.SyncState,
		nullableTime(list.RemoteUpdatedAt),
		boolInt(list.IsDeleted),
		nullableTime(list.DeletedAt),
		formatTime(list.UpdatedAt),
		list.ProviderID,
		list.ID,
	)
	if err != nil {
		return List{}, fmt.Errorf("sqlite: update list %q: %w", list.ID, err)
	}
	return s.getListByProvider(ctx, list.ProviderID, list.ID)
}

func (s *Store) upsertList(ctx context.Context, list List) (List, error) {
	list, err := prepareList(list)
	if err != nil {
		return List{}, err
	}
	if err := s.resolveListID(ctx, &list); err != nil {
		return List{}, err
	}
	if list.ID == "" {
		if err := assignListID(&list); err != nil {
			return List{}, err
		}
	}
	if err := s.insertList(ctx, nil, list); err != nil {
		if !isConstraintError(err) {
			return List{}, fmt.Errorf("sqlite: upsert list %q: %w", list.ID, err)
		}
		if _, updateErr := s.db.ExecContext(ctx, `
			UPDATE lists SET space_id = ?, remote_id = ?, name = ?, sync_state = ?,
				remote_updated_at = ?, is_deleted = ?, deleted_at = ?, updated_at = ?
			WHERE provider_id = ? AND id = ?`,
			list.SpaceID,
			remoteIDValue(list.RemoteID),
			list.Name,
			list.SyncState,
			nullableTime(list.RemoteUpdatedAt),
			boolInt(list.IsDeleted),
			nullableTime(list.DeletedAt),
			formatTime(list.UpdatedAt),
			list.ProviderID,
			list.ID,
		); updateErr != nil {
			return List{}, fmt.Errorf("sqlite: upsert list %q: %w", list.ID, updateErr)
		}
	}
	return s.getListByProvider(ctx, list.ProviderID, list.ID)
}

func (s *Store) deleteListByProvider(ctx context.Context, providerID, id string) error {
	return s.tombstoneList(ctx, providerID, id, nil)
}

func (s *Store) tombstoneList(ctx context.Context, providerID, id string, deletedAt *time.Time) error {
	if deletedAt == nil {
		now := time.Now().UTC()
		deletedAt = &now
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE lists SET is_deleted = 1, deleted_at = ?, sync_state = ?, updated_at = ?
		WHERE provider_id = ? AND id = ?`,
		formatTime(*deletedAt),
		SyncStateSynced,
		formatTime(time.Now()),
		providerID,
		id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: tombstone list %q: %w", id, err)
	}
	return requireAffected(result, "tombstone list", id)
}

func (s *Store) insertList(ctx context.Context, tx *sql.Tx, list List) error {
	_, err := execerFor(s.db, tx).ExecContext(ctx, `
		INSERT INTO lists (
			id, provider_id, space_id, remote_id, name, sync_state, remote_updated_at,
			is_deleted, deleted_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		list.ID,
		list.ProviderID,
		list.SpaceID,
		remoteIDValue(list.RemoteID),
		list.Name,
		list.SyncState,
		nullableTime(list.RemoteUpdatedAt),
		boolInt(list.IsDeleted),
		nullableTime(list.DeletedAt),
		formatTime(list.CreatedAt),
		formatTime(list.UpdatedAt),
	)
	return err
}

func (s *Store) resolveListID(ctx context.Context, list *List) error {
	if list.ID != "" {
		return nil
	}
	if list.RemoteID != nil && *list.RemoteID != "" {
		var id string
		err := s.db.QueryRowContext(ctx,
			"SELECT id FROM lists WHERE provider_id = ? AND remote_id = ?",
			list.ProviderID,
			*list.RemoteID,
		).Scan(&id)
		if err == nil {
			list.ID = id
			return nil
		}
		if !isNoRows(err) {
			return fmt.Errorf("sqlite: resolve list remote id: %w", err)
		}
	}
	return nil
}

func (s *Store) getList(ctx context.Context, queryer queryer, id, providerID string) (List, error) {
	query := listSelect + " WHERE id = ?"
	args := []any{id}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, providerID)
	}
	list, err := scanList(queryer.QueryRowContext(ctx, query, args...))
	if err != nil {
		return List{}, queryError("get list", id, err)
	}
	return list, nil
}

func listQuery(ctx context.Context, queryer queryer, query string, args ...any) ([]List, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list lists: %w", err)
	}
	defer rows.Close()
	lists := make([]List, 0)
	for rows.Next() {
		list, err := scanList(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan list: %w", err)
		}
		lists = append(lists, list)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list lists: %w", err)
	}
	return lists, nil
}

func prepareList(list List) (List, error) {
	if list.ProviderID == "" {
		return List{}, fmt.Errorf("sqlite: list provider id is empty")
	}
	if list.SpaceID == "" {
		return List{}, fmt.Errorf("sqlite: list space id is empty")
	}
	list.RemoteID = normalizeRemoteID(list.RemoteID)
	if list.CreatedAt.IsZero() {
		list.CreatedAt = time.Now().UTC()
	}
	if list.UpdatedAt.IsZero() {
		list.UpdatedAt = list.CreatedAt
	}
	if list.SyncState == "" {
		list.SyncState = SyncStateLocal
	}
	return list, nil
}

func assignListID(list *List) error {
	id, err := newID()
	if err != nil {
		return fmt.Errorf("sqlite: generate list id: %w", err)
	}
	list.ID = id
	return nil
}

func mergeListTimes(list, existing List) List {
	if list.CreatedAt.IsZero() {
		list.CreatedAt = existing.CreatedAt
	}
	if list.UpdatedAt.IsZero() {
		list.UpdatedAt = time.Now().UTC()
	}
	return list
}

func scanList(row rowScanner) (List, error) {
	var (
		list                       List
		remoteID                   sql.NullString
		syncState                  string
		remoteUpdatedAt, deletedAt sql.NullString
		isDeleted                  int
		createdAt, updatedAt       sql.NullString
	)
	if err := row.Scan(
		&list.ID,
		&list.ProviderID,
		&list.SpaceID,
		&remoteID,
		&list.Name,
		&syncState,
		&remoteUpdatedAt,
		&isDeleted,
		&deletedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return List{}, err
	}
	list.RemoteID = nullableString(remoteID)
	list.SyncState = SyncState(syncState)
	list.IsDeleted = isDeleted != 0
	var err error
	if list.RemoteUpdatedAt, err = scanNullableTime(remoteUpdatedAt); err != nil {
		return List{}, fmt.Errorf("remote updated timestamp: %w", err)
	}
	if list.DeletedAt, err = scanNullableTime(deletedAt); err != nil {
		return List{}, fmt.Errorf("deleted timestamp: %w", err)
	}
	if list.CreatedAt, err = parseNullableRequiredTime(createdAt); err != nil {
		return List{}, fmt.Errorf("created timestamp: %w", err)
	}
	if list.UpdatedAt, err = parseNullableRequiredTime(updatedAt); err != nil {
		return List{}, fmt.Errorf("updated timestamp: %w", err)
	}
	return list, nil
}
