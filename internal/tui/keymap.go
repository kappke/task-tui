package tui

import "strings"

// Action is a semantic keyboard action. Mapping keys to actions separately
// from update logic keeps Vim and arrow navigation deterministic and
// configurable.
type Action string

const (
	ActionNone          Action = "none"
	ActionMoveUp        Action = "move_up"
	ActionMoveDown      Action = "move_down"
	ActionPreviousPanel Action = "previous_panel"
	ActionNextPanel     Action = "next_panel"
	ActionScrollLeft    Action = "scroll_left"
	ActionScrollRight   Action = "scroll_right"
	ActionSelect        Action = "select"
	ActionToggleGroup   Action = "toggle_group"
	ActionFirst         Action = "first"
	ActionLast          Action = "last"
	ActionQuit          Action = "quit"
	ActionCreate        Action = "create"
	ActionEdit          Action = "edit"
	ActionComplete      Action = "complete"
	ActionDelete        Action = "delete"
	ActionSearch        Action = "search"
	ActionFilter        Action = "filter"
	ActionRefresh       Action = "refresh"
	ActionCommand       Action = "command"
	ActionCancel        Action = "cancel"
	ActionBackspace     Action = "backspace"
	ActionCursorLeft    Action = "cursor_left"
	ActionCursorRight   Action = "cursor_right"
)

// KeyMap maps normalized key names to semantic actions. Key names use the
// same spelling commonly used by terminal frameworks: up, down, enter, esc,
// backspace, and ctrl+c.
type KeyMap struct {
	Bindings map[string]Action
}

// DefaultKeyMap provides the MVP Vim and arrow bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{Bindings: map[string]Action{
		"j":           ActionMoveDown,
		"down":        ActionMoveDown,
		"k":           ActionMoveUp,
		"up":          ActionMoveUp,
		"h":           ActionScrollLeft,
		"left":        ActionScrollLeft,
		"l":           ActionScrollRight,
		"right":       ActionScrollRight,
		"tab":         ActionNextPanel,
		"shift+tab":   ActionPreviousPanel,
		"enter":       ActionSelect,
		"space":       ActionToggleGroup,
		"g":           ActionFirst,
		"G":           ActionLast,
		"q":           ActionQuit,
		"ctrl+c":      ActionQuit,
		"n":           ActionCreate,
		"e":           ActionEdit,
		"x":           ActionComplete,
		"d":           ActionDelete,
		"/":           ActionSearch,
		"f":           ActionFilter,
		"r":           ActionRefresh,
		":":           ActionCommand,
		"esc":         ActionCancel,
		"escape":      ActionCancel,
		"backspace":   ActionBackspace,
		"ctrl+h":      ActionBackspace,
		"shift+left":  ActionCursorLeft,
		"shift+right": ActionCursorRight,
	}}
}

// Action returns the action bound to key. A zero KeyMap uses the defaults.
func (km KeyMap) Action(key string) Action {
	key = normalizeKey(key)
	bindings := km.Bindings
	if len(bindings) == 0 {
		bindings = DefaultKeyMap().Bindings
	}
	if action, ok := bindings[key]; ok {
		return action
	}
	return ActionNone
}

// MapKey maps a key using the default MVP bindings.
func MapKey(key string) Action {
	return DefaultKeyMap().Action(key)
}

// KeyAction is a descriptive alias for MapKey.
func KeyAction(key string) Action {
	return MapKey(key)
}

// Key is a convenience message for callers that do not need KeyMsg fields.
type Key string

// KeyMsg is the framework-neutral keyboard message accepted by Model.Update.
// Text or Runes can carry pasted/multi-rune input in text modes.
type KeyMsg struct {
	Key   string
	Text  string
	Runes []rune
}

// NewKeyMsg creates a key message from a terminal key name.
func NewKeyMsg(key string) KeyMsg {
	return KeyMsg{Key: key}
}

func (k KeyMsg) name() string {
	if k.Key != "" {
		return normalizeKey(k.Key)
	}
	if len(k.Runes) == 1 {
		return normalizeKey(string(k.Runes[0]))
	}
	return normalizeKey(k.Text)
}

func (k KeyMsg) text() string {
	if len(k.Runes) > 0 {
		return string(k.Runes)
	}
	if k.Text != "" {
		return k.Text
	}
	if k.Key == "" || isNamedKey(k.Key) {
		return ""
	}
	return k.Key
}

func isNamedKey(value string) bool {
	switch normalizeKey(value) {
	case "up", "down", "left", "right", "enter", "esc", "escape", "backspace", "ctrl+h", "ctrl+c", "tab", "shift+tab", "home", "end", "delete":
		return true
	default:
		return false
	}
}

func normalizeKey(value string) string {
	switch strings.ToLower(value) {
	case " ":
		return "space"
	case "\r", "\n":
		return "enter"
	case "return":
		return "enter"
	case "\t":
		return "tab"
	case "arrowup":
		return "up"
	case "arrowdown":
		return "down"
	case "arrowleft":
		return "left"
	case "arrowright":
		return "right"
	case "\x1b":
		return "esc"
	case "\b", "\x7f":
		return "backspace"
	}
	if value == "G" {
		return value
	}
	return strings.TrimSpace(strings.ToLower(value))
}
