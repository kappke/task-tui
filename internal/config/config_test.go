package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.App.DefaultProvider != DefaultProvider {
		t.Fatal("default provider is not local")
	}
	if cfg.Database.Path == "" || cfg.Logging.Path == "" {
		t.Fatal("default paths must be populated")
	}
	if !cfg.Sync.Enabled || cfg.Sync.Interval != time.Minute || !cfg.Sync.RetryFailed {
		t.Fatal("sync defaults are incorrect")
	}
	if !cfg.UI.VimKeys || !cfg.UI.ShowSyncStatus {
		t.Fatal("UI defaults are incorrect")
	}
	if cfg.Logging.Level != "info" {
		t.Fatal("logging default is not info")
	}
	provider, ok := cfg.Providers[DefaultProvider]
	if !ok || provider.Type != DefaultProvider || !provider.Enabled {
		t.Fatal("local provider default is incorrect")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
}

func TestLoadTOMLAndEnvironmentOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := `[app]
default_provider = "clickup-work"

[database]
path = "~/data/tasks.db"

[sync]
enabled = false
interval = 45
retry_failed = false

[ui]
vim_keys = false
show_sync_status = false

[logging]
path = "~/logs/tasktui.log"
level = "debug"

[providers."clickup-work"]
type = "clickup"
name = "Work"
enabled = true
credential_ref = "clickup-work"

[providers."clickup-work".settings]
workspace_id = "workspace-1"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TASKTUI_SYNC_INTERVAL", "90")
	t.Setenv("TASKTUI_SYNC_RETRY_FAILED", "true")
	t.Setenv("TASKTUI_UI_VIM_KEYS", "true")
	t.Setenv("TASKTUI_LOG_LEVEL", "trace")
	t.Setenv("TASKTUI_PROVIDER_CLICKUP_WORK_CREDENTIAL_REF", "work-reference")
	t.Setenv("TASKTUI_PROVIDER_CLICKUP_WORK_SETTING_REGION", "us-east")

	cfg, err := Load(context.Background(), path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Sync.Enabled || cfg.Sync.Interval != 90*time.Second || !cfg.Sync.RetryFailed {
		t.Fatal("environment sync overrides were not applied")
	}
	if !cfg.UI.VimKeys || cfg.UI.ShowSyncStatus {
		t.Fatal("UI overrides were not applied")
	}
	if cfg.Logging.Level != "trace" {
		t.Fatal("logging level override was not applied")
	}
	provider := cfg.Providers["clickup-work"]
	if provider.CredentialRef != "work-reference" || provider.Settings["region"] != "us-east" {
		t.Fatal("provider overrides were not applied")
	}
	if provider.Settings["workspace_id"] != "workspace-1" {
		t.Fatal("provider TOML settings were not decoded")
	}
}

func TestDefaultConfigFileIsOptional(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	lookup := func(string) (string, bool) { return "", false }
	cfg, err := LoadWithOptions(context.Background(), LoadOptions{
		LookupEnv: lookup,
		Environ:   func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("missing default config should be optional: %v", err)
	}
	if cfg.App.DefaultProvider != DefaultProvider {
		t.Fatal("optional config did not return defaults")
	}
}

func TestExplicitMissingConfigFileIsAnError(t *testing.T) {
	_, err := Load(context.Background(), filepath.Join(t.TempDir(), "missing.toml"))
	if !errors.Is(err, ErrConfigNotFound) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit config did not return typed not-found error: %v", err)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "zero interval",
			mutate: func(cfg *Config) {
				cfg.Sync.Interval = 0
			},
		},
		{
			name: "unknown default provider",
			mutate: func(cfg *Config) {
				cfg.App.DefaultProvider = "missing"
			},
		},
		{
			name: "invalid log level",
			mutate: func(cfg *Config) {
				cfg.Logging.Level = "verbose"
			},
		},
		{
			name: "raw provider secret",
			mutate: func(cfg *Config) {
				cfg.Providers[DefaultProvider].Settings["api_token"] = "must-not-be-kept"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Defaults()
			test.mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected invalid config error, got %v", err)
			}
		})
	}
}

func TestRawSecretConfigIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[providers.local]\ntoken = \"super-secret-value\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(context.Background(), path)
	if !errors.Is(err, ErrSecretConfig) {
		t.Fatalf("raw secret field was not rejected: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "super-secret-value") {
		t.Fatal("raw secret appeared in configuration error")
	}
}
