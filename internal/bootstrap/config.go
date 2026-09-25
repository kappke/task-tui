package bootstrap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	configpkg "github.com/kappke/task-tui/internal/config"
)

// Config is the complete typed runtime configuration. Credentials are
// deliberately represented by an environment variable name, not persisted in
// this value when it is loaded from a file.
type Config struct {
	App      AppConfig
	Sync     SyncConfig
	UI       UIConfig
	Logging  LoggingConfig
	Database DatabaseConfig
	ClickUp  ClickUpConfig
}

// AppConfig controls application-wide behavior.
type AppConfig struct {
	DefaultProvider ProviderID
}

// SyncConfig controls background synchronization.
type SyncConfig struct {
	Enabled         bool
	Interval        time.Duration
	RetryFailed     bool
	ShutdownTimeout time.Duration
}

// UIConfig controls presentation behavior.
type UIConfig struct {
	VimKeys        bool
	ShowSyncStatus bool
	Headless       bool
}

// LoggingConfig controls file logging. Log files are always opened with
// owner-only permissions by OpenLogger.
type LoggingConfig struct {
	Path  string
	Level string
}

// DatabaseConfig controls SQLite persistence.
type DatabaseConfig struct {
	Path        string
	BusyTimeout time.Duration
}

const defaultClickUpBaseURL = "https://api.clickup.com/api/v2"

// ClickUpConfig describes an optional ClickUp provider instance. TokenEnv is
// read only when the provider performs its first request.
type ClickUpConfig struct {
	Enabled     bool
	ID          ProviderID
	Name        string
	BaseURL     string
	WorkspaceID string
	TokenEnv    string
}

// DefaultConfig returns safe defaults for a local-only installation.
func DefaultConfig() Config {
	dataDir := defaultDataDir()
	stateDir := defaultStateDir()
	return Config{
		App: AppConfig{DefaultProvider: ProviderID("local")},
		Sync: SyncConfig{
			Enabled:         true,
			Interval:        time.Minute,
			RetryFailed:     true,
			ShutdownTimeout: 5 * time.Second,
		},
		UI: UIConfig{
			VimKeys:        true,
			ShowSyncStatus: true,
		},
		Logging: LoggingConfig{
			Path:  filepath.Join(stateDir, "tasktui.log"),
			Level: "trace",
		},
		Database: DatabaseConfig{
			Path:        filepath.Join(dataDir, "tasktui.db"),
			BusyTimeout: 5 * time.Second,
		},
		ClickUp: ClickUpConfig{
			ID:       ProviderID("clickup"),
			Name:     "ClickUp",
			BaseURL:  defaultClickUpBaseURL,
			TokenEnv: "TASKTUI_CLICKUP_TOKEN",
		},
	}
}

func defaultDataDir() string {
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		return filepath.Join(value, "tasktui")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "tasktui")
	}
	return filepath.Join(os.TempDir(), "tasktui")
}

func defaultStateDir() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "tasktui")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "tasktui")
	}
	return filepath.Join(os.TempDir(), "tasktui")
}

// LoadConfig discovers the conventional user configuration file when path is
// empty, reads the supported TOML subset, and applies environment overrides. A
// missing conventional file means defaults, which keeps first launch usable.
// Unknown keys are ignored so unrelated provider configuration does not
// prevent startup.
func LoadConfig(ctx context.Context, path string) (Config, error) {
	if ctx == nil {
		return Config{}, errors.New("load config: nil context")
	}
	resolvedPath, explicit, err := resolveConfigPath(path)
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig()
	file, err := os.Open(resolvedPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
		if explicit {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
	} else {
		err = parseConfig(ctx, file, &cfg)
		closeErr := file.Close()
		if err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		if closeErr != nil {
			return Config{}, fmt.Errorf("close config: %w", closeErr)
		}
	}
	applyEnvironment(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func resolveConfigPath(path string) (string, bool, error) {
	path = strings.TrimSpace(path)
	explicit := path != ""
	if path == "" {
		if configuredPath, configured := os.LookupEnv("TASKTUI_CONFIG_PATH"); configured && strings.TrimSpace(configuredPath) != "" {
			path = configuredPath
			explicit = true
		} else if configuredPath, configured := os.LookupEnv("TASKTUI_CONFIG_FILE"); configured && strings.TrimSpace(configuredPath) != "" {
			path = configuredPath
			explicit = true
		} else if configuredPath, configured := os.LookupEnv("TASKTUI_CONFIG"); configured && strings.TrimSpace(configuredPath) != "" {
			path = configuredPath
			explicit = true
		} else {
			var err error
			path, err = configpkg.DefaultPath()
			if err != nil {
				return "", false, err
			}
		}
	}
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, fmt.Errorf("expand config path: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Clean(path), explicit, nil
}

func parseConfig(ctx context.Context, file *os.File, cfg *Config) error {
	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid line %q", line)
		}
		if err := setConfigValue(cfg, section, strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func stripComment(line string) string {
	quoted := false
	for i, r := range line {
		switch r {
		case '"':
			quoted = !quoted
		case '#':
			if !quoted {
				return line[:i]
			}
		}
	}
	return line
}

func setConfigValue(cfg *Config, section, key, raw string) error {
	value, err := parseString(raw)
	if err != nil {
		return err
	}
	fullKey := section + "." + key
	switch fullKey {
	case "app.default_provider":
		cfg.App.DefaultProvider = ProviderID(value)
	case "sync.enabled":
		cfg.Sync.Enabled, err = strconv.ParseBool(value)
	case "sync.interval":
		cfg.Sync.Interval, err = parseDuration(value, time.Second)
	case "sync.retry_failed":
		cfg.Sync.RetryFailed, err = strconv.ParseBool(value)
	case "sync.shutdown_timeout":
		cfg.Sync.ShutdownTimeout, err = parseDuration(value, time.Second)
	case "ui.vim_keys":
		cfg.UI.VimKeys, err = strconv.ParseBool(value)
	case "ui.show_sync_status":
		cfg.UI.ShowSyncStatus, err = strconv.ParseBool(value)
	case "ui.headless":
		cfg.UI.Headless, err = strconv.ParseBool(value)
	case "logging.path":
		cfg.Logging.Path = value
	case "logging.level":
		cfg.Logging.Level = value
	case "database.path":
		cfg.Database.Path = value
	case "database.busy_timeout":
		cfg.Database.BusyTimeout, err = parseDuration(value, time.Second)
	case "providers.clickup.enabled":
		cfg.ClickUp.Enabled, err = strconv.ParseBool(value)
	case "providers.clickup.id":
		cfg.ClickUp.ID = ProviderID(value)
	case "providers.clickup.name":
		cfg.ClickUp.Name = value
	case "providers.clickup.base_url":
		cfg.ClickUp.BaseURL = value
	case "providers.clickup.workspace_id":
		cfg.ClickUp.WorkspaceID = value
	case "providers.clickup.token_env":
		cfg.ClickUp.TokenEnv = value
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("invalid %s: %w", fullKey, err)
	}
	return nil
}

func parseString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && ((raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
		if raw[0] == '\'' {
			return raw[1 : len(raw)-1], nil
		}
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("invalid quoted value: %w", err)
		}
		return value, nil
	}
	return raw, nil
}

func parseDuration(value string, unit time.Duration) (time.Duration, error) {
	if duration, err := time.ParseDuration(value); err == nil {
		return duration, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("duration %q: %w", value, err)
	}
	return time.Duration(seconds) * unit, nil
}

func applyEnvironment(cfg *Config) {
	if value, ok := os.LookupEnv("TASKTUI_DEFAULT_PROVIDER"); ok {
		cfg.App.DefaultProvider = ProviderID(value)
	}
	if value, ok := os.LookupEnv("TASKTUI_SYNC_ENABLED"); ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Sync.Enabled = parsed
		}
	}
	if value, ok := os.LookupEnv("TASKTUI_SYNC_INTERVAL"); ok {
		if parsed, err := parseDuration(value, time.Second); err == nil {
			cfg.Sync.Interval = parsed
		}
	}
	if value, ok := os.LookupEnv("TASKTUI_SYNC_RETRY_FAILED"); ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Sync.RetryFailed = parsed
		}
	}
	if value, ok := os.LookupEnv("TASKTUI_DB_PATH"); ok {
		cfg.Database.Path = value
	}
	if value, ok := os.LookupEnv("TASKTUI_LOG_PATH"); ok {
		cfg.Logging.Path = value
	}
	if value, ok := os.LookupEnv("TASKTUI_LOG_LEVEL"); ok {
		cfg.Logging.Level = value
	}
	if value, ok := os.LookupEnv("TASKTUI_HEADLESS"); ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.UI.Headless = parsed
		}
	}
	if value, ok := os.LookupEnv("TASKTUI_CLICKUP_ENABLED"); ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.ClickUp.Enabled = parsed
		}
	}
	if value, ok := os.LookupEnv("TASKTUI_CLICKUP_BASE_URL"); ok {
		cfg.ClickUp.BaseURL = value
	}
	if value, ok := os.LookupEnv("TASKTUI_CLICKUP_WORKSPACE_ID"); ok {
		cfg.ClickUp.WorkspaceID = value
	}
	if value, ok := os.LookupEnv("TASKTUI_CLICKUP_TOKEN_ENV"); ok {
		cfg.ClickUp.TokenEnv = value
	}
}

// Validate checks values before resources are opened.
func (c Config) Validate() error {
	if strings.TrimSpace(string(c.App.DefaultProvider)) == "" {
		return errors.New("config: default provider is required")
	}
	if c.Sync.Interval <= 0 {
		return errors.New("config: sync interval must be positive")
	}
	if c.Sync.ShutdownTimeout <= 0 {
		return errors.New("config: shutdown timeout must be positive")
	}
	if c.Database.BusyTimeout < 0 {
		return errors.New("config: database busy timeout cannot be negative")
	}
	if strings.TrimSpace(c.Database.Path) == "" {
		return errors.New("config: database path is required")
	}
	if strings.TrimSpace(c.Logging.Path) == "" {
		return errors.New("config: log path is required")
	}
	if c.ClickUp.Enabled {
		if strings.TrimSpace(string(c.ClickUp.ID)) == "" || strings.TrimSpace(c.ClickUp.Name) == "" {
			return errors.New("config: clickup id and name are required when enabled")
		}
		if strings.TrimSpace(c.ClickUp.BaseURL) == "" {
			return errors.New("config: clickup base URL is required when enabled")
		}
		if strings.TrimSpace(c.ClickUp.TokenEnv) == "" {
			return errors.New("config: clickup token environment variable is required when enabled")
		}
		if c.ClickUp.ID == ProviderID("local") {
			return errors.New("config: clickup provider ID duplicates the local provider")
		}
	}
	return nil
}
