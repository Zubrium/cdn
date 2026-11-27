package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var errContentBridgeDependencyUnavailable = errors.New("content bridge worker dependency is unavailable")

// contentBridgeWorkflowClient is the module-scoped Core workflow port. It is
// assembled only by composition and never exposed to an engine domain package.
type contentBridgeWorkflowClient struct{ dispatcher contracts.Dispatcher }

func newContentBridgeWorkflowClient(dispatcher contracts.Dispatcher) *contentBridgeWorkflowClient {
	return &contentBridgeWorkflowClient{dispatcher: dispatcher}
}

func (client *contentBridgeWorkflowClient) Start(ctx context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowStartReceipt{}, errContentBridgeDependencyUnavailable
	}
	return contracts.CallWorkflowStart(ctx, client.dispatcher, input)
}

func (client *contentBridgeWorkflowClient) Get(ctx context.Context, id uuid.UUID) (contracts.WorkflowState, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowState{}, errContentBridgeDependencyUnavailable
	}
	return contracts.CallWorkflowState(ctx, client.dispatcher, id)
}

func (client *contentBridgeWorkflowClient) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowTaskResult{}, errContentBridgeDependencyUnavailable
	}
	return contracts.CallWorkflowTaskResult(ctx, client.dispatcher, workflowID, taskID)
}

func (client *contentBridgeWorkflowClient) ResolveTaskID(ctx context.Context, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	if client == nil || client.dispatcher == nil {
		return uuid.Nil, errContentBridgeDependencyUnavailable
	}
	return contracts.CallWorkflowTaskIDResolve(ctx, client.dispatcher, workflowID, emissionKey)
}

// websiteProjectionWorker is the narrow Website-side adapter. It owns no
// dispatcher and cannot select an operation, owner or class from its payload.
type websiteProjectionWorker struct{ core *coreComponent }

func (worker websiteProjectionWorker) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	request, err := contracts.DecodePayload[contracts.ContentProjectionRequest](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, contentbridge.ErrInvalid
	}
	if err := contracts.ValidateWebsiteProjectionPayload(request); err != nil {
		return contracts.OperationOutcome{}, err
	}
	if worker.core == nil {
		return contracts.OperationOutcome{}, errContentBridgeDependencyUnavailable
	}
	website := worker.core.websiteHandle.Load()
	if website == nil || website.projectionProvider == nil || website.serviceCtx == nil {
		return contracts.OperationOutcome{}, errContentBridgeDependencyUnavailable
	}
	projection, err := website.projectionProvider.getContentProjectionsForLocaleAllowState(
		website.serviceCtx, request.PageSnapshotID, request.Locale, request.AllowProvisioning)
	if err != nil || !projection.VerifyScope(request.PageSnapshotID) || projection.Locale != request.Locale {
		return contracts.OperationOutcome{}, errContentBridgeDependencyUnavailable
	}
	response := contracts.ContentProjectionResponse{
		Format: contracts.WebsiteProjectionResultFormat, Projection: encodeContentProjection(projection),
	}
	result, err := json.Marshal(response)
	if err != nil {
		return contracts.OperationOutcome{}, contentbridge.ErrInvalid
	}
	return contracts.OperationOutcome{Result: result}, nil
}

// contentBuildWorker is the narrow Content-side adapter for the authoritative
// publish revision and locale-specific editorial selection digest.
type contentBuildWorker struct {
	core      *coreComponent
	workflows contentbridge.WorkflowClient
}

func (worker contentBuildWorker) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	request, err := contracts.DecodePayload[contracts.ContentBuildProjectionRequest](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, contentbridge.ErrInvalid
	}
	if err := contracts.ValidateBuildWorkflowPayload(contracts.ContentBuildWorkflowPayload{
		PageSnapshotID: request.PageSnapshotID, Locale: request.Locale,
		ExpectedPublicationRevision: request.ExpectedPublicationRevision,
	}); err != nil {
		return contracts.OperationOutcome{}, err
	}
	buildEmission := contracts.ContentBuildEmissionKey(request.PageSnapshotID, request.Locale)
	if task.EmissionKey != buildEmission {
		return contracts.OperationOutcome{}, contentbridge.ErrInvalid
	}
	if worker.core == nil || worker.workflows == nil {
		return contracts.OperationOutcome{}, fmt.Errorf("content build worker dependencies: %w", errContentBridgeDependencyUnavailable)
	}
	content := worker.core.contentHandle.Load()
	if content == nil {
		return contracts.OperationOutcome{}, fmt.Errorf("content engine handle: %w", errContentBridgeDependencyUnavailable)
	}
	projection, err := worker.upstreamProjection(ctx, task.WorkflowID, request.PageSnapshotID, request.Locale)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	// Workspace resolution needs the server-owned authenticated Content
	// context; the task context carries only the Core workflow identity.
	response, err := content.ResolveContentBuildProjectionFromProjections(content.serviceCtx, projection, request.Locale)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	result, err := json.Marshal(response)
	if err != nil {
		return contracts.OperationOutcome{}, contentbridge.ErrInvalid
	}
	return contracts.OperationOutcome{Result: result}, nil
}

// upstreamProjection resolves the declared Website child by emission key. The
// build worker cannot choose another task, owner or operation; Core has already
// enforced the dependency declared by the root workflow.
func (worker contentBuildWorker) upstreamProjection(ctx context.Context, workflowIDText, pageSnapshotID, locale string) (contentapi.Projections, error) {
	workflowID, err := uuid.Parse(workflowIDText)
	if err != nil || workflowID == uuid.Nil {
		return contentapi.Projections{}, contentbridge.ErrInvalid
	}
	emission := contracts.ContentBuildProjectionEmissionKey(pageSnapshotID, locale)
	taskID, err := worker.workflows.ResolveTaskID(ctx, workflowID, emission)
	if err != nil {
		return contentapi.Projections{}, fmt.Errorf("resolve build projection task: %w", err)
	}
	if taskID == uuid.Nil {
		return contentapi.Projections{}, fmt.Errorf("resolve build projection task: %w", errContentBridgeDependencyUnavailable)
	}
	result, err := worker.workflows.GetTaskResult(ctx, workflowID, taskID)
	if err != nil {
		return contentapi.Projections{}, fmt.Errorf("read build projection task result: %w", err)
	}
	if result.TaskID != taskID || result.WorkflowID != workflowID || result.State != "succeeded" ||
		result.Operation == nil || result.Operation.ClassID != contentBridgeTaskClassWebsite ||
		result.Operation.ID != contracts.ContentBuildProjectionOperationID ||
		result.Operation.Version != contracts.ContentBridgeChildVersion {
		return contentapi.Projections{}, contentbridge.ErrInvalid
	}
	response, err := contracts.DecodePayload[contracts.ContentProjectionResponse](result.Result)
	if err != nil || response.Format != contracts.WebsiteProjectionResultFormat ||
		response.Projection.Page.PageSnapshotID != pageSnapshotID || response.Projection.Locale != locale {
		return contentapi.Projections{}, contentbridge.ErrInvalid
	}
	projection := decodeContentProjection(response.Projection)
	if !projection.VerifyScope(pageSnapshotID) {
		return contentapi.Projections{}, contentbridge.ErrInvalid
	}
	return projection, nil
}

// contentLocaleWorker is the narrow Content-side adapter for the locale
// catalog revision. The service context is server-owned and never request data.
type contentLocaleWorker struct{ core *coreComponent }

func (worker contentLocaleWorker) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	request, err := contracts.DecodePayload[contracts.ContentLocaleWorkflowPayload](task.Payload)
	if err != nil || contracts.ValidateLocaleWorkflowPayload(request) != nil {
		return contracts.OperationOutcome{}, contentbridge.ErrInvalid
	}
	if worker.core == nil {
		return contracts.OperationOutcome{}, errContentBridgeDependencyUnavailable
	}
	content := worker.core.contentHandle.Load()
	if content == nil || content.engine == nil || content.serviceCtx == nil {
		return contracts.OperationOutcome{}, errContentBridgeDependencyUnavailable
	}
	config, err := content.engine.GetLocaleConfig(content.serviceCtx)
	if err != nil || config.Revision < 1 {
		return contracts.OperationOutcome{}, errContentBridgeDependencyUnavailable
	}
	response, err := json.Marshal(contracts.ContentLocaleCatalogResponse{
		Revision: config.Revision, Locales: append([]string(nil), config.Locales...),
	})
	if err != nil {
		return contracts.OperationOutcome{}, contentbridge.ErrInvalid
	}
	return contracts.OperationOutcome{Result: response}, nil
}
