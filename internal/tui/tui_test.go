package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultKeyMap(t *testing.T) {
	tests := []struct {
		key    string
		action Action
	}{
		{key: "j", action: ActionMoveDown},
		{key: "down", action: ActionMoveDown},
		{key: "k", action: ActionMoveUp},
		{key: "up", action: ActionMoveUp},
		{key: "h", action: ActionScrollLeft},
		{key: "left", action: ActionScrollLeft},
		{key: "l", action: ActionScrollRight},
		{key: "right", action: ActionScrollRight},
		{key: "tab", action: ActionNextPanel},
		{key: "shift+tab", action: ActionPreviousPanel},
		{key: "enter", action: ActionSelect},
		{key: "space", action: ActionToggleGroup},
		{key: "g", action: ActionFirst},
		{key: "G", action: ActionLast},
		{key: "n", action: ActionCreate},
		{key: "e", action: ActionEdit},
		{key: "x", action: ActionComplete},
		{key: "d", action: ActionDelete},
		{key: "/", action: ActionSearch},
		{key: "f", action: ActionFilter},
		{key: "r", action: ActionRefresh},
		{key: ":", action: ActionCommand},
		{key: "q", action: ActionQuit},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			if got := MapKey(test.key); got != test.action {
				t.Fatalf("MapKey(%q) = %q, want %q", test.key, got, test.action)
			}
		})
	}
}

func TestNavigationAndProviderScopedTaskSelection(t *testing.T) {
	model := New(testSnapshot())
	if model.UI.SelectedNode.ProviderID != "work" || model.UI.SelectedNode.ListID != "backend" {
		t.Fatalf("initial selection = %#v, want work/backend", model.UI.SelectedNode)
	}

	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Focus != PanelTasks {
		t.Fatalf("focus after l = %q, want tasks", model.UI.Focus)
	}
	if model.UI.SelectedTask.ProviderID != "work" || model.UI.SelectedTask.TaskID != "same" {
		t.Fatalf("selected task = %#v, want work/same", model.UI.SelectedTask)
	}

	model, _ = model.Update(KeyMsg{Key: "j"})
	if model.UI.SelectedTask.TaskID != "work-second" {
		t.Fatalf("selected task after j = %#v, want work-second", model.UI.SelectedTask)
	}
	model, _ = model.Update(KeyMsg{Key: "G"})
	if model.UI.SelectedTask.TaskID != "work-second" {
		t.Fatalf("selected task after G = %#v, want work-second", model.UI.SelectedTask)
	}
	model, _ = model.Update(KeyMsg{Key: "g"})
	if model.UI.SelectedTask.TaskID != "same" || model.UI.SelectedTask.ProviderID != "work" {
		t.Fatalf("selected task after g = %#v, want work/same", model.UI.SelectedTask)
	}

	model, _ = model.Update(KeyMsg{Key: "shift+tab"})
	if model.UI.Focus != PanelHierarchy {
		t.Fatalf("focus after h = %q, want hierarchy", model.UI.Focus)
	}
	model, _ = model.Update(KeyMsg{Key: "up"})
	if model.UI.SelectedNode.ProviderID != "work" || model.UI.SelectedNode.SpaceID != "engineering" {
		t.Fatalf("selected node after arrow up = %#v, want work/engineering", model.UI.SelectedNode)
	}
}

func TestTaskDetailViewOpensScrollsAndCloses(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Tasks[0].Description = strings.Repeat("This description explains the authentication regression and the required fix. ", 4)
	model := New(snapshot)
	model, _ = model.Update(KeyMsg{Key: "tab"})
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command != nil || model.UI.Mode != ModeDetail {
		t.Fatalf("open detail: mode=%q command=%v", model.UI.Mode, command)
	}
	view := model.View()
	for _, value := range []string{"TASK DETAIL", "TITLE: Fix auth", "PROVIDER: Work (work)", "DESCRIPTION"} {
		if !strings.Contains(view, value) {
			t.Fatalf("detail view does not contain %q:\n%s", value, view)
		}
	}

	model, _ = model.Update(WindowSizeMsg{Width: 50, Height: 8})
	model, _ = model.Update(KeyMsg{Key: "j"})
	if model.UI.DetailOffset == 0 {
		t.Fatal("detail offset did not move down")
	}
	model, _ = model.Update(KeyMsg{Key: "G"})
	if model.UI.DetailOffset != model.maxDetailOffset() {
		t.Fatalf("detail offset = %d, want bottom %d", model.UI.DetailOffset, model.maxDetailOffset())
	}
	model, _ = model.Update(KeyMsg{Key: "esc"})
	if model.UI.Mode != ModeBrowse || model.UI.DetailOffset != 0 {
		t.Fatalf("close detail: mode=%q offset=%d", model.UI.Mode, model.UI.DetailOffset)
	}
}

func TestTaskDetailClosesWhenSelectedTaskIsRemoved(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "tab"})
	model, _ = model.Update(KeyMsg{Key: "enter"})
	model, _ = model.Update(TasksLoadedMsg{
		ProviderID: "work",
		ListID:     "backend",
		Tasks:      []Task{testSnapshot().Tasks[1]},
		Replace:    true,
	})
	if model.UI.Mode != ModeBrowse {
		t.Fatalf("mode after selected task removal = %q, want %q", model.UI.Mode, ModeBrowse)
	}
}

func TestSearchModeEmitsLocalSearchCommand(t *testing.T) {
	model := New(testSnapshot())
	model, command := model.Update(KeyMsg{Key: "/"})
	if command != nil || model.UI.Mode != ModeSearch {
		t.Fatalf("search start: mode=%q command=%v", model.UI.Mode, command)
	}
	model, _ = model.Update(KeyMsg{Runes: []rune("fix auth")})
	if model.UI.SearchQuery != "fix auth" {
		t.Fatalf("live search query = %q, want %q", model.UI.SearchQuery, "fix auth")
	}
	rows := model.SearchResults()
	if len(rows) != 1 || rows[0].ProviderID != "work" {
		t.Fatalf("search rows = %#v, want one work result", rows)
	}

	model, command = model.Update(KeyMsg{Key: "enter"})
	if model.UI.Mode != ModeBrowse || command == nil {
		t.Fatalf("search submit: mode=%q command=%v", model.UI.Mode, command)
	}
	message, ok := command().(CommandMsg)
	if !ok || message.Command.Kind != CommandSearch || message.Command.Query != "fix auth" {
		t.Fatalf("search command = %#v", message)
	}
}

func TestFilterAndCommandPalette(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "f"})
	model, _ = typeInput(model, "status:open provider:personal")
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command == nil || !model.UI.FilterActive {
		t.Fatalf("filter submit: active=%v command=%v", model.UI.FilterActive, command)
	}
	rows := model.VisibleTasks()
	if len(rows) != 1 || rows[0].ProviderID != "personal" {
		t.Fatalf("filtered rows = %#v, want one personal result", rows)
	}

	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "refresh")
	model, command = model.Update(KeyMsg{Key: "enter"})
	message, ok := command().(CommandMsg)
	if !ok || message.Command.Kind != CommandRefresh || message.Command.ProviderID != "work" {
		t.Fatalf("palette refresh command = %#v", message)
	}
}

func TestTaskGroupingByStatusAssigneeAndHierarchy(t *testing.T) {
	parentID := TaskID("parent")
	snapshot := testSnapshot()
	snapshot.Tasks = []Task{
		{ID: "open-b", ProviderID: "work", ListID: "backend", Title: "Open B", Status: "open", Assignee: "Bob"},
		{ID: "done", ProviderID: "work", ListID: "backend", Title: "Done", Status: "done", Assignee: "Alice"},
		{ID: parentID, ProviderID: "work", ListID: "backend", Title: "Parent", Status: "open", Assignee: "Alice"},
		{ID: "child", ProviderID: "work", ListID: "backend", ParentTaskID: &parentID, Title: "Child", Status: "open", Assignee: "Alice"},
		{ID: "unassigned", ProviderID: "work", ListID: "backend", Title: "Unassigned", Status: "open"},
	}
	model := New(snapshot)

	model.UI.GroupBy = TaskGroupStatus
	statusGroups := model.VisibleTaskGroups()
	if len(statusGroups) != 2 || statusGroups[0].Label != "done" || statusGroups[1].Label != "open" {
		t.Fatalf("status groups = %#v, want done/open", statusGroups)
	}
	if got := model.VisibleTasks()[0].Task.ID; got != "done" {
		t.Fatalf("status grouping first task = %q, want done", got)
	}

	model.UI.GroupBy = TaskGroupAssignee
	assigneeGroups := model.VisibleTaskGroups()
	if len(assigneeGroups) != 3 || assigneeGroups[0].Label != "Alice" || assigneeGroups[2].Label != "Unassigned" {
		t.Fatalf("assignee groups = %#v, want Alice/Bob/Unassigned", assigneeGroups)
	}

	model.UI.GroupBy = TaskGroupTasksSubtasks
	hierarchyGroups := model.VisibleTaskGroups()
	if len(hierarchyGroups) != 4 {
		t.Fatalf("hierarchy groups = %#v, want four roots", hierarchyGroups)
	}
	var hierarchy []TaskRow
	for _, group := range hierarchyGroups {
		hierarchy = append(hierarchy, group.Rows...)
	}
	if len(hierarchy) != 5 || hierarchy[2].Task.ID != parentID || hierarchy[3].Task.ID != "child" || hierarchy[3].HierarchyDepth != 1 {
		t.Fatalf("hierarchy rows = %#v, want parent followed by indented child", hierarchy)
	}
}

func TestTaskGroupingCommandPalette(t *testing.T) {
	for input, want := range map[string]TaskGroupMode{
		"group status":            TaskGroupStatus,
		"group assignee":          TaskGroupAssignee,
		"group tasks":             TaskGroupTasksSubtasks,
		"group by tasks/subtasks": TaskGroupTasksSubtasks,
		"ungroup":                 TaskGroupNone,
	} {
		command, err := ParseCommand(input)
		if err != nil {
			t.Fatalf("ParseCommand(%q): %v", input, err)
		}
		if command.Kind != CommandGroup || command.GroupBy != want {
			t.Fatalf("ParseCommand(%q) = %#v, want group %q", input, command, want)
		}
	}

	model := New(testSnapshot())
	model, command := model.Update(KeyMsg{Key: ":"})
	if command != nil {
		t.Fatal("opening command palette emitted a command")
	}
	model, _ = typeInput(model, "group status")
	model, command = model.Update(KeyMsg{Key: "enter"})
	if command == nil || model.UI.GroupBy != TaskGroupStatus {
		t.Fatalf("group submit: group=%q command=%v", model.UI.GroupBy, command)
	}
}

func TestTaskGroupsCanBeCollapsedAndExpanded(t *testing.T) {
	parentID := TaskID("parent")
	snapshot := Snapshot{
		Providers: []Provider{{ID: "work", Name: "Work", Type: ProviderTypeLocal, SyncState: SyncStateLocal}},
		Spaces:    []Space{{ID: "space", ProviderID: "work", Name: "Space", SyncState: SyncStateLocal}},
		Lists:     []List{{ID: "list", ProviderID: "work", SpaceID: "space", Name: "List", SyncState: SyncStateLocal}},
		Tasks: []Task{
			{ID: parentID, ProviderID: "work", ListID: "list", Title: "Parent", Status: "open", Assignee: "Alice"},
			{ID: "child", ProviderID: "work", ListID: "list", ParentTaskID: &parentID, Title: "Child", Status: "open", Assignee: "Alice"},
			{ID: "done", ProviderID: "work", ListID: "list", Title: "Done", Status: "done", Assignee: "Bob"},
			{ID: "other", ProviderID: "work", ListID: "list", Title: "Other", Status: "open", Assignee: "Bob"},
		},
	}

	tests := []struct {
		name string
		mode TaskGroupMode
	}{
		{name: "status", mode: TaskGroupStatus},
		{name: "assignee", mode: TaskGroupAssignee},
		{name: "tasks and subtasks", mode: TaskGroupTasksSubtasks},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := New(snapshot)
			model.UI.Focus = PanelTasks
			model.UI.GroupBy = test.mode
			model.selectTaskAt(0)

			groups := model.VisibleTaskGroups()
			if len(groups) == 0 {
				t.Fatal("grouping produced no groups")
			}
			selected := model.UI.SelectedTask
			beforeRows := len(model.VisibleTasks())
			collapsedRows := len(groups[0].Rows)

			model, _ = model.Update(KeyMsg{Runes: []rune{' '}})
			groups = model.VisibleTaskGroups()
			if !groups[0].Collapsed {
				t.Fatalf("first %s group was not collapsed: %#v", test.name, groups[0])
			}
			if got := len(model.VisibleTasks()); got != beforeRows-collapsedRows {
				t.Fatalf("visible rows after collapse = %d, want %d", got, beforeRows-collapsedRows)
			}
			if model.UI.SelectedTask != selected {
				t.Fatalf("selected task changed while collapsing: got %#v, want %#v", model.UI.SelectedTask, selected)
			}
			if _, ok := model.selectedTask(); !ok {
				t.Fatal("selected task could not be resolved while its group was collapsed")
			}
			if view := model.View(); !strings.Contains(view, "[+]") {
				t.Fatalf("collapsed group marker missing from view:\n%s", view)
			}

			model, command := model.Update(KeyMsg{Key: "x"})
			completed := commandMessage(t, command)
			if completed.TaskID != selected.TaskID || completed.ProviderID != selected.ProviderID {
				t.Fatalf("collapsed task command = %#v, want task %#v", completed, selected)
			}

			model, _ = model.Update(KeyMsg{Key: "space"})
			groups = model.VisibleTaskGroups()
			if groups[0].Collapsed {
				t.Fatalf("first %s group remained collapsed after toggle", test.name)
			}
			if len(model.VisibleTasks()) != beforeRows {
				t.Fatalf("visible rows after expand = %d, want %d", len(model.VisibleTasks()), beforeRows)
			}
			if model.UI.SelectedTask != selected {
				t.Fatalf("selected task changed while expanding: got %#v, want %#v", model.UI.SelectedTask, selected)
			}
		})
	}
}

func TestCollapsedGroupHeadersCanBeSelectedAndExpanded(t *testing.T) {
	snapshot := Snapshot{
		Providers: []Provider{{ID: "work", Name: "Work", Type: ProviderTypeLocal, SyncState: SyncStateLocal}},
		Spaces:    []Space{{ID: "space", ProviderID: "work", Name: "Space", SyncState: SyncStateLocal}},
		Lists:     []List{{ID: "list", ProviderID: "work", SpaceID: "space", Name: "List", SyncState: SyncStateLocal}},
		Tasks: []Task{
			{ID: "first", ProviderID: "work", ListID: "list", Title: "First", Status: "done"},
			{ID: "second", ProviderID: "work", ListID: "list", Title: "Second", Status: "open"},
		},
	}
	model := New(snapshot)
	model.UI.Focus = PanelTasks
	model.UI.GroupBy = TaskGroupStatus
	model.selectTaskAt(0)
	groups := model.VisibleTaskGroups()
	firstKey := taskGroupStateKey(TaskGroupStatus, groups[0].Key)

	model, _ = model.Update(KeyMsg{Key: "space"})
	model, _ = model.Update(KeyMsg{Key: "j"})
	if !model.UI.TaskHeaderSelected || model.UI.FocusedGroup == firstKey {
		t.Fatalf("down from collapsed header did not select the next header: %#v", model.UI)
	}
	model, _ = model.Update(KeyMsg{Key: "k"})
	if !model.UI.TaskHeaderSelected || model.UI.FocusedGroup != firstKey {
		t.Fatalf("up did not return to the collapsed header: %#v", model.UI)
	}

	model, _ = model.Update(KeyMsg{Key: "enter"})
	if model.VisibleTaskGroups()[0].Collapsed {
		t.Fatal("enter did not expand the selected collapsed header")
	}
}

func TestDeleteConfirmationPreservesProviderIdentity(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "tab"})
	model, command := model.Update(KeyMsg{Key: "d"})
	if command != nil || model.UI.Mode != ModeConfirm || !model.UI.HasPending {
		t.Fatalf("delete start: mode=%q pending=%v command=%v", model.UI.Mode, model.UI.HasPending, command)
	}
	model, command = model.Update(KeyMsg{Key: "n"})
	if command != nil || model.UI.Mode != ModeBrowse || model.UI.HasPending {
		t.Fatalf("delete cancel: mode=%q pending=%v command=%v", model.UI.Mode, model.UI.HasPending, command)
	}
	model, _ = model.Update(KeyMsg{Key: "d"})
	model, command = model.Update(KeyMsg{Key: "Y"})
	if command == nil {
		t.Fatal("confirmed delete emitted no command")
	}
	message, ok := command().(CommandMsg)
	if !ok || message.Command.Kind != CommandDeleteTask || message.Command.ProviderID != "work" || message.Command.TaskID != "same" {
		t.Fatalf("delete command = %#v", message)
	}
}

func TestMVPTaskActionsEmitScopedCommands(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "n"})
	if model.UI.Mode != ModeCreateTask {
		t.Fatalf("new task mode = %q, want %q", model.UI.Mode, ModeCreateTask)
	}
	model, _ = typeInput(model, "Write docs")
	model, command := model.Update(KeyMsg{Key: "enter"})
	created := commandMessage(t, command)
	if created.Kind != CommandCreateTask || created.ProviderID != "work" || created.ListID != "backend" || created.Title != "Write docs" {
		t.Fatalf("create command = %#v", created)
	}

	model, _ = model.Update(KeyMsg{Key: "tab"})
	model, _ = model.Update(KeyMsg{Key: "e"})
	model, _ = model.Update(KeyMsg{Key: "end"})
	model, _ = typeInput(model, " updated")
	model, command = model.Update(KeyMsg{Key: "enter"})
	updated := commandMessage(t, command)
	if updated.Kind != CommandUpdateTask || updated.ProviderID != "work" || updated.TaskID != "same" || updated.Title != "Fix auth updated" {
		t.Fatalf("update command = %#v", updated)
	}

	model, command = model.Update(KeyMsg{Key: "x"})
	completed := commandMessage(t, command)
	if completed.Kind != CommandCompleteTask || completed.ProviderID != "work" || completed.TaskID != "same" || !completed.Completed {
		t.Fatalf("complete command = %#v", completed)
	}
}

func TestRenderIsPureAndSearchRowsOmitProviderAndSyncStatus(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "/"})
	model, _ = typeInput(model, "shared")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	before := model
	view := model.View()
	if !reflect.DeepEqual(model, before) {
		t.Fatal("View mutated model state")
	}
	for _, value := range []string{"Shared work task", "Shared personal task"} {
		if !strings.Contains(view, value) {
			t.Fatalf("view does not contain %q:\n%s", value, view)
		}
	}
	for _, value := range []string{"Work|work", "Personal|personal", "<work>", "<personal>", "pending", "failed"} {
		if strings.Contains(view, value) {
			t.Fatalf("view still contains removed presentation field %q:\n%s", value, view)
		}
	}
}

func TestAsyncSnapshotSyncAndResize(t *testing.T) {
	model := NewEmpty(Options{})
	if model.Init() == nil {
		t.Fatal("empty model did not request cached data")
	}
	model, _ = model.Update(NewSnapshotMsg(testSnapshot()))
	model, _ = model.Update(SyncStateMsg{ProviderID: "work", State: SyncStateFailed, Error: "offline"})
	if model.Data.Providers[0].SyncState != SyncStateFailed || model.Status.Level != StatusError {
		t.Fatalf("sync update: provider=%#v status=%#v", model.Data.Providers[0], model.Status)
	}
	model, _ = model.Update(WindowSizeMsg{Width: 42, Height: 8})
	if model.UI.Width != 42 || model.UI.Height != 8 {
		t.Fatalf("size = %dx%d, want 42x8", model.UI.Width, model.UI.Height)
	}
	if lines := strings.Count(model.View(), "\n") + 1; lines > 8 {
		t.Fatalf("rendered %d lines for height 8", lines)
	}
}

func typeInput(model Model, input string) (Model, Cmd) {
	return model.Update(KeyMsg{Runes: []rune(input)})
}

func commandMessage(t *testing.T, command Cmd) AppCommand {
	t.Helper()
	if command == nil {
		t.Fatal("expected an emitted command")
	}
	result := command()
	message, ok := result.(CommandMsg)
	if !ok {
		t.Fatalf("command message = %T, want CommandMsg", result)
	}
	return message.Command
}

func testSnapshot() Snapshot {
	return Snapshot{
		Providers: []Provider{
			{ID: "work", Name: "Work", Type: "clickup", SyncState: SyncStateSynced},
			{ID: "personal", Name: "Personal", Type: "local", SyncState: SyncStateLocal},
		},
		Spaces: []Space{
			{ID: "engineering", ProviderID: "work", Name: "Engineering", SyncState: SyncStateSynced},
			{ID: "home", ProviderID: "personal", Name: "Home", SyncState: SyncStateLocal},
		},
		Lists: []List{
			{ID: "backend", ProviderID: "work", SpaceID: "engineering", Name: "Backend", SyncState: SyncStateSynced},
			{ID: "today", ProviderID: "personal", SpaceID: "home", Name: "Today", SyncState: SyncStateLocal},
		},
		Tasks: []Task{
			{ID: "same", ProviderID: "work", ListID: "backend", Title: "Fix auth", Description: "fix auth regression; shared", Status: "open", SyncState: SyncStatePending},
			{ID: "work-second", ProviderID: "work", ListID: "backend", Title: "Add metrics", Status: "open", Priority: "high", SyncState: SyncStateSynced},
			{ID: "same", ProviderID: "personal", ListID: "today", Title: "Shared personal task", Status: "done", SyncState: SyncStateFailed},
			{ID: "personal-shared", ProviderID: "personal", ListID: "today", Title: "Shared work task", Status: "open", SyncState: SyncStateLocal},
		},
	}
}

func TestCharmModelUsesBubbleTeaMessagesAndRendersPanels(t *testing.T) {
	model := NewCharmModel(New(testSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.Focus != PanelTasks {
		t.Fatalf("focus = %q, want tasks", model.CoreModel().UI.Focus)
	}

	view := model.View()
	for _, value := range []string{"TASK MANAGER", "SPACES / LISTS", "TASKS", "Work", "Personal"} {
		if !strings.Contains(view, value) {
			t.Fatalf("Charm view does not contain %q:\n%s", value, view)
		}
	}
}

func TestCharmModelRendersTaskDetails(t *testing.T) {
	model := NewCharmModel(New(testSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.Mode != ModeDetail {
		t.Fatalf("mode = %q, want %q", model.CoreModel().UI.Mode, ModeDetail)
	}
	view := model.View()
	for _, value := range []string{"TASK DETAIL", "DESCRIPTION", "fix auth regression; shared"} {
		if !strings.Contains(view, value) {
			t.Fatalf("detail view does not contain %q:\n%s", value, view)
		}
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.Mode != ModeBrowse {
		t.Fatalf("mode after escape = %q, want %q", model.CoreModel().UI.Mode, ModeBrowse)
	}
}

func TestCharmModelDispatchesCommandsThroughTeaCommands(t *testing.T) {
	var received AppCommand
	model := NewCharmModel(New(testSnapshot()), CharmOptions{
		OnCommand: func(command AppCommand) tea.Cmd {
			received = command
			return func() tea.Msg { return StatusMsg{Text: "handled"} }
		},
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(*CharmModel)
	if received.Kind != CommandCompleteTask || received.ProviderID != "work" || received.TaskID != "same" {
		t.Fatalf("received command = %#v", received)
	}
	if model.CoreModel().Status.Text != "Completing task locally; sync is asynchronous" {
		t.Fatalf("status = %#v, want local completion notice", model.CoreModel().Status)
	}
}

func TestCharmModelKeepsBubbleInputAndCoreInputInSync(t *testing.T) {
	model := NewCharmModel(New(testSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix auth")})
	model = updated.(*CharmModel)
	if got := model.CoreModel().UI.Input; got != "fix auth" {
		t.Fatalf("core input = %q, want %q", got, "fix auth")
	}
	if got := model.CoreModel().UI.SearchQuery; got != "fix auth" {
		t.Fatalf("live search query = %q, want %q", got, "fix auth")
	}
	if view := model.View(); !strings.Contains(view, "SEARCH") || !strings.Contains(view, "fix auth") {
		t.Fatalf("search view does not show the input: %s", view)
	}
}

func TestCharmPanelsScrollWithSelection(t *testing.T) {
	model := NewCharmModel(New(scrollableSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 50, Height: 16})
	model = updated.(*CharmModel)

	for index := 0; index < 8; index++ {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		model = updated.(*CharmModel)
	}
	if model.CoreModel().UI.TreeOffset == 0 {
		t.Fatal("hierarchy offset did not move with the selected node")
	}
	if view := model.View(); !strings.Contains(view, "List 08") {
		t.Fatalf("scrolled hierarchy does not show the selected list:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.TreeHorizontalOffset == 0 {
		t.Fatal("hierarchy horizontal offset did not move for overflowing text")
	}
	if view := model.View(); !strings.Contains(view, "hierarchy lab") {
		t.Fatalf("horizontally scrolled hierarchy does not show the node body:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.TaskHorizontalOffset == 0 {
		t.Fatal("task horizontal offset did not move for overflowing text")
	}
	if view := model.View(); !strings.Contains(view, "long task title") {
		t.Fatalf("horizontally scrolled tasks do not show the task body:\n%s", view)
	}
	for index := 0; index < 6; index++ {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		model = updated.(*CharmModel)
	}
	if model.CoreModel().UI.TaskOffset == 0 {
		t.Fatal("task offset did not move with the selected task")
	}
	if view := model.View(); !strings.Contains(view, "Task 06") {
		t.Fatalf("scrolled tasks do not show the selected task:\n%s", view)
	}
}

func scrollableSnapshot() Snapshot {
	snapshot := Snapshot{
		Providers: []Provider{{ID: "work", Name: "Work", Type: ProviderTypeLocal, SyncState: SyncStateLocal}},
		Spaces:    []Space{{ID: "space", ProviderID: "work", Name: "Space", SyncState: SyncStateLocal}},
	}
	for index := 0; index < 12; index++ {
		listID := ListID(fmt.Sprintf("list-%02d", index))
		snapshot.Lists = append(snapshot.Lists, List{
			ID:         listID,
			ProviderID: "work",
			SpaceID:    "space",
			Name:       fmt.Sprintf("List %02d with a very long hierarchy label", index),
			SyncState:  SyncStateLocal,
		})
	}
	for index := 0; index < 10; index++ {
		snapshot.Tasks = append(snapshot.Tasks, Task{
			ID:         TaskID(fmt.Sprintf("task-%02d", index)),
			ProviderID: "work",
			ListID:     "list-08",
			Title:      fmt.Sprintf("Task %02d with a very long task title", index),
			Status:     "open",
			SyncState:  SyncStateLocal,
		})
	}
	return snapshot
}
