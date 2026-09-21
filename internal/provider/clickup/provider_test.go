package clickup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/kappke/task-tui/internal/domain"
)

func TestProviderRejectsMismatchedTaskProviderBeforeHTTP(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"})
	provider := New(client, domain.ProviderID("clickup-work"))
	remoteID := "remote-task"
	localTask := domain.Task{ProviderID: domain.ProviderID("local"), RemoteID: &remoteID}

	if _, err := provider.UpdateTask(context.Background(), localTask); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if err := provider.DeleteTask(context.Background(), localTask); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("HTTP requests = %d, want 0", requests)
	}
}

func TestProviderUsesRemoteIDsForTaskOperations(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/task/remote-task":
			_, _ = io.WriteString(w, `{"id":"remote-task","name":"updated","status":{"status":"open"}}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/task/remote-task":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"})
	clickupProvider := New(client, domain.ProviderID("clickup-work"))
	remoteID := "remote-task"
	task := domain.Task{
		ID:         domain.TaskID("local-task"),
		ProviderID: domain.ProviderID("clickup-work"),
		RemoteID:   &remoteID,
		ListID:     domain.ListID("remote-list"),
		Title:      "updated",
	}

	updated, err := clickupProvider.UpdateTask(context.Background(), task)
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if updated.ID == "" || updated.ID == "remote-task" {
		t.Fatalf("updated ID = %q, want independent local identity", updated.ID)
	}
	if updated.RemoteID == nil || *updated.RemoteID != "remote-task" {
		t.Fatalf("updated RemoteID = %v, want remote-task", updated.RemoteID)
	}
	if err := clickupProvider.DeleteTask(context.Background(), task); err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"PUT /task/remote-task", "DELETE /task/remote-task"}) {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestProviderCreatesTaskInRemoteListAndMapsProviderID(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost || gotPath != "/list/remote-list/task" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"id":"remote-created","name":"new task","list":{"id":"remote-list"}}`)
	}))
	defer server.Close()

	clickupProvider := New(NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: "token",
	}), ProviderConfig{
		ProviderID: "clickup-work",
		RemoteListResolver: func(domain.ListID) (string, error) {
			return "remote-list", nil
		},
	})
	task := domain.Task{
		ProviderID: domain.ProviderID("clickup-work"),
		ListID:     domain.ListID("local-list"),
		Title:      "new task",
	}

	created, err := clickupProvider.CreateTask(context.Background(), task)
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if gotPath != "/list/remote-list/task" || created.ProviderID != "clickup-work" || created.ListID != "local-list" {
		t.Fatalf("path = %q, task = %#v", gotPath, created)
	}
}

func TestProviderResolvesLocalSpaceIDBeforeFetchingLists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/space/remote-space/list" && r.URL.Path != "/space/remote-space/folder" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/space/remote-space/list" {
			_, _ = io.WriteString(w, `{"lists":[{"id":"remote-list","name":"Backend"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"folders":[]}`)
	}))
	defer server.Close()
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID:          "clickup-work",
		RemoteSpaceResolver: func(domain.SpaceID) (string, error) { return "remote-space", nil },
	})
	lists, err := provider.FetchLists(context.Background(), "local-space")
	if err != nil || len(lists) != 1 || lists[0].SpaceID != "local-space" {
		t.Fatalf("FetchLists() = %#v, %v", lists, err)
	}
}

func TestProviderFetchTaskUsesRemoteTaskCallAndLocalListIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/task/remote-task" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"id":"remote-task","name":"Task","list":{"id":"remote-list"}}`)
	}))
	defer server.Close()

	provider := New(NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: "token",
	}), ProviderConfig{
		ProviderID:         "clickup-work",
		RemoteTaskResolver: func(domain.TaskID) (string, error) { return "remote-task", nil },
		LocalListResolver:  func(string) (domain.ListID, error) { return "local-list", nil },
	})

	task, err := provider.FetchTask(context.Background(), "local-task")
	if err != nil {
		t.Fatalf("FetchTask() error = %v", err)
	}
	if task.ID != "local-task" || task.ListID != "local-list" || task.RemoteID == nil || *task.RemoteID != "remote-task" {
		t.Fatalf("FetchTask() = %#v, want local task/list identities and remote task ID", task)
	}
}

func TestProviderCapabilitiesAdvertiseSupportedTaskFeatures(t *testing.T) {
	clickupProvider := New(NewClientWithToken("token"), domain.ProviderID("clickup"))
	capabilities := clickupProvider.Capabilities()
	if !capabilities.CreateTask || !capabilities.UpdateTask || !capabilities.DeleteTask || !capabilities.DueDates {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestProviderUpdatePayloadClearsDescriptionAndResolvesParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		switch r.URL.Path {
		case "/task/remote-task":
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode update payload: %v", err)
			}
			if payload["description"] != " " {
				t.Errorf("description payload = %#v, want single space", payload["description"])
			}
			if payload["parent"] != "remote-parent" {
				t.Errorf("parent payload = %#v, want remote parent ID", payload["parent"])
			}
			if value, ok := payload["due_date"]; !ok || value != nil {
				t.Errorf("due date payload = %#v, want explicit null", payload["due_date"])
			}
			_, _ = io.WriteString(w, `{"id":"remote-task","name":"updated","status":{"status":"open"},"list":{"id":"old-list"}}`)
		case "/api/v3/workspaces/workspace/tasks/remote-task/home_list/remote-list":
			_, _ = io.WriteString(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := New(NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: "token",
	}), ProviderConfig{
		ProviderID: "clickup-work",
		TeamID:     "workspace",
		RemoteTaskResolver: func(domain.TaskID) (string, error) {
			return "remote-parent", nil
		},
		RemoteListResolver: func(domain.ListID) (string, error) {
			return "remote-list", nil
		},
	})
	parentID := domain.TaskID("local-parent")
	_, err := provider.UpdateTask(context.Background(), domain.Task{
		ID:           "local-task",
		ProviderID:   "clickup-work",
		ListID:       "local-list",
		RemoteID:     stringPointer("remote-task"),
		ParentTaskID: &parentID,
		Title:        "updated",
		Status:       "open",
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
}

func TestProviderUpdatePayloadClearsParentWithNull(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode update payload: %v", err)
		}
		if value, ok := payload["parent"]; !ok || value != nil {
			t.Errorf("parent payload = %#v, want explicit null", payload["parent"])
		}
		_, _ = io.WriteString(w, `{"id":"remote-task","name":"updated","status":{"status":"open"}}`)
	}))
	defer server.Close()
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID:         "clickup-work",
		RemoteListResolver: func(domain.ListID) (string, error) { return "remote-list", nil },
	})
	_, err := provider.UpdateTask(context.Background(), domain.Task{
		ProviderID: "clickup-work", ListID: "local-list", RemoteID: stringPointer("remote-task"), Title: "updated",
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
}
