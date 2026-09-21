package clickup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kappke/task-tui/internal/domain"
	"github.com/kappke/task-tui/internal/provider"
)

const ProviderType = "clickup"

const (
	Type              = ProviderType
	DefaultProviderID = domain.ProviderID(ProviderType)
)

var (
	ErrProviderMismatch = domain.ErrProviderMismatch
	ErrRemoteIDMissing  = errors.New("clickup remote ID is required")
)

// ProviderConfig binds a ClickUp client to one provider instance. ProviderID
// identifies the account/configuration instance, not merely the ClickUp type.
type ProviderConfig struct {
	ProviderID          domain.ProviderID
	ID                  domain.ProviderID
	Client              *Client
	BaseURL             string
	HTTPClient          HTTPDoer
	TokenSource         any
	Timeout             time.Duration
	MaxBodyBytes        int64
	MaxPages            int
	Mapper              *Mapper
	TeamID              string
	ParentResolver      any
	ParentIDs           map[string]domain.TaskID
	RemoteTaskResolver  any
	RemoteListResolver  any
	LocalListResolver   any
	RemoteSpaceResolver any
}

// Config is the provider-level configuration used during registration.
type Config = ProviderConfig

// ProviderOptions is a descriptive alias for ProviderConfig.
type ProviderOptions = ProviderConfig

// Provider adapts ClickUp to the common provider contract.
type Provider struct {
	client              *Client
	mapper              Mapper
	id                  domain.ProviderID
	remoteTaskResolver  any
	remoteListResolver  any
	localListResolver   any
	remoteSpaceResolver any
}

// New constructs a provider from a client, provider identity, or
// ProviderConfig. It intentionally performs no network request.
func New(values ...any) *Provider {
	var config ProviderConfig
	for _, value := range values {
		switch value := value.(type) {
		case *Client:
			config.Client = value
		case ClientConfig:
			config.Client = NewClient(value)
		case *ClientConfig:
			if value != nil {
				config.Client = NewClient(*value)
			}
		case ProviderConfig:
			client := config.Client
			config = value
			if config.Client == nil {
				config.Client = client
			}
		case *ProviderConfig:
			if value != nil {
				client := config.Client
				config = *value
				if config.Client == nil {
					config.Client = client
				}
			}
		case domain.ProviderID:
			config.ProviderID = value
		case string:
			config.ProviderID = domain.ProviderID(value)
		}
	}
	return newProvider(config)
}

func NewProvider(values ...any) *Provider {
	return New(values...)
}

func NewWithConfig(config ProviderConfig) *Provider {
	return newProvider(config)
}

func NewWithProviderID(providerID domain.ProviderID, client *Client) *Provider {
	return newProvider(ProviderConfig{ProviderID: providerID, Client: client})
}

func NewFromToken(token string, providerID domain.ProviderID) *Provider {
	return New(NewClientWithToken(token), providerID)
}

func newProvider(config ProviderConfig) *Provider {
	if config.ProviderID == "" {
		config.ProviderID = config.ID
	}
	if config.ProviderID == "" {
		config.ProviderID = DefaultProviderID
	}
	if config.Client == nil {
		config.Client = NewClient(ClientConfig{
			BaseURL:      config.BaseURL,
			HTTPClient:   config.HTTPClient,
			TokenSource:  config.TokenSource,
			Timeout:      config.Timeout,
			MaxBodyBytes: config.MaxBodyBytes,
			MaxPages:     config.MaxPages,
			TeamID:       config.TeamID,
		})
	}
	if config.TeamID != "" {
		client := *config.Client
		client.teamID = strings.TrimSpace(config.TeamID)
		config.Client = &client
	}

	mapperConfig := MapperConfig{
		ProviderID:     config.ProviderID,
		ParentResolver: config.ParentResolver,
		ParentIDs:      config.ParentIDs,
		TeamID:         config.TeamID,
	}
	if config.Mapper != nil {
		mapperValue := *config.Mapper
		if config.ProviderID != "" {
			mapperValue.ProviderID = config.ProviderID
		}
		if mapperValue.ParentResolver == nil {
			mapperValue.ParentResolver = config.ParentResolver
		}
		if mapperValue.ParentIDs == nil {
			mapperValue.ParentIDs = config.ParentIDs
		}
		mapperConfig = MapperConfig{
			ProviderID:     mapperValue.ProviderID,
			ParentResolver: mapperValue.ParentResolver,
			ParentIDs:      mapperValue.ParentIDs,
			TeamID:         mapperValue.TeamID,
		}
	}

	return &Provider{
		client:              config.Client,
		mapper:              *NewMapper(mapperConfig),
		id:                  config.ProviderID,
		remoteTaskResolver:  config.RemoteTaskResolver,
		remoteListResolver:  config.RemoteListResolver,
		localListResolver:   config.LocalListResolver,
		remoteSpaceResolver: config.RemoteSpaceResolver,
	}
}

func (p *Provider) ID() domain.ProviderID {
	return p.id
}

func (p *Provider) Type() domain.ProviderType {
	return domain.ProviderTypeClickUp
}

func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		FetchSpaces: true,
		FetchLists:  true,
		FetchTasks:  true,
		FetchTask:   true,
		CreateTask:  true,
		UpdateTask:  true,
		DeleteTask:  true,
		DueDates:    true,
		Subtasks:    true,
	}
}

var _ provider.Provider = (*Provider)(nil)

var _ provider.Authenticator = (*Provider)(nil)

// Authenticate resolves the configured token without performing eager
// authentication during construction. API requests authenticate themselves
// through the same source when they are actually needed.
func (p *Provider) Authenticate(ctx context.Context) error {
	if err := p.ensureReady(); err != nil {
		return err
	}
	token, err := p.client.token(ctx)
	if err != nil {
		return fmt.Errorf("authenticate ClickUp provider: %w", err)
	}
	if token == "" {
		return ErrMissingTokenSource
	}
	return nil
}

func (p *Provider) FetchSpaces(ctx context.Context) ([]domain.Space, error) {
	if err := p.ensureReady(); err != nil {
		return nil, err
	}
	spaces, err := p.client.GetSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch ClickUp spaces: %w", err)
	}
	return p.mapper.MapSpaces(spaces), nil
}

func (p *Provider) FetchLists(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	if err := p.ensureReady(); err != nil {
		return nil, err
	}
	remoteSpaceID, err := p.resolveSpaceRemoteID(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("resolve ClickUp space %s: %w", spaceID, err)
	}
	lists, err := p.client.GetLists(ctx, remoteSpaceID)
	if err != nil {
		return nil, fmt.Errorf("fetch ClickUp lists for space %s: %w", spaceID, err)
	}
	return p.mapper.MapLists(lists, spaceID), nil
}

func (p *Provider) FetchTasks(ctx context.Context, listID domain.ListID) ([]domain.Task, error) {
	if err := p.ensureReady(); err != nil {
		return nil, err
	}
	remoteListID, err := p.resolveListRemoteID(ctx, listID)
	if err != nil {
		return nil, fmt.Errorf("resolve ClickUp list %s: %w", listID, err)
	}
	tasks, err := p.client.GetTasks(ctx, remoteListID)
	if err != nil {
		return nil, fmt.Errorf("fetch ClickUp tasks for list %s: %w", remoteListID, err)
	}
	return p.mapper.MapTasksContext(ctx, tasks, listID), nil
}

func (p *Provider) FetchTask(ctx context.Context, taskID domain.TaskID) (domain.Task, error) {
	if err := p.ensureReady(); err != nil {
		return domain.Task{}, err
	}
	remoteTaskID, err := p.resolveParentRemoteID(ctx, string(taskID))
	if err != nil {
		return domain.Task{}, fmt.Errorf("resolve ClickUp task %s: %w", taskID, err)
	}
	task, err := p.client.GetTask(ctx, remoteTaskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("fetch ClickUp task %s: %w", remoteTaskID, err)
	}
	listID, err := p.resolveListLocalID(ctx, task.List.ID.String())
	if err != nil {
		return domain.Task{}, fmt.Errorf("resolve ClickUp list %s: %w", task.List.ID, err)
	}
	result := p.mapper.MapTaskContext(ctx, task, listID)
	result.ID = taskID
	return result, nil
}

func (p *Provider) resolveListLocalID(ctx context.Context, remoteID string) (domain.ListID, error) {
	remoteID = strings.TrimSpace(remoteID)
	if remoteID == "" {
		return "", errors.New("ClickUp task response has no list ID")
	}
	if p.localListResolver == nil {
		return domain.ListID(remoteID), nil
	}
	var listID domain.ListID
	var err error
	switch resolver := p.localListResolver.(type) {
	case func(context.Context, string) (domain.ListID, error):
		listID, err = resolver(ctx, remoteID)
	case func(string) (domain.ListID, error):
		listID, err = resolver(remoteID)
	case func(context.Context, string) (string, error):
		var value string
		value, err = resolver(ctx, remoteID)
		listID = domain.ListID(value)
	case func(string) (string, error):
		var value string
		value, err = resolver(remoteID)
		listID = domain.ListID(value)
	case func(string) domain.ListID:
		listID = resolver(remoteID)
	case func(string) string:
		listID = domain.ListID(resolver(remoteID))
	case interface {
		ResolveListLocalID(context.Context, string) (domain.ListID, error)
	}:
		listID, err = resolver.ResolveListLocalID(ctx, remoteID)
	case interface {
		ResolveListLocalID(string) (domain.ListID, error)
	}:
		listID, err = resolver.ResolveListLocalID(remoteID)
	default:
		return "", fmt.Errorf("unsupported ClickUp local list resolver %T", p.localListResolver)
	}
	if err != nil {
		return "", err
	}
	if listID = domain.ListID(strings.TrimSpace(string(listID))); listID == "" {
		return "", errors.New("ClickUp local list resolver returned an empty ID")
	}
	return listID, nil
}

func (p *Provider) CreateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := p.validateCreateTaskProvider(task); err != nil {
		return domain.Task{}, err
	}
	if err := p.ensureReady(); err != nil {
		return domain.Task{}, err
	}
	task.ProviderID = p.id

	listID := domainListID(task)
	remoteListID, err := p.resolveListRemoteID(ctx, listID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("resolve ClickUp list %s: %w", listID, err)
	}
	payload, err := p.createPayload(ctx, task)
	if err != nil {
		return domain.Task{}, err
	}
	created, err := p.client.CreateTask(ctx, remoteListID, payload)
	if err != nil {
		return domain.Task{}, fmt.Errorf("create ClickUp task in list %s: %w", remoteListID, err)
	}
	result := p.mapper.MapTaskContext(ctx, created, listID)
	if task.ID != "" {
		result.ID = task.ID
	}
	return result, nil
}

func (p *Provider) UpdateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := p.validateTaskProvider(task); err != nil {
		return domain.Task{}, err
	}
	remoteID := domainRemoteID(task)
	if remoteID == "" {
		return domain.Task{}, ErrRemoteIDMissing
	}
	if err := p.ensureReady(); err != nil {
		return domain.Task{}, err
	}

	payload, err := p.updatePayload(ctx, task)
	if err != nil {
		return domain.Task{}, err
	}
	remoteListID, err := p.resolveListRemoteID(ctx, domainListID(task))
	if err != nil {
		return domain.Task{}, fmt.Errorf("resolve ClickUp list %s: %w", domainListID(task), err)
	}
	updated, err := p.client.UpdateTask(ctx, remoteID, payload)
	if err != nil {
		return domain.Task{}, fmt.Errorf("update ClickUp task %s: %w", remoteID, err)
	}
	if updated.List.ID.String() != "" && updated.List.ID.String() != remoteListID {
		if err := p.client.MoveTask(ctx, remoteID, remoteListID); err != nil {
			return domain.Task{}, fmt.Errorf("move ClickUp task %s to list %s: %w", remoteID, remoteListID, err)
		}
	}
	result := p.mapper.MapTaskContext(ctx, updated, domainListID(task))
	result.ID = task.ID
	return result, nil
}

func (p *Provider) DeleteTask(ctx context.Context, task domain.Task) error {
	if err := p.validateTaskProvider(task); err != nil {
		return err
	}
	remoteID := domainRemoteID(task)
	if remoteID == "" {
		return ErrRemoteIDMissing
	}
	if err := p.ensureReady(); err != nil {
		return err
	}
	if err := p.client.DeleteTask(ctx, remoteID); err != nil {
		return fmt.Errorf("delete ClickUp task %s: %w", remoteID, err)
	}
	return nil
}

// GetSpaces, GetLists, GetTasks, and GetTask are small aliases for callers
// that use the older provider naming while the shared contract uses Fetch*.
func (p *Provider) GetSpaces(ctx context.Context) ([]domain.Space, error) {
	return p.FetchSpaces(ctx)
}

func (p *Provider) GetLists(ctx context.Context, spaceID domain.SpaceID) ([]domain.List, error) {
	return p.FetchLists(ctx, spaceID)
}

func (p *Provider) GetTasks(ctx context.Context, listID domain.ListID) ([]domain.Task, error) {
	return p.FetchTasks(ctx, listID)
}

func (p *Provider) GetTask(ctx context.Context, taskID domain.TaskID) (domain.Task, error) {
	return p.FetchTask(ctx, taskID)
}

func (p *Provider) ensureReady() error {
	if p == nil || p.client == nil {
		return errors.New("clickup provider client is not configured")
	}
	if p.id == "" {
		return errors.New("clickup provider ID is not configured")
	}
	return nil
}

func (p *Provider) validateTaskProvider(task domain.Task) error {
	if p == nil {
		return errors.New("clickup provider is nil")
	}
	providerID := domainTaskProviderID(task)
	if providerID != p.id {
		return fmt.Errorf("%w: provider=%s entity=%s", ErrProviderMismatch, p.id, providerID)
	}
	return nil
}

func (p *Provider) validateCreateTaskProvider(task domain.Task) error {
	if p == nil {
		return errors.New("clickup provider is nil")
	}
	if task.ProviderID.IsZero() {
		return nil
	}
	return p.validateTaskProvider(task)
}

func (p *Provider) createPayload(ctx context.Context, task domain.Task) (taskCreatePayload, error) {
	payload := taskCreatePayload{
		Name:        task.Title,
		Description: task.Description,
		Status:      task.Status,
		DueDate:     timeMillisPointer(task.DueAt),
		Priority:    clickUpPriority(task.Priority),
	}
	if task.ParentTaskID != nil {
		remoteParentID, err := p.resolveParentRemoteID(ctx, string(*task.ParentTaskID))
		if err != nil {
			return taskCreatePayload{}, fmt.Errorf("resolve ClickUp parent task %s: %w", *task.ParentTaskID, err)
		}
		payload.Parent = &remoteParentID
	}
	return payload, nil
}

func (p *Provider) updatePayload(ctx context.Context, task domain.Task) (taskUpdatePayload, error) {
	description := task.Description
	if description == "" {
		// ClickUp requires a single space to clear an existing description.
		description = " "
	}
	payload := taskUpdatePayload{
		Name:        &task.Title,
		Description: &description,
		Status:      &task.Status,
		DueDate:     timeMillisPointer(task.DueAt),
		Priority:    clickUpPriority(task.Priority),
	}
	if task.ParentTaskID != nil {
		remoteParentID, err := p.resolveParentRemoteID(ctx, string(*task.ParentTaskID))
		if err != nil {
			return taskUpdatePayload{}, fmt.Errorf("resolve ClickUp parent task %s: %w", *task.ParentTaskID, err)
		}
		payload.Parent = &remoteParentID
	}
	return payload, nil
}

func (p *Provider) resolveListRemoteID(ctx context.Context, listID domain.ListID) (string, error) {
	if strings.TrimSpace(string(listID)) == "" {
		return "", errors.New("ClickUp list ID is required")
	}
	resolver := p.remoteListResolver
	var remoteID string
	var err error
	switch resolver := resolver.(type) {
	case func(context.Context, domain.ListID) (string, error):
		remoteID, err = resolver(ctx, listID)
	case func(context.Context, string) (string, error):
		remoteID, err = resolver(ctx, string(listID))
	case func(context.Context, domain.ProviderID, domain.ListID) (string, error):
		remoteID, err = resolver(ctx, p.id, listID)
	case func(domain.ListID) (string, error):
		remoteID, err = resolver(listID)
	case func(string) (string, error):
		remoteID, err = resolver(string(listID))
	case func(domain.ProviderID, domain.ListID) (string, error):
		remoteID, err = resolver(p.id, listID)
	case func(domain.ListID) string:
		remoteID = resolver(listID)
	case func(string) string:
		remoteID = resolver(string(listID))
	case interface {
		ResolveListRemoteID(context.Context, domain.ListID) (string, error)
	}:
		remoteID, err = resolver.ResolveListRemoteID(ctx, listID)
	case interface {
		ResolveListRemoteID(domain.ListID) (string, error)
	}:
		remoteID, err = resolver.ResolveListRemoteID(listID)
	case interface {
		ResolveListRemoteID(context.Context, domain.ProviderID, domain.ListID) (string, error)
	}:
		remoteID, err = resolver.ResolveListRemoteID(ctx, p.id, listID)
	case interface {
		ResolveListRemoteID(context.Context, string) (string, error)
	}:
		remoteID, err = resolver.ResolveListRemoteID(ctx, string(listID))
	case interface {
		ResolveListRemoteID(string) (string, error)
	}:
		remoteID, err = resolver.ResolveListRemoteID(string(listID))
	default:
		remoteID = string(listID)
	}
	if err != nil {
		return "", err
	}
	remoteID = strings.TrimSpace(remoteID)
	if strings.TrimSpace(remoteID) == "" {
		return "", errors.New("ClickUp list resolver returned an empty remote ID")
	}
	return remoteID, nil
}

func (p *Provider) resolveSpaceRemoteID(ctx context.Context, spaceID domain.SpaceID) (string, error) {
	if strings.TrimSpace(string(spaceID)) == "" {
		return "", errors.New("ClickUp space ID is required")
	}
	if p.remoteSpaceResolver == nil {
		return string(spaceID), nil
	}
	var remoteID string
	var err error
	switch resolver := p.remoteSpaceResolver.(type) {
	case func(context.Context, domain.ProviderID, domain.SpaceID) (string, error):
		remoteID, err = resolver(ctx, p.id, spaceID)
	case func(domain.ProviderID, domain.SpaceID) (string, error):
		remoteID, err = resolver(p.id, spaceID)
	case func(context.Context, domain.SpaceID) (string, error):
		remoteID, err = resolver(ctx, spaceID)
	case func(context.Context, string) (string, error):
		remoteID, err = resolver(ctx, string(spaceID))
	case func(domain.SpaceID) (string, error):
		remoteID, err = resolver(spaceID)
	case func(string) (string, error):
		remoteID, err = resolver(string(spaceID))
	case func(domain.SpaceID) string:
		remoteID = resolver(spaceID)
	case func(string) string:
		remoteID = resolver(string(spaceID))
	default:
		return "", fmt.Errorf("unsupported ClickUp space resolver %T", p.remoteSpaceResolver)
	}
	if err != nil {
		return "", err
	}
	if remoteID = strings.TrimSpace(remoteID); remoteID == "" {
		return "", errors.New("ClickUp space resolver returned an empty remote ID")
	}
	return remoteID, nil
}

func (p *Provider) resolveParentRemoteID(ctx context.Context, localID string) (string, error) {
	var remoteID string
	var err error
	switch resolver := p.remoteTaskResolver.(type) {
	case func(context.Context, domain.ProviderID, domain.TaskID) (string, error):
		remoteID, err = resolver(ctx, p.id, domain.TaskID(localID))
	case func(context.Context, domain.ProviderID, string) (string, error):
		remoteID, err = resolver(ctx, p.id, localID)
	case func(context.Context, domain.TaskID) (string, error):
		remoteID, err = resolver(ctx, domain.TaskID(localID))
	case func(context.Context, string) (string, error):
		remoteID, err = resolver(ctx, localID)
	case func(domain.TaskID) (string, error):
		remoteID, err = resolver(domain.TaskID(localID))
	case func(string) (string, error):
		remoteID, err = resolver(localID)
	case func(domain.ProviderID, domain.TaskID) (string, error):
		remoteID, err = resolver(p.id, domain.TaskID(localID))
	case func(domain.ProviderID, string) (string, error):
		remoteID, err = resolver(p.id, localID)
	case func(domain.TaskID) string:
		remoteID = resolver(domain.TaskID(localID))
	case func(string) string:
		remoteID = resolver(localID)
	case interface {
		ResolveTaskRemoteID(context.Context, domain.ProviderID, domain.TaskID) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(ctx, p.id, domain.TaskID(localID))
	case interface {
		ResolveTaskRemoteID(context.Context, domain.TaskID) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(ctx, domain.TaskID(localID))
	case interface {
		ResolveTaskRemoteID(domain.TaskID) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(domain.TaskID(localID))
	case interface {
		ResolveTaskRemoteID(context.Context, domain.ProviderID, string) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(ctx, p.id, localID)
	case interface {
		ResolveTaskRemoteID(context.Context, string) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(ctx, localID)
	case interface {
		ResolveTaskRemoteID(domain.ProviderID, domain.TaskID) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(p.id, domain.TaskID(localID))
	case interface {
		ResolveTaskRemoteID(domain.ProviderID, string) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(p.id, localID)
	case interface {
		ResolveTaskRemoteID(string) (string, error)
	}:
		remoteID, err = resolver.ResolveTaskRemoteID(localID)
	default:
		remoteID = localID
	}
	if err != nil {
		return "", err
	}
	remoteID = strings.TrimSpace(remoteID)
	if strings.TrimSpace(remoteID) == "" {
		return "", errors.New("ClickUp task resolver returned an empty remote ID")
	}
	return remoteID, nil
}

func domainTaskProviderID(task domain.Task) domain.ProviderID {
	return task.ProviderID
}

func domainRemoteID(task domain.Task) string {
	if task.RemoteID == nil {
		return ""
	}
	return *task.RemoteID
}

func domainListID(task domain.Task) domain.ListID {
	return task.ListID
}

func timeMillisPointer(value *time.Time) *int64 {
	if value == nil || value.IsZero() {
		return nil
	}
	millis := value.UnixMilli()
	return &millis
}

func clickUpPriority(value domain.Priority) *int {
	var rank int
	switch value {
	case domain.PriorityUrgent:
		rank = 1
	case domain.PriorityHigh:
		rank = 2
	case domain.PriorityNormal:
		rank = 3
	case domain.PriorityLow:
		rank = 4
	default:
		return nil
	}
	return &rank
}
