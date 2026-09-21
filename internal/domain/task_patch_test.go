package domain

import (
	"encoding/json"
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
