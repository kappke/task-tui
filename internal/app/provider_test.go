package app

import (
	"errors"
	"testing"
)

func TestNewRegistryRejectsDuplicateProviderIDs(t *testing.T) {
	first := fullProvider("duplicate", "local")
	second := fullProvider("duplicate", "clickup")
	registry := NewRegistry(first)
	if err := registry.Register(second); !errors.Is(err, ErrProviderAlreadyRegistered) {
		t.Fatalf("duplicate registration error = %v", err)
	}
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("NewRegistry accepted duplicate provider ID")
		}
	}()
	NewRegistry(first, second)
}
