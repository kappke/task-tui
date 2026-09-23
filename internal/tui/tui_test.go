package tui

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

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
		{key: "+", action: ActionExpandAll},
		{key: "=", action: ActionExpandAll},
		{key: "-", action: ActionCollapseAll},
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

func TestSwitchingToTasksLoadsTheHighlightedList(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "tab"})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider personal")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	model, _ = model.Update(KeyMsg{Key: "shift+tab"})
	model, command := model.Update(KeyMsg{Key: "tab"})
	if command == nil {
		t.Fatal("switching to the personal task panel did not request its tasks")
	}
	message := commandMessage(t, command)
	if message.Kind != CommandLoadCached || message.ProviderID != "personal" || message.ListID != "today" || message.FetchRemote {
		t.Fatalf("navigation load command = %#v, want personal/today", message)
	}

}

func TestTaskDetailViewOpensScrollsAndCloses(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Tasks[0].Description = strings.Repeat("This description explains the authentication regression and the required fix. ", 4)
	model := New(snapshot)
	model, _ = model.Update(KeyMsg{Key: "tab"})
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command == nil || model.UI.Mode != ModeDetail {
		t.Fatalf("open detail: mode=%q command=%v", model.UI.Mode, command)
	}
	if message := commandMessage(t, command); message.Kind != CommandFetchTask || message.ProviderID != "work" || message.TaskID != "same" {
		t.Fatalf("open detail fetch = %#v, want work/same", message)
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

func TestTaskDocumentRoundTripPreservesMetadataAndParsesEdits(t *testing.T) {
	task := testSnapshot().Tasks[0]
	document := RenderTaskDocument(task)
	document = strings.Replace(document, `title: "Fix auth"`, `title: "Updated auth"`, 1)
	document = strings.Replace(document, "fix auth regression; shared", "A longer body\nwith two lines.", 1)
	document = strings.Replace(document, `due_at: null`, `due_at: "2026-09-21T12:00:00Z"`, 1)

	updated, err := ParseTaskDocument(document, task)
	if err != nil {
		t.Fatalf("ParseTaskDocument: %v", err)
	}
	if updated.ID != task.ID || updated.ProviderID != task.ProviderID || updated.ListID != task.ListID {
		t.Fatalf("identity changed: %#v", updated)
	}
	if updated.Title != "Updated auth" || updated.Description != "A longer body\nwith two lines." {
		t.Fatalf("editable text = title %q, description %q", updated.Title, updated.Description)
	}
	if updated.DueAt == nil || updated.DueAt.Format(time.RFC3339) != "2026-09-21T12:00:00Z" {
		t.Fatalf("due date = %v", updated.DueAt)
	}
}

func TestTaskEditorAppliesSavedBufferAndDiscardsUnchangedBuffer(t *testing.T) {
	var received AppCommand
	model := NewCharmModel(New(testSnapshot()), CharmOptions{OnCommand: func(command AppCommand) tea.Cmd {
		received = command
		return nil
	}})
	row, ok := model.core.selectedTask()
	if !ok {
		t.Fatal("no selected task")
	}
	original := []byte(RenderTaskDocument(row.Task))
	file, err := os.CreateTemp("", "task-tui-test-*.md")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)
	changed := strings.Replace(string(original), `title: "Fix auth"`, `title: "Saved auth"`, 1)
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	model.pendingTaskEdit = &pendingTaskEdit{path: path, original: original}
	if command := model.finishTaskEditor(taskEditorFinishedMsg{path: path}); command != nil {
		_ = command()
	}
	if received.Title != "Saved auth" || received.Kind != CommandUpdateTask {
		t.Fatalf("saved task command = %#v", received)
	}

	file, err = os.CreateTemp("", "task-tui-test-*.md")
	if err != nil {
		t.Fatal(err)
	}
	path = file.Name()
	if _, err := file.Write(original); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	model.pendingTaskEdit = &pendingTaskEdit{path: path, original: original}
	if command := model.finishTaskEditor(taskEditorFinishedMsg{path: path}); command != nil {
		t.Fatal("unchanged buffer emitted a task command")
	}
	if !strings.Contains(model.core.Status.Text, "discarded") {
		t.Fatalf("unchanged buffer status = %q", model.core.Status.Text)
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

func TestSearchStaysWithinSelectedList(t *testing.T) {
	model := New(testSnapshot())
	model.UI.SearchActive = true
	model.UI.SearchQuery = "shared"

	rows := model.SearchResults()
	if len(rows) != 1 {
		t.Fatalf("search rows = %#v, want one result from the selected list", rows)
	}
	if rows[0].ProviderID != "work" || rows[0].ListID != "backend" {
		t.Fatalf("search row = %#v, want work/backend result", rows[0])
	}
}

func TestFilterAndCommandPalette(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider personal")
	model, _ = model.Update(KeyMsg{Key: "enter"})
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
	if !ok || message.Command.Kind != CommandFetchLists || message.Command.ProviderID != "personal" || message.Command.SpaceID != "home" {
		t.Fatalf("palette refresh command = %#v", message)
	}
}

func TestRefreshUsesSelectedTaskListWhenHierarchySelectionIsProvider(t *testing.T) {
	model := New(testSnapshot())
	model.UI.SelectedNode = TreeNodeRef{Kind: TreeNodeProvider, ProviderID: "work"}
	model.UI.Focus = PanelTasks

	model, command := model.Update(KeyMsg{Key: "r"})
	if command == nil {
		t.Fatal("refresh command is nil")
	}
	result := command()
	message, ok := result.(CommandMsg)
	if !ok {
		t.Fatalf("refresh command type = %T", result)
	}
	if message.Command.Kind != CommandFetchTasks || message.Command.ProviderID != "work" || message.Command.ListID != "backend" {
		t.Fatalf("refresh command = %#v, want work/backend", message.Command)
	}
}

func TestRefreshFetchesOnlyTheFocusedPaneScope(t *testing.T) {
	model := New(testSnapshot())
	model, command := model.Update(KeyMsg{Key: "r"})
	if command == nil {
		t.Fatal("hierarchy refresh did not emit a fetch")
	}
	message := commandMessage(t, command)
	if message.Kind != CommandFetchLists || message.ProviderID != "work" || message.SpaceID != "engineering" || message.ListID != "" {
		t.Fatalf("hierarchy refresh = %#v, want work/engineering lists", message)
	}

	model.UI.Focus = PanelTasks
	model, command = model.Update(KeyMsg{Key: "r"})
	if command == nil {
		t.Fatal("task refresh did not emit a fetch")
	}
	message = commandMessage(t, command)
	if message.Kind != CommandFetchTasks || message.ProviderID != "work" || message.ListID != "backend" || message.SpaceID != "" {
		t.Fatalf("task refresh = %#v, want work/backend tasks", message)
	}
}

func TestOnlyActiveProviderIsDisplayed(t *testing.T) {
	model := New(testSnapshot())
	if model.UI.ActiveProviderID != "work" {
		t.Fatalf("active provider = %q, want work", model.UI.ActiveProviderID)
	}
	if nodes := model.TreeNodes(); len(nodes) == 0 || nodes[0].ProviderID != "work" {
		t.Fatalf("hierarchy = %#v, want work provider only", nodes)
	}
	for _, row := range model.VisibleTasks() {
		if row.ProviderID != "work" {
			t.Fatalf("visible task = %#v, belongs to inactive provider", row)
		}
	}
}

func TestProviderSwitchCommandPalette(t *testing.T) {
	for _, input := range []string{"provider personal", "provider switch personal"} {
		command, err := ParseCommand(input)
		if err != nil {
			t.Fatalf("ParseCommand(%q): %v", input, err)
		}
		if command.Kind != CommandSwitchProvider || command.ProviderID != "personal" {
			t.Fatalf("ParseCommand(%q) = %#v, want personal provider switch", input, command)
		}
	}

	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider personal")
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command != nil || model.UI.ActiveProviderID != "personal" {
		t.Fatalf("provider switch: active=%q command=%v", model.UI.ActiveProviderID, command)
	}
	if nodes := model.TreeNodes(); len(nodes) == 0 || nodes[0].ProviderID != "personal" {
		t.Fatalf("switched hierarchy = %#v, want personal provider only", nodes)
	}
	if rows := model.VisibleTasks(); len(rows) == 0 || rows[0].ProviderID != "personal" {
		t.Fatalf("switched tasks = %#v, want personal tasks only", rows)
	}
}

func TestCommandCompletionCyclesCommandsAndOptions(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: ":"})
	if view := model.View(); !strings.Contains(view, "create") || !strings.Contains(view, "refresh") {
		t.Fatalf("command suggestions are not visible:\n%s", view)
	}

	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "create" {
		t.Fatalf("first command completion = %q, want %q", model.UI.Input, "create")
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "edit" {
		t.Fatalf("second command completion = %q, want %q", model.UI.Input, "edit")
	}
	model, _ = model.Update(KeyMsg{Key: "shift+tab"})
	if model.UI.Input != "create" {
		t.Fatalf("reverse command completion = %q, want %q", model.UI.Input, "create")
	}

	model.UI.Input = "group"
	model.UI.InputCursor = runeCount(model.UI.Input)
	model.resetCommandCompletion()
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "group" {
		t.Fatalf("parent command completion added a space: %q", model.UI.Input)
	}
	model, _ = model.Update(KeyMsg{Runes: []rune{' '}})
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "group status" {
		t.Fatalf("first group option = %q, want %q", model.UI.Input, "group status")
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "group assignee" {
		t.Fatalf("second group option = %q, want %q", model.UI.Input, "group assignee")
	}
	model, _ = model.Update(KeyMsg{Key: "shift+tab"})
	if model.UI.Input != "group status" {
		t.Fatalf("reverse group option = %q, want %q", model.UI.Input, "group status")
	}
}

func TestCharmCommandCompletionIsRendered(t *testing.T) {
	model := NewCharmModel(New(testSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(*CharmModel)
	view := model.View()
	if !strings.Contains(view, "create") || !strings.Contains(view, "refresh") {
		t.Fatalf("Charm command suggestions are not visible:\n%s", view)
	}
}

func TestCommandCompletionScrollsThroughOverflowingOptions(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(WindowSizeMsg{Width: 35, Height: 24})
	model, _ = model.Update(KeyMsg{Key: ":"})
	for index := 0; index < len(commandNames); index++ {
		model, _ = model.Update(KeyMsg{Key: "tab"})
	}
	line := model.commandCompletionLine(35)
	if !strings.Contains(line, "[help]") {
		t.Fatalf("selected final command is not visible in completion window:\n%s", line)
	}
	if strings.Contains(line, "~") {
		t.Fatalf("completion options were truncated instead of scrolled:\n%s", line)
	}
}

func TestEmptyHierarchyCommandFlowEmitsProviderScopedCreationCommands(t *testing.T) {
	model := New(Snapshot{Providers: []Provider{{ID: "local", Name: "Local", Type: ProviderTypeLocal}}})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "space create Personal")
	model, command := model.Update(KeyMsg{Key: "enter"})
	space := commandMessage(t, command)
	if space.Kind != CommandCreateSpace || space.ProviderID != "local" || space.Title != "Personal" {
		t.Fatalf("space command = %#v", space)
	}

	model.Data.Spaces = []Space{{ID: "personal", ProviderID: "local", Name: "Personal"}}
	model.UI.SelectedNode = TreeNodeRef{Kind: TreeNodeSpace, ProviderID: "local", SpaceID: "personal"}
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "list create Today")
	model, command = model.Update(KeyMsg{Key: "enter"})
	list := commandMessage(t, command)
	if list.Kind != CommandCreateList || list.ProviderID != "local" || list.SpaceID != "personal" || list.Title != "Today" {
		t.Fatalf("list command = %#v", list)
	}

	model = New(Snapshot{Providers: []Provider{{ID: "local", Name: "Local", Type: ProviderTypeLocal}}, Spaces: []Space{{ID: "personal", ProviderID: "local", Name: "Personal"}}})
	model, _ = model.Update(NewSnapshotMsg(Snapshot{
		Providers: model.Data.Providers,
		Spaces:    model.Data.Spaces,
		Lists:     []List{{ID: "today", ProviderID: "local", SpaceID: "personal", Name: "Today"}},
	}))
	if model.UI.SelectedNode.Kind != TreeNodeList || model.UI.SelectedNode.ListID != "today" {
		t.Fatalf("selection after first list reload = %#v, want today list", model.UI.SelectedNode)
	}
}

func TestEmptyHierarchyPaletteUsesTheRequestedCreationMode(t *testing.T) {
	model := New(Snapshot{Providers: []Provider{{ID: "local", Name: "Local", Type: ProviderTypeLocal}}})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "space create")
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command != nil || model.UI.Mode != ModeCreateSpace {
		t.Fatalf("space input mode = %q, command=%v", model.UI.Mode, command)
	}

	model, _ = model.Update(KeyMsg{Key: "esc"})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "list create")
	model, command = model.Update(KeyMsg{Key: "enter"})
	if command != nil || model.UI.Mode != ModeCreateList {
		t.Fatalf("list input mode = %q, command=%v", model.UI.Mode, command)
	}
}

func TestTaskCreatePaletteUsesTaskCreationMode(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "task create")
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command != nil || model.UI.Mode != ModeCreateTask {
		t.Fatalf("task input mode = %q, command=%v, want %q", model.UI.Mode, command, ModeCreateTask)
	}
	if !strings.Contains(model.View(), "NEW TASK") {
		t.Fatalf("task creation prompt does not identify a new task:\n%s", model.View())
	}
}

func TestOpeningListEmitsScopedLocalLoad(t *testing.T) {
	model := New(testSnapshot())
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command == nil {
		t.Fatal("opening list did not request local page")
	}
	message := commandMessage(t, command)
	if message.Kind != CommandLoadCached || message.ProviderID != "work" || message.SpaceID != "engineering" || message.ListID != "backend" || !message.FetchRemote {
		t.Fatalf("load command = %#v", message)
	}
}

func TestTaskGroupingByStatusAssigneeAndHierarchy(t *testing.T) {
	parentID := TaskID("parent")
	snapshot := testSnapshot()
	snapshot.Tasks = []Task{
		{ID: "open-b", ProviderID: "work", ListID: "backend", Title: "Open B", Status: "open", Priority: PriorityHigh, Assignee: "Bob"},
		{ID: "done", ProviderID: "work", ListID: "backend", Title: "Done", Status: "done", Priority: PriorityNormal, Assignee: "Alice"},
		{ID: parentID, ProviderID: "work", ListID: "backend", Title: "Parent", Status: "open", Assignee: "Alice"},
		{ID: "child", ProviderID: "work", ListID: "backend", ParentTaskID: &parentID, Title: "Child", Status: "open", Assignee: "Alice"},
		{ID: "unassigned", ProviderID: "work", ListID: "backend", Title: "Unassigned", Status: "open", Priority: PriorityNone},
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

	model.UI.GroupBy = TaskGroupPriority
	priorityGroups := model.VisibleTaskGroups()
	if len(priorityGroups) != 3 || priorityGroups[0].Label != "high" || priorityGroups[1].Label != "normal" || priorityGroups[2].Label != "None" {
		t.Fatalf("priority groups = %#v, want high/normal/None", priorityGroups)
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
		"group priority":          TaskGroupPriority,
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
		{name: "priority", mode: TaskGroupPriority},
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

func TestExpandAndCollapseAllHierarchyNodes(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "-"})

	nodes := model.TreeNodes()
	if len(nodes) != 1 {
		t.Fatalf("collapsed hierarchy nodes = %d, want one provider", len(nodes))
	}
	for _, node := range nodes {
		if node.Ref.Kind != TreeNodeProvider || node.Expanded {
			t.Fatalf("collapsed hierarchy node = %#v, want collapsed provider", node)
		}
	}
	if model.UI.SelectedNode.Kind != TreeNodeProvider || model.UI.SelectedNode.ProviderID != "work" {
		t.Fatalf("selection after collapse all = %#v, want work provider", model.UI.SelectedNode)
	}

	model, _ = model.Update(KeyMsg{Key: "+"})
	nodes = model.TreeNodes()
	if len(nodes) != 3 {
		t.Fatalf("expanded hierarchy nodes = %d, want provider, space, and list", len(nodes))
	}
	for _, node := range nodes {
		if node.Ref.Kind != TreeNodeProvider && node.Ref.Kind != TreeNodeSpace {
			continue
		}
		if !node.Expanded {
			t.Fatalf("expanded hierarchy node = %#v, want expanded", node)
		}
	}
}

func TestExpandAndCollapseAllTaskGroups(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Tasks = append(snapshot.Tasks, Task{
		ID:         "done",
		ProviderID: "work",
		ListID:     "backend",
		Title:      "Done task",
		Status:     "done",
		SyncState:  SyncStateLocal,
	})
	model := New(snapshot)
	model.UI.Focus = PanelTasks
	model.UI.GroupBy = TaskGroupStatus
	model.selectTaskRef(TaskRef{ProviderID: "work", TaskID: "same"})
	taskCount := len(model.VisibleTasks())

	model, _ = model.Update(KeyMsg{Key: "-"})
	groups := model.VisibleTaskGroups()
	if len(groups) != 2 {
		t.Fatalf("task groups = %d, want two", len(groups))
	}
	for _, group := range groups {
		if !group.Collapsed {
			t.Fatalf("task group = %#v, want collapsed", group)
		}
	}
	if got := len(model.VisibleTasks()); got != 0 {
		t.Fatalf("visible tasks after collapse all = %d, want zero", got)
	}
	if !model.UI.TaskHeaderSelected || model.UI.SelectedTask.TaskID != "same" {
		t.Fatalf("task selection after collapse all = %#v, want selected hidden task", model.UI)
	}

	model, _ = model.Update(KeyMsg{Key: "+"})
	for _, group := range model.VisibleTaskGroups() {
		if group.Collapsed {
			t.Fatalf("task group = %#v, want expanded", group)
		}
	}
	if got := len(model.VisibleTasks()); got != taskCount {
		t.Fatalf("visible tasks after expand all = %d, want %d", got, taskCount)
	}
	if model.UI.SelectedTask.TaskID != "same" || model.UI.TaskHeaderSelected {
		t.Fatalf("task selection after expand all = %#v, want selected task row", model.UI)
	}
}

func TestCollapsedTaskGroupHeadersScrollWhenTheyOverflow(t *testing.T) {
	snapshot := Snapshot{
		Providers: []Provider{{ID: "work", Name: "Work", Type: ProviderTypeLocal, SyncState: SyncStateLocal}},
		Spaces:    []Space{{ID: "space", ProviderID: "work", Name: "Space", SyncState: SyncStateLocal}},
		Lists:     []List{{ID: "list", ProviderID: "work", SpaceID: "space", Name: "List", SyncState: SyncStateLocal}},
	}
	for index := 0; index < 12; index++ {
		snapshot.Tasks = append(snapshot.Tasks, Task{
			ID:         TaskID(fmt.Sprintf("task-%02d", index)),
			ProviderID: "work",
			ListID:     "list",
			Title:      fmt.Sprintf("Task %02d", index),
			Status:     fmt.Sprintf("status-%02d", index),
		})
	}

	model := New(snapshot)
	model.UI.Focus = PanelTasks
	model.UI.GroupBy = TaskGroupStatus
	model.selectTaskAt(0)
	model, _ = model.Update(WindowSizeMsg{Width: 100, Height: 12})
	model, _ = model.Update(KeyMsg{Key: "-"})
	for index := 0; index < 11; index++ {
		model, _ = model.Update(KeyMsg{Key: "j"})
	}
	if model.UI.TaskOffset == 0 {
		t.Fatal("collapsed task group headers did not advance the task offset")
	}
	if view := model.View(); !strings.Contains(view, "STATUS-11") {
		t.Fatalf("plain renderer did not scroll to the selected collapsed group:\n%s", view)
	}

	charm := NewCharmModel(model, CharmOptions{})
	updated, _ := charm.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	charm = updated.(*CharmModel)
	if view := charm.View(); !strings.Contains(view, "STATUS-11") {
		t.Fatalf("Charm renderer did not keep the selected collapsed group visible:\n%s", view)
	}
}

func TestExpandedTaskGroupsScrollByHeadersAndRows(t *testing.T) {
	snapshot := Snapshot{
		Providers: []Provider{{ID: "work", Name: "Work", Type: ProviderTypeLocal, SyncState: SyncStateLocal}},
		Spaces:    []Space{{ID: "space", ProviderID: "work", Name: "Space", SyncState: SyncStateLocal}},
		Lists:     []List{{ID: "list", ProviderID: "work", SpaceID: "space", Name: "List", SyncState: SyncStateLocal}},
	}
	for index := 0; index < 3; index++ {
		snapshot.Tasks = append(snapshot.Tasks, Task{
			ID:         TaskID(fmt.Sprintf("open-%d", index)),
			ProviderID: "work",
			ListID:     "list",
			Title:      fmt.Sprintf("Open %d", index),
			Status:     "open",
		})
		snapshot.Tasks = append(snapshot.Tasks, Task{
			ID:         TaskID(fmt.Sprintf("done-%d", index)),
			ProviderID: "work",
			ListID:     "list",
			Title:      fmt.Sprintf("Done %d", index),
			Status:     "done",
		})
	}

	model := New(snapshot)
	model.UI.Focus = PanelTasks
	model.UI.GroupBy = TaskGroupStatus
	model, _ = model.Update(WindowSizeMsg{Width: 100, Height: 40})
	model.selectTaskAt(0)

	for index := 0; index < 3; index++ {
		model, _ = model.Update(KeyMsg{Key: "j"})
	}
	if !model.UI.TaskHeaderSelected || model.UI.FocusedGroup != taskGroupStateKey(TaskGroupStatus, "open") {
		t.Fatalf("next group selection = %#v, want open header", model.UI)
	}
	if model.UI.TaskOffset != 0 {
		t.Fatalf("task offset at next group = %d, want no movement within viewport", model.UI.TaskOffset)
	}
	view := model.View()
	if !strings.Contains(view, "Done 2") || !strings.Contains(view, "OPEN") {
		t.Fatalf("group transition did not scroll smoothly:\n%s", view)
	}

	model, _ = model.Update(WindowSizeMsg{Width: 100, Height: 12})
	if model.UI.TaskOffset != 3 {
		t.Fatalf("task offset after shrinking viewport = %d, want smooth visual offset 3", model.UI.TaskOffset)
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
	if !strings.Contains(view, "Fix auth") {
		t.Fatalf("view does not contain the matching task from the selected list:\n%s", view)
	}
	if strings.Contains(view, "Shared work task") || strings.Contains(view, "Shared personal task") {
		t.Fatalf("view contains a task from another list:\n%s", view)
	}
	for _, value := range []string{"Work|work", "Personal|personal", "<work>", "<personal>", "pending", "failed"} {
		if strings.Contains(view, value) {
			t.Fatalf("view still contains removed presentation field %q:\n%s", value, view)
		}
	}
}

func TestTaskTableRendersMetadataAndUppercasesStatus(t *testing.T) {
	snapshot := testSnapshot()
	estimate := 90 * time.Minute
	tracked := 45 * time.Minute
	due := time.Date(2026, time.September, 18, 0, 0, 0, 0, time.UTC)
	snapshot.Tasks[0].Title = "A task title that is intentionally much longer than the name column"
	snapshot.Tasks[0].Status = "open"
	snapshot.Tasks[0].Assignee = "alice, Bob"
	snapshot.Tasks[0].Priority = PriorityHigh
	snapshot.Tasks[0].TimeEstimate = &estimate
	snapshot.Tasks[0].TimeTracked = &tracked
	snapshot.Tasks[0].DueAt = &due
	model := New(snapshot)
	model, _ = model.Update(WindowSizeMsg{Width: 230, Height: 20})

	view := model.View()
	for _, value := range []string{
		"TASK", "STATUS", "ASSIGNEES", "PRIORITY", "TIME ESTIMATE", "TIME TRACKED", "DUE DATE",
		"OPEN", "alice, Bob", "HIGH", "1h 30m", "45m", "2026-09-18", "~",
	} {
		if !strings.Contains(view, value) {
			t.Fatalf("task table does not contain %q:\n%s", value, view)
		}
	}
	if strings.Contains(view, " status ") || strings.Contains(view, "(open)") {
		t.Fatalf("task status was not rendered as an uppercase table value:\n%s", view)
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
	for _, value := range []string{"TASK MANAGER", "SPACES / LISTS", "TASKS", "LIST Backend", "Work"} {
		if !strings.Contains(view, value) {
			t.Fatalf("Charm view does not contain %q:\n%s", value, view)
		}
	}
	if strings.Contains(view, "Personal") {
		t.Fatalf("Charm view contains inactive provider:\n%s", view)
	}
}

func TestCharmModelRendersTaskDetails(t *testing.T) {
	model := NewCharmModel(New(testSnapshot()), CharmOptions{})
	defer func() {
		if model.pendingTaskEdit != nil {
			_ = os.Remove(model.pendingTaskEdit.path)
		}
	}()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.Mode != ModeBrowse || model.pendingTaskEdit == nil {
		t.Fatalf("mode = %q, pending editor=%v", model.CoreModel().UI.Mode, model.pendingTaskEdit != nil)
	}
	if data, err := os.ReadFile(model.pendingTaskEdit.path); err != nil || !strings.Contains(string(data), "fix auth regression; shared") {
		t.Fatalf("editor buffer = %q, error=%v", data, err)
	}
}

func TestCharmEnterFetchesOpenedTask(t *testing.T) {
	var requested []AppCommand
	model := NewCharmModel(New(testSnapshot()), CharmOptions{
		OnCommand: func(command AppCommand) tea.Cmd {
			requested = append(requested, command)
			return nil
		},
	})
	defer func() {
		if model.pendingTaskEdit != nil {
			_ = os.Remove(model.pendingTaskEdit.path)
			if model.pendingTaskEdit.script != "" {
				_ = os.Remove(model.pendingTaskEdit.script)
			}
		}
	}()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*CharmModel)
	if len(requested) != 2 || requested[1].Kind != CommandFetchTask || requested[1].ProviderID != "work" || requested[1].TaskID != "same" {
		t.Fatalf("task fetch requests = %#v, want work/same", requested)
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
