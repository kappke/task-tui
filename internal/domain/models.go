package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// Provider is a configured provider instance. Configuration is intentionally
// opaque to the core model and should not contain credentials when a secret
// store is available.
type Provider struct {
	ID            ProviderID      `json:"id"`
	Type          ProviderType    `json:"type"`
	Name          string          `json:"name"`
	Enabled       bool            `json:"enabled"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
	SyncState     SyncState       `json:"sync_state,omitempty"`
	SyncCursor    *string         `json:"sync_cursor,omitempty"`
	SyncError     string          `json:"sync_error,omitempty"`
	LastSyncAt    *time.Time      `json:"last_sync_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Space is the top-level hierarchy entity owned by one provider instance.
type Space struct {
	ID              SpaceID    `json:"id"`
	ProviderID      ProviderID `json:"provider_id"`
	RemoteID        *string    `json:"remote_id,omitempty"`
	Name            string     `json:"name"`
	SyncState       SyncState  `json:"sync_state"`
	RemoteUpdatedAt *time.Time `json:"remote_updated_at,omitempty"`
	IsDeleted       bool       `json:"is_deleted,omitempty"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// List is a list within a space. Its provider must match both its space and
// every task stored under it.
type List struct {
	ID              ListID     `json:"id"`
	ProviderID      ProviderID `json:"provider_id"`
	SpaceID         SpaceID    `json:"space_id"`
	RemoteID        *string    `json:"remote_id,omitempty"`
	Name            string     `json:"name"`
	SyncState       SyncState  `json:"sync_state"`
	RemoteUpdatedAt *time.Time `json:"remote_updated_at,omitempty"`
	IsDeleted       bool       `json:"is_deleted,omitempty"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Task is the common task representation shared by all providers.
type Task struct {
	ID              TaskID         `json:"id"`
	ProviderID      ProviderID     `json:"provider_id"`
	ListID          ListID         `json:"list_id"`
	ListIDs         []ListID       `json:"list_ids,omitempty"`
	RemoteID        *string        `json:"remote_id,omitempty"`
	ParentTaskID    *TaskID        `json:"parent_task_id,omitempty"`
	Assignee        string         `json:"assignee,omitempty"`
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	Status          string         `json:"status"`
	Priority        Priority       `json:"priority"`
	TimeEstimate    *time.Duration `json:"time_estimate,omitempty"`
	TimeTracked     *time.Duration `json:"time_tracked,omitempty"`
	DueAt           *time.Time     `json:"due_at,omitempty"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
	SyncState       SyncState      `json:"sync_state"`
	RemoteUpdatedAt *time.Time     `json:"remote_updated_at,omitempty"`
	IsDeleted       bool           `json:"is_deleted,omitempty"`
	DeletedAt       *time.Time     `json:"deleted_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// ProviderMetadata stores provider-specific values without adding provider
// fields to core entities.
type ProviderMetadata struct {
	EntityType EntityType `json:"entity_type"`
	EntityID   string     `json:"entity_id"`
	ProviderID ProviderID `json:"provider_id"`
	Key        string     `json:"key"`
	Value      string     `json:"value"`
}

// SyncOperation is a durable remote-affecting operation. EntityID is a string
// because its concrete type is selected by EntityType; remote identifiers are
// always scoped by ProviderID.
type SyncOperation struct {
	ID             OperationID     `json:"id"`
	ProviderID     ProviderID      `json:"provider_id"`
	EntityType     EntityType      `json:"entity_type"`
	EntityID       string          `json:"entity_id"`
	Operation      OperationType   `json:"operation"`
	Payload        json.RawMessage `json:"payload"`
	Attempts       int             `json:"attempts"`
	Status         SyncStatus      `json:"status"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	LastAttemptAt  *time.Time      `json:"last_attempt_at,omitempty"`
	NextAttemptAt  *time.Time      `json:"next_attempt_at,omitempty"`
	LeaseOwner     string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
}

// FieldConflict preserves the three values needed for an explicit conflict
// decision. Values are raw JSON so provider-specific field representations do
// not leak into the domain model.
type FieldConflict struct {
	Field       string          `json:"field"`
	LocalValue  json.RawMessage `json:"local_value,omitempty"`
	RemoteValue json.RawMessage `json:"remote_value,omitempty"`
	BaseValue   json.RawMessage `json:"base_value,omitempty"`
}

// Conflict records competing representations of one provider-owned entity.
type Conflict struct {
	ID         ConflictID      `json:"id"`
	ProviderID ProviderID      `json:"provider_id"`
	EntityType EntityType      `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Fields     []FieldConflict `json:"fields,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	ResolvedAt *time.Time      `json:"resolved_at,omitempty"`
	Resolution string          `json:"resolution,omitempty"`
}

// SyncBase is the common synchronization snapshot metadata. Entities expose
// the same fields directly for ergonomic literals, while SyncBase is useful to
// storage and synchronization code that handles them generically.
type SyncBase struct {
	ProviderID      ProviderID      `json:"provider_id"`
	RemoteID        *string         `json:"remote_id,omitempty"`
	SyncState       SyncState       `json:"sync_state"`
	RemoteUpdatedAt *time.Time      `json:"remote_updated_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	EntityType      EntityType      `json:"entity_type,omitempty"`
	EntityID        string          `json:"entity_id,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	RemoteVersion   *string         `json:"remote_version,omitempty"`
	CapturedAt      time.Time       `json:"captured_at,omitempty"`
}

// Capabilities describes optional provider features. Read and task-write
// capabilities cover the required MVP contract; the remaining fields allow
// optional hierarchy writes without provider-specific branching in callers.
type Capabilities struct {
	// RemoteSync marks providers whose state is synchronized through a remote
	// service. Local-only providers should leave it false.
	RemoteSync bool `json:"remote_sync"`
	Remote     bool `json:"remote"`
	Network    bool `json:"network"`
	Pull       bool `json:"pull"`
	Local      bool `json:"local"`

	FetchSpaces bool `json:"fetch_spaces"`
	FetchLists  bool `json:"fetch_lists"`
	FetchTasks  bool `json:"fetch_tasks"`
	FetchTask   bool `json:"fetch_task"`

	CreateTask  bool `json:"create_task"`
	UpdateTask  bool `json:"update_task"`
	DeleteTask  bool `json:"delete_task"`
	CreateList  bool `json:"create_list"`
	UpdateList  bool `json:"update_list"`
	DeleteList  bool `json:"delete_list"`
	CreateSpace bool `json:"create_space"`
	UpdateSpace bool `json:"update_space"`
	DeleteSpace bool `json:"delete_space"`

	DueDates bool `json:"due_dates"`
	Subtasks bool `json:"subtasks"`
}

// RequiresNetwork reports whether a provider needs a remote synchronization
// worker. Local is explicit so a transport-shaped local adapter cannot be
// scheduled accidentally.
func (c Capabilities) RequiresNetwork() bool {
	return !c.Local && (c.RemoteSync || c.Remote || c.Network)
}

// AppState is a JSON-encoded application state value persisted by key.
type AppState struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Validate checks provider identity and enum fields at a persistence or
// provider boundary.
func (p Provider) Validate() error {
	if err := p.ID.Validate(); err != nil {
		return err
	}
	if err := p.Type.Validate(); err != nil {
		return err
	}
	if !p.SyncState.IsZero() {
		if err := p.SyncState.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks the complete space shape without checking a parent, since a
// space is the hierarchy root.
func (s Space) Validate() error {
	if err := s.ID.Validate(); err != nil {
		return err
	}
	if err := s.ProviderID.Validate(); err != nil {
		return err
	}
	if err := validateRemoteID(s.RemoteID); err != nil {
		return err
	}
	return s.SyncState.Validate()
}

// Validate checks the complete list shape without checking its space record.
func (l List) Validate() error {
	if err := l.ID.Validate(); err != nil {
		return err
	}
	if err := l.ProviderID.Validate(); err != nil {
		return err
	}
	if err := l.SpaceID.Validate(); err != nil {
		return err
	}
	if err := validateRemoteID(l.RemoteID); err != nil {
		return err
	}
	return l.SyncState.Validate()
}

// Validate checks the complete task shape without checking its list or
// optional parent record.
func (t Task) Validate() error {
	if err := t.ID.Validate(); err != nil {
		return err
	}
	if err := t.ProviderID.Validate(); err != nil {
		return err
	}
	if err := t.ListID.Validate(); err != nil {
		return err
	}
	if t.ParentTaskID != nil {
		if err := t.ParentTaskID.Validate(); err != nil {
			return fmt.Errorf("%w: parent task: %v", ErrInvalidParent, err)
		}
	}
	if err := validateRemoteID(t.RemoteID); err != nil {
		return err
	}
	if err := t.Priority.Validate(); err != nil {
		return err
	}
	return t.SyncState.Validate()
}

// Validate checks metadata ownership and its dynamic key/value identity.
func (m ProviderMetadata) Validate() error {
	if err := m.EntityType.Validate(); err != nil {
		return err
	}
	if err := validateEntityID("metadata", m.EntityID); err != nil {
		return err
	}
	if err := m.ProviderID.Validate(); err != nil {
		return err
	}
	return validateEntityID("metadata key", m.Key)
}

// Validate checks that a queued operation has a valid provider-scoped target.
func (o SyncOperation) Validate() error {
	if err := o.ID.Validate(); err != nil {
		return err
	}
	if err := o.ProviderID.Validate(); err != nil {
		return err
	}
	if err := o.EntityType.Validate(); err != nil {
		return err
	}
	if err := validateEntityID("operation", o.EntityID); err != nil {
		return err
	}
	if err := o.Operation.Validate(); err != nil {
		return err
	}
	if err := o.Status.Validate(); err != nil {
		return err
	}
	if o.Attempts < 0 {
		return fmt.Errorf("%w: attempts cannot be negative", ErrInvalidID)
	}
	return nil
}

// Validate checks a field conflict's field identity.
func (f FieldConflict) Validate() error { return validateEntityID("conflict field", f.Field) }

// Validate checks conflict ownership and all field entries.
func (c Conflict) Validate() error {
	if err := c.ID.Validate(); err != nil {
		return err
	}
	if err := c.ProviderID.Validate(); err != nil {
		return err
	}
	if err := c.EntityType.Validate(); err != nil {
		return err
	}
	if err := validateEntityID("conflict", c.EntityID); err != nil {
		return err
	}
	for i, field := range c.Fields {
		if err := field.Validate(); err != nil {
			return fmt.Errorf("conflict field %d: %w", i, err)
		}
	}
	return nil
}

// Validate checks the identity of a stored synchronization snapshot.
func (b SyncBase) Validate() error {
	if err := b.ProviderID.Validate(); err != nil {
		return err
	}
	if b.EntityType.IsValid() || b.EntityID != "" {
		if err := b.EntityType.Validate(); err != nil {
			return err
		}
		if err := validateEntityID("sync base", b.EntityID); err != nil {
			return err
		}
	}
	return b.SyncState.Validate()
}

// Validate checks the key used for application state persistence.
func (s AppState) Validate() error { return validateEntityID("application state key", s.Key) }

// NormalizeUTC returns a copy with all timestamps represented in UTC.
func (p Provider) NormalizeUTC() Provider {
	p.LastSyncAt = normalizeTimePointer(p.LastSyncAt)
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p
}

// NormalizeUTC returns a copy with all timestamps represented in UTC.
func (s Space) NormalizeUTC() Space {
	s.RemoteUpdatedAt = normalizeTimePointer(s.RemoteUpdatedAt)
	s.CreatedAt = s.CreatedAt.UTC()
	s.UpdatedAt = s.UpdatedAt.UTC()
	return s
}

// NormalizeUTC returns a copy with all timestamps represented in UTC.
func (l List) NormalizeUTC() List {
	l.RemoteUpdatedAt = normalizeTimePointer(l.RemoteUpdatedAt)
	l.CreatedAt = l.CreatedAt.UTC()
	l.UpdatedAt = l.UpdatedAt.UTC()
	return l
}

// NormalizeUTC returns a copy with all timestamps represented in UTC.
func (t Task) NormalizeUTC() Task {
	t.DueAt = normalizeTimePointer(t.DueAt)
	t.CompletedAt = normalizeTimePointer(t.CompletedAt)
	t.RemoteUpdatedAt = normalizeTimePointer(t.RemoteUpdatedAt)
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	return t
}

// NormalizeUTC returns a copy with all timestamps represented in UTC.
func (o SyncOperation) NormalizeUTC() SyncOperation {
	o.CreatedAt = o.CreatedAt.UTC()
	o.LastAttemptAt = normalizeTimePointer(o.LastAttemptAt)
	o.NextAttemptAt = normalizeTimePointer(o.NextAttemptAt)
	return o
}

// NormalizeUTC returns a copy with all timestamps represented in UTC.
func (c Conflict) NormalizeUTC() Conflict {
	c.CreatedAt = c.CreatedAt.UTC()
	c.UpdatedAt = c.UpdatedAt.UTC()
	c.ResolvedAt = normalizeTimePointer(c.ResolvedAt)
	return c
}

// NormalizeUTC returns a copy with all timestamps represented in UTC.
func (b SyncBase) NormalizeUTC() SyncBase {
	b.RemoteUpdatedAt = normalizeTimePointer(b.RemoteUpdatedAt)
	b.CreatedAt = b.CreatedAt.UTC()
	b.UpdatedAt = b.UpdatedAt.UTC()
	return b
}

// NormalizeUTC returns a copy with the application-state timestamp in UTC.
func (s AppState) NormalizeUTC() AppState {
	s.UpdatedAt = s.UpdatedAt.UTC()
	return s
}

// SyncBase returns the synchronization fields for a space.
func (s Space) SyncBase() SyncBase {
	return SyncBase{
		ProviderID:      s.ProviderID,
		RemoteID:        s.RemoteID,
		SyncState:       s.SyncState,
		RemoteUpdatedAt: s.RemoteUpdatedAt,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		EntityType:      EntityTypeSpace,
		EntityID:        s.ID.String(),
	}
}

// SyncBase returns the synchronization fields for a list.
func (l List) SyncBase() SyncBase {
	return SyncBase{
		ProviderID:      l.ProviderID,
		RemoteID:        l.RemoteID,
		SyncState:       l.SyncState,
		RemoteUpdatedAt: l.RemoteUpdatedAt,
		CreatedAt:       l.CreatedAt,
		UpdatedAt:       l.UpdatedAt,
		EntityType:      EntityTypeList,
		EntityID:        l.ID.String(),
	}
}

// SyncBase returns the synchronization fields for a task.
func (t Task) SyncBase() SyncBase {
	return SyncBase{
		ProviderID:      t.ProviderID,
		RemoteID:        t.RemoteID,
		SyncState:       t.SyncState,
		RemoteUpdatedAt: t.RemoteUpdatedAt,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		EntityType:      EntityTypeTask,
		EntityID:        t.ID.String(),
	}
}

func normalizeTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}
