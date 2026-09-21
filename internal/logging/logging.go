package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const RedactedValue = "[REDACTED]"

var ErrInvalidConfig = errors.New("invalid logging configuration")

type Level string

const (
	LevelError Level = "error"
	LevelWarn  Level = "warn"
	LevelInfo  Level = "info"
	LevelDebug Level = "debug"
	LevelTrace Level = "trace"
)

type Config struct {
	Path  string
	Level Level
}

// Logger owns the file backing its slog.Logger and must be closed by its
// owner during application shutdown.
type Logger struct {
	*slog.Logger

	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

// New creates a text slog logger backed by a private file. Parent directories
// are created with owner-only permissions when they do not exist.
func New(cfg Config) (*Logger, error) {
	path := expandHome(strings.TrimSpace(cfg.Path))
	if path == "" {
		return nil, fmt.Errorf("%w: log path must not be empty", ErrInvalidConfig)
	}
	level, err := normalizeLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: log path must not be a symbolic link", ErrInvalidConfig)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect log path: %w", err)
	}

	file, err := os.OpenFile(path, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("secure log file permissions: %v; close log file: %w", err, closeErr)
		}
		return nil, fmt.Errorf("secure log file permissions: %w", err)
	}

	handler := slog.NewTextHandler(file, &slog.HandlerOptions{
		Level:       level.slogLevel(),
		ReplaceAttr: RedactAttr,
	})
	return &Logger{
		Logger: slog.New(handler),
		file:   file,
	}, nil
}

// NewFile is a convenience constructor for callers that do not need a
// separate logging configuration structure.
func NewFile(path string, level Level) (*Logger, error) {
	return New(Config{Path: path, Level: level})
}

// NewLogger is an explicit alias for New for bootstrap code.
func NewLogger(cfg Config) (*Logger, error) { return New(cfg) }

// Slog returns the standard logger for APIs that accept *slog.Logger.
func (l *Logger) Slog() *slog.Logger {
	if l == nil {
		return nil
	}
	return l.Logger
}

// Close closes the owned log file. It is safe to call more than once.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closeErr = l.file.Close()
	})
	return l.closeErr
}

// Trace records a trace-level message for callers that want the level named
// by the configuration without depending on slog's lower-level numeric API.
func (l *Logger) Trace(msg string, args ...any) {
	if l == nil || l.Logger == nil {
		return
	}
	l.Logger.Log(context.Background(), slog.Level(-8), msg, args...)
}

func ParseLevel(value string) (Level, error) {
	level, err := normalizeLevel(Level(value))
	if err != nil {
		return "", err
	}
	return level, nil
}

func normalizeLevel(level Level) (Level, error) {
	if strings.TrimSpace(string(level)) == "" {
		return LevelInfo, nil
	}
	level = Level(strings.ToLower(strings.TrimSpace(string(level))))
	switch level {
	case LevelError, LevelWarn, LevelInfo, LevelDebug, LevelTrace:
		return level, nil
	default:
		return "", fmt.Errorf("%w: unsupported log level", ErrInvalidConfig)
	}
}

func (level Level) slogLevel() slog.Level {
	switch level {
	case LevelError:
		return slog.LevelError
	case LevelWarn:
		return slog.LevelWarn
	case LevelDebug:
		return slog.LevelDebug
	case LevelTrace:
		return slog.Level(-8)
	default:
		return slog.LevelInfo
	}
}

// RedactAttr is suitable for slog.HandlerOptions.ReplaceAttr. Sensitive
// attributes are replaced before the handler formats values, including
// sensitive groups and values supplied through slog.LogValuer.
func RedactAttr(groups []string, attr slog.Attr) slog.Attr {
	if sensitiveKey(attr.Key) {
		return slog.Attr{Key: attr.Key, Value: slog.StringValue(RedactedValue)}
	}
	for _, group := range groups {
		if sensitiveKey(group) {
			return slog.Attr{Key: attr.Key, Value: slog.StringValue(RedactedValue)}
		}
	}
	return attr
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"token", "authorization", "password", "secret", "credential"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~\\") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
