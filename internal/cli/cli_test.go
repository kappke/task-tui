package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/storage/sqlite"
)

func TestLocalTaskCommandsUsePersistentCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "tasks.db")
	configPath := filepath.Join(dir, "config.toml")
	logPath := filepath.Join(dir, "tasks.log")
	config := "[app]\ndefault_provider = \"local\"\n\n[database]\npath = \"" + databasePath + "\"\n\n[logging]\npath = \"" + logPath + "\"\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateProvider(context.Background(), domain.Provider{
		ID: "local", Type: domain.ProviderTypeLocal, Name: "Local", Enabled: true,
		SyncState: domain.SyncStateLocal, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	space, err := store.CreateSpace(context.Background(), domain.Space{
		ID: "space-1", ProviderID: "local", Name: "Personal", SyncState: domain.SyncStateLocal,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.CreateList(context.Background(), domain.List{
		ID: "list-1", ProviderID: "local", SpaceID: space.ID, Name: "Today", SyncState: domain.SyncStateLocal,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var created bytes.Buffer
	err = Run(context.Background(), []string{"--config", configPath, "task", "add", "--json", "--sync", "--provider", "local", "--list", string(list.ID), "--title", "Buy coffee"}, &created, &created)
	if err != nil {
		t.Fatal(err)
	}
	var createdTask domain.Task
	if err := json.Unmarshal(created.Bytes(), &createdTask); err != nil {
		t.Fatalf("decode create output %q: %v", created.String(), err)
	}
	if createdTask.Title != "Buy coffee" || createdTask.SyncState != domain.SyncStateLocal {
		t.Fatalf("unexpected created task: %#v", createdTask)
	}

	var details bytes.Buffer
	err = Run(context.Background(), []string{"--config", configPath, "task", "get", "--json", "--provider", "local", "--id", string(createdTask.ID)}, &details, &details)
	if err != nil {
		t.Fatal(err)
	}
	var fetchedTask domain.Task
	if err := json.Unmarshal(details.Bytes(), &fetchedTask); err != nil {
		t.Fatalf("decode get output %q: %v", details.String(), err)
	}
	if fetchedTask.ID != createdTask.ID || fetchedTask.Title != createdTask.Title {
		t.Fatalf("unexpected fetched task: %#v", fetchedTask)
	}
}

func TestIsCommand(t *testing.T) {
	if !IsCommand([]string{"--config", "config.toml", "sync"}) {
		t.Fatal("sync command was not detected")
	}
	if IsCommand([]string{"--headless"}) {
		t.Fatal("headless mode was detected as a CLI command")
	}
}
