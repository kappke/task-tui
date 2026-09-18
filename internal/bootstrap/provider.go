package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider is the common adapter boundary used by the sync engine and
// application. Implementations operate only on their own provider ID.
type Provider interface {
	ID() ProviderID
	Type() ProviderType
	Name() string
	Record() ProviderRecord
	Capabilities() Capabilities

	FetchSpaces(context.Context) ([]Space, error)
	FetchLists(context.Context, SpaceID) ([]List, error)
	FetchTasks(context.Context, ListID) ([]Task, error)

	CreateTask(context.Context, Task) (Task, error)
	UpdateTask(context.Context, Task) (Task, error)
	DeleteTask(context.Context, Task) error
}

// Capabilities describes operations supported by one provider instance.
type Capabilities struct {
	CreateTask bool
	UpdateTask bool
	DeleteTask bool
	RemoteSync bool
}

// Registry owns provider instances without relying on package-global state.
type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
}

// NewRegistry constructs an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[ProviderID]Provider)}
}

// Register adds a provider instance. IDs are immutable registry keys.
func (r *Registry) Register(provider Provider) error {
	if r == nil {
		return errors.New("register provider: nil registry")
	}
	if provider == nil {
		return errors.New("register provider: nil provider")
	}
	if strings.TrimSpace(string(provider.ID())) == "" {
		return errors.New("register provider: empty provider ID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[provider.ID()]; exists {
		return fmt.Errorf("register provider %s: already registered", provider.ID())
	}
	r.providers[provider.ID()] = provider
	return nil
}

// Get returns one provider instance.
func (r *Registry) Get(id ProviderID) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[id]
	return provider, ok
}

// All returns a stable provider snapshot for worker construction.
func (r *Registry) All() []Provider {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]Provider, 0, len(r.providers))
	for _, provider := range r.providers {
		providers = append(providers, provider)
	}
	for i := 1; i < len(providers); i++ {
		for j := i; j > 0 && providers[j].ID() < providers[j-1].ID(); j-- {
			providers[j], providers[j-1] = providers[j-1], providers[j]
		}
	}
	return providers
}

// LocalProvider is the first-class offline provider. Local data is already in
// the repository, so fetches are empty and mutations are acknowledged locally.
type LocalProvider struct {
	id   ProviderID
	name string
}

// NewLocalProvider constructs a local provider instance.
func NewLocalProvider(id ProviderID, name string) (*LocalProvider, error) {
	if strings.TrimSpace(string(id)) == "" {
		id = ProviderID("local")
	}
	if strings.TrimSpace(name) == "" {
		name = "Local"
	}
	return &LocalProvider{id: id, name: name}, nil
}

func (p *LocalProvider) ID() ProviderID     { return p.id }
func (p *LocalProvider) Type() ProviderType { return ProviderTypeLocal }
func (p *LocalProvider) Name() string       { return p.name }
func (p *LocalProvider) Capabilities() Capabilities {
	return Capabilities{CreateTask: true, UpdateTask: true, DeleteTask: true}
}
func (p *LocalProvider) Record() ProviderRecord {
	now := time.Now().UTC()
	return ProviderRecord{ID: p.id, Type: p.Type(), Name: p.name, Enabled: true, CreatedAt: now, UpdatedAt: now}
}
func (p *LocalProvider) FetchSpaces(context.Context) ([]Space, error) { return nil, nil }
func (p *LocalProvider) FetchLists(context.Context, SpaceID) ([]List, error) {
	return nil, nil
}
func (p *LocalProvider) FetchTasks(context.Context, ListID) ([]Task, error) { return nil, nil }
func (p *LocalProvider) CreateTask(ctx context.Context, task Task) (Task, error) {
	if err := ctxErr(ctx); err != nil {
		return Task{}, err
	}
	if task.ProviderID != p.id {
		return Task{}, ErrProviderMismatch
	}
	return task, nil
}
func (p *LocalProvider) UpdateTask(ctx context.Context, task Task) (Task, error) {
	return p.CreateTask(ctx, task)
}
func (p *LocalProvider) DeleteTask(ctx context.Context, task Task) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if task.ProviderID != p.id {
		return ErrProviderMismatch
	}
	return nil
}

// TokenSource resolves a credential only when a ClickUp request is made.
type TokenSource interface {
	Token(context.Context) (string, error)
}

// TokenFunc adapts a function to TokenSource for tests and embedding.
type TokenFunc func(context.Context) (string, error)

// Token implements TokenSource.
func (f TokenFunc) Token(ctx context.Context) (string, error) {
	if f == nil {
		return "", ErrUnauthenticated
	}
	return f(ctx)
}

// EnvTokenSource reads a token from an environment variable at request time.
// The value is never included in errors or log attributes.
type EnvTokenSource struct {
	Name string
}

// Token implements TokenSource.
func (s EnvTokenSource) Token(ctx context.Context) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	if strings.TrimSpace(s.Name) == "" {
		return "", ErrUnauthenticated
	}
	// LookupEnv is intentionally done here, rather than in the constructor, so
	// constructing a runtime never authenticates or reads a secret eagerly.
	value, ok := os.LookupEnv(s.Name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", ErrUnauthenticated
	}
	return value, nil
}

// ErrUnauthenticated means a provider cannot make an authenticated request.
var ErrUnauthenticated = errors.New("provider is not authenticated")

// ErrUnsupported means a requested provider operation is unavailable.
var ErrUnsupported = errors.New("provider operation is unsupported")

// ClickUpProvider is a lazy ClickUp adapter. NewClickUpProvider performs only
// local validation; authentication and network I/O happen inside methods.
type ClickUpProvider struct {
	cfg    ClickUpConfig
	token  TokenSource
	client *http.Client
	base   *url.URL
}

// NewClickUpProvider constructs a ClickUp provider without authenticating.
func NewClickUpProvider(cfg ClickUpConfig, token TokenSource, client *http.Client) (*ClickUpProvider, error) {
	if strings.TrimSpace(string(cfg.ID)) == "" {
		cfg.ID = ProviderID("clickup")
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = "ClickUp"
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://api.clickup.com/api/v2"
	}
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/") + "/")
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("create clickup provider: invalid base URL")
	}
	if base.User != nil {
		return nil, fmt.Errorf("create clickup provider: credentials in base URL are not allowed")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if client.Timeout <= 0 {
		return nil, errors.New("create clickup provider: HTTP client timeout must be positive")
	}
	if token == nil {
		token = EnvTokenSource{Name: cfg.TokenEnv}
	}
	return &ClickUpProvider{cfg: cfg, token: token, client: client, base: base}, nil
}

func (p *ClickUpProvider) ID() ProviderID     { return p.cfg.ID }
func (p *ClickUpProvider) Type() ProviderType { return ProviderTypeClickUp }
func (p *ClickUpProvider) Name() string       { return p.cfg.Name }
func (p *ClickUpProvider) Capabilities() Capabilities {
	return Capabilities{CreateTask: true, UpdateTask: true, DeleteTask: true, RemoteSync: true}
}
func (p *ClickUpProvider) Record() ProviderRecord {
	now := time.Now().UTC()
	configuration, _ := json.Marshal(struct {
		BaseURL     string `json:"base_url"`
		WorkspaceID string `json:"workspace_id,omitempty"`
	}{BaseURL: p.cfg.BaseURL, WorkspaceID: p.cfg.WorkspaceID})
	return ProviderRecord{ID: p.ID(), Type: p.Type(), Name: p.Name(), Enabled: p.cfg.Enabled, Configuration: string(configuration), CreatedAt: now, UpdatedAt: now}
}

// Authenticate intentionally exists as an explicit operation; it is never
// called by Build. FetchSpaces is used as the ClickUp account check.
func (p *ClickUpProvider) Authenticate(ctx context.Context) error {
	_, err := p.FetchSpaces(ctx)
	return err
}

func (p *ClickUpProvider) FetchSpaces(ctx context.Context) ([]Space, error) {
	var response clickUpTeamsResponse
	if err := p.getJSON(ctx, "/team", nil, &response); err != nil {
		return nil, fmt.Errorf("clickup fetch spaces: %w", err)
	}
	spaces := make([]Space, 0, len(response.Teams))
	for _, team := range response.Teams {
		if p.cfg.WorkspaceID != "" && team.ID != p.cfg.WorkspaceID {
			continue
		}
		remoteID := team.ID
		spaces = append(spaces, Space{
			ID:         SpaceID(remoteEntityID(p.ID(), "space", remoteID)),
			ProviderID: p.ID(),
			RemoteID:   &remoteID,
			Name:       team.Name,
			SyncState:  SyncStateSynced,
			UpdatedAt:  time.Now().UTC(),
		})
	}
	return spaces, nil
}

func (p *ClickUpProvider) FetchLists(ctx context.Context, spaceID SpaceID) ([]List, error) {
	remoteSpaceID, err := p.remoteEntity(spaceID, "space")
	if err != nil {
		return nil, err
	}
	var folders clickUpFoldersResponse
	if err := p.getJSON(ctx, "/space/"+url.PathEscape(remoteSpaceID)+"/folder", map[string]string{"archived": "false"}, &folders); err != nil {
		return nil, fmt.Errorf("clickup fetch folders: %w", err)
	}
	var direct clickUpListsResponse
	if err := p.getJSON(ctx, "/space/"+url.PathEscape(remoteSpaceID)+"/list", map[string]string{"archived": "false"}, &direct); err != nil {
		return nil, fmt.Errorf("clickup fetch lists: %w", err)
	}
	lists := make([]List, 0, len(folders.Folders)+len(direct.Lists))
	for _, folder := range folders.Folders {
		for _, remoteList := range folder.Lists {
			lists = append(lists, p.mapList(spaceID, remoteList))
		}
	}
	for _, remoteList := range direct.Lists {
		lists = append(lists, p.mapList(spaceID, remoteList))
	}
	return lists, nil
}

func (p *ClickUpProvider) FetchTasks(ctx context.Context, listID ListID) ([]Task, error) {
	remoteListID, err := p.remoteEntity(listID, "list")
	if err != nil {
		return nil, err
	}
	var response clickUpTasksResponse
	if err := p.getJSON(ctx, "/list/"+url.PathEscape(remoteListID)+"/task", map[string]string{"include_closed": "true"}, &response); err != nil {
		return nil, fmt.Errorf("clickup fetch tasks: %w", err)
	}
	tasks := make([]Task, 0, len(response.Tasks))
	for _, remoteTask := range response.Tasks {
		tasks = append(tasks, p.mapTask(listID, remoteTask))
	}
	return tasks, nil
}

func (p *ClickUpProvider) CreateTask(ctx context.Context, task Task) (Task, error) {
	if task.ProviderID != p.ID() {
		return Task{}, ErrProviderMismatch
	}
	remoteListID, err := p.remoteEntity(task.ListID, "list")
	if err != nil {
		return Task{}, err
	}
	body := taskRequestBody(task)
	var response clickUpTask
	if err := p.sendJSON(ctx, http.MethodPost, "/list/"+url.PathEscape(remoteListID)+"/task", nil, body, &response); err != nil {
		return Task{}, fmt.Errorf("clickup create task: %w", err)
	}
	if response.ID == "" {
		return task, nil
	}
	return p.mapTask(task.ListID, response), nil
}

func (p *ClickUpProvider) UpdateTask(ctx context.Context, task Task) (Task, error) {
	if task.ProviderID != p.ID() {
		return Task{}, ErrProviderMismatch
	}
	if task.RemoteID == nil || *task.RemoteID == "" {
		return Task{}, fmt.Errorf("clickup update task: %w", ErrInvalidEntity)
	}
	var response clickUpTask
	if err := p.sendJSON(ctx, http.MethodPut, "/task/"+url.PathEscape(*task.RemoteID), nil, taskRequestBody(task), &response); err != nil {
		return Task{}, fmt.Errorf("clickup update task: %w", err)
	}
	if response.ID == "" {
		return task, nil
	}
	return p.mapTask(task.ListID, response), nil
}

func (p *ClickUpProvider) DeleteTask(ctx context.Context, task Task) error {
	if task.ProviderID != p.ID() {
		return ErrProviderMismatch
	}
	if task.RemoteID == nil || *task.RemoteID == "" {
		return fmt.Errorf("clickup delete task: %w", ErrInvalidEntity)
	}
	if err := p.sendJSON(ctx, http.MethodDelete, "/task/"+url.PathEscape(*task.RemoteID), nil, nil, nil); err != nil {
		return fmt.Errorf("clickup delete task: %w", err)
	}
	return nil
}

func (p *ClickUpProvider) remoteEntity(id interface{}, kind string) (string, error) {
	value := idString(id)
	prefix := string(p.ID()) + "/" + kind + "/"
	if !strings.HasPrefix(value, prefix) {
		return "", fmt.Errorf("clickup %s %s: %w", kind, value, ErrProviderMismatch)
	}
	remoteID := strings.TrimPrefix(value, prefix)
	if remoteID == "" {
		return "", fmt.Errorf("clickup %s: %w", kind, ErrInvalidEntity)
	}
	return remoteID, nil
}

func remoteEntityID(providerID ProviderID, kind, remoteID string) string {
	return string(providerID) + "/" + kind + "/" + remoteID
}

func (p *ClickUpProvider) mapList(spaceID SpaceID, remote clickUpList) List {
	remoteID := remote.ID
	return List{
		ID:         ListID(remoteEntityID(p.ID(), "list", remote.ID)),
		ProviderID: p.ID(),
		SpaceID:    spaceID,
		RemoteID:   &remoteID,
		Name:       remote.Name,
		SyncState:  SyncStateSynced,
		UpdatedAt:  parseMillis(remote.DateUpdated),
	}
}

func (p *ClickUpProvider) mapTask(listID ListID, remote clickUpTask) Task {
	remoteID := remote.ID
	task := Task{
		ID:              TaskID(remoteEntityID(p.ID(), "task", remote.ID)),
		ProviderID:      p.ID(),
		ListID:          listID,
		RemoteID:        &remoteID,
		Assignee:        clickUpAssignee(remote.Assignees),
		Title:           remote.Name,
		Description:     remote.Description,
		Status:          remote.Status.Status,
		Priority:        remote.Priority.Priority,
		TimeEstimate:    parseTaskDuration(remote.TimeEstimate),
		TimeTracked:     parseTaskDuration(remote.TimeSpent),
		DueAt:           parseMillisPtr(remote.DueDate),
		SyncState:       SyncStateSynced,
		RemoteUpdatedAt: parseMillisPtr(remote.DateUpdated),
		UpdatedAt:       time.Now().UTC(),
	}
	if strings.EqualFold(remote.Status.Type, "closed") || strings.EqualFold(remote.Status.Status, "complete") || strings.EqualFold(remote.Status.Status, "done") {
		completed := task.UpdatedAt
		task.CompletedAt = &completed
	}
	return task
}

func taskRequestBody(task Task) map[string]any {
	body := map[string]any{
		"name":        task.Title,
		"description": task.Description,
		"status":      task.Status,
		"priority":    task.Priority,
		"notify_all":  false,
	}
	if task.DueAt != nil {
		body["due_date"] = strconv.FormatInt(task.DueAt.UnixMilli(), 10)
	}
	return body
}

func (p *ClickUpProvider) getJSON(ctx context.Context, path string, query map[string]string, target any) error {
	return p.sendJSON(ctx, http.MethodGet, path, query, nil, target)
}

func (p *ClickUpProvider) sendJSON(ctx context.Context, method, path string, query map[string]string, body any, target any) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	token, err := p.token.Token(ctx)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return ErrUnauthenticated
		}
		return fmt.Errorf("resolve ClickUp credentials: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return ErrUnauthenticated
	}
	endpoint := p.base.ResolveReference(&url.URL{Path: strings.TrimPrefix(path, "/")})
	values := endpoint.Query()
	for key, value := range query {
		values.Set(key, value)
	}
	endpoint.RawQuery = values.Encode()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode ClickUp request: %w", err)
		}
		reader = strings.NewReader(string(payload))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return fmt.Errorf("create ClickUp request: %w", err)
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("ClickUp request failed: %w", sanitizeHTTPError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &APIError{StatusCode: response.StatusCode}
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode ClickUp response: %w", err)
	}
	return nil
}

// APIError exposes only the HTTP status, never an untrusted response body.
type APIError struct {
	StatusCode int
}

func (e *APIError) Error() string {
	if e == nil {
		return "provider request failed"
	}
	return fmt.Sprintf("provider request failed with status %d", e.StatusCode)
}

func sanitizeHTTPError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(SafeErrorText(err))
}

type clickUpTeamsResponse struct {
	Teams []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"teams"`
}

type clickUpFoldersResponse struct {
	Folders []struct {
		Lists []clickUpList `json:"lists"`
	} `json:"folders"`
}

type clickUpListsResponse struct {
	Lists []clickUpList `json:"lists"`
}

type clickUpList struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DateUpdated string `json:"date_updated"`
}

type clickUpTasksResponse struct {
	Tasks []clickUpTask `json:"tasks"`
}

type clickUpTask struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	DateUpdated string `json:"date_updated"`
	DueDate     string `json:"due_date"`
	Status      struct {
		Status string `json:"status"`
		Type   string `json:"type"`
	} `json:"status"`
	Priority struct {
		Priority string `json:"priority"`
	} `json:"priority"`
	TimeEstimate string `json:"time_estimate"`
	TimeSpent    string `json:"time_spent"`
	Assignees    []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"assignees"`
}

func clickUpAssignee(values []struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}) string {
	labels := make([]string, 0, len(values))
	for _, value := range values {
		label := strings.TrimSpace(value.Username)
		if label == "" {
			label = strings.TrimSpace(value.Name)
		}
		if label == "" {
			label = strings.TrimSpace(value.ID)
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, ", ")
}

func parseMillis(value string) time.Time {
	parsed := parseMillisPtr(value)
	if parsed == nil {
		return time.Now().UTC()
	}
	return *parsed
}

func parseMillisPtr(value string) *time.Time {
	if value == "" {
		return nil
	}
	millis, err := strconv.ParseInt(value, 10, 64)
	if err != nil || millis <= 0 {
		return nil
	}
	parsed := time.UnixMilli(millis).UTC()
	return &parsed
}

func parseTaskDuration(value string) *time.Duration {
	if value == "" {
		return nil
	}
	millis, err := strconv.ParseInt(value, 10, 64)
	if err != nil || millis < 0 || millis > maxDurationMillis {
		return nil
	}
	duration := time.Duration(millis) * time.Millisecond
	return &duration
}

const maxDurationMillis = int64(1<<63-1) / int64(time.Millisecond)

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}
