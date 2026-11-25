package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

// coreWorkflowOperations adapts Core's owner-scoped Coordinator API to neutral
// DataBus DTOs. It is registered only for statically selected module owners.
type coreWorkflowOperations struct {
	runtime *platform.Runtime
	resolve func() *platform.Runtime
	owner   string
}

func (adapter coreWorkflowOperations) currentRuntime() *platform.Runtime {
	if adapter.resolve != nil {
		return adapter.resolve()
	}
	return adapter.runtime
}

func (adapter coreWorkflowOperations) client() (*platform.WorkflowClient, error) {
	runtime := adapter.currentRuntime()
	if runtime == nil || adapter.owner == "" {
		return nil, contracts.ErrWorkflowUnavailable
	}
	client, err := runtime.NewWorkflowClient(adapter.owner)
	if err != nil {
		return nil, mapWorkflowError(err)
	}
	return client, nil
}

func (adapter coreWorkflowOperations) Start(ctx context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	client, err := adapter.client()
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	receipt, err := client.Start(ctx, platform.WorkflowStartRequest{
		OperationID: input.OperationID, ContractVersion: input.ContractVersion,
		IdempotencyKey: input.IdempotencyKey, Payload: input.Payload,
	})
	if err != nil {
		return contracts.WorkflowStartReceipt{}, mapWorkflowError(err)
	}
	return contracts.WorkflowStartReceipt{WorkflowID: receipt.WorkflowID, RootTaskID: receipt.RootTaskID, State: receipt.State, Created: receipt.Created}, nil
}

func (adapter coreWorkflowOperations) Get(ctx context.Context, workflowID uuid.UUID) (contracts.WorkflowState, error) {
	client, err := adapter.client()
	if err != nil {
		return contracts.WorkflowState{}, err
	}
	workflow, err := client.Get(ctx, workflowID)
	if err != nil {
		return contracts.WorkflowState{}, mapWorkflowError(err)
	}
	if workflow.OwnerClassID != "" && workflow.OwnerClassID != adapter.owner {
		return contracts.WorkflowState{}, contracts.ErrWorkflowTaskNotOwned
	}
	return contracts.WorkflowState{ID: workflow.ID, RootTaskID: workflow.RootTaskID, State: workflow.State, Result: workflow.Result}, nil
}

func (adapter coreWorkflowOperations) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	client, err := adapter.client()
	if err != nil {
		return contracts.WorkflowTaskResult{}, err
	}
	result, err := client.GetTaskResult(ctx, workflowID, taskID)
	if err != nil {
		return contracts.WorkflowTaskResult{}, mapWorkflowError(err)
	}
	contractResult := contracts.WorkflowTaskResult{WorkflowID: result.WorkflowID, TaskID: result.TaskID, State: result.State, Result: result.Result, ErrorCode: result.ErrorCode}
	if result.Operation != nil {
		contractResult.Operation = &contracts.WorkflowOperationRef{ClassID: result.Operation.ClassID, ID: result.Operation.ID, Version: result.Operation.Version}
	}
	return contractResult, nil
}

func (adapter coreWorkflowOperations) ResolveTaskID(ctx context.Context, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	if ctx == nil || workflowID == uuid.Nil || emissionKey == "" {
		return uuid.Nil, contracts.ErrInvalidOperation
	}
	runtime := adapter.currentRuntime()
	if runtime == nil || adapter.owner == "" {
		return uuid.Nil, contracts.ErrWorkflowUnavailable
	}
	control, err := runtime.NewWorkflowControl()
	if err != nil {
		return uuid.Nil, mapWorkflowError(err)
	}
	var before *platform.TaskCursor
	for page := 0; page < 1000; page++ {
		detail, err := control.Get(ctx, workflowID, platform.WorkflowDetailFilter{Limit: 1000, Before: before})
		if err != nil {
			return uuid.Nil, mapWorkflowError(err)
		}
		if detail.Workflow.OwnerClassID != "" && detail.Workflow.OwnerClassID != adapter.owner {
			return uuid.Nil, contracts.ErrWorkflowTaskNotOwned
		}
		for _, task := range detail.TaskPage.Items {
			if task.EmissionKey == emissionKey && task.ID != uuid.Nil {
				return task.ID, nil
			}
		}
		if detail.TaskPage.Next == nil {
			break
		}
		cursor := *detail.TaskPage.Next
		if cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
			return uuid.Nil, contracts.ErrInvalidOperation
		}
		before = &cursor
	}
	return uuid.Nil, contracts.ErrWorkflowTaskMissing
}

func mapWorkflowError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platform.ErrTaskMissing):
		return errors.Join(contracts.ErrWorkflowTaskMissing, err)
	case errors.Is(err, platform.ErrTaskNotOwned):
		return errors.Join(contracts.ErrWorkflowTaskNotOwned, err)
	case errors.Is(err, platform.ErrCoordinatorNotReady), errors.Is(err, platform.ErrUnavailable):
		return errors.Join(contracts.ErrWorkflowUnavailable, err)
	default:
		return err
	}
}
