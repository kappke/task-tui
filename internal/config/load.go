package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// LoadOptions makes configuration loading deterministic in tests without
// changing process-global environment state.
type LoadOptions struct {
	Path      string
	LookupEnv func(string) (string, bool)
	Environ   func() []string
}

// Load loads path, or the default path when path is empty. A missing default
// file is optional; a missing non-empty path is an error.
func Load(ctx context.Context, path string) (Config, error) {
	return LoadWithOptions(ctx, LoadOptions{Path: path})
}

func LoadConfig(ctx context.Context, path string) (Config, error) {
	return Load(ctx, path)
}

// LoadDefault loads the conventional optional configuration file.
func LoadDefault(ctx context.Context) (Config, error) {
	return LoadWithOptions(ctx, LoadOptions{})
}

// LoadFile always treats path as explicit. Use Load or LoadDefault when a
// missing conventional file should be accepted.
func LoadFile(ctx context.Context, path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, invalid("config.path", "must not be empty")
	}
	return load(ctx, LoadOptions{Path: path}, true)
}

// LoadWithOptions loads a typed configuration and applies environment
// overrides after file values. The default file remains optional.
func LoadWithOptions(ctx context.Context, options LoadOptions) (Config, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return load(ctx, options, false)
}

type configNotFoundError struct {
	Path string
}

func (e *configNotFoundError) Error() string {
	return fmt.Sprintf("configuration file does not exist: %s", e.Path)
}

func (e *configNotFoundError) Unwrap() []error {
	return []error{ErrConfigNotFound, os.ErrNotExist}
}

func load(ctx context.Context, options LoadOptions, explicit bool) (Config, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}

	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	environ := options.Environ
	if environ == nil {
		environ = os.Environ
	}

	path := strings.TrimSpace(options.Path)
	if path == "" {
		configuredPath, configured := firstEnv(lookupEnv, "TASKTUI_CONFIG_PATH", "TASKTUI_CONFIG_FILE", "TASKTUI_CONFIG")
		if configured && strings.TrimSpace(configuredPath) != "" {
			path = expandPath(configuredPath)
			explicit = true
		} else {
			defaultPath, err := DefaultPath()
			if err != nil {
				return Config{}, err
			}
			path = defaultPath
		}
	} else {
		path = expandPath(path)
		explicit = true
	}

	cfg := Defaults()
	data, err := readFile(ctx, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !explicit {
				if err := applyEnvironment(&cfg, lookupEnv, environ); err != nil {
					return Config{}, err
				}
				normalizeConfig(&cfg)
				if err := cfg.Validate(); err != nil {
					return Config{}, err
				}
				return cfg, nil
			}
			return Config{}, &configNotFoundError{Path: path}
		}
		return Config{}, fmt.Errorf("read configuration file: %w", err)
	}

	document, err := parseTOML(data)
	if err != nil {
		return Config{}, err
	}
	if err := applyDocument(&cfg, document); err != nil {
		return Config{}, err
	}
	if err := applyEnvironment(&cfg, lookupEnv, environ); err != nil {
		return Config{}, err
	}
	normalizeConfig(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func readFile(ctx context.Context, path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	var data bytes.Buffer
	buffer := make([]byte, 32*1024)
	var readErr error
	for {
		if err := ctx.Err(); err != nil {
			readErr = err
			break
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, writeErr := data.Write(buffer[:n])
			if writeErr != nil {
				readErr = writeErr
				break
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

func normalizeConfig(cfg *Config) {
	cfg.App.DefaultProvider = strings.TrimSpace(cfg.App.DefaultProvider)
	cfg.Database.Path = expandPath(cfg.Database.Path)
	cfg.Logging.Path = expandPath(cfg.Logging.Path)
	cfg.Logging.Level = LogLevel(strings.ToLower(strings.TrimSpace(string(cfg.Logging.Level))))
	for id, provider := range cfg.Providers {
		if provider.ID == "" {
			provider.ID = id
		}
		if provider.Type == "" {
			provider.Type = id
		}
		if provider.Name == "" {
			provider.Name = id
		}
		if provider.Settings == nil {
			provider.Settings = map[string]string{}
		}
		cfg.Providers[id] = provider
	}
}
