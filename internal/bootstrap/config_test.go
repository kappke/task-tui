package bootstrap

import (
	"strings"
	"testing"
)

func TestConfigRejectsClickUpIDThatCollidesWithLocalProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ClickUp.Enabled = true
	cfg.ClickUp.ID = ProviderID("local")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicates the local provider") {
		t.Fatalf("validation error = %v", err)
	}
}
