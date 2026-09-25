package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kappke/task-tui/internal/credentials"
)

var errOnboardingCancelled = errors.New("provider setup cancelled")

type onboardingResult struct {
	provider ProviderID
	token    string
}

type onboardingMode uint8

const (
	onboardingChooseProvider onboardingMode = iota
	onboardingEnterToken
	onboardingComplete
)

type onboardingModel struct {
	mode       onboardingMode
	provider   ProviderID
	selection  int
	input      textinput.Model
	token      string
	cancelled  bool
	missingKey bool
	errorText  string
}

func newOnboardingModel(missingKey bool) *onboardingModel {
	input := textinput.New()
	input.Prompt = "Token: "
	input.Placeholder = "paste ClickUp API token"
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.CharLimit = 4096
	input.Width = 56
	model := &onboardingModel{
		mode:       onboardingChooseProvider,
		provider:   ProviderID("local"),
		input:      input,
		missingKey: missingKey,
	}
	if missingKey {
		model.provider = ProviderID("clickup")
		model.beginTokenInput()
	}
	return model
}

func (m *onboardingModel) beginTokenInput() {
	m.mode = onboardingEnterToken
	m.input.SetValue("")
	m.input.Focus()
}

func (m *onboardingModel) Init() tea.Cmd {
	if m.mode == onboardingEnterToken {
		return textinput.Blink
	}
	return nil
}

func (m *onboardingModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := message.(tea.KeyMsg)
	if isKey && (key.String() == "ctrl+c" || key.String() == "esc") {
		m.cancelled = true
		m.mode = onboardingComplete
		m.token = ""
		m.input.SetValue("")
		return m, tea.Quit
	}

	switch m.mode {
	case onboardingChooseProvider:
		if !isKey {
			return m, nil
		}
		switch key.String() {
		case "up", "k":
			m.selection = 0
		case "down", "j":
			m.selection = 1
		case "1", "l":
			m.selection = 0
			m.provider = ProviderID("local")
			m.mode = onboardingComplete
			return m, tea.Quit
		case "2", "c":
			m.selection = 1
			m.provider = ProviderID("clickup")
			m.beginTokenInput()
			return m, textinput.Blink
		case "enter":
			if m.selection == 0 {
				m.provider = ProviderID("local")
				m.mode = onboardingComplete
				return m, tea.Quit
			}
			m.provider = ProviderID("clickup")
			m.beginTokenInput()
			return m, textinput.Blink
		}
	case onboardingEnterToken:
		if isKey && key.String() == "enter" {
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				m.errorText = "Token cannot be empty"
				return m, nil
			}
			m.token = value
			m.input.SetValue("")
			m.mode = onboardingComplete
			return m, tea.Quit
		}
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		m.errorText = ""
		return m, command
	}
	return m, nil
}

func (m *onboardingModel) View() string {
	switch m.mode {
	case onboardingChooseProvider:
		local := "  Local   Offline tasks stored on this device"
		clickup := "  ClickUp  Sync tasks with your ClickUp account"
		if m.selection == 0 {
			local = "> Local   Offline tasks stored on this device"
		} else {
			clickup = "> ClickUp  Sync tasks with your ClickUp account"
		}
		return "Welcome to Task Manager\n\nWhich provider would you like to start with?\n\n" + local + "\n" + clickup + "\n\nUse ↑/↓ and Enter, or press 1 or 2."
	case onboardingEnterToken:
		message := "Set up ClickUp\n\nCreate an API token in your ClickUp account settings, then enter it below. It will be stored separately from your task database and hidden while you type.\n\n" + m.input.View()
		if m.missingKey {
			message = "ClickUp has no saved API token. Enter one to open this provider.\n\nCreate an API token in your ClickUp account settings. The value is stored separately from your task database and hidden while you type.\n\n" + m.input.View()
		}
		if m.errorText != "" {
			message += "\n\n" + m.errorText
		}
		return message + "\n\nEnter to save · Esc to cancel"
	default:
		return ""
	}
}

func runOnboarding(ctx context.Context, input io.Reader, output io.Writer, missingKey bool) (onboardingResult, error) {
	if ctx == nil {
		return onboardingResult{}, errors.New("run provider setup: nil context")
	}
	if err := ctx.Err(); err != nil {
		return onboardingResult{}, err
	}
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = os.Stdout
	}
	program := tea.NewProgram(newOnboardingModel(missingKey), tea.WithInput(input), tea.WithOutput(output), tea.WithContext(ctx), tea.WithoutSignalHandler())
	result, err := program.Run()
	if err != nil {
		return onboardingResult{}, fmt.Errorf("run provider setup: %w", err)
	}
	model, ok := result.(*onboardingModel)
	if !ok {
		return onboardingResult{}, errors.New("run provider setup: invalid result")
	}
	if model.cancelled {
		return onboardingResult{}, errOnboardingCancelled
	}
	return onboardingResult{provider: model.provider, token: model.token}, nil
}

func runFirstRunSetup(ctx context.Context, options Options, configPath string, cfg Config) (Config, error) {
	result, err := runOnboarding(ctx, options.Input, options.Output, false)
	if err != nil {
		return Config{}, err
	}
	cfg.App.DefaultProvider = result.provider
	if result.provider == ProviderID("clickup") {
		cfg.ClickUp.Enabled = true
		if strings.TrimSpace(cfg.ClickUp.TokenEnv) == "" {
			cfg.ClickUp.TokenEnv = "TASKTUI_CLICKUP_TOKEN"
		}
		if err := saveClickUpToken(ctx, result.token); err != nil {
			return Config{}, err
		}
	}
	if err := saveOnboardingConfig(configPath, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func promptForClickUpToken(ctx context.Context, options Options, cfg Config) error {
	if hasClickUpToken(ctx, cfg) {
		return nil
	}
	result, err := runOnboarding(ctx, options.Input, options.Output, true)
	if err != nil {
		return err
	}
	return saveClickUpToken(ctx, result.token)
}

func hasClickUpToken(ctx context.Context, cfg Config) bool {
	path, err := credentials.DefaultFilePath()
	if err != nil {
		return false
	}
	if _, err := credentials.NewFileStore(path).Lookup(ctx, "clickup"); err == nil {
		return true
	}
	if cfg.ClickUp.TokenEnv == "" {
		return false
	}
	value, ok := os.LookupEnv(cfg.ClickUp.TokenEnv)
	return ok && strings.TrimSpace(value) != ""
}

func saveClickUpToken(ctx context.Context, token string) error {
	path, err := credentials.DefaultFilePath()
	if err != nil {
		return err
	}
	if err := credentials.NewFileStore(path).Set(ctx, "clickup", token); err != nil {
		return fmt.Errorf("save ClickUp token: %w", err)
	}
	return nil
}

func saveOnboardingConfig(path string, cfg Config) error {
	var data strings.Builder
	data.WriteString("[app]\n")
	data.WriteString("default_provider = " + strconv.Quote(string(cfg.App.DefaultProvider)) + "\n")
	if cfg.ClickUp.Enabled {
		data.WriteString("\n[providers.clickup]\n")
		data.WriteString("enabled = true\n")
		data.WriteString("id = " + strconv.Quote(string(cfg.ClickUp.ID)) + "\n")
		data.WriteString("name = " + strconv.Quote(cfg.ClickUp.Name) + "\n")
		data.WriteString("base_url = " + strconv.Quote(cfg.ClickUp.BaseURL) + "\n")
		if cfg.ClickUp.WorkspaceID != "" {
			data.WriteString("workspace_id = " + strconv.Quote(cfg.ClickUp.WorkspaceID) + "\n")
		}
		data.WriteString("token_env = " + strconv.Quote(cfg.ClickUp.TokenEnv) + "\n")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure config directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".config-*")
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure config file: %w", err)
	}
	if _, err := temporary.WriteString(data.String()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write config file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync config file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close config file: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("install config file: %w", err)
	}
	return nil
}
