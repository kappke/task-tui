package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteDriverName = "sqlite"

// OpenSQLite opens a durable SQLite database and applies all migrations before
// returning. The connection limit is intentionally one: SQLite still permits
// concurrent callers through database/sql while avoiding accidental writer
// stampedes and making :memory: databases behave predictably in tests.
func OpenSQLite(ctx context.Context, cfg DatabaseConfig) (*sql.DB, error) {
	if ctx == nil {
		return nil, errors.New("open sqlite: nil context")
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, errors.New("open sqlite: database path is empty")
	}
	if cfg.Path != ":memory:" && !strings.HasPrefix(cfg.Path, "file::memory:") {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		if file, err := os.OpenFile(cfg.Path, os.O_CREATE, 0o600); err != nil {
			return nil, fmt.Errorf("create database file: %w", err)
		} else {
			if err := file.Chmod(0o600); err != nil {
				closeErr := file.Close()
				if closeErr != nil {
					return nil, fmt.Errorf("set database permissions: %w; close database file: %v", err, closeErr)
				}
				return nil, fmt.Errorf("set database permissions: %w", err)
			}
			if err := file.Close(); err != nil {
				return nil, fmt.Errorf("close database file: %w", err)
			}
		}
	}
	db, err := sql.Open(sqliteDriverName, cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite connection: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	busyTimeout := cfg.BusyTimeout
	if busyTimeout <= 0 {
		busyTimeout = 5 * time.Second
	}
	pragmaCtx, cancel := context.WithTimeout(ctx, busyTimeout)
	defer cancel()
	if _, err := db.ExecContext(pragmaCtx, "PRAGMA foreign_keys = ON"); err != nil {
		closeDatabase(db, err)
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := db.ExecContext(pragmaCtx, "PRAGMA journal_mode = WAL"); err != nil {
		closeDatabase(db, err)
		return nil, fmt.Errorf("enable sqlite WAL: %w", err)
	}
	if _, err := db.ExecContext(pragmaCtx, fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds())); err != nil {
		closeDatabase(db, err)
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	if err := MigrateSQLite(pragmaCtx, db); err != nil {
		closeDatabase(db, err)
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		closeDatabase(db, err)
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}

func closeDatabase(db *sql.DB, original error) {
	if err := db.Close(); err != nil && original != nil {
		return
	}
}

type migration struct {
	version int
	name    string
	sql     string
}

var sqliteMigrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		sql: `
CREATE TABLE providers (
		id TEXT PRIMARY KEY NOT NULL,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
		configuration TEXT NOT NULL DEFAULT '',
		last_sync_at TEXT,
		sync_error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
);

CREATE TABLE spaces (
		id TEXT PRIMARY KEY NOT NULL,
		provider_id TEXT NOT NULL,
		remote_id TEXT,
		name TEXT NOT NULL,
		sync_state TEXT NOT NULL,
		remote_updated_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE (id, provider_id),
		UNIQUE (provider_id, remote_id),
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE RESTRICT
);

CREATE TABLE lists (
		id TEXT PRIMARY KEY NOT NULL,
		provider_id TEXT NOT NULL,
		space_id TEXT NOT NULL,
		remote_id TEXT,
		name TEXT NOT NULL,
		sync_state TEXT NOT NULL,
		remote_updated_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE (id, provider_id),
		UNIQUE (provider_id, remote_id),
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE RESTRICT,
		FOREIGN KEY (space_id, provider_id) REFERENCES spaces(id, provider_id) ON DELETE RESTRICT
);

CREATE TABLE tasks (
		id TEXT PRIMARY KEY NOT NULL,
		provider_id TEXT NOT NULL,
		list_id TEXT NOT NULL,
		remote_id TEXT,
		parent_task_id TEXT,
		title TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		priority TEXT NOT NULL DEFAULT '',
		due_at TEXT,
		completed_at TEXT,
		sync_state TEXT NOT NULL,
		remote_updated_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE (id, provider_id),
		UNIQUE (provider_id, remote_id),
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE RESTRICT,
		FOREIGN KEY (list_id, provider_id) REFERENCES lists(id, provider_id) ON DELETE RESTRICT,
		FOREIGN KEY (parent_task_id, provider_id) REFERENCES tasks(id, provider_id) ON DELETE RESTRICT
);

CREATE TABLE provider_metadata (
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (entity_type, entity_id, provider_id, key),
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE RESTRICT
);

CREATE TABLE sync_operations (
		id TEXT PRIMARY KEY NOT NULL,
		provider_id TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		operation TEXT NOT NULL CHECK (operation IN ('create', 'update', 'delete')),
		payload BLOB NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
		status TEXT NOT NULL CHECK (status IN ('pending', 'syncing', 'failed', 'completed')),
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		last_attempt_at TEXT,
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE RESTRICT
);

CREATE TABLE ui_state (
		key TEXT PRIMARY KEY NOT NULL,
		value BLOB NOT NULL,
		updated_at TEXT NOT NULL
);

CREATE INDEX idx_spaces_provider ON spaces(provider_id);
CREATE INDEX idx_lists_provider_space ON lists(provider_id, space_id);
CREATE INDEX idx_tasks_provider_list ON tasks(provider_id, list_id);
CREATE INDEX idx_tasks_sync_state ON tasks(provider_id, sync_state);
CREATE INDEX idx_tasks_status ON tasks(provider_id, status);
CREATE INDEX idx_tasks_due_at ON tasks(provider_id, due_at);
CREATE INDEX idx_tasks_updated_at ON tasks(provider_id, updated_at);
CREATE INDEX idx_operations_provider_status ON sync_operations(provider_id, status, created_at);
CREATE INDEX idx_operations_entity ON sync_operations(provider_id, entity_type, entity_id, created_at);
`,
	},
}

// MigrateSQLite applies pending schema versions in a transaction. The
// migration table is created separately so a failed migration can be retried
// without recording a partially applied version.
func MigrateSQLite(ctx context.Context, db *sql.DB) error {
	if ctx == nil {
		return errors.New("migrate sqlite: nil context")
	}
	if db == nil {
		return errors.New("migrate sqlite: nil database")
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY NOT NULL,
		name TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	for _, migration := range sqliteMigrations {
		var applied int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", migration.version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", migration.version, err)
		}
		if applied != 0 {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			rollbackErr := tx.Rollback()
			if rollbackErr != nil {
				return fmt.Errorf("apply migration %d: %w; rollback: %v", migration.version, err, rollbackErr)
			}
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)", migration.version, migration.name, formatTime(time.Now().UTC())); err != nil {
			rollbackErr := tx.Rollback()
			if rollbackErr != nil {
				return fmt.Errorf("record migration %d: %w; rollback: %v", migration.version, err, rollbackErr)
			}
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse timestamp %q: %w", value.String, err)
	}
	return &parsed, nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTaskID(value *TaskID) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
