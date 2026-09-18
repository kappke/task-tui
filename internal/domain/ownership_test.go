package domain

import (
	"errors"
	"testing"
)

func TestProviderIsolation(t *testing.T) {
	tests := []struct {
		name  string
		check func() error
		want  error
	}{
		{
			name: "provider ownership matches",
			check: func() error {
				return ValidateProviderOwnership(ProviderID("clickup-work"), ProviderID("clickup-work"))
			},
		},
		{
			name: "list provider mismatch",
			check: func() error {
				return ValidateListProvider(
					List{ProviderID: ProviderID("local")},
					Space{ProviderID: ProviderID("clickup")},
				)
			},
			want: ErrProviderMismatch,
		},
		{
			name: "task provider mismatch",
			check: func() error {
				return ValidateTaskProvider(
					Task{ProviderID: ProviderID("local")},
					List{ProviderID: ProviderID("clickup")},
				)
			},
			want: ErrProviderMismatch,
		},
		{
			name: "normal move rejects another provider",
			check: func() error {
				return ValidateSameProviderMove(ProviderID("local"), ProviderID("clickup"))
			},
			want: ErrCrossProviderMove,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check()
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestHierarchyParentValidation(t *testing.T) {
	parentID := TaskID("parent")
	parent := Task{
		ID:         parentID,
		ProviderID: ProviderID("local"),
	}
	child := Task{
		ID:           TaskID("child"),
		ProviderID:   ProviderID("local"),
		ParentTaskID: &parentID,
	}

	if err := ValidateTaskParent(child, parent); err != nil {
		t.Fatalf("same-provider parent rejected: %v", err)
	}

	otherProvider := parent
	otherProvider.ProviderID = ProviderID("clickup")
	if err := ValidateTaskParent(child, otherProvider); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("got %v, want provider mismatch", err)
	}

	wrongParent := parent
	wrongParent.ID = TaskID("other")
	if err := ValidateTaskParent(child, wrongParent); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("got %v, want invalid parent", err)
	}

	if err := ValidateOptionalTaskParent(
		Task{ID: TaskID("orphan"), ProviderID: ProviderID("local"), ParentTaskID: &parentID},
		nil,
	); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("got %v, want invalid parent for missing parent", err)
	}
}

func TestValidationRejectsInvalidIDsAndEnums(t *testing.T) {
	if err := ProviderID(" ").Validate(); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("got %v, want invalid ID", err)
	}
	if err := ProviderType("trello").Validate(); !errors.Is(err, ErrInvalidEnum) {
		t.Fatalf("got %v, want invalid enum", err)
	}
	if err := (Task{
		ID:         TaskID("task"),
		ProviderID: ProviderID("local"),
		ListID:     ListID("list"),
		Priority:   Priority("critical"),
		SyncState:  SyncStateLocal,
	}).Validate(); !errors.Is(err, ErrInvalidEnum) {
		t.Fatalf("got %v, want invalid priority enum", err)
	}
}

func TestPriorityComparison(t *testing.T) {
	if ComparePriority(PriorityLow, PriorityHigh) >= 0 {
		t.Fatal("low should sort below high")
	}
	if ComparePriority(PriorityUrgent, PriorityNormal) <= 0 {
		t.Fatal("urgent should sort above normal")
	}
	if ComparePriority(PriorityNone, PriorityNone) != 0 {
		t.Fatal("equal priorities should compare equally")
	}
	if !PriorityHigh.AtLeast(PriorityNormal) {
		t.Fatal("high should be at least normal")
	}
	if PriorityLow.AtLeast(PriorityUrgent) {
		t.Fatal("low should not be at least urgent")
	}
}
