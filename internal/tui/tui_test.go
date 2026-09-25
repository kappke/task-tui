package tui

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
		{key: "o", action: ActionSort},
		{key: "c", action: ActionConfigureColumns},
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

func TestListViewFilterAndGroupingAreRememberedPerList(t *testing.T) {
	model := New(testSnapshot())
	filter, err := ParseFilter("status:open")
	if err != nil {
		t.Fatal(err)
	}
	model.UI.Filter = filter
	model.UI.FilterActive = true
	model.UI.GroupBy = TaskGroupStatus
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "sort status asc")
	model, _ = model.Update(KeyMsg{Key: "enter"})

	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider personal")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if model.UI.SelectedNode.ListID != "today" {
		t.Fatalf("selected list after provider switch = %q, want today", model.UI.SelectedNode.ListID)
	}
	if model.UI.FilterActive || model.UI.GroupBy != TaskGroupNone || len(model.UI.SortBy) != 0 {
		t.Fatalf("new list inherited previous view: filter=%#v active=%v group=%q sort=%v", model.UI.Filter, model.UI.FilterActive, model.UI.GroupBy, model.UI.SortBy)
	}

	model, _ = model.Update(KeyMsg{Key: "f"})
	model, _ = typeInput(model, "priority:high")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "group priority")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "sort priority desc, title asc")
	model, _ = model.Update(KeyMsg{Key: "enter"})

	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider work")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if model.UI.SelectedNode.ListID != "backend" || !model.UI.FilterActive || model.UI.Filter.String() != "status:open" || model.UI.GroupBy != TaskGroupStatus || sortCriteriaString(model.UI.SortBy) != "status asc" {
		t.Fatalf("restored work list view: list=%q filter=%q active=%v group=%q sort=%v", model.UI.SelectedNode.ListID, model.UI.Filter.String(), model.UI.FilterActive, model.UI.GroupBy, model.UI.SortBy)
	}

	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider personal")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if model.UI.SelectedNode.ListID != "today" || !model.UI.FilterActive || model.UI.Filter.String() != "priority:high" || model.UI.GroupBy != TaskGroupPriority || sortCriteriaString(model.UI.SortBy) != "priority desc, task asc" {
		t.Fatalf("restored personal list view: list=%q filter=%q active=%v group=%q sort=%v", model.UI.SelectedNode.ListID, model.UI.Filter.String(), model.UI.FilterActive, model.UI.GroupBy, model.UI.SortBy)
	}
}

func TestTaskColumnsAreDiscoveredDisplayedAndConfiguredPerList(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.TaskColumns = []ListTaskColumn{
		{ProviderID: "work", ListID: "backend", ID: "custom:roi", Name: "ROI", Type: "number"},
	}
	snapshot.TaskColumnValues = []TaskColumnValueSet{
		{ProviderID: "work", TaskID: "same", Values: map[string]string{"custom:roi": "13"}},
	}
	model := New(snapshot)
	header := model.taskTableHeaderLine(0)
	if !strings.Contains(header, "ROI") {
		t.Fatalf("discovered list column missing from header: %q", header)
	}
	rows := model.VisibleTasks()
	if len(rows) == 0 || !strings.Contains(model.taskTableLine(rows[0], "  ", 0), "13") {
		t.Fatalf("task column value was not displayed: %#v", rows)
	}
	renderModel := model
	renderModel.UI.ColumnPreferences = []TaskColumnPreference{
		{ID: taskColumnStatus, Visible: false},
		{ID: taskColumnAssignees, Visible: false},
		{ID: taskColumnPriority, Visible: false},
		{ID: taskColumnEstimate, Visible: false},
		{ID: taskColumnTracked, Visible: false},
		{ID: taskColumnDue, Visible: false},
	}
	if lines := strings.Join(renderModel.taskLines(120, 4), "\n"); !strings.Contains(lines, "13") {
		t.Fatalf("visible-row projection omitted dynamic column values:\n%s", lines)
	}

	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "columns")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if model.UI.Mode != ModeColumnConfig {
		t.Fatalf("columns command mode = %q, want column configuration", model.UI.Mode)
	}
	model, _ = model.Update(KeyMsg{Key: "j"})
	model, _ = model.Update(KeyMsg{Key: "space"})
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if strings.Contains(model.taskTableHeaderLine(0), "STATUS") {
		t.Fatalf("hidden status column remains visible: %q", model.taskTableHeaderLine(0))
	}

	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider personal")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if !strings.Contains(model.taskTableHeaderLine(0), "STATUS") || strings.Contains(model.taskTableHeaderLine(0), "ROI") {
		t.Fatalf("personal list inherited work columns: %q", model.taskTableHeaderLine(0))
	}
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "provider work")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if strings.Contains(model.taskTableHeaderLine(0), "STATUS") || !strings.Contains(model.taskTableHeaderLine(0), "ROI") {
		t.Fatalf("work list preferences were not restored: %q", model.taskTableHeaderLine(0))
	}

	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "columns")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	for range 7 {
		model, _ = model.Update(KeyMsg{Key: "j"})
	}
	model, _ = model.Update(KeyMsg{Key: "+"})
	model, _ = model.Update(KeyMsg{Key: "enter"})
	preference, ok := model.taskColumnPreference("custom:roi")
	if !ok || preference.Width != 13 {
		t.Fatalf("custom column preference = %#v, exists=%v; want width 13", preference, ok)
	}
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "columns")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	for range 20 {
		model, _ = model.Update(KeyMsg{Key: "-"})
	}
	model, _ = model.Update(KeyMsg{Key: "enter"})
	preference, ok = model.taskColumnPreference("custom:roi")
	columns := model.taskTableColumns(0)
	columnWidth := 0
	for _, column := range columns {
		if column.ID == "custom:roi" {
			columnWidth = column.Width
		}
	}
	if !ok || preference.Width != 1 || columnWidth != 1 {
		t.Fatalf("minimum custom column width = %#v, exists=%v, visible columns=%#v, list=%#v; want 1", preference, ok, columns, model.UI.SelectedNode)
	}
}

func TestTaskColumnsCanBeReorderedAndFixated(t *testing.T) {
	model := New(testSnapshot())
	model.UI.SelectedNode = TreeNodeRef{Kind: TreeNodeList, ProviderID: "work", SpaceID: "engineering", ListID: "backend"}
	model, _ = model.Update(KeyMsg{Key: "c"})
	if model.UI.Mode != ModeColumnConfig {
		t.Fatalf("column configuration mode = %q, want column config", model.UI.Mode)
	}
	model, _ = model.Update(KeyMsg{Key: "j"})
	model, _ = model.Update(KeyMsg{Key: "]"})
	columns := model.availableTaskColumns()
	if columns[0].ID != taskColumnTask || columns[1].ID != taskColumnAssignees || columns[2].ID != taskColumnStatus {
		t.Fatalf("column order after moving status right = %#v", columns)
	}
	model, _ = model.Update(KeyMsg{Key: "f"})
	for _, id := range []string{taskColumnTask, taskColumnAssignees, taskColumnStatus} {
		preference, ok := model.taskColumnPreference(id)
		if !ok || !preference.Fixed {
			t.Errorf("column %q fixation preference = %#v, exists=%v; want fixed prefix", id, preference, ok)
		}
	}
	if !strings.Contains(model.columnConfigLines(120)[4], "[F]") {
		t.Fatalf("column configuration did not display fixation state:\n%s", strings.Join(model.columnConfigLines(120), "\n"))
	}
	state := model.UI.ListViews[ListViewKey{ProviderID: "work", ListID: "backend"}]
	if len(state.Columns) < 3 || state.Columns[0].ID != taskColumnTask || state.Columns[0].Order != 1 ||
		state.Columns[1].ID != taskColumnAssignees || state.Columns[1].Order != 2 ||
		state.Columns[2].ID != taskColumnStatus || state.Columns[2].Order != 3 || !state.Columns[2].Fixed {
		t.Fatalf("per-list order/fixation state = %#v", state.Columns)
	}

	model, _ = model.Update(KeyMsg{Key: "j"})
	model, _ = model.Update(KeyMsg{Key: "["})
	columns = model.availableTaskColumns()
	if columns[2].ID != taskColumnPriority || columns[3].ID != taskColumnStatus || model.fixedTaskColumnCount(columns) != 4 {
		t.Fatalf("reordering before a fixated column did not preserve the fixed prefix: columns=%#v preferences=%#v", columns, model.UI.ColumnPreferences)
	}
	model, _ = model.Update(KeyMsg{Key: "f"})
	if model.fixedTaskColumnCount(model.availableTaskColumns()) != 2 {
		t.Fatalf("unfixating the selected column left an invalid prefix: %#v", model.UI.ColumnPreferences)
	}
}

func TestFixatedColumnsStayVisibleDuringHorizontalScroll(t *testing.T) {
	model := New(testSnapshot())
	model.UI.ColumnPreferences = []TaskColumnPreference{
		{ID: "alpha", Visible: true, Width: 3, Order: 1, Fixed: true},
		{ID: "beta", Visible: true, Width: 3, Order: 2, Fixed: true},
		{ID: "gamma", Visible: true, Width: 3, Order: 3},
	}
	columns := []taskTableColumn{
		{ID: "alpha", Label: "ALPHA", Width: 3},
		{ID: "beta", Label: "BETA", Width: 3},
		{ID: "gamma", Label: "GAMMA", Width: 3},
	}
	row := TaskRow{ColumnValues: map[string]string{"alpha": "a1", "beta": "b1", "gamma": "gamma"}}
	left := model.taskTableLineAtOffset(row, "  ", columns, 20, 0)
	scrolled := model.taskTableLineAtOffset(row, "  ", columns, 20, 2)
	const fixedPrefixWidth = 12
	if left[:fixedPrefixWidth] != scrolled[:fixedPrefixWidth] {
		t.Fatalf("fixed prefix changed during scroll: before %q after %q", left[:fixedPrefixWidth], scrolled[:fixedPrefixWidth])
	}
	if left[fixedPrefixWidth:] == scrolled[fixedPrefixWidth:] {
		t.Fatalf("scrolling did not move the unfixed columns: before %q after %q", left, scrolled)
	}
}

func TestTaskPanelAndGroupHeadingsStayFixedWithoutFixatedColumns(t *testing.T) {
	model := New(testSnapshot())
	model.UI.SelectedNode = TreeNodeRef{Kind: TreeNodeList, ProviderID: "work", SpaceID: "engineering", ListID: "backend"}
	model.UI.Focus = PanelTasks
	model.UI.GroupBy = TaskGroupStatus
	model.UI.TaskHorizontalOffset = 4
	if fixed := model.fixedTaskColumnCount(model.taskTableColumns(60)); fixed != 0 {
		t.Fatalf("test has %d fixated columns; want none", fixed)
	}

	checkHeadings := func(name string, lines []string) {
		t.Helper()
		if len(lines) < 3 || !strings.Contains(lines[0], "TASKS | LIST Backend") {
			t.Errorf("%s panel heading scrolled with table content: %#v", name, lines)
		}
		for _, line := range lines {
			if strings.Contains(line, "[-]") {
				if !strings.Contains(line, "  [-]") || !strings.Contains(line, "OPEN") {
					t.Errorf("%s group heading scrolled with table content: %q", name, line)
				}
				return
			}
		}
		t.Errorf("%s view omitted the task group heading: %#v", name, lines)
	}
	checkHeadings("core", model.taskLines(60, 12))
	checkHeadings("Charm", NewCharmModel(model, CharmOptions{}).charmTaskLines(60, 12))
}

func TestStatusColorRangeCannotBleedIntoFixatedColumns(t *testing.T) {
	columns := []taskTableColumn{
		{ID: "pinned", Width: 4},
		{ID: taskColumnStatus, Width: 4},
		{ID: "later", Width: 4},
	}
	const fixedCount = 1
	const markerWidth = 2
	fixedWidth := taskTableFixedPrefixWidth(columns, fixedCount, markerWidth)
	for _, test := range []struct {
		name      string
		offset    int
		wantStart int
		wantEnd   int
	}{
		{name: "partly hidden behind pinned prefix", offset: 3, wantStart: fixedWidth, wantEnd: fixedWidth + 1},
		{name: "fully hidden behind pinned prefix", offset: 4, wantStart: fixedWidth, wantEnd: fixedWidth},
	} {
		t.Run(test.name, func(t *testing.T) {
			start, end, ok := taskStatusCellRange(columns, fixedCount, test.offset, markerWidth)
			if !ok || start != test.wantStart || end != test.wantEnd {
				t.Fatalf("status color range = (%d, %d, %v), want (%d, %d, true)", start, end, ok, test.wantStart, test.wantEnd)
			}
		})
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

func TestSubmittingEmptySearchClearsSearchState(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: "/"})
	model, _ = typeInput(model, "fix auth")
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if !model.UI.SearchActive {
		t.Fatal("search did not become active")
	}
	if got := len(model.VisibleTasks()); got != 1 {
		t.Fatalf("search results = %d, want 1", got)
	}

	model, _ = model.Update(KeyMsg{Key: "/"})
	for range runeCount(model.UI.Input) {
		model, _ = model.Update(KeyMsg{Key: "backspace"})
	}
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command != nil {
		t.Fatalf("clearing search emitted command %v", command)
	}
	if model.UI.Mode != ModeBrowse || model.UI.SearchActive || model.UI.SearchQuery != "" {
		t.Fatalf("search state after clearing = mode %q, active %v, query %q", model.UI.Mode, model.UI.SearchActive, model.UI.SearchQuery)
	}
	if model.Status.Level != StatusInfo || model.Status.Text != "Search cleared" {
		t.Fatalf("clear status = %#v, want informational Search cleared", model.Status)
	}
	if got := len(model.VisibleTasks()); got != 2 {
		t.Fatalf("tasks after clearing search = %d, want 2", got)
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

func TestCompositeSortUsesColumnValuesInSelectedOrderAndCanBeEdited(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.TaskColumns = []ListTaskColumn{{ProviderID: "work", ListID: "backend", ID: "custom:roi", Name: "ROI", Type: "number"}}
	snapshot.TaskColumnValues = []TaskColumnValueSet{
		{ProviderID: "work", TaskID: "same", Values: map[string]string{"custom:roi": "2"}},
		{ProviderID: "work", TaskID: "work-second", Values: map[string]string{"custom:roi": "13"}},
		{ProviderID: "work", TaskID: "third", Values: map[string]string{"custom:roi": "9"}},
		{ProviderID: "work", TaskID: "fourth", Values: map[string]string{"custom:roi": "9"}},
	}
	snapshot.Tasks = []Task{
		{ID: "same", ProviderID: "work", ListID: "backend", Title: "Alpha", Status: "open", Priority: PriorityHigh},
		{ID: "work-second", ProviderID: "work", ListID: "backend", Title: "Bravo", Status: "open", Priority: PriorityNormal},
		{ID: "third", ProviderID: "work", ListID: "backend", Title: "Charlie", Status: "done", Priority: PriorityUrgent},
		{ID: "fourth", ProviderID: "work", ListID: "backend", Title: "Delta", Status: "done", Priority: PriorityLow},
	}
	model := New(snapshot)

	model, _ = model.Update(KeyMsg{Key: "o"})
	if model.UI.Mode != ModeSort || model.UI.Input != "" {
		t.Fatalf("new sort editor = mode %q input %q", model.UI.Mode, model.UI.Input)
	}
	model, _ = typeInput(model, "ROI asc, priority desc")
	model, command := model.Update(KeyMsg{Key: "enter"})
	if got := commandMessage(t, command); got.Kind != CommandSort || sortCriteriaString(got.Sort) != "custom:roi asc, priority desc" {
		t.Fatalf("sort command = %#v", got)
	}
	if got, want := visibleTaskIDs(model), []TaskID{"same", "third", "fourth", "work-second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ROI then priority order = %v, want %v", got, want)
	}

	model, _ = model.Update(KeyMsg{Key: "o"})
	if model.UI.Input != "custom:roi asc, priority desc" {
		t.Fatalf("sort editor did not preserve current criteria: %q", model.UI.Input)
	}
	model, _ = model.Update(KeyMsg{Key: "home"})
	for range runeCount("custom:roi asc, ") {
		model, _ = model.Update(KeyMsg{Key: "right"})
	}
	for range runeCount("custom:roi asc, ") {
		model, _ = model.Update(KeyMsg{Key: "backspace"})
	}
	model, _ = model.Update(KeyMsg{Key: "end"})
	model, _ = typeInput(model, ", custom:roi asc")
	model, command = model.Update(KeyMsg{Key: "enter"})
	commandMessage(t, command)
	if got, want := visibleTaskIDs(model), []TaskID{"third", "same", "work-second", "fourth"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("edited priority-first order = %v, want %v", got, want)
	}
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "sort ROI desc")
	model, command = model.Update(KeyMsg{Key: "enter"})
	commandMessage(t, command)
	if got, want := visibleTaskIDs(model), []TaskID{"work-second", "third", "fourth", "same"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("descending dynamic-column order = %v, want %v", got, want)
	}
}

func TestCompositeFilterSupportsBooleanColumnRulesAndDynamicColumns(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.TaskColumns = []ListTaskColumn{{ProviderID: "work", ListID: "backend", ID: "custom:roi", Name: "ROI", Type: "number"}}
	snapshot.TaskColumnValues = []TaskColumnValueSet{
		{ProviderID: "work", TaskID: "same", Values: map[string]string{"custom:roi": "2"}},
		{ProviderID: "work", TaskID: "work-second", Values: map[string]string{"custom:roi": "13"}},
		{ProviderID: "work", TaskID: "third", Values: map[string]string{"custom:roi": "9"}},
	}
	snapshot.Tasks = []Task{
		{ID: "same", ProviderID: "work", ListID: "backend", Title: "Alpha", Status: "open", Priority: PriorityHigh, Assignee: "Ada"},
		{ID: "work-second", ProviderID: "work", ListID: "backend", Title: "Bravo", Status: "open", Priority: PriorityNormal, Assignee: "Bob"},
		{ID: "third", ProviderID: "work", ListID: "backend", Title: "Charlie", Status: "done", Priority: PriorityUrgent, Assignee: "Ada"},
	}
	model := New(snapshot)
	model, _ = model.Update(KeyMsg{Key: "f"})
	model, _ = typeInput(model, `status:open AND (priority:high OR "ROI">=10) AND NOT assignee:Bob`)
	model, command := model.Update(KeyMsg{Key: "enter"})
	if commandMessage(t, command).Kind != CommandFilter {
		t.Fatal("filter did not emit the normalized filter command")
	}
	if got, want := visibleTaskIDs(model), []TaskID{"same"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("composite filter matches = %v, want %v", got, want)
	}

	model, _ = model.Update(KeyMsg{Key: "f"})
	if model.UI.Input != `status:open AND (priority:high OR "ROI">=10) AND NOT assignee:Bob` {
		t.Fatalf("filter editor did not preserve current expression: %q", model.UI.Input)
	}
	for range runeCount(model.UI.Input) {
		model, _ = model.Update(KeyMsg{Key: "backspace"})
	}
	model, _ = typeInput(model, `"ROI">=9 OR status:done`)
	model, _ = model.Update(KeyMsg{Key: "enter"})
	if got, want := visibleTaskIDs(model), []TaskID{"work-second", "third"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("OR filter matches = %v, want %v", got, want)
	}
}

func TestCompositeFilterRejectsUnknownColumnsAndMalformedExpressions(t *testing.T) {
	for _, input := range []string{"status:open OR", "(status:open", `status:"unfinished`, "unknown:value"} {
		model := New(testSnapshot())
		model, _ = model.Update(KeyMsg{Key: "f"})
		model, _ = typeInput(model, input)
		model, command := model.Update(KeyMsg{Key: "enter"})
		if command != nil || model.UI.Mode != ModeFilter || model.Status.Level != StatusError {
			t.Fatalf("filter %q result: mode=%q command=%v status=%#v", input, model.UI.Mode, command, model.Status)
		}
	}
}

func TestBareFilterAndSortCommandsOpenTheirEditors(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "filter status:open")
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command == nil || !model.UI.FilterActive || model.UI.Filter.String() != "status:open" {
		t.Fatalf("filter application: active=%v filter=%q command=%v", model.UI.FilterActive, model.UI.Filter.String(), command)
	}

	model, _ = model.Update(KeyMsg{Key: "f"})
	if model.UI.Mode != ModeFilter || model.UI.Input != "status:open" {
		t.Fatalf("filter shortcut reopened mode %q with input %q", model.UI.Mode, model.UI.Input)
	}

	model, _ = model.Update(KeyMsg{Key: "esc"})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "filter")
	model, command = model.Update(KeyMsg{Key: "enter"})
	if command != nil || model.UI.Mode != ModeFilter || model.UI.Input != "status:open" {
		t.Fatalf(":filter reopened mode %q with input %q and command %v", model.UI.Mode, model.UI.Input, command)
	}

	model, _ = model.Update(KeyMsg{Key: "esc"})
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "sort")
	model, command = model.Update(KeyMsg{Key: "enter"})
	if command != nil || model.UI.Mode != ModeSort {
		t.Fatalf(":sort opened mode %q with command %v", model.UI.Mode, command)
	}
}

func TestFilterEditorRecoversTextFromSavedListState(t *testing.T) {
	model := New(testSnapshot())
	model.Data.Tasks[1].Status = "done"
	parsed, err := ParseFilter("status:open")
	if err != nil {
		t.Fatal(err)
	}
	model.UI.Filter = Filter{Expression: parsed.Expression}
	model.UI.FilterActive = true
	key := ListViewKey{ProviderID: "work", ListID: "backend"}
	model.UI.ListViews = make(map[ListViewKey]ListViewState)
	model.UI.ListViews[key] = ListViewState{Filter: "status:open"}
	if got := len(model.VisibleTasks()); got != 1 {
		t.Fatalf("saved filter result count = %d, want one applied result", got)
	}

	model, _ = model.Update(KeyMsg{Key: "f"})
	if model.UI.Mode != ModeFilter || model.UI.Input != "status:open" {
		t.Fatalf("reopened saved filter = mode %q input %q", model.UI.Mode, model.UI.Input)
	}
}

func TestCharmReopensTheAppliedFilterTextForEditing(t *testing.T) {
	model := NewCharmModel(New(testSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("filter status:open")})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*CharmModel)
	if !model.core.UI.FilterActive || model.core.UI.Filter.String() != "status:open" {
		t.Fatalf("applied Charm filter = active %v expression %q", model.core.UI.FilterActive, model.core.UI.Filter.String())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updated.(*CharmModel)
	if model.core.UI.Mode != ModeFilter || model.core.UI.Input != "status:open" || !strings.Contains(model.View(), "FILTER: status:open") {
		t.Fatalf("reopened Charm filter = mode %q input %q\n%s", model.core.UI.Mode, model.core.UI.Input, model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("filter")})
	model = updated.(*CharmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*CharmModel)
	if model.core.UI.Mode != ModeFilter || model.core.UI.Input != "status:open" {
		t.Fatalf("bare palette filter reopened with mode %q input %q", model.core.UI.Mode, model.core.UI.Input)
	}
}

func TestFilterAndSortEditorsOfferContextualCompletions(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.TaskColumns = []ListTaskColumn{{ProviderID: "work", ListID: "backend", ID: "custom:roi", Name: "ROI", Type: "number"}}
	snapshot.TaskColumnValues = []TaskColumnValueSet{
		{ProviderID: "work", TaskID: "same", Values: map[string]string{"custom:roi": "2"}},
		{ProviderID: "work", TaskID: "work-second", Values: map[string]string{"custom:roi": "13"}},
	}
	model := New(snapshot)
	model, _ = model.Update(KeyMsg{Key: "f"})
	if line := model.commandCompletionLine(160); !strings.Contains(line, "status") || !strings.Contains(line, "priority") {
		t.Fatalf("filter field completions missing columns:\n%s", line)
	}
	model, _ = typeInput(model, "status")
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "status:" {
		t.Fatalf("status operator completion = %q, want status:", model.UI.Input)
	}
	model, _ = typeInput(model, "open")
	if line := model.commandCompletionLine(160); !strings.Contains(line, "open") {
		t.Fatalf("status value completions omit open:\n%s", line)
	}

	model.UI.Input = "priority:"
	model.UI.InputCursor = runeCount(model.UI.Input)
	model.resetCommandCompletion()
	if line := model.commandCompletionLine(160); !strings.Contains(line, "high") || !strings.Contains(line, "urgent") {
		t.Fatalf("priority value completions missing levels:\n%s", line)
	}
	model.UI.Input = `"ROI":`
	model.UI.InputCursor = runeCount(model.UI.Input)
	model.resetCommandCompletion()
	if line := model.commandCompletionLine(160); !strings.Contains(line, "13") || !strings.Contains(line, "2") {
		t.Fatalf("custom-column value completions missing cached values:\n%s", line)
	}
	model.UI.Input = "status:open "
	model.UI.InputCursor = runeCount(model.UI.Input)
	model.resetCommandCompletion()
	if line := model.commandCompletionLine(160); !strings.Contains(line, "AND") || !strings.Contains(line, "OR") || !strings.Contains(line, "NOT") {
		t.Fatalf("filter Boolean completions missing conditions:\n%s", line)
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "status:open AND" {
		t.Fatalf("Boolean completion = %q, want a completed AND condition", model.UI.Input)
	}
	if line := model.commandCompletionLine(160); !strings.Contains(line, "OR") || !strings.Contains(line, "NOT") {
		t.Fatalf("Boolean completion choices were not retained:\n%s", line)
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "status:open OR" {
		t.Fatalf("second Boolean completion = %q, want OR", model.UI.Input)
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "status:open NOT" {
		t.Fatalf("third Boolean completion = %q, want NOT", model.UI.Input)
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "status:open AND" {
		t.Fatalf("Boolean completion cycle = %q, want AND", model.UI.Input)
	}
	model, _ = typeInput(model, " p")
	if line := model.commandCompletionLine(160); !strings.Contains(line, "priority") || strings.Contains(line, "OR") {
		t.Fatalf("completion after Boolean condition should offer columns:\n%s", line)
	}

	model, _ = model.Update(KeyMsg{Key: "esc"})
	model, _ = model.Update(KeyMsg{Key: "o"})
	if line := model.commandCompletionLine(160); !strings.Contains(line, "ROI") || !strings.Contains(line, "priority") {
		t.Fatalf("sort column completions missing available columns:\n%s", line)
	}
	model, _ = typeInput(model, "priority ")
	if line := model.commandCompletionLine(160); !strings.Contains(line, "asc") || !strings.Contains(line, "desc") {
		t.Fatalf("sort direction completions missing directions:\n%s", line)
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "priority asc" {
		t.Fatalf("sort direction completion = %q, want priority asc", model.UI.Input)
	}
}

func TestCommandPaletteFilterCompletionUsesColumnValues(t *testing.T) {
	model := New(testSnapshot())
	model, _ = model.Update(KeyMsg{Key: ":"})
	model, _ = typeInput(model, "filter status:")
	if line := model.commandCompletionLine(160); !strings.Contains(line, "open") {
		t.Fatalf("palette filter suggestions omit task statuses:\n%s", line)
	}
	model, _ = model.Update(KeyMsg{Key: "tab"})
	if model.UI.Input != "filter status:done" && model.UI.Input != "filter status:open" {
		t.Fatalf("palette filter completion = %q, want a known status", model.UI.Input)
	}
}

func TestRefreshUsesSelectedTaskListWhenHierarchySelectionIsProvider(t *testing.T) {
	model := New(testSnapshot())
	model.UI.SelectedNode = TreeNodeRef{Kind: TreeNodeWorkspace, ProviderID: "work", WorkspaceID: "work"}
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

func TestTaskAppearsInEveryMemberListAndShowsMemberships(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Lists = append(snapshot.Lists, List{
		ID: "planning", ProviderID: "work", SpaceID: "engineering", Name: "Planning", SyncState: SyncStateSynced,
	})
	snapshot.Tasks[0].ListIDs = []ListID{"backend", "planning"}
	model := New(snapshot)
	model.UI.SelectedNode = TreeNodeRef{Kind: TreeNodeList, ProviderID: "work", SpaceID: "engineering", ListID: "planning"}
	model.UI.SelectedTask = TaskRef{ProviderID: "work", TaskID: "same"}

	rows := model.VisibleTasks()
	if len(rows) != 1 || rows[0].Task.ListID != "backend" || rows[0].ListID != "planning" {
		t.Fatalf("planning list rows = %#v", rows)
	}
	if !reflect.DeepEqual(rows[0].ListNames, []string{"Backend", "Planning"}) {
		t.Fatalf("task list labels = %v", rows[0].ListNames)
	}
	lines := model.detailLines(120)
	if !strings.Contains(strings.Join(lines, "\n"), "LISTS: Backend, Planning") {
		t.Fatalf("task detail does not show all list memberships:\n%s", strings.Join(lines, "\n"))
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

func TestTaskListCommandPaletteTargetsSelectedTask(t *testing.T) {
	parsed, err := ParseCommand(":task add-list backlog")
	if err != nil {
		t.Fatalf("ParseCommand() error = %v", err)
	}
	if parsed.Kind != CommandAddTaskToList || parsed.DestinationListID != "backlog" {
		t.Fatalf("parsed command = %#v", parsed)
	}

	model := New(testSnapshot())
	model.selectTaskAt(0)
	model.UI.Mode = ModeCommand
	model.UI.Input = "task add-list backlog"
	model.UI.InputCursor = runeCount(model.UI.Input)
	model, command := model.Update(KeyMsg{Key: "enter"})
	if command == nil {
		t.Fatal("task list command emitted no command")
	}
	result := command()
	message, ok := result.(CommandMsg)
	if !ok {
		t.Fatalf("command message = %T", result)
	}
	if message.Command.Kind != CommandAddTaskToList || message.Command.TaskID != model.UI.SelectedTask.TaskID ||
		message.Command.ProviderID != model.UI.SelectedTask.ProviderID || message.Command.DestinationListID != "backlog" {
		t.Fatalf("emitted command = %#v", message.Command)
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

func TestCharmFilterColumnCompletionsAreRendered(t *testing.T) {
	model := NewCharmModel(New(testSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.Mode != ModeFilter {
		t.Fatalf("filter shortcut opened mode %q", model.CoreModel().UI.Mode)
	}
	view := model.View()
	if !strings.Contains(view, "options:") || !strings.Contains(view, "status") || !strings.Contains(view, "priority") {
		t.Fatalf("Charm filter column suggestions are missing:\n%s", view)
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
	if model.UI.SelectedNode.Kind != TreeNodeWorkspace || model.UI.SelectedNode.ProviderID != "local" {
		t.Fatalf("selection after first list reload = %#v, want the local workspace", model.UI.SelectedNode)
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
		t.Fatalf("collapsed hierarchy nodes = %d, want one workspace", len(nodes))
	}
	for _, node := range nodes {
		if node.Ref.Kind != TreeNodeWorkspace || node.Expanded {
			t.Fatalf("collapsed hierarchy node = %#v, want collapsed workspace", node)
		}
	}
	if model.UI.SelectedNode.Kind != TreeNodeWorkspace || model.UI.SelectedNode.ProviderID != "work" {
		t.Fatalf("selection after collapse all = %#v, want work workspace", model.UI.SelectedNode)
	}

	model, _ = model.Update(KeyMsg{Key: "+"})
	nodes = model.TreeNodes()
	if len(nodes) != 3 {
		t.Fatalf("expanded hierarchy nodes = %d, want workspace, space, and list", len(nodes))
	}
	for _, node := range nodes {
		if node.Ref.Kind != TreeNodeWorkspace && node.Ref.Kind != TreeNodeSpace {
			continue
		}
		if !node.Expanded {
			t.Fatalf("expanded hierarchy node = %#v, want expanded", node)
		}
	}
}

func TestWorkspaceAndSpaceNodesOrganizeAndCollapseTheirLists(t *testing.T) {
	snapshot := Snapshot{
		Providers: []Provider{{ID: "clickup-work", Name: "ClickUp Work", Type: ProviderTypeClickUp}},
		Workspaces: []Workspace{
			{ID: "team-work", ProviderID: "clickup-work", Name: "Work"},
			{ID: "team-personal", ProviderID: "clickup-work", Name: "Personal"},
		},
		Spaces: []Space{
			{ID: "engineering", ProviderID: "clickup-work", WorkspaceID: "team-work", Name: "Engineering"},
			{ID: "home", ProviderID: "clickup-work", WorkspaceID: "team-personal", Name: "Home"},
		},
		Lists: []List{
			{ID: "backend", ProviderID: "clickup-work", SpaceID: "engineering", Name: "Backend"},
			{ID: "today", ProviderID: "clickup-work", SpaceID: "home", Name: "Today"},
		},
		Tasks: []Task{
			{ID: "work-task", ProviderID: "clickup-work", ListID: "backend", Title: "Work task", Status: "open"},
			{ID: "personal-task", ProviderID: "clickup-work", ListID: "today", Title: "Personal task", Status: "open"},
		},
	}
	model := New(snapshot)
	nodes := model.TreeNodes()
	want := []struct {
		kind  TreeNodeKind
		name  string
		depth int
	}{
		{TreeNodeWorkspace, "Work", 0},
		{TreeNodeSpace, "Engineering", 1},
		{TreeNodeList, "Backend", 2},
		{TreeNodeWorkspace, "Personal", 0},
		{TreeNodeSpace, "Home", 1},
		{TreeNodeList, "Today", 2},
	}
	if len(nodes) != len(want) {
		t.Fatalf("tree nodes = %#v, want %d nodes", nodes, len(want))
	}
	for index, expected := range want {
		if nodes[index].Ref.Kind != expected.kind || nodes[index].Name != expected.name || nodes[index].Depth != expected.depth {
			t.Fatalf("tree node %d = %#v, want %s %q at depth %d", index, nodes[index], expected.kind, expected.name, expected.depth)
		}
	}

	workspaceRef := TreeNodeRef{Kind: TreeNodeWorkspace, ProviderID: "clickup-work", WorkspaceID: "team-work"}
	model.UI.SelectedNode = workspaceRef
	rows := model.VisibleTasks()
	if len(rows) != 1 || rows[0].Task.ID != "work-task" {
		t.Fatalf("workspace-scoped tasks = %#v, want only Work task", rows)
	}
	model.UI.TreeCursor = findNode(nodes, workspaceRef)
	model, _ = model.Update(KeyMsg{Key: "enter"})
	nodes = model.TreeNodes()
	if len(nodes) != 4 || nodes[0].Name != "Work" || nodes[0].Expanded {
		t.Fatalf("collapsed workspace subtree = %#v, want only its root", nodes)
	}

	model, command := model.Update(KeyMsg{Key: "space"})
	if message := commandMessage(t, command); message.Kind != CommandFetchLists || message.ProviderID != "clickup-work" {
		t.Fatalf("opening workspace with space = %#v, want workspace list fetch", message)
	}
	nodes = model.TreeNodes()
	spaceRef := TreeNodeRef{Kind: TreeNodeSpace, ProviderID: "clickup-work", WorkspaceID: "team-work", SpaceID: "engineering"}
	model.UI.SelectedNode = spaceRef
	model.UI.TreeCursor = findNode(nodes, spaceRef)
	model, _ = model.Update(KeyMsg{Key: "space"})
	nodes = model.TreeNodes()
	if len(nodes) != 5 {
		t.Fatalf("collapsed space subtree nodes = %d, want 5", len(nodes))
	}
	for _, node := range nodes {
		if node.Ref.Kind == TreeNodeList && node.Ref.ListID == "backend" {
			t.Fatal("collapsed space still displays its list")
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

func visibleTaskIDs(model Model) []TaskID {
	rows := model.VisibleTasks()
	ids := make([]TaskID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Task.ID)
	}
	return ids
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

func largeTaskSnapshot(taskCount int) Snapshot {
	snapshot := Snapshot{
		Providers: []Provider{{ID: "work", Name: "Work", Type: ProviderTypeLocal, SyncState: SyncStateLocal}},
		Spaces:    []Space{{ID: "space", ProviderID: "work", Name: "Space", SyncState: SyncStateLocal}},
		Lists:     []List{{ID: "list", ProviderID: "work", SpaceID: "space", Name: "List", SyncState: SyncStateLocal}},
		Tasks:     make([]Task, taskCount),
	}
	for index := range snapshot.Tasks {
		snapshot.Tasks[index] = Task{
			ID:         TaskID(fmt.Sprintf("task-%05d", index)),
			ProviderID: "work",
			ListID:     "list",
			Title:      fmt.Sprintf("Task %05d", index),
			Status:     "open",
			Priority:   PriorityNormal,
		}
	}
	return snapshot
}

func TestLargeTaskListRenderingUsesOnlyTheVisibleRows(t *testing.T) {
	model := New(largeTaskSnapshot(100))
	model.UI.Focus = PanelTasks
	model.UI.TaskOffset = 40
	model.UI.TaskCursor = 40
	model.UI.SelectedTask = TaskRef{ProviderID: "work", TaskID: "task-00040"}

	for name, view := range map[string]string{
		"core":  model.View(),
		"charm": NewCharmModel(model, CharmOptions{}).View(),
	} {
		if !strings.Contains(view, "Task 00040") {
			t.Errorf("%s view omitted the first visible task:\n%s", name, view)
		}
		if strings.Contains(view, "Task 00099") {
			t.Errorf("%s view rendered tasks beyond the terminal viewport", name)
		}
	}

	model.UI.TaskOffset = 1000
	if view := model.View(); !strings.Contains(view, "Task 00099") {
		t.Fatalf("core view did not clamp an out-of-range offset to the last task:\n%s", view)
	}
}

func BenchmarkLargeTaskListView(b *testing.B) {
	model := New(largeTaskSnapshot(5000))
	model.UI.Focus = PanelTasks
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = model.View()
	}
}

func BenchmarkLargeTaskListCharmView(b *testing.B) {
	model := NewCharmModel(New(largeTaskSnapshot(5000)), CharmOptions{})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = model.View()
	}
}

func BenchmarkLargeTaskListNavigation(b *testing.B) {
	model := New(largeTaskSnapshot(5000))
	model.UI.Focus = PanelTasks
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		model, _ = model.Update(KeyMsg{Key: "j"})
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
	selectedCoreLine := false
	for _, line := range model.CoreModel().treeLines(80) {
		if strings.Contains(line, "Backend") && strings.Contains(line, ">") {
			selectedCoreLine = true
			break
		}
	}
	if !selectedCoreLine {
		t.Fatal("core renderer removed the selected list highlight when focus moved to tasks")
	}
	selectedCharmLine := false
	for _, line := range model.charmTreeLines(80) {
		if strings.Contains(line, "Backend") && strings.Contains(line, ">") {
			selectedCharmLine = true
			break
		}
	}
	if !selectedCharmLine {
		t.Fatal("Charm renderer removed the selected list highlight when focus moved to tasks")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(*CharmModel)
	if model.CoreModel().UI.Focus != PanelHierarchy {
		t.Fatalf("focus = %q, want hierarchy", model.CoreModel().UI.Focus)
	}
	selectedCoreTask := false
	for _, line := range model.CoreModel().taskLines(80, 20) {
		if strings.Contains(line, "Fix auth") && strings.Contains(line, ">") {
			selectedCoreTask = true
			break
		}
	}
	if !selectedCoreTask {
		t.Fatal("core renderer removed the selected task highlight when focus moved to hierarchy")
	}
	selectedCharmTask := false
	for _, line := range model.charmTaskLines(80, 20) {
		if strings.Contains(line, "Fix auth") && strings.Contains(line, ">") {
			selectedCharmTask = true
			break
		}
	}
	if !selectedCharmTask {
		t.Fatal("Charm renderer removed the selected task highlight when focus moved to hierarchy")
	}

	view := model.View()
	for _, value := range []string{"TASK MANAGER", "WORKSPACES", "TASKS", "LIST Backend", "Work"} {
		if !strings.Contains(view, value) {
			t.Fatalf("Charm view does not contain %q:\n%s", value, view)
		}
	}
	if strings.Contains(view, "Personal") {
		t.Fatalf("Charm view contains inactive provider:\n%s", view)
	}
}

func TestInactiveSelectionUsesForegroundOnly(t *testing.T) {
	if _, ok := charmInactiveSelectedStyle.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatal("inactive selection should not set a background color")
	}
	if _, ok := charmInactiveSelectedStyle.GetForeground().(lipgloss.NoColor); ok {
		t.Fatal("inactive selection should set a foreground color")
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

func TestHierarchyPanelNarrowsAndKeepsHorizontalScrolling(t *testing.T) {
	for _, test := range []struct {
		name             string
		width            int
		minimumWidth     int
		minimumTaskWidth int
		wantWidth        int
	}{
		{name: "core layout minimum", width: 72, minimumWidth: 10, minimumTaskWidth: 24, wantWidth: 10},
		{name: "Charm layout minimum", width: 72, minimumWidth: 12, minimumTaskWidth: 30, wantWidth: 12},
		{name: "typical terminal", width: 100, minimumWidth: 12, minimumTaskWidth: 30, wantWidth: 18},
		{name: "wide terminal", width: 230, minimumWidth: 12, minimumTaskWidth: 30, wantWidth: 59},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := hierarchyPanelWidth(test.width, test.minimumWidth, test.minimumTaskWidth); got != test.wantWidth {
				t.Fatalf("hierarchy panel width = %d, want %d", got, test.wantWidth)
			}
		})
	}

	model := NewCharmModel(New(scrollableSnapshot()), CharmOptions{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	model = updated.(*CharmModel)
	for index := 0; index < 8; index++ {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
		model = updated.(*CharmModel)
	}
	if model.CoreModel().UI.TreeHorizontalOffset == 0 {
		t.Fatal("narrow hierarchy panel did not scroll horizontally")
	}
	if view := model.View(); !strings.Contains(view, "hierarchy") {
		t.Fatalf("horizontally scrolled hierarchy does not show long list names:\n%s", view)
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
