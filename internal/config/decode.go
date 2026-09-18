package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func applyDocument(cfg *Config, document *tomlDocument) error {
	for _, entry := range document.entries {
		if len(entry.path) == 0 {
			continue
		}
		switch entry.path[0] {
		case "app", "database", "sync", "ui", "logging":
			if len(entry.path) != 2 {
				return invalid(pathText(entry.path), "must be a scalar configuration field")
			}
			if err := applyCoreField(cfg, entry.path[0], entry.path[1], entry.value); err != nil {
				return err
			}
		case "providers", "provider":
			if entry.path[0] == "provider" {
				entry.path = append([]string{"providers"}, entry.path[1:]...)
			}
			if err := applyProviderEntry(cfg, entry); err != nil {
				return err
			}
		default:
			return invalid(pathText(entry.path), "unknown configuration section")
		}
	}
	return nil
}

func applyCoreField(cfg *Config, section, key string, value tomlValue) error {
	field := section + "." + key
	switch section {
	case "app":
		switch key {
		case "default_provider":
			parsed, err := requireString(value, field)
			if err != nil {
				return err
			}
			cfg.App.DefaultProvider = parsed
		default:
			return invalid(field, "unknown field")
		}
	case "database":
		switch key {
		case "path", "file":
			parsed, err := requireString(value, field)
			if err != nil {
				return err
			}
			cfg.Database.Path = parsed
		default:
			return invalid(field, "unknown field")
		}
	case "sync":
		switch key {
		case "enabled":
			parsed, err := requireBool(value, field)
			if err != nil {
				return err
			}
			cfg.Sync.Enabled = parsed
		case "interval", "interval_seconds":
			parsed, err := requireDuration(value, field)
			if err != nil {
				return err
			}
			cfg.Sync.Interval = parsed
		case "retry_failed", "retry":
			parsed, err := requireBool(value, field)
			if err != nil {
				return err
			}
			cfg.Sync.RetryFailed = parsed
		default:
			return invalid(field, "unknown field")
		}
	case "ui":
		switch key {
		case "vim_keys", "vim":
			parsed, err := requireBool(value, field)
			if err != nil {
				return err
			}
			cfg.UI.VimKeys = parsed
		case "show_sync_status", "show_status":
			parsed, err := requireBool(value, field)
			if err != nil {
				return err
			}
			cfg.UI.ShowSyncStatus = parsed
		default:
			return invalid(field, "unknown field")
		}
	case "logging":
		switch key {
		case "path", "file":
			parsed, err := requireString(value, field)
			if err != nil {
				return err
			}
			cfg.Logging.Path = parsed
		case "level":
			parsed, err := requireString(value, field)
			if err != nil {
				return err
			}
			cfg.Logging.Level = LogLevel(strings.ToLower(strings.TrimSpace(parsed)))
		default:
			return invalid(field, "unknown field")
		}
	}
	return nil
}

func applyProviderEntry(cfg *Config, entry tomlEntry) error {
	if len(entry.path) < 3 {
		return invalid(pathText(entry.path), "provider entries require an instance ID and field")
	}
	id := entry.path[1]
	provider, exists := cfg.Providers[id]
	if !exists {
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

	if len(entry.path) == 3 && entry.path[2] == "settings" {
		if entry.value.kind != tomlTable {
			return invalid(pathText(entry.path), "must be a table")
		}
		if err := applySettingsTable(&provider, entry.path, entry.value.table); err != nil {
			return err
		}
		cfg.Providers[id] = provider
		return nil
	}

	if len(entry.path) >= 4 {
		if entry.path[2] != "settings" || len(entry.path) != 4 {
			return invalid(pathText(entry.path), "unknown provider section")
		}
		if err := applyProviderSetting(&provider, entry.path[3], entry.value, pathText(entry.path)); err != nil {
			return err
		}
		cfg.Providers[id] = provider
		return nil
	}

	if err := applyProviderField(&provider, entry.path[2], entry.value, pathText(entry.path)); err != nil {
		return err
	}
	cfg.Providers[id] = provider
	return nil
}

func applyProviderField(provider *ProviderConfig, key string, value tomlValue, field string) error {
	switch key {
	case "id":
		parsed, err := requireString(value, field)
		if err != nil {
			return err
		}
		provider.ID = parsed
	case "name", "display_name":
		parsed, err := requireString(value, field)
		if err != nil {
			return err
		}
		provider.Name = parsed
	case "type":
		parsed, err := requireString(value, field)
		if err != nil {
			return err
		}
		provider.Type = parsed
	case "enabled":
		parsed, err := requireBool(value, field)
		if err != nil {
			return err
		}
		provider.Enabled = parsed
	case "base_url", "url":
		parsed, err := requireString(value, field)
		if err != nil {
			return err
		}
		provider.BaseURL = parsed
	case "credential_ref", "credential_reference":
		parsed, err := requireString(value, field)
		if err != nil {
			return err
		}
		provider.CredentialRef = parsed
	case "settings":
		if value.kind != tomlTable {
			return invalid(field, "must be a table")
		}
		return applySettingsTable(provider, []string{field}, value.table)
	default:
		return applyProviderSetting(provider, key, value, field)
	}
	return nil
}

func applySettingsTable(provider *ProviderConfig, path []string, values map[string]tomlValue) error {
	for key, value := range values {
		if err := applyProviderSetting(provider, key, value, pathText(append(path, key))); err != nil {
			return err
		}
	}
	return nil
}

func applyProviderSetting(provider *ProviderConfig, key string, value tomlValue, field string) error {
	if isSensitiveConfigKey(key) {
		return fmt.Errorf("%w: %s", ErrSecretConfig, field)
	}
	setting, err := settingString(value, field)
	if err != nil {
		return err
	}
	provider.Settings[key] = setting
	return nil
}

func requireString(value tomlValue, field string) (string, error) {
	if value.kind != tomlString {
		return "", invalid(field, "must be a string")
	}
	return value.stringVal, nil
}

func requireBool(value tomlValue, field string) (bool, error) {
	if value.kind != tomlBoolean {
		return false, invalid(field, "must be a boolean")
	}
	return value.boolean, nil
}

func requireDuration(value tomlValue, field string) (time.Duration, error) {
	switch value.kind {
	case tomlInteger:
		return secondsDuration(value.integer, field)
	case tomlString:
		return parseDurationText(value.stringVal, field)
	default:
		return 0, invalid(field, "must be a number of seconds or a duration string")
	}
}

func parseDurationText(text, field string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if seconds, err := strconv.ParseInt(text, 10, 64); err == nil {
		return secondsDuration(seconds, field)
	}
	duration, err := time.ParseDuration(text)
	if err != nil {
		return 0, invalid(field, "must be a number of seconds or a duration string")
	}
	return duration, nil
}

func secondsDuration(seconds int64, field string) (time.Duration, error) {
	maxSeconds := int64((1<<63 - 1) / int64(time.Second))
	if seconds > maxSeconds || seconds < -maxSeconds {
		return 0, invalid(field, "is outside the supported duration range")
	}
	return time.Duration(seconds) * time.Second, nil
}

func settingString(value tomlValue, field string) (string, error) {
	switch value.kind {
	case tomlString:
		return value.stringVal, nil
	case tomlBoolean:
		return strconv.FormatBool(value.boolean), nil
	case tomlInteger:
		return strconv.FormatInt(value.integer, 10), nil
	default:
		return "", invalid(field, "must be a string, boolean, or integer")
	}
}
