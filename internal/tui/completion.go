package tui

import "strings"

var commandNames = []string{
	"create",
	"edit",
	"complete",
	"delete",
	"move",
	"add-list",
	"remove-list",
	"search",
	"filter",
	"sort",
	"group",
	"ungroup",
	"provider",
	"refresh",
	"task",
	"space",
	"list",
	"columns",
	"quit",
	"help",
}

var taskCommandNames = []string{"create", "edit", "complete", "delete", "move", "add-list", "remove-list"}

var hierarchyCommandNames = []string{"create", "new"}

var groupNames = []string{"status", "assignee", "priority", "tasks", "none"}

func (m Model) commandCompletion() (prefix string, candidates []string, start, end int) {
	input := m.UI.Input
	cursor := clamp(m.UI.InputCursor, 0, runeCount(input))
	runes := []rune(input)
	start = cursor
	for start > 0 && !isInputSpace(runes[start-1]) {
		start--
	}
	prefix = string(runes[:start])
	fragment := string(runes[start:cursor])
	before := strings.Fields(prefix)
	if len(before) == 0 {
		candidates = matchingCompletions(commandNames, fragment)
		_, end = inputTokenRange(input, cursor, false)
		return prefix, candidates, start, end
	}

	first := normalize(before[0])
	commandEnd := 0
	for commandEnd < len(runes) && !isInputSpace(runes[commandEnd]) {
		commandEnd++
	}
	if (first == "filter" || first == "sort" || first == "order") && commandEnd < cursor {
		argumentStart := commandEnd
		for argumentStart < len(runes) && isInputSpace(runes[argumentStart]) {
			argumentStart++
		}
		if cursor >= argumentStart {
			argumentModel := m
			if first == "filter" {
				argumentModel.UI.Mode = ModeFilter
			} else {
				argumentModel.UI.Mode = ModeSort
			}
			argumentModel.UI.Input = string(runes[argumentStart:])
			argumentModel.UI.InputCursor = clamp(cursor-argumentStart, 0, runeCount(argumentModel.UI.Input))
			context := argumentModel.inputCompletionContext()
			start = argumentStart + context.Start
			end = argumentStart + context.End
			return string(runes[:start]), context.Candidates, start, end
		}
	}
	switch first {
	case "task":
		if len(before) == 1 {
			candidates = matchingCompletions(taskCommandNames, fragment)
		}
	case "space", "list":
		if len(before) == 1 {
			candidates = matchingCompletions(hierarchyCommandNames, fragment)
		}
	case "group", "groupby":
		if len(before) == 1 || (len(before) == 2 && normalize(before[1]) == "by") {
			candidates = matchingCompletions(groupNames, fragment)
		}
	case "provider":
		if len(before) == 1 {
			candidates = matchingCompletions(m.providerCommandNames(), fragment)
		} else if len(before) == 2 && (normalize(before[1]) == "switch" || normalize(before[1]) == "select") {
			candidates = matchingCompletions(m.providerCommandNames(), fragment)
		}
	}
	_, end = inputTokenRange(input, cursor, false)
	return prefix, candidates, start, end
}

func (m Model) providerCommandNames() []string {
	providers := m.allProviders()
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, string(provider.ID))
	}
	return names
}

func matchingCompletions(values []string, fragment string) []string {
	fragment = normalize(fragment)
	candidates := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, fragment) {
			candidates = append(candidates, value)
		}
	}
	return candidates
}

func isInputSpace(value rune) bool {
	return value == ' ' || value == '\t'
}

func (m *Model) completeCommand(reverse bool) {
	context := m.inputCompletionContext()
	candidates := context.Candidates
	start, end := context.Start, context.End
	cycling := len(m.UI.CommandCompletion) > 0 && m.UI.InputCursor == m.UI.CommandCompletionEnd
	if cycling {
		candidates = m.UI.CommandCompletion
		start = m.UI.CommandCompletionStart
		end = m.UI.CommandCompletionEnd
	}
	if len(candidates) == 0 {
		m.resetCommandCompletion()
		return
	}
	if cycling && reverse {
		m.UI.CommandCompletionIndex--
		if m.UI.CommandCompletionIndex < 0 {
			m.UI.CommandCompletionIndex = len(candidates) - 1
		}
	} else if cycling {
		m.UI.CommandCompletionIndex++
		if m.UI.CommandCompletionIndex >= len(candidates) {
			m.UI.CommandCompletionIndex = 0
		}
	} else if reverse {
		m.UI.CommandCompletionIndex = len(candidates) - 1
	} else {
		m.UI.CommandCompletionIndex = 0
	}

	value := candidates[m.UI.CommandCompletionIndex]
	inputRunes := []rune(m.UI.Input)
	completed := make([]rune, 0, len(inputRunes)+runeCount(value))
	completed = append(completed, inputRunes[:start]...)
	completed = append(completed, []rune(value)...)
	completed = append(completed, inputRunes[end:]...)
	m.UI.Input = string(completed)
	m.UI.InputCursor = start + runeCount(value)
	m.UI.CommandCompletion = candidates
	m.UI.CommandCompletionStart = start
	m.UI.CommandCompletionEnd = m.UI.InputCursor
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (m *Model) resetCommandCompletion() {
	m.UI.CommandCompletion = nil
	m.UI.CommandCompletionIndex = 0
	m.UI.CommandCompletionStart = 0
	m.UI.CommandCompletionEnd = 0
}

func (m Model) commandCompletionLine(width int) string {
	if m.UI.Mode != ModeCommand && m.UI.Mode != ModeFilter && m.UI.Mode != ModeSort {
		return ""
	}
	candidates, selected := m.completionOptions()
	if len(candidates) == 0 {
		return ""
	}
	start, end := completionWindow(candidates, selected, width)
	parts := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		candidate := strings.TrimSpace(candidates[index])
		if index == selected {
			parts = append(parts, "["+candidate+"]")
		} else {
			parts = append(parts, candidate)
		}
	}
	return fit("  options: "+strings.Join(parts, "  "), width)
}

func (m Model) completionOptions() ([]string, int) {
	if len(m.UI.CommandCompletion) > 0 && m.UI.InputCursor == m.UI.CommandCompletionEnd {
		return m.UI.CommandCompletion, clamp(m.UI.CommandCompletionIndex, 0, len(m.UI.CommandCompletion)-1)
	}
	context := m.inputCompletionContext()
	return context.Candidates, 0
}

func (m Model) inputCompletionContext() inputCompletionContext {
	switch m.UI.Mode {
	case ModeCommand:
		_, candidates, start, end := m.commandCompletion()
		return inputCompletionContext{Start: start, End: end, Candidates: candidates}
	case ModeFilter:
		return m.filterCompletionContext()
	case ModeSort:
		return m.sortCompletionContext()
	default:
		return inputCompletionContext{}
	}
}

func completionWindow(candidates []string, selected, width int) (start, end int) {
	if len(candidates) == 0 {
		return 0, 0
	}
	selected = clamp(selected, 0, len(candidates)-1)
	available := width - runeCount("  options: ")
	if available < 1 {
		return selected, selected + 1
	}

	start = 0
	for {
		end = start
		used := 0
		for end < len(candidates) {
			itemWidth := runeCount(candidates[end]) + 2
			if end == selected {
				itemWidth += 2
			}
			separator := 0
			if end > start {
				separator = 2
			}
			if used+separator+itemWidth > available && end > start {
				break
			}
			used += separator + itemWidth
			end++
			if used >= available {
				break
			}
		}
		if selected >= start && selected < end {
			return start, end
		}
		start++
	}
}
