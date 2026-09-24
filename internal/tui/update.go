package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

const horizontalScrollStep = 4

// Update applies one local message and optionally emits one application
// command. It performs no provider or persistence work.
func (m Model) Update(msg Message) (Model, Cmd) {
	m.ensureUI()
	previousList, hadPreviousList := m.selectedListViewKey()
	if hadPreviousList {
		m.rememberListView(previousList, m.currentListViewState())
	}

	updated, command := m.updateMessage(msg)
	updated.ensureUI()
	currentList, hasCurrentList := updated.selectedListViewKey()
	if hasCurrentList && (!hadPreviousList || currentList != previousList) {
		if state, ok := updated.UI.ListViews[currentList]; ok {
			updated.restoreListViewState(state)
		} else {
			updated.restoreListViewState(ListViewState{})
		}
	} else if hadPreviousList && !hasCurrentList {
		updated.UI.ColumnPreferences = nil
		updated.UI.ColumnCursor = 0
	}
	if hasCurrentList {
		updated.rememberListView(currentList, updated.currentListViewState())
	}
	return updated, command
}

func (m Model) updateMessage(msg Message) (Model, Cmd) {
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
	if m.UI.CollapsedGroups == nil {
		m.UI.CollapsedGroups = make(map[string]bool)
	}
	if m.UI.ListViews == nil {
		m.UI.ListViews = make(map[ListViewKey]ListViewState)
	}
	if len(m.KeyMap.Bindings) == 0 {
		m.KeyMap = DefaultKeyMap()
	}
}

func (m Model) selectedListViewKey() (ListViewKey, bool) {
	ref := m.UI.SelectedNode
	if ref.Kind != TreeNodeList || ref.ProviderID == "" || ref.ListID == "" {
		return ListViewKey{}, false
	}
	return ListViewKey{ProviderID: ref.ProviderID, ListID: ref.ListID}, true
}

func (m Model) currentListViewState() ListViewState {
	state := ListViewState{Sort: sortCriteriaString(m.UI.SortBy), GroupBy: m.UI.GroupBy, Columns: cloneColumnPreferences(m.UI.ColumnPreferences)}
	if m.UI.FilterActive {
		state.Filter = m.UI.Filter.String()
	}
	return state
}

func (m *Model) rememberListView(key ListViewKey, state ListViewState) {
	if current, ok := m.UI.ListViews[key]; ok && sameListViewState(current, state) {
		return
	}
	views := make(map[ListViewKey]ListViewState, len(m.UI.ListViews)+1)
	for currentKey, currentState := range m.UI.ListViews {
		currentState.Columns = cloneColumnPreferences(currentState.Columns)
		views[currentKey] = currentState
	}
	state.Columns = cloneColumnPreferences(state.Columns)
	views[key] = state
	m.UI.ListViews = views
}

func sameListViewState(left, right ListViewState) bool {
	if left.Filter != right.Filter || left.Sort != right.Sort || left.GroupBy != right.GroupBy || len(left.Columns) != len(right.Columns) {
		return false
	}
	for index := range left.Columns {
		if left.Columns[index] != right.Columns[index] {
			return false
		}
	}
	return true
}

func (m *Model) restoreListViewState(state ListViewState) {
	m.UI.ColumnPreferences = cloneColumnPreferences(state.Columns)
	m.UI.ColumnCursor = 0
	m.UI.Filter = Filter{}
	m.UI.FilterActive = false
	if strings.TrimSpace(state.Filter) != "" {
		if filter, err := ParseFilter(state.Filter); err == nil {
			m.UI.Filter = filter
			m.UI.FilterActive = filter.String() != ""
		}
	}
	m.UI.SortBy = nil
	if strings.TrimSpace(state.Sort) != "" {
		if criteria, err := ParseSort(state.Sort); err == nil {
			if criteria, err = m.normalizeSortCriteria(criteria); err == nil {
				m.UI.SortBy = criteria
			}
		}
	}
	m.UI.GroupBy = TaskGroupNone
	if group, err := ParseTaskGroupMode(string(state.GroupBy)); err == nil {
		m.UI.GroupBy = group
	}
	m.UI.FocusedGroup = ""
	m.UI.TaskGroupCursor = 0
	m.UI.TaskHeaderSelected = false
	m.UI.TaskHeaderTask = TaskRef{}
	m.UI.TaskOffset = 0
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
}

func (m *Model) initializeSelection() {
	m.ensureActiveProvider()
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

func (m *Model) ensureActiveProvider() {
	providers := m.allProviders()
	for _, provider := range providers {
		if provider.ID == m.UI.ActiveProviderID {
			return
		}
	}
	if len(providers) > 0 {
		m.UI.ActiveProviderID = providers[0].ID
		return
	}
	m.UI.ActiveProviderID = ""
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
	hadLists := len(m.Data.Lists) > 0
	m.Data = cloneSnapshot(data)
	m.ensureActiveProvider()
	m.UI.ExpandedNodes = cloneExpanded(m.UI.ExpandedNodes)
	m.initializeExpansion()
	m.reconcileSelection(oldNode, oldTask)
	if !hadLists && len(m.Data.Lists) > 0 && oldNode.Kind == TreeNodeProvider {
		for index, node := range m.TreeNodes() {
			if node.Ref.Kind == TreeNodeList && node.Ref.ProviderID == oldNode.ProviderID {
				m.UI.TreeCursor = index
				m.UI.SelectedNode = node.Ref
				m.selectTaskAt(0)
				break
			}
		}
	}
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
		m.selectTaskAt(taskIndex)
		m.keepVisible()
		return
	}
	if _, ok := m.taskRowForRef(oldTask); ok {
		m.UI.SelectedTask = oldTask
		groups := m.VisibleTaskGroups()
		for index, group := range groups {
			if findTask(group.Rows, oldTask) >= 0 {
				m.focusTaskGroupHeader(groups, index, oldTask)
				break
			}
		}
		m.keepVisible()
		return
	}
	if m.UI.Mode == ModeDetail {
		m.UI.Mode = ModeBrowse
		m.UI.DetailOffset = 0
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
		if event.ListID != "" && !taskHasList(incoming, event.ListID) {
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
	return event.ListID == "" || taskHasList(task, event.ListID)
}

func taskHasList(task Task, listID ListID) bool {
	if task.ListID == listID {
		return true
	}
	for _, membership := range task.ListIDs {
		if membership == listID {
			return true
		}
	}
	return false
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
	if event.Message != "" {
		text = providerLabel(m, event.ProviderID) + " " + event.Message
	}
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
	if event.Command.Kind == CommandRefresh {
		m.Status = Status{Level: StatusInfo, Text: "Refreshing ClickUp data..."}
		return m
	}
	if event.Command.Kind == CommandFetchLists || event.Command.Kind == CommandFetchTasks || event.Command.Kind == CommandFetchTask {
		m.Status = Status{Level: StatusInfo, Text: text}
		return m
	}
	m.Status = Status{Level: StatusSuccess, Text: text}
	return m
}

func commandResultText(command AppCommand) string {
	switch command.Kind {
	case CommandFetchLists:
		return "Refreshing lists..."
	case CommandFetchTasks:
		return "Refreshing tasks..."
	case CommandFetchTask:
		return "Refreshing task details..."
	case CommandCreateSpace:
		return "Space created"
	case CommandCreateList:
		return "List created"
	case CommandCreateTask:
		return "Task created"
	case CommandUpdateTask:
		return "Task updated"
	case CommandCompleteTask:
		return "Task completion updated"
	case CommandDeleteTask:
		return "Task deleted"
	case CommandGroup:
		if command.GroupBy == TaskGroupNone {
			return "Task grouping cleared"
		}
		return "Tasks grouped by " + string(command.GroupBy)
	case CommandRefresh:
		return "Refresh started"
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
	if m.UI.Mode == ModeColumnConfig {
		return m.updateColumnConfig(keyName)
	}
	if m.UI.Mode == ModeDetail {
		return m.updateDetail(key)
	}
	if m.UI.Mode != ModeBrowse {
		return m.updateInput(key)
	}
	return m.updateBrowse(key, m.KeyMap.Action(keyName))
}

func (m Model) updateDetail(key KeyMsg) (Model, Cmd) {
	switch m.KeyMap.Action(key.name()) {
	case ActionMoveUp:
		m.scrollDetail(-1)
	case ActionMoveDown:
		m.scrollDetail(1)
	case ActionFirst:
		m.UI.DetailOffset = 0
	case ActionLast:
		m.UI.DetailOffset = m.maxDetailOffset()
	case ActionCancel:
		m.closeDetail()
	case ActionRefresh:
		return m, m.refreshFocusedPane()
	case ActionEdit:
		m.beginDetailEdit()
	case ActionQuit:
		m.UI.Quitting = true
		m.Status = Status{Level: StatusInfo, Text: "Quit requested"}
		return m, m.emit(AppCommand{Kind: CommandQuit})
	}
	return m, nil
}

func (m Model) updateBrowse(key KeyMsg, action Action) (Model, Cmd) {
	if action == ActionNone && key.name() == "" {
		return m, nil
	}
	previousNode := m.UI.SelectedNode
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
		return m, m.nextPanel()
	case ActionScrollLeft:
		m.scrollHorizontal(-horizontalScrollStep)
	case ActionScrollRight:
		m.scrollHorizontal(horizontalScrollStep)
	case ActionSelect:
		return m, m.selectCurrent()
	case ActionExpandAll:
		m.setAllExpanded(true)
	case ActionCollapseAll:
		m.setAllExpanded(false)
	case ActionToggleGroup:
		m.toggleTaskGroup()
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
	case ActionSort:
		m.beginSort()
	case ActionConfigureColumns:
		m.beginColumnConfiguration()
	case ActionRefresh:
		return m, m.refreshFocusedPane()
	case ActionCommand:
		m.beginCommand()
	case ActionCancel:
		m.cancelBrowseView()
	}
	m.keepVisible()
	if m.UI.Focus == PanelHierarchy && previousNode != m.UI.SelectedNode {
		switch action {
		case ActionMoveUp, ActionMoveDown, ActionFirst, ActionLast:
			if command := m.loadSelectedList(); command != nil {
				return m, command
			}
		}
	}
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
		if m.UI.GroupBy != TaskGroupNone {
			m.moveGroupedCursor(delta)
			return
		}
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

func (m *Model) moveGroupedCursor(delta int) {
	groups := m.VisibleTaskGroups()
	if len(groups) == 0 {
		m.Status = Status{Level: StatusWarning, Text: "No tasks in this view"}
		return
	}

	groupIndex := m.currentTaskGroupIndex(groups)
	if groupIndex < 0 {
		m.focusTaskGroupHeader(groups, 0, TaskRef{})
		return
	}

	if m.UI.TaskHeaderSelected {
		if delta > 0 {
			if !groups[groupIndex].Collapsed && len(groups[groupIndex].Rows) > 0 {
				m.selectTaskAt(m.taskGroupRowStart(groups, groupIndex))
				return
			}
			if groupIndex+1 < len(groups) {
				m.focusTaskGroupHeader(groups, groupIndex+1, TaskRef{})
			}
			return
		}

		if groupIndex > 0 {
			previous := groupIndex - 1
			if !groups[previous].Collapsed && len(groups[previous].Rows) > 0 {
				m.selectTaskAt(m.taskGroupRowStart(groups, previous) + len(groups[previous].Rows) - 1)
				return
			}
			m.focusTaskGroupHeader(groups, previous, TaskRef{})
		}
		return
	}

	if groups[groupIndex].Collapsed {
		m.focusTaskGroupHeader(groups, groupIndex, m.UI.SelectedTask)
		return
	}
	localIndex := findTask(groups[groupIndex].Rows, m.UI.SelectedTask)
	if localIndex < 0 {
		m.focusTaskGroupHeader(groups, groupIndex, TaskRef{})
		return
	}
	if delta > 0 {
		if localIndex+1 < len(groups[groupIndex].Rows) {
			m.selectTaskAt(m.taskGroupRowStart(groups, groupIndex) + localIndex + 1)
			return
		}
		if groupIndex+1 < len(groups) {
			m.focusTaskGroupHeader(groups, groupIndex+1, TaskRef{})
		}
		return
	}
	if localIndex > 0 {
		m.selectTaskAt(m.taskGroupRowStart(groups, groupIndex) + localIndex - 1)
		return
	}
	m.focusTaskGroupHeader(groups, groupIndex, TaskRef{})
}

func (m Model) currentTaskGroupIndex(groups []TaskGroup) int {
	if m.UI.TaskHeaderSelected {
		for index, group := range groups {
			if taskGroupStateKey(m.UI.GroupBy, group.Key) == m.UI.FocusedGroup {
				return index
			}
		}
		if m.UI.TaskGroupCursor >= 0 && m.UI.TaskGroupCursor < len(groups) {
			return m.UI.TaskGroupCursor
		}
		return -1
	}
	if m.UI.SelectedTask == (TaskRef{}) {
		return -1
	}
	for index, group := range groups {
		for _, row := range group.Rows {
			if taskRef(row) == m.UI.SelectedTask {
				return index
			}
		}
	}
	return -1
}

func (m Model) taskGroupRowStart(groups []TaskGroup, target int) int {
	if len(groups) == 0 {
		return 0
	}
	if allTaskGroupsCollapsed(groups) {
		return clamp(target, 0, len(groups)-1)
	}
	start := 0
	for index, group := range groups {
		if index == target {
			return start
		}
		if !group.Collapsed {
			start += len(group.Rows)
		}
	}
	return start
}

func (m *Model) focusTaskGroupHeader(groups []TaskGroup, index int, preserve TaskRef) {
	if index < 0 || index >= len(groups) {
		return
	}
	m.UI.TaskGroupCursor = index
	m.UI.TaskHeaderSelected = true
	m.UI.TaskHeaderTask = preserve
	m.UI.FocusedGroup = taskGroupStateKey(m.UI.GroupBy, groups[index].Key)
	m.UI.TaskCursor = -1
	if preserve == (TaskRef{}) {
		m.UI.SelectedTask = TaskRef{}
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
	if m.UI.GroupBy != TaskGroupNone {
		groups := m.VisibleTaskGroups()
		if len(groups) == 0 {
			m.Status = Status{Level: StatusWarning, Text: "No tasks in this view"}
			return
		}
		if !last {
			m.focusTaskGroupHeader(groups, 0, TaskRef{})
			return
		}
		lastGroup := len(groups) - 1
		if groups[lastGroup].Collapsed || len(groups[lastGroup].Rows) == 0 {
			m.focusTaskGroupHeader(groups, lastGroup, TaskRef{})
			return
		}
		m.selectTaskAt(m.taskGroupRowStart(groups, lastGroup) + len(groups[lastGroup].Rows) - 1)
		return
	}
	rows := m.VisibleTasks()
	if len(rows) == 0 {
		m.Status = Status{Level: StatusWarning, Text: "No tasks in this view"}
		return
	}
	if last {
		m.selectTaskAt(len(rows) - 1)
		return
	}
	m.selectTaskAt(0)
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

func (m *Model) nextPanel() Cmd {
	if m.UI.Focus == PanelHierarchy {
		m.UI.Focus = PanelTasks
		m.Status = Status{Level: StatusInfo, Text: "Focus: tasks"}
		return m.loadSelectedList()
	}
	m.UI.Focus = PanelHierarchy
	m.Status = Status{Level: StatusInfo, Text: "Focus: hierarchy"}
	return nil
}

func (m *Model) loadSelectedList() Cmd {
	list, ok := m.selectedList()
	if !ok {
		return nil
	}
	return m.emit(AppCommand{
		Kind:       CommandLoadCached,
		ProviderID: list.ProviderID,
		SpaceID:    list.SpaceID,
		ListID:     list.ID,
	})
}

func (m *Model) refreshFocusedPane() Cmd {
	ref := m.UI.SelectedNode
	if m.UI.Focus == PanelHierarchy {
		if ref.ProviderID == "" {
			m.Status = Status{Level: StatusWarning, Text: "Select a provider before refreshing lists"}
			return nil
		}
		m.Status = Status{Level: StatusInfo, Text: "List refresh requested; cached data remains available"}
		return m.emit(AppCommand{
			Kind:       CommandFetchLists,
			ProviderID: ref.ProviderID,
			SpaceID:    ref.SpaceID,
		})
	}

	providerID := ref.ProviderID
	listID := ref.ListID
	if row, ok := m.selectedTask(); ok && listID == "" {
		providerID = row.Task.ProviderID
		listID = row.Task.ListID
	}
	if providerID == "" || listID == "" {
		m.Status = Status{Level: StatusWarning, Text: "Select a list before refreshing tasks"}
		return nil
	}
	m.Status = Status{Level: StatusInfo, Text: "Task refresh requested; cached data remains available"}
	return m.emit(AppCommand{Kind: CommandFetchTasks, ProviderID: providerID, ListID: listID})
}

func (m *Model) beginColumnConfiguration() {
	list, ok := m.selectedList()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a list before configuring columns"}
		return
	}
	m.UI.Mode = ModeColumnConfig
	m.UI.ColumnCursor = clamp(m.UI.ColumnCursor, 0, maxInt(len(m.availableTaskColumns())-1, 0))
	m.Status = Status{Level: StatusInfo, Text: "Configure task columns for " + list.Name}
}

func (m Model) updateColumnConfig(key string) (Model, Cmd) {
	columns := m.availableTaskColumns()
	if len(columns) == 0 {
		m.UI.Mode = ModeBrowse
		return m, nil
	}
	m.UI.ColumnCursor = clamp(m.UI.ColumnCursor, 0, len(columns)-1)
	column := columns[m.UI.ColumnCursor]
	switch key {
	case "j", "down":
		m.UI.ColumnCursor = clamp(m.UI.ColumnCursor+1, 0, len(columns)-1)
	case "k", "up":
		m.UI.ColumnCursor = clamp(m.UI.ColumnCursor-1, 0, len(columns)-1)
	case "space":
		if column.ID == taskColumnTask {
			m.Status = Status{Level: StatusWarning, Text: "The task title column is required"}
			return m, nil
		}
		preference, configured := m.taskColumnPreference(column.ID)
		if !configured {
			preference = TaskColumnPreference{ID: column.ID, Visible: true, Width: column.Width}
		}
		preference.Visible = !preference.Visible
		m.setTaskColumnPreference(preference)
		state := "Hidden"
		if preference.Visible {
			state = "Shown"
		}
		list, _ := m.selectedList()
		m.Status = Status{Level: StatusSuccess, Text: fmt.Sprintf("%s %s for %s", state, column.Label, list.Name)}
	case "+", "=", "right", "l":
		m.resizeTaskColumn(column, 1)
	case "-", "left", "h":
		m.resizeTaskColumn(column, -1)
	case "enter", "esc", "escape", "q":
		m.UI.Mode = ModeBrowse
		m.Status = Status{Level: StatusInfo, Text: "Column configuration closed"}
	}
	return m, nil
}

func (m *Model) resizeTaskColumn(column taskTableColumn, delta int) {
	preference, configured := m.taskColumnPreference(column.ID)
	if !configured {
		preference = TaskColumnPreference{ID: column.ID, Visible: true, Width: column.Width}
	}
	if preference.Width <= 0 {
		preference.Width = column.Width
	}
	preference.Width = clamp(preference.Width+delta, 1, 120)
	preference.Visible = true
	m.setTaskColumnPreference(preference)
	m.Status = Status{Level: StatusInfo, Text: fmt.Sprintf("%s width: %d", column.Label, preference.Width)}
}

func (m *Model) setTaskColumnPreference(preference TaskColumnPreference) {
	preferences := cloneColumnPreferences(m.UI.ColumnPreferences)
	for index := range preferences {
		if preferences[index].ID == preference.ID {
			preferences[index] = preference
			m.UI.ColumnPreferences = preferences
			return
		}
	}
	m.UI.ColumnPreferences = append(preferences, preference)
}

func (m *Model) selectCurrent() Cmd {
	if m.UI.Focus == PanelTasks {
		if m.UI.TaskHeaderSelected {
			m.toggleTaskGroup()
			return nil
		}
		row, ok := m.selectedTask()
		if !ok {
			m.Status = Status{Level: StatusWarning, Text: "No task selected"}
			return nil
		}
		m.UI.Mode = ModeDetail
		m.UI.DetailOffset = 0
		m.Status = Status{Level: StatusInfo, Text: "Opened task details: " + row.Task.Title}
		return m.emit(AppCommand{
			Kind:       CommandFetchTask,
			ProviderID: row.Task.ProviderID,
			ListID:     row.Task.ListID,
			TaskID:     row.Task.ID,
		})
	}
	node, ok := m.currentNode()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "No hierarchy item selected"}
		return nil
	}
	if node.Ref.Kind == TreeNodeList {
		m.UI.Focus = PanelTasks
		m.Status = Status{Level: StatusInfo, Text: "Opened " + node.Name}
		return m.emit(AppCommand{Kind: CommandLoadCached, ProviderID: node.Ref.ProviderID, SpaceID: node.Ref.SpaceID, ListID: node.Ref.ListID, FetchRemote: true})
	}
	expanded := !node.Expanded
	m.UI.ExpandedNodes = cloneExpanded(m.UI.ExpandedNodes)
	m.UI.ExpandedNodes[node.Ref] = expanded
	m.Status = Status{Level: StatusInfo, Text: expansionText(node, expanded)}
	if !expanded {
		return nil
	}
	return m.emit(AppCommand{Kind: CommandFetchLists, ProviderID: node.Ref.ProviderID, SpaceID: node.Ref.SpaceID})
}

func (m *Model) setAllExpanded(expanded bool) {
	if m.UI.Focus == PanelHierarchy {
		m.setAllHierarchyExpanded(expanded)
		return
	}
	m.setAllTaskGroupsExpanded(expanded)
}

func (m *Model) setAllHierarchyExpanded(expanded bool) {
	providers := m.viewProviders()
	if len(providers) == 0 {
		m.Status = Status{Level: StatusWarning, Text: "No cached hierarchy to expand or collapse"}
		return
	}

	oldNode := m.UI.SelectedNode
	m.UI.ExpandedNodes = cloneExpanded(m.UI.ExpandedNodes)
	for _, provider := range providers {
		providerRef := TreeNodeRef{Kind: TreeNodeProvider, ProviderID: provider.ID}
		m.UI.ExpandedNodes[providerRef] = expanded
		for _, space := range m.Data.Spaces {
			if space.ProviderID != provider.ID {
				continue
			}
			spaceRef := TreeNodeRef{
				Kind:       TreeNodeSpace,
				ProviderID: provider.ID,
				SpaceID:    space.ID,
			}
			m.UI.ExpandedNodes[spaceRef] = expanded
		}
	}

	nodes := m.TreeNodes()
	selectedIndex := findNode(nodes, oldNode)
	if selectedIndex < 0 {
		ancestor := TreeNodeRef{Kind: TreeNodeProvider, ProviderID: oldNode.ProviderID}
		selectedIndex = findNode(nodes, ancestor)
	}
	if selectedIndex < 0 {
		if len(nodes) == 0 {
			m.UI.SelectedNode = TreeNodeRef{}
			m.UI.TreeCursor = 0
		} else {
			selectedIndex = clamp(m.UI.TreeCursor, 0, len(nodes)-1)
		}
	}
	if selectedIndex >= 0 {
		m.UI.TreeCursor = selectedIndex
		m.UI.SelectedNode = nodes[selectedIndex].Ref
	}
	if oldNode != m.UI.SelectedNode {
		m.UI.SelectedTask = TaskRef{}
		m.UI.TaskCursor = 0
		m.selectTaskAt(0)
	}
	m.Status = Status{Level: StatusInfo, Text: hierarchyExpansionText(expanded)}
}

func hierarchyExpansionText(expanded bool) string {
	if expanded {
		return "Expanded all lists"
	}
	return "Collapsed all lists"
}

func (m *Model) setAllTaskGroupsExpanded(expanded bool) {
	if m.UI.GroupBy == TaskGroupNone {
		m.Status = Status{Level: StatusWarning, Text: "Enable task grouping before expanding or collapsing all tasks"}
		return
	}

	groups := m.VisibleTaskGroups()
	if len(groups) == 0 {
		m.Status = Status{Level: StatusWarning, Text: "No task groups to expand or collapse"}
		return
	}

	selected := m.UI.SelectedTask
	if m.UI.TaskHeaderSelected && m.UI.TaskHeaderTask != (TaskRef{}) {
		selected = m.UI.TaskHeaderTask
	}
	m.UI.CollapsedGroups = cloneCollapsed(m.UI.CollapsedGroups)
	for _, group := range groups {
		stateKey := taskGroupStateKey(m.UI.GroupBy, group.Key)
		if expanded {
			delete(m.UI.CollapsedGroups, stateKey)
		} else {
			m.UI.CollapsedGroups[stateKey] = true
		}
	}

	if expanded {
		m.UI.TaskHeaderSelected = false
		m.UI.TaskHeaderTask = TaskRef{}
		m.UI.FocusedGroup = ""
		if !m.selectTaskRef(selected) {
			m.selectTaskAt(0)
		}
		m.Status = Status{Level: StatusInfo, Text: "Expanded all task groups"}
		return
	}

	groups = m.VisibleTaskGroups()
	groupIndex := taskGroupIndexForRef(groups, selected)
	if groupIndex < 0 {
		groupIndex = clamp(m.UI.TaskGroupCursor, 0, len(groups)-1)
	}
	m.focusTaskGroupHeader(groups, groupIndex, selected)
	m.Status = Status{Level: StatusInfo, Text: "Collapsed all task groups"}
}

func taskGroupIndexForRef(groups []TaskGroup, wanted TaskRef) int {
	if wanted == (TaskRef{}) {
		return -1
	}
	for index, group := range groups {
		if findTask(group.Rows, wanted) >= 0 {
			return index
		}
	}
	return -1
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
	m.UI.EditAllFields = false
	m.Status = Status{Level: StatusInfo, Text: "Edit task title; press enter to submit"}
	return true
}

func (m *Model) beginDetailEdit() {
	row, ok := m.selectedTask()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a task before editing"}
		return
	}
	m.UI.Mode = ModeEditTask
	m.UI.EditTask = cloneTask(row.Task)
	m.UI.EditField = EditFieldTitle
	m.UI.EditAllFields = true
	m.setEditInput()
	m.Status = Status{Level: StatusInfo, Text: "Edit task fields; tab/enter moves between fields"}
}

func (m *Model) setEditInput() {
	switch m.UI.EditField {
	case EditFieldTitle:
		m.UI.Input = m.UI.EditTask.Title
	case EditFieldDescription:
		m.UI.Input = m.UI.EditTask.Description
	case EditFieldAssignee:
		m.UI.Input = m.UI.EditTask.Assignee
	case EditFieldStatus:
		m.UI.Input = m.UI.EditTask.Status
	case EditFieldPriority:
		m.UI.Input = string(m.UI.EditTask.Priority)
	case EditFieldDue:
		m.UI.Input = ""
		if m.UI.EditTask.DueAt != nil {
			m.UI.Input = m.UI.EditTask.DueAt.Format("2006-01-02")
		}
	}
	m.UI.InputCursor = runeCount(m.UI.Input)
}

func (m *Model) commitEditInput() {
	switch m.UI.EditField {
	case EditFieldTitle:
		m.UI.EditTask.Title = strings.TrimSpace(m.UI.Input)
	case EditFieldDescription:
		m.UI.EditTask.Description = m.UI.Input
	case EditFieldAssignee:
		m.UI.EditTask.Assignee = strings.TrimSpace(m.UI.Input)
	case EditFieldStatus:
		m.UI.EditTask.Status = strings.TrimSpace(m.UI.Input)
	case EditFieldPriority:
		m.UI.EditTask.Priority = Priority(strings.TrimSpace(m.UI.Input))
	case EditFieldDue:
		value := strings.TrimSpace(m.UI.Input)
		m.UI.EditTask.DueAt = nil
		if value != "" {
			if due, err := parseEditDue(value); err == nil {
				m.UI.EditTask.DueAt = &due
			}
		}
	}
}

func parseEditDue(value string) (time.Time, error) {
	for _, layout := range []string{time.DateOnly, time.RFC3339, "2006-01-02 15:04"} {
		if due, err := time.Parse(layout, value); err == nil {
			return due.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("due date must be YYYY-MM-DD or RFC3339")
}

func (m *Model) moveEditField(delta int) {
	m.commitEditInput()
	field := int(m.UI.EditField) + delta
	if field < int(EditFieldTitle) {
		field = int(EditFieldDue)
	}
	if field > int(EditFieldDue) {
		field = int(EditFieldTitle)
	}
	m.UI.EditField = EditField(field)
	m.setEditInput()
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
	m.resetCommandCompletion()
	m.UI.Mode = ModeFilter
	m.UI.Input = m.UI.Filter.String()
	m.UI.InputCursor = runeCount(m.UI.Input)
	m.UI.InputOrigin = m.UI.Input
	m.Status = Status{Level: StatusInfo, Text: `Filter columns with AND/OR/NOT; e.g. status:open AND (priority:high OR assignee:"Ada Lovelace")`}
}

func (m *Model) beginSort() {
	if _, ok := m.selectedList(); !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a list before sorting tasks"}
		return
	}
	m.resetCommandCompletion()
	m.UI.Mode = ModeSort
	m.UI.Input = sortCriteriaString(m.UI.SortBy)
	m.UI.InputCursor = runeCount(m.UI.Input)
	m.UI.InputOrigin = m.UI.Input
	m.Status = Status{Level: StatusInfo, Text: "Sort by ordered columns; e.g. priority desc, due asc"}
}

func (m *Model) beginCommand() {
	m.UI.Mode = ModeCommand
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.InputOrigin = ""
	m.resetCommandCompletion()
	m.Status = Status{Level: StatusInfo, Text: "Commands: create, edit, complete, delete, search, filter, sort, group, columns, refresh"}
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
	if m.UI.Mode == ModeEditTask && m.UI.EditAllFields && m.UI.EditField == EditFieldDescription && keyName == "ctrl+j" {
		m.insertInputRune('\n')
		return m, nil
	}
	switch keyName {
	case "esc", "escape":
		m.cancelInput()
		return m, nil
	case "enter":
		if m.UI.Mode == ModeEditTask && m.UI.EditAllFields && m.UI.EditField != EditFieldDue {
			m.moveEditField(1)
			return m, nil
		}
		return m.submitInput()
	case "tab":
		if m.UI.Mode == ModeEditTask && m.UI.EditAllFields {
			m.moveEditField(1)
			return m, nil
		}
		if m.UI.Mode == ModeCommand || m.UI.Mode == ModeFilter || m.UI.Mode == ModeSort {
			m.completeCommand(false)
		}
		return m, nil
	case "shift+tab":
		if m.UI.Mode == ModeEditTask && m.UI.EditAllFields {
			m.moveEditField(-1)
			return m, nil
		}
		if m.UI.Mode == ModeCommand || m.UI.Mode == ModeFilter || m.UI.Mode == ModeSort {
			m.completeCommand(true)
		}
		return m, nil
	case "backspace", "ctrl+h":
		m.resetCommandCompletion()
		m.deleteInputRune()
		m.liveSearch()
		return m, nil
	case "left", "shift+left":
		m.resetCommandCompletion()
		if m.UI.InputCursor > 0 {
			m.UI.InputCursor--
		}
		return m, nil
	case "right", "shift+right":
		m.resetCommandCompletion()
		if m.UI.InputCursor < runeCount(m.UI.Input) {
			m.UI.InputCursor++
		}
		return m, nil
	case "home":
		m.resetCommandCompletion()
		m.UI.InputCursor = 0
		return m, nil
	case "end":
		m.resetCommandCompletion()
		m.UI.InputCursor = runeCount(m.UI.Input)
		return m, nil
	}

	text := key.text()
	if text == "" {
		return m, nil
	}
	m.resetCommandCompletion()
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
	m.UI.EditTask = Task{}
	m.UI.EditAllFields = false
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
			m.clearSearch()
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
	case ModeSort:
		criteria, err := ParseSort(m.UI.Input)
		if err != nil {
			m.Status = Status{Level: StatusError, Text: err.Error()}
			return m, nil
		}
		return m.applySort(criteria, AppCommand{Kind: CommandSort, Sort: criteria})
	case ModeCommand:
		return m.submitPalette()
	case ModeCreateTask:
		return m.submitCreate()
	case ModeCreateSpace:
		return m.submitHierarchyCreate(CommandCreateSpace, m.UI.Input)
	case ModeCreateList:
		return m.submitHierarchyCreate(CommandCreateList, m.UI.Input)
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
	if err := m.validateFilterExpression(filter.Expression); err != nil {
		m.Status = Status{Level: StatusError, Text: err.Error()}
		return m, nil
	}
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

func (m Model) applySort(criteria []SortCriterion, command AppCommand) (Model, Cmd) {
	if _, ok := m.selectedList(); !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a list before sorting tasks"}
		return m, nil
	}
	criteria, err := m.normalizeSortCriteria(criteria)
	if err != nil {
		m.Status = Status{Level: StatusError, Text: err.Error()}
		return m, nil
	}
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.SortBy = criteria
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
	command.Sort = append([]SortCriterion(nil), criteria...)
	if len(criteria) == 0 {
		m.Status = Status{Level: StatusInfo, Text: "Task sorting cleared"}
	} else {
		m.Status = Status{Level: StatusInfo, Text: "Tasks sorted by " + sortCriteriaString(criteria)}
	}
	return m, m.emit(command)
}

func (m Model) applyGrouping(mode TaskGroupMode, command AppCommand) (Model, Cmd) {
	if mode != TaskGroupNone && mode != TaskGroupStatus && mode != TaskGroupAssignee && mode != TaskGroupPriority && mode != TaskGroupTasksSubtasks {
		m.Status = Status{Level: StatusError, Text: "unknown task group " + string(mode)}
		return m, nil
	}
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	if m.UI.GroupBy != mode {
		m.UI.FocusedGroup = ""
		m.UI.TaskGroupCursor = 0
		m.UI.TaskHeaderSelected = false
		m.UI.TaskHeaderTask = TaskRef{}
	}
	m.UI.GroupBy = mode
	m.UI.TaskOffset = 0
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.UI.TaskGroupCursor = 0
	m.UI.TaskHeaderSelected = false
	m.UI.TaskHeaderTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
	command.GroupBy = mode
	if mode == TaskGroupNone {
		m.Status = Status{Level: StatusInfo, Text: "Task grouping cleared"}
	} else {
		m.Status = Status{Level: StatusInfo, Text: "Tasks grouped by " + string(mode)}
	}
	return m, m.emit(command)
}

func (m Model) submitPalette() (Model, Cmd) {
	command, err := ParseCommand(m.UI.Input)
	if err != nil {
		m.Status = Status{Level: StatusError, Text: err.Error()}
		return m, nil
	}
	switch command.Kind {
	case CommandSwitchProvider:
		return m.switchProvider(command.ProviderID)
	case CommandSearch:
		return m.applySearch(command.Query, command)
	case CommandFilter:
		if len(strings.Fields(strings.TrimPrefix(command.Raw, ":"))) == 1 {
			m.beginFilter()
			return m, nil
		}
		return m.applyFilter(command.Filter, command)
	case CommandSort:
		if len(command.Sort) == 0 && len(strings.Fields(strings.TrimPrefix(command.Raw, ":"))) == 1 {
			m.beginSort()
			return m, nil
		}
		return m.applySort(command.Sort, command)
	case CommandGroup:
		return m.applyGrouping(command.GroupBy, command)
	case CommandConfigureColumns:
		m.UI.Mode = ModeBrowse
		m.beginColumnConfiguration()
		return m, nil
	case CommandCreateTask:
		if command.Title == "" {
			m.UI.Mode = modeForCreation(command.Kind)
			m.UI.Input = ""
			m.UI.InputCursor = 0
			m.Status = Status{Level: StatusInfo, Text: "Enter a title for the new task"}
			return m, nil
		}
		return m.submitCreateTitle(command.Title, command)
	case CommandCreateSpace, CommandCreateList:
		if command.Title == "" {
			m.UI.Mode = modeForCreation(command.Kind)
			m.UI.Input = ""
			m.UI.InputCursor = 0
			m.Status = Status{Level: StatusInfo, Text: creationPrompt(command.Kind)}
			return m, nil
		}
		return m.submitHierarchyCreate(command.Kind, command.Title)
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
	case CommandMoveTask, CommandAddTaskToList, CommandRemoveTaskFromList:
		return m.submitTaskListCommand(command)
	case CommandRefresh:
		m.UI.Mode = ModeBrowse
		m.UI.Input = ""
		return m, m.refreshFocusedPane()
	case CommandQuit:
		m.UI.Quitting = true
		m.UI.Mode = ModeBrowse
		return m, m.emit(command)
	case CommandHelp:
		m.UI.Mode = ModeBrowse
		m.UI.Input = ""
		m.Status = Status{Level: StatusInfo, Text: "Keys: j/k move, tab switch panel, h/l scroll, enter open, n/e/x/d, / search, f filter, o sort, c columns, r refresh; :sort and :filter edit task views"}
		return m, nil
	default:
		m.Status = Status{Level: StatusError, Text: "Unsupported command"}
		return m, nil
	}
}

func (m Model) switchProvider(requested ProviderID) (Model, Cmd) {
	requested = ProviderID(strings.TrimSpace(string(requested)))
	for _, provider := range m.allProviders() {
		if normalize(string(provider.ID)) != normalize(string(requested)) && normalize(displayProviderName(provider)) != normalize(string(requested)) {
			continue
		}
		m.UI.ActiveProviderID = provider.ID
		m.UI.Mode = ModeBrowse
		m.UI.Input = ""
		m.UI.InputCursor = 0
		m.UI.SearchActive = false
		m.UI.SearchQuery = ""
		m.UI.FilterActive = false
		m.UI.Filter = Filter{}
		m.UI.SelectedNode = TreeNodeRef{}
		m.UI.SelectedTask = TaskRef{}
		m.UI.TreeCursor = 0
		m.UI.TaskCursor = 0
		m.initializeSelection()
		m.Status = Status{Level: StatusInfo, Text: "Switched to provider " + displayProviderName(provider)}
		return m, nil
	}
	m.Status = Status{Level: StatusError, Text: "Unknown provider " + string(requested)}
	return m, nil
}

func (m Model) submitCreate() (Model, Cmd) {
	return m.submitCreateTitle(strings.TrimSpace(m.UI.Input), AppCommand{Kind: CommandCreateTask, Title: strings.TrimSpace(m.UI.Input)})
}

func creationPrompt(kind CommandKind) string {
	if kind == CommandCreateSpace {
		return "Enter a name for the new space"
	}
	return "Enter a name for the new list"
}

func modeForCreation(kind CommandKind) Mode {
	switch kind {
	case CommandCreateSpace:
		return ModeCreateSpace
	case CommandCreateTask:
		return ModeCreateTask
	default:
		return ModeCreateList
	}
}

func (m Model) submitHierarchyCreate(kind CommandKind, title string) (Model, Cmd) {
	title = strings.TrimSpace(title)
	if title == "" {
		m.Status = Status{Level: StatusError, Text: "Name cannot be empty"}
		return m, nil
	}
	command := AppCommand{Kind: kind, Title: title, ProviderID: m.UI.SelectedNode.ProviderID}
	if command.ProviderID == "" {
		m.Status = Status{Level: StatusError, Text: "Select a provider before creating hierarchy"}
		return m, nil
	}
	if kind == CommandCreateList {
		space, ok := m.selectedSpace()
		if !ok {
			m.Status = Status{Level: StatusError, Text: "Select a space before creating a list"}
			return m, nil
		}
		command.SpaceID = space.ID
	}
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.Status = Status{Level: StatusInfo, Text: "Hierarchy creation requested locally"}
	return m, m.emit(command)
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
	if m.UI.EditAllFields {
		return m.submitDetailEdit()
	}
	return m.submitEditTitle(strings.TrimSpace(m.UI.Input), AppCommand{Kind: CommandUpdateTask})
}

func (m Model) submitDetailEdit() (Model, Cmd) {
	m.commitEditInput()
	if strings.TrimSpace(m.UI.EditTask.Title) == "" {
		m.Status = Status{Level: StatusError, Text: "Task title cannot be empty"}
		return m, nil
	}
	if m.UI.EditField == EditFieldDue && strings.TrimSpace(m.UI.Input) != "" {
		due, err := parseEditDue(strings.TrimSpace(m.UI.Input))
		if err != nil {
			m.Status = Status{Level: StatusError, Text: err.Error()}
			return m, nil
		}
		m.UI.EditTask.DueAt = &due
	}
	command := AppCommand{
		Kind:          CommandUpdateTask,
		ProviderID:    m.UI.EditTask.ProviderID,
		ListID:        m.UI.EditTask.ListID,
		TaskID:        m.UI.EditTask.ID,
		Title:         m.UI.EditTask.Title,
		Description:   m.UI.EditTask.Description,
		Assignee:      m.UI.EditTask.Assignee,
		Status:        m.UI.EditTask.Status,
		Priority:      m.UI.EditTask.Priority,
		DueAt:         cloneTime(m.UI.EditTask.DueAt),
		ClearDueAt:    m.UI.EditTask.DueAt == nil,
		EditAllFields: true,
	}
	m.UI.Mode = ModeDetail
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.EditTask = Task{}
	m.UI.EditAllFields = false
	m.Status = Status{Level: StatusInfo, Text: "Edit requested locally; sync is asynchronous"}
	return m, m.emit(command)
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

func (m Model) submitTaskListCommand(command AppCommand) (Model, Cmd) {
	row, ok := m.selectedTask()
	if !ok {
		m.Status = Status{Level: StatusWarning, Text: "Select a task first"}
		return m, nil
	}
	if command.DestinationListID == "" {
		m.Status = Status{Level: StatusError, Text: "Specify a destination list id"}
		return m, nil
	}
	command.ProviderID = row.Task.ProviderID
	command.ListID = row.Task.ListID
	command.TaskID = row.Task.ID
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	switch command.Kind {
	case CommandMoveTask:
		m.Status = Status{Level: StatusInfo, Text: "Move requested locally; sync is asynchronous"}
	case CommandAddTaskToList:
		m.Status = Status{Level: StatusInfo, Text: "List membership requested locally; sync is asynchronous"}
	case CommandRemoveTaskFromList:
		m.Status = Status{Level: StatusInfo, Text: "List membership removal requested locally; sync is asynchronous"}
	}
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
		m.clearSearch()
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

func (m *Model) clearSearch() {
	m.UI.Mode = ModeBrowse
	m.UI.Input = ""
	m.UI.InputCursor = 0
	m.UI.InputOrigin = ""
	m.UI.InputOriginSearchActive = false
	m.UI.SearchActive = false
	m.UI.SearchQuery = ""
	m.UI.TaskCursor = 0
	m.UI.SelectedTask = TaskRef{}
	m.selectTaskAt(0)
	m.keepVisible()
	m.Status = Status{Level: StatusInfo, Text: "Search cleared"}
}

func (m *Model) closeDetail() {
	m.UI.Mode = ModeBrowse
	m.UI.DetailOffset = 0
	m.UI.EditTask = Task{}
	m.UI.EditAllFields = false
	m.Status = Status{Level: StatusInfo, Text: "Closed task details"}
}

func (m *Model) scrollDetail(delta int) {
	m.UI.DetailOffset = clamp(m.UI.DetailOffset+delta, 0, m.maxDetailOffset())
}

func (m Model) maxDetailOffset() int {
	return maxInt(len(m.detailLines(maxInt(m.UI.Width, 1)))-m.detailViewportHeight(), 0)
}

func (m Model) detailViewportHeight() int {
	height := m.UI.Height
	if height < 1 {
		height = 24
	}
	return maxInt(height-4, 1)
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

func (m Model) selectedSpace() (Space, bool) {
	ref := m.UI.SelectedNode
	for _, space := range m.Data.Spaces {
		if space.ProviderID != ref.ProviderID {
			continue
		}
		if ref.Kind == TreeNodeSpace && space.ID == ref.SpaceID {
			return space, true
		}
		if ref.Kind == TreeNodeList && space.ID == ref.SpaceID {
			return space, true
		}
	}
	if ref.Kind == TreeNodeProvider {
		for _, space := range m.Data.Spaces {
			if space.ProviderID == ref.ProviderID {
				return space, true
			}
		}
	}
	return Space{}, false
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
	if m.UI.TaskHeaderSelected {
		if m.UI.SelectedTask == (TaskRef{}) {
			return TaskRow{}, false
		}
		groups := m.VisibleTaskGroups()
		groupIndex := m.currentTaskGroupIndex(groups)
		if groupIndex < 0 || groupIndex >= len(groups) {
			return TaskRow{}, false
		}
		for _, row := range groups[groupIndex].Rows {
			if taskRef(row) == m.UI.SelectedTask {
				return row, true
			}
		}
		return TaskRow{}, false
	}
	if m.UI.SelectedTask != (TaskRef{}) {
		if index := findTask(rows, m.UI.SelectedTask); index >= 0 {
			return rows[index], true
		}
		if row, ok := m.taskRowForRef(m.UI.SelectedTask); ok {
			return row, true
		}
	}
	if len(rows) == 0 {
		return TaskRow{}, false
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
		m.UI.TaskHeaderSelected = false
		m.UI.TaskHeaderTask = TaskRef{}
		return
	}
	cursor = clamp(cursor, 0, len(rows)-1)
	m.UI.TaskCursor = cursor
	m.UI.SelectedTask = taskRef(rows[cursor])
	m.UI.TaskHeaderSelected = false
	m.UI.TaskHeaderTask = TaskRef{}
	m.UI.FocusedGroup = ""
	if m.UI.GroupBy != TaskGroupNone {
		groups := m.VisibleTaskGroups()
		for index, group := range groups {
			if findTask(group.Rows, m.UI.SelectedTask) >= 0 {
				m.UI.TaskGroupCursor = index
				break
			}
		}
	}
}

func (m *Model) selectTaskRef(wanted TaskRef) bool {
	rows := m.VisibleTasks()
	if len(rows) == 0 {
		m.UI.TaskCursor = 0
		return false
	}
	if wanted != (TaskRef{}) {
		if cursor := findTask(rows, wanted); cursor >= 0 {
			m.selectTaskAt(cursor)
			return true
		}
	}
	m.selectTaskAt(0)
	return false
}

func (m *Model) toggleTaskGroup() {
	if m.UI.Focus != PanelTasks {
		m.Status = Status{Level: StatusWarning, Text: "Focus the task panel to toggle a group"}
		return
	}
	if m.UI.GroupBy == TaskGroupNone {
		m.Status = Status{Level: StatusWarning, Text: "Enable task grouping before collapsing groups"}
		return
	}

	groups := m.VisibleTaskGroups()
	selected := m.UI.SelectedTask
	groupIndex := -1
	if m.UI.TaskHeaderSelected {
		groupIndex = m.currentTaskGroupIndex(groups)
		selected = m.UI.TaskHeaderTask
		if selected == (TaskRef{}) {
			selected = m.UI.SelectedTask
		}
	} else if selected == (TaskRef{}) {
		if row, ok := m.selectedTask(); ok {
			selected = taskRef(row)
		}
	}
	if groupIndex < 0 {
		for index, group := range groups {
			if selected != (TaskRef{}) {
				for _, row := range group.Rows {
					if taskRef(row) == selected {
						groupIndex = index
						break
					}
				}
			}
			if groupIndex >= 0 {
				break
			}
			if m.UI.FocusedGroup == taskGroupStateKey(m.UI.GroupBy, group.Key) {
				groupIndex = index
			}
		}
	}
	if groupIndex < 0 {
		m.Status = Status{Level: StatusWarning, Text: "Select a grouped task first"}
		return
	}

	group := groups[groupIndex]
	stateKey := taskGroupStateKey(m.UI.GroupBy, group.Key)
	m.UI.CollapsedGroups = cloneCollapsed(m.UI.CollapsedGroups)
	if group.Collapsed {
		delete(m.UI.CollapsedGroups, stateKey)
		desired := selected
		if desired == (TaskRef{}) && len(group.Rows) > 0 {
			desired = taskRef(group.Rows[0])
		}
		m.UI.TaskHeaderSelected = false
		m.UI.TaskHeaderTask = TaskRef{}
		m.UI.FocusedGroup = ""
		m.selectTaskRef(desired)
		m.Status = Status{Level: StatusInfo, Text: "Expanded group " + group.Label}
		return
	}

	m.UI.CollapsedGroups[stateKey] = true
	m.UI.TaskGroupCursor = groupIndex
	m.UI.TaskHeaderSelected = true
	m.UI.TaskHeaderTask = selected
	m.UI.FocusedGroup = stateKey
	// Keep SelectedTask intact so provider-scoped actions still target the same
	// task while its group is hidden.
	// A negative cursor lets the next j/k movement land on the first visible
	// task instead of skipping it.
	m.UI.TaskCursor = -1
	m.UI.TaskOffset = 0
	m.Status = Status{Level: StatusInfo, Text: "Collapsed group " + group.Label}
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
	if m.UI.Mode == ModeDetail {
		m.UI.DetailOffset = clamp(m.UI.DetailOffset, 0, m.maxDetailOffset())
		return
	}
	treeViewport, taskViewport := m.panelViewports()
	m.UI.TreeOffset = keepCursorVisible(m.UI.TreeCursor, m.UI.TreeOffset, treeViewport)
	if m.UI.Focus == PanelTasks && m.UI.TaskHeaderSelected {
		groups := m.VisibleTaskGroups()
		groupIndex := m.currentTaskGroupIndex(groups)
		if groupIndex >= 0 {
			m.UI.TaskOffset = keepCursorVisible(taskGroupVisualStart(groups, groupIndex), m.UI.TaskOffset, taskViewport)
		} else {
			m.UI.TaskOffset = 0
		}
	} else if m.UI.Focus == PanelTasks && m.UI.GroupBy != TaskGroupNone {
		groups := m.VisibleTaskGroups()
		if position, ok := taskVisualPosition(groups, m.UI.SelectedTask); ok {
			m.UI.TaskOffset = keepCursorVisible(position, m.UI.TaskOffset, taskViewport)
		} else {
			m.UI.TaskOffset = 0
		}
	} else {
		m.UI.TaskOffset = keepCursorVisible(m.UI.TaskCursor, m.UI.TaskOffset, taskViewport)
	}
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
		return visible, taskPanelItemViewport(bodyHeight)
	}

	treeHeight := bodyHeight / 2
	if treeHeight < 3 {
		treeHeight = 3
	}
	if treeHeight >= bodyHeight {
		treeHeight = maxInt(bodyHeight-1, 1)
	}
	taskHeight := maxInt(bodyHeight-treeHeight, 1)
	return panelItemViewport(treeHeight), taskPanelItemViewport(taskHeight)
}

func panelItemViewport(panelHeight int) int {
	// Leave room for the panel border, heading, and the scroll marker.
	return maxInt(panelHeight-4, 1)
}

func taskPanelItemViewport(panelHeight int) int {
	// Task panels reserve an additional line for the table column headings.
	return maxInt(panelItemViewport(panelHeight)-1, 1)
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
