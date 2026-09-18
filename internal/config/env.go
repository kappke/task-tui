package config

import (
	"fmt"
	"strconv"
	"strings"
)

func applyEnvironment(cfg *Config, lookup func(string) (string, bool), environ func() []string) error {
	if value, ok := firstEnv(lookup, "TASKTUI_APP_DEFAULT_PROVIDER", "TASKTUI_DEFAULT_PROVIDER"); ok {
		cfg.App.DefaultProvider = value
	}
	if value, ok := firstEnv(lookup, "TASKTUI_DATABASE_PATH", "TASKTUI_DATABASE_FILE", "TASKTUI_DB_PATH"); ok {
		cfg.Database.Path = value
	}
	if value, ok := firstEnv(lookup, "TASKTUI_SYNC_ENABLED"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return invalid("TASKTUI_SYNC_ENABLED", "must be a boolean")
		}
		cfg.Sync.Enabled = parsed
	}
	if value, ok := firstEnv(lookup, "TASKTUI_SYNC_INTERVAL", "TASKTUI_SYNC_INTERVAL_SECONDS"); ok {
		parsed, err := parseDurationText(value, "TASKTUI_SYNC_INTERVAL")
		if err != nil {
			return err
		}
		cfg.Sync.Interval = parsed
	}
	if value, ok := firstEnv(lookup, "TASKTUI_SYNC_RETRY_FAILED", "TASKTUI_SYNC_RETRY"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return invalid("TASKTUI_SYNC_RETRY_FAILED", "must be a boolean")
		}
		cfg.Sync.RetryFailed = parsed
	}
	if value, ok := firstEnv(lookup, "TASKTUI_UI_VIM_KEYS", "TASKTUI_UI_VIM"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return invalid("TASKTUI_UI_VIM_KEYS", "must be a boolean")
		}
		cfg.UI.VimKeys = parsed
	}
	if value, ok := firstEnv(lookup, "TASKTUI_UI_SHOW_SYNC_STATUS", "TASKTUI_UI_SHOW_STATUS"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return invalid("TASKTUI_UI_SHOW_SYNC_STATUS", "must be a boolean")
		}
		cfg.UI.ShowSyncStatus = parsed
	}
	if value, ok := firstEnv(lookup, "TASKTUI_LOG_PATH", "TASKTUI_LOG_FILE", "TASKTUI_LOGGING_PATH"); ok {
		cfg.Logging.Path = value
	}
	if value, ok := firstEnv(lookup, "TASKTUI_LOG_LEVEL", "TASKTUI_LOGGING_LEVEL"); ok {
		cfg.Logging.Level = LogLevel(value)
	}

	return applyProviderEnvironment(cfg, lookup, environ)
}

func firstEnv(lookup func(string) (string, bool), names ...string) (string, bool) {
	for _, name := range names {
		if value, ok := lookup(name); ok {
			return value, true
		}
	}
	return "", false
}

type providerEnvOverride struct {
	name   string
	id     string
	suffix string
	value  string
}

func applyProviderEnvironment(cfg *Config, lookup func(string) (string, bool), environ func() []string) error {
	knownIDs := make(map[string]string, len(cfg.Providers))
	for id := range cfg.Providers {
		knownIDs[providerEnvID(id)] = id
	}

	overrides := make([]providerEnvOverride, 0)
	seenEnvironmentNames := make(map[string]struct{})
	for _, item := range environ() {
		name, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		for _, prefix := range []string{"TASKTUI_PROVIDER_", "TASKTUI_PROVIDERS_"} {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			rest := strings.TrimPrefix(name, prefix)
			id, suffix, matched := splitProviderEnv(rest, knownIDs)
			if !matched {
				if providerEnvLooksSecret(rest) {
					return fmt.Errorf("%w: environment variable %s", ErrSecretConfig, name)
				}
				continue
			}
			overrides = append(overrides, providerEnvOverride{
				name:   name,
				id:     id,
				suffix: suffix,
				value:  value,
			})
			seenEnvironmentNames[name] = struct{}{}
			break
		}
	}

	for _, override := range overrides {
		id := override.id
		if existingID, ok := knownIDs[providerEnvID(id)]; ok {
			id = existingID
		}
		provider, ok := cfg.Providers[id]
		if !ok {
			provider = ProviderConfig{
				ID:       id,
				Name:     id,
				Type:     id,
				Enabled:  true,
				Settings: map[string]string{},
			}
		}
		if provider.Settings == nil {
			provider.Settings = map[string]string{}
		}
		if err := applyProviderEnvironmentField(&provider, override.suffix, override.value, override.name); err != nil {
			return err
		}
		cfg.Providers[id] = provider
		knownIDs[providerEnvID(id)] = id
	}

	// Apply known provider fields directly as well so callers can inject a
	// LookupEnv function without having to reproduce os.Environ.
	for id := range cfg.Providers {
		prefixes := []string{
			"TASKTUI_PROVIDER_" + providerEnvID(id) + "_",
			"TASKTUI_PROVIDERS_" + providerEnvID(id) + "_",
		}
		for _, prefix := range prefixes {
			for _, suffix := range []string{"TYPE", "NAME", "ENABLED", "BASE_URL", "URL", "CREDENTIAL_REF", "CREDENTIAL_REFERENCE"} {
				name := prefix + suffix
				if _, seen := seenEnvironmentNames[name]; seen {
					continue
				}
				value, ok := lookup(name)
				if !ok {
					continue
				}
				provider := cfg.Providers[id]
				if provider.Settings == nil {
					provider.Settings = map[string]string{}
				}
				if err := applyProviderEnvironmentField(&provider, suffix, value, name); err != nil {
					return err
				}
				cfg.Providers[id] = provider
			}
		}
	}
	return nil
}

func applyProviderEnvironmentField(provider *ProviderConfig, suffix, value, envName string) error {
	switch suffix {
	case "TYPE":
		provider.Type = value
	case "NAME":
		provider.Name = value
	case "ENABLED":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return invalid(envName, "must be a boolean")
		}
		provider.Enabled = parsed
	case "BASE_URL", "URL":
		provider.BaseURL = value
	case "CREDENTIAL_REF", "CREDENTIAL_REFERENCE":
		provider.CredentialRef = value
	default:
		if strings.HasPrefix(suffix, "SETTING_") {
			key := strings.TrimPrefix(suffix, "SETTING_")
			if key == "" {
				return invalid(envName, "must include a setting name")
			}
			if isSensitiveConfigKey(key) {
				return fmt.Errorf("%w: environment variable %s", ErrSecretConfig, envName)
			}
			provider.Settings[strings.ToLower(strings.ReplaceAll(key, "-", "_"))] = value
			return nil
		}
		if isSensitiveConfigKey(suffix) {
			return fmt.Errorf("%w: environment variable %s", ErrSecretConfig, envName)
		}
		return nil
	}
	return nil
}

func splitProviderEnv(rest string, knownIDs map[string]string) (string, string, bool) {
	for _, suffix := range []string{"CREDENTIAL_REFERENCE", "CREDENTIAL_REF", "BASE_URL", "ENABLED", "SETTING_", "TYPE", "NAME", "URL"} {
		marker := "_" + suffix
		if !strings.HasSuffix(rest, marker) && suffix != "SETTING_" {
			continue
		}
		var encodedID string
		if suffix == "SETTING_" {
			index := strings.Index(rest, marker)
			if index <= 0 || index+len(marker) >= len(rest) {
				continue
			}
			encodedID = rest[:index]
			settingName := rest[index+len(marker):]
			id := decodeProviderEnvID(encodedID, knownIDs)
			return id, "SETTING_" + settingName, true
		}
		encodedID = strings.TrimSuffix(rest, marker)
		if encodedID == "" {
			continue
		}
		return decodeProviderEnvID(encodedID, knownIDs), suffix, true
	}
	return "", "", false
}

func providerEnvLooksSecret(rest string) bool {
	index := strings.LastIndexByte(rest, '_')
	if index < 1 || index+1 >= len(rest) {
		return false
	}
	return isSensitiveConfigKey(rest[index+1:])
}

func providerEnvID(id string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(strings.TrimSpace(id)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
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

func decodeProviderEnvID(encoded string, knownIDs map[string]string) string {
	if id, ok := knownIDs[encoded]; ok {
		return id
	}
	return strings.ToLower(strings.ReplaceAll(encoded, "_", "-"))
}
