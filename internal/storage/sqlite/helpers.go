package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func stringOrNil(value, fallback SyncState) string {
	if value == "" {
		return string(fallback)
	}
	return string(value)
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func normalizeRemoteID(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	copy := *value
	return &copy
}

func remoteIDValue(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func isConstraintError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "constraint")
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func execerFor(db *sql.DB, tx *sql.Tx) execer {
	if tx != nil {
		return tx
	}
	return db
}

func parseNullableRequiredTime(value sql.NullString) (time.Time, error) {
	if !value.Valid || value.String == "" {
		return time.Time{}, errors.New("timestamp is NULL")
	}
	return parseTime(value.String)
}

func queryError(operation, id string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %q", ErrNotFound, operation, id)
	}
	return fmt.Errorf("sqlite: %s %q: %w", operation, id, err)
}

func requireAffected(result sql.Result, operation, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: %s %q rows affected: %w", operation, id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s %q", ErrNotFound, operation, id)
	}
	return nil
}
