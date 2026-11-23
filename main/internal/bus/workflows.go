package bus

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var ErrInvalidWorkflowRequest = errors.New("invalid workflow operation request")

// WorkflowControl is the neutral workflow-control port exposed through the
// process-local bus. Only the composition root supplies a Core-backed
// implementation; consumers see only pkg/contracts DTOs.
type WorkflowControl interface {
	List(context.Context, contracts.AdminWorkflowListFilter) (contracts.AdminWorkflowPage, error)
	Get(context.Context, uuid.UUID, contracts.AdminWorkflowDetailFilter) (contracts.AdminWorkflowDetail, error)
	RetryTask(context.Context, uuid.UUID, uuid.UUID) error
}

func RegisterWorkflowControl(dispatcher *Dispatcher, control WorkflowControl) error {
	if dispatcher == nil || control == nil {
		return contracts.ErrInvalidOperation
	}
	if err := Register(dispatcher, contracts.WorkflowListOperation(), func(ctx context.Context, request contracts.WorkflowListRequest) (contracts.WorkflowResponse, error) {
		filter := contracts.AdminWorkflowListFilter{State: request.State, Limit: request.Limit}
		if request.Before != nil {
			id, err := parseWorkflowID(request.Before.ID)
			if err != nil {
				return contracts.WorkflowResponse{}, err
			}
			filter.Before = &contracts.AdminWorkflowCursor{ID: id, CreatedAt: request.Before.CreatedAt}
		}
		page, err := control.List(ctx, filter)
		if err != nil {
			return contracts.WorkflowResponse{}, err
		}
		return encodeWorkflowResponse(page)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.WorkflowGetOperation(), func(ctx context.Context, request contracts.WorkflowGetRequest) (contracts.WorkflowResponse, error) {
		id, err := parseWorkflowID(request.WorkflowID)
		if err != nil {
			return contracts.WorkflowResponse{}, err
		}
		filter := contracts.AdminWorkflowDetailFilter{Limit: request.Limit}
		if request.Before != nil {
			taskID, err := parseWorkflowID(request.Before.ID)
			if err != nil {
				return contracts.WorkflowResponse{}, err
			}
			filter.Before = &contracts.AdminTaskCursor{ID: taskID, CreatedAt: request.Before.CreatedAt}
		}
		detail, err := control.Get(ctx, id, filter)
		if err != nil {
			return contracts.WorkflowResponse{}, err
		}
		return encodeWorkflowResponse(detail)
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.WorkflowRetryOperation(), func(ctx context.Context, request contracts.WorkflowRetryRequest) (contracts.WorkflowResponse, error) {
		workflowID, err := parseWorkflowID(request.WorkflowID)
		if err != nil {
			return contracts.WorkflowResponse{}, err
		}
		taskID, err := parseWorkflowID(request.TaskID)
		if err != nil {
			return contracts.WorkflowResponse{}, err
		}
		if err := control.RetryTask(ctx, workflowID, taskID); err != nil {
			return contracts.WorkflowResponse{}, err
		}
		return encodeWorkflowResponse(struct{}{})
	})
}

func parseWorkflowID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrInvalidWorkflowRequest
	}
	return id, nil
}

func encodeWorkflowResponse(value any) (contracts.WorkflowResponse, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return contracts.WorkflowResponse{}, err
	}
	return contracts.WorkflowResponse{Payload: payload}, nil
}