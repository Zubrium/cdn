package bus

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

// WorkflowOperations is an internal Core-facing port. The module receiving
// calls sees only neutral DataBus operation DTOs in pkg/contracts.
type WorkflowOperations interface {
	Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error)
	Get(context.Context, uuid.UUID) (contracts.WorkflowState, error)
	GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (contracts.WorkflowTaskResult, error)
	ResolveTaskID(context.Context, uuid.UUID, string) (uuid.UUID, error)
}

func RegisterWorkflowOperations(dispatcher *Dispatcher, byModule map[string]WorkflowOperations) error {
	if dispatcher == nil || len(byModule) == 0 {
		return contracts.ErrInvalidOperation
	}
	for moduleID, operations := range byModule {
		if !moduleIdentityPattern.MatchString(moduleID) || operations == nil {
			return ErrInvalidModuleIdentity
		}
	}
	resolve := func(ctx context.Context) (WorkflowOperations, error) {
		moduleID := ModuleIDFromContext(ctx)
		operations, ok := byModule[moduleID]
		if !ok || operations == nil {
			return nil, ErrInvalidModuleIdentity
		}
		return operations, nil
	}
	if err := Register(dispatcher, contracts.WorkflowStartOperation(), func(ctx context.Context, request contracts.WorkflowStartRequest) (contracts.WorkflowStartResponse, error) {
		operations, err := resolve(ctx)
		if err != nil {
			return contracts.WorkflowStartResponse{}, err
		}
		receipt, err := operations.Start(ctx, request.Input)
		return contracts.WorkflowStartResponse{Receipt: receipt}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.WorkflowStateOperation(), func(ctx context.Context, request contracts.WorkflowStateRequest) (contracts.WorkflowStateResponse, error) {
		operations, err := resolve(ctx)
		if err != nil {
			return contracts.WorkflowStateResponse{}, err
		}
		workflow, err := operations.Get(ctx, request.WorkflowID)
		return contracts.WorkflowStateResponse{Workflow: workflow}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.WorkflowTaskResultOperation(), func(ctx context.Context, request contracts.WorkflowTaskResultRequest) (contracts.WorkflowTaskResultResponse, error) {
		operations, err := resolve(ctx)
		if err != nil {
			return contracts.WorkflowTaskResultResponse{}, err
		}
		result, err := operations.GetTaskResult(ctx, request.WorkflowID, request.TaskID)
		return contracts.WorkflowTaskResultResponse{TaskResult: result}, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.WorkflowTaskIDResolveOperation(), func(ctx context.Context, request contracts.WorkflowTaskIDResolveRequest) (contracts.WorkflowTaskIDResolveResponse, error) {
		operations, err := resolve(ctx)
		if err != nil {
			return contracts.WorkflowTaskIDResolveResponse{}, err
		}
		taskID, err := operations.ResolveTaskID(ctx, request.WorkflowID, request.EmissionKey)
		return contracts.WorkflowTaskIDResolveResponse{TaskID: taskID}, err
	})
}
