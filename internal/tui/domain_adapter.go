package tui

import "github.com/kappke/task-tui/internal/domain"

// DomainSnapshot is the narrow bridge from normalized foundation entities to
// the presentation snapshot. UI-only selection and mode fields stay local.
type DomainSnapshot struct {
	Providers  []domain.Provider
	Workspaces []domain.Workspace
	Spaces     []domain.Space
	Lists      []domain.List
	Tasks      []domain.Task
}

// SnapshotFromDomain copies normalized foundation entities without sharing
// mutable slices or pointer fields.
func SnapshotFromDomain(input DomainSnapshot) Snapshot {
	return cloneSnapshot(Snapshot{
		Providers:  append([]Provider(nil), input.Providers...),
		Workspaces: append([]Workspace(nil), input.Workspaces...),
		Spaces:     append([]Space(nil), input.Spaces...),
		Lists:      append([]List(nil), input.Lists...),
		Tasks:      append([]Task(nil), input.Tasks...),
	})
}

// NewFromDomain creates a model directly from normalized foundation entities.
func NewFromDomain(input DomainSnapshot) Model {
	return New(SnapshotFromDomain(input))
}
