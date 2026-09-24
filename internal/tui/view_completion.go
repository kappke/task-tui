package tui

import (
	"sort"
	"strconv"
	"strings"
)

type inputCompletionContext struct {
	Start      int
	End        int
	Candidates []string
}

func (m Model) filterCompletionContext() inputCompletionContext {
	cursor := clamp(m.UI.InputCursor, 0, runeCount(m.UI.Input))
	start, end := inputTokenRange(m.UI.Input, cursor, false)
	runes := []rune(m.UI.Input)
	fragment := string(runes[start:cursor])
	before := string(runes[:start])
	if column, _, ok := trailingFilterCondition(before); ok {
		return inputCompletionContext{
			Start:      start,
			End:        end,
			Candidates: matchInputCompletions(m.filterValueCompletions(column), fragment),
		}
	}

	previous := strings.TrimSpace(before)
	previousWord := lastInputWord(previous, false)
	if fragment == "" && previousWord != "" && !hasFilterOperator(previous) && m.isFilterColumn(previousWord) {
		return inputCompletionContext{Start: cursor, End: cursor, Candidates: m.filterOperatorCompletions(previousWord)}
	}
	if fragment != "" && !hasFilterOperator(before) && m.isFilterColumn(fragment) && expectsFilterColumn(before) {
		return inputCompletionContext{Start: cursor, End: cursor, Candidates: m.filterOperatorCompletions(fragment)}
	}

	if isFilterBooleanPrefix(fragment) && hasFilterOperator(before) && !endsWithBooleanWord(before) {
		return inputCompletionContext{
			Start:      start,
			End:        end,
			Candidates: matchInputCompletions(filterBooleanCompletions(before), fragment),
		}
	}

	if endsWithBooleanWord(before) || strings.HasSuffix(strings.TrimSpace(before), "(") {
		return inputCompletionContext{
			Start:      start,
			End:        end,
			Candidates: matchInputCompletions(m.filterColumnCompletions(), fragment),
		}
	}
	if fragment == "" && hasFilterOperator(before) {
		return inputCompletionContext{
			Start:      cursor,
			End:        cursor,
			Candidates: filterBooleanCompletions(before),
		}
	}
	if hasFilterOperator(before) && !isFilterBooleanPrefix(fragment) {
		return inputCompletionContext{
			Start:      start,
			End:        end,
			Candidates: matchInputCompletions(m.filterColumnCompletions(), fragment),
		}
	}

	candidates := m.filterColumnCompletions()
	if previous == "" && (fragment == "" || isFilterBooleanPrefix(fragment)) {
		candidates = append(candidates, "NOT")
	}
	return inputCompletionContext{Start: start, End: end, Candidates: matchInputCompletions(candidates, fragment)}
}

func (m Model) sortCompletionContext() inputCompletionContext {
	cursor := clamp(m.UI.InputCursor, 0, runeCount(m.UI.Input))
	runes := []rune(m.UI.Input)
	segmentStart := lastSortSeparator(runes, cursor) + 1
	for segmentStart < cursor && isInputSpace(runes[segmentStart]) {
		segmentStart++
	}
	segment := string(runes[segmentStart:cursor])
	localCursor := runeCount(segment)
	wordStart, wordEnd := inputTokenRange(segment, localCursor, true)
	fragment := string([]rune(segment)[wordStart:localCursor])
	columnBeforeDirection := strings.TrimSpace(segment[:wordStart])

	if columnBeforeDirection != "" && m.isSortColumn(unquoteCompletionValue(columnBeforeDirection)) {
		if wordStart == localCursor && strings.TrimSpace(segment) == columnBeforeDirection {
			return inputCompletionContext{
				Start:      cursor,
				End:        cursor,
				Candidates: []string{"asc", "desc"},
			}
		}
		return inputCompletionContext{
			Start:      segmentStart + wordStart,
			End:        segmentStart + wordEnd,
			Candidates: matchingCompletions([]string{"asc", "desc"}, fragment),
		}
	}

	columnText := strings.TrimSpace(segment)
	return inputCompletionContext{
		Start:      segmentStart,
		End:        cursor,
		Candidates: matchInputCompletions(m.sortColumnCompletions(), columnText),
	}
}

func inputTokenRange(input string, cursor int, sortInput bool) (int, int) {
	runes := []rune(input)
	cursor = clamp(cursor, 0, len(runes))
	for index := 0; index < len(runes); {
		if inputCompletionDelimiter(runes[index], sortInput) {
			index++
			continue
		}
		start := index
		quoted := false
		for index < len(runes) {
			if runes[index] == '"' && !isEscapedRune(runes, index) {
				quoted = !quoted
				index++
				continue
			}
			if !quoted && inputCompletionDelimiter(runes[index], sortInput) {
				break
			}
			index++
		}
		if cursor >= start && cursor <= index {
			return start, index
		}
	}
	return cursor, cursor
}

func inputCompletionDelimiter(value rune, sortInput bool) bool {
	if isInputSpace(value) || value == '(' || value == ')' {
		return true
	}
	if sortInput {
		return value == ','
	}
	switch value {
	case ',', ':', '=', '!', '>', '<', '~':
		return true
	default:
		return false
	}
}

func isEscapedRune(runes []rune, index int) bool {
	backslashes := 0
	for position := index - 1; position >= 0 && runes[position] == '\\'; position-- {
		backslashes++
	}
	return backslashes%2 != 0
}

func (m Model) filterColumnCompletions() []string {
	columns := []string{"provider", "space", "list", "status", "priority", "assignee", "due", "sync", "completed", "text"}
	for _, column := range m.availableTaskColumns() {
		switch column.ID {
		case taskColumnTask:
			columns = append(columns, "title")
		case taskColumnStatus, taskColumnPriority:
			continue
		case taskColumnAssignees:
			continue
		case taskColumnEstimate:
			columns = append(columns, "estimate")
		case taskColumnTracked:
			columns = append(columns, "tracked")
		case taskColumnDue:
			continue
		default:
			for _, definition := range m.Data.TaskColumns {
				if definition.ID == column.ID {
					columns = append(columns, quoteCompletionName(definition.Name))
					break
				}
			}
		}
	}
	return uniqueCompletions(columns)
}

func (m Model) sortColumnCompletions() []string {
	columns := make([]string, 0, len(m.availableTaskColumns()))
	for _, column := range m.availableTaskColumns() {
		switch column.ID {
		case taskColumnTask:
			columns = append(columns, "title")
		case taskColumnAssignees:
			columns = append(columns, "assignee")
		case taskColumnEstimate:
			columns = append(columns, "estimate")
		case taskColumnTracked:
			columns = append(columns, "tracked")
		case taskColumnDue:
			columns = append(columns, "due")
		case taskColumnStatus, taskColumnPriority:
			columns = append(columns, column.ID)
		default:
			for _, definition := range m.Data.TaskColumns {
				if definition.ID == column.ID {
					columns = append(columns, quoteCompletionName(definition.Name))
					break
				}
			}
		}
	}
	return uniqueCompletions(columns)
}

func quoteCompletionName(value string) string {
	if isFilterBoolean(value) || strings.ContainsAny(value, " \t,:=!<>~()\"") {
		return strconv.Quote(value)
	}
	return value
}

func uniqueCompletions(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		key := normalize(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func (m Model) isFilterColumn(value string) bool {
	_, ok := m.resolveFilterColumn(unquoteCompletionValue(value))
	return ok
}

func (m Model) isSortColumn(value string) bool {
	_, ok := m.resolveSortColumn(unquoteCompletionValue(value))
	return ok
}

func (m Model) filterOperatorCompletions(column string) []string {
	resolved, ok := m.resolveFilterColumn(unquoteCompletionValue(column))
	if !ok {
		return nil
	}
	if resolved == "@completed" || resolved == "@provider" || resolved == "@space" || resolved == "@list" || resolved == "@sync" || resolved == "@text" {
		return []string{":", "=", "!=", "~"}
	}
	if resolved == taskColumnPriority || resolved == taskColumnDue || resolved == taskColumnEstimate || resolved == taskColumnTracked {
		return []string{":", "=", "!=", ">", ">=", "<", "<=", "~"}
	}
	columnType := m.sortColumnType(resolved)
	if columnType == "number" || columnType == "numeric" || columnType == "date" || columnType == "datetime" {
		return []string{":", "=", "!=", ">", ">=", "<", "<=", "~"}
	}
	return []string{":", "=", "!=", "~"}
}

func (m Model) filterValueCompletions(column string) []string {
	resolved, ok := m.resolveFilterColumn(unquoteCompletionValue(column))
	if !ok {
		return nil
	}
	values := make([]string, 0)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		values = append(values, quoteCompletionName(value))
	}
	switch resolved {
	case taskColumnStatus:
		for _, task := range m.tasksInCompletionScope() {
			add(task.Status)
		}
		if list, ok := m.selectedList(); ok {
			for _, options := range m.Data.EditorOptions {
				if options.ProviderID == list.ProviderID && options.ListID == list.ID {
					for _, status := range options.Statuses {
						add(status)
					}
				}
			}
		}
	case taskColumnPriority:
		for _, priority := range []Priority{PriorityNone, PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent} {
			add(string(priority))
		}
	case taskColumnAssignees:
		for _, task := range m.tasksInCompletionScope() {
			add(task.Assignee)
		}
	case "@completed":
		values = []string{"true", "false"}
	case "@sync":
		values = []string{"synced", "pending", "syncing", "conflict", "failed", "local"}
	case "@provider":
		for _, provider := range m.allProviders() {
			add(string(provider.ID))
			add(provider.Name)
		}
	case "@space":
		for _, space := range m.Data.Spaces {
			if m.UI.ActiveProviderID == "" || space.ProviderID == m.UI.ActiveProviderID {
				add(string(space.ID))
				add(space.Name)
			}
		}
	case "@list":
		for _, list := range m.Data.Lists {
			if m.UI.ActiveProviderID == "" || list.ProviderID == m.UI.ActiveProviderID {
				add(string(list.ID))
				add(list.Name)
			}
		}
	case "@text", taskColumnTask:
		return nil
	case taskColumnDue:
		for _, task := range m.tasksInCompletionScope() {
			if task.DueAt != nil {
				add(task.DueAt.Format("2006-01-02"))
			}
		}
	default:
		tasks := m.tasksInCompletionScope()
		inScope := make(map[scopedID]struct{}, len(tasks))
		for _, task := range tasks {
			inScope[scopedID{provider: task.ProviderID, id: string(task.ID)}] = struct{}{}
		}
		for _, valueSet := range m.Data.TaskColumnValues {
			if _, ok := inScope[scopedID{provider: valueSet.ProviderID, id: string(valueSet.TaskID)}]; ok {
				add(valueSet.Values[resolved])
			}
		}
	}
	values = uniqueCompletions(values)
	if resolved != taskColumnPriority {
		sort.SliceStable(values, func(i, j int) bool { return normalize(values[i]) < normalize(values[j]) })
	}
	if len(values) > 40 {
		values = values[:40]
	}
	return values
}

func (m Model) tasksInCompletionScope() []Task {
	list, hasList := m.selectedList()
	tasks := make([]Task, 0)
	for _, task := range m.Data.Tasks {
		if task.IsDeleted || (m.UI.ActiveProviderID != "" && task.ProviderID != m.UI.ActiveProviderID) {
			continue
		}
		if hasList && (task.ProviderID != list.ProviderID || !taskHasList(task, list.ID)) {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks
}

func filterBooleanCompletions(before string) []string {
	choices := []string{"AND", "OR", "NOT"}
	if open, close := filterParenthesisCount(before); open > close {
		choices = append(choices, ")")
	}
	return choices
}

func filterParenthesisCount(value string) (int, int) {
	open, close := 0, 0
	quoted := false
	runes := []rune(value)
	for index, char := range runes {
		if char == '"' && !isEscapedRune(runes, index) {
			quoted = !quoted
		}
		if quoted {
			continue
		}
		if char == '(' {
			open++
		} else if char == ')' {
			close++
		}
	}
	return open, close
}

func trailingFilterCondition(value string) (column, operator string, ok bool) {
	value = strings.TrimRight(value, " \t\r\n")
	for _, candidate := range []string{">=", "<=", "!=", ":", "=", ">", "<", "~"} {
		if !strings.HasSuffix(value, candidate) {
			continue
		}
		operatorStart := runeCount(value) - runeCount(candidate)
		if quotedAt(value, operatorStart) {
			continue
		}
		left := strings.TrimSpace(string([]rune(value)[:operatorStart]))
		column = lastInputWord(left, false)
		if column != "" && !isFilterBoolean(unquoteCompletionValue(column)) {
			return column, candidate, true
		}
	}
	return "", "", false
}

func quotedAt(value string, index int) bool {
	quoted := false
	runes := []rune(value)
	for offset, char := range runes {
		if offset >= index {
			break
		}
		if char == '"' && !isEscapedRune(runes, offset) {
			quoted = !quoted
		}
	}
	return quoted
}

func hasFilterOperator(value string) bool {
	quoted := false
	runes := []rune(value)
	for index, char := range runes {
		if char == '"' && !isEscapedRune(runes, index) {
			quoted = !quoted
			continue
		}
		if !quoted && strings.ContainsRune(":=!<>~", char) {
			return true
		}
	}
	return false
}

func endsWithBooleanWord(value string) bool {
	word := lastInputWord(strings.TrimSpace(value), false)
	return !strings.HasPrefix(word, `"`) && isFilterBoolean(word)
}

func isFilterBooleanPrefix(value string) bool {
	value = strings.TrimSpace(unquoteCompletionValue(value))
	if value == "" {
		return false
	}
	for _, keyword := range []string{"AND", "OR", "NOT"} {
		if strings.HasPrefix(strings.ToLower(keyword), strings.ToLower(value)) {
			return true
		}
	}
	return false
}

func expectsFilterColumn(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || endsWithBooleanWord(value) || strings.HasSuffix(value, "(")
}

func lastInputWord(value string, sortInput bool) string {
	value = strings.TrimRight(value, " \t\r\n")
	if value == "" {
		return ""
	}
	start, end := inputTokenRange(value, runeCount(value), sortInput)
	if start == end {
		return ""
	}
	return string([]rune(value)[start:end])
}

func unquoteCompletionValue(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, `"`) {
		if strings.HasSuffix(value, `"`) {
			if unquoted, err := strconv.Unquote(value); err == nil {
				return unquoted
			}
		}
		return strings.TrimPrefix(value, `"`)
	}
	return value
}

func matchInputCompletions(candidates []string, fragment string) []string {
	fragment = normalize(unquoteCompletionValue(fragment))
	matched := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.HasPrefix(normalize(unquoteCompletionValue(candidate)), fragment) {
			matched = append(matched, candidate)
		}
	}
	return matched
}

func lastSortSeparator(runes []rune, cursor int) int {
	quoted := false
	last := -1
	for index := 0; index < cursor; index++ {
		if runes[index] == '"' && !isEscapedRune(runes, index) {
			quoted = !quoted
			continue
		}
		if !quoted && runes[index] == ',' {
			last = index
		}
	}
	return last
}
