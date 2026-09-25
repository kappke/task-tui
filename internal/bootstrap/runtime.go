package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kappke/task-tui/internal/app"
	"github.com/kappke/task-tui/internal/command"
	"github.com/kappke/task-tui/internal/storage/sqlite"
	foundationsync "github.com/kappke/task-tui/internal/sync"
)

// Options controls Build. Zero options use the local-first defaults.
type Options struct {
	Config     Config
	ConfigPath string
	Headless   bool

	Input  io.Reader
	Output io.Writer

	// Logger is borrowed, not closed, by Runtime. Supplying it is useful for
	// tests and embedding; the default is an owner-only file logger.
	Logger *slog.Logger

	// Dependencies can replace the concrete store, application, sync engine,
	// and UI for lifecycle tests. If it is nil, Build constructs all defaults.
	Dependencies *RuntimeDependencies
}

// RuntimeDependencies are the lifecycle seams used by Build and tests.
type RuntimeDependencies struct {
	Store   DataStore
	Handler command.Handler
	Sync    SyncController
	UI      UIController

	// CloseLogger is called only when a test or embedding supplies a logger
	// whose lifetime is managed outside slog. The normal file logger uses its
	// internal LogSink instead.
	CloseLogger func() error
}

// Runtime owns all application resources. It has no package-global mutable
// state and can be constructed more than once in one process.
type Runtime struct {
	config Config

	store   DataStore
	handler command.Handler
	sync    SyncController
	ui      UIController
	logger  *slog.Logger
	logSink *LogSink

	registry *Registry
	repo     *Repository
	app      *Application
	engine   *SyncEngine

	cliStore     *sqlite.Store
	cliProviders *app.Registry
	cliService   *app.Service
	cliEngine    *foundationsync.Engine

	mu            sync.Mutex
	phase         runtimePhase
	uiInitialized bool
	shutdownErr   error
	closeLogger   func() error
}

type runtimePhase uint8

const (
	runtimeNew runtimePhase = iota
	runtimeStarting
	runtimeRunning
	runtimeShuttingDown
	runtimeStopped
)

// Build constructs the complete dependency graph without entering the
// terminal, authenticating, synchronizing, or making network requests.
func Build(ctx context.Context, options Options) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("build runtime: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("build runtime: %w", err)
	}
	cfg, err := buildConfig(ctx, options)
	if err != nil {
		return nil, err
	}
	logger := options.Logger
	var logSink *LogSink
	closeLogger := func() error { return nil }
	if logger == nil {
		logger, logSink, err = OpenLogger(cfg.Logging)
		if err != nil {
			return nil, fmt.Errorf("build runtime logger: %w", err)
		}
		closeLogger = logSink.Close
	}
	if options.Dependencies != nil && options.Dependencies.CloseLogger != nil {
		closeLogger = options.Dependencies.CloseLogger
	}

	if options.Dependencies != nil && hasCompleteDependencies(options.Dependencies) {
		return &Runtime{
			config:      cfg,
			store:       options.Dependencies.Store,
			handler:     options.Dependencies.Handler,
			sync:        options.Dependencies.Sync,
			ui:          options.Dependencies.UI,
			logger:      logger,
			logSink:     logSink,
			phase:       runtimeNew,
			closeLogger: closeLogger,
		}, nil
	}
	if options.Dependencies != nil && hasPartialDependencies(options.Dependencies) {
		if logSink != nil {
			_ = logSink.Close()
		}
		return nil, errors.New("build runtime: dependencies must be supplied together")
	}

	var terminal Terminal
	if options.Headless || cfg.UI.Headless {
		terminal = NewStreamTerminal(options.Input, options.Output, true)
	} else {
		terminal = NewDefaultTerminal(options.Input, options.Output)
	}
	graph, err := buildFoundationGraph(ctx, cfg, terminal, logger)
	if err != nil {
		if logSink != nil {
			_ = logSink.Close()
		}
		return nil, fmt.Errorf("build runtime foundation graph: %w", err)
	}
	return &Runtime{
		config:       cfg,
		store:        graph.store,
		handler:      graph.handler,
		sync:         graph.sync,
		ui:           graph.ui,
		logger:       logger,
		logSink:      logSink,
		cliStore:     graph.cliStore,
		cliProviders: graph.cliProviders,
		cliService:   graph.cliService,
		cliEngine:    graph.cliEngine,
		phase:        runtimeNew,
		closeLogger:  closeLogger,
	}, nil
}

func buildConfig(ctx context.Context, options Options) (Config, error) {
	if options.ConfigPath != "" {
		cfg, err := LoadConfig(ctx, options.ConfigPath)
		if err != nil {
			return Config{}, fmt.Errorf("build runtime config: %w", err)
		}
		return cfg, nil
	}
	cfg := options.Config
	if isZeroConfig(cfg) {
		return LoadConfig(ctx, "")
	}
	applyEnvironment(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("build runtime config: %w", err)
	}
	return cfg, nil
}

func isZeroConfig(cfg Config) bool {
	return cfg.App == (AppConfig{}) && cfg.Sync == (SyncConfig{}) && cfg.UI == (UIConfig{}) && cfg.Logging == (LoggingConfig{}) && cfg.Database == (DatabaseConfig{}) && cfg.ClickUp == (ClickUpConfig{})
}

func hasCompleteDependencies(dependencies *RuntimeDependencies) bool {
	return dependencies != nil && dependencies.Store != nil && dependencies.Handler != nil && dependencies.Sync != nil && dependencies.UI != nil
}

func hasPartialDependencies(dependencies *RuntimeDependencies) bool {
	if dependencies == nil {
		return false
	}
	return dependencies.Store != nil || dependencies.Handler != nil || dependencies.Sync != nil || dependencies.UI != nil
}

// Start performs the local-first startup sequence without starting provider
// synchronization. Remote fetches are requested explicitly by TUI actions.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil {
		return errors.New("start runtime: nil runtime")
	}
	if ctx == nil {
		return errors.New("start runtime: nil context")
	}
	r.mu.Lock()
	if r.phase != runtimeNew {
		phase := r.phase
		r.mu.Unlock()
		return fmt.Errorf("start runtime: invalid lifecycle phase %d", phase)
	}
	r.phase = runtimeStarting
	r.mu.Unlock()
	logContext(ctx, r.logger, slog.LevelInfo, "runtime start", "config_path", r.config.Logging.Path, "headless", r.config.UI.Headless)
	if err := ctx.Err(); err != nil {
		r.failStart()
		return fmt.Errorf("start runtime: %w", err)
	}
	if err := r.ui.Initialize(ctx); err != nil {
		r.failStart()
		return fmt.Errorf("initialize UI: %w", err)
	}
	logContext(ctx, r.logger, slog.LevelDebug, "ui initialized")
	r.mu.Lock()
	r.uiInitialized = true
	r.mu.Unlock()
	state, err := r.store.LoadUIState(ctx)
	if err != nil {
		r.failStart()
		return fmt.Errorf("load UI state: %w", err)
	}
	view, err := r.store.Snapshot(ctx)
	if err != nil {
		r.failStart()
		return fmt.Errorf("load local cached view: %w", err)
	}
	logContext(ctx, r.logger, slog.LevelDebug, "local cache loaded")
	r.ui.SetState(state)
	if err := r.ui.Render(ctx, view); err != nil {
		r.failStart()
		return fmt.Errorf("render initial cached view: %w", err)
	}
	r.mu.Lock()
	r.phase = runtimeRunning
	r.mu.Unlock()
	return nil
}

func (r *Runtime) failStart() {
	r.mu.Lock()
	if r.phase == runtimeStarting {
		r.phase = runtimeNew
	}
	r.mu.Unlock()
}

// Run starts the runtime, processes the TUI, and always performs ordered
// shutdown. Context cancellation is a normal exit path.
func (r *Runtime) Run(ctx context.Context) error {
	if r == nil {
		return errors.New("run runtime: nil runtime")
	}
	if err := r.Start(ctx); err != nil {
		cleanupCtx, cancel := shutdownContext(r.config.Sync.ShutdownTimeout)
		cleanupErr := r.Shutdown(cleanupCtx)
		cancel()
		return errors.Join(err, cleanupErr)
	}
	uiErr := r.ui.Run(ctx)
	cleanupCtx, cancel := shutdownContext(r.config.Sync.ShutdownTimeout)
	cleanupErr := r.Shutdown(cleanupCtx)
	cancel()
	if uiErr != nil && !errors.Is(uiErr, context.Canceled) && !errors.Is(uiErr, context.DeadlineExceeded) {
		return errors.Join(uiErr, cleanupErr)
	}
	return cleanupErr
}

// Shutdown persists UI state, stops outstanding sync requests, closes the
// store, and restores terminal state in that order. It is idempotent.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("shutdown runtime: nil context")
	}
	r.mu.Lock()
	if r.phase == runtimeStopped {
		err := r.shutdownErr
		r.mu.Unlock()
		return err
	}
	if r.phase == runtimeShuttingDown {
		r.mu.Unlock()
		return errors.New("shutdown runtime already in progress")
	}
	r.phase = runtimeShuttingDown
	uiInitialized := r.uiInitialized
	r.mu.Unlock()
	logContext(ctx, r.logger, slog.LevelInfo, "runtime shutdown")

	var shutdownErr error
	r.ui.StopAccepting()
	if stopper, ok := r.handler.(interface{ StopAccepting() }); ok {
		stopper.StopAccepting()
	}
	if uiInitialized {
		if err := r.store.SaveUIState(ctx, r.ui.State()); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("save UI state: %w", err))
		}
	}
	if r.sync != nil {
		if err := r.sync.Stop(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("stop sync requests: %w", err))
			if retryErr := r.sync.Stop(context.Background()); retryErr != nil {
				shutdownErr = errors.Join(shutdownErr, fmt.Errorf("finish sync requests: %w", retryErr))
			}
		}
	}
	if err := r.store.Close(); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close local store: %w", err))
	}
	if err := r.ui.Restore(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("restore terminal: %w", err))
	}
	if r.closeLogger != nil {
		if err := r.closeLogger(); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close logger: %w", err))
		}
	}
	r.mu.Lock()
	r.shutdownErr = shutdownErr
	r.phase = runtimeStopped
	r.mu.Unlock()
	return shutdownErr
}

// Config returns the immutable configuration snapshot used by the runtime.
func (r *Runtime) Config() Config {
	if r == nil {
		return Config{}
	}
	return r.config
}

// TUI returns the default presentation model.
func (r *Runtime) TUI() *TUI {
	if r == nil {
		return nil
	}
	tui, _ := r.ui.(*TUI)
	return tui
}

// Run builds no resources itself; it exists as a testable top-level lifecycle
// function and installs signal cancellation around the supplied runtime.
func Run(ctx context.Context, runtime *Runtime) error {
	if ctx == nil {
		return errors.New("run: nil context")
	}
	if runtime == nil {
		return errors.New("run: nil runtime")
	}
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runtime.Run(signalCtx)
}

// Main is the small executable-facing composition entry point.
func Main(ctx context.Context, options Options) error {
	if isZeroConfig(options.Config) {
		cfg, err := LoadConfig(ctx, options.ConfigPath)
		if err != nil {
			return err
		}
		if onboardingEnabled(options, cfg) {
			path, pathIsExplicit, err := resolveConfigPath(options.ConfigPath)
			if err != nil {
				return err
			}
			_, statErr := os.Stat(path)
			configExists := statErr == nil
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("inspect configuration file: %w", statErr)
			}
			if !configExists && !pathIsExplicit && !cfg.ClickUp.Enabled {
				cfg, err = runFirstRunSetup(ctx, options, path, cfg)
				if err != nil {
					return err
				}
			} else if cfg.ClickUp.Enabled {
				if err := promptForClickUpToken(ctx, options, cfg); err != nil {
					return err
				}
			}
		}
		options.Config = cfg
		options.ConfigPath = ""
	}
	runtime, err := Build(ctx, options)
	if err != nil {
		return err
	}
	return Run(ctx, runtime)
}

func onboardingEnabled(options Options, cfg Config) bool {
	if options.Headless || cfg.UI.Headless {
		return false
	}
	input := options.Input
	if input == nil {
		input = os.Stdin
	}
	output := options.Output
	if output == nil {
		output = os.Stdout
	}
	return isCharDevice(input) && isCharDevice(output)
}

func shutdownContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}
