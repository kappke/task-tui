package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CharmOptions connects the framework-neutral model to an application command
// boundary. The callback is evaluated by Bubble Tea as a command, so database
// and synchronization work never runs in the input handler.
type CharmOptions struct {
	OnCommand func(AppCommand) tea.Cmd
}

// CharmModel adapts the presentation model to Bubble Tea. The core model stays
// responsible for selection, input modes, filtering, and command creation;
// CharmModel owns only terminal-framework components and rendering.
type CharmModel struct {
	core    Model
	options CharmOptions

	input           textinput.Model
	help            help.Model
	helpKeys        charmHelpKeyMap
	viewport        viewport.Model
	pendingTaskEdit *pendingTaskEdit
	refreshing      bool
	refreshFrame    int
}

type pendingTaskEdit struct {
	path     string
	original []byte
	script   string
}

type taskEditorFinishedMsg struct {
	path string
	err  error
}

type refreshTickMsg struct{}

var refreshFrames = [...]string{"|", "/", "-", "\\"}

var _ tea.Model = (*CharmModel)(nil)

var (
	charmAccent = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7D56F4"}
	charmMuted  = lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#777777"}
	charmGood   = lipgloss.AdaptiveColor{Light: "#218838", Dark: "#73D216"}
	charmWarn   = lipgloss.AdaptiveColor{Light: "#A15C00", Dark: "#E5C07B"}
	charmError  = lipgloss.AdaptiveColor{Light: "#B42318", Dark: "#FF7B72"}

	charmHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(charmAccent).
				Padding(0, 1)
	charmSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(charmAccent)
	charmSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(charmAccent)
	charmMutedStyle        = lipgloss.NewStyle().Foreground(charmMuted)
	charmAccentStyle       = lipgloss.NewStyle().Foreground(charmAccent)
	charmGoodStyle         = lipgloss.NewStyle().Foreground(charmGood)
	charmWarnStyle         = lipgloss.NewStyle().Foreground(charmWarn)
	charmErrorStyle        = lipgloss.NewStyle().Foreground(charmError)
	charmTableHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(charmMuted)
	charmSelectedTaskStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(charmAccent)
)

// NewCharmModel creates a Bubble Tea model from an already available cached
// snapshot. It does not load data or contact a provider.
func NewCharmModel(core Model, options CharmOptions) *CharmModel {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "type here"
	input.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	input.Cursor.Style = lipgloss.NewStyle().Foreground(charmAccent)

	return &CharmModel{
		core:     core,
		options:  options,
		input:    input,
		help:     help.New(),
		helpKeys: newCharmHelpKeyMap(),
		viewport: viewport.New(1, 1),
	}
}

// NewTeaModel is a descriptive alias for integrations that call Bubble Tea's
// root value a tea model.
func NewTeaModel(core Model, options CharmOptions) *CharmModel {
	return NewCharmModel(core, options)
}

// CoreModel returns the framework-neutral model after Bubble Tea exits.
func (m *CharmModel) CoreModel() Model {
	if m == nil {
		return Model{}
	}
	return m.core
}

// Init implements tea.Model. A cache-load command is emitted only for models
// created without an initial snapshot.
func (m *CharmModel) Init() tea.Cmd {
	if m == nil {
		return nil
	}
	return m.dispatch(m.core.Init())
}

// Update translates Bubble Tea messages into the existing presentation state
// machine and dispatches application commands as asynchronous tea commands.
func (m *CharmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m == nil {
		return m, nil
	}

	if finished, ok := msg.(taskEditorFinishedMsg); ok {
		return m, m.finishTaskEditor(finished)
	}
	if _, ok := msg.(refreshTickMsg); ok {
		if !m.refreshing {
			return m, nil
		}
		m.refreshFrame = (m.refreshFrame + 1) % len(refreshFrames)
		return m, refreshTick()
	}

	if key, ok := msg.(tea.KeyMsg); ok && m.shouldOpenTaskEditor(key) {
		return m, m.startTaskEditor(charmKeyMessage(key).name() == "enter")
	}

	var inputCmd tea.Cmd
	if m.inputMode() {
		m.input, inputCmd = m.input.Update(msg)
	}

	var coreMessage Message
	switch value := msg.(type) {
	case tea.WindowSizeMsg:
		coreMessage = WindowSizeMsg{Width: value.Width, Height: value.Height}
	case tea.KeyMsg:
		coreMessage = charmKeyMessage(value)
	case SnapshotMsg, TasksLoadedMsg, SyncStateMsg, ErrorMsg, StatusMsg, CommandResultMsg, QuitMsg:
		coreMessage = value
	default:
		m.syncInput()
		return m, inputCmd
	}

	next, coreCmd := m.core.Update(coreMessage)
	m.core = next
	switch value := coreMessage.(type) {
	case SyncStateMsg:
		m.refreshing = value.State == SyncStateSyncing
		if !m.refreshing {
			m.refreshFrame = 0
		}
	}
	m.syncInput()

	commands := make([]tea.Cmd, 0, 2)
	if inputCmd != nil {
		commands = append(commands, inputCmd)
	}
	if command := m.dispatch(coreCmd); command != nil {
		commands = append(commands, command)
	}
	if m.refreshing {
		commands = append(commands, refreshTick())
	}
	if m.core.UI.Quitting {
		commands = append(commands, tea.Quit)
	}
	return m, tea.Batch(commands...)
}

func refreshTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func (m *CharmModel) shouldOpenTaskEditor(msg tea.KeyMsg) bool {
	if m.pendingTaskEdit != nil || m.core.UI.Mode != ModeBrowse || m.core.UI.Focus != PanelTasks {
		return false
	}
	key := charmKeyMessage(msg)
	return key.name() == "enter" || key.name() == "e"
}

func (m *CharmModel) startTaskEditor(fetchTask bool) tea.Cmd {
	row, ok := m.core.selectedTask()
	if !ok {
		m.core.Status = Status{Level: StatusWarning, Text: "Select a task before editing"}
		return nil
	}
	original := []byte(RenderTaskDocument(row.Task))
	file, err := os.CreateTemp("", "task-tui-*.md")
	if err != nil {
		m.core.Status = Status{Level: StatusError, Text: "Could not create task editor buffer: " + err.Error()}
		return nil
	}
	path := file.Name()
	if _, err := file.Write(original); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		m.core.Status = Status{Level: StatusError, Text: "Could not write task editor buffer: " + err.Error()}
		return nil
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		m.core.Status = Status{Level: StatusError, Text: "Could not close task editor buffer: " + err.Error()}
		return nil
	}
	script, err := m.createTaskEditorScript(row.Task)
	if err != nil {
		_ = os.Remove(path)
		m.core.Status = Status{Level: StatusError, Text: "Could not create task editor configuration: " + err.Error()}
		return nil
	}
	m.pendingTaskEdit = &pendingTaskEdit{path: path, original: original, script: script}
	m.core.Status = Status{Level: StatusInfo, Text: "Editing task in Neovim; save the buffer to apply changes"}
	commands := make([]tea.Cmd, 0, 2)
	if fetchTask {
		commands = append(commands, m.dispatch(m.core.emit(AppCommand{
			Kind:       CommandFetchTask,
			ProviderID: row.Task.ProviderID,
			ListID:     row.Task.ListID,
			TaskID:     row.Task.ID,
		})))
	}
	commands = append(commands, tea.ExecProcess(exec.Command("nvim", "-S", script, path), func(err error) tea.Msg {
		return taskEditorFinishedMsg{path: path, err: err}
	}))
	return tea.Batch(commands...)
}

func (m *CharmModel) finishTaskEditor(finished taskEditorFinishedMsg) tea.Cmd {
	pending := m.pendingTaskEdit
	m.pendingTaskEdit = nil
	defer os.Remove(finished.path)
	if pending != nil && pending.script != "" {
		defer os.Remove(pending.script)
	}
	if finished.err != nil {
		m.core.Status = Status{Level: StatusError, Text: "Neovim exited with an error: " + finished.err.Error()}
		return nil
	}
	data, err := os.ReadFile(finished.path)
	if err != nil {
		m.core.Status = Status{Level: StatusError, Text: "Could not read task editor buffer: " + err.Error()}
		return nil
	}
	if pending != nil && bytes.Equal(data, pending.original) {
		m.core.Status = Status{Level: StatusInfo, Text: "Task edit discarded"}
		return nil
	}
	row, ok := m.core.selectedTask()
	if !ok {
		m.core.Status = Status{Level: StatusError, Text: "Selected task is no longer available"}
		return nil
	}
	task, err := ParseTaskDocumentWithOptions(string(data), row.Task, m.taskEditorOptions(row.Task))
	if err != nil {
		m.core.Status = Status{Level: StatusError, Text: "Invalid task document: " + err.Error()}
		return nil
	}
	if taskDocumentEqual(task, row.Task) {
		m.core.Status = Status{Level: StatusInfo, Text: "Task edit discarded"}
		return nil
	}
	command := AppCommand{
		Kind:          CommandUpdateTask,
		ProviderID:    task.ProviderID,
		ListID:        task.ListID,
		TaskID:        task.ID,
		Title:         task.Title,
		Description:   task.Description,
		Assignee:      task.Assignee,
		Status:        task.Status,
		Priority:      task.Priority,
		DueAt:         cloneTime(task.DueAt),
		ClearDueAt:    task.DueAt == nil,
		EditAllFields: true,
	}
	m.core.Status = Status{Level: StatusInfo, Text: "Task update requested locally; sync is asynchronous"}
	return m.dispatch(m.core.emit(command))
}

func (m *CharmModel) taskEditorOptions(task Task) TaskEditorOptions {
	for _, options := range m.core.Data.EditorOptions {
		if options.ListID == task.ListID && options.ProviderID == task.ProviderID {
			return options
		}
	}
	return TaskEditorOptions{}
}

func (m *CharmModel) createTaskEditorScript(task Task) (string, error) {
	options := m.taskEditorOptions(task)
	values := append([]string{}, options.Statuses...)
	values = append(values, "urgent", "high", "normal", "low")
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+strings.ReplaceAll(strings.ReplaceAll(value, "'", "''"), "\\", "\\\\")+"'")
	}
	content := "let g:task_tui_complete_values = [" + strings.Join(quoted, ",") + "]\n" +
		"function! TaskTuiComplete(findstart, base) abort\n" +
		"  if a:findstart\n" +
		"    let line = getline('.')\n" +
		"    let start = col('.') - 1\n" +
		"    while start > 0 && line[start - 1] =~ '\\S'\n" +
		"      let start -= 1\n" +
		"    endwhile\n" +
		"    return start\n" +
		"  endif\n" +
		`  return filter(copy(g:task_tui_complete_values), 'v:val =~? "^" . escape(a:base, "\\.*$^~[]")')` + "\n" +
		"endfunction\n" +
		"autocmd BufReadPost,BufNewFile * setlocal omnifunc=TaskTuiComplete\n"
	file, err := os.CreateTemp("", "task-tui-*.vim")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// View implements tea.Model and uses Lip Gloss for the full-window layout.
// Bubbles' viewport clips the composed body so a small terminal remains
// usable instead of forcing the renderer beyond the available height.
func (m *CharmModel) View() string {
	if m == nil {
		return ""
	}
	width := m.core.UI.Width
	if width < 1 {
		width = 100
	}
	height := m.core.UI.Height
	if height < 1 {
		height = 24
	}

	headerText := "TASK MANAGER  /  local-first cache  /  sync " + string(m.core.overallSync())
	header := charmHeaderStyle.Width(maxInt(width-2, 1)).Render(
		fitAtOffset(headerText, maxInt(width-4, 1), 0),
	)
	modeLine := m.charmModeLine(width)
	completionLine := m.charmCommandCompletionLine(width)
	statusLine := m.charmStatusLine(width)
	footer := m.charmFooter(width)

	fixedHeight := 2
	if modeLine != "" {
		fixedHeight++
	}
	if completionLine != "" {
		fixedHeight++
	}
	if statusLine != "" {
		fixedHeight++
	}
	if footer != "" {
		fixedHeight++
	}
	bodyHeight := height - fixedHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	body := m.charmBody(width, bodyHeight)
	lines := make([]string, 0, height)
	lines = append(lines, header, charmMutedStyle.Render(strings.Repeat("-", maxInt(width, 1))))
	if modeLine != "" {
		lines = append(lines, modeLine)
	}
	if completionLine != "" {
		lines = append(lines, completionLine)
	}
	lines = append(lines, body)
	if statusLine != "" {
		lines = append(lines, statusLine)
	}
	if footer != "" {
		lines = append(lines, footer)
	}
	return strings.Join(lines, "\n")
}

func (m *CharmModel) dispatch(cmd Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	message := cmd()
	commandMessage, ok := message.(CommandMsg)
	if !ok {
		return nil
	}
	commands := make([]tea.Cmd, 0, 2)
	if m.options.OnCommand != nil {
		if command := m.options.OnCommand(commandMessage.Command); command != nil {
			commands = append(commands, command)
		}
	}
	if commandMessage.Command.Kind == CommandQuit {
		commands = append(commands, tea.Quit)
	}
	return tea.Batch(commands...)
}

func (m *CharmModel) inputMode() bool {
	if m == nil {
		return false
	}
	switch m.core.UI.Mode {
	case ModeSearch, ModeFilter, ModeCommand, ModeCreateSpace, ModeCreateList, ModeCreateTask, ModeEditTask:
		return true
	default:
		return false
	}
}

func (m *CharmModel) syncInput() {
	if m == nil {
		return
	}
	if !m.inputMode() {
		m.input.Blur()
		return
	}
	m.input.Width = maxInt(m.core.UI.Width-18, 12)
	m.input.SetValue(m.core.UI.Input)
	m.input.SetCursor(clamp(m.core.UI.InputCursor, 0, runeCount(m.core.UI.Input)))
	_ = m.input.Focus()
}

func charmKeyMessage(msg tea.KeyMsg) KeyMsg {
	switch msg.Type {
	case tea.KeyRunes:
		return KeyMsg{Runes: append([]rune(nil), msg.Runes...), Text: string(msg.Runes)}
	case tea.KeyEnter:
		return KeyMsg{Key: "enter"}
	case tea.KeyEscape:
		return KeyMsg{Key: "esc"}
	case tea.KeyBackspace, tea.KeyCtrlH:
		return KeyMsg{Key: "backspace"}
	case tea.KeyDelete:
		return KeyMsg{Key: "delete"}
	case tea.KeyUp:
		return KeyMsg{Key: "up"}
	case tea.KeyDown:
		return KeyMsg{Key: "down"}
	case tea.KeyLeft:
		return KeyMsg{Key: "left"}
	case tea.KeyRight:
		return KeyMsg{Key: "right"}
	case tea.KeyTab:
		return KeyMsg{Key: "tab"}
	case tea.KeyShiftTab:
		return KeyMsg{Key: "shift+tab"}
	case tea.KeyHome:
		return KeyMsg{Key: "home"}
	case tea.KeyEnd:
		return KeyMsg{Key: "end"}
	case tea.KeyCtrlC:
		return KeyMsg{Key: "ctrl+c"}
	default:
		return KeyMsg{Key: msg.String(), Runes: append([]rune(nil), msg.Runes...), Text: string(msg.Runes)}
	}
}

func (m *CharmModel) charmBody(width, height int) string {
	if m.core.UI.Mode == ModeDetail || m.core.UI.Mode == ModeColumnConfig {
		lines := m.core.visibleModeLines(width, height)
		return m.clipBody(strings.Join(lines, "\n"), width, height)
	}
	if width < 72 {
		treeHeight := height / 2
		if treeHeight < 3 {
			treeHeight = 3
		}
		if treeHeight >= height {
			treeHeight = maxInt(height-1, 1)
		}
		tasksHeight := maxInt(height-treeHeight, 1)
		body := lipgloss.JoinVertical(
			lipgloss.Left,
			m.charmPanel(strings.Join(m.charmTreeLines(maxInt(width-4, 1)), "\n"), width, treeHeight, PanelHierarchy),
			m.charmPanel(strings.Join(m.charmTaskLines(maxInt(width-4, 1)), "\n"), width, tasksHeight, PanelTasks),
		)
		return m.clipBody(body, width, height)
	}

	leftWidth := width * 32 / 100
	if leftWidth < 26 {
		leftWidth = 26
	}
	if leftWidth > width-30 {
		leftWidth = width - 30
	}
	rightWidth := maxInt(width-leftWidth-2, 1)
	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.charmPanel(strings.Join(m.charmTreeLines(maxInt(leftWidth-4, 1)), "\n"), leftWidth, height, PanelHierarchy),
		m.charmPanel(strings.Join(m.charmTaskLines(maxInt(rightWidth-4, 1)), "\n"), rightWidth, height, PanelTasks),
	)
	return m.clipBody(body, width, height)
}

func (m *CharmModel) clipBody(content string, width, height int) string {
	viewportModel := m.viewport
	viewportModel.Width = maxInt(width, 1)
	viewportModel.Height = maxInt(height, 1)
	viewportModel.SetContent(content)
	viewportModel.SetYOffset(0)
	return viewportModel.View()
}

func (m *CharmModel) charmPanel(content string, width, height int, panel Panel) string {
	active := m.core.UI.Focus == panel
	border := lipgloss.RoundedBorder()
	color := charmMuted
	if active {
		color = charmAccent
	}
	style := lipgloss.NewStyle().
		Border(border).
		BorderForeground(color).
		Padding(0, 1)
	borderWidth := style.GetHorizontalBorderSize()
	borderHeight := style.GetVerticalBorderSize()
	contentHeight := maxInt(height-borderHeight, 1)
	content = clipLines(content, contentHeight)
	return style.
		Width(maxInt(width-borderWidth, 1)).
		Height(contentHeight).
		Render(content)
}

func clipLines(content string, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (m *CharmModel) charmTreeLines(width int) []string {
	lines := []string{charmSectionStyle.Render(fitAtOffset("SPACES / LISTS", width, m.core.UI.TreeHorizontalOffset))}
	nodes := m.core.TreeNodes()
	if len(nodes) == 0 {
		return append(lines, charmMutedStyle.Render(fitAtOffset("(no cached hierarchy)", width, m.core.UI.TreeHorizontalOffset)))
	}
	offset := clamp(m.core.UI.TreeOffset, 0, len(nodes)-1)
	if offset > 0 {
		lines = append(lines, charmMutedStyle.Render("  ..."))
	}
	for _, node := range nodes[offset:] {
		marker := "  "
		if node.Ref == m.core.UI.SelectedNode && m.core.UI.Focus == PanelHierarchy {
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
		line := fmt.Sprintf("%s%s%s %s %s", marker, strings.Repeat("  ", node.Depth), kind, expansion, safeText(node.Name))
		line = fitAtOffset(line, width, m.core.UI.TreeHorizontalOffset)
		if node.Ref == m.core.UI.SelectedNode && m.core.UI.Focus == PanelHierarchy {
			lines = append(lines, charmSelectedStyle.Render(line))
		} else {
			lines = append(lines, line)
		}
	}
	return lines
}

func (m *CharmModel) charmTaskLines(width int) []string {
	lines := []string{
		charmSectionStyle.Render(fitAtOffset(m.core.taskHeading(), width, m.core.UI.TaskHorizontalOffset)),
		charmTableHeaderStyle.Render(fitAtOffset(m.core.taskTableHeaderLine(width), width, m.core.UI.TaskHorizontalOffset)),
	}
	groups := m.core.VisibleTaskGroups()
	rows := flattenTaskGroups(groups)
	if len(rows) == 0 && (m.core.UI.GroupBy == TaskGroupNone || len(groups) == 0) {
		if m.core.UI.SearchActive {
			return append(lines, charmMutedStyle.Render(fitAtOffset("(no local search results)", width, m.core.UI.TaskHorizontalOffset)))
		}
		return append(lines, charmMutedStyle.Render(fitAtOffset("(no tasks in this view)", width, m.core.UI.TaskHorizontalOffset)))
	}
	offset := 0
	if m.core.UI.GroupBy == TaskGroupNone {
		if len(rows) > 0 {
			offset = clamp(m.core.UI.TaskOffset, 0, len(rows)-1)
		}
	} else {
		offset = clamp(m.core.UI.TaskOffset, 0, maxInt(taskGroupVisualLength(groups)-1, 0))
	}
	if offset > 0 {
		lines = append(lines, charmMutedStyle.Render("  ..."))
	}
	appendRow := func(row TaskRow, index int) {
		marker := "  "
		selected := index == m.core.UI.TaskCursor && m.core.UI.Focus == PanelTasks
		if selected {
			marker = "> "
		}
		lines = append(lines, m.charmTaskRowLine(row, marker, width, selected))
	}
	if m.core.UI.GroupBy == TaskGroupNone {
		for index, row := range rows[offset:] {
			appendRow(row, index+offset)
		}
		return lines
	}

	visualIndex := 0
	rowIndex := 0
	for _, group := range groups {
		if visualIndex >= offset {
			heading := fitAtOffset(m.core.taskGroupLine(group), width, m.core.UI.TaskHorizontalOffset)
			if m.core.taskGroupSelected(group) {
				lines = append(lines, charmSelectedStyle.Render(heading))
			} else {
				lines = append(lines, charmMutedStyle.Render(heading))
			}
		}
		visualIndex++
		if group.Collapsed {
			continue
		}
		for _, row := range group.Rows {
			if visualIndex >= offset {
				appendRow(row, rowIndex)
			}
			visualIndex++
			rowIndex++
		}
	}
	return lines
}

func (m *CharmModel) charmTaskRowLine(row TaskRow, marker string, width int, selected bool) string {
	columns := m.core.taskTableColumns(width)
	fullLine := m.core.taskTableLine(row, marker, width)
	offset := clamp(m.core.UI.TaskHorizontalOffset, 0, runeCount(fullLine))
	line := fitAtOffset(fullLine, width, offset)
	line = colorTaskStatusCell(line, row.Task.Status, offset, marker, columns)
	if selected {
		return charmSelectedTaskStyle.Render(line)
	}
	return line
}

func colorTaskStatusCell(line, status string, offset int, marker string, columns []taskTableColumn) string {
	if line == "" {
		return line
	}
	statusStart := runeCount(marker)
	statusEnd := statusStart
	statusFound := false
	for index, column := range columns {
		if index > 0 {
			statusStart += runeCount(taskTableGap)
		}
		if column.ID == taskColumnStatus {
			statusEnd = statusStart + column.Width
			statusFound = true
			break
		}
		statusStart += column.Width
	}
	if !statusFound {
		return line
	}
	start := statusStart - offset
	end := statusEnd - offset
	visible := []rune(line)
	if end <= 0 || start >= len(visible) {
		return line
	}
	start = clamp(start, 0, len(visible))
	end = clamp(end, start, len(visible))
	if start == end {
		return line
	}
	styled := taskStatusStyle(status).Render(string(visible[start:end]))
	return string(visible[:start]) + styled + string(visible[end:])
}

func taskStatusStyle(status string) lipgloss.Style {
	switch normalize(status) {
	case "done", "complete", "completed", "closed":
		return charmGoodStyle
	case "blocked", "cancelled", "canceled":
		return charmErrorStyle
	case "in progress", "in_progress", "doing":
		return charmWarnStyle
	default:
		return charmAccentStyle
	}
}

func (m *CharmModel) charmModeLine(width int) string {
	if m.core.UI.Mode == ModeColumnConfig {
		return fit("COLUMNS: j/k select | space toggle | +/- or h/l resize | enter/esc close", width)
	}
	if m.core.UI.Mode == ModeConfirm {
		return charmWarnStyle.Render(fit("CONFIRM: "+safeText(m.core.UI.ConfirmPrompt)+"  [y/enter] yes  [n/esc] no", width))
	}
	prefix := ""
	suffix := " enter apply | esc cancel"
	switch m.core.UI.Mode {
	case ModeSearch:
		prefix = "SEARCH"
	case ModeFilter:
		prefix = "FILTER"
	case ModeCommand:
		prefix = "COMMAND"
		suffix = " tab/shift+tab complete | enter run | esc cancel"
	case ModeCreateTask:
		prefix = "NEW TASK"
		suffix = " enter submit | esc cancel"
	case ModeCreateSpace:
		prefix = "NEW SPACE"
		suffix = " enter submit | esc cancel"
	case ModeCreateList:
		prefix = "NEW LIST"
		suffix = " enter submit | esc cancel"
	case ModeEditTask:
		prefix = "EDIT TASK " + editFieldLabel(m.core.UI.EditField)
		if m.core.UI.EditAllFields {
			suffix = " tab/enter next | final enter save | esc cancel"
		} else {
			suffix = " enter submit | esc cancel"
		}
	default:
		return ""
	}
	line := prefix + ": " + m.input.View() + suffix
	return fit(line, width)
}

func (m *CharmModel) charmCommandCompletionLine(width int) string {
	if m.core.UI.Mode != ModeCommand {
		return ""
	}
	_, candidates, _, _ := m.core.commandCompletion()
	if len(m.core.UI.CommandCompletion) > 0 {
		candidates = m.core.UI.CommandCompletion
	}
	if len(candidates) == 0 {
		return ""
	}
	start, end := completionWindow(candidates, m.core.UI.CommandCompletionIndex, width)
	parts := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		candidate := candidates[index]
		if index == m.core.UI.CommandCompletionIndex {
			parts = append(parts, charmSelectedStyle.Render(candidate))
		} else {
			parts = append(parts, charmMutedStyle.Render(candidate))
		}
	}
	return "options: " + strings.Join(parts, "  ")
}

func (m *CharmModel) charmStatusLine(width int) string {
	if strings.TrimSpace(m.core.Status.Text) == "" {
		return ""
	}
	level := strings.ToUpper(string(m.core.Status.Level))
	if level == "" {
		level = "INFO"
	}
	prefix := "STATUS " + level + ": "
	if m.refreshing {
		prefix = refreshFrames[m.refreshFrame] + " " + prefix
	}
	line := fit(prefix+safeText(m.core.Status.Text), width)
	switch m.core.Status.Level {
	case StatusError:
		return charmErrorStyle.Render(line)
	case StatusWarning:
		return charmWarnStyle.Render(line)
	case StatusSuccess:
		return charmGoodStyle.Render(line)
	default:
		return charmMutedStyle.Render(line)
	}
}

func (m *CharmModel) charmFooter(width int) string {
	helpModel := m.help
	helpModel.Width = width
	helpKeys := m.helpKeys
	if m.core.UI.Mode == ModeDetail {
		helpKeys = newCharmDetailHelpKeyMap()
	} else if m.core.UI.Mode == ModeColumnConfig {
		helpKeys = newCharmColumnHelpKeyMap()
	}
	return fit(helpModel.View(helpKeys), width)
}

type charmHelpKeyMap struct {
	short []key.Binding
	full  [][]key.Binding
}

func newCharmHelpKeyMap() charmHelpKeyMap {
	bind := func(keys []string, helpKey, description string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, description))
	}
	short := []key.Binding{
		bind([]string{"j", "k", "up", "down"}, "j/k", "move"),
		bind([]string{"tab", "shift+tab"}, "tab", "panel"),
		bind([]string{"h", "l", "left", "right"}, "h/l", "scroll"),
		bind([]string{"enter"}, "enter", "open/toggle"),
		bind([]string{"space"}, "space", "group"),
		bind([]string{"+", "="}, "+", "expand all"),
		bind([]string{"-"}, "-", "collapse all"),
		bind([]string{"n", "e", "x", "d"}, "n/e/x/d", "task"),
		bind([]string{"/"}, "/", "search"),
		bind([]string{"f"}, "f", "filter"),
		bind([]string{"c"}, "c", "columns"),
		bind([]string{"q", "ctrl+c"}, "q", "quit"),
	}
	full := [][]key.Binding{
		short,
		{
			bind([]string{"g"}, "g", "first"),
			bind([]string{"G"}, "G", "last"),
			bind([]string{"r"}, "r", "refresh"),
			bind([]string{":"}, ":", "commands"),
		},
	}
	return charmHelpKeyMap{short: short, full: full}
}

func newCharmDetailHelpKeyMap() charmHelpKeyMap {
	bind := func(keys []string, helpKey, description string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, description))
	}
	short := []key.Binding{
		bind([]string{"j", "k", "up", "down"}, "j/k", "scroll"),
		bind([]string{"g", "G"}, "g/G", "top/bottom"),
		bind([]string{"e"}, "e", "edit"),
		bind([]string{"esc"}, "esc", "close"),
		bind([]string{"q", "ctrl+c"}, "q", "quit"),
	}
	return charmHelpKeyMap{short: short, full: [][]key.Binding{short}}
}

func newCharmColumnHelpKeyMap() charmHelpKeyMap {
	bind := func(keys []string, helpKey, description string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, description))
	}
	short := []key.Binding{
		bind([]string{"j", "k", "up", "down"}, "j/k", "select"),
		bind([]string{"space"}, "space", "show/hide"),
		bind([]string{"+", "=", "-", "h", "l", "left", "right"}, "+/-", "resize"),
		bind([]string{"enter", "esc"}, "enter", "close"),
	}
	return charmHelpKeyMap{short: short, full: [][]key.Binding{short}}
}

func (k charmHelpKeyMap) ShortHelp() []key.Binding { return k.short }

func (k charmHelpKeyMap) FullHelp() [][]key.Binding { return k.full }
