package contracts

import (
	"encoding/json"
	"time"
)

// OperationTask is the transport-neutral input sent through the DataBus to a
// registered operation. Core persistence fields and component identities stay
// inside the composition adapter.
type OperationTask struct {
	TaskID         string          `json:"task_id"`
	WorkflowID     string          `json:"workflow_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	EmissionKey    string          `json:"emission_key"`
	Payload        json.RawMessage `json:"payload"`
}

// OperationTaskChild is a follow-up operation emitted by a successful task.
type OperationTaskChild struct {
	OperationID        string          `json:"operation_id"`
	Version            uint32          `json:"version"`
	EmissionKey        string          `json:"emission_key"`
	Payload            json.RawMessage `json:"payload"`
	MaxAttempts        int             `json:"max_attempts"`
	AvailableAt        time.Time       `json:"available_at,omitempty"`
	DependsOn          []string        `json:"depends_on,omitempty"`
	DependsOnEmissions []string        `json:"depends_on_emissions,omitempty"`
}

// OperationOutcome contains an operation result and optional child operations.
type OperationOutcome struct {
	Result   json.RawMessage      `json:"result"`
	Children []OperationTaskChild `json:"children,omitempty"`
}
