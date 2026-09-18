package bootstrap

import (
	"context"
	"errors"

	"github.com/kappke/task-tui/internal/command"
)

// UIController is the lifecycle-facing TUI contract.
type UIController interface {
	Initialize(context.Context) error
	SetState(UIState)
	Render(context.Context, View) error
	Run(context.Context) error
	State() UIState
	StopAccepting()
	Restore(context.Context) error
}

// ViewLoader reloads local data after an application command. It must never
// access a remote provider.
type ViewLoader func(context.Context) (View, error)

// TUI is the Bubble Tea-backed presentation controller used by the default
// runtime. The alias preserves the small lifecycle API for embedders while the
// implementation remains in the foundation adapter.
type TUI = foundationUIController

// NewTUI constructs the Charm-based TUI without a synchronization event
// stream. The default foundation graph adds its provider events separately.
func NewTUI(terminal Terminal, handler command.Handler, load ViewLoader) (*TUI, error) {
	if terminal == nil {
		return nil, errors.New("new TUI: nil terminal")
	}
	if handler == nil {
		return nil, errors.New("new TUI: nil command handler")
	}
	return newFoundationUIController(terminal, handler, load, nil)
}
