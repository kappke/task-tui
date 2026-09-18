package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	DefaultProvider     = "local"
	DefaultProviderID   = DefaultProvider
	DefaultConfigName   = "config.toml"
	DefaultDatabaseName = "tasktui.db"
	DefaultLogName      = "tasktui.log"
	DefaultSyncInterval = time.Minute
)

var (
	ErrInvalidConfig  = errors.New("invalid configuration")
	ErrConfigNotFound = errors.New("configuration file not found")
	ErrConfigParse    = errors.New("configuration file parse error")
	ErrSecretConfig   = errors.New("raw secrets are not accepted in configuration")
)

// Config is the complete runtime configuration. Credential values are never
// part of this structure; providers refer to credentials by name instead.
type Config struct {
	App       AppConfig
	Database  DatabaseConfig
	Sync      SyncConfig
	UI        UIConfig
	Logging   LoggingConfig
	Providers map[string]ProviderConfig
}

type AppConfig struct {
	DefaultProvider string
}

type DatabaseConfig struct {
	Path string
}

type SyncConfig struct {
	Enabled     bool
	Interval    time.Duration
	RetryFailed bool
}

type UIConfig struct {
	VimKeys        bool
	ShowSyncStatus bool
}

type LoggingConfig struct {
	Path  string
	Level LogLevel
}

type LogLevel string

const (
	LogLevelError LogLevel = "error"
	LogLevelWarn  LogLevel = "warn"
	LogLevelInfo  LogLevel = "info"
	LogLevelDebug LogLevel = "debug"
	LogLevelTrace LogLevel = "trace"
)

// ProviderConfig describes one provider instance. Settings are intentionally
// string-valued because their schema belongs to the provider package.
type ProviderConfig struct {
	ID            string
	Name          string
	Type          string
	Enabled       bool
	BaseURL       string
	CredentialRef string
	Settings      map[string]string
}

type ProviderInstance = ProviderConfig

// ValidationError identifies the configuration field that failed validation.
// It never includes the field's value, which keeps accidental secret values
// out of diagnostics.
type ValidationError struct {
	Field   string
	Problem string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ErrInvalidConfig.Error()
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Problem)
}

func (e *ValidationError) Unwrap() error { return ErrInvalidConfig }

func invalid(field, problem string) error {
	return &ValidationError{Field: field, Problem: problem}
}

// Defaults returns a usable local-only configuration. It does not create any
// directories or files.
func Defaults() Config {
	dataDir := defaultDataDir()
	stateDir := defaultStateDir()

	return Config{
		App: AppConfig{DefaultProvider: DefaultProvider},
		Database: DatabaseConfig{
			Path: filepath.Join(dataDir, "tasktui", DefaultDatabaseName),
		},
		Sync: SyncConfig{
			Enabled:     true,
			Interval:    DefaultSyncInterval,
			RetryFailed: true,
		},
		UI: UIConfig{
			VimKeys:        true,
			ShowSyncStatus: true,
		},
		Logging: LoggingConfig{
			Path:  filepath.Join(stateDir, "tasktui", DefaultLogName),
			Level: LogLevelInfo,
		},
		Providers: map[string]ProviderConfig{
			DefaultProvider: {
				ID:       DefaultProvider,
				Name:     "Local",
				Type:     DefaultProvider,
				Enabled:  true,
				Settings: map[string]string{},
			},
		},
	}
}

// Default is kept as a concise constructor for callers that prefer a noun-like
// package API.
func Default() Config { return Defaults() }

func DefaultConfig() Config { return Defaults() }

// DefaultPath returns the conventional user configuration file path.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determine user config directory: %w", err)
	}
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("determine user config directory: empty path")
	}
	return filepath.Join(dir, "tasktui", DefaultConfigName), nil
}

func DefaultConfigPath() (string, error) { return DefaultPath() }

// DefaultDatabasePath returns the conventional local database path.
func DefaultDatabasePath() (string, error) {
	dir, err := userDataDir()
	if err != nil {
		return "", fmt.Errorf("determine user data directory: %w", err)
	}
	return filepath.Join(dir, "tasktui", DefaultDatabaseName), nil
}

// DefaultLogPath returns the conventional log path. XDG_STATE_HOME is used
// when available; log files are state rather than cache data.
func DefaultLogPath() (string, error) {
	dir, err := userStateDir()
	if err != nil {
		return "", fmt.Errorf("determine user state directory: %w", err)
	}
	return filepath.Join(dir, "tasktui", DefaultLogName), nil
}

func defaultDataDir() string {
	dir, err := userDataDir()
	if err == nil && strings.TrimSpace(dir) != "" {
		return dir
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
		return filepath.Join(home, ".local", "share")
	}
	return "."
}

func userDataDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("empty home directory")
	}
	return filepath.Join(home, ".local", "share"), nil
}

func defaultStateDir() string {
	dir, err := userStateDir()
	if err == nil && strings.TrimSpace(dir) != "" {
		return dir
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
		return filepath.Join(home, ".local", "state")
	}
	return "."
}

func userStateDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("empty home directory")
	}
	return filepath.Join(home, ".local", "state"), nil
}

// Validate checks all values that can be consumed by other packages.
func (c Config) Validate() error {
	if strings.TrimSpace(c.App.DefaultProvider) == "" {
		return invalid("app.default_provider", "must not be empty")
	}
	if strings.TrimSpace(c.Database.Path) == "" {
		return invalid("database.path", "must not be empty")
	}
	if c.Sync.Interval <= 0 {
		return invalid("sync.interval", "must be greater than zero")
	}
	if strings.TrimSpace(c.Logging.Path) == "" {
		return invalid("logging.path", "must not be empty")
	}
	if !validLogLevel(string(c.Logging.Level)) {
		return invalid("logging.level", "must be one of error, warn, info, debug, or trace")
	}
	if len(c.Providers) == 0 {
		return invalid("providers", "must contain at least one provider")
	}

	defaultProvider, ok := c.Providers[c.App.DefaultProvider]
	if !ok {
		return invalid("app.default_provider", "must reference a configured provider")
	}
	if !defaultProvider.Enabled {
		return invalid("app.default_provider", "must reference an enabled provider")
	}

	for id, provider := range c.Providers {
		field := "providers." + id
		if !validProviderID(id) {
			return invalid(field, "has an invalid provider ID")
		}
		if strings.TrimSpace(provider.Type) == "" {
			return invalid(field+".type", "must not be empty")
		}
		if !validProviderType(provider.Type) {
			return invalid(field+".type", "contains invalid characters")
		}
		if provider.ID != "" && provider.ID != id {
			return invalid(field+".id", "must match the provider map key")
		}
		if !validCredentialReference(provider.CredentialRef) {
			return invalid(field+".credential_ref", "contains invalid characters")
		}
		if provider.BaseURL != "" {
			parsed, err := url.Parse(provider.BaseURL)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
				return invalid(field+".base_url", "must be an absolute URL without user information")
			}
			for key := range parsed.Query() {
				if isSensitiveConfigKey(key) {
					return invalid(field+".base_url", "must not contain credential query parameters")
				}
			}
		}
		for key := range provider.Settings {
			if strings.TrimSpace(key) == "" {
				return invalid(field+".settings", "contains an empty setting name")
			}
			if isSensitiveConfigKey(key) {
				return invalid(field+".settings."+key, "raw secrets are not accepted; use credential_ref")
			}
		}
	}

	return nil
}

func validProviderID(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func validProviderType(providerType string) bool {
	providerType = strings.TrimSpace(providerType)
	if providerType == "" {
		return false
	}
	for _, r := range providerType {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func validCredentialReference(reference string) bool {
	if reference == "" {
		return true
	}
	for _, r := range reference {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune("/\\?=#", r) {
			return false
		}
	}
	return true
}

func validLogLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "warn", "info", "debug", "trace":
		return true
	default:
		return false
	}
}

func isSensitiveConfigKey(key string) bool {
	normalized := normalizeName(key)
	switch normalized {
	case "credential_ref", "credential_reference":
		return false
	}
	for _, marker := range []string{"token", "authorization", "password", "secret", "credential", "cookie"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func normalizeName(value string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func expandPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return filepath.Clean(path)
}
