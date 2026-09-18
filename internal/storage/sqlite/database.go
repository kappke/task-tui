package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store owns the SQLite connection used by the local repositories.
type Store struct {
	db       *sql.DB
	workerID string
}

// DB is kept as an alias so callers that use either name can depend on the
// same concrete storage implementation.
type DB = Store

// Open opens path, configures SQLite, and applies all embedded migrations.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite: database path is empty")
	}

	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", path, err)
	}

	if path == ":memory:" || strings.HasPrefix(path, "file::memory:") {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(8)
	}

	workerID, err := newID()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: generate queue worker id: %w", err)
	}
	store := &Store{db: db, workerID: workerID}
	if err := store.configure(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the underlying database connection pool.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SQLDB returns the underlying database for diagnostics and narrowly scoped
// maintenance operations. Application persistence should use Store methods.
func (s *Store) SQLDB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) DB() *sql.DB { return s.SQLDB() }

func (s *Store) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite: migrate on closed store")
	}
	return s.migrate(ctx)
}

func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("sqlite: migrate on nil database")
	}
	return (&Store{db: db}).migrate(ctx)
}

func (s *Store) configure(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	}
	for _, pragma := range pragmas {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("sqlite: configure %q: %w", pragma, err)
		}
	}
	return nil
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY NOT NULL,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("sqlite: create migration table: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		var applied bool
		err := s.db.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = ?)",
			migration.version,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("sqlite: check migration %03d: %w", migration.version, err)
		}
		if applied {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("sqlite: begin migration %03d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlite: apply migration %03d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
			migration.version,
			migration.name,
			formatTime(time.Now()),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlite: record migration %03d (%s): %w", migration.version, migration.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite: commit migration %03d (%s): %w", migration.version, migration.name, err)
		}
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	paths, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list migrations: %w", err)
	}
	sort.Strings(paths)

	migrations := make([]migration, 0, len(paths))
	seen := make(map[int]struct{}, len(paths))
	for _, path := range paths {
		base := filepath.Base(path)
		underscore := strings.IndexByte(base, '_')
		if underscore <= 0 {
			return nil, fmt.Errorf("sqlite: migration %q has no numeric prefix", path)
		}
		version, err := strconv.Atoi(base[:underscore])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("sqlite: migration %q has invalid version", path)
		}
		if _, ok := seen[version]; ok {
			return nil, fmt.Errorf("sqlite: duplicate migration version %03d", version)
		}
		contents, err := migrationFiles.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("sqlite: read migration %q: %w", path, err)
		}
		seen[version] = struct{}{}
		migrations = append(migrations, migration{
			version: version,
			name:    base,
			sql:     string(contents),
		})
	}
	return migrations, nil
}

func sqliteDSN(path string) (string, error) {
	pragmas := "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	if path == ":memory:" || strings.HasPrefix(path, "file::memory:") {
		id, err := newID()
		if err != nil {
			return "", fmt.Errorf("sqlite: generate memory database name: %w", err)
		}
		return "file:tasktui-memory-" + id + "?mode=memory&cache=shared&" + pragmas, nil
	}
	if strings.Contains(path, "?") {
		return path + "&" + pragmas, nil
	}
	return path + "?" + pragmas, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func nullableDuration(value *time.Duration) any {
	if value == nil {
		return nil
	}
	return value.Milliseconds()
}

func scanNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func scanNullableDuration(value sql.NullInt64) (*time.Duration, error) {
	if !value.Valid {
		return nil, nil
	}
	if value.Int64 < 0 || value.Int64 > maxDurationMillis {
		return nil, errors.New("duration must be non-negative")
	}
	duration := time.Duration(value.Int64) * time.Millisecond
	return &duration, nil
}

const maxDurationMillis = int64(1<<63-1) / int64(time.Millisecond)
