package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

const (
	taskDocumentHeader = "---"
	taskDocumentEnd    = "---"
)

// RenderTaskDocument creates the Markdown buffer used by the external task
// editor. The front matter contains task metadata; the body is the description.
func RenderTaskDocument(task Task) string {
	lines := []string{taskDocumentHeader}
	add := func(key, value string) { lines = append(lines, key+": "+value) }
	add("id", quoteDocumentValue(string(task.ID)))
	add("provider_id", quoteDocumentValue(string(task.ProviderID)))
	add("list_id", quoteDocumentValue(string(task.ListID)))
	add("remote_id", optionalString(task.RemoteID))
	add("parent_task_id", optionalTaskID(task.ParentTaskID))
	add("assignee", quoteDocumentValue(task.Assignee))
	add("title", quoteDocumentValue(task.Title))
	add("status", quoteDocumentValue(task.Status))
	add("priority", quoteDocumentValue(string(task.Priority)))
	add("time_estimate", optionalDuration(task.TimeEstimate))
	add("time_tracked", optionalDuration(task.TimeTracked))
	add("due_at", optionalTime(task.DueAt))
	add("completed_at", optionalTime(task.CompletedAt))
	add("sync_state", quoteDocumentValue(string(task.SyncState)))
	add("remote_updated_at", optionalTime(task.RemoteUpdatedAt))
	add("is_deleted", strconv.FormatBool(task.IsDeleted))
	add("deleted_at", optionalTime(task.DeletedAt))
	add("created_at", optionalTimeValue(task.CreatedAt))
	add("updated_at", optionalTimeValue(task.UpdatedAt))
	lines = append(lines, taskDocumentEnd, "", task.Description)
	// Keep a final newline so Neovim's normal fileformat behavior does not turn
	// an unchanged save into a description edit.
	return strings.Join(lines, "\n") + "\n"
}

// ParseTaskDocument parses an edited buffer over the original task. Metadata
// absent from an older or hand-written buffer remains unchanged.
func ParseTaskDocument(data string, original Task) (Task, error) {
	return ParseTaskDocumentWithOptions(data, original, TaskEditorOptions{})
}

// ParseTaskDocumentWithOptions validates provider-backed values when cached
// editor options are available. Empty options deliberately preserve offline
// editing behavior.
func ParseTaskDocumentWithOptions(data string, original Task, options TaskEditorOptions) (Task, error) {
	parts := strings.SplitN(strings.ReplaceAll(data, "\r\n", "\n"), "\n", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) != taskDocumentHeader {
		return Task{}, fmt.Errorf("task document is missing front matter")
	}
	end := strings.Index(parts[1], "\n"+taskDocumentEnd)
	if end < 0 {
		return Task{}, fmt.Errorf("task document front matter is not closed")
	}
	header := parts[1][:end]
	body := parts[1][end+len("\n"+taskDocumentEnd):]
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimSuffix(body, "\n")
	updated := original
	values := make(map[string]string)
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return Task{}, fmt.Errorf("invalid front matter line %q", line)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := parseDocumentString(values, "id", func(value string) { updated.ID = domain.TaskID(value) }); err != nil {
		return Task{}, err
	}
	if err := parseDocumentString(values, "provider_id", func(value string) { updated.ProviderID = domain.ProviderID(value) }); err != nil {
		return Task{}, err
	}
	if err := parseDocumentString(values, "list_id", func(value string) { updated.ListID = domain.ListID(value) }); err != nil {
		return Task{}, err
	}
	for key, set := range map[string]func(string){
		"assignee": func(value string) { updated.Assignee = value },
		"title":    func(value string) { updated.Title = value },
		"status":   func(value string) { updated.Status = value },
	} {
		if err := parseDocumentString(values, key, set); err != nil {
			return Task{}, err
		}
	}
	if value, ok := values["priority"]; ok {
		parsed, err := documentString(value)
		if err != nil {
			return Task{}, fmt.Errorf("priority: %w", err)
		}
		updated.Priority = domain.Priority(parsed)
		if parsed != "" && !containsString([]string{"urgent", "high", "normal", "low"}, parsed) {
			return Task{}, fmt.Errorf("priority: unsupported value %q", parsed)
		}
	}
	if value, ok := values["status"]; ok && len(options.Statuses) > 0 {
		parsed, err := documentString(value)
		if err != nil {
			return Task{}, fmt.Errorf("status: %w", err)
		}
		if !containsString(options.Statuses, parsed) {
			return Task{}, fmt.Errorf("status: unsupported value %q", parsed)
		}
	}
	if value, ok := values["due_at"]; ok {
		parsed, err := optionalDocumentTime(value)
		if err != nil {
			return Task{}, fmt.Errorf("due_at: %w", err)
		}
		updated.DueAt = parsed
	}
	if updated.ID != original.ID || updated.ProviderID != original.ProviderID || updated.ListID != original.ListID {
		return Task{}, fmt.Errorf("task identity and location cannot be changed in the editor")
	}
	updated.Description = body
	return updated, nil
}

func quoteDocumentValue(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func optionalString(value *string) string {
	if value == nil {
		return "null"
	}
	return quoteDocumentValue(*value)
}

func optionalTaskID(value *TaskID) string {
	if value == nil {
		return "null"
	}
	return quoteDocumentValue(string(*value))
}

func optionalDuration(value *time.Duration) string {
	if value == nil {
		return "null"
	}
	return quoteDocumentValue(value.String())
}

func optionalTime(value *time.Time) string {
	if value == nil {
		return "null"
	}
	return quoteDocumentValue(value.UTC().Format(time.RFC3339Nano))
}

func optionalTimeValue(value time.Time) string {
	if value.IsZero() {
		return "null"
	}
	return quoteDocumentValue(value.UTC().Format(time.RFC3339Nano))
}

func parseDocumentString(values map[string]string, key string, set func(string)) error {
	value, ok := values[key]
	if !ok {
		return nil
	}
	parsed, err := documentString(value)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	set(parsed)
	return nil
}

func documentString(value string) (string, error) {
	var parsed string
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return "", fmt.Errorf("expected quoted string: %w", err)
	}
	return parsed, nil
}

func optionalDocumentTime(value string) (*time.Time, error) {
	if value == "null" {
		return nil, nil
	}
	parsed, err := documentString(value)
	if err != nil {
		return nil, err
	}
	due, err := time.Parse(time.RFC3339Nano, parsed)
	if err != nil {
		return nil, err
	}
	due = due.UTC()
	return &due, nil
}

func taskDocumentEqual(left, right Task) bool {
	if left.Title != right.Title || left.Description != right.Description ||
		left.Assignee != right.Assignee || left.Status != right.Status || left.Priority != right.Priority {
		return false
	}
	if left.DueAt == nil || right.DueAt == nil {
		return left.DueAt == nil && right.DueAt == nil
	}
	return left.DueAt.Equal(*right.DueAt)
}
