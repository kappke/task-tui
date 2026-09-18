package tui

import (
	"fmt"
	"strings"
	"unicode"
)

const horizontalScrollStep = 4

// Update applies one local message and optionally emits one application
// command. It performs no provider or persistence work.
func (m Model) Update(msg Message) (Model, Cmd) {
	m.ensureUI()
	switch value := msg.(type) {
	case KeyMsg:
		return m.updateKey(value)
	case string:
		return m.updateKey(KeyMsg{Key: value})
	case rune:
		return m.updateKey(KeyMsg{Runes: []rune{value}})
	case Key:
		return m.updateKey(KeyMsg{Key: string(value)})
	case WindowSizeMsg:
		m.UI.Width = value.Width
		m.UI.Height = value.Height
		if m.UI.Width < 1 {
			m.UI.Width = 1
		}
		if m.UI.Height < 1 {
			m.UI.Height = 1
		}
		m.keepVisible()
		return m, nil
	case SnapshotMsg:
		return m.applySnapshot(value.Data), nil
	case TasksLoadedMsg:
		return m.applyTasksLoaded(value), nil
	case SyncStateMsg:
		return m.applySyncState(value), nil
	case ErrorMsg:
		return m.applyError(value), nil
	case StatusMsg:
		level := value.Level
		if level == "" {
			level = StatusInfo
		}
		m.Status = Status{Level: level, Text: value.Text}
		return m, nil
	case CommandResultMsg:
		return m.applyCommandResult(value), nil
	case QuitMsg:
		m.UI.Quitting = true
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) ensureUI() {
	if m.UI.Focus == "" {
		m.UI.Focus = PanelHierarchy
	}
	if m.UI.Mode == "" {
		m.UI.Mode = ModeBrowse
	}
	if m.UI.Width == 0 {
		m.UI.Width = 100
	}
	if m.UI.Height == 0 {
		m.UI.Height = 24
	}
	if m.UI.ExpandedNodes == nil {
		m.UI.ExpandedNodes = make(map[TreeNodeRef]bool)
	}
	if len(m.KeyMap.Bindings) == 0 {
		m.KeyMap = DefaultKeyMap()
	}
}

func (m *Model) initializeSelection() {
	m.UI.ExpandedNodes = cloneExpanded(m.UI.ExpandedNodes)
	for _, provider := range m.viewProviders() {
		providerRef := TreeNodeRef{Kind: TreeNodeProvider, ProviderID: provider.ID}
		if _, ok := m.UI.ExpandedNodes[providerRef]; !ok {
			m.UI.ExpandedNodes[providerRef] = true
		}
		for _, space := range m.Data.Spaces {
			if space.ProviderID != provider.ID {
				continue
			}
			spaceRef := TreeNodeRef{
				Kind:       TreeNodeSpace,
				ProviderID: provider.ID,
				SpaceID:    space.ID,
			}
			if _, ok := m.UI.ExpandedNodes[spaceRef]; !ok {
				m.UI.ExpandedNodes[spaceRef] = true
			}
		}
	}

	nodes := m.TreeNodes()
	if len(nodes) == 0 {
		m.UI.SelectedNode = TreeNodeRef{}
		m.UI.TreeCursor = 0
		m.UI.SelectedTask = TaskRef{}
		m.UI.TaskCursor = 0
		m.keepVisible()
		return
	}
	index := firstListIndex(nodes)
	if index < 0 {
		index = 0
	}
	m.UI.TreeCursor = index
	m.UI.SelectedNode = nodes[index].Ref
	m.selectTaskAt(0)
	m.keepVisible()
}

func firstListIndex(nodes []TreeNode) int {
	for index, node := range nodes {
		if node.Ref.Kind == TreeNodeList {
			return index
		}
	}
	return -1
}

func (m *Model) applySnapshot(data Snapshot) Model {
	oldNode := m.UI.SelectedNode
	oldTask := m.UI.SelectedTask
	m.Data = cloneSnapshot(data)
	m.UI.ExpandedNodes = cloneExpanded(m.UI.ExpandedNodes)
	m.initializeExpansion()
	m.reconcileSelection(oldNode, oldTask)
	m.Status = Status{
		Level: StatusInfo,
		Text:  fmt.Sprintf("Loaded cached data: %d providers, %d tasks", len(m.Data.Providers), len(m.Data.Tasks)),
	}
	return *m
}

func (m *Model) initializeExpansion() {
	for _, provider := range m.viewProviders() {
		providerRef := TreeNodeRef{Kind: TreeNodeProvider, ProviderID: provider.ID}
		if _, ok := m.UI.ExpandedNodes[providerRef]; !ok {
			m.UI.ExpandedNodes[providerRef] = true
		}
		for _, space := range m.Data.Spaces {
			if space.ProviderID != provider.ID {
				continue
			}
			spaceRef := TreeNodeRef{Kind: TreeNodeSpace, ProviderID: provider.ID, SpaceID: space.ID}
			if _, ok := m.UI.ExpandedNodes[spaceRef]; !ok {
				m.UI.ExpandedNodes[spaceRef] = true
			}
		}
	}
}

func (m *Model) reconcileSelection(oldNode TreeNodeRef, oldTask TaskRef) {
	nodes := m.TreeNodes()
	if len(nodes) == 0 {
		m.UI.SelectedNode = TreeNodeRef{}
		m.UI.TreeCursor = 0
		m.UI.SelectedTask = TaskRef{}
		m.UI.TaskCursor = 0
		return
	}

	index := findNode(nodes, oldNode)
	if index < 0 {
		if m.UI.TreeCursor < 0 {
			m.UI.TreeCursor = 0
		}
		if m.UI.TreeCursor >= len(nodes) {
			m.UI.TreeCursor = len(nodes) - 1
		}
		index = m.UI.TreeCursor
	}
	m.UI.TreeCursor = index
	m.UI.SelectedNode = nodes[index].Ref

	rows := m.VisibleTasks()
	if taskIndex := findTask(rows, oldTask); taskIndex >= 0 {
		m.UI.TaskCursor = taskIndex
		m.UI.SelectedTask = taskRef(rows[taskIndex])
		m.keepVisible()
		return
	}
	m.selectTaskAt(m.UI.TaskCursor)
	m.keepVisible()
}

func (m Model) applyTasksLoaded(event TasksLoadedMsg) Model {
	if event.ProviderID == "" && (event.ListID != "" || event.Replace) {
		m.Status = Status{Level: StatusError, Text: "Task update requires provider identity for a scoped or replacement load"}
		return m
	}
	oldNode := m.UI.SelectedNode
	oldTask := m.UI.SelectedTask
	data := cloneSnapshot(m.Data)
	acceptedTasks := make([]Task, 0, len(event.Tasks))
	rejected := 0
	for _, incoming := range event.Tasks {
		if incoming.ProviderID == "" || (event.ProviderID != "" && incoming.ProviderID != event.ProviderID) {
			rejected++
			continue
		}
		if event.ListID != "" && incoming.ListID != event.ListID {
			rejected++
			continue
		}
		acceptedTasks = append(acceptedTasks, cloneTask(incoming))
	}
	if event.Replace && rejected > 0 {
		m.Status = Status{Level: StatusError, Text: fmt.Sprintf("Ignored replacement load with %d task(s) missing matching provider/list identity", rejected)}
		return m
	}
	if event.Replace {
		kept := make([]Task, 0, len(data.Tasks)+len(acceptedTasks))
		for _, task := range data.Tasks {
			if taskInEventScope(task, event) {
				continue
			}
			kept = append(kept, task)
		}
		data.Tasks = kept
	}
	for _, incoming := range acceptedTasks {
		updated := false
		for index := range data.Tasks {
			if sameTask(data.Tasks[index], incoming) {
				data.Tasks[index] = incoming
				updated = true
				break
			}
		}
		if !updated {
			data.Tasks = append(data.Tasks, incoming)
		}
	}
	m.Data = data
	m.reconcileSelection(oldNode, oldTask)
	if rejected > 0 {
		m.Status = Status{Level: StatusError, Text: fmt.Sprintf("Ignored %d task(s) without matching provider/list identity", rejected)}
	} else {
		m.Status = Status{Level: StatusInfo, Text: fmt.Sprintf("Loaded %d task(s)", len(acceptedTasks))}
	}
	return m
}

func taskInEventScope(task Task, event TasksLoadedMsg) bool {
	if event.ProviderID != "" && task.ProviderID != event.ProviderID {
		return false
	}
	return event.ListID == "" || task.ListID == event.ListID
}

func sameTask(left, right Task) bool {
	return left.ProviderID == right.ProviderID && left.ID == right.ID
}

func (m Model) applySyncState(event SyncStateMsg) Model {
	if event.ProviderID == "" {
		m.Status = Status{Level: StatusError, Text: "Sync update requires provider identity"}
		return m
	}
	m.Data = cloneSnapshot(m.Data)
	state := stateOr(event.State, SyncStateUnknown)
	entityType := normalize(string(event.EntityType))
	switch entityType {
	case "", "provider":
		for index := range m.Data.Providers {
			if m.Data.Providers[index].ID != event.ProviderID {
				continue
			}
			m.Data.Providers[index].SyncState = state
			m.Data.Providers[index].SyncError = event.Error
			m.Data.Providers[index].LastSyncAt = cloneTime(event.LastSyncAt)
		}
	case "space":
		for index := range m.Data.Spaces {
			if m.Data.Spaces[index].ProviderID == event.ProviderID && string(m.Data.Spaces[index].ID) == event.EntityID {
				m.Data.Spaces[index].SyncState = state
			}
		}
	case "list":
		for index := range m.Data.Lists {
			if m.Data.Lists[index].ProviderID == event.ProviderID && string(m.Data.Lists[index].ID) == event.EntityID {
				m.Data.Lists[index].SyncState = state
			}
		}
	case "task":
		for index := range m.Data.Tasks {
			if m.Data.Tasks[index].ProviderID == event.ProviderID && string(m.Data.Tasks[index].ID) == event.EntityID {
				m.Data.Tasks[index].SyncState = state
			}
		}
	default:
		m.Status = Status{Level: StatusError, Text: "Unknown sync entity " + string(event.EntityType)}
		return m
	}

	level := StatusInfo
	text := fmt.Sprintf("%s sync: %s", providerLabel(m, event.ProviderID), string(state))
	if event.Error != "" || state == SyncStateFailed || state == SyncStateConflict {
		level = StatusError
		if event.Error != "" {
			text += " - " + event.Error
		}
	}
	m.Status = Status{Level: level, Text: text}
	return m
}

func (m Model) applyError(event ErrorMsg) Model {
	text := strings.TrimSpace(event.Text)
	if text == "" && event.Err != nil {
		text = event.Err.Error()
	}
	if text == "" {
		text = "operation failed"
	}
	m.Status = Status{Level: StatusError, Text: text}
	return m
}

func (m Model) applyCommandResult(event CommandResultMsg) Model {
	if event.Err != nil {
		return m.applyError(ErrorMsg{Err: event.Err, Text: event.Text})
	}
	text := event.Text
	if text == "" {
		text = commandResultText(event.Command)
	}
	m.Status = Status{Level: StatusSuccess, Text: text}
	return m
}

func commandResultText(command AppCommand) string {
	switch command.Kind {
	case CommandCreateTask:
		return "Task created"
	case CommandUpdateTask:
		return "Task updated"
	case CommandCompleteTask:
		return "Task completion updated"
	case CommandDeleteTask:
		return "Task deleted"
	case CommandRefresh:
		return "Refresh completed"
	default:
		return "Command completed"
	}
}

func providerLabel(m Model, providerID ProviderID) string {
	for _, provider := range m.Data.Providers {
		if provider.ID == providerID {
			return displayProviderName(provider) + " (" + string(providerID) + ")"
		}
	}
	return string(providerID)
}

func (m Model) updateKey(key KeyMsg) (Model, Cmd) {
	if m.UI.Quitting {
		return m, nil
	}
	keyName := key.name()
	if keyName == "ctrl+c" {
		m.UI.Quitting = true
		m.Status = Status{Level: StatusInfo, Text: "Quit requested"}
		return m, m.emit(AppCommand{Kind: CommandQuit})
	}
	if m.UI.Mode != ModeBrowse {
		return m.updateInput(key)
	}
	return m.updateBrowse(key, m.KeyMap.Action(keyName))
}

func (m Model) updateBrowse(key KeyMsg, action Action) (Model, Cmd) {
	if action == ActionNone && key.name() == "" {
		return m, nil
	}
	switch action {
	case ActionMoveUp:
		m.moveCursor(-1)
	case ActionMoveDown:
		m.moveCursor(1)
	case ActionFirst:
		m.moveCursorToEdge(false)
	case ActionLast:
		m.moveCursorToEdge(true)
	case ActionPreviousPanel:
		m.previousPanel()
	case ActionNextPanel:
		m.nextPanel()
	case ActionScrollLeft:
		m.scrollHorizontal(-horizontalScrollStep)
	case ActionScrollRight:
		m.scrollHorizontal(horizontalScrollStep)
	case ActionSelect:
		m.selectCurrent()
	case ActionQuit:
		m.UI.Quitting = true
		m.Status = Status{Level: StatusInfo, Text: "Quit requested"}
		return m, m.emit(AppCommand{Kind: CommandQuit})
	case ActionCreate:
		if !m.beginCreate() {
			return m, nil
		}
	case ActionEdit:
		if !m.beginEdit() {
			return m, nil
		}
	case ActionComplete:
		command, ok := m.taskCommand(CommandCompleteTask)
		if !ok {
			m.Status = Status{Level: StatusWarning, Text: "Select a task first"}
			return m, nil
		}
		row, _ := m.selectedTask()
		command.Completed = !isTaskComplete(row.Task)
		command.Status = row.Task.Status
		m.Status = Status{Level: StatusInfo, Text: "Completing task locally; sync is asynchronous"}
		return m, m.emit(command)
	case ActionDelete:
		command, ok := m.taskCommand(CommandDeleteTask)
		if !ok {
			m.Status = Status{Level: StatusWarning, Text: "Select a task first"}
			return m, nil
		}
		row, _ := m.selectedTask()
		m.UI.PendingCommand = command
		m.UI.HasPending = true
		m.UI.Mode = ModeConfirm
		m.UI.ConfirmPrompt = fmt.Sprintf("Delete %q from %s?", row.Task.Title, taskProviderLabel(row))
		m.Status = Status{Level: StatusWarning, Text: "Confirm deletion: y/enter yes, n/esc no"}
	case ActionSearch:
		m.beginSearch()
	case ActionFilter:
		m.beginFilter()
	case ActionRefresh:
		command := AppCommand{Kind: CommandRefresh, ProviderID: m.UI.SelectedNode.ProviderID}
		m.Status = Status{Level: StatusInfo, Text: "Refresh requested; local data remains available"}
		return m, m.emit(command)
	case ActionCommand:
		m.beginCommand()
	case ActionCancel:
		m.cancelBrowseView()
	}
	m.keepVisible()
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if m.UI.Focus == PanelHierarchy {
		nodes := m.TreeNodes()
		if len(nodes) == 0 {
			m.Status = Status{Level: StatusWarning, Text: "No cached hierarchy"}
			return
		}
		cursor := clamp(m.UI.TreeCursor+delta, 0, len(nodes)-1)
		m.UI.TreeCursor = cursor
		m.UI.SelectedNode = nodes[cursor].Ref
		m.UI.SelectedTask = TaskRef{}
		m.UI.TaskCursor = 0
		m.selectTaskAt(0)
	} else {
		rows := m.VisibleTasks()
		if len(rows) == 0 {
			m.Status = Status{Level: StatusWarning, Text: "No tasks in this view"}
			return
		}
		cursor := clamp(m.UI.TaskCursor+delta, 0, len(rows)-1)
		m.UI.TaskCursor = cursor
		m.UI.SelectedTask = taskRef(rows[cursor])
	}
}

func (m *Model) moveCursorToEdge(last bool) {
	if m.UI.Focus == PanelHierarchy {
		nodes := m.TreeNodes()
		if len(nodes) == 0 {
			m.Status = Status{Level: StatusWarning, Text: "No cached hierarchy"}
			return
		}
		if last {
			m.UI.TreeCursor = len(nodes) - 1
		} else {
			m.UI.TreeCursor = 0
		}
		m.UI.SelectedNode = nodes[m.UI.TreeCursor].Ref
		m.UI.SelectedTask = TaskRef{}
		m.UI.TaskCursor = 0
		m.selectTaskAt(0)
		return
	}
	rows := m.VisibleTasks()
	if len(rows) == 0 {
		m.Status = Status{Level: StatusWarning, Text: "No tasks in this view"}
		return
	}
	if last {
		m.UI.TaskCursor = len(rows) - 1
	} else {
		m.UI.TaskCursor = 0
	}
	m.UI.SelectedTask = taskRef(rows[m.UI.TaskCursor])
}

func (m *Model) previousPanel() {
	if m.UI.Focus == PanelTasks {
		m.UI.Focus = PanelHierarchy
		m.Status = Status{Level: StatusInfo, Text: "Focus: hierarchy"}
		return
	}
	m.UI.Focus = PanelTasks
	m.Status = Status{Level: StatusInfo, Text: "Focus: tasks"}
}

func (m *Model) nextPanel() {
	if m.UI.Focus == PanelHierarchy {
		m.UI.Focus = PanelTasks
		m.Status = Status{Level: StatusInfo, Text: "Focus: tasks"}
		return
	}
	m.UI.Focus = PanelHierarchy
	m.Status = Status{Level: StatusInfo, Text: "Focus: hierarchy"}
}

func (m *Model) selectCurrent() {
	if m.UI.Focus == PanelTasks {
		if _, ok := m.selectedTask(); !ok {
			m.Status = Status{Level: StatusWarning, Text: "No task selected"}
		}
		return
	}
	node, ok := m.currentNode()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "No hierarchy item selected"}
		return
	}
	if node.Ref.Kind == TreeNodeList {
		m.UI.Focus = PanelTasks
		m.Status = Status{Level: StatusInfo, Text: "Opened " + node.Name}
		return
	}
	m.UI.ExpandedNodes = cloneExpanded(m.UI.ExpandedNodes)
	m.UI.ExpandedNodes[node.Ref] = !node.Expanded
	m.Status = Status{Level: StatusInfo, Text: expansionText(node, !node.Expanded)}
}

func expansionText(node TreeNode, expanded bool) string {
	if expanded {
		return "Expanded " + node.Name
	}
	return "Collapsed " + node.Name
}

func (m Model) currentNode() (TreeNode, bool) {
	nodes := m.TreeNodes()
	if m.UI.TreeCursor < 0 || m.UI.TreeCursor >= len(nodes) {
		return TreeNode{}, false
	}
	return nodes[m.UI.TreeCursor], true
}

func (m *Model) beginCreate() bool {
	list, ok := m.selectedList()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a list before creating a task"}
		return false
	}
	m.UI.Mode = ModeCreateTask
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.InputOrigin = ""
	m.Status = Status{Level: StatusInfo, Text: "Create task in " + list.Name + "; enter title, then press enter"}
	return true
}

func (m *Model) beginEdit() bool {
	row, ok := m.selectedTask()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a task before editing"}
		return false
	}
	m.UI.Mode = ModeEditTask
	m.UI.Input = row.Task.Title
	m.UI.InputCursor = runeCount(m.UI.Input)
	m.UI.InputOrigin = m.UI.Input
	m.Status = Status{Level: StatusInfo, Text: "Edit task title; press enter to submit"}
	return true
}

func (m *Model) beginSearch() {
	m.UI.Mode = ModeSearch
	m.UI.Input = m.UI.SearchQuery
	m.UI.InputCursor = runeCount(m.UI.Input)
	m.UI.InputOrigin = m.UI.SearchQuery
	m.UI.InputOriginSearchActive = m.UI.SearchActive
	m.UI.SearchActive = true
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.Status = Status{Level: StatusInfo, Text: "Search local tasks; type a query and press enter"}
}

func (m *Model) beginFilter() {
	m.UI.Mode = ModeFilter
	m.UI.Input = m.UI.Filter.String()
	m.UI.InputCursor = runeCount(m.UI.Input)
	m.UI.InputOrigin = m.UI.Input
	m.Status = Status{Level: StatusInfo, Text: "Filter local tasks; e.g. status:open priority:high"}
}

func (m *Model) beginCommand() {
	m.UI.Mode = ModeCommand
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.InputOrigin = ""
	m.Status = Status{Level: StatusInfo, Text: "Command palette: create, edit, complete, delete, search, filter, refresh"}
}

func (m Model) updateInput(key KeyMsg) (Model, Cmd) {
	keyName := key.name()
	if keyName == "ctrl+c" {
		m.UI.Quitting = true
		return m, m.emit(AppCommand{Kind: CommandQuit})
	}
	if m.UI.Mode == ModeConfirm {
		switch strings.ToLower(keyName) {
		case "y", "yes", "enter":
			return m.confirmInput()
		case "n", "no", "esc", "escape":
			m.cancelInput()
			return m, nil
		default:
			return m, nil
		}
	}
	switch keyName {
	case "esc", "escape":
		m.cancelInput()
		return m, nil
	case "enter":
		return m.submitInput()
	case "backspace", "ctrl+h":
		m.deleteInputRune()
		m.liveSearch()
		return m, nil
	case "left", "shift+left":
		if m.UI.InputCursor > 0 {
			m.UI.InputCursor--
		}
		return m, nil
	case "right", "shift+right":
		if m.UI.InputCursor < runeCount(m.UI.Input) {
			m.UI.InputCursor++
		}
		return m, nil
	case "home":
		m.UI.InputCursor = 0
		return m, nil
	case "end":
		m.UI.InputCursor = runeCount(m.UI.Input)
		return m, nil
	}

	text := key.text()
	if text == "" {
		return m, nil
	}
	inserted := false
	for _, char := range text {
		if unicode.IsPrint(char) || char == '\t' {
			m.insertInputRune(char)
			inserted = true
		}
	}
	if inserted {
		m.liveSearch()
	}
	return m, nil
}

func (m *Model) liveSearch() {
	if m.UI.Mode != ModeSearch {
		return
	}
	m.UI.SearchQuery = m.UI.Input
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
}

func (m *Model) insertInputRune(char rune) {
	runes := []rune(m.UI.Input)
	cursor := clamp(m.UI.InputCursor, 0, len(runes))
	runes = append(runes, 0)
	copy(runes[cursor+1:], runes[cursor:])
	runes[cursor] = char
	m.UI.Input = string(runes)
	m.UI.InputCursor = cursor + 1
}

func (m *Model) deleteInputRune() {
	runes := []rune(m.UI.Input)
	cursor := clamp(m.UI.InputCursor, 0, len(runes))
	if cursor == 0 {
		return
	}
	runes = append(runes[:cursor-1], runes[cursor:]...)
	m.UI.Input = string(runes)
	m.UI.InputCursor = cursor - 1
}

func (m *Model) cancelInput() {
	if m.UI.Mode == ModeSearch {
		m.UI.SearchQuery = m.UI.InputOrigin
		m.UI.SearchActive = m.UI.InputOriginSearchActive
	}
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.InputOrigin = ""
	m.UI.InputOriginSearchActive = false
	m.UI.HasPending = false
	m.UI.PendingCommand = AppCommand{}
	m.UI.ConfirmPrompt = ""
	m.UI.SearchQuery = strings.TrimSpace(m.UI.SearchQuery)
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
	m.Status = Status{Level: StatusInfo, Text: "Input cancelled"}
}

func (m Model) submitInput() (Model, Cmd) {
	switch m.UI.Mode {
	case ModeSearch:
		if strings.TrimSpace(m.UI.Input) == "" {
			m.Status = Status{Level: StatusError, Text: "Search query cannot be empty"}
			return m, nil
		}
		return m.applySearch(m.UI.Input, AppCommand{Kind: CommandSearch, Query: m.UI.Input})
	case ModeFilter:
		filter, err := ParseFilter(m.UI.Input)
		if err != nil {
			m.Status = Status{Level: StatusError, Text: err.Error()}
			return m, nil
		}
		return m.applyFilter(filter, AppCommand{Kind: CommandFilter, Filter: filter})
	case ModeCommand:
		return m.submitPalette()
	case ModeCreateTask:
		return m.submitCreate()
	case ModeEditTask:
		return m.submitEdit()
	case ModeConfirm:
		return m.confirmInput()
	default:
		return m, nil
	}
}

func (m Model) applySearch(query string, command AppCommand) (Model, Cmd) {
	query = strings.TrimSpace(query)
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.SearchQuery = query
	m.UI.SearchActive = true
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
	m.Status = Status{Level: StatusInfo, Text: fmt.Sprintf("Local search: %d result(s)", len(m.VisibleTasks()))}
	command.Query = query
	return m, m.emit(command)
}

func (m Model) applyFilter(filter Filter, command AppCommand) (Model, Cmd) {
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.Filter = filter
	m.UI.FilterActive = filter.String() != ""
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
	m.Status = Status{Level: StatusInfo, Text: fmt.Sprintf("Local filter: %d result(s)", len(m.VisibleTasks()))}
	command.Filter = filter
	return m, m.emit(command)
}

func (m Model) submitPalette() (Model, Cmd) {
	command, err := ParseCommand(m.UI.Input)
	if err != nil {
		m.Status = Status{Level: StatusError, Text: err.Error()}
		return m, nil
	}
	switch command.Kind {
	case CommandSearch:
		return m.applySearch(command.Query, command)
	case CommandFilter:
		return m.applyFilter(command.Filter, command)
	case CommandCreateTask:
		if command.Title == "" {
			m.UI.Mode = ModeCreateTask
			m.UI.Input = ""
			m.UI.InputCursor = 0
			m.Status = Status{Level: StatusInfo, Text: "Enter a title for the new task"}
			return m, nil
		}
		return m.submitCreateTitle(command.Title, command)
	case CommandUpdateTask:
		if command.Title == "" {
			if !m.beginEdit() {
				return m, nil
			}
			return m, nil
		}
		return m.submitEditTitle(command.Title, command)
	case CommandCompleteTask:
		return m.submitTaskCommand(command, false)
	case CommandDeleteTask:
		return m.submitTaskCommand(command, true)
	case CommandRefresh:
		command.ProviderID = m.UI.SelectedNode.ProviderID
		m.UI.Mode = ModeBrowse
		m.UI.Input = ""
		m.Status = Status{Level: StatusInfo, Text: "Refresh requested; local data remains available"}
		return m, m.emit(command)
	case CommandQuit:
		m.UI.Quitting = true
		m.UI.Mode = ModeBrowse
		return m, m.emit(command)
	case CommandHelp:
		m.UI.Mode = ModeBrowse
		m.UI.Input = ""
		m.Status = Status{Level: StatusInfo, Text: "Keys: j/k move, tab switch panel, h/l scroll, enter open, n/e/x/d, / search, f filter, r refresh"}
		return m, nil
	default:
		m.Status = Status{Level: StatusError, Text: "Unsupported command"}
		return m, nil
	}
}

func (m Model) submitCreate() (Model, Cmd) {
	return m.submitCreateTitle(strings.TrimSpace(m.UI.Input), AppCommand{Kind: CommandCreateTask, Title: strings.TrimSpace(m.UI.Input)})
}

func (m Model) submitCreateTitle(title string, command AppCommand) (Model, Cmd) {
	if strings.TrimSpace(title) == "" {
		m.Status = Status{Level: StatusError, Text: "Task title cannot be empty"}
		return m, nil
	}
	list, ok := m.selectedList()
	if !ok {
		m.UI.Mode = ModeBrowse
		m.Status = Status{Level: StatusError, Text: "Select a list before creating a task"}
		return m, nil
	}
	command.Title = strings.TrimSpace(title)
	command.ProviderID = list.ProviderID
	command.SpaceID = list.SpaceID
	command.ListID = list.ID
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.Status = Status{Level: StatusInfo, Text: "Create requested locally; sync is asynchronous"}
	return m, m.emit(command)
}

func (m Model) submitEdit() (Model, Cmd) {
	return m.submitEditTitle(strings.TrimSpace(m.UI.Input), AppCommand{Kind: CommandUpdateTask})
}

func (m Model) submitEditTitle(title string, command AppCommand) (Model, Cmd) {
	if strings.TrimSpace(title) == "" {
		m.Status = Status{Level: StatusError, Text: "Task title cannot be empty"}
		return m, nil
	}
	row, ok := m.selectedTask()
	if !ok {
		m.UI.Mode = ModeBrowse
		m.Status = Status{Level: StatusError, Text: "Select a task before editing"}
		return m, nil
	}
	command.Title = strings.TrimSpace(title)
	command.ProviderID = row.Task.ProviderID
	command.ListID = row.Task.ListID
	command.TaskID = row.Task.ID
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.Status = Status{Level: StatusInfo, Text: "Edit requested locally; sync is asynchronous"}
	return m, m.emit(command)
}

func (m Model) submitTaskCommand(command AppCommand, confirmDelete bool) (Model, Cmd) {
	row, ok := m.selectedTask()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a task first"}
		return m, nil
	}
	command.ProviderID = row.Task.ProviderID
	command.ListID = row.Task.ListID
	command.TaskID = row.Task.ID
	if command.Kind == CommandCompleteTask {
		command.Completed = !isTaskComplete(row.Task)
		command.Status = row.Task.Status
	}
	if confirmDelete {
		m.UI.PendingCommand = command
		m.UI.HasPending = true
		m.UI.Mode = ModeConfirm
		m.UI.ConfirmPrompt = fmt.Sprintf("Delete %q from %s?", row.Task.Title, taskProviderLabel(row))
		m.Status = Status{Level: StatusWarning, Text: "Confirm deletion: y/enter yes, n/esc no"}
		return m, nil
	}
	m.UI.Mode = ModeBrowse
	m.Status = Status{Level: StatusInfo, Text: "Completing task locally; sync is asynchronous"}
	return m, m.emit(command)
}

func (m Model) confirmInput() (Model, Cmd) {
	if !m.UI.HasPending {
		m.UI.Mode = ModeBrowse
		return m, nil
	}
	command := m.UI.PendingCommand
	m.UI.Mode = ModeBrowse
	m.UI.HasPending = false
	m.UI.ConfirmPrompt = ""
	m.UI.Input = ""
	m.UI.PendingCommand = AppCommand{}
	m.Status = Status{Level: StatusInfo, Text: "Delete requested locally; sync is asynchronous"}
	return m, m.emit(command)
}

func (m *Model) cancelBrowseView() {
	if m.UI.SearchActive {
		m.UI.SearchActive = false
		m.UI.SearchQuery = ""
		m.UI.TaskCursor = 0
		m.UI.SelectedTask = TaskRef{}
		m.selectTaskAt(0)
		m.Status = Status{Level: StatusInfo, Text: "Search cleared"}
		return
	}
	if m.UI.FilterActive {
		m.UI.FilterActive = false
		m.UI.Filter = Filter{}
		m.UI.TaskCursor = 0
		m.UI.SelectedTask = TaskRef{}
		m.selectTaskAt(0)
		m.Status = Status{Level: StatusInfo, Text: "Filter cleared"}
	}
}

func (m Model) taskCommand(kind CommandKind) (AppCommand, bool) {
	row, ok := m.selectedTask()
	if !ok {
		return AppCommand{}, false
	}
	return AppCommand{
		Kind:       kind,
		ProviderID: row.Task.ProviderID,
		ListID:     row.Task.ListID,
		TaskID:     row.Task.ID,
	}, true
}

func (m Model) selectedList() (List, bool) {
	ref := m.UI.SelectedNode
	if ref.Kind != TreeNodeList {
		return List{}, false
	}
	for _, list := range m.Data.Lists {
		if list.ProviderID == ref.ProviderID && list.ID == ref.ListID && list.SpaceID == ref.SpaceID {
			return list, true
		}
	}
	return List{}, false
}

func taskProviderLabel(row TaskRow) string {
	name := row.ProviderName
	if name == "" {
		name = string(row.ProviderID)
	}
	if name == "" {
		name = "unknown provider"
	}
	if row.ProviderID == "" {
		return name
	}
	return name + " (" + string(row.ProviderID) + ")"
}

func (m Model) selectedTask() (TaskRow, bool) {
	rows := m.VisibleTasks()
	if len(rows) == 0 {
		return TaskRow{}, false
	}
	if m.UI.SelectedTask != (TaskRef{}) {
		if index := findTask(rows, m.UI.SelectedTask); index >= 0 {
			return rows[index], true
		}
	}
	if m.UI.TaskCursor < 0 || m.UI.TaskCursor >= len(rows) {
		return TaskRow{}, false
	}
	return rows[m.UI.TaskCursor], true
}

func (m *Model) selectTaskAt(cursor int) {
	rows := m.VisibleTasks()
	if len(rows) == 0 {
		m.UI.TaskCursor = 0
		m.UI.SelectedTask = TaskRef{}
		return
	}
	cursor = clamp(cursor, 0, len(rows)-1)
	m.UI.TaskCursor = cursor
	m.UI.SelectedTask = taskRef(rows[cursor])
}

func taskRef(row TaskRow) TaskRef {
	return TaskRef{ProviderID: row.Task.ProviderID, TaskID: row.Task.ID}
}

func findTask(rows []TaskRow, wanted TaskRef) int {
	if wanted == (TaskRef{}) {
		return -1
	}
	for index, row := range rows {
		if taskRef(row) == wanted {
			return index
		}
	}
	return -1
}

func findNode(nodes []TreeNode, wanted TreeNodeRef) int {
	if wanted == (TreeNodeRef{}) {
		return -1
	}
	for index, node := range nodes {
		if node.Ref == wanted {
			return index
		}
	}
	return -1
}

func (m *Model) keepVisible() {
	treeViewport, taskViewport := m.panelViewports()
	m.UI.TreeOffset = keepCursorVisible(m.UI.TreeCursor, m.UI.TreeOffset, treeViewport)
	m.UI.TaskOffset = keepCursorVisible(m.UI.TaskCursor, m.UI.TaskOffset, taskViewport)
	m.UI.TreeHorizontalOffset = clamp(m.UI.TreeHorizontalOffset, 0, m.maxHorizontalOffset(PanelHierarchy))
	m.UI.TaskHorizontalOffset = clamp(m.UI.TaskHorizontalOffset, 0, m.maxHorizontalOffset(PanelTasks))
}

func (m *Model) scrollHorizontal(delta int) {
	if delta == 0 {
		return
	}
	if m.UI.Focus == PanelHierarchy {
		m.UI.TreeHorizontalOffset = clamp(
			m.UI.TreeHorizontalOffset+delta,
			0,
			m.maxHorizontalOffset(PanelHierarchy),
		)
		return
	}
	m.UI.TaskHorizontalOffset = clamp(
		m.UI.TaskHorizontalOffset+delta,
		0,
		m.maxHorizontalOffset(PanelTasks),
	)
}

func keepCursorVisible(cursor, offset, viewport int) int {
	if viewport < 1 {
		viewport = 1
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+viewport {
		offset = cursor - viewport + 1
	}
	if offset < 0 {
		return 0
	}
	return offset
}

func (m Model) panelViewports() (tree, tasks int) {
	width := m.UI.Width
	if width < 1 {
		width = 100
	}
	height := m.UI.Height
	if height < 1 {
		height = 24
	}

	// Reserve the full chrome so an offset remains valid when a status or
	// input line appears after navigation.
	bodyHeight := height - 5
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	if width >= 72 {
		visible := panelItemViewport(bodyHeight)
		return visible, visible
	}

	treeHeight := bodyHeight / 2
	if treeHeight < 3 {
		treeHeight = 3
	}
	if treeHeight >= bodyHeight {
		treeHeight = maxInt(bodyHeight-1, 1)
	}
	taskHeight := maxInt(bodyHeight-treeHeight, 1)
	return panelItemViewport(treeHeight), panelItemViewport(taskHeight)
}

func panelItemViewport(panelHeight int) int {
	// Leave room for the panel border, heading, and the scroll marker.
	return maxInt(panelHeight-4, 1)
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func runeCount(value string) int {
	return len([]rune(value))
}
