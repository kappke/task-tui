package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Filter is a local task filter. Empty fields do not constrain a result.
// Multiple fields are combined with AND semantics.
type Filter struct {
	Query        string
	ProviderID   ProviderID
	SpaceID      SpaceID
	ListID       ListID
	Status       string
	StatusNot    string
	Priority     string
	PriorityNot  string
	PriorityMin  string
	PriorityMax  string
	SyncState    SyncState
	Completed    *bool
	CompletedNot *bool
	DueBefore    *time.Time
	DueAfter     *time.Time
}

// ParseFilter parses simple local expressions such as
// "status:open provider:work" and "priority >= high".
func ParseFilter(input string) (Filter, error) {
	var filter Filter
	tokens := strings.Fields(strings.TrimSpace(input))
	plain := make([]string, 0, len(tokens))

	for i := 0; i < len(tokens); i++ {
		field, operator, value, ok := splitFilterToken(tokens[i])
		if !ok && i+2 < len(tokens) && isFilterField(tokens[i]) && isFilterOperator(tokens[i+1]) {
			field = tokens[i]
			operator = tokens[i+1]
			value = tokens[i+2]
			i += 2
			ok = true
		}
		if !ok {
			if strings.Contains(tokens[i], ":") || strings.ContainsAny(tokens[i], "!<>=") {
				return Filter{}, fmt.Errorf("invalid filter expression %q", tokens[i])
			}
			plain = append(plain, tokens[i])
			continue
		}
		if err := applyFilterTerm(&filter, field, operator, value); err != nil {
			return Filter{}, err
		}
	}

	filter.Query = strings.Join(plain, " ")
	return filter, nil
}

func splitFilterToken(token string) (field, operator, value string, ok bool) {
	for _, candidate := range []string{"!=", ">=", "<=", ">", "<", ":", "="} {
		if index := strings.Index(token, candidate); index > 0 {
			value = token[index+len(candidate):]
			if value == "" {
				return "", "", "", false
			}
			return token[:index], candidate, value, true
		}
	}
	return "", "", "", false
}

func isFilterField(value string) bool {
	switch normalize(value) {
	case "provider", "provider_id", "space", "space_id", "list", "list_id", "status", "priority", "sync", "sync_state", "completed", "complete", "done", "due", "text", "query":
		return true
	default:
		return false
	}
}

func isFilterOperator(value string) bool {
	switch value {
	case ":", "=", "!=", ">=", "<=", ">", "<":
		return true
	default:
		return false
	}
}

func applyFilterTerm(filter *Filter, field, operator, value string) error {
	field = normalize(field)
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("filter value for %s cannot be empty", field)
	}
	if !isFilterField(field) {
		return fmt.Errorf("unknown filter field %q", field)
	}

	switch field {
	case "provider", "provider_id":
		if operator != ":" && operator != "=" {
			return fmt.Errorf("provider only supports ':' or '='")
		}
		filter.ProviderID = ProviderID(value)
	case "space", "space_id":
		if operator != ":" && operator != "=" {
			return fmt.Errorf("space only supports ':' or '='")
		}
		filter.SpaceID = SpaceID(value)
	case "list", "list_id":
		if operator != ":" && operator != "=" {
			return fmt.Errorf("list only supports ':' or '='")
		}
		filter.ListID = ListID(value)
	case "status":
		switch operator {
		case ":", "=":
			filter.Status = value
		case "!=":
			filter.StatusNot = value
		default:
			return fmt.Errorf("status only supports ':', '=', or '!='")
		}
	case "priority":
		switch operator {
		case ":", "=":
			filter.Priority = value
		case "!=":
			filter.PriorityNot = value
		case ">=":
			filter.PriorityMin = value
		case ">":
			filter.PriorityMin = value
		case "<=":
			filter.PriorityMax = value
		case "<":
			filter.PriorityMax = value
		default:
			return fmt.Errorf("unsupported priority operator %q", operator)
		}
	case "sync", "sync_state":
		if operator != ":" && operator != "=" {
			return fmt.Errorf("sync only supports ':' or '='")
		}
		filter.SyncState = SyncState(normalize(value))
	case "completed", "complete", "done":
		if operator != ":" && operator != "=" && operator != "!=" {
			return fmt.Errorf("completed only supports ':', '=', or '!='")
		}
		completed, err := strconv.ParseBool(strings.ToLower(value))
		if err != nil {
			switch normalize(value) {
			case "yes", "done", "complete", "completed":
				completed = true
			case "no", "open", "active":
				completed = false
			default:
				return fmt.Errorf("invalid completed value %q", value)
			}
		}
		if operator == "!=" {
			filter.CompletedNot = &completed
		} else {
			filter.Completed = &completed
		}
	case "due":
		date, err := parseDueDate(value)
		if err != nil {
			return err
		}
		switch operator {
		case "<":
			filter.DueBefore = date
		case "<=", ":", "=":
			end := endOfDay(*date)
			filter.DueBefore = &end
		case ">":
			end := endOfDay(*date)
			filter.DueAfter = &end
		case ">=":
			filter.DueAfter = date
		default:
			return fmt.Errorf("due only supports ':', '=', '<=', or '>='")
		}
	case "text", "query":
		if operator != ":" && operator != "=" {
			return fmt.Errorf("text only supports ':' or '='")
		}
		filter.Query = value
	}
	return nil
}

func parseDueDate(value string) (*time.Time, error) {
	var date time.Time
	var err error
	if normalize(value) == "today" {
		now := time.Now()
		date = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	} else {
		date, err = time.Parse("2006-01-02", value)
		if err != nil {
			return nil, fmt.Errorf("invalid due date %q: use YYYY-MM-DD or today", value)
		}
	}
	return &date, nil
}

func endOfDay(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), value.Location())
}

// String returns a deterministic, human-editable representation for the
// filter prompt.
func (f Filter) String() string {
	parts := make([]string, 0, 8)
	if f.ProviderID != "" {
		parts = append(parts, "provider:"+string(f.ProviderID))
	}
	if f.SpaceID != "" {
		parts = append(parts, "space:"+string(f.SpaceID))
	}
	if f.ListID != "" {
		parts = append(parts, "list:"+string(f.ListID))
	}
	if f.Status != "" {
		parts = append(parts, "status:"+f.Status)
	}
	if f.StatusNot != "" {
		parts = append(parts, "status!="+f.StatusNot)
	}
	if f.Priority != "" {
		parts = append(parts, "priority:"+f.Priority)
	}
	if f.PriorityNot != "" {
		parts = append(parts, "priority!="+f.PriorityNot)
	}
	if f.PriorityMin != "" {
		parts = append(parts, "priority>="+f.PriorityMin)
	}
	if f.PriorityMax != "" {
		parts = append(parts, "priority<="+f.PriorityMax)
	}
	if f.SyncState != "" {
		parts = append(parts, "sync:"+string(f.SyncState))
	}
	if f.Completed != nil {
		parts = append(parts, "completed:"+strconv.FormatBool(*f.Completed))
	}
	if f.CompletedNot != nil {
		parts = append(parts, "completed!="+strconv.FormatBool(*f.CompletedNot))
	}
	if f.DueBefore != nil {
		parts = append(parts, "due<="+f.DueBefore.Format("2006-01-02"))
	}
	if f.DueAfter != nil {
		parts = append(parts, "due>="+f.DueAfter.Format("2006-01-02"))
	}
	if f.Query != "" {
		parts = append(parts, f.Query)
	}
	return strings.Join(parts, " ")
}
