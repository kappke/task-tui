package clickup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL  = "https://api.clickup.com/api/v2"
	defaultTimeout  = 15 * time.Second
	defaultBodySize = 4 << 20
	defaultMaxPages = 1000
)

// HTTPDoer is the part of http.Client used by Client. It keeps transport
// behavior injectable without requiring a custom HTTP implementation.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// ClientConfig configures the ClickUp transport. TokenSource is intentionally
// accepted as a value so callers can provide either a context-aware function,
// a no-argument function, or a TokenSource implementation.
type ClientConfig struct {
	BaseURL        string
	HTTPClient     HTTPDoer
	TokenSource    any
	Timeout        time.Duration
	MaxBodyBytes   int64
	MaxPages       int
	TeamID         string
	ResponseLogger func(method, endpoint string, statusCode int, body []byte)
}

// ClientOptions is a descriptive alias for ClientConfig.
type ClientOptions = ClientConfig

// ClientOption modifies a ClientConfig before construction.
type ClientOption func(*ClientConfig)

func WithBaseURL(value string) ClientOption {
	return func(config *ClientConfig) { config.BaseURL = value }
}

func WithHTTPClient(value HTTPDoer) ClientOption {
	return func(config *ClientConfig) { config.HTTPClient = value }
}

func WithTokenSource(value any) ClientOption {
	return func(config *ClientConfig) { config.TokenSource = value }
}

func WithTimeout(value time.Duration) ClientOption {
	return func(config *ClientConfig) { config.Timeout = value }
}

func WithMaxBodyBytes(value int64) ClientOption {
	return func(config *ClientConfig) { config.MaxBodyBytes = value }
}

func WithTeamID(value string) ClientOption {
	return func(config *ClientConfig) { config.TeamID = value }
}

// Client contains only transport concerns. It does not expose ClickUp wire
// models outside this package and does not authenticate until a request is
// made.
type Client struct {
	baseURL        *url.URL
	baseErr        error
	httpClient     HTTPDoer
	tokenSource    any
	timeout        time.Duration
	maxBodySize    int64
	maxPages       int
	teamID         string
	responseLogger func(method, endpoint string, statusCode int, body []byte)
}

func (c *Client) String() string {
	return "clickup client"
}

// NewClient constructs a ClickUp client without contacting the service. It
// accepts ClientConfig, a compatible ProviderConfig, ClientOption values, or a
// fixed token string.
func NewClient(values ...any) *Client {
	config := ClientConfig{}
	for _, value := range values {
		switch value := value.(type) {
		case ClientConfig:
			config = value
		case *ClientConfig:
			if value != nil {
				config = *value
			}
		case ProviderConfig:
			config = clientConfigFromProvider(value)
		case *ProviderConfig:
			if value != nil {
				config = clientConfigFromProvider(*value)
			}
		case ClientOption:
			if value != nil {
				value(&config)
			}
		case string:
			config.TokenSource = value
		}
	}
	return newClient(config)
}

func clientConfigFromProvider(value ProviderConfig) ClientConfig {
	config := ClientConfig{
		BaseURL:        value.BaseURL,
		HTTPClient:     value.HTTPClient,
		TokenSource:    value.TokenSource,
		Timeout:        value.Timeout,
		MaxBodyBytes:   value.MaxBodyBytes,
		MaxPages:       value.MaxPages,
		TeamID:         value.TeamID,
		ResponseLogger: value.ResponseLogger,
	}
	if value.Client != nil {
		if config.BaseURL == "" && value.Client.baseURL != nil {
			config.BaseURL = value.Client.baseURL.String()
		}
		if config.HTTPClient == nil {
			config.HTTPClient = value.Client.httpClient
		}
		if config.TokenSource == nil {
			config.TokenSource = value.Client.tokenSource
		}
		if config.Timeout <= 0 {
			config.Timeout = value.Client.timeout
		}
		if config.MaxBodyBytes <= 0 {
			config.MaxBodyBytes = value.Client.maxBodySize
		}
		if config.MaxPages <= 0 {
			config.MaxPages = value.Client.maxPages
		}
	}
	return config
}

func newClient(config ClientConfig) *Client {
	if config.BaseURL == "" {
		config.BaseURL = DefaultBaseURL
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultBodySize
	}
	if config.MaxPages <= 0 {
		config.MaxPages = defaultMaxPages
	}
	if config.TokenSource == nil {
		config.TokenSource = TokenSourceFunc(EnvTokenSource)
	}

	baseURL, err := url.Parse(config.BaseURL)
	if err == nil && (baseURL.Scheme == "" || baseURL.Host == "") {
		err = fmt.Errorf("base URL must be absolute")
	}
	if err == nil {
		baseURL.Path = strings.TrimRight(baseURL.Path, "/")
		if baseURL.RawQuery != "" || baseURL.Fragment != "" {
			err = fmt.Errorf("base URL must not contain a query or fragment")
		}
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.Timeout}
	} else if client, ok := httpClient.(*http.Client); ok && client != nil && client.Timeout <= 0 {
		// Clone caller-owned clients rather than mutating their timeout in place.
		clone := *client
		clone.Timeout = config.Timeout
		httpClient = &clone
	}

	return &Client{
		baseURL:        baseURL,
		baseErr:        err,
		httpClient:     httpClient,
		tokenSource:    config.TokenSource,
		timeout:        config.Timeout,
		maxBodySize:    config.MaxBodyBytes,
		maxPages:       config.MaxPages,
		teamID:         strings.TrimSpace(config.TeamID),
		responseLogger: config.ResponseLogger,
	}
}

// NewClientWithOptions is the option-based form of NewClient.
func NewClientWithOptions(options ...ClientOption) *Client {
	config := ClientConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return NewClient(config)
}

// NewClientWithToken creates a client using a fixed token. The token is kept
// in a private source and is never included in request errors.
func NewClientWithToken(token string) *Client {
	return NewClient(ClientConfig{TokenSource: staticTokenSource(token)})
}

// NewClientWithDependencies is useful when the three transport dependencies
// are already available separately.
func NewClientWithDependencies(httpClient HTTPDoer, baseURL string, tokenSource any) *Client {
	return NewClient(ClientConfig{
		BaseURL:     baseURL,
		HTTPClient:  httpClient,
		TokenSource: tokenSource,
	})
}

func (c *Client) GetTeams(ctx context.Context) ([]wireTeam, error) {
	var response teamsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/team", nil, &response); err != nil {
		return nil, err
	}
	for _, team := range response.Teams {
		if team.ID.String() == "" {
			return nil, c.malformedResponse(http.MethodGet, "/team", "team ID is missing")
		}
	}
	return response.Teams, nil
}

func (c *Client) Teams(ctx context.Context) ([]wireTeam, error) {
	return c.GetTeams(ctx)
}

func (c *Client) GetSpaces(ctx context.Context, teamIDs ...string) ([]wireSpace, error) {
	teamID := c.teamID
	if len(teamIDs) > 0 {
		teamID = strings.TrimSpace(teamIDs[0])
	}
	teams, err := c.teamsForScope(ctx, teamID)
	if err != nil {
		return nil, err
	}

	spaces := make([]wireSpace, 0)
	seen := make(map[string]struct{})
	for _, team := range teams {
		teamSpaces, err := c.getSpacesByTeam(ctx, team.ID.String())
		if err != nil {
			return nil, err
		}
		for _, space := range teamSpaces {
			space.TeamID = team.ID.String()
			space.TeamName = team.Name
			key := space.ID.String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			spaces = append(spaces, space)
		}
	}
	return spaces, nil
}

func (c *Client) Spaces(ctx context.Context, teamIDs ...string) ([]wireSpace, error) {
	return c.GetSpaces(ctx, teamIDs...)
}

func (c *Client) GetTeamSpaces(ctx context.Context, teamID string) ([]wireSpace, error) {
	spaces, err := c.getSpacesByTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for i := range spaces {
		spaces[i].TeamID = teamID
	}
	return spaces, nil
}

// GetTeamsForConfiguredScope returns the configured team as a one-item scope,
// or discovers all teams when no team ID was configured.
func (c *Client) GetTeamsForConfiguredScope(ctx context.Context) ([]wireTeam, error) {
	return c.teamsForScope(ctx, c.teamID)
}

func (c *Client) teamsForScope(ctx context.Context, teamID string) ([]wireTeam, error) {
	if teamID != "" {
		return []wireTeam{{ID: wireString(teamID)}}, nil
	}
	return c.GetTeams(ctx)
}

func (c *Client) getSpacesByTeam(ctx context.Context, teamID string) ([]wireSpace, error) {
	var response spacesResponse
	path := "/team/" + url.PathEscape(teamID) + "/space"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	for _, space := range response.Spaces {
		if space.ID.String() == "" {
			return nil, c.malformedResponse(http.MethodGet, path, "space ID is missing")
		}
	}
	return response.Spaces, nil
}

func (c *Client) GetLists(ctx context.Context, spaceID string) ([]wireList, error) {
	folderless, err := c.getListsBySpace(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	folders, err := c.getFoldersBySpace(ctx, spaceID)
	if err != nil {
		return nil, err
	}

	lists := make([]wireList, 0, len(folderless))
	seen := make(map[string]struct{}, len(folderless))
	for _, list := range folderless {
		if list.ID.String() == "" {
			return nil, c.malformedResponse(http.MethodGet, "/space/"+url.PathEscape(spaceID)+"/list?archived=false", "list ID is missing")
		}
		key := list.ID.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		lists = append(lists, list)
	}
	for _, folder := range folders {
		if folder.ID.String() == "" {
			return nil, c.malformedResponse(http.MethodGet, "/space/"+url.PathEscape(spaceID)+"/folder?archived=false", "folder ID is missing")
		}
		folderLists, err := c.getListsByFolder(ctx, folder.ID.String())
		if err != nil {
			return nil, err
		}
		for _, list := range folderLists {
			if list.ID.String() == "" {
				return nil, c.malformedResponse(http.MethodGet, "/folder/"+url.PathEscape(folder.ID.String())+"/list?archived=false", "list ID is missing")
			}
			key := list.ID.String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			lists = append(lists, list)
		}
	}
	return lists, nil
}

func (c *Client) Lists(ctx context.Context, spaceID string) ([]wireList, error) {
	return c.GetLists(ctx, spaceID)
}

func (c *Client) GetFolderlessLists(ctx context.Context, spaceID string) ([]wireList, error) {
	return c.getListsBySpace(ctx, spaceID)
}

func (c *Client) GetFolderLists(ctx context.Context, folderID string) ([]wireList, error) {
	return c.getListsByFolder(ctx, folderID)
}

func (c *Client) getListsBySpace(ctx context.Context, spaceID string) ([]wireList, error) {
	var response listsResponse
	path := "/space/" + url.PathEscape(spaceID) + "/list?archived=false"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	for _, list := range response.Lists {
		if list.ID.String() == "" {
			return nil, c.malformedResponse(http.MethodGet, path, "list ID is missing")
		}
	}
	return response.Lists, nil
}

func (c *Client) getFoldersBySpace(ctx context.Context, spaceID string) ([]wireFolder, error) {
	var response foldersResponse
	path := "/space/" + url.PathEscape(spaceID) + "/folder?archived=false"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	for _, folder := range response.Folders {
		if folder.ID.String() == "" {
			return nil, c.malformedResponse(http.MethodGet, path, "folder ID is missing")
		}
	}
	return response.Folders, nil
}

func (c *Client) getListsByFolder(ctx context.Context, folderID string) ([]wireList, error) {
	var response listsResponse
	path := "/folder/" + url.PathEscape(folderID) + "/list?archived=false"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	for _, list := range response.Lists {
		if list.ID.String() == "" {
			return nil, c.malformedResponse(http.MethodGet, path, "list ID is missing")
		}
	}
	return response.Lists, nil
}

func (c *Client) GetTasks(ctx context.Context, listID string) ([]wireTask, error) {
	allTasks := make([]wireTask, 0)
	for page := 0; ; page++ {
		if page >= c.maxPages {
			return nil, fmt.Errorf("clickup task pagination exceeded safety limit for list %s", listID)
		}

		values := url.Values{}
		values.Set("include_closed", "true")
		values.Set("include_timl", "true")
		values.Set("page", strconv.Itoa(page))
		values.Set("subtasks", "true")

		var response tasksResponse
		path := "/list/" + url.PathEscape(listID) + "/task?" + values.Encode()
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		for _, task := range response.Tasks {
			if task.ID.String() == "" {
				return nil, c.malformedResponse(http.MethodGet, path, "task ID is missing")
			}
		}
		if len(response.Tasks) == 0 {
			// ClickUp can mark a non-empty page as the last page, so an empty
			// response is the reliable end-of-pagination signal.
			return allTasks, nil
		}
		allTasks = append(allTasks, response.Tasks...)
	}
}

func (c *Client) Tasks(ctx context.Context, listID string) ([]wireTask, error) {
	return c.GetTasks(ctx, listID)
}

func (c *Client) GetTask(ctx context.Context, taskID string) (wireTask, error) {
	var response wireTask
	path := "/task/" + url.PathEscape(taskID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return wireTask{}, err
	}
	if response.ID.String() == "" {
		return wireTask{}, c.malformedResponse(http.MethodGet, path, "task ID is missing")
	}
	return response, nil
}

func (c *Client) GetListDetails(ctx context.Context, listID string) (wireListDetails, error) {
	var response wireListDetails
	path := "/list/" + url.PathEscape(listID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return wireListDetails{}, err
	}
	if response.ID.String() == "" {
		return wireListDetails{}, c.malformedResponse(http.MethodGet, path, "list ID is missing")
	}
	return response, nil
}

// GetListFields fetches the custom fields available to tasks in a list.
func (c *Client) GetListFields(ctx context.Context, listID string) ([]wireCustomField, error) {
	var response customFieldsResponse
	path := "/list/" + url.PathEscape(listID) + "/field"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	for _, field := range response.Fields {
		if strings.TrimSpace(field.ID.String()) == "" {
			return nil, c.malformedResponse(http.MethodGet, path, "custom field ID is missing")
		}
	}
	return response.Fields, nil
}

func (c *Client) Task(ctx context.Context, taskID string) (wireTask, error) {
	return c.GetTask(ctx, taskID)
}

func (c *Client) CreateTask(ctx context.Context, listID string, payload any) (wireTask, error) {
	var response wireTask
	path := "/list/" + url.PathEscape(listID) + "/task"
	if err := c.doJSON(ctx, http.MethodPost, path, payload, &response); err != nil {
		return wireTask{}, err
	}
	if response.ID.String() == "" {
		return wireTask{}, c.malformedResponse(http.MethodPost, path, "task ID is missing")
	}
	return response, nil
}

func (c *Client) UpdateTask(ctx context.Context, taskID string, payload any) (wireTask, error) {
	var response wireTask
	path := "/task/" + url.PathEscape(taskID)
	if err := c.doJSON(ctx, http.MethodPut, path, payload, &response); err != nil {
		return wireTask{}, err
	}
	if response.ID.String() == "" {
		return wireTask{}, c.malformedResponse(http.MethodPut, path, "task ID is missing")
	}
	return response, nil
}

// MoveTask changes a task's home List through ClickUp's v3 endpoint. The v2
// Update Task endpoint does not accept a list ID.
func (c *Client) MoveTask(ctx context.Context, taskID string, listID string) error {
	if strings.TrimSpace(c.teamID) == "" {
		return errors.New("ClickUp workspace ID is required to move a task")
	}
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(listID) == "" {
		return errors.New("ClickUp task and list IDs are required to move a task")
	}
	path := "/workspaces/" + url.PathEscape(c.teamID) + "/tasks/" + url.PathEscape(taskID) + "/home_list/" + url.PathEscape(listID)
	return c.doJSONVersion(ctx, http.MethodPut, path, struct{}{}, nil, "v3")
}

// AddTaskToList adds a task to an additional list without changing its home list.
func (c *Client) AddTaskToList(ctx context.Context, listID, taskID string) error {
	return c.updateTaskListMembership(ctx, http.MethodPost, listID, taskID)
}

// RemoveTaskFromList removes a task's membership in one list.
func (c *Client) RemoveTaskFromList(ctx context.Context, listID, taskID string) error {
	return c.updateTaskListMembership(ctx, http.MethodDelete, listID, taskID)
}

func (c *Client) updateTaskListMembership(ctx context.Context, method, listID, taskID string) error {
	if strings.TrimSpace(listID) == "" || strings.TrimSpace(taskID) == "" {
		return errors.New("ClickUp task and list IDs are required to update list membership")
	}
	path := "/list/" + url.PathEscape(listID) + "/task/" + url.PathEscape(taskID)
	return c.doJSON(ctx, method, path, nil, nil)
}

func (c *Client) DeleteTask(ctx context.Context, taskID string) error {
	path := "/task/" + url.PathEscape(taskID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) endpoint(path string) (string, error) {
	if c.baseErr != nil {
		return "", c.baseErr
	}
	if c.baseURL == nil {
		return "", errors.New("clickup base URL is not configured")
	}

	relative, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	if relative.IsAbs() || relative.Host != "" {
		return "", errors.New("ClickUp request path must be relative")
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(relative.Path, "/")
	endpoint.RawPath = strings.TrimRight(c.baseURL.EscapedPath(), "/") + "/" + strings.TrimLeft(relative.EscapedPath(), "/")
	endpoint.RawQuery = relative.RawQuery
	return endpoint.String(), nil
}

func (c *Client) versionedEndpoint(path string, version string) (string, error) {
	if c.baseErr != nil {
		return "", c.baseErr
	}
	if c.baseURL == nil {
		return "", errors.New("clickup base URL is not configured")
	}
	relative, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	if relative.IsAbs() || relative.Host != "" {
		return "", errors.New("ClickUp request path must be relative")
	}
	endpoint := *c.baseURL
	basePath := strings.TrimRight(endpoint.Path, "/")
	basePath = strings.TrimSuffix(basePath, "/v2")
	if basePath == "" {
		basePath = "/api"
	}
	endpoint.Path = basePath + "/" + strings.Trim(version, "/") + "/" + strings.TrimLeft(relative.Path, "/")
	rawBasePath := strings.TrimRight(c.baseURL.EscapedPath(), "/")
	rawBasePath = strings.TrimSuffix(rawBasePath, "/v2")
	if rawBasePath == "" {
		rawBasePath = "/api"
	}
	endpoint.RawPath = rawBasePath + "/" + strings.Trim(version, "/") + "/" + strings.TrimLeft(relative.EscapedPath(), "/")
	endpoint.RawQuery = relative.RawQuery
	return endpoint.String(), nil
}

func (c *Client) token(ctx context.Context) (string, error) {
	switch source := c.tokenSource.(type) {
	case TokenSource:
		return source.Token(ctx)
	case func(context.Context) (string, error):
		return source(ctx)
	case func() (string, error):
		return source()
	case interface{ Token() (string, error) }:
		return source.Token()
	case func(context.Context) string:
		return source(ctx), nil
	case func() string:
		return source(), nil
	case string:
		return source, nil
	case nil:
		return "", ErrMissingTokenSource
	default:
		return "", fmt.Errorf("unsupported ClickUp token source %T", c.tokenSource)
	}
}

func (c *Client) doJSON(ctx context.Context, method string, path string, input any, output any) (resultErr error) {
	return c.doJSONInternal(ctx, method, path, input, output)
}

func (c *Client) doJSONVersion(ctx context.Context, method string, path string, input any, output any, version string) (resultErr error) {
	if ctx == nil {
		return &RequestError{Method: method, URL: path, Err: errors.New("nil context")}
	}
	endpoint, err := c.versionedEndpoint(path, version)
	if err != nil {
		return &RequestError{Method: method, URL: path, Err: err}
	}
	return c.doJSONEndpoint(ctx, method, endpoint, input, output)
}

func (c *Client) doJSONInternal(ctx context.Context, method string, path string, input any, output any) (resultErr error) {
	if ctx == nil {
		return &RequestError{Method: method, URL: path, Err: errors.New("nil context")}
	}

	endpoint, err := c.endpoint(path)
	if err != nil {
		return &RequestError{Method: method, URL: path, Err: err}
	}
	return c.doJSONEndpoint(ctx, method, endpoint, input, output)
}

func (c *Client) doJSONEndpoint(ctx context.Context, method string, endpoint string, input any, output any) (resultErr error) {
	requestContext := ctx
	cancel := func() {}
	if c.timeout > 0 {
		requestContext, cancel = context.WithTimeout(ctx, c.timeout)
	}
	defer cancel()
	if err := requestContext.Err(); err != nil {
		return &RequestError{Method: method, URL: endpoint, Err: err}
	}

	token, err := c.token(requestContext)
	if err != nil {
		return &RequestError{Method: method, URL: endpoint, Err: err}
	}
	if token == "" {
		return &RequestError{Method: method, URL: endpoint, Err: ErrMissingTokenSource}
	}

	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return &RequestError{Method: method, URL: endpoint, Err: err}
		}
		body = bytes.NewReader(payload)
	}

	request, err := http.NewRequestWithContext(requestContext, method, endpoint, body)
	if err != nil {
		return &RequestError{Method: method, URL: endpoint, Err: err}
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return &RequestError{Method: method, URL: endpoint, Err: err}
	}
	if response == nil {
		return &ResponseError{Method: method, URL: endpoint, Err: errors.New("nil HTTP response")}
	}
	if response.Body == nil {
		return &ResponseError{
			Method:     method,
			URL:        endpoint,
			StatusCode: response.StatusCode,
			Err:        errors.New("nil HTTP response body"),
		}
	}
	defer func() {
		closeErr := response.Body.Close()
		if resultErr == nil && closeErr != nil {
			resultErr = &ResponseError{
				Method:     method,
				URL:        endpoint,
				StatusCode: response.StatusCode,
				Err:        closeErr,
			}
		}
	}()

	raw, err := c.readBody(response.Body)
	if err != nil {
		return &ResponseError{
			Method:     method,
			URL:        endpoint,
			StatusCode: response.StatusCode,
			Err:        err,
		}
	}
	if c.responseLogger != nil {
		c.responseLogger(method, endpoint, response.StatusCode, raw)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.responseError(method, endpoint, response, raw, token)
	}
	if output == nil {
		return nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &ResponseError{
			Method:     method,
			URL:        endpoint,
			StatusCode: response.StatusCode,
			Body:       string(raw),
			Err:        fmt.Errorf("%w: empty response body", ErrMalformedResponse),
		}
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return &ResponseError{
			Method:     method,
			URL:        endpoint,
			StatusCode: response.StatusCode,
			Body:       string(raw),
			Err:        err,
		}
	}
	return nil
}

func (c *Client) malformedResponse(method string, path string, detail string) error {
	endpoint, err := c.endpoint(path)
	if err != nil {
		endpoint = path
	}
	return &ResponseError{
		Method: method,
		URL:    endpoint,
		Err:    fmt.Errorf("%w: %s", ErrMalformedResponse, detail),
	}
}

func (c *Client) readBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, c.maxBodySize+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > c.maxBodySize {
		return nil, ErrResponseTooLarge
	}
	return raw, nil
}

func (c *Client) responseError(method string, endpoint string, response *http.Response, raw []byte, token string) error {
	raw = redactToken(raw, token)
	apiErr := &APIError{
		StatusCode: response.StatusCode,
		Status:     response.Status,
		Method:     method,
		URL:        endpoint,
		Message:    responseMessage(raw),
		Body:       string(raw),
	}
	apiErr.Code = responseCode(raw)
	if response.StatusCode != http.StatusTooManyRequests {
		return apiErr
	}

	rateErr := &RateLimitError{APIError: apiErr}
	rateErr.RetryAfter, rateErr.RetryAt = parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	return rateErr
}

func redactToken(raw []byte, token string) []byte {
	if token == "" {
		return raw
	}
	return []byte(strings.ReplaceAll(string(raw), token, "[REDACTED]"))
}

func responseMessage(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Err     string `json:"err"`
		Code    string `json:"ECODE"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		for _, value := range []string{payload.Error, payload.Message, payload.Err, payload.Code} {
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return trimmed
}

func responseCode(raw []byte) string {
	var payload struct {
		Code  string `json:"code"`
		ECode string `json:"ECODE"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if payload.Code != "" {
		return payload.Code
	}
	return payload.ECode
}

func parseRetryAfter(value string, now time.Time) (time.Duration, time.Time) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, time.Time{}
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, time.Time{}
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, time.Time{}
	}
	if when.After(now) {
		return when.Sub(now), when
	}
	return 0, when
}
