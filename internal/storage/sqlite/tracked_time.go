package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

type trackedTimeEntryExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// UpsertTrackedTimeEntry stores a completed task time interval. Remote entries
// are idempotent per provider and remote entry ID.
func (s *Store) UpsertTrackedTimeEntry(ctx context.Context, entry domain.TrackedTimeEntry) (domain.TrackedTimeEntry, error) {
	if err := upsertTrackedTimeEntry(ctx, s.db, &entry); err != nil {
		return domain.TrackedTimeEntry{}, err
	}
	if entry.RemoteEntryID != nil {
		stored, err := s.getTrackedTimeEntryByRemoteID(ctx, entry.ProviderID, *entry.RemoteEntryID)
		if err != nil {
			return domain.TrackedTimeEntry{}, err
		}
		return stored, nil
	}
	return s.GetTrackedTimeEntry(ctx, entry.ID)
}

// UpsertTrackedTimeEntryAndDeleteAppState stores a completed entry and clears
// its active session atomically when the related task is not cached locally.
func (s *Store) UpsertTrackedTimeEntryAndDeleteAppState(ctx context.Context, entry domain.TrackedTimeEntry, key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("sqlite: app state key is empty")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin tracked time entry completion: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := upsertTrackedTimeEntry(ctx, tx, &entry); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM app_state WHERE key = ?", key); err != nil {
		return fmt.Errorf("sqlite: clear app state %q: %w", key, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit tracked time entry completion: %w", err)
	}
	committed = true
	return nil
}

// ListTrackedTimeEntries returns entries whose start times fall within [start,end).
// A non-positive limit returns every matching entry.
func (s *Store) ListTrackedTimeEntries(ctx context.Context, start, end time.Time, limit int) ([]domain.TrackedTimeEntry, error) {
	query := `SELECT id, provider_id, task_id, remote_task_id, remote_entry_id, task_title,
		started_at, ended_at, duration_ms FROM tracked_time_entries WHERE 1 = 1`
	args := make([]any, 0, 3)
	if !start.IsZero() {
		query += ` AND started_at >= ?`
		args = append(args, formatTime(start))
	}
	if !end.IsZero() {
		query += ` AND started_at < ?`
		args = append(args, formatTime(end))
	}
	query += ` ORDER BY started_at DESC, id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list tracked time entries: %w", err)
	}
	defer rows.Close()
	entries := make([]domain.TrackedTimeEntry, 0)
	for rows.Next() {
		entry, err := scanTrackedTimeEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan tracked time entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list tracked time entries: %w", err)
	}
	return entries, nil
}

func (s *Store) GetTrackedTimeEntry(ctx context.Context, id domain.TimeEntryID) (domain.TrackedTimeEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, provider_id, task_id, remote_task_id, remote_entry_id, task_title,
		started_at, ended_at, duration_ms FROM tracked_time_entries WHERE id = ?`, id.String())
	entry, err := scanTrackedTimeEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TrackedTimeEntry{}, fmt.Errorf("%w: tracked time entry %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.TrackedTimeEntry{}, fmt.Errorf("sqlite: get tracked time entry %s: %w", id, err)
	}
	return entry, nil
}

func (s *Store) getTrackedTimeEntryByRemoteID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.TrackedTimeEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, provider_id, task_id, remote_task_id, remote_entry_id, task_title,
		started_at, ended_at, duration_ms FROM tracked_time_entries WHERE provider_id = ? AND remote_entry_id = ?`, providerID, remoteID)
	entry, err := scanTrackedTimeEntry(row)
	if err != nil {
		return domain.TrackedTimeEntry{}, fmt.Errorf("sqlite: get tracked time entry %s/%s: %w", providerID, remoteID, err)
	}
	return entry, nil
}

func upsertTrackedTimeEntry(ctx context.Context, exec trackedTimeEntryExecer, entry *domain.TrackedTimeEntry) error {
	if entry == nil {
		return errors.New("sqlite: nil tracked time entry")
	}
	if entry.ID.IsZero() {
		id, err := newID()
		if err != nil {
			return fmt.Errorf("sqlite: generate tracked time entry ID: %w", err)
		}
		entry.ID = domain.TimeEntryID(id)
	}
	entry.StartedAt = entry.StartedAt.UTC()
	entry.EndedAt = entry.EndedAt.UTC()
	if err := entry.Validate(); err != nil {
		return err
	}
	var taskID any
	if entry.TaskID != nil {
		taskID = entry.TaskID.String()
	}
	_, err := exec.ExecContext(ctx, `INSERT INTO tracked_time_entries (
		id, provider_id, task_id, remote_task_id, remote_entry_id, task_title, started_at, ended_at, duration_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (provider_id, remote_entry_id) WHERE remote_entry_id IS NOT NULL DO UPDATE SET
		task_id = excluded.task_id,
		remote_task_id = excluded.remote_task_id,
		task_title = excluded.task_title,
		started_at = excluded.started_at,
		ended_at = excluded.ended_at,
		duration_ms = excluded.duration_ms`,
		entry.ID, entry.ProviderID, taskID, entry.RemoteTaskID, remoteIDValue(entry.RemoteEntryID), entry.TaskTitle,
		formatTime(entry.StartedAt), formatTime(entry.EndedAt), entry.Duration.Milliseconds())
	if err != nil {
		return fmt.Errorf("sqlite: upsert tracked time entry: %w", err)
	}
	return nil
}

type trackedTimeEntryScanner interface {
	Scan(...any) error
}

func scanTrackedTimeEntry(scanner trackedTimeEntryScanner) (domain.TrackedTimeEntry, error) {
	var entry domain.TrackedTimeEntry
	var taskID, remoteEntryID sql.NullString
	var startedAt, endedAt string
	var durationMillis int64
	if err := scanner.Scan(
		&entry.ID, &entry.ProviderID, &taskID, &entry.RemoteTaskID, &remoteEntryID, &entry.TaskTitle,
		&startedAt, &endedAt, &durationMillis,
	); err != nil {
		return domain.TrackedTimeEntry{}, err
	}
	if taskID.Valid {
		value := domain.TaskID(taskID.String)
		entry.TaskID = &value
	}
	if remoteEntryID.Valid {
		entry.RemoteEntryID = &remoteEntryID.String
	}
	var err error
	if entry.StartedAt, err = parseTime(startedAt); err != nil {
		return domain.TrackedTimeEntry{}, fmt.Errorf("started at: %w", err)
	}
	if entry.EndedAt, err = parseTime(endedAt); err != nil {
		return domain.TrackedTimeEntry{}, fmt.Errorf("ended at: %w", err)
	}
	if durationMillis < 0 || durationMillis > maxDurationMillis {
		return domain.TrackedTimeEntry{}, errors.New("tracked time entry duration is invalid")
	}
	entry.Duration = time.Duration(durationMillis) * time.Millisecond
	return entry, entry.Validate()
}
