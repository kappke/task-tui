package tui

import (
	"fmt"
	"strings"
	"time"
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
	if m.UI.Mode == ModeDetail {
		return m.visibleDetailLines(width, height)
	}
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
	lines := []string{
		fitAtOffset(m.taskHeading(), width, m.UI.TaskHorizontalOffset),
		fitAtOffset(taskTableHeaderLine(width), width, m.UI.TaskHorizontalOffset),
	}
	groups := m.VisibleTaskGroups()
	rows := flattenTaskGroups(groups)
	if len(rows) == 0 && (m.UI.GroupBy == TaskGroupNone || len(groups) == 0) {
		if m.UI.SearchActive {
			return append(lines, fit("  (no local search results)", width))
		}
		return append(lines, fit("  (no tasks in this view)", width))
	}
	if m.UI.GroupBy != TaskGroupNone && allTaskGroupsCollapsed(groups) {
		offset := clamp(m.UI.TaskOffset, 0, len(groups)-1)
		if offset > 0 {
			lines = append(lines, fit("  ...", width))
		}
		for _, group := range groups[offset:] {
			lines = append(lines, fitAtOffset(m.taskGroupLine(group), width, m.UI.TaskHorizontalOffset))
		}
		return lines
	}
	offset := 0
	if len(rows) > 0 {
		offset = clamp(m.UI.TaskOffset, 0, len(rows)-1)
	}
	if offset > 0 {
		lines = append(lines, fit("  ...", width))
	}
	if m.UI.GroupBy == TaskGroupNone {
		for index, row := range rows[offset:] {
			lines = append(lines, m.taskRowLine(row, index+offset, width))
		}
		return lines
	}

	rowIndex := 0
	for _, group := range groups {
		if group.Collapsed {
			lines = append(lines, fitAtOffset(m.taskGroupLine(group), width, m.UI.TaskHorizontalOffset))
			continue
		}
		groupStart := rowIndex
		groupEnd := groupStart + len(group.Rows)
		rowIndex = groupEnd
		if groupEnd <= offset {
			continue
		}
		lines = append(lines, fitAtOffset(m.taskGroupLine(group), width, m.UI.TaskHorizontalOffset))
		start := maxInt(offset-groupStart, 0)
		for index, row := range group.Rows[start:] {
			lines = append(lines, m.taskRowLine(row, groupStart+start+index, width))
		}
	}
	return lines
}

func (m Model) taskHeading() string {
	heading := "TASKS"
	if m.UI.SearchActive {
		heading += " | SEARCH " + quoteOrEmpty(m.UI.SearchQuery)
	}
	if m.UI.FilterActive {
		heading += " | FILTER " + quoteOrEmpty(m.UI.Filter.String())
	}
	if m.UI.GroupBy != TaskGroupNone {
		heading += " | GROUP " + string(m.UI.GroupBy)
	}
	return heading
}

func taskGroupHeading(mode TaskGroupMode, group TaskGroup) string {
	label := safeText(group.Label)
	if label == "" {
		return ""
	}
	if mode == TaskGroupStatus {
		label = displayTaskStatus(label)
	}
	expansion := "[-]"
	if group.Collapsed {
		expansion = "[+]"
	}
	return fmt.Sprintf("%s %s (%d)", expansion, label, len(group.Rows))
}

func (m Model) taskGroupLine(group TaskGroup) string {
	marker := "  "
	if m.taskGroupSelected(group) {
		marker = "> "
	}
	return marker + taskGroupHeading(m.UI.GroupBy, group)
}

func (m Model) taskGroupSelected(group TaskGroup) bool {
	if m.UI.Focus != PanelTasks || !m.UI.TaskHeaderSelected {
		return false
	}
	return m.UI.FocusedGroup == taskGroupStateKey(m.UI.GroupBy, group.Key)
}

func (m Model) taskRowLine(row TaskRow, index, width int) string {
	marker := "  "
	if index == m.UI.TaskCursor && m.UI.Focus == PanelTasks {
		marker = "> "
	}
	return fitAtOffset(taskTableLine(row, marker, taskTableLayoutFor(width)), width, m.UI.TaskHorizontalOffset)
}

func (m Model) detailLines(width int) []string {
	width = maxInt(width, 1)
	lines := []string{fit("TASK DETAIL", width)}
	row, ok := m.selectedTask()
	if !ok {
		return append(lines, fit("(no task selected)", width))
	}

	task := row.Task
	title := safeText(task.Title)
	if title == "" {
		title = "(untitled task)"
	}
	appendField := func(label, value string) {
		lines = append(lines, fit(label+": "+safeText(value), width))
	}

	lines = append(lines, "")
	appendField("TITLE", title)
	appendField("TASK ID", string(task.ID))
	appendField("PROVIDER", taskProviderLabel(row))
	location := safeText(row.SpaceName)
	if location == "" {
		location = string(row.SpaceID)
	}
	if row.ListName != "" {
		location += "/" + safeText(row.ListName)
	} else if row.ListID != "" {
		location += "/" + string(row.ListID)
	}
	appendField("LOCATION", location)
	assignee := safeText(task.Assignee)
	if assignee == "" {
		assignee = "(unassigned)"
	}
	appendField("ASSIGNEE", assignee)

	status := safeText(task.Status)
	if status == "" {
		status = "(unspecified)"
	}
	appendField("STATUS", displayTaskStatus(status))
	priority := safeText(string(task.Priority))
	if priority == "" {
		priority = "(none)"
	}
	appendField("PRIORITY", priority)
	appendField("TIME ESTIMATE", formatTaskDuration(task.TimeEstimate))
	appendField("TIME TRACKED", formatTaskDuration(task.TimeTracked))
	due := "(none)"
	if task.DueAt != nil {
		due = task.DueAt.Format("2006-01-02 15:04 MST")
	}
	appendField("DUE", due)
	appendField("SYNC", string(stateOr(task.SyncState, SyncStateUnknown)))
	if !task.CreatedAt.IsZero() {
		appendField("CREATED", task.CreatedAt.Format("2006-01-02 15:04 MST"))
	}
	if !task.UpdatedAt.IsZero() {
		appendField("UPDATED", task.UpdatedAt.Format("2006-01-02 15:04 MST"))
	}

	lines = append(lines, "", fit("DESCRIPTION", width))
	description := strings.TrimSpace(task.Description)
	if description == "" {
		lines = append(lines, fit("  (no description)", width))
		return lines
	}
	for _, line := range wrapDetailText(description, maxInt(width-2, 1)) {
		if line == "" {
			lines = append(lines, fit("", width))
			continue
		}
		lines = append(lines, fit("  "+line, width))
	}
	return lines
}

func (m Model) visibleDetailLines(width, height int) []string {
	lines := m.detailLines(width)
	height = maxInt(height, 1)
	if len(lines) <= height {
		return lines
	}
	offset := clamp(m.UI.DetailOffset, 0, len(lines)-height)
	return lines[offset : offset+height]
}

func wrapDetailText(text string, width int) []string {
	width = maxInt(width, 1)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	paragraphs := strings.Split(text, "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}

		current := ""
		for _, word := range words {
			wordRunes := []rune(word)
			for len(wordRunes) > width {
				if current != "" {
					lines = append(lines, current)
					current = ""
				}
				lines = append(lines, string(wordRunes[:width]))
				wordRunes = wordRunes[width:]
			}
			word = string(wordRunes)
			if word == "" {
				continue
			}
			if current == "" {
				current = word
				continue
			}
			if runeCount(current)+1+runeCount(word) <= width {
				current += " " + word
				continue
			}
			lines = append(lines, current)
			current = word
		}
		if current != "" {
			lines = append(lines, current)
		}
	}
	if len(lines) == 0 {
		return []string{""}
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
	if m.UI.Mode == ModeDetail {
		return "j/k or up/down scroll | g/G top/bottom | esc close | q quit"
	}
	return "j/k or up/down move | tab switch panel | h/l or left/right scroll | enter open/toggle group | space collapse/expand group | +/- expand/collapse all | g/G first/last | n new | e edit | x complete | d delete | / search | f filter | : group/filter/commands | r refresh | q quit"
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

type taskTableLayout struct {
	Name      int
	Status    int
	Assignees int
	Priority  int
	Estimate  int
	Tracked   int
	Due       int
}

const taskTableGap = "  "

func taskTableLayoutFor(width int) taskTableLayout {
	columns := taskTableLayout{
		Name:      40,
		Status:    12,
		Assignees: 18,
		Priority:  10,
		Estimate:  14,
		Tracked:   13,
		Due:       12,
	}
	if width > 0 {
		baseWidth := 2 + columns.Name + columns.Status + columns.Assignees + columns.Priority + columns.Estimate + columns.Tracked + columns.Due + runeCount(taskTableGap)*6
		if width > baseWidth {
			columns.Name += width - baseWidth
		}
	}
	return columns
}

func taskTableHeaderLine(width int) string {
	columns := taskTableLayoutFor(width)
	return taskTableCell("  "+"TASK", 2+columns.Name) + taskTableGap +
		taskTableCell("STATUS", columns.Status) + taskTableGap +
		taskTableCell("ASSIGNEES", columns.Assignees) + taskTableGap +
		taskTableCell("PRIORITY", columns.Priority) + taskTableGap +
		taskTableCell("TIME ESTIMATE", columns.Estimate) + taskTableGap +
		taskTableCell("TIME TRACKED", columns.Tracked) + taskTableGap +
		taskTableCell("DUE DATE", columns.Due)
}

func taskTableLine(row TaskRow, marker string, columns taskTableLayout) string {
	completion := "  "
	if isTaskComplete(row.Task) {
		completion = "x "
	}
	name := completion + strings.Repeat("  ", maxInt(row.HierarchyDepth, 0)) + taskTitle(row)
	return marker + taskTableCell(name, columns.Name) + taskTableGap +
		taskTableCell(displayTaskStatus(row.Task.Status), columns.Status) + taskTableGap +
		taskTableCell(taskAssigneeLabel(row), columns.Assignees) + taskTableGap +
		taskTableCell(displayTaskPriority(row.Task.Priority), columns.Priority) + taskTableGap +
		taskTableCell(formatTaskDuration(row.Task.TimeEstimate), columns.Estimate) + taskTableGap +
		taskTableCell(formatTaskDuration(row.Task.TimeTracked), columns.Tracked) + taskTableGap +
		taskTableCell(formatTaskDueDate(row.Task.DueAt), columns.Due)
}

func taskTableCell(value string, width int) string {
	return fit(safeText(value), width)
}

func taskAssigneeLabel(row TaskRow) string {
	value := strings.TrimSpace(row.Assignee)
	if value == "" {
		value = strings.TrimSpace(row.Task.Assignee)
	}
	if value == "" {
		return "(UNASSIGNED)"
	}
	return value
}

func displayTaskStatus(value string) string {
	value = strings.TrimSpace(safeText(value))
	if value == "" {
		return "(UNSPECIFIED)"
	}
	return strings.ToUpper(value)
}

func displayTaskPriority(value Priority) string {
	priority := strings.TrimSpace(string(value))
	if priority == "" || strings.EqualFold(priority, string(PriorityNone)) {
		return "-"
	}
	return strings.ToUpper(priority)
}

func formatTaskDuration(value *time.Duration) string {
	if value == nil {
		return "-"
	}
	if *value <= 0 {
		return "0m"
	}
	remaining := *value
	days := remaining / (24 * time.Hour)
	remaining %= 24 * time.Hour
	hours := remaining / time.Hour
	remaining %= time.Hour
	minutes := remaining / time.Minute
	seconds := remaining % time.Minute / time.Second
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if len(parts) == 0 && seconds > 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	if len(parts) == 0 {
		return "<1m"
	}
	return strings.Join(parts, " ")
}

func formatTaskDueDate(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return value.Format("2006-01-02")
}

func taskLineText(row TaskRow, marker string) string {
	return taskTableLine(row, marker, taskTableLayoutFor(0))
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

	maxWidth := runeCount(m.taskHeading())
	groups := m.VisibleTaskGroups()
	for _, group := range groups {
		maxWidth = maxInt(maxWidth, runeCount(m.taskGroupLine(group)))
	}
	for _, row := range flattenTaskGroups(groups) {
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
