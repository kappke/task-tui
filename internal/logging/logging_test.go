package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLoggerRedactsSensitiveAttributes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasktui.log")
	logger, err := New(Config{Path: path, Level: LevelTrace})
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	logger.Info("request completed",
		"token", "token-value-that-must-not-appear",
		"Authorization", "Bearer authorization-value",
		"password", "password-value",
		"secret", "secret-value",
		"credential", "credential-value",
		"status", "ok",
	)
	logger.Info("grouped request", slog.Group("headers", "authorization", "nested-authorization-value"))

	if err := logger.Close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("second close should be harmless: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, secret := range []string{
		"token-value-that-must-not-appear",
		"Bearer authorization-value",
		"password-value",
		"secret-value",
		"credential-value",
		"nested-authorization-value",
	} {
		if strings.Contains(output, secret) {
			t.Fatal("sensitive log attribute was not redacted")
		}
	}
	if !strings.Contains(output, RedactedValue) || !strings.Contains(output, "status=ok") {
		t.Fatal("redacted or non-sensitive log attributes are missing")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log file permissions are not private: %o", info.Mode().Perm())
	}
}

func TestLogLevelFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasktui.log")
	logger, err := New(Config{Path: path, Level: LevelInfo})
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("debug message")
	logger.Info("info message")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	if strings.Contains(output, "debug message") || !strings.Contains(output, "info message") {
		t.Fatal("log level filtering is incorrect")
	}
}
