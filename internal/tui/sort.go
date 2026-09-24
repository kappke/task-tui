package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SortDirection controls the order of one task-table sort criterion.
type SortDirection string

const (
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

// SortCriterion identifies a task-table column and its direction. The slice
// order is significant: earlier criteria break ties in later criteria.
type SortCriterion struct {
	Column    string
	Direction SortDirection
}

// ParseSort parses comma-separated ordered criteria such as
// "priority desc, due asc". A missing direction defaults to ascending.
func ParseSort(input string) ([]SortCriterion, error) {
	input = strings.TrimSpace(input)
	if input == "" || normalize(input) == "clear" || normalize(input) == "none" || normalize(input) == "off" {
		return nil, nil
	}
	parts := splitSortCriteria(input)
	criteria := make([]SortCriterion, 0, len(parts))
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			return nil, fmt.Errorf("sort criteria cannot be empty")
		}
		direction := SortAscending
		if last := normalize(fields[len(fields)-1]); last == "asc" || last == "ascending" || last == "desc" || last == "descending" {
			fields = fields[:len(fields)-1]
			if last == "desc" || last == "descending" {
				direction = SortDescending
			}
		}
		column := strings.TrimSpace(strings.Join(fields, " "))
		if strings.HasPrefix(column, `"`) && strings.HasSuffix(column, `"`) {
			unquoted, err := strconv.Unquote(column)
			if err != nil {
				return nil, fmt.Errorf("invalid quoted sort column %q: %w", column, err)
			}
			column = unquoted
		}
		if column == "" {
			return nil, fmt.Errorf("sort direction requires a column")
		}
		criteria = append(criteria, SortCriterion{Column: column, Direction: direction})
	}
	return criteria, nil
}

func splitSortCriteria(input string) []string {
	runes := []rune(input)
	parts := make([]string, 0, strings.Count(input, ",")+1)
	start := 0
	quoted := false
	for index, value := range runes {
		if value == '"' && !isEscapedRune(runes, index) {
			quoted = !quoted
			continue
		}
		if value == ',' && !quoted {
			parts = append(parts, string(runes[start:index]))
			start = index + 1
		}
	}
	return append(parts, string(runes[start:]))
}

func sortCriteriaString(criteria []SortCriterion) string {
	parts := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		parts = append(parts, criterion.Column+" "+string(criterion.Direction))
	}
	return strings.Join(parts, ", ")
}

func (m Model) normalizeSortCriteria(criteria []SortCriterion) ([]SortCriterion, error) {
	resolved := make([]SortCriterion, 0, len(criteria))
	seen := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		if criterion.Direction != SortAscending && criterion.Direction != SortDescending {
			return nil, fmt.Errorf("sort direction for %q must be asc or desc", criterion.Column)
		}
		column, ok := m.resolveSortColumn(criterion.Column)
		if !ok {
			return nil, fmt.Errorf("unknown sort column %q", criterion.Column)
		}
		if _, exists := seen[column]; exists {
			return nil, fmt.Errorf("sort column %q appears more than once", criterion.Column)
		}
		seen[column] = struct{}{}
		resolved = append(resolved, SortCriterion{Column: column, Direction: criterion.Direction})
	}
	return resolved, nil
}

func (m Model) resolveSortColumn(input string) (string, bool) {
	column := canonicalTaskColumn(input)
	for _, available := range m.availableTaskColumns() {
		if normalize(available.ID) == column || normalize(available.Label) == column {
			return available.ID, true
		}
		for _, definition := range m.Data.TaskColumns {
			if definition.ID == available.ID && normalize(definition.Name) == column {
				return available.ID, true
			}
		}
	}
	return "", false
}

func (m Model) sortTaskRows(rows []TaskRow) []TaskRow {
	criteria, err := m.normalizeSortCriteria(m.UI.SortBy)
	if err != nil || len(criteria) == 0 || len(rows) < 2 {
		return rows
	}
	sort.SliceStable(rows, func(left, right int) bool {
		for _, criterion := range criteria {
			leftMissing := m.sortColumnMissing(rows[left], criterion.Column)
			rightMissing := m.sortColumnMissing(rows[right], criterion.Column)
			if leftMissing != rightMissing {
				return !leftMissing
			}
			comparison := m.compareTaskColumn(rows[left], rows[right], criterion.Column)
			if comparison == 0 {
				continue
			}
			if criterion.Direction == SortDescending {
				return comparison > 0
			}
			return comparison < 0
		}
		return false
	})
	return rows
}

func (m Model) sortColumnMissing(row TaskRow, column string) bool {
	switch column {
	case taskColumnPriority:
		return false
	case taskColumnEstimate:
		return row.Task.TimeEstimate == nil
	case taskColumnTracked:
		return row.Task.TimeTracked == nil
	case taskColumnDue:
		return row.Task.DueAt == nil
	default:
		_, missing := m.sortColumnString(row, column)
		return missing
	}
}

func (m Model) compareTaskColumn(left, right TaskRow, column string) int {
	if column == taskColumnPriority {
		return compareInts(priorityRank(left.Task.Priority), priorityRank(right.Task.Priority))
	}
	if column == taskColumnEstimate {
		return compareDurations(left.Task.TimeEstimate, right.Task.TimeEstimate)
	}
	if column == taskColumnTracked {
		return compareDurations(left.Task.TimeTracked, right.Task.TimeTracked)
	}
	if column == taskColumnDue {
		return compareTimes(left.Task.DueAt, right.Task.DueAt)
	}
	leftValue, _ := m.sortColumnString(left, column)
	rightValue, _ := m.sortColumnString(right, column)
	columnType := m.sortColumnType(column)
	if columnType == "number" || columnType == "numeric" {
		leftNumber, leftErr := strconv.ParseFloat(strings.TrimSpace(leftValue), 64)
		rightNumber, rightErr := strconv.ParseFloat(strings.TrimSpace(rightValue), 64)
		if leftErr == nil && rightErr == nil {
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
			return 0
		}
	}
	if columnType == "date" || columnType == "datetime" {
		leftDate, leftErr := parseFilterDate(leftValue)
		rightDate, rightErr := parseFilterDate(rightValue)
		if leftErr == nil && rightErr == nil {
			if leftDate.Before(rightDate) {
				return -1
			}
			if leftDate.After(rightDate) {
				return 1
			}
			return 0
		}
	}
	return strings.Compare(normalize(leftValue), normalize(rightValue))
}

func (m Model) sortColumnString(row TaskRow, column string) (string, bool) {
	switch column {
	case taskColumnTask:
		return row.Task.Title, strings.TrimSpace(row.Task.Title) == ""
	case taskColumnStatus:
		return row.Task.Status, strings.TrimSpace(row.Task.Status) == ""
	case taskColumnAssignees:
		value := row.Assignee
		if strings.TrimSpace(value) == "" {
			value = row.Task.Assignee
		}
		return value, strings.TrimSpace(value) == ""
	default:
		value, ok := row.ColumnValues[column]
		return value, !ok || strings.TrimSpace(value) == ""
	}
}

func (m Model) sortColumnType(column string) string {
	for _, definition := range m.Data.TaskColumns {
		if definition.ID == column {
			return normalize(definition.Type)
		}
	}
	return ""
}

func compareDurations(left, right *time.Duration) int {
	if left == nil || right == nil {
		if left == nil && right == nil {
			return 0
		}
		if left == nil {
			return 1
		}
		return -1
	}
	return compareInt64(int64(*left), int64(*right))
}

func compareTimes(left, right *time.Time) int {
	if left == nil || right == nil {
		if left == nil && right == nil {
			return 0
		}
		if left == nil {
			return 1
		}
		return -1
	}
	if left.Before(*right) {
		return -1
	}
	if left.After(*right) {
		return 1
	}
	return 0
}
