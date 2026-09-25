package clickup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	ResponseLogger      func(method, endpoint string, statusCode int, body []byte)
	TeamID              string
	ParentResolver      any
	ParentIDs           map[string]domain.TaskID
	RemoteTaskResolver  any
	RemoteListResolver  any
	WorkspaceResolver   any
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
	workspaceResolver   any
	localListResolver   any
	remoteSpaceResolver any
	spaceStatuses       map[string][]wireStatus
	listStatuses        map[string]listStatusMetadata
	columnMetadataMu    sync.RWMutex
	listTaskColumns     map[string]listTaskColumns
	taskColumnValues    map[string]domain.TaskColumnValues
	workspaceMu         sync.RWMutex
	workspaces          []domain.Workspace
}

type listTaskColumns struct {
	columns []domain.TaskColumn
	loaded  bool
}

type listStatusMetadata struct {
	override bool
	statuses []wireStatus
}

type statusMetadataValue struct {
	Statuses         []StatusOption `json:"statuses"`
	OverrideStatuses bool           `json:"override_statuses,omitempty"`
}

// StatusOption is a provider-neutral completion option with optional display
// metadata retained for editor integrations.
type StatusOption struct {
	Name  string `json:"name"`
	Type  string `json:"type,omitempty"`
	Order int    `json:"order"`
	Color string `json:"color,omitempty"`
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
			BaseURL:        config.BaseURL,
			HTTPClient:     config.HTTPClient,
			TokenSource:    config.TokenSource,
			Timeout:        config.Timeout,
			MaxBodyBytes:   config.MaxBodyBytes,
			MaxPages:       config.MaxPages,
			TeamID:         config.TeamID,
			ResponseLogger: config.ResponseLogger,
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
		workspaceResolver:   config.WorkspaceResolver,
		localListResolver:   config.LocalListResolver,
		remoteSpaceResolver: config.RemoteSpaceResolver,
		spaceStatuses:       make(map[string][]wireStatus),
		listStatuses:        make(map[string]listStatusMetadata),
		listTaskColumns:     make(map[string]listTaskColumns),
		taskColumnValues:    make(map[string]domain.TaskColumnValues),
	}
}

var _ provider.StatusMetadataProvider = (*Provider)(nil)
var _ provider.TaskColumnMetadataProvider = (*Provider)(nil)

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
	teams, spaces, err := p.client.GetAllWorkspaceSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch ClickUp spaces: %w", err)
	}
	workspaces := make([]domain.Workspace, 0, len(teams))
	for _, team := range teams {
		remoteID := team.ID.String()
		if remoteID == "" {
			continue
		}
		name := strings.TrimSpace(team.Name)
		if name == "" {
			name = remoteID
		}
		workspaces = append(workspaces, domain.Workspace{
			ID:         domain.WorkspaceID(remoteID),
			ProviderID: p.id,
			RemoteID:   &remoteID,
			Name:       name,
		})
	}
	p.workspaceMu.Lock()
	p.workspaces = workspaces
	p.workspaceMu.Unlock()
	for _, space := range spaces {
		p.spaceStatuses[space.ID.String()] = append([]wireStatus(nil), space.Statuses...)
	}
	return p.mapper.MapSpaces(spaces), nil
}

func (p *Provider) Workspaces() []domain.Workspace {
	if p == nil {
		return nil
	}
	p.workspaceMu.RLock()
	defer p.workspaceMu.RUnlock()
	return append([]domain.Workspace(nil), p.workspaces...)
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
	for _, list := range lists {
		details, err := p.client.GetListDetails(ctx, list.ID.String())
		if err == nil {
			p.listStatuses[list.ID.String()] = listStatusMetadata{
				override: details.OverrideStatuses,
				statuses: append([]wireStatus(nil), details.Statuses...),
			}
		}
		fields, err := p.client.GetListFields(ctx, list.ID.String())
		if err == nil {
			p.rememberListTaskColumns(list.ID.String(), fields)
		}
	}
	return p.mapper.MapLists(lists, spaceID), nil
}

func (p *Provider) SpaceStatusMetadata(space domain.Space) []domain.ProviderMetadata {
	if p == nil || space.RemoteID == nil {
		return nil
	}
	statuses := p.spaceStatuses[*space.RemoteID]
	return []domain.ProviderMetadata{statusMetadata(space.ProviderID, domain.EntityTypeSpace, string(space.ID), statuses, false)}
}

func (p *Provider) ListStatusMetadata(list domain.List) []domain.ProviderMetadata {
	if p == nil || list.RemoteID == nil {
		return nil
	}
	metadata, ok := p.listStatuses[*list.RemoteID]
	if !ok {
		return nil
	}
	return []domain.ProviderMetadata{statusMetadata(list.ProviderID, domain.EntityTypeList, string(list.ID), metadata.statuses, metadata.override)}
}

func statusMetadata(providerID domain.ProviderID, entityType domain.EntityType, entityID string, statuses []wireStatus, override bool) domain.ProviderMetadata {
	options := make([]StatusOption, 0, len(statuses))
	for index, status := range statuses {
		options = append(options, StatusOption{Name: status.Status, Type: status.Type, Order: index, Color: status.Color})
	}
	value, _ := json.Marshal(statusMetadataValue{Statuses: options, OverrideStatuses: override})
	return domain.ProviderMetadata{ProviderID: providerID, EntityType: entityType, EntityID: entityID, Key: "clickup.statuses", Value: string(value)}
}

func (p *Provider) rememberListTaskColumns(remoteListID string, fields []wireCustomField) {
	columns := make([]domain.TaskColumn, 0, len(fields))
	for _, field := range fields {
		id := strings.TrimSpace(field.ID.String())
		if id == "" {
			continue
		}
		name := strings.TrimSpace(field.Name)
		if name == "" {
			name = id
		}
		columns = append(columns, domain.TaskColumn{
			ID:   customFieldColumnID(id),
			Name: name,
			Type: strings.TrimSpace(field.Type),
		})
	}
	p.columnMetadataMu.Lock()
	p.listTaskColumns[remoteListID] = listTaskColumns{columns: columns, loaded: true}
	p.columnMetadataMu.Unlock()
}

// ListTaskColumns returns normalized dynamic task columns for a list.
func (p *Provider) ListTaskColumns(list domain.List) []domain.ProviderMetadata {
	if p == nil || list.ProviderID != p.id || list.RemoteID == nil {
		return nil
	}
	p.columnMetadataMu.RLock()
	metadata, ok := p.listTaskColumns[strings.TrimSpace(*list.RemoteID)]
	p.columnMetadataMu.RUnlock()
	if !ok || !metadata.loaded {
		return nil
	}
	value, err := json.Marshal(metadata.columns)
	if err != nil {
		return nil
	}
	return []domain.ProviderMetadata{{
		ProviderID: list.ProviderID,
		EntityType: domain.EntityTypeList,
		EntityID:   string(list.ID),
		Key:        domain.MetadataKeyTaskColumns,
		Value:      string(value),
	}}
}

// TaskColumnValues returns display-ready dynamic field values for a task.
func (p *Provider) TaskColumnValues(task domain.Task) []domain.ProviderMetadata {
	if p == nil || task.ProviderID != p.id || task.RemoteID == nil {
		return nil
	}
	p.columnMetadataMu.RLock()
	values, ok := p.taskColumnValues[strings.TrimSpace(*task.RemoteID)]
	p.columnMetadataMu.RUnlock()
	if !ok {
		return nil
	}
	value, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	return []domain.ProviderMetadata{{
		ProviderID: task.ProviderID,
		EntityType: domain.EntityTypeTask,
		EntityID:   string(task.ID),
		Key:        domain.MetadataKeyTaskColumnValues,
		Value:      string(value),
	}}
}

func customFieldColumnID(id string) string {
	return "custom:" + id
}

func (p *Provider) rememberTaskColumnValues(task wireTask) {
	remoteID := strings.TrimSpace(task.ID.String())
	if remoteID == "" {
		return
	}
	values := make(domain.TaskColumnValues, len(task.CustomFields))
	for _, field := range task.CustomFields {
		id := strings.TrimSpace(field.ID.String())
		if id == "" {
			continue
		}
		values[customFieldColumnID(id)] = customFieldDisplayValue(field)
	}
	p.columnMetadataMu.Lock()
	p.taskColumnValues[remoteID] = values
	p.columnMetadataMu.Unlock()
}

func customFieldDisplayValue(field wireCustomField) string {
	if len(field.Value) == 0 {
		return ""
	}
	decoder := json.NewDecoder(strings.NewReader(string(field.Value)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return strings.TrimSpace(string(field.Value))
	}
	return formatCustomFieldValue(value, field.Type)
}

func formatCustomFieldValue(value any, fieldType string) string {
	switch value := value.(type) {
	case nil:
		return ""
	case string:
		text := strings.TrimSpace(value)
		if strings.EqualFold(fieldType, "date") {
			if millis, err := strconv.ParseInt(text, 10, 64); err == nil && millis > 0 {
				return time.UnixMilli(millis).UTC().Format(time.DateOnly)
			}
		}
		return text
	case json.Number:
		if strings.EqualFold(fieldType, "date") {
			if millis, err := value.Int64(); err == nil && millis > 0 {
				return time.UnixMilli(millis).UTC().Format(time.DateOnly)
			}
		}
		return value.String()
	case bool:
		return fmt.Sprint(value)
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if text := formatCustomFieldValue(item, ""); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		for _, key := range []string{"display_value", "name", "username", "email", "value"} {
			if nested, ok := value[key]; ok {
				return formatCustomFieldValue(nested, fieldType)
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(encoded)
	default:
		return fmt.Sprint(value)
	}
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
	fieldDefinitions := make([]wireCustomField, 0)
	seenFieldIDs := make(map[string]struct{})
	for _, task := range tasks {
		p.rememberTaskColumnValues(task)
		for _, field := range task.CustomFields {
			id := strings.TrimSpace(field.ID.String())
			if id == "" {
				continue
			}
			if _, exists := seenFieldIDs[id]; exists {
				continue
			}
			seenFieldIDs[id] = struct{}{}
			fieldDefinitions = append(fieldDefinitions, field)
		}
	}
	if len(fieldDefinitions) > 0 {
		p.rememberListTaskColumns(remoteListID, fieldDefinitions)
	}
	mapped := p.mapper.MapTasksContext(ctx, tasks, listID)
	for index := range mapped {
		primaryListID := p.taskPrimaryListID(ctx, tasks[index], listID)
		mapped[index].ListID = primaryListID
		mapped[index].ListIDs = p.resolveTaskListIDs(ctx, tasks[index], primaryListID, listID)
	}
	return mapped, nil
}

func uniqueListIDs(values []domain.ListID) []domain.ListID {
	seen := make(map[domain.ListID]struct{}, len(values))
	result := make([]domain.ListID, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
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
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return domain.Task{}, fmt.Errorf("fetch ClickUp task %s: %w", remoteTaskID, domain.ErrNotFound)
		}
		return domain.Task{}, fmt.Errorf("fetch ClickUp task %s: %w", remoteTaskID, err)
	}
	p.rememberTaskColumnValues(task)
	listID, err := p.resolveListLocalID(ctx, task.List.ID.String())
	if err != nil {
		return domain.Task{}, fmt.Errorf("resolve ClickUp list %s: %w", task.List.ID, err)
	}
	result := p.mapper.MapTaskContext(ctx, task, listID)
	result.ListIDs = p.resolveTaskListIDs(ctx, task, listID)
	result.ID = taskID
	return result, nil
}

func (p *Provider) taskPrimaryListID(ctx context.Context, task wireTask, fallback domain.ListID) domain.ListID {
	remoteID := strings.TrimSpace(task.List.ID.String())
	if remoteID == "" || p.localListResolver == nil {
		return fallback
	}
	primary, err := p.resolveListLocalID(ctx, remoteID)
	if err != nil {
		return fallback
	}
	return primary
}

func (p *Provider) resolveTaskListIDs(ctx context.Context, task wireTask, primary domain.ListID, included ...domain.ListID) []domain.ListID {
	memberships := make([]domain.ListID, 0, len(task.Lists)+len(task.Locations)+1+len(included))
	memberships = append(memberships, primary)
	for _, remoteLists := range [][]wireList{task.Lists, task.Locations} {
		for _, remoteList := range remoteLists {
			resolved, err := p.resolveListLocalID(ctx, remoteList.ID.String())
			if err == nil {
				memberships = append(memberships, resolved)
			}
		}
	}
	memberships = append(memberships, included...)
	return uniqueListIDs(memberships)
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
	task, err := task.NormalizeListMemberships()
	if err != nil {
		return domain.Task{}, fmt.Errorf("create ClickUp task memberships: %w", err)
	}

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
	remoteTaskID := created.ID.String()
	result := p.mapper.MapTaskContext(ctx, created, listID)
	result.ListIDs = task.Memberships()
	if task.ID != "" {
		result.ID = task.ID
	}
	for _, listID := range task.Memberships() {
		if listID == task.ListID {
			continue
		}
		additionalRemoteListID, resolveErr := p.resolveListRemoteID(ctx, listID)
		if resolveErr != nil {
			return p.rollbackCreatedTask(ctx, result, remoteTaskID,
				fmt.Errorf("resolve additional ClickUp list %s: %w", listID, resolveErr))
		}
		if err := p.client.AddTaskToList(ctx, additionalRemoteListID, remoteTaskID); err != nil {
			return p.rollbackCreatedTask(ctx, result, remoteTaskID,
				fmt.Errorf("add ClickUp task %s to list %s: %w", remoteTaskID, additionalRemoteListID, err))
		}
	}
	return result, nil
}

func (p *Provider) rollbackCreatedTask(ctx context.Context, created domain.Task, remoteTaskID string, operationErr error) (domain.Task, error) {
	if err := p.client.DeleteTask(ctx, remoteTaskID); err == nil {
		return domain.Task{}, operationErr
	} else {
		return created, errors.Join(operationErr, fmt.Errorf("delete partially created ClickUp task %s: %w", remoteTaskID, err))
	}
}

func (p *Provider) UpdateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := p.validateTaskProvider(task); err != nil {
		return domain.Task{}, err
	}
	normalized, err := task.NormalizeListMemberships()
	if err != nil {
		return domain.Task{}, fmt.Errorf("update ClickUp task memberships: %w", err)
	}
	task = normalized
	remoteID := domainRemoteID(task)
	if remoteID == "" {
		return domain.Task{}, ErrRemoteIDMissing
	}
	if err := p.ensureReady(); err != nil {
		return domain.Task{}, err
	}
	current, err := p.client.GetTask(ctx, remoteID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("fetch ClickUp task %s before update: %w", remoteID, err)
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
	if current.List.ID.String() != "" && current.List.ID.String() != remoteListID {
		workspaceID, err := p.resolveListWorkspaceID(ctx, domainListID(task))
		if err != nil {
			return domain.Task{}, fmt.Errorf("resolve ClickUp workspace for list %s: %w", domainListID(task), err)
		}
		if err := p.client.MoveTaskInWorkspace(ctx, workspaceID, remoteID, remoteListID); err != nil {
			return domain.Task{}, fmt.Errorf("move ClickUp task %s to list %s: %w", remoteID, remoteListID, err)
		}
	}
	if err := p.syncTaskListMemberships(ctx, remoteID, current, task, remoteListID); err != nil {
		return domain.Task{}, err
	}
	result := p.mapper.MapTaskContext(ctx, updated, domainListID(task))
	result.ListIDs = task.Memberships()
	result.ID = task.ID
	return result, nil
}

func (p *Provider) resolveListWorkspaceID(ctx context.Context, listID domain.ListID) (string, error) {
	resolver := p.workspaceResolver
	if resolver == nil {
		return strings.TrimSpace(p.client.teamID), nil
	}
	var workspaceID string
	var err error
	switch resolver := resolver.(type) {
	case func(context.Context, domain.ProviderID, domain.ListID) (string, error):
		workspaceID, err = resolver(ctx, p.id, listID)
	case func(domain.ProviderID, domain.ListID) (string, error):
		workspaceID, err = resolver(p.id, listID)
	case func(context.Context, domain.ListID) (string, error):
		workspaceID, err = resolver(ctx, listID)
	case func(domain.ListID) (string, error):
		workspaceID, err = resolver(listID)
	case func(context.Context, string) (string, error):
		workspaceID, err = resolver(ctx, string(listID))
	case func(string) (string, error):
		workspaceID, err = resolver(string(listID))
	case func(domain.ListID) string:
		workspaceID = resolver(listID)
	case func(string) string:
		workspaceID = resolver(string(listID))
	default:
		return "", fmt.Errorf("unsupported ClickUp workspace resolver %T", p.workspaceResolver)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(workspaceID), nil
}

func (p *Provider) syncTaskListMemberships(ctx context.Context, remoteTaskID string, current wireTask, task domain.Task, homeRemoteListID string) error {
	currentIDs := make(map[string]struct{}, len(current.Lists)+1)
	if current.List.ID.String() != "" {
		currentIDs[current.List.ID.String()] = struct{}{}
	}
	for _, list := range current.Lists {
		if list.ID.String() != "" {
			currentIDs[list.ID.String()] = struct{}{}
		}
	}
	currentIDs[homeRemoteListID] = struct{}{}

	desiredIDs := make(map[string]struct{}, len(task.Memberships()))
	for _, listID := range task.Memberships() {
		remoteListID, err := p.resolveListRemoteID(ctx, listID)
		if err != nil {
			return fmt.Errorf("resolve ClickUp list %s: %w", listID, err)
		}
		desiredIDs[remoteListID] = struct{}{}
	}
	for listID := range desiredIDs {
		if listID == homeRemoteListID {
			continue
		}
		if _, exists := currentIDs[listID]; exists {
			continue
		}
		if err := p.client.AddTaskToList(ctx, listID, remoteTaskID); err != nil {
			return fmt.Errorf("add ClickUp task %s to list %s: %w", remoteTaskID, listID, err)
		}
	}
	for listID := range currentIDs {
		if listID == homeRemoteListID {
			continue
		}
		if _, exists := desiredIDs[listID]; exists {
			continue
		}
		if err := p.client.RemoveTaskFromList(ctx, listID, remoteTaskID); err != nil {
			return fmt.Errorf("remove ClickUp task %s from list %s: %w", remoteTaskID, listID, err)
		}
	}
	return nil
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
