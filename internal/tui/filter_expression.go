package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FilterExpression is an immutable Boolean expression over task columns.
type FilterExpression struct {
	Operator  string
	Condition FilterCondition
	Left      *FilterExpression
	Right     *FilterExpression
}

// FilterCondition compares one task column with a user-supplied value.
type FilterCondition struct {
	Column   string
	Operator string
	Value    string
}

type filterTokenKind uint8

const (
	filterTokenValue filterTokenKind = iota
	filterTokenOperator
	filterTokenLeftParen
	filterTokenRightParen
)

type filterToken struct {
	kind   filterTokenKind
	value  string
	quoted bool
}

type filterParser struct {
	tokens []filterToken
	index  int
}

// ParseFilter parses implicit or explicit AND expressions, OR expressions,
// and NOT expressions. AND binds more tightly than OR, and parentheses can
// make precedence explicit. Column values containing spaces should be quoted.
func ParseFilter(input string) (Filter, error) {
	source := strings.TrimSpace(input)
	if source == "" {
		return Filter{}, nil
	}
	tokens, err := tokenizeFilter(source)
	if err != nil {
		return Filter{}, err
	}
	parser := filterParser{tokens: tokens}
	expression, err := parser.parseOr()
	if err != nil {
		return Filter{}, err
	}
	if parser.index != len(parser.tokens) {
		return Filter{}, fmt.Errorf("unexpected filter token %q", parser.tokens[parser.index].value)
	}
	return Filter{Expression: expression, Source: source}, nil
}

func tokenizeFilter(input string) ([]filterToken, error) {
	runes := []rune(input)
	tokens := make([]filterToken, 0, len(runes)/2)
	for index := 0; index < len(runes); {
		if runes[index] == ' ' || runes[index] == '\t' || runes[index] == '\n' || runes[index] == '\r' {
			index++
			continue
		}
		switch runes[index] {
		case '(':
			tokens = append(tokens, filterToken{kind: filterTokenLeftParen, value: "("})
			index++
			continue
		case ')':
			tokens = append(tokens, filterToken{kind: filterTokenRightParen, value: ")"})
			index++
			continue
		case '"':
			start := index
			index++
			for index < len(runes) {
				if runes[index] == '\\' {
					index += 2
					continue
				}
				if runes[index] == '"' {
					index++
					break
				}
				index++
			}
			if index > len(runes) || runes[index-1] != '"' {
				return nil, fmt.Errorf("unterminated quoted filter value")
			}
			quoted := string(runes[start:index])
			value, err := strconvUnquote(quoted)
			if err != nil {
				return nil, fmt.Errorf("invalid quoted filter value: %w", err)
			}
			tokens = append(tokens, filterToken{kind: filterTokenValue, value: value, quoted: true})
			continue
		}
		if operator, size := filterOperatorAt(runes, index); size > 0 {
			tokens = append(tokens, filterToken{kind: filterTokenOperator, value: operator})
			index += size
			continue
		}
		start := index
		for index < len(runes) && !filterTokenBoundary(runes[index]) {
			index++
		}
		if start == index {
			return nil, fmt.Errorf("unexpected filter character %q", runes[index])
		}
		tokens = append(tokens, filterToken{kind: filterTokenValue, value: string(runes[start:index])})
	}
	return tokens, nil
}

func filterTokenBoundary(value rune) bool {
	if value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '(' || value == ')' || value == '"' {
		return true
	}
	_, size := filterOperatorAt([]rune{value}, 0)
	return size > 0
}

func filterOperatorAt(runes []rune, index int) (string, int) {
	for _, operator := range []string{">=", "<=", "!=", ":", "=", ">", "<", "~", "!"} {
		chars := []rune(operator)
		if index+len(chars) > len(runes) {
			continue
		}
		matched := true
		for offset, char := range chars {
			if runes[index+offset] != char {
				matched = false
				break
			}
		}
		if matched {
			return operator, len(chars)
		}
	}
	return "", 0
}

func strconvUnquote(value string) (string, error) {
	return strconv.Unquote(value)
}

func (p *filterParser) parseOr() (*FilterExpression, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.hasKeyword("or") {
		p.index++
		right, err := p.parseAnd()
		if err != nil {
			return nil, fmt.Errorf("OR requires a condition: %w", err)
		}
		left = &FilterExpression{Operator: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *filterParser) parseAnd() (*FilterExpression, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.index < len(p.tokens) {
		if p.hasKeyword("or") || p.tokens[p.index].kind == filterTokenRightParen {
			break
		}
		if p.hasKeyword("and") {
			p.index++
		} else if !p.startsExpression() {
			break
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, fmt.Errorf("AND requires a condition: %w", err)
		}
		left = &FilterExpression{Operator: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *filterParser) parseUnary() (*FilterExpression, error) {
	if p.hasKeyword("not") || (p.index < len(p.tokens) && p.tokens[p.index].kind == filterTokenOperator && p.tokens[p.index].value == "!") {
		p.index++
		child, err := p.parseUnary()
		if err != nil {
			return nil, fmt.Errorf("NOT requires a condition: %w", err)
		}
		return &FilterExpression{Operator: "not", Left: child}, nil
	}
	if p.index >= len(p.tokens) {
		return nil, fmt.Errorf("expected a filter condition")
	}
	if p.tokens[p.index].kind == filterTokenLeftParen {
		p.index++
		child, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.index >= len(p.tokens) || p.tokens[p.index].kind != filterTokenRightParen {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.index++
		return child, nil
	}
	return p.parseCondition()
}

func (p *filterParser) parseCondition() (*FilterExpression, error) {
	column := p.tokens[p.index]
	if column.kind != filterTokenValue || (!column.quoted && isFilterBoolean(column.value)) {
		return nil, fmt.Errorf("expected a column or text value")
	}
	p.index++
	if p.index < len(p.tokens) && p.tokens[p.index].kind == filterTokenOperator && p.tokens[p.index].value != "!" {
		operator := p.tokens[p.index].value
		p.index++
		if p.index >= len(p.tokens) || p.tokens[p.index].kind != filterTokenValue || (!p.tokens[p.index].quoted && isFilterBoolean(p.tokens[p.index].value)) {
			return nil, fmt.Errorf("%s requires a value", operator)
		}
		value := p.tokens[p.index].value
		p.index++
		return &FilterExpression{Condition: FilterCondition{Column: column.value, Operator: operator, Value: value}}, nil
	}
	return &FilterExpression{Condition: FilterCondition{Column: "text", Operator: "~", Value: column.value}}, nil
}

func (p *filterParser) startsExpression() bool {
	if p.index >= len(p.tokens) {
		return false
	}
	token := p.tokens[p.index]
	if token.kind == filterTokenValue {
		return token.quoted || !isFilterBoolean(token.value)
	}
	return token.kind == filterTokenLeftParen || (token.kind == filterTokenOperator && token.value == "!")
}

func (p *filterParser) hasKeyword(value string) bool {
	return p.index < len(p.tokens) && p.tokens[p.index].kind == filterTokenValue && !p.tokens[p.index].quoted && strings.EqualFold(p.tokens[p.index].value, value)
}

func isFilterBoolean(value string) bool {
	switch strings.ToLower(value) {
	case "and", "or", "not":
		return true
	default:
		return false
	}
}

func filterExpressionRequiresAggregate(expression *FilterExpression) bool {
	if expression == nil {
		return false
	}
	if expression.Operator == "" {
		switch normalize(expression.Condition.Column) {
		case "provider", "provider_id", "space", "space_id", "list", "list_id":
			return true
		}
	}
	return filterExpressionRequiresAggregate(expression.Left) || filterExpressionRequiresAggregate(expression.Right)
}

func (m Model) validateFilterExpression(expression *FilterExpression) error {
	if expression == nil {
		return nil
	}
	if expression.Operator == "" {
		column, ok := m.resolveFilterColumn(expression.Condition.Column)
		if !ok {
			return fmt.Errorf("unknown filter column %q", expression.Condition.Column)
		}
		if !validFilterOperator(column, expression.Condition.Operator) {
			return fmt.Errorf("operator %q is not supported for filter column %q", expression.Condition.Operator, expression.Condition.Column)
		}
	}
	if err := m.validateFilterExpression(expression.Left); err != nil {
		return err
	}
	return m.validateFilterExpression(expression.Right)
}

func validFilterOperator(column, operator string) bool {
	switch column {
	case "@text":
		return operator == ":" || operator == "=" || operator == "!=" || operator == "~"
	case "@provider", "@space", "@list", "@sync":
		return operator == ":" || operator == "=" || operator == "!=" || operator == "~"
	case "@completed":
		return operator == ":" || operator == "=" || operator == "!="
	default:
		return operator == ":" || operator == "=" || operator == "!=" || operator == "~" || operator == ">" || operator == ">=" || operator == "<" || operator == "<="
	}
}

func (m Model) resolveFilterColumn(input string) (string, bool) {
	column := normalize(input)
	switch column {
	case "provider", "provider_id":
		return "@provider", true
	case "space", "space_id":
		return "@space", true
	case "list", "list_id":
		return "@list", true
	case "sync", "sync_state":
		return "@sync", true
	case "completed", "complete", "done":
		return "@completed", true
	case "text", "query":
		return "@text", true
	}
	column = canonicalTaskColumn(column)
	switch column {
	case taskColumnTask, taskColumnStatus, taskColumnAssignees, taskColumnPriority, taskColumnEstimate, taskColumnTracked, taskColumnDue:
		return column, true
	}
	selected, hasSelectedList := m.selectedList()
	for _, definition := range m.Data.TaskColumns {
		if hasSelectedList {
			if definition.ProviderID != selected.ProviderID || definition.ListID != selected.ID {
				continue
			}
		} else {
			if m.UI.ActiveProviderID != "" && definition.ProviderID != m.UI.ActiveProviderID {
				continue
			}
			if m.UI.SelectedNode.Kind == TreeNodeSpace {
				list, ok := m.listByID(definition.ProviderID, definition.ListID)
				if !ok || list.SpaceID != m.UI.SelectedNode.SpaceID {
					continue
				}
			}
		}
		if normalize(definition.ID) == column || normalize(definition.Name) == column {
			return definition.ID, true
		}
	}
	return "", false
}

func canonicalTaskColumn(column string) string {
	switch normalize(column) {
	case "title", "name", "task":
		return taskColumnTask
	case "assignee", "assignees", "owner":
		return taskColumnAssignees
	case "time estimate", "time_estimate", "estimate":
		return taskColumnEstimate
	case "time tracked", "time_tracked", "tracked":
		return taskColumnTracked
	case "due date", "due_date", "due":
		return taskColumnDue
	default:
		return normalize(column)
	}
}

func (m Model) matchesFilterExpression(row TaskRow, expression *FilterExpression) bool {
	if expression == nil {
		return true
	}
	switch expression.Operator {
	case "and":
		return m.matchesFilterExpression(row, expression.Left) && m.matchesFilterExpression(row, expression.Right)
	case "or":
		return m.matchesFilterExpression(row, expression.Left) || m.matchesFilterExpression(row, expression.Right)
	case "not":
		return !m.matchesFilterExpression(row, expression.Left)
	default:
		return m.matchesFilterCondition(row, expression.Condition)
	}
}

func (m Model) matchesFilterCondition(row TaskRow, condition FilterCondition) bool {
	column, ok := m.resolveFilterColumn(condition.Column)
	if !ok {
		return false
	}
	want := normalize(condition.Value)
	operator := condition.Operator
	if operator == ":" {
		operator = "="
	}
	if column == "@text" {
		matched := containsTaskText(row, condition.Value)
		return matched == (operator != "!=")
	}
	if column == "@completed" {
		matched := false
		switch want {
		case "true", "yes", "done", "complete", "completed":
			matched = isTaskComplete(row.Task)
		case "false", "no", "open", "active":
			matched = !isTaskComplete(row.Task)
		default:
			return false
		}
		return matchFilterBoolean(matched, operator, want)
	}
	if column == "@provider" {
		return matchFilterString(string(row.ProviderID), row.ProviderName, operator, want)
	}
	if column == "@space" {
		return matchFilterString(string(row.SpaceID), row.SpaceName, operator, want)
	}
	if column == "@list" {
		matched := taskHasList(row.Task, ListID(condition.Value)) || matchesIdentifier(condition.Value, string(row.ListID), row.ListName)
		for _, name := range row.ListNames {
			matched = matched || matchesIdentifier(condition.Value, name, name)
		}
		if operator == "!=" {
			return !matched
		}
		if operator == "~" {
			return strings.Contains(strings.ToLower(strings.Join(append([]string{row.ListName}, row.ListNames...), " ")), want)
		}
		return matched
	}
	if column == "@sync" {
		return matchFilterString(string(row.Task.SyncState), "", operator, want)
	}

	value, missing := m.filterColumnValue(row, column)
	if operator == "~" {
		return !missing && strings.Contains(normalize(value), want)
	}
	if operator == "=" || operator == "!=" {
		matched := !missing && normalize(value) == want
		if operator == "!=" {
			return !matched
		}
		return matched
	}
	if missing {
		return false
	}
	comparison := m.compareFilterColumnValue(column, value, condition.Value)
	switch operator {
	case ">":
		return comparison > 0
	case ">=":
		return comparison >= 0
	case "<":
		return comparison < 0
	case "<=":
		return comparison <= 0
	default:
		return false
	}
}

func (m Model) filterColumnValue(row TaskRow, column string) (string, bool) {
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
	case taskColumnPriority:
		value := strings.TrimSpace(string(row.Task.Priority))
		if value == "" {
			value = string(PriorityNone)
		}
		return value, false
	case taskColumnEstimate:
		return formatTaskDuration(row.Task.TimeEstimate), row.Task.TimeEstimate == nil
	case taskColumnTracked:
		return formatTaskDuration(row.Task.TimeTracked), row.Task.TimeTracked == nil
	case taskColumnDue:
		return formatTaskDueDate(row.Task.DueAt), row.Task.DueAt == nil
	default:
		value, ok := row.ColumnValues[column]
		return value, !ok || strings.TrimSpace(value) == ""
	}
}

func (m Model) compareFilterColumnValue(column, left, right string) int {
	if column == taskColumnPriority {
		return compareInts(priorityRank(Priority(left)), priorityRank(Priority(right)))
	}
	columnType := m.sortColumnType(column)
	if column == taskColumnDue || columnType == "date" || columnType == "datetime" {
		leftDate, leftErr := parseFilterDate(left)
		rightDate, rightErr := parseFilterDate(right)
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
	if column == taskColumnEstimate || column == taskColumnTracked {
		leftDuration, leftErr := time.ParseDuration(left)
		rightDuration, rightErr := time.ParseDuration(right)
		if leftErr == nil && rightErr == nil {
			return compareInt64(int64(leftDuration), int64(rightDuration))
		}
	}
	if leftNumber, leftErr := strconv.ParseFloat(strings.TrimSpace(left), 64); leftErr == nil {
		if rightNumber, rightErr := strconv.ParseFloat(strings.TrimSpace(right), 64); rightErr == nil {
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
			return 0
		}
	}
	return strings.Compare(normalize(left), normalize(right))
}

func parseFilterDate(value string) (time.Time, error) {
	if normalize(value) == "today" {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), nil
	}
	for _, layout := range []string{time.DateOnly, time.RFC3339, "2006-01-02 15:04"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q", value)
}

func compareInts(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareInt64(left, right int64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func matchFilterString(value, alternate, operator, want string) bool {
	matched := normalize(value) == want || (alternate != "" && normalize(alternate) == want)
	if operator == "~" {
		matched = strings.Contains(normalize(value), want) || strings.Contains(normalize(alternate), want)
	}
	if operator == "!=" {
		return !matched
	}
	return matched
}

func matchFilterBoolean(value bool, operator, want string) bool {
	if operator == "!=" {
		return !value
	}
	return (operator == ":" || operator == "=") && value
}
