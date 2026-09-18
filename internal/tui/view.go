package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// View renders the current model without changing it or performing I/O.
func (m Model) View() string {
	width := m.UI.Width
	height := m.UI.Height
	if width < 1 {
		width = 100
	}
	if height < 1 {
		height = 24
	}

	header := fit(" TASK MANAGER | overall sync: "+string(m.overallSync()), width)
	divider := fit(strings.Repeat("-", width), width)
	modeLine := m.modeLine(width)
	statusLine := m.statusLine(width)
	fixedLines := 3
	if modeLine != "" {
		fixedLines++
	}
	if statusLine != "" {
		fixedLines++
	}
	bodyHeight := height - fixedLines
	if bodyHeight < 1 {
		lines := []string{header}
		if len(lines) < height {
			lines = append(lines, divider)
		}
		if len(lines) < height && modeLine != "" {
			lines = append(lines, modeLine)
		}
		if len(lines) < height && statusLine != "" {
			lines = append(lines, statusLine)
		}
		if len(lines) < height {
			lines = append(lines, fit(m.footerLine(), width))
		}
		return strings.Join(lines[:minInt(len(lines), height)], "\n")
	}

	body := m.bodyLines(width, bodyHeight)
	lines := make([]string, 0, height)
	lines = append(lines, header, divider)
	if modeLine != "" {
		lines = append(lines, modeLine)
	}
	for len(body) < bodyHeight {
		body = append(body, "")
	}
	if len(body) > bodyHeight {
		body = body[:bodyHeight]
	}
	lines = append(lines, body...)
	if statusLine != "" {
		lines = append(lines, statusLine)
	}
	lines = append(lines, fit(m.footerLine(), width))
	return strings.Join(lines[:minInt(len(lines), height)], "\n")
}

// Render is an adapter-friendly alias for View.
func (m Model) Render() string {
	return m.View()
}

func (m Model) bodyLines(width, height int) []string {
	if width < 60 {
		lines := m.treeLines(width)
		lines = append(lines, fit("", width))
		lines = append(lines, m.taskLines(width)...)
		return lines
	}

	leftWidth := width * 32 / 100
	if leftWidth < 24 {
		leftWidth = 24
	}
	if leftWidth > width-24 {
		leftWidth = width - 24
	}
	rightWidth := width - leftWidth - 3
	left := m.treeLines(leftWidth)
	right := m.taskLines(rightWidth)
	lines := make([]string, 0, maxInt(len(left), len(right)))
	rowCount := maxInt(len(left), len(right))
	for index := 0; index < rowCount; index++ {
		leftLine := ""
		rightLine := ""
		if index < len(left) {
			leftLine = left[index]
		}
		if index < len(right) {
			rightLine = right[index]
		}
		lines = append(lines, fit(leftLine, leftWidth)+" | "+fit(rightLine, rightWidth))
	}
	if len(lines) > height {
		return lines[:height]
	}
	return lines
}

func (m Model) treeLines(width int) []string {
	nodes := m.TreeNodes()
	lines := []string{fitAtOffset("SPACES / LISTS", width, m.UI.TreeHorizontalOffset)}
	if len(nodes) == 0 {
		return append(lines, fit("  (no cached hierarchy)", width))
	}
	offset := clamp(m.UI.TreeOffset, 0, len(nodes)-1)
	if offset > 0 {
		lines = append(lines, fit("  ...", width))
	}
	for _, node := range nodes[offset:] {
		selected := node.Ref == m.UI.SelectedNode && m.UI.Focus == PanelHierarchy
		marker := "  "
		if selected {
			marker = "> "
		}
		expansion := "   "
		if node.Ref.Kind == TreeNodeProvider || node.Ref.Kind == TreeNodeSpace {
			if node.Expanded {
				expansion = "[-]"
			} else {
				expansion = "[+]"
			}
		}
		kind := "L"
		switch node.Ref.Kind {
		case TreeNodeProvider:
			kind = "P"
		case TreeNodeSpace:
			kind = "S"
		}
		name := safeText(node.Name)
		line := fmt.Sprintf("%s%s %s %s [%s]", marker, strings.Repeat("  ", node.Depth), kind, expansion, name)
		lines = append(lines, fitAtOffset(line, width, m.UI.TreeHorizontalOffset))
	}
	return lines
}

func (m Model) taskLines(width int) []string {
	heading := "TASKS"
	if m.UI.SearchActive {
		heading += " | SEARCH " + quoteOrEmpty(m.UI.SearchQuery)
	}
	if m.UI.FilterActive {
		heading += " | FILTER " + quoteOrEmpty(m.UI.Filter.String())
	}
	lines := []string{fitAtOffset(heading, width, m.UI.TaskHorizontalOffset)}
	rows := m.VisibleTasks()
	if len(rows) == 0 {
		if m.UI.SearchActive {
			return append(lines, fit("  (no local search results)", width))
		}
		return append(lines, fit("  (no tasks in this view)", width))
	}
	offset := clamp(m.UI.TaskOffset, 0, len(rows)-1)
	if offset > 0 {
		lines = append(lines, fit("  ...", width))
	}
	for index, row := range rows[offset:] {
		actualIndex := index + offset
		selected := actualIndex == m.UI.TaskCursor && m.UI.Focus == PanelTasks
		marker := "  "
		if selected {
			marker = "> "
		}
		line := taskLineText(row, marker)
		lines = append(lines, fitAtOffset(line, width, m.UI.TaskHorizontalOffset))
	}
	return lines
}

func (m Model) modeLine(width int) string {
	var prefix, suffix string
	switch m.UI.Mode {
	case ModeSearch:
		prefix = "SEARCH"
		suffix = " enter apply | esc cancel"
	case ModeFilter:
		prefix = "FILTER"
		suffix = " enter apply | esc cancel"
	case ModeCommand:
		prefix = "COMMAND"
		suffix = " enter run | esc cancel"
	case ModeCreateTask:
		prefix = "NEW TASK"
		suffix = " enter submit | esc cancel"
	case ModeEditTask:
		prefix = "EDIT TASK"
		suffix = " enter submit | esc cancel"
	case ModeConfirm:
		return fit("CONFIRM: "+safeText(m.UI.ConfirmPrompt)+"  [y/enter] yes  [n/esc] no", width)
	default:
		return ""
	}
	return fit(prefix+": "+inputWithCursor(m.UI.Input, m.UI.InputCursor)+suffix, width)
}

func (m Model) statusLine(width int) string {
	if strings.TrimSpace(m.Status.Text) == "" {
		return ""
	}
	level := strings.ToUpper(string(m.Status.Level))
	if level == "" {
		level = "INFO"
	}
	return fit("STATUS "+level+": "+safeText(m.Status.Text), width)
}

func (m Model) footerLine() string {
	if m.UI.Quitting {
		return "closing..."
	}
	return "j/k or up/down move | tab switch panel | h/l or left/right scroll | enter open | g/G first/last | n new | e edit | x complete | d delete | / search | f filter | : commands | r refresh | q quit"
}

func (m Model) overallSync() SyncState {
	providers := m.viewProviders()
	if len(providers) == 0 {
		return SyncStateLocal
	}
	best := SyncStateUnknown
	bestRank := syncRank(best)
	for _, provider := range providers {
		state := stateOr(provider.SyncState, SyncStateUnknown)
		if syncRank(state) > bestRank {
			best = state
			bestRank = syncRank(state)
		}
	}
	return best
}

func syncRank(state SyncState) int {
	switch state {
	case SyncStateFailed, SyncStateConflict:
		return 5
	case SyncStateSyncing:
		return 4
	case SyncStatePending:
		return 3
	case SyncStateSynced:
		return 2
	case SyncStateLocal:
		return 1
	default:
		return 0
	}
}

func inputWithCursor(value string, cursor int) string {
	runes := []rune(value)
	cursor = clamp(cursor, 0, len(runes))
	withCursor := append([]rune(nil), runes...)
	withCursor = append(withCursor, 0)
	copy(withCursor[cursor+1:], withCursor[cursor:])
	withCursor[cursor] = '_'
	return string(withCursor)
}

func quoteOrEmpty(value string) string {
	if value == "" {
		return "(all)"
	}
	return fmt.Sprintf("%q", safeText(value))
}

func safeText(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\t", " ")
}

func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > width {
		if width == 1 {
			return string(runes[:1])
		}
		return string(runes[:width-1]) + "~"
	}
	return value + strings.Repeat(" ", width-utf8.RuneCountInString(value))
}

func fitAtOffset(value string, width, offset int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	offset = clamp(offset, 0, len(runes))
	end := minInt(offset+width, len(runes))
	visible := string(runes[offset:end])
	return visible + strings.Repeat(" ", width-runeCount(visible))
}

func taskLineText(row TaskRow, marker string) string {
	complete := " "
	if isTaskComplete(row.Task) {
		complete = "x"
	}
	title := safeText(row.Task.Title)
	if title == "" {
		title = "(untitled task)"
	}
	location := ""
	if row.SpaceName != "" || row.ListName != "" {
		location = " @ " + safeText(row.SpaceName) + "/" + safeText(row.ListName)
	}
	status := safeText(row.Task.Status)
	if status == "" {
		status = "unspecified"
	}
	metadata := status
	if row.Task.Priority != "" {
		metadata += "; priority " + safeText(string(row.Task.Priority))
	}
	if row.Task.DueAt != nil {
		metadata += "; due " + row.Task.DueAt.Format("2006-01-02")
	}
	return fmt.Sprintf("%s[%s] %s%s (%s)", marker, complete, title, location, metadata)
}

func (m Model) maxHorizontalOffset(panel Panel) int {
	width := m.horizontalPanelWidth(panel)
	return maxInt(m.maxPanelLineWidth(panel)-width, 0)
}

func (m Model) horizontalPanelWidth(panel Panel) int {
	width := m.UI.Width
	if width < 1 {
		width = 100
	}
	if width < 72 {
		return maxInt(width-4, 1)
	}

	leftWidth := width * 32 / 100
	if leftWidth < 26 {
		leftWidth = 26
	}
	if leftWidth > width-30 {
		leftWidth = width - 30
	}
	if panel == PanelHierarchy {
		return maxInt(leftWidth-4, 1)
	}
	return maxInt(width-leftWidth-6, 1)
}

func (m Model) maxPanelLineWidth(panel Panel) int {
	if panel == PanelHierarchy {
		maxWidth := runeCount("SPACES / LISTS")
		for _, node := range m.TreeNodes() {
			kind, expansion := treeDisplayParts(node)
			name := safeText(node.Name)
			charmLine := fmt.Sprintf("  %s%s %s %s", strings.Repeat("  ", node.Depth), kind, expansion, name)
			plainLine := fmt.Sprintf("  %s %s %s [%s]", strings.Repeat("  ", node.Depth), kind, expansion, name)
			maxWidth = maxInt(maxWidth, runeCount(charmLine))
			maxWidth = maxInt(maxWidth, runeCount(plainLine))
		}
		return maxWidth
	}

	heading := "TASKS"
	if m.UI.SearchActive {
		heading += " | SEARCH " + quoteOrEmpty(m.UI.SearchQuery)
	}
	if m.UI.FilterActive {
		heading += " | FILTER " + quoteOrEmpty(m.UI.Filter.String())
	}
	maxWidth := runeCount(heading)
	for _, row := range m.VisibleTasks() {
		maxWidth = maxInt(maxWidth, runeCount(taskLineText(row, "  ")))
	}
	return maxWidth
}

func treeDisplayParts(node TreeNode) (kind, expansion string) {
	kind = "L"
	switch node.Ref.Kind {
	case TreeNodeProvider:
		kind = "P"
	case TreeNodeSpace:
		kind = "S"
	}
	expansion = "   "
	if node.Ref.Kind == TreeNodeProvider || node.Ref.Kind == TreeNodeSpace {
		if node.Expanded {
			expansion = "[-]"
		} else {
			expansion = "[+]"
		}
	}
	return kind, expansion
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
