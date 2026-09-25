package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsClickUpIDThatCollidesWithLocalProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ClickUp.Enabled = true
	cfg.ClickUp.ID = ProviderID("local")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicates the local provider") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestLoadConfigDiscoversTheDefaultUserFile(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("TASKTUI_CONFIG_PATH", "")
	t.Setenv("TASKTUI_CONFIG_FILE", "")
	t.Setenv("TASKTUI_CONFIG", "")
	path := filepath.Join(configRoot, "tasktui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[app]\ndefault_provider = \"clickup\"\n[providers.clickup]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.App.DefaultProvider != ProviderID("clickup") || !cfg.ClickUp.Enabled {
		t.Fatalf("discovered config = %#v", cfg)
	}
}

func TestLoadConfigUsesDiscoveredPathEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected.toml")
	if err := os.WriteFile(path, []byte("[app]\ndefault_provider = \"local\"\n[providers.clickup]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASKTUI_CONFIG_PATH", path)

	cfg, err := LoadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.ClickUp.Enabled {
		t.Fatal("config path environment override was not loaded")
	}
}

func TestOnboardingConfigContainsProviderSetupButNoToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasktui", "config.toml")
	cfg := DefaultConfig()
	cfg.App.DefaultProvider = ProviderID("clickup")
	cfg.ClickUp.Enabled = true
	if err := saveOnboardingConfig(path, cfg); err != nil {
		t.Fatalf("save onboarding config: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "token-value") || !strings.Contains(string(data), `default_provider = "clickup"`) {
		t.Fatalf("unexpected onboarding config: %s", data)
	}
	loaded, err := LoadConfig(context.Background(), path)
	if err != nil {
		t.Fatalf("load onboarding config: %v", err)
	}
	if loaded.App.DefaultProvider != ProviderID("clickup") || !loaded.ClickUp.Enabled {
		t.Fatalf("loaded onboarding config = %#v", loaded)
	}
}
