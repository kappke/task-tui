package domain

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestTaskMutationPayloadRoundTrip(t *testing.T) {
	title := "changed"
	task := Task{
		ID:         "task-1",
		ProviderID: "provider-1",
		ListID:     "list-1",
		Title:      title,
		Priority:   PriorityNormal,
	}
	patch := TaskPatch{Title: &title}
	payload := NewTaskUpdateMutationPayload(task, patch)

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded TaskMutationPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Version != TaskMutationPayloadVersion || decoded.ProviderID != task.ProviderID || decoded.TaskID != task.ID {
		t.Fatalf("identity = %#v, want versioned task identity", decoded)
	}
	if decoded.Snapshot == nil || decoded.Snapshot.Title != task.Title {
		t.Fatalf("snapshot = %#v, want task snapshot", decoded.Snapshot)
	}
	if decoded.TaskPatch == nil || decoded.TaskPatch.Title == nil || *decoded.TaskPatch.Title != title {
		t.Fatalf("patch = %#v, want title patch", decoded.TaskPatch)
	}
}

func TestTaskListMembershipInvariantAndOperations(t *testing.T) {
	task := Task{ID: "task-1", ProviderID: "provider-1", ListID: "list-1", Priority: PriorityNormal, SyncState: SyncStateLocal}
	if err := task.Validate(); err != nil {
		t.Fatalf("single-list legacy task Validate() error = %v", err)
	}

	shared, err := task.AddToList("list-2")
	if err != nil {
		t.Fatalf("AddToList() error = %v", err)
	}
	if shared.ListID != "list-1" || !reflect.DeepEqual(shared.ListIDs, []ListID{"list-1", "list-2"}) {
		t.Fatalf("added task = primary %q, memberships %v", shared.ListID, shared.ListIDs)
	}

	withoutPrimary, err := shared.RemoveFromList("list-1")
	if err != nil {
		t.Fatalf("RemoveFromList(primary) error = %v", err)
	}
	if withoutPrimary.ListID != "list-2" || !reflect.DeepEqual(withoutPrimary.ListIDs, []ListID{"list-2"}) {
		t.Fatalf("task after removing primary = primary %q, memberships %v", withoutPrimary.ListID, withoutPrimary.ListIDs)
	}
	if _, err := withoutPrimary.RemoveFromList("list-2"); !errors.Is(err, ErrLastTaskList) {
		t.Fatalf("RemoveFromList(last) error = %v, want ErrLastTaskList", err)
	}

	moved, err := shared.MoveToList("list-3")
	if err != nil {
		t.Fatalf("MoveToList() error = %v", err)
	}
	if moved.ListID != "list-3" || !reflect.DeepEqual(moved.ListIDs, []ListID{"list-3"}) {
		t.Fatalf("moved task = primary %q, memberships %v", moved.ListID, moved.ListIDs)
	}
}

func TestTaskValidateRejectsInvalidMembershipSets(t *testing.T) {
	base := Task{ID: "task-1", ProviderID: "provider-1", ListID: "list-1"}
	tests := []struct {
		name string
		task Task
	}{
		{name: "empty primary", task: Task{ID: base.ID, ProviderID: base.ProviderID}},
		{name: "primary omitted", task: Task{ID: base.ID, ProviderID: base.ProviderID, ListID: base.ListID, ListIDs: []ListID{"list-2"}}},
		{name: "duplicate membership", task: Task{ID: base.ID, ProviderID: base.ProviderID, ListID: base.ListID, ListIDs: []ListID{"list-1", "list-1"}}},
		{name: "empty membership", task: Task{ID: base.ID, ProviderID: base.ProviderID, ListID: base.ListID, ListIDs: []ListID{"list-1", ""}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.task.Validate(); err == nil {
				t.Fatal("Validate() succeeded for invalid list membership set")
			}
		})
	}
}

func TestTaskMembershipPatchRoundTrip(t *testing.T) {
	patch := TaskPatch{AddListIDs: []ListID{"list-2"}, RemoveListIDs: []ListID{"list-3"}}
	data, err := json.Marshal(NewTaskUpdateMutationPayload(
		Task{ID: "task-1", ProviderID: "provider-1", ListID: "list-1", ListIDs: []ListID{"list-1", "list-2"}}, patch,
	))
	if err != nil {
		t.Fatal(err)
	}
	var decoded TaskMutationPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TaskPatch == nil || !reflect.DeepEqual(decoded.TaskPatch.AddListIDs, patch.AddListIDs) || !reflect.DeepEqual(decoded.TaskPatch.RemoveListIDs, patch.RemoveListIDs) {
		t.Fatalf("decoded membership patch = %#v", decoded.TaskPatch)
	}
}

func TestTaskCreateMutationKeepsAdditionalListMemberships(t *testing.T) {
	task := Task{
		ID: "task-1", ProviderID: "provider-1", ListID: "list-1",
		ListIDs: []ListID{"list-1", "list-2"}, Priority: PriorityNormal, SyncState: SyncStatePending,
	}
	data, err := json.Marshal(NewTaskCreateMutationPayload(task))
	if err != nil {
		t.Fatal(err)
	}
	var decoded TaskMutationPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TaskPatch != nil {
		t.Fatalf("create payload was interpreted as an update patch: %#v", decoded.TaskPatch)
	}
	if decoded.Snapshot == nil || !reflect.DeepEqual(decoded.Snapshot.Memberships(), task.ListIDs) {
		t.Fatalf("create snapshot memberships = %#v", decoded.Snapshot)
	}
}

func TestRemovedListPatchReplaysAgainstUpdatedSnapshot(t *testing.T) {
	task := Task{
		ID: "task-1", ProviderID: "provider-1", ListID: "list-2",
		ListIDs: []ListID{"list-2"}, Priority: PriorityNormal, SyncState: SyncStatePending,
	}
	data, err := json.Marshal(NewTaskUpdateMutationPayload(task, TaskPatch{RemoveListIDs: []ListID{"list-1"}}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded TaskMutationPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	updated, err := decoded.TaskPatch.Apply(*decoded.Snapshot)
	if err != nil {
		t.Fatalf("replay removal patch: %v", err)
	}
	if !reflect.DeepEqual(updated.Memberships(), []ListID{"list-2"}) {
		t.Fatalf("replayed memberships = %v", updated.Memberships())
	}
}

func TestHierarchyMutationPayloadRoundTrip(t *testing.T) {
	space := Space{ID: "space-1", ProviderID: "provider-1", Name: "Work"}
	list := List{ID: "list-1", ProviderID: "provider-1", SpaceID: space.ID, Name: "Today"}
	spaceData, err := json.Marshal(NewSpaceCreateMutationPayload(space))
	if err != nil {
		t.Fatal(err)
	}
	listData, err := json.Marshal(NewListCreateMutationPayload(list))
	if err != nil {
		t.Fatal(err)
	}
	var decodedSpace SpaceMutationPayload
	var decodedList ListMutationPayload
	if err := json.Unmarshal(spaceData, &decodedSpace); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(listData, &decodedList); err != nil {
		t.Fatal(err)
	}
	if decodedSpace.Version != HierarchyMutationPayloadVersion || decodedSpace.Snapshot == nil || *decodedSpace.Snapshot != space {
		t.Fatalf("space payload = %#v", decodedSpace)
	}
	if decodedList.Version != HierarchyMutationPayloadVersion || decodedList.Snapshot == nil || *decodedList.Snapshot != list {
		t.Fatalf("list payload = %#v", decodedList)
	}
}
