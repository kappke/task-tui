package bootstrap

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOnboardingChoosesClickUpAndMasksTokenInput(t *testing.T) {
	model := newOnboardingModel(false)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(*onboardingModel)
	if model.mode != onboardingEnterToken || model.provider != ProviderID("clickup") {
		t.Fatalf("state after selecting ClickUp = mode %d provider %q", model.mode, model.provider)
	}

	const token = "secret-clickup-token"
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(token)})
	model = updated.(*onboardingModel)
	if strings.Contains(model.View(), token) {
		t.Fatal("token was rendered in plaintext while entering it")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*onboardingModel)
	if model.mode != onboardingComplete || model.token != token {
		t.Fatalf("completed onboarding = mode %d token saved %v", model.mode, model.token == token)
	}
}

func TestOnboardingCanStartWithLocalProvider(t *testing.T) {
	model := newOnboardingModel(false)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	model = updated.(*onboardingModel)
	if model.mode != onboardingComplete || model.provider != ProviderID("local") || command == nil {
		t.Fatalf("local provider selection = mode %d provider %q command %v", model.mode, model.provider, command != nil)
	}
}

func TestMissingClickUpTokenStartsDirectlyAtPasswordPrompt(t *testing.T) {
	model := newOnboardingModel(true)
	if model.mode != onboardingEnterToken || model.provider != ProviderID("clickup") {
		t.Fatalf("missing-token onboarding = mode %d provider %q", model.mode, model.provider)
	}
	if !strings.Contains(model.View(), "no saved API token") {
		t.Fatalf("missing-token prompt = %q", model.View())
	}
}
