package retirement

import (
	"context"
	"encoding/json"
	"fmt"

	workflow "github.com/Zubrium/cdn/pkg/contracts"
)

// RetirementStartConfig is the complete, server-owned configuration of the
// root retirement task.
type RetirementStartConfig struct {
	MaxAttempts int
}

// RetirementStartHandler validates the durable payload and declares one
// class-scoped Router child per server-owned route plus a dependent Website
// finalizer. It never calls an engine and cannot select a module or class.
type RetirementStartHandler struct {
	config RetirementStartConfig
}

func NewRetirementStartHandler(config RetirementStartConfig) (*RetirementStartHandler, error) {
	if config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, ErrUnavailable
	}
	return &RetirementStartHandler{config: config}, nil
}

func (handler *RetirementStartHandler) WorkerHandler(_ context.Context, task workflow.OperationTask) (workflow.OperationOutcome, error) {
	if handler == nil {
		return workflow.OperationOutcome{}, ErrUnavailable
	}
	payload, err := workflow.DecodePayload[workflow.RetirementWorkflowPayload](task.Payload)
	if err != nil {
		return workflow.OperationOutcome{}, fmt.Errorf("%w: malformed root payload", ErrInvalid)
	}
	if err := workflow.ValidateRetirementWorkflowPayload(payload); err != nil {
		return workflow.OperationOutcome{}, err
	}

	children := make([]workflow.OperationTaskChild, 0, len(payload.Routes)+1)
	result := workflow.RetirementStartResult{
		Format: workflow.RetirementWorkflowFormat, JobID: payload.JobID, PageID: payload.PageID,
		RouteEmissionKeys:   make([]string, 0, len(payload.Routes)),
		FinalizeEmissionKey: workflow.RetirementFinalizeEmissionKey(payload.JobID),
	}
	for _, routeID := range payload.Routes {
		routePayload, err := json.Marshal(workflow.RetirementRouteRemovePayload{
			Attempt: payload.Attempt, JobID: payload.JobID, PageID: payload.PageID, RouteID: routeID, IdempotencyKey: payload.IdempotencyKey,
		})
		if err != nil {
			return workflow.OperationOutcome{}, err
		}
		emission := workflow.RetirementRouteEmissionKey(payload.JobID, routeID)
		children = append(children, workflow.OperationTaskChild{
			OperationID: workflow.RetirementRouteRemoveOperationID, Version: workflow.RetirementChildContractVersion,
			EmissionKey: emission, Payload: routePayload, MaxAttempts: handler.config.MaxAttempts,
		})
		result.RouteEmissionKeys = append(result.RouteEmissionKeys, emission)
	}
	finalizePayload, err := json.Marshal(workflow.RetirementPageFinalizePayload{
		Attempt: payload.Attempt, JobID: payload.JobID, PageID: payload.PageID,
		ExpectedRevision: payload.ExpectedRevision, IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		return workflow.OperationOutcome{}, err
	}
	children = append(children, workflow.OperationTaskChild{
		OperationID: workflow.RetirementPageFinalizeOperationID, Version: workflow.RetirementChildContractVersion,
		EmissionKey: result.FinalizeEmissionKey, Payload: finalizePayload, MaxAttempts: handler.config.MaxAttempts,
		DependsOnEmissions: append([]string(nil), result.RouteEmissionKeys...),
	})
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return workflow.OperationOutcome{}, err
	}
	return workflow.OperationOutcome{Result: resultJSON, Children: children}, nil
}
