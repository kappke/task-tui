package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(token|secret|password|cookie|authorization|api[_-]?key)([=:][^\s,}]*)`)
	bearerPattern           = regexp.MustCompile(`(?i)bearer\s+[^\s,}]+`)
)

// LogSink owns the file backing a logger. Closing it is separate from the
// logger so callers can use a supplied logger without taking ownership of it.
type LogSink struct {
	file *os.File
	once sync.Once
	err  error
}

// Close flushes and closes the log file. File writes are unbuffered by the
// slog text handler, so no additional flush operation is required.
func (s *LogSink) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	s.once.Do(func() {
		s.err = s.file.Close()
	})
	return s.err
}

// OpenLogger creates a structured logger backed by an owner-readable log file.
// It never logs credentials and does not alter the process-wide default logger.
func OpenLogger(cfg LoggingConfig) (*slog.Logger, *LogSink, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, nil, errors.New("open logger: log path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, nil, fmt.Errorf("set log permissions: %w; close log file: %v", err, closeErr)
		}
		return nil, nil, fmt.Errorf("set log permissions: %w", err)
	}
	level := parseLogLevel(cfg.Level)
	handler := slog.NewTextHandler(file, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			return redactAttr(groups, attr)
		},
	})
	return slog.New(handler), &LogSink{file: file}, nil
}

func parseLogLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace":
		return slog.Level(-8)
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func redactAttr(groups []string, attr slog.Attr) slog.Attr {
	key := strings.ToLower(attr.Key)
	if strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "cookie") ||
		strings.Contains(key, "authorization") ||
		strings.Contains(key, "api_key") ||
		strings.Contains(key, "apikey") {
		return slog.String(attr.Key, "[REDACTED]")
	}
	if attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, redactString(attr.Value.String()))
	}
	return attr
}

func redactString(value string) string {
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	return secretAssignmentPattern.ReplaceAllString(value, "$1=[REDACTED]")
}

// SafeErrorText returns an error message suitable for a human-facing stderr
// path. Provider errors are expected to be sanitized already, but this final
// boundary prevents common accidental credential formats from escaping.
func SafeErrorText(err error) string {
	if err == nil {
		return ""
	}
	return redactString(err.Error())
}

// DiscardLogger returns a logger useful for tests or headless callers that do
// not want a file. It is an instance value and does not mutate slog.Default.
func DiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// logContext is used by lifecycle paths to keep logging failures from
// replacing the original operation error.
func logContext(ctx context.Context, logger *slog.Logger, level slog.Level, message string, args ...any) {
	if ctx == nil || logger == nil {
		return
	}
	logger.Log(ctx, level, message, args...)
}
