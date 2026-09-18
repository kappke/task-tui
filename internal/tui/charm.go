package tui

import (
	"fmt"
	"strings"

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

	input    textinput.Model
	help     help.Model
	helpKeys charmHelpKeyMap
	viewport viewport.Model
}

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
	charmMutedStyle = lipgloss.NewStyle().Foreground(charmMuted)
	charmGoodStyle  = lipgloss.NewStyle().Foreground(charmGood)
	charmWarnStyle  = lipgloss.NewStyle().Foreground(charmWarn)
	charmErrorStyle = lipgloss.NewStyle().Foreground(charmError)
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
	m.syncInput()

	commands := make([]tea.Cmd, 0, 2)
	if inputCmd != nil {
		commands = append(commands, inputCmd)
	}
	if command := m.dispatch(coreCmd); command != nil {
		commands = append(commands, command)
	}
	if m.core.UI.Quitting {
		commands = append(commands, tea.Quit)
	}
	return m, tea.Batch(commands...)
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
	statusLine := m.charmStatusLine(width)
	footer := m.charmFooter(width)

	fixedHeight := 2
	if modeLine != "" {
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
	case ModeSearch, ModeFilter, ModeCommand, ModeCreateTask, ModeEditTask:
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
	if m.core.UI.Mode == ModeDetail {
		lines := m.core.visibleDetailLines(width, height)
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
	lines := []string{charmSectionStyle.Render(fitAtOffset(m.core.taskHeading(), width, m.core.UI.TaskHorizontalOffset))}
	groups := m.core.VisibleTaskGroups()
	rows := flattenTaskGroups(groups)
	if len(rows) == 0 && (m.core.UI.GroupBy == TaskGroupNone || len(groups) == 0) {
		if m.core.UI.SearchActive {
			return append(lines, charmMutedStyle.Render(fitAtOffset("(no local search results)", width, m.core.UI.TaskHorizontalOffset)))
		}
		return append(lines, charmMutedStyle.Render(fitAtOffset("(no tasks in this view)", width, m.core.UI.TaskHorizontalOffset)))
	}
	offset := 0
	if len(rows) > 0 {
		offset = clamp(m.core.UI.TaskOffset, 0, len(rows)-1)
	}
	if offset > 0 {
		lines = append(lines, charmMutedStyle.Render("  ..."))
	}
	appendRow := func(row TaskRow, index int) {
		selected := index == m.core.UI.TaskCursor && m.core.UI.Focus == PanelTasks
		marker := "  "
		if selected {
			marker = "> "
		}
		line := fitAtOffset(taskLineText(row, marker), width, m.core.UI.TaskHorizontalOffset)
		if selected {
			lines = append(lines, charmSelectedStyle.Render(line))
		} else {
			lines = append(lines, line)
		}
	}
	if m.core.UI.GroupBy == TaskGroupNone {
		for index, row := range rows[offset:] {
			appendRow(row, index+offset)
		}
		return lines
	}

	rowIndex := 0
	for _, group := range groups {
		if group.Collapsed {
			heading := fitAtOffset(m.core.taskGroupLine(group), width, m.core.UI.TaskHorizontalOffset)
			if m.core.taskGroupSelected(group) {
				lines = append(lines, charmSelectedStyle.Render(heading))
			} else {
				lines = append(lines, charmMutedStyle.Render(heading))
			}
			continue
		}
		groupStart := rowIndex
		groupEnd := groupStart + len(group.Rows)
		rowIndex = groupEnd
		if groupEnd <= offset {
			continue
		}
		heading := fitAtOffset(m.core.taskGroupLine(group), width, m.core.UI.TaskHorizontalOffset)
		lines = append(lines, charmMutedStyle.Render(heading))
		start := maxInt(offset-groupStart, 0)
		for index, row := range group.Rows[start:] {
			appendRow(row, groupStart+start+index)
		}
	}
	return lines
}

func (m *CharmModel) charmModeLine(width int) string {
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
		suffix = " enter run | esc cancel"
	case ModeCreateTask:
		prefix = "NEW TASK"
		suffix = " enter submit | esc cancel"
	case ModeEditTask:
		prefix = "EDIT TASK"
		suffix = " enter submit | esc cancel"
	default:
		return ""
	}
	line := prefix + ": " + m.input.View() + suffix
	return fit(line, width)
}

func (m *CharmModel) charmStatusLine(width int) string {
	if strings.TrimSpace(m.core.Status.Text) == "" {
		return ""
	}
	level := strings.ToUpper(string(m.core.Status.Level))
	if level == "" {
		level = "INFO"
	}
	line := fit("STATUS "+level+": "+safeText(m.core.Status.Text), width)
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
		bind([]string{"n", "e", "x", "d"}, "n/e/x/d", "task"),
		bind([]string{"/"}, "/", "search"),
		bind([]string{"f"}, "f", "filter"),
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
		bind([]string{"esc"}, "esc", "close"),
		bind([]string{"q", "ctrl+c"}, "q", "quit"),
	}
	return charmHelpKeyMap{short: short, full: [][]key.Binding{short}}
}

func (k charmHelpKeyMap) ShortHelp() []key.Binding { return k.short }

func (k charmHelpKeyMap) FullHelp() [][]key.Binding { return k.full }
