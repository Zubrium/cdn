package composition

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var errWebsiteBuildUnavailable = errors.New("Website build capability is not initialized")

// websiteBuildWorkflowClient is the narrow, owner-scoped adapter supplied to
// Website application over neutral DataBus workflow DTOs. The dispatcher binds
// the module identity; no request can choose an operation, owner or task.
type websiteBuildWorkflowClient struct {
	dispatcher contracts.Dispatcher
}

func newWebsiteBuildWorkflowClient(dispatcher contracts.Dispatcher) *websiteBuildWorkflowClient {
	return &websiteBuildWorkflowClient{dispatcher: dispatcher}
}

func (client *websiteBuildWorkflowClient) Start(ctx context.Context, request contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowStartReceipt{}, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowStart(ctx, client.dispatcher, request)
}

func (client *websiteBuildWorkflowClient) Get(ctx context.Context, workflowID uuid.UUID) (contracts.WorkflowState, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowState{}, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowState(ctx, client.dispatcher, workflowID)
}

func (client *websiteBuildWorkflowClient) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowTaskResult{}, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowTaskResult(ctx, client.dispatcher, workflowID, taskID)
}

func (client *websiteBuildWorkflowClient) ResolveTaskID(ctx context.Context, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	if client == nil || client.dispatcher == nil {
		return uuid.Nil, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowTaskIDResolve(ctx, client.dispatcher, workflowID, emissionKey)
}
