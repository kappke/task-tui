package clickup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientFetchesSpacesAndDeduplicatesLists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/team":
			_, _ = io.WriteString(w, `{"teams":[{"id":"team-1","name":"Work"}]}`)
		case "/team/team-1/space":
			_, _ = io.WriteString(w, `{"spaces":[{"id":"space-1","name":"Engineering"}]}`)
		case "/space/space-1/list":
			if r.URL.Query().Get("archived") != "false" {
				t.Errorf("archived query = %q", r.URL.Query().Get("archived"))
			}
			_, _ = io.WriteString(w, `{"lists":[{"id":"list-1","name":"Inbox"},{"id":"duplicate","name":"Folderless"}]}`)
		case "/space/space-1/folder":
			_, _ = io.WriteString(w, `{"folders":[{"id":"folder-1"}]}`)
		case "/folder/folder-1/list":
			_, _ = io.WriteString(w, `{"lists":[{"id":"duplicate","name":"Duplicate"},{"id":"list-2","name":"Backend"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: func(context.Context) (string, error) { return "test-token", nil },
	})

	spaces, err := client.GetSpaces(context.Background())
	if err != nil {
		t.Fatalf("GetSpaces() error = %v", err)
	}
	if len(spaces) != 1 || spaces[0].ID.String() != "space-1" || spaces[0].TeamID != "team-1" {
		t.Fatalf("spaces = %#v", spaces)
	}

	lists, err := client.GetLists(context.Background(), "space-1")
	if err != nil {
		t.Fatalf("GetLists() error = %v", err)
	}
	if len(lists) != 3 {
		t.Fatalf("got %d lists, want 3: %#v", len(lists), lists)
	}
	if lists[1].ID.String() != "duplicate" || lists[2].ID.String() != "list-2" {
		t.Fatalf("lists = %#v", lists)
	}
}

func TestClientConfiguredTeamAvoidsTeamDiscovery(t *testing.T) {
	var teamRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/team" {
			teamRequests.Add(1)
			http.Error(w, "unexpected team discovery", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/team/team-configured/space" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"spaces":[]}`)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: "token",
		TeamID:      "team-configured",
	})
	if _, err := client.GetSpaces(context.Background()); err != nil {
		t.Fatalf("GetSpaces() error = %v", err)
	}
	if got := teamRequests.Load(); got != 0 {
		t.Fatalf("team discovery requests = %d, want 0", got)
	}
}

func TestClientResolvesTokenLazily(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "lazy-token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"id":"task-1"}`)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			calls.Add(1)
			return "lazy-token", nil
		},
	})
	if got := calls.Load(); got != 0 {
		t.Fatalf("token source calls during construction = %d", got)
	}
	if _, err := client.GetTask(context.Background(), "task-1"); err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("token source calls = %d, want 1", got)
	}
}

func TestClientPaginatesIncludingClosedAndMultiListTasks(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/list/list-1/task" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("include_closed") != "true" || r.URL.Query().Get("subtasks") != "true" || r.URL.Query().Get("include_timl") != "true" {
			t.Errorf("query = %v", r.URL.Query())
		}
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "0":
			_, _ = io.WriteString(w, `{"tasks":[{"id":"task-1","name":"Open"}],"last_page":true}`)
		case "1":
			_, _ = io.WriteString(w, `{"tasks":[{"id":"task-2","name":"Shared"}],"last_page":false}`)
		case "2":
			_, _ = io.WriteString(w, `{"tasks":[],"last_page":true}`)
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: "token",
	})
	tasks, err := client.GetTasks(context.Background(), "list-1")
	if err != nil {
		t.Fatalf("GetTasks() error = %v", err)
	}
	if len(tasks) != 2 || len(pages) != 3 || pages[0] != "0" || pages[1] != "1" || pages[2] != "2" {
		t.Fatalf("tasks = %#v, pages = %#v", tasks, pages)
	}
}

func TestClientCRUDUsesAuthorizationAndRemotePaths(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "secret-token" {
			t.Errorf("Authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/list/list-1/task":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode create payload: %v", err)
			}
			if payload["name"] != "created" {
				t.Errorf("create payload = %#v", payload)
			}
			_, _ = io.WriteString(w, `{"id":"task-created","name":"created"}`)
		case r.Method == http.MethodPut && r.URL.Path == "/task/task-1":
			_, _ = io.WriteString(w, `{"id":"task-1","name":"updated"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/task/task-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/list/list-2/task/task-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/list/list-2/task/task-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: func() (string, error) { return "secret-token", nil },
	})

	created, err := client.CreateTask(context.Background(), "list-1", taskCreatePayload{Name: "created"})
	if err != nil || created.ID.String() != "task-created" {
		t.Fatalf("CreateTask() = %#v, %v", created, err)
	}
	updated, err := client.UpdateTask(context.Background(), "task-1", taskUpdatePayload{Name: stringPointer("updated")})
	if err != nil || updated.ID.String() != "task-1" {
		t.Fatalf("UpdateTask() = %#v, %v", updated, err)
	}
	if err := client.DeleteTask(context.Background(), "task-1"); err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if err := client.AddTaskToList(context.Background(), "list-2", "task-1"); err != nil {
		t.Fatalf("AddTaskToList() error = %v", err)
	}
	if err := client.RemoveTaskFromList(context.Background(), "list-2", "task-1"); err != nil {
		t.Fatalf("RemoveTaskFromList() error = %v", err)
	}
	if len(methods) != 5 {
		t.Fatalf("methods = %#v", methods)
	}
}

func TestClientReturnsTypedErrorsAndClosesBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"err":"not authorized: secret-token"}`)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "secret-token"})
	_, err := client.GetTask(context.Background(), "task-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %T %v", err, err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error leaked token: %v", err)
	}

	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{malformed`)
	}))
	defer malformed.Close()
	client = NewClient(ClientConfig{BaseURL: malformed.URL, HTTPClient: malformed.Client(), TokenSource: "token"})
	_, err = client.GetTask(context.Background(), "task-1")
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) {
		t.Fatalf("malformed error = %T %v", err, err)
	}
}

func TestClientErrorsExposeRetryabilityClassification(t *testing.T) {
	for _, test := range []struct {
		status    int
		permanent bool
	}{
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusNotFound, true},
		{http.StatusInternalServerError, false},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"message":"failure"}`)
			}))
			defer server.Close()
			client := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"})
			_, err := client.GetTask(context.Background(), "task")
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Permanent() != test.permanent {
				t.Fatalf("error = %T %v, permanent = %v", err, err, apiErr != nil && apiErr.Permanent())
			}
		})
	}
}

func TestMissingTokenIsPermanent(t *testing.T) {
	client := NewClient(ClientConfig{BaseURL: "https://clickup.test", HTTPClient: &http.Client{}, TokenSource: ""})
	_, err := client.GetTask(context.Background(), "task")
	var requestErr *RequestError
	if !errors.As(err, &requestErr) || !requestErr.Permanent() {
		t.Fatalf("error = %T %v, want permanent request error", err, err)
	}
}

func TestClientRateLimitParsesSecondsAndDate(t *testing.T) {
	for _, test := range []struct {
		name      string
		header    string
		wantAfter time.Duration
		wantDate  bool
	}{
		{name: "seconds", header: "7", wantAfter: 7 * time.Second},
		{name: "date", header: time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat), wantDate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", test.header)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"message":"slow down"}`)
			}))
			defer server.Close()

			client := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"})
			_, err := client.GetTask(context.Background(), "task-1")
			var rateErr *RateLimitError
			if !errors.As(err, &rateErr) || rateErr.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("error = %T %v", err, err)
			}
			if test.wantDate {
				if rateErr.RetryAt.IsZero() || rateErr.RetryAfter <= 0 {
					t.Fatalf("date rate error = %#v", rateErr)
				}
			} else if rateErr.RetryAfter != test.wantAfter {
				t.Fatalf("RetryAfter = %s, want %s", rateErr.RetryAfter, test.wantAfter)
			}
		})
	}
}

func TestClientHonorsTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		TokenSource: "token",
		Timeout:     20 * time.Millisecond,
	})
	_, err := client.GetTask(context.Background(), "task-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %T %v", err, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.GetTask(ctx, "task-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %T %v", err, err)
	}
}

func TestClientDoesNotRetryAPIErrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "failure", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), TokenSource: "token"})
	_, err := client.GetTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("GetTask() error = nil")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestClientBoundsAndClosesResponseBody(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader(`{"id":"this response is too large"}`)}
	client := NewClient(ClientConfig{
		BaseURL: serverURLForRoundTrip,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       body,
			}, nil
		})},
		TokenSource:  "token",
		MaxBodyBytes: 8,
	})

	_, err := client.GetTask(context.Background(), "task-1")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %T %v", err, err)
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}

func TestClientWrapsTransportErrors(t *testing.T) {
	wantErr := errors.New("transport unavailable")
	client := NewClient(ClientConfig{
		BaseURL: "https://clickup.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, wantErr
		})},
		TokenSource: "token",
	})

	_, err := client.GetTask(context.Background(), "task-1")
	var requestErr *RequestError
	if !errors.As(err, &requestErr) || !errors.Is(err, wantErr) {
		t.Fatalf("error = %T %v", err, err)
	}
}

const serverURLForRoundTrip = "https://clickup.test/api/v2"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}
