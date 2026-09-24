package domain

const (
	MetadataKeyTaskColumns      = "task.columns"
	MetadataKeyTaskColumnValues = "task.column_values"
)

// TaskColumn is a provider-neutral description of a dynamic task column.
type TaskColumn struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// TaskColumnValues contains display-ready values keyed by TaskColumn.ID.
type TaskColumnValues map[string]string
