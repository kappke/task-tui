package tui

import (
	"sort"
	"strings"
)

type scopedID struct {
	provider ProviderID
	id       string
}

const SearchResultLimit = 100

// TreeNodes returns the visible hierarchy in stable snapshot order.
func (m Model) TreeNodes() []TreeNode {
	providers := m.viewProviders()
	nodes := make([]TreeNode, 0, len(providers)+len(m.Data.Spaces)+len(m.Data.Lists))
	for _, provider := range providers {
		providerRef := TreeNodeRef{
			Kind:       TreeNodeProvider,
			ProviderID: provider.ID,
		}
		providerExpanded := m.isExpanded(providerRef)
		nodes = append(nodes, TreeNode{
			Ref:          providerRef,
			Name:         displayProviderName(provider),
			ProviderID:   provider.ID,
			ProviderName: displayProviderName(provider),
			SyncState:    stateOr(provider.SyncState, SyncStateUnknown),
			SyncError:    provider.SyncError,
			Depth:        0,
			Expanded:     providerExpanded,
		})
		if !providerExpanded {
			continue
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
			spaceExpanded := m.isExpanded(spaceRef)
			nodes = append(nodes, TreeNode{
				Ref:          spaceRef,
				Name:         space.Name,
				ProviderID:   provider.ID,
				ProviderName: displayProviderName(provider),
				SyncState:    stateOr(space.SyncState, provider.SyncState),
				Depth:        1,
				Expanded:     spaceExpanded,
			})
			if !spaceExpanded {
				continue
			}

			for _, list := range m.Data.Lists {
				if list.ProviderID != provider.ID || list.SpaceID != space.ID {
					continue
				}
				listRef := TreeNodeRef{
					Kind:       TreeNodeList,
					ProviderID: provider.ID,
					SpaceID:    space.ID,
					ListID:     list.ID,
				}
				nodes = append(nodes, TreeNode{
					Ref:          listRef,
					Name:         list.Name,
					ProviderID:   provider.ID,
					ProviderName: displayProviderName(provider),
					SyncState:    stateOr(list.SyncState, provider.SyncState),
					Depth:        2,
					Expanded:     false,
				})
			}
		}
	}
	return nodes
}

// Nodes is a concise alias for TreeNodes.
func (m Model) Nodes() []TreeNode {
	return m.TreeNodes()
}

// VisibleTasks returns the task rows for the active hierarchy/search/filter
// view, arranged according to the active grouping mode.
func (m Model) VisibleTasks() []TaskRow {
	aggregate := false
	if m.UI.FilterActive && (m.UI.Filter.ProviderID != "" || m.UI.Filter.SpaceID != "" || m.UI.Filter.ListID != "") {
		aggregate = true
	}
	return flattenTaskGroups(m.visibleTaskGroups(aggregate))
}

// CurrentTasks is an expressive alias for VisibleTasks.
func (m Model) CurrentTasks() []TaskRow {
	return m.VisibleTasks()
}

// SearchResults returns the locally computed search projection for the
// currently selected hierarchy scope.
func (m Model) SearchResults() []TaskRow {
	return flattenTaskGroups(m.visibleTaskGroups(false))
}

// VisibleTaskGroups returns the filtered task rows arranged according to the
// active grouping mode. An empty grouping mode returns one unlabelled group.
func (m Model) VisibleTaskGroups() []TaskGroup {
	aggregate := false
	if m.UI.FilterActive && (m.UI.Filter.ProviderID != "" || m.UI.Filter.SpaceID != "" || m.UI.Filter.ListID != "") {
		aggregate = true
	}
	return m.visibleTaskGroups(aggregate)
}

// GroupedTasks is an alias for VisibleTaskGroups.
func (m Model) GroupedTasks() []TaskGroup {
	return m.VisibleTaskGroups()
}

func (m Model) taskRows(aggregate bool) []TaskRow {
	providers := m.viewProviders()
	providerByID := make(map[ProviderID]Provider, len(providers))
	for _, provider := range providers {
		providerByID[provider.ID] = provider
	}

	spaces := make(map[scopedID]Space, len(m.Data.Spaces))
	for _, space := range m.Data.Spaces {
		spaces[scopedID{provider: space.ProviderID, id: string(space.ID)}] = space
	}
	lists := make(map[scopedID]List, len(m.Data.Lists))
	for _, list := range m.Data.Lists {
		lists[scopedID{provider: list.ProviderID, id: string(list.ID)}] = list
	}
	columnValues := make(map[scopedID]map[string]string, len(m.Data.TaskColumnValues))
	for _, values := range m.Data.TaskColumnValues {
		columnValues[scopedID{provider: values.ProviderID, id: string(values.TaskID)}] = values.Values
	}

	rows := make([]TaskRow, 0, len(m.Data.Tasks))
	for _, task := range m.Data.Tasks {
		if m.UI.ActiveProviderID != "" && task.ProviderID != m.UI.ActiveProviderID {
			continue
		}
		if !aggregate && !m.inSelectedScope(task, lists) {
			continue
		}
		memberships := task.Memberships()
		listNames := make([]string, 0, len(memberships))
		for _, membership := range memberships {
			name := string(membership)
			if list, ok := lists[scopedID{provider: task.ProviderID, id: string(membership)}]; ok {
				name = list.Name
			}
			listNames = append(listNames, name)
		}

		provider, providerOK := providerByID[task.ProviderID]
		if !providerOK {
			provider = Provider{ID: task.ProviderID, Name: string(task.ProviderID), SyncState: SyncStateUnknown}
		}
		listID := task.ListID
		if m.UI.SelectedNode.Kind == TreeNodeList && taskHasList(task, m.UI.SelectedNode.ListID) {
			listID = m.UI.SelectedNode.ListID
		}
		list, listOK := lists[scopedID{provider: task.ProviderID, id: string(listID)}]
		space, spaceOK := spaces[scopedID{provider: task.ProviderID, id: string(list.SpaceID)}]
		row := TaskRow{
			Task:         task,
			ProviderID:   task.ProviderID,
			ProviderName: displayProviderName(provider),
			Assignee:     task.Assignee,
			SpaceID:      list.SpaceID,
			ListID:       listID,
			ListNames:    listNames,
			ColumnValues: columnValues[scopedID{provider: task.ProviderID, id: string(task.ID)}],
			SearchResult: m.UI.SearchActive,
		}
		if listOK {
			row.ListName = list.Name
			row.SpaceID = list.SpaceID
		}
		if !spaceOK {
			row.SpaceID = list.SpaceID
		}
		if row.ListName == "" {
			row.ListName = string(listID)
		}
		if spaceOK {
			row.SpaceName = space.Name
		}
		if row.SpaceName == "" {
			row.SpaceName = string(row.SpaceID)
		}

		if !m.matchesRow(row) {
			continue
		}
		rows = append(rows, row)
		if m.UI.SearchActive && len(rows) >= SearchResultLimit {
			break
		}
	}
	return rows
}

func (m Model) visibleTaskGroups(aggregate bool) []TaskGroup {
	groups := groupTaskRows(m.taskRows(aggregate), m.UI.GroupBy)
	if m.UI.GroupBy == TaskGroupNone {
		return groups
	}
	for index := range groups {
		groups[index].Collapsed = m.UI.CollapsedGroups[taskGroupStateKey(m.UI.GroupBy, groups[index].Key)]
	}
	return groups
}

func flattenTaskGroups(groups []TaskGroup) []TaskRow {
	if len(groups) == 0 {
		return nil
	}
	rows := make([]TaskRow, 0)
	for _, group := range groups {
		if group.Collapsed {
			continue
		}
		rows = append(rows, group.Rows...)
	}
	return rows
}

func allTaskGroupsCollapsed(groups []TaskGroup) bool {
	if len(groups) == 0 {
		return false
	}
	for _, group := range groups {
		if !group.Collapsed {
			return false
		}
	}
	return true
}

// taskGroupVisualLength is the number of lines occupied by grouped content.
// Group headers are scroll items alongside task rows, rather than decoration
// outside the task offset.
func taskGroupVisualLength(groups []TaskGroup) int {
	length := 0
	for _, group := range groups {
		length++
		if !group.Collapsed {
			length += len(group.Rows)
		}
	}
	return length
}

func taskGroupVisualStart(groups []TaskGroup, target int) int {
	start := 0
	for index, group := range groups {
		if index == target {
			return start
		}
		start++
		if !group.Collapsed {
			start += len(group.Rows)
		}
	}
	return start
}

func taskVisualPosition(groups []TaskGroup, wanted TaskRef) (int, bool) {
	position := 0
	for _, group := range groups {
		position++
		if !group.Collapsed {
			for _, row := range group.Rows {
				if taskRef(row) == wanted {
					return position, true
				}
				position++
			}
		}
	}
	return 0, false
}

func taskGroupStateKey(mode TaskGroupMode, key string) string {
	if mode == TaskGroupNone || key == "" {
		return ""
	}
	return string(mode) + ":" + key
}

func (m Model) taskRowForRef(wanted TaskRef) (TaskRow, bool) {
	if wanted == (TaskRef{}) {
		return TaskRow{}, false
	}
	for _, group := range m.VisibleTaskGroups() {
		for _, row := range group.Rows {
			if taskRef(row) == wanted {
				return row, true
			}
		}
	}
	return TaskRow{}, false
}

func groupTaskRows(rows []TaskRow, mode TaskGroupMode) []TaskGroup {
	if len(rows) == 0 {
		return nil
	}
	if mode == TaskGroupNone {
		return []TaskGroup{{Rows: append([]TaskRow(nil), rows...)}}
	}

	switch mode {
	case TaskGroupStatus, TaskGroupAssignee, TaskGroupPriority:
		groupsByKey := make(map[string]int, len(rows))
		groups := make([]TaskGroup, 0, len(rows))
		for _, row := range rows {
			key, label := taskGroupValue(row, mode)
			groupIndex, ok := groupsByKey[key]
			if !ok {
				groupIndex = len(groups)
				groupsByKey[key] = groupIndex
				groups = append(groups, TaskGroup{Key: key, Label: label})
			}
			groups[groupIndex].Rows = append(groups[groupIndex].Rows, row)
		}
		sort.SliceStable(groups, func(left, right int) bool {
			if mode == TaskGroupPriority {
				return priorityRank(Priority(groups[left].Key)) > priorityRank(Priority(groups[right].Key))
			}
			return normalize(groups[left].Label) < normalize(groups[right].Label)
		})
		return groups
	case TaskGroupTasksSubtasks:
		return taskHierarchyGroups(rows)
	default:
		return []TaskGroup{{Rows: append([]TaskRow(nil), rows...)}}
	}
}

func taskGroupValue(row TaskRow, mode TaskGroupMode) (key, label string) {
	value := ""
	switch mode {
	case TaskGroupStatus:
		value = strings.TrimSpace(row.Task.Status)
		if value == "" {
			value = "Unspecified"
		}
	case TaskGroupAssignee:
		value = strings.TrimSpace(row.Assignee)
		if value == "" {
			value = strings.TrimSpace(row.Task.Assignee)
		}
		if value == "" {
			value = "Unassigned"
		}
	case TaskGroupPriority:
		value = strings.TrimSpace(string(row.Task.Priority))
		if value == "" || normalize(value) == string(PriorityNone) {
			value = "None"
		}
	default:
		value = "Tasks"
	}
	return normalize(value), value
}

func taskHierarchyGroups(rows []TaskRow) []TaskGroup {
	byRef := make(map[TaskRef]int, len(rows))
	children := make(map[TaskRef][]int, len(rows))
	for index, row := range rows {
		byRef[taskRef(row)] = index
	}

	roots := make([]int, 0, len(rows))
	for index, row := range rows {
		parent, hasParent := taskParentRef(row)
		if !hasParent {
			roots = append(roots, index)
			continue
		}
		if _, ok := byRef[parent]; !ok {
			roots = append(roots, index)
			continue
		}
		children[parent] = append(children[parent], index)
	}

	groups := make([]TaskGroup, 0, len(roots))
	visited := make(map[int]bool, len(rows))
	for _, root := range roots {
		group := TaskGroup{
			Key:   string(rows[root].ProviderID) + "/" + string(rows[root].Task.ID),
			Label: taskTitle(rows[root]),
		}
		appendTaskTree(&group.Rows, root, 0, rows, children, visited)
		groups = append(groups, group)
	}
	for index := range rows {
		if visited[index] {
			continue
		}
		group := TaskGroup{
			Key:   string(rows[index].ProviderID) + "/" + string(rows[index].Task.ID),
			Label: taskTitle(rows[index]),
		}
		appendTaskTree(&group.Rows, index, 0, rows, children, visited)
		groups = append(groups, group)
	}
	return groups
}

func appendTaskTree(output *[]TaskRow, index, depth int, rows []TaskRow, children map[TaskRef][]int, visited map[int]bool) {
	if visited[index] {
		return
	}
	visited[index] = true
	row := rows[index]
	row.HierarchyDepth = depth
	*output = append(*output, row)
	for _, child := range children[taskRef(row)] {
		appendTaskTree(output, child, depth+1, rows, children, visited)
	}
}

func taskParentRef(row TaskRow) (TaskRef, bool) {
	if row.Task.ParentTaskID == nil || *row.Task.ParentTaskID == "" {
		return TaskRef{}, false
	}
	return TaskRef{ProviderID: row.Task.ProviderID, TaskID: *row.Task.ParentTaskID}, true
}

func taskTitle(row TaskRow) string {
	title := strings.TrimSpace(row.Task.Title)
	if title == "" {
		return "(untitled task)"
	}
	return title
}

func (m Model) inSelectedScope(task Task, lists map[scopedID]List) bool {
	ref := m.UI.SelectedNode
	switch ref.Kind {
	case TreeNodeProvider:
		return task.ProviderID == ref.ProviderID
	case TreeNodeSpace:
		if task.ProviderID != ref.ProviderID {
			return false
		}
		for _, listID := range task.Memberships() {
			list, ok := lists[scopedID{provider: task.ProviderID, id: string(listID)}]
			if ok && list.SpaceID == ref.SpaceID {
				return true
			}
		}
		return false
	case TreeNodeList:
		return task.ProviderID == ref.ProviderID && taskHasList(task, ref.ListID)
	default:
		return true
	}
}

func (m Model) matchesRow(row TaskRow) bool {
	query := ""
	if m.UI.SearchActive {
		query = m.UI.SearchQuery
	}
	if m.UI.FilterActive && m.UI.Filter.Query != "" {
		if query == "" {
			query = m.UI.Filter.Query
		} else {
			query += " " + m.UI.Filter.Query
		}
	}
	if query != "" && !containsTaskText(row, query) {
		return false
	}
	if !m.UI.FilterActive {
		return true
	}

	filter := m.UI.Filter
	if filter.ProviderID != "" && !matchesIdentifier(string(filter.ProviderID), string(row.ProviderID), row.ProviderName) {
		return false
	}
	if filter.SpaceID != "" && !matchesIdentifier(string(filter.SpaceID), string(row.SpaceID), row.SpaceName) {
		return false
	}
	if filter.ListID != "" && !taskHasList(row.Task, filter.ListID) {
		matched := matchesIdentifier(string(filter.ListID), string(row.ListID), row.ListName)
		for _, listName := range row.ListNames {
			matched = matched || matchesIdentifier(string(filter.ListID), listName, listName)
		}
		if !matched {
			return false
		}
	}
	if filter.Status != "" && normalize(row.Task.Status) != normalize(filter.Status) {
		return false
	}
	if filter.StatusNot != "" && normalize(row.Task.Status) == normalize(filter.StatusNot) {
		return false
	}
	if filter.Priority != "" && normalize(string(row.Task.Priority)) != normalize(filter.Priority) {
		return false
	}
	if filter.PriorityNot != "" && normalize(string(row.Task.Priority)) == normalize(filter.PriorityNot) {
		return false
	}
	if filter.PriorityMin != "" && priorityRank(row.Task.Priority) < priorityRank(Priority(filter.PriorityMin)) {
		return false
	}
	if filter.PriorityMax != "" && priorityRank(row.Task.Priority) > priorityRank(Priority(filter.PriorityMax)) {
		return false
	}
	if filter.SyncState != "" && normalize(string(row.Task.SyncState)) != normalize(string(filter.SyncState)) {
		return false
	}
	complete := isTaskComplete(row.Task)
	if filter.Completed != nil && complete != *filter.Completed {
		return false
	}
	if filter.CompletedNot != nil && complete == *filter.CompletedNot {
		return false
	}
	if filter.DueBefore != nil && (row.Task.DueAt == nil || row.Task.DueAt.After(*filter.DueBefore)) {
		return false
	}
	if filter.DueAfter != nil && (row.Task.DueAt == nil || row.Task.DueAt.Before(*filter.DueAfter)) {
		return false
	}
	return true
}

func containsTaskText(row TaskRow, query string) bool {
	terms := strings.Fields(normalize(query))
	if len(terms) == 0 {
		return true
	}
	values := []string{
		row.Task.Title,
		row.Task.Description,
		row.Assignee,
		row.ListName,
		strings.Join(row.ListNames, " "),
		row.SpaceName,
		row.ProviderName,
		string(row.ProviderID),
	}
	for _, value := range row.ColumnValues {
		values = append(values, value)
	}
	for _, term := range terms {
		found := false
		for _, value := range values {
			if strings.Contains(normalize(value), term) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchesIdentifier(want, id, name string) bool {
	want = normalize(want)
	return want == normalize(id) || want == normalize(name)
}

func priorityRank(priority Priority) int {
	switch normalize(string(priority)) {
	case "urgent", "critical", "p0":
		return 4
	case "high", "p1":
		return 3
	case "normal", "medium", "p2":
		return 2
	case "low", "p3":
		return 1
	default:
		return 0
	}
}

func stateOr(state, fallback SyncState) SyncState {
	if state != "" {
		return state
	}
	if fallback != "" {
		return fallback
	}
	return SyncStateUnknown
}

func displayProviderName(provider Provider) string {
	if provider.Name != "" {
		return provider.Name
	}
	if provider.ID != "" {
		return string(provider.ID)
	}
	if provider.Type != "" {
		return string(provider.Type)
	}
	return "Unknown provider"
}

func (m Model) viewProviders() []Provider {
	providers := m.allProviders()
	if m.UI.ActiveProviderID != "" {
		for _, provider := range providers {
			if provider.ID == m.UI.ActiveProviderID {
				return []Provider{provider}
			}
		}
	}
	return providers
}

func (m Model) allProviders() []Provider {
	providers := append([]Provider(nil), m.Data.Providers...)
	seen := make(map[ProviderID]bool, len(providers))
	for _, provider := range providers {
		seen[provider.ID] = true
	}
	appendSynthetic := func(providerID ProviderID) {
		if seen[providerID] {
			return
		}
		seen[providerID] = true
		providers = append(providers, Provider{
			ID:        providerID,
			Name:      string(providerID),
			SyncState: SyncStateUnknown,
		})
	}
	for _, space := range m.Data.Spaces {
		appendSynthetic(space.ProviderID)
	}
	for _, list := range m.Data.Lists {
		appendSynthetic(list.ProviderID)
	}
	for _, task := range m.Data.Tasks {
		appendSynthetic(task.ProviderID)
	}
	return providers
}

func (m Model) isExpanded(ref TreeNodeRef) bool {
	if expanded, ok := m.UI.ExpandedNodes[ref]; ok {
		return expanded
	}
	return true
}
