package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const providerSelect = `
	SELECT id, type, name, enabled, configuration, sync_state, sync_cursor,
		sync_error, last_sync_at, created_at, updated_at
	FROM providers`

func (s *Store) createProvider(ctx context.Context, provider Provider) (Provider, error) {
	provider, err := prepareProvider(provider)
	if err != nil {
		return Provider{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO providers (
			id, type, name, enabled, configuration, sync_state, sync_cursor,
			sync_error, last_sync_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		provider.ID,
		provider.Type,
		provider.Name,
		boolInt(provider.Enabled),
		provider.Configuration,
		stringOrNil(provider.SyncState, SyncStateLocal),
		provider.SyncCursor,
		provider.SyncError,
		nullableTime(provider.LastSyncAt),
		formatTime(provider.CreatedAt),
		formatTime(provider.UpdatedAt),
	)
	if err != nil {
		return Provider{}, fmt.Errorf("sqlite: create provider %q: %w", provider.ID, err)
	}
	return s.getProvider(ctx, provider.ID)
}

func (s *Store) getProvider(ctx context.Context, id string) (Provider, error) {
	row := s.db.QueryRowContext(ctx, providerSelect+" WHERE id = ?", id)
	provider, err := scanProvider(row)
	if err != nil {
		return Provider{}, queryError("get provider", id, err)
	}
	return provider, nil
}

func (s *Store) listProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, providerSelect+" ORDER BY name, id")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list providers: %w", err)
	}
	defer rows.Close()

	providers := make([]Provider, 0)
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan provider: %w", err)
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list providers: %w", err)
	}
	return providers, nil
}

func (s *Store) updateProvider(ctx context.Context, provider Provider) (Provider, error) {
	if provider.ID == "" {
		return Provider{}, fmt.Errorf("sqlite: update provider: empty id")
	}
	existing, err := s.getProvider(ctx, provider.ID)
	if err != nil {
		return Provider{}, err
	}
	provider = mergeProviderTimes(provider, existing)
	if provider.SyncState == "" {
		provider.SyncState = existing.SyncState
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE providers SET
			type = ?, name = ?, enabled = ?, configuration = ?, sync_state = ?,
			sync_cursor = ?, sync_error = ?, last_sync_at = ?, updated_at = ?
		WHERE id = ?`,
		provider.Type,
		provider.Name,
		boolInt(provider.Enabled),
		provider.Configuration,
		provider.SyncState,
		provider.SyncCursor,
		provider.SyncError,
		nullableTime(provider.LastSyncAt),
		formatTime(provider.UpdatedAt),
		provider.ID,
	); err != nil {
		return Provider{}, fmt.Errorf("sqlite: update provider %q: %w", provider.ID, err)
	}
	return s.getProvider(ctx, provider.ID)
}

func (s *Store) upsertProvider(ctx context.Context, provider Provider) (Provider, error) {
	provider, err := prepareProvider(provider)
	if err != nil {
		return Provider{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO providers (
			id, type, name, enabled, configuration, sync_state, sync_cursor,
			sync_error, last_sync_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			enabled = excluded.enabled,
			configuration = excluded.configuration,
			sync_state = excluded.sync_state,
			sync_cursor = excluded.sync_cursor,
			sync_error = excluded.sync_error,
			last_sync_at = excluded.last_sync_at,
			updated_at = excluded.updated_at`,
		provider.ID,
		provider.Type,
		provider.Name,
		boolInt(provider.Enabled),
		provider.Configuration,
		stringOrNil(provider.SyncState, SyncStateLocal),
		provider.SyncCursor,
		provider.SyncError,
		nullableTime(provider.LastSyncAt),
		formatTime(provider.CreatedAt),
		formatTime(provider.UpdatedAt),
	)
	if err != nil {
		return Provider{}, fmt.Errorf("sqlite: upsert provider %q: %w", provider.ID, err)
	}
	return s.getProvider(ctx, provider.ID)
}

func (s *Store) deleteProvider(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM providers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete provider %q: %w", id, err)
	}
	return requireAffected(result, "delete provider", id)
}

func (s *Store) getProviderSyncState(ctx context.Context, providerID string) (ProviderSyncState, error) {
	provider, err := s.getProvider(ctx, providerID)
	if err != nil {
		return ProviderSyncState{}, err
	}
	return ProviderSyncState{
		ProviderID: provider.ID,
		State:      provider.SyncState,
		Cursor:     provider.SyncCursor,
		Error:      provider.SyncError,
		LastSyncAt: provider.LastSyncAt,
	}, nil
}

func (s *Store) setProviderSyncState(ctx context.Context, state ProviderSyncState) (ProviderSyncState, error) {
	if state.ProviderID == "" {
		return ProviderSyncState{}, fmt.Errorf("sqlite: set provider sync state: empty provider id")
	}
	if state.State == "" {
		state.State = SyncStateLocal
	}
	updatedAt := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE providers SET sync_state = ?, sync_cursor = ?, sync_error = ?,
			last_sync_at = ?, updated_at = ?
		WHERE id = ?`,
		state.State,
		state.Cursor,
		state.Error,
		nullableTime(state.LastSyncAt),
		formatTime(updatedAt),
		state.ProviderID,
	)
	if err != nil {
		return ProviderSyncState{}, fmt.Errorf("sqlite: set sync state for provider %q: %w", state.ProviderID, err)
	}
	if err := requireAffected(result, "set provider sync state", state.ProviderID); err != nil {
		return ProviderSyncState{}, err
	}
	return s.getProviderSyncState(ctx, state.ProviderID)
}

func (s *Store) GetProviderSyncState(ctx context.Context, providerID string) (ProviderSyncState, error) {
	return s.getProviderSyncState(ctx, providerID)
}

func (s *Store) SetProviderSyncState(ctx context.Context, state ProviderSyncState) (ProviderSyncState, error) {
	return s.setProviderSyncState(ctx, state)
}

func prepareProvider(provider Provider) (Provider, error) {
	if provider.ID == "" {
		id, err := newID()
		if err != nil {
			return Provider{}, fmt.Errorf("sqlite: generate provider id: %w", err)
		}
		provider.ID = id
	}
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = time.Now().UTC()
	}
	if provider.UpdatedAt.IsZero() {
		provider.UpdatedAt = provider.CreatedAt
	}
	if provider.SyncState == "" {
		provider.SyncState = SyncStateLocal
	}
	return provider, nil
}

func mergeProviderTimes(provider, existing Provider) Provider {
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = existing.CreatedAt
	}
	if provider.UpdatedAt.IsZero() {
		provider.UpdatedAt = time.Now().UTC()
	}
	return provider
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProvider(row rowScanner) (Provider, error) {
	var (
		provider                         Provider
		enabled                          int
		configuration                    []byte
		syncState                        string
		syncCursor, syncError            sql.NullString
		lastSyncAt, createdAt, updatedAt sql.NullString
	)
	if err := row.Scan(
		&provider.ID,
		&provider.Type,
		&provider.Name,
		&enabled,
		&configuration,
		&syncState,
		&syncCursor,
		&syncError,
		&lastSyncAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Provider{}, err
	}
	provider.Enabled = enabled != 0
	provider.Configuration = append([]byte(nil), configuration...)
	provider.SyncState = SyncState(syncState)
	provider.SyncCursor = nullableString(syncCursor)
	provider.SyncError = nullableString(syncError)
	var err error
	if provider.LastSyncAt, err = scanNullableTime(lastSyncAt); err != nil {
		return Provider{}, fmt.Errorf("last sync timestamp: %w", err)
	}
	if provider.CreatedAt, err = parseNullableRequiredTime(createdAt); err != nil {
		return Provider{}, fmt.Errorf("created timestamp: %w", err)
	}
	if provider.UpdatedAt, err = parseNullableRequiredTime(updatedAt); err != nil {
		return Provider{}, fmt.Errorf("updated timestamp: %w", err)
	}
	return provider, nil
}
