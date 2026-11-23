package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Workflow transport DTOs are operation payloads. They intentionally contain
// no Core runtime, owner capability, task handler, or repository interface.
type WorkflowStartInput struct {
	OperationID     string          `json:"operation_id"`
	ContractVersion int             `json:"contract_version"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Payload         json.RawMessage `json:"payload"`
}

type WorkflowStartReceipt struct {
	WorkflowID uuid.UUID `json:"workflow_id"`
	RootTaskID uuid.UUID `json:"root_task_id"`
	State      string    `json:"state"`
	Created    bool      `json:"created"`
}

type WorkflowState struct {
	ID         uuid.UUID       `json:"id"`
	State      string          `json:"state"`
	RootTaskID uuid.UUID       `json:"root_task_id"`
	Result     json.RawMessage `json:"result"`
}

type WorkflowTaskResult struct {
	TaskID     uuid.UUID             `json:"task_id"`
	WorkflowID uuid.UUID             `json:"workflow_id"`
	State      string                `json:"state"`
	Result     json.RawMessage       `json:"result"`
	ErrorCode  string                `json:"error_code"`
	Operation  *WorkflowOperationRef `json:"operation,omitempty"`
}

type WorkflowOperationRef struct {
	ClassID string `json:"class_id"`
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type WorkflowStartRequest struct {
	Input WorkflowStartInput `json:"input"`
}

type WorkflowStartResponse struct {
	Receipt WorkflowStartReceipt `json:"receipt"`
}

type WorkflowStateRequest struct {
	WorkflowID uuid.UUID `json:"workflow_id"`
}

type WorkflowStateResponse struct {
	Workflow WorkflowState `json:"workflow"`
}

type WorkflowTaskResultRequest struct {
	WorkflowID uuid.UUID `json:"workflow_id"`
	TaskID     uuid.UUID `json:"task_id"`
}

type WorkflowTaskResultResponse struct {
	TaskResult WorkflowTaskResult `json:"task_result"`
}

type WorkflowTaskIDResolveRequest struct {
	WorkflowID  uuid.UUID `json:"workflow_id"`
	EmissionKey string    `json:"emission_key"`
}

type WorkflowTaskIDResolveResponse struct {
	TaskID uuid.UUID `json:"task_id"`
}

func CallWorkflowStart(ctx context.Context, dispatcher Dispatcher, input WorkflowStartInput) (WorkflowStartReceipt, error) {
	response, err := Call(ctx, dispatcher, WorkflowStartOperation(), WorkflowStartRequest{Input: input})
	return response.Receipt, err
}

func CallWorkflowState(ctx context.Context, dispatcher Dispatcher, workflowID uuid.UUID) (WorkflowState, error) {
	response, err := Call(ctx, dispatcher, WorkflowStateOperation(), WorkflowStateRequest{WorkflowID: workflowID})
	return response.Workflow, err
}

func CallWorkflowTaskResult(ctx context.Context, dispatcher Dispatcher, workflowID, taskID uuid.UUID) (WorkflowTaskResult, error) {
	response, err := Call(ctx, dispatcher, WorkflowTaskResultOperation(), WorkflowTaskResultRequest{WorkflowID: workflowID, TaskID: taskID})
	return response.TaskResult, err
}

func CallWorkflowTaskIDResolve(ctx context.Context, dispatcher Dispatcher, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	response, err := Call(ctx, dispatcher, WorkflowTaskIDResolveOperation(), WorkflowTaskIDResolveRequest{WorkflowID: workflowID, EmissionKey: emissionKey})
	return response.TaskID, err
}

var ErrWorkflowTaskMissing = errors.New("workflow task is missing")
var ErrWorkflowTaskNotOwned = errors.New("workflow task is not owned")
var ErrWorkflowUnavailable = errors.New("workflow service unavailable")

func WorkflowStartOperation() Operation[WorkflowStartRequest, WorkflowStartResponse] {
	return Operation[WorkflowStartRequest, WorkflowStartResponse]{
		Key: Key{ID: "core.coordinator.workflow.start", Version: 1},
		Validate: func(request WorkflowStartRequest) error {
			input := request.Input
			if input.OperationID == "" || input.ContractVersion <= 0 || input.IdempotencyKey == "" || len(input.Payload) == 0 {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func WorkflowStateOperation() Operation[WorkflowStateRequest, WorkflowStateResponse] {
	return Operation[WorkflowStateRequest, WorkflowStateResponse]{Key: Key{ID: "core.coordinator.workflow.get", Version: 1}, Validate: func(request WorkflowStateRequest) error {
		if request.WorkflowID == uuid.Nil {
			return ErrInvalidOperation
		}
		return nil
	}}
}

func WorkflowTaskResultOperation() Operation[WorkflowTaskResultRequest, WorkflowTaskResultResponse] {
	return Operation[WorkflowTaskResultRequest, WorkflowTaskResultResponse]{Key: Key{ID: "core.coordinator.workflow.task-result", Version: 1}, Validate: func(request WorkflowTaskResultRequest) error {
		if request.WorkflowID == uuid.Nil || request.TaskID == uuid.Nil {
			return ErrInvalidOperation
		}
		return nil
	}}
}

func WorkflowTaskIDResolveOperation() Operation[WorkflowTaskIDResolveRequest, WorkflowTaskIDResolveResponse] {
	return Operation[WorkflowTaskIDResolveRequest, WorkflowTaskIDResolveResponse]{Key: Key{ID: "core.coordinator.workflow.resolve-task-id", Version: 1}, Validate: func(request WorkflowTaskIDResolveRequest) error {
		if request.WorkflowID == uuid.Nil || request.EmissionKey == "" {
			return ErrInvalidOperation
		}
		return nil
	}}
}

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
