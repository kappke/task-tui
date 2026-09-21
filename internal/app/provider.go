package app

import (
	"fmt"
	"sync"

	"github.com/kappke/task-tui/internal/domain"
	providerpkg "github.com/kappke/task-tui/internal/provider"
)

type Provider = providerpkg.Provider
type Capabilities = domain.Capabilities

type Capability string

const (
	CapabilityCreateSpace  Capability = "create_space"
	CapabilityCreateList   Capability = "create_list"
	CapabilityCreateTask   Capability = "create_task"
	CapabilityUpdateTask   Capability = "update_task"
	CapabilityCompleteTask Capability = "complete_task"
	CapabilityDeleteTask   Capability = "delete_task"
	CapabilityMoveTask     Capability = "move_task"
	CapabilityDueDates     Capability = "due_dates"
	CapabilityPriorities   Capability = "priorities"
	CapabilitySubtasks     Capability = "subtasks"
)

func AllCapabilities() Capabilities {
	return Capabilities{
		CreateSpace: true,
		CreateList:  true,
		CreateTask:  true,
		UpdateTask:  true,
		DeleteTask:  true,
		UpdateList:  true,
		DeleteList:  true,
		UpdateSpace: true,
		DeleteSpace: true,
		DueDates:    true,
		Subtasks:    true,
	}
}

// ProviderRegistry is a metadata and capability lookup. It must not perform
// authentication, synchronization, or any other provider network operation.
type ProviderRegistry interface {
	Get(id ProviderID) (Provider, bool)
}

type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: make(map[ProviderID]Provider, len(providers))}
	for _, provider := range providers {
		if provider != nil && provider.ID() != "" {
			id := provider.ID()
			if _, exists := r.providers[id]; exists {
				panic(fmt.Sprintf("new provider registry: duplicate provider ID %s", id))
			}
			r.providers[id] = provider
		}
	}
	return r
}

func NewProviderRegistry(providers ...Provider) *Registry {
	return NewRegistry(providers...)
}

func (r *Registry) Register(provider Provider) error {
	if r == nil {
		return ErrProviderRegistryUnavailable
	}
	if provider == nil || provider.ID() == "" {
		return fmt.Errorf("register provider: %w: provider id is empty", ErrInvalidInput)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[ProviderID]Provider)
	}
	id := provider.ID()
	if _, exists := r.providers[id]; exists {
		return fmt.Errorf("register provider %s: %w", id, ErrProviderAlreadyRegistered)
	}
	r.providers[id] = provider
	return nil
}

func (r *Registry) Get(id ProviderID) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[id]
	return provider, ok
}

func supportsCapability(provider Provider, capability Capability) bool {
	if provider == nil {
		return false
	}
	caps := provider.Capabilities()
	switch capability {
	case CapabilityCreateSpace:
		return caps.Supports(domain.EntityTypeSpace, domain.OperationTypeCreate)
	case CapabilityCreateList:
		return caps.Supports(domain.EntityTypeList, domain.OperationTypeCreate)
	case CapabilityCreateTask:
		return caps.Supports(domain.EntityTypeTask, domain.OperationTypeCreate)
	case CapabilityUpdateTask, CapabilityCompleteTask, CapabilityMoveTask:
		return caps.Supports(domain.EntityTypeTask, domain.OperationTypeUpdate)
	case CapabilityDeleteTask:
		return caps.Supports(domain.EntityTypeTask, domain.OperationTypeDelete)
	case CapabilityDueDates:
		return caps.DueDates
	case CapabilitySubtasks:
		return caps.Subtasks
	case CapabilityPriorities:
		// The foundation capabilities intentionally do not split priority from
		// the common task update contract.
		return caps.Supports(domain.EntityTypeTask, domain.OperationTypeUpdate)
	default:
		return false
	}
}
