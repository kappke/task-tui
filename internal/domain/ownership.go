package domain

import (
	"errors"
	"fmt"
)

// ValidateProviderOwnership verifies that two valid provider instance IDs are
// equal. It is the primitive check used by all hierarchy relationships.
func ValidateProviderOwnership(owner, entity ProviderID) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	if err := entity.Validate(); err != nil {
		return err
	}
	if owner != entity {
		return fmt.Errorf("%w: %s does not belong to %s", ErrProviderMismatch, entity, owner)
	}
	return nil
}

// ValidateListProvider verifies that a list belongs to the same provider as
// its space. If both parent IDs are populated, it also verifies the direct
// list-to-space relationship; strict complete-shape checking belongs to
// ValidateHierarchy.
func ValidateListProvider(list List, space Space) error {
	if err := ValidateProviderOwnership(space.ProviderID, list.ProviderID); err != nil {
		return err
	}
	if !list.SpaceID.IsZero() && !space.ID.IsZero() && list.SpaceID != space.ID {
		return fmt.Errorf("%w: list %s does not belong to space %s", ErrInvalidParent, list.ID, space.ID)
	}
	return nil
}

// ValidateTaskProvider verifies that a task belongs to the same provider as
// its list. If both parent IDs are populated, it also verifies the direct
// task-to-list relationship; strict complete-shape checking belongs to
// ValidateHierarchy.
func ValidateTaskProvider(task Task, list List) error {
	if err := ValidateProviderOwnership(list.ProviderID, task.ProviderID); err != nil {
		return err
	}
	if !task.ListID.IsZero() && !list.ID.IsZero() && task.ListID != list.ID {
		return fmt.Errorf("%w: task %s does not belong to list %s", ErrInvalidParent, task.ID, list.ID)
	}
	return nil
}

// ValidateTaskParent verifies an explicit task-parent relationship. Parent
// tasks must remain in the same provider hierarchy and a task cannot parent
// itself.
func ValidateTaskParent(task, parent Task) error {
	if err := ValidateProviderOwnership(task.ProviderID, parent.ProviderID); err != nil {
		return err
	}
	if task.ParentTaskID == nil {
		return fmt.Errorf("%w: task %s has no parent", ErrInvalidParent, task.ID)
	}
	if parent.ID.IsZero() || *task.ParentTaskID != parent.ID {
		return fmt.Errorf("%w: task %s references %s, got %s", ErrInvalidParent, task.ID, task.ParentTaskID, parent.ID)
	}
	if !task.ID.IsZero() && task.ID == parent.ID {
		return fmt.Errorf("%w: task cannot parent itself", ErrInvalidParent)
	}
	return nil
}

// ValidateOptionalTaskParent verifies a nullable parent relationship. A nil
// parent is valid only when the task has no ParentTaskID.
func ValidateOptionalTaskParent(task Task, parent *Task) error {
	if parent == nil {
		if task.ParentTaskID != nil {
			return fmt.Errorf("%w: parent task %s is missing", ErrInvalidParent, task.ParentTaskID)
		}
		return nil
	}
	return ValidateTaskParent(task, *parent)
}

// ValidateSameProviderMove rejects a normal move whose destination provider
// differs from the source. Cross-provider operations must use an explicit
// copy or transfer workflow instead.
func ValidateSameProviderMove(source, destination ProviderID) error {
	if err := ValidateProviderOwnership(source, destination); err != nil {
		if errors.Is(err, ErrProviderMismatch) {
			return fmt.Errorf("%w: %s to %s", ErrCrossProviderMove, source, destination)
		}
		return err
	}
	return nil
}

// ValidateMove is an alias in intent for ValidateSameProviderMove and makes
// the rejection explicit at normal move call sites.
func ValidateMove(source, destination ProviderID) error {
	return ValidateSameProviderMove(source, destination)
}

// ValidateCrossProviderTransfer verifies that a transfer really crosses
// provider boundaries. Same-provider moves should use the normal move path.
func ValidateCrossProviderTransfer(source, destination ProviderID) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if err := destination.Validate(); err != nil {
		return err
	}
	if source == destination {
		return fmt.Errorf("%w: source and destination are %s", ErrCrossProviderMove, source)
	}
	return nil
}

// ValidateHierarchy validates a complete provider-to-task chain. The parent
// argument is optional; when absent, task.ParentTaskID must also be absent.
func ValidateHierarchy(provider Provider, space Space, list List, task Task, parents ...*Task) error {
	if len(parents) > 1 {
		return fmt.Errorf("%w: at most one parent task may be supplied", ErrInvalidParent)
	}
	var parent *Task
	if len(parents) == 1 {
		parent = parents[0]
	}

	if err := provider.Validate(); err != nil {
		return err
	}
	if err := space.Validate(); err != nil {
		return err
	}
	if err := list.Validate(); err != nil {
		return err
	}
	if err := task.Validate(); err != nil {
		return err
	}
	if err := ValidateProviderOwnership(provider.ID, space.ProviderID); err != nil {
		return err
	}
	if err := ValidateListProvider(list, space); err != nil {
		return err
	}
	if err := ValidateTaskProvider(task, list); err != nil {
		return err
	}
	return ValidateOptionalTaskParent(task, parent)
}
