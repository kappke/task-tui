package clickup

import (
	"testing"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

func TestMapperInjectsProviderAndLocalParentIdentity(t *testing.T) {
	mapper := NewMapper(MapperConfig{
		ProviderID: "clickup-work",
		ParentIDs: map[string]domain.TaskID{
			"remote-parent": "local-parent",
		},
	})
	input := wireTask{
		ID:          "remote-task",
		Name:        "Implement adapter",
		Description: "details",
		Status:      wireStatus{Status: "vendor-custom", Color: "#123456", Type: "custom"},
		Priority:    &wirePriority{Priority: "high", Color: "#ff0000", OrderIndex: "2"},
		DueDate:     wireStringPointer("1710000000123"),
		DateCreated: "1700000000000",
		DateUpdated: "1700000001000",
		DateDone:    wireStringPointer("1700000002000"),
		Parent:      wireStringPointer("remote-parent"),
	}

	task := mapper.MapTask(input, domain.ListID("local-list"))
	if task.ProviderID != "clickup-work" {
		t.Fatalf("ProviderID = %q", task.ProviderID)
	}
	if task.ListID != "local-list" {
		t.Fatalf("ListID = %q", task.ListID)
	}
	if task.ID != "remote-task" {
		t.Fatalf("ID = %q", task.ID)
	}
	if task.RemoteID == nil || *task.RemoteID != "remote-task" {
		t.Fatalf("RemoteID = %v", task.RemoteID)
	}
	if task.ParentTaskID == nil || *task.ParentTaskID != "local-parent" {
		t.Fatalf("ParentTaskID = %v", task.ParentTaskID)
	}
	if task.Status != "vendor-custom" {
		t.Fatalf("Status = %q", task.Status)
	}

	if task.DueAt == nil || !task.DueAt.Equal(time.UnixMilli(1710000000123).UTC()) {
		t.Fatalf("DueAt = %v", task.DueAt)
	}
	if task.CompletedAt == nil || !task.CompletedAt.Equal(time.UnixMilli(1700000002000).UTC()) {
		t.Fatalf("CompletedAt = %v", task.CompletedAt)
	}
	if task.Priority != domain.PriorityHigh {
		t.Fatalf("Priority = %q", task.Priority)
	}
}

func TestMapperUsesClosedTimestampForCompletedStatus(t *testing.T) {
	mapper := NewMapper(MapperConfig{ProviderID: "clickup"})
	task := mapper.MapTask(wireTask{
		ID:         "task-1",
		Status:     wireStatus{Status: "finished", Type: "closed"},
		DateClosed: wireStringPointer("1700000003000"),
	}, domain.ListID("list-1"))
	if task.CompletedAt == nil || !task.CompletedAt.Equal(time.UnixMilli(1700000003000).UTC()) {
		t.Fatalf("CompletedAt = %v", task.CompletedAt)
	}
}

func TestMapperLeavesUnresolvedParentUnset(t *testing.T) {
	mapper := NewMapper(MapperConfig{ProviderID: "clickup"})
	task := mapper.MapTask(wireTask{
		ID:     "task-1",
		Parent: wireStringPointer("remote-parent"),
	}, domain.ListID("list-1"))
	if task.ParentTaskID != nil {
		t.Fatalf("ParentTaskID = %v, want nil", task.ParentTaskID)
	}
}

func TestMapperMapsHierarchyOwnership(t *testing.T) {
	mapper := NewMapper(MapperConfig{ProviderID: "clickup-work"})
	space := mapper.MapSpace(wireSpace{ID: "space-1", Name: "Engineering"})
	list := mapper.MapList(wireList{ID: "list-1", Name: "Backend"}, domain.SpaceID("space-1"))
	if space.ProviderID != "clickup-work" {
		t.Fatalf("space ProviderID = %q", space.ProviderID)
	}
	if list.ProviderID != "clickup-work" {
		t.Fatalf("list ProviderID = %q", list.ProviderID)
	}
	if list.SpaceID != "space-1" {
		t.Fatalf("list SpaceID = %q", list.SpaceID)
	}
}

func wireStringPointer(value string) *wireString {
	result := wireString(value)
	return &result
}
