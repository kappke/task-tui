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
		case r.Method == http.MethodGet && r.URL.Path == "/task/remote-task":
			_, _ = io.WriteString(w, `{"id":"remote-task","list":{"id":"remote-list"}}`)
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
	if !reflect.DeepEqual(paths, []string{"GET /task/remote-task", "PUT /task/remote-task", "DELETE /task/remote-task"}) {
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

func TestProviderCreatesTaskAndAddsItsAdditionalListMemberships(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/list/remote-primary/task":
			_, _ = io.WriteString(w, `{"id":"remote-task","name":"Shared","list":{"id":"remote-primary"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/list/remote-extra/task/remote-task":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID: "clickup-work",
		RemoteListResolver: func(listID domain.ListID) (string, error) {
			if listID == "primary" {
				return "remote-primary", nil
			}
			return "remote-extra", nil
		},
	})

	created, err := provider.CreateTask(context.Background(), domain.Task{
		ProviderID: "clickup-work", ListID: "primary", ListIDs: []domain.ListID{"primary", "extra"}, Title: "Shared",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if !reflect.DeepEqual(created.ListIDs, []domain.ListID{"primary", "extra"}) {
		t.Fatalf("created memberships = %v", created.ListIDs)
	}
	want := []string{"POST /list/remote-primary/task", "POST /list/remote-extra/task/remote-task"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("create paths = %v, want %v", paths, want)
	}
}

func TestProviderRollsBackTaskWhenAdditionalListCreateFails(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/list/remote-primary/task":
			_, _ = io.WriteString(w, `{"id":"remote-task","name":"Shared","list":{"id":"remote-primary"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/list/remote-extra/task/remote-task":
			http.Error(w, `{"err":"temporary failure"}`, http.StatusServiceUnavailable)
		case r.Method == http.MethodDelete && r.URL.Path == "/task/remote-task":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID: "clickup-work",
		RemoteListResolver: func(listID domain.ListID) (string, error) {
			if listID == "primary" {
				return "remote-primary", nil
			}
			return "remote-extra", nil
		},
	})

	created, err := provider.CreateTask(context.Background(), domain.Task{
		ProviderID: "clickup-work", ListID: "primary", ListIDs: []domain.ListID{"primary", "extra"}, Title: "Shared",
	})
	if err == nil || created.ID != "" {
		t.Fatalf("CreateTask() = %#v, %v; wanted failed operation rolled back", created, err)
	}
	want := []string{
		"POST /list/remote-primary/task",
		"POST /list/remote-extra/task/remote-task",
		"DELETE /task/remote-task",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("rollback paths = %v, want %v", paths, want)
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

func TestProviderMapsPerListTaskColumnsAndCustomValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/space/remote-space/list":
			_, _ = io.WriteString(w, `{"lists":[{"id":"remote-list","name":"Roadmap"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/space/remote-space/folder":
			_, _ = io.WriteString(w, `{"folders":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/list/remote-list":
			_, _ = io.WriteString(w, `{"id":"remote-list","statuses":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/list/remote-list/field":
			_, _ = io.WriteString(w, `{"fields":[{"id":"field-cost","name":"Cost","type":"currency"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/list/remote-list/task":
			if r.URL.Query().Get("page") == "0" {
				_, _ = io.WriteString(w, `{"tasks":[{"id":"remote-task","name":"Ship feature","list":{"id":"remote-list"},"custom_fields":[{"id":"field-cost","name":"Cost","type":"currency","value":45.5},{"id":"field-owner","name":"Owner","type":"users","value":[{"id":1,"username":"sam"}]}]}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"tasks":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var localListID domain.ListID
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID:          "clickup-work",
		RemoteSpaceResolver: func(domain.SpaceID) (string, error) { return "remote-space", nil },
		RemoteListResolver:  func(domain.ListID) (string, error) { return "remote-list", nil },
		LocalListResolver: func(string) (domain.ListID, error) {
			return localListID, nil
		},
	})
	lists, err := provider.FetchLists(context.Background(), "local-space")
	if err != nil || len(lists) != 1 {
		t.Fatalf("FetchLists() = %#v, %v", lists, err)
	}
	localListID = lists[0].ID
	var columns []domain.TaskColumn
	for _, metadata := range provider.ListTaskColumns(lists[0]) {
		if metadata.Key == domain.MetadataKeyTaskColumns {
			if err := json.Unmarshal([]byte(metadata.Value), &columns); err != nil {
				t.Fatalf("decode mapped columns: %v", err)
			}
		}
	}
	if len(columns) != 1 || columns[0].ID != "custom:field-cost" || columns[0].Name != "Cost" || columns[0].Type != "currency" {
		t.Fatalf("mapped columns = %#v", columns)
	}

	tasks, err := provider.FetchTasks(context.Background(), localListID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("FetchTasks() = %#v, %v", tasks, err)
	}
	var values domain.TaskColumnValues
	for _, metadata := range provider.TaskColumnValues(tasks[0]) {
		if metadata.Key == domain.MetadataKeyTaskColumnValues {
			if err := json.Unmarshal([]byte(metadata.Value), &values); err != nil {
				t.Fatalf("decode mapped custom values: %v", err)
			}
		}
	}
	if values["custom:field-cost"] != "45.5" || values["custom:field-owner"] != "sam" {
		t.Fatalf("mapped custom values = %#v", values)
	}
}

func TestProviderFetchTaskUsesRemoteTaskCallAndLocalListIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/task/remote-task" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"id":"remote-task","name":"Task","list":{"id":"remote-list"},"lists":[{"id":"remote-extra-list"}]}`)
	}))
	defer server.Close()

	provider := New(NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: "token",
	}), ProviderConfig{
		ProviderID:         "clickup-work",
		RemoteTaskResolver: func(domain.TaskID) (string, error) { return "remote-task", nil },
		LocalListResolver: func(remoteID string) (domain.ListID, error) {
			if remoteID == "remote-extra-list" {
				return "local-extra-list", nil
			}
			return "local-list", nil
		},
	})

	task, err := provider.FetchTask(context.Background(), "local-task")
	if err != nil {
		t.Fatalf("FetchTask() error = %v", err)
	}
	if task.ID != "local-task" || task.ListID != "local-list" || task.RemoteID == nil || *task.RemoteID != "remote-task" {
		t.Fatalf("FetchTask() = %#v, want local task/list identities and remote task ID", task)
	}
	if !reflect.DeepEqual(task.ListIDs, []domain.ListID{"local-list", "local-extra-list"}) {
		t.Fatalf("FetchTask() list memberships = %v", task.ListIDs)
	}
}

func TestProviderFetchTaskMapsRemoteNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID:         "clickup-work",
		RemoteTaskResolver: func(domain.TaskID) (string, error) { return "remote-missing", nil },
	})
	if _, err := provider.FetchTask(context.Background(), "local-task"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("FetchTask() error = %v, want domain not found", err)
	}
}

func TestProviderFetchTasksKeepsHomeListAndEveryMembership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/list/remote-request-list/task" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") == "0" {
			if r.URL.Query().Get("include_timl") != "true" {
				t.Errorf("include_timl query = %q", r.URL.Query().Get("include_timl"))
			}
			_, _ = io.WriteString(w, `{"last_page":true,"tasks":[{"id":"remote-task","name":"Shared","list":{"id":"remote-home-list"},"lists":[{"id":"remote-extra-list"}],"locations":[{"id":"remote-location-list"}]}]}`)
			return
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = io.WriteString(w, `{"last_page":true,"tasks":[]}`)
			return
		}
		http.Error(w, "unexpected page", http.StatusBadRequest)
	}))
	defer server.Close()
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID:         "clickup-work",
		RemoteListResolver: func(domain.ListID) (string, error) { return "remote-request-list", nil },
		LocalListResolver: func(remoteID string) (domain.ListID, error) {
			switch remoteID {
			case "remote-home-list":
				return "local-home-list", nil
			case "remote-extra-list":
				return "local-extra-list", nil
			case "remote-location-list":
				return "local-location-list", nil
			default:
				return "local-request-list", nil
			}
		},
	})

	tasks, err := provider.FetchTasks(context.Background(), "local-request-list")
	if err != nil {
		t.Fatalf("FetchTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ListID != "local-home-list" {
		t.Fatalf("FetchTasks() primary list = %#v", tasks)
	}
	want := []domain.ListID{"local-home-list", "local-extra-list", "local-location-list", "local-request-list"}
	if !reflect.DeepEqual(tasks[0].ListIDs, want) {
		t.Fatalf("FetchTasks() memberships = %v, want %v", tasks[0].ListIDs, want)
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
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, `{"id":"remote-task","list":{"id":"old-list"}}`)
				return
			}
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
		case "/list/old-list/task/remote-task":
			w.WriteHeader(http.StatusNoContent)
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
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"id":"remote-task","list":{"id":"remote-list"}}`)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode update payload: %v", err)
		}
		if value, ok := payload["parent"]; !ok || value != nil {
			t.Errorf("parent payload = %#v, want explicit null", payload["parent"])
		}
		_, _ = io.WriteString(w, `{"id":"remote-task","name":"updated","status":{"status":"open"},"list":{"id":"remote-list"}}`)
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

func TestProviderUpdateSynchronizesTaskListMemberships(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/task/remote-task":
			_, _ = io.WriteString(w, `{"id":"remote-task","list":{"id":"remote-home"},"lists":[{"id":"remote-existing"}]}`)
		case r.Method == http.MethodPut && r.URL.Path == "/task/remote-task":
			_, _ = io.WriteString(w, `{"id":"remote-task","name":"updated","list":{"id":"remote-home"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/list/remote-new/task/remote-task":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/list/remote-existing/task/remote-task":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := New(NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"}), ProviderConfig{
		ProviderID: "clickup-work",
		RemoteListResolver: func(listID domain.ListID) (string, error) {
			switch listID {
			case "local-home":
				return "remote-home", nil
			case "local-new":
				return "remote-new", nil
			default:
				return "remote-existing", nil
			}
		},
	})

	_, err := provider.UpdateTask(context.Background(), domain.Task{
		ID: "local-task", ProviderID: "clickup-work", ListID: "local-home",
		ListIDs: []domain.ListID{"local-home", "local-new"}, RemoteID: stringPointer("remote-task"), Title: "updated",
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	want := []string{
		"GET /task/remote-task",
		"PUT /task/remote-task",
		"POST /list/remote-new/task/remote-task",
		"DELETE /list/remote-existing/task/remote-task",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("membership update paths = %v, want %v", paths, want)
	}
}
