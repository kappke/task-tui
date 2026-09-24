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
		return prefix, candidates, start, cursor
	}

	first := normalize(before[0])
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
	return prefix, candidates, start, cursor
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
	prefix, candidates, start, end := m.commandCompletion()
	inputRunes := []rune(m.UI.Input)
	fragment := string(inputRunes[start:end])
	continuing := fragment != "" && containsString(m.UI.CommandCompletion, fragment)
	if continuing {
		candidates = m.UI.CommandCompletion
	}
	if !continuing {
		m.UI.CommandCompletion = candidates
		m.UI.CommandCompletionStart = start
		m.UI.CommandCompletionEnd = end
		m.UI.CommandCompletionIndex = 0
	}
	if len(candidates) == 0 {
		return
	}
	if reverse {
		m.UI.CommandCompletionIndex--
		if m.UI.CommandCompletionIndex < 0 {
			m.UI.CommandCompletionIndex = len(candidates) - 1
		}
	} else if continuing {
		m.UI.CommandCompletionIndex++
		if m.UI.CommandCompletionIndex >= len(candidates) {
			m.UI.CommandCompletionIndex = 0
		}
	} else if m.UI.CommandCompletionIndex >= len(candidates) {
		m.UI.CommandCompletionIndex = 0
	}

	value := candidates[m.UI.CommandCompletionIndex]
	completed := prefix + value
	m.UI.Input = completed
	m.UI.InputCursor = runeCount(completed)
	m.UI.CommandCompletion = candidates
	m.UI.CommandCompletionStart = start
	m.UI.CommandCompletionEnd = runeCount(completed)
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
	if m.UI.Mode != ModeCommand {
		return ""
	}
	_, candidates, _, _ := m.commandCompletion()
	if len(m.UI.CommandCompletion) > 0 {
		candidates = m.UI.CommandCompletion
	}
	if len(candidates) == 0 {
		return ""
	}
	start, end := completionWindow(candidates, m.UI.CommandCompletionIndex, width)
	parts := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		candidate := candidates[index]
		if index == m.UI.CommandCompletionIndex {
			parts = append(parts, "["+candidate+"]")
		} else {
			parts = append(parts, candidate)
		}
	}
	return fit("  options: "+strings.Join(parts, "  "), width)
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
