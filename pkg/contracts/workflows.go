package contracts

import (
	"encoding/json"
	"time"
)

// WorkflowCursor and TaskCursor use wire-compatible string identifiers so the
// contracts module does not depend on Core's domain or application packages.
type WorkflowCursor struct {
	ID        string
	CreatedAt time.Time
}

type TaskCursor struct {
	ID        string
	CreatedAt time.Time
}

type WorkflowListRequest struct {
	State  string
	Limit  int
	Before *WorkflowCursor
}

type WorkflowGetRequest struct {
	WorkflowID string
	Limit      int
	Before     *TaskCursor
}

type WorkflowRetryRequest struct {
	WorkflowID string
	TaskID     string
}

// WorkflowResponse carries Core's opaque workflow projection. The caller owns
// the platform-level type and decodes the payload into it.
type WorkflowResponse struct {
	Payload json.RawMessage
}

func WorkflowListOperation() Operation[WorkflowListRequest, WorkflowResponse] {
	return Operation[WorkflowListRequest, WorkflowResponse]{Key: Key{ID: "core.workflow.list", Version: 1}}
}

func WorkflowGetOperation() Operation[WorkflowGetRequest, WorkflowResponse] {
	return Operation[WorkflowGetRequest, WorkflowResponse]{Key: Key{ID: "core.workflow.get", Version: 1}}
}

func WorkflowRetryOperation() Operation[WorkflowRetryRequest, WorkflowResponse] {
	return Operation[WorkflowRetryRequest, WorkflowResponse]{Key: Key{ID: "core.workflow.retry", Version: 1}}
}
