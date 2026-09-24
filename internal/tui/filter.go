package tui

import (
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
	Expression   *FilterExpression
	Source       string
}

// String returns a deterministic, human-editable representation for the
// filter prompt.
func (f Filter) String() string {
	if f.Source != "" {
		return f.Source
	}
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
