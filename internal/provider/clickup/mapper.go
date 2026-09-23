package clickup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// MapperConfig contains the local identity information needed to turn
// ClickUp objects into domain objects. ClickUp has no knowledge of local IDs,
// so parent resolution is deliberately supplied by the caller.
type MapperConfig struct {
	ProviderID     domain.ProviderID
	ParentResolver any
	ParentIDs      map[string]domain.TaskID
	TeamID         string
}

// Mapper converts private ClickUp transport values into domain values.
type Mapper struct {
	ProviderID     domain.ProviderID
	ParentResolver any
	ParentIDs      map[string]domain.TaskID
	TeamID         string
}

// ParentResolverFunc is the simplest parent resolver form accepted by
// MapperConfig.
type ParentResolverFunc func(domain.ProviderID, string) (domain.TaskID, bool)

func (f ParentResolverFunc) ResolveTaskID(providerID domain.ProviderID, remoteID string) (domain.TaskID, bool) {
	return f(providerID, remoteID)
}

// ContextParentResolverFunc is the context-aware resolver form accepted by
// MapperConfig.
type ContextParentResolverFunc func(context.Context, domain.ProviderID, string) (domain.TaskID, bool)

func (f ContextParentResolverFunc) ResolveTaskID(ctx context.Context, providerID domain.ProviderID, remoteID string) (domain.TaskID, bool) {
	return f(ctx, providerID, remoteID)
}

// NewMapper constructs a mapper without contacting ClickUp. It accepts a
// MapperConfig, provider ID, parent resolver, or parent-ID map.
func NewMapper(values ...any) *Mapper {
	var config MapperConfig
	for _, value := range values {
		switch value := value.(type) {
		case MapperConfig:
			config = value
		case *MapperConfig:
			if value != nil {
				config = *value
			}
		case domain.ProviderID:
			config.ProviderID = value
		case string:
			config.ProviderID = domain.ProviderID(value)
		case map[string]domain.TaskID:
			config.ParentIDs = value
		default:
			config.ParentResolver = value
		}
	}
	return &Mapper{
		ProviderID:     config.ProviderID,
		ParentResolver: config.ParentResolver,
		ParentIDs:      config.ParentIDs,
		TeamID:         config.TeamID,
	}
}

func NewMapperForProvider(providerID domain.ProviderID) *Mapper {
	return NewMapper(MapperConfig{ProviderID: providerID})
}

func (m Mapper) MapSpace(input wireSpace) domain.Space {
	remoteID := input.ID.String()
	return domain.Space{
		ID:         domain.SpaceID(newLocalID("space")),
		ProviderID: m.ProviderID,
		RemoteID:   stringPointer(remoteID),
		Name:       input.Name,
		SyncState:  domain.SyncStateSynced,
	}
}

func (m Mapper) MapList(input wireList, spaceID domain.SpaceID) domain.List {
	remoteID := input.ID.String()
	return domain.List{
		ID:         domain.ListID(newLocalID("list")),
		ProviderID: m.ProviderID,
		SpaceID:    spaceID,
		RemoteID:   stringPointer(remoteID),
		Name:       input.Name,
		SyncState:  domain.SyncStateSynced,
	}
}

// MapTask maps a task and resolves its parent to a local TaskID when a
// resolver or explicit parent-ID map is configured. An unresolved remote
// parent is left unset rather than leaking a provider ID into the domain.
func (m Mapper) MapTask(input wireTask, listID domain.ListID) domain.Task {
	return m.MapTaskContext(context.Background(), input, listID)
}

func (m Mapper) MapTaskContext(ctx context.Context, input wireTask, listID domain.ListID) domain.Task {
	remoteID := input.ID.String()
	output := domain.Task{
		ID:           domain.TaskID(newLocalID("task")),
		ProviderID:   m.ProviderID,
		ListID:       listID,
		ListIDs:      []domain.ListID{listID},
		RemoteID:     stringPointer(remoteID),
		Assignee:     mapAssignee(input.Assignees),
		Title:        input.Name,
		Description:  input.Description,
		Status:       input.Status.Status,
		Priority:     domain.PriorityNone,
		TimeEstimate: parseDurationMillis(input.TimeEstimate),
		TimeTracked:  parseDurationMillis(input.TimeSpent),
		SyncState:    domain.SyncStateSynced,
	}

	if createdAt, ok := parseMillis(input.DateCreated); ok {
		output.CreatedAt = createdAt
	}
	if updatedAt, ok := parseMillis(input.DateUpdated); ok {
		output.UpdatedAt = updatedAt
		output.RemoteUpdatedAt = &updatedAt
	}
	if dueAt, ok := parseOptionalMillis(input.DueDate); ok {
		output.DueAt = &dueAt
	}
	if completedAt := completedTime(input); !completedAt.IsZero() {
		output.CompletedAt = &completedAt
	}
	if input.Priority != nil {
		output.Priority = mapPriority(input.Priority.Priority, input.Priority.OrderIndex.String())
	}

	if input.Parent != nil {
		if parentID, ok := m.resolveParent(ctx, input.Parent.String()); ok {
			output.ParentTaskID = &parentID
		}
	}
	return output
}

func (m Mapper) MapSpaces(inputs []wireSpace) []domain.Space {
	output := make([]domain.Space, 0, len(inputs))
	for _, input := range inputs {
		output = append(output, m.MapSpace(input))
	}
	return output
}

func (m Mapper) MapLists(inputs []wireList, spaceID domain.SpaceID) []domain.List {
	output := make([]domain.List, 0, len(inputs))
	for _, input := range inputs {
		output = append(output, m.MapList(input, spaceID))
	}
	return output
}

func (m Mapper) MapTasks(inputs []wireTask, listID domain.ListID) []domain.Task {
	return m.MapTasksContext(context.Background(), inputs, listID)
}

func (m Mapper) MapTasksContext(ctx context.Context, inputs []wireTask, listID domain.ListID) []domain.Task {
	output := make([]domain.Task, 0, len(inputs))
	parentIDs := make(map[string]domain.TaskID, len(inputs))
	for _, input := range inputs {
		if remoteID := strings.TrimSpace(input.ID.String()); remoteID != "" {
			parentIDs[remoteID] = domain.TaskID(newLocalID("task"))
		}
	}
	mapper := m
	mapper.ParentIDs = make(map[string]domain.TaskID, len(m.ParentIDs)+len(parentIDs))
	for remoteID, localID := range m.ParentIDs {
		mapper.ParentIDs[remoteID] = localID
	}
	for remoteID, localID := range parentIDs {
		mapper.ParentIDs[remoteID] = localID
	}
	for _, input := range inputs {
		task := mapper.MapTaskContext(ctx, input, listID)
		if localID, ok := parentIDs[input.ID.String()]; ok {
			task.ID = localID
		}
		output = append(output, task)
	}
	return output
}

func newLocalID(kind string) string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return fmt.Sprintf("clickup-%s-%s", kind, hex.EncodeToString(bytes[:]))
	}
	// crypto/rand failure is exceptionally unlikely; the remote ID remains
	// isolated in RemoteID and this fallback still avoids using it as a key.
	return fmt.Sprintf("clickup-%s-%d", kind, time.Now().UnixNano())
}

func (m Mapper) resolveParent(ctx context.Context, remoteID string) (domain.TaskID, bool) {
	remoteID = strings.TrimSpace(remoteID)
	if remoteID == "" {
		return "", false
	}
	if parentID, ok := m.ParentIDs[remoteID]; ok && parentID != "" {
		return parentID, true
	}

	switch resolver := m.ParentResolver.(type) {
	case ParentResolverFunc:
		return resolver(m.ProviderID, remoteID)
	case func(domain.ProviderID, string) (domain.TaskID, bool):
		return resolver(m.ProviderID, remoteID)
	case func(string) (domain.TaskID, bool):
		return resolver(remoteID)
	case func(string) domain.TaskID:
		parentID := resolver(remoteID)
		return parentID, parentID != ""
	case func(context.Context, domain.ProviderID, string) (domain.TaskID, error):
		parentID, err := resolver(ctx, m.ProviderID, remoteID)
		return parentID, err == nil && parentID != ""
	case func(domain.ProviderID, string) (domain.TaskID, error):
		parentID, err := resolver(m.ProviderID, remoteID)
		return parentID, err == nil && parentID != ""
	case func(string) (domain.TaskID, error):
		parentID, err := resolver(remoteID)
		return parentID, err == nil && parentID != ""
	case interface {
		ResolveTaskID(context.Context, domain.ProviderID, string) (domain.TaskID, bool)
	}:
		return resolver.ResolveTaskID(ctx, m.ProviderID, remoteID)
	case interface {
		ResolveTaskID(domain.ProviderID, string) (domain.TaskID, bool)
	}:
		return resolver.ResolveTaskID(m.ProviderID, remoteID)
	case interface {
		Resolve(context.Context, domain.ProviderID, string) (domain.TaskID, bool)
	}:
		return resolver.Resolve(ctx, m.ProviderID, remoteID)
	case interface {
		Resolve(domain.ProviderID, string) (domain.TaskID, bool)
	}:
		return resolver.Resolve(m.ProviderID, remoteID)
	case interface {
		ResolveTaskID(context.Context, domain.ProviderID, string) (domain.TaskID, error)
	}:
		parentID, err := resolver.ResolveTaskID(ctx, m.ProviderID, remoteID)
		return parentID, err == nil && parentID != ""
	case interface {
		ResolveTaskID(domain.ProviderID, string) (domain.TaskID, error)
	}:
		parentID, err := resolver.ResolveTaskID(m.ProviderID, remoteID)
		return parentID, err == nil && parentID != ""
	case interface {
		Resolve(context.Context, domain.ProviderID, string) (domain.TaskID, error)
	}:
		parentID, err := resolver.Resolve(ctx, m.ProviderID, remoteID)
		return parentID, err == nil && parentID != ""
	case interface {
		Resolve(domain.ProviderID, string) (domain.TaskID, error)
	}:
		parentID, err := resolver.Resolve(m.ProviderID, remoteID)
		return parentID, err == nil && parentID != ""
	}
	return "", false
}

func completedTime(input wireTask) time.Time {
	if completedAt, ok := parseOptionalMillis(input.DateDone); ok {
		return completedAt
	}
	if closedAt, ok := parseOptionalMillis(input.DateClosed); ok {
		return closedAt
	}
	return time.Time{}
}

func parseOptionalMillis(value *wireString) (time.Time, bool) {
	if value == nil {
		return time.Time{}, false
	}
	return parseMillis(*value)
}

func parseDurationMillis(value *wireString) *time.Duration {
	if value == nil {
		return nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value.String()), 10, 64)
	if err != nil || parsed < 0 || parsed > maxDurationMillis {
		return nil
	}
	duration := time.Duration(parsed) * time.Millisecond
	return &duration
}

const maxDurationMillis = int64(1<<63-1) / int64(time.Millisecond)

func parseMillis(value wireString) (time.Time, bool) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value.String()), 10, 64)
	if err != nil || parsed <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(parsed).UTC(), true
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func mapPriority(value string, orderIndex string) domain.Priority {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "urgent":
		return domain.PriorityUrgent
	case "high":
		return domain.PriorityHigh
	case "normal", "medium":
		return domain.PriorityNormal
	case "low":
		return domain.PriorityLow
	}
	switch strings.TrimSpace(orderIndex) {
	case "1":
		return domain.PriorityUrgent
	case "2":
		return domain.PriorityHigh
	case "3":
		return domain.PriorityNormal
	case "4":
		return domain.PriorityLow
	default:
		return domain.PriorityNone
	}
}

func mapAssignee(values []wireAssignee) string {
	labels := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		label := strings.TrimSpace(value.Username)
		if label == "" {
			label = strings.TrimSpace(value.Name)
		}
		if label == "" {
			label = strings.TrimSpace(value.ID.String())
		}
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}
