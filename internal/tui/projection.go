package tui

import "strings"

type scopedID struct {
	provider ProviderID
	id       string
}

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
// view. Search rows are aggregated across providers.
func (m Model) VisibleTasks() []TaskRow {
	aggregate := m.UI.SearchActive
	if m.UI.FilterActive && (m.UI.Filter.ProviderID != "" || m.UI.Filter.SpaceID != "" || m.UI.Filter.ListID != "") {
		aggregate = true
	}
	return m.taskRows(aggregate)
}

// CurrentTasks is an expressive alias for VisibleTasks.
func (m Model) CurrentTasks() []TaskRow {
	return m.VisibleTasks()
}

// SearchResults returns the locally computed aggregate search projection.
func (m Model) SearchResults() []TaskRow {
	return m.taskRows(true)
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

	rows := make([]TaskRow, 0, len(m.Data.Tasks))
	for _, task := range m.Data.Tasks {
		if !aggregate && !m.inSelectedScope(task, lists) {
			continue
		}

		provider, providerOK := providerByID[task.ProviderID]
		if !providerOK {
			provider = Provider{ID: task.ProviderID, Name: string(task.ProviderID), SyncState: SyncStateUnknown}
		}
		list, listOK := lists[scopedID{provider: task.ProviderID, id: string(task.ListID)}]
		space, spaceOK := spaces[scopedID{provider: task.ProviderID, id: string(list.SpaceID)}]
		row := TaskRow{
			Task:         task,
			ProviderID:   task.ProviderID,
			ProviderName: displayProviderName(provider),
			SpaceID:      list.SpaceID,
			ListID:       task.ListID,
			SearchResult: aggregate,
		}
		if listOK {
			row.ListName = list.Name
			row.SpaceID = list.SpaceID
		}
		if !spaceOK {
			row.SpaceID = list.SpaceID
		}
		if row.ListName == "" {
			row.ListName = string(task.ListID)
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
	}
	return rows
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
		list, ok := lists[scopedID{provider: task.ProviderID, id: string(task.ListID)}]
		return ok && list.SpaceID == ref.SpaceID
	case TreeNodeList:
		return task.ProviderID == ref.ProviderID && task.ListID == ref.ListID
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
	if filter.ListID != "" && !matchesIdentifier(string(filter.ListID), string(row.ListID), row.ListName) {
		return false
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
		row.ListName,
		row.SpaceName,
		row.ProviderName,
		string(row.ProviderID),
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
