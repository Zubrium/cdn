package contracts

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Neutral Admin workflow projection DTOs. They mirror the wire shape of Core's
// workflow control projections without importing Core domain types.
type AdminTaskKind string

const (
	AdminTaskKindOperation AdminTaskKind = "operation"
	AdminTaskKindJoin      AdminTaskKind = "join"
)

type AdminOperationRef struct {
	ClassID string `json:"class_id"`
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type AdminWorkflowCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
}

type AdminTaskCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
}

type AdminWorkflowListFilter struct {
	State  string
	Limit  int
	Before *AdminWorkflowCursor
}

type AdminWorkflowDetailFilter struct {
	Limit  int
	Before *AdminTaskCursor
}

type AdminWorkflowSummary struct {
	ID           uuid.UUID `json:"id"`
	OwnerClassID string    `json:"owner_class_id"`
	State        string    `json:"state"`
	TaskCount    int       `json:"task_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type AdminWorkflowPage struct {
	Items []AdminWorkflowSummary `json:"items"`
	Next  *AdminWorkflowCursor   `json:"next,omitempty"`
}

type AdminWorkflow struct {
	ID           uuid.UUID       `json:"id"`
	OwnerClassID string          `json:"owner_class_id"`
	State        string          `json:"state"`
	RootTaskID   uuid.UUID       `json:"root_task_id"`
	Result       json.RawMessage `json:"result,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type AdminTaskSummary struct {
	ID           uuid.UUID          `json:"id"`
	WorkflowID   uuid.UUID          `json:"workflow_id"`
	Kind         AdminTaskKind      `json:"kind"`
	Operation    *AdminOperationRef `json:"operation,omitempty"`
	ParentTaskID uuid.UUID          `json:"parent_task_id"`
	DependsOn    []uuid.UUID        `json:"depends_on,omitempty"`
	EmissionKey  string             `json:"emission_key"`
	State        string             `json:"state"`
	Attempts     int                `json:"attempts"`
	MaxAttempts  int                `json:"max_attempts"`
	AvailableAt  time.Time          `json:"available_at"`
	LeaseUntil   *time.Time         `json:"lease_until,omitempty"`
	ErrorCode    string             `json:"error_code,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

type AdminTaskPage struct {
	Items []AdminTaskSummary `json:"items"`
	Next  *AdminTaskCursor   `json:"next,omitempty"`
}

type AdminWorkflowDetail struct {
	Workflow AdminWorkflow `json:"workflow"`
	TaskPage AdminTaskPage `json:"task_page"`
}
