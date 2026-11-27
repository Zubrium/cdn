package contentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

const (
	bridgePollInterval = 25 * time.Millisecond
	bridgeWaitTimeout  = 30 * time.Second
)

var (
	selectionDigestPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	configurationScopePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
)

func validSelectionDigest(value string) bool { return selectionDigestPattern.MatchString(value) }

// WorkflowClient is the narrow Core-facing port used by the synchronous
// bridge. It carries no engine handle, owner capability or task handler.
type WorkflowClient interface {
	Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error)
	Get(context.Context, uuid.UUID) (contracts.WorkflowState, error)
	GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (contracts.WorkflowTaskResult, error)
	ResolveTaskID(context.Context, uuid.UUID, string) (uuid.UUID, error)
}

// Client starts a neutral root task and waits only for the single child task
// the root declared. The caller receives a validated neutral DTO; it can
// neither select a workflow owner nor name another task.
type Client struct {
	workflows          WorkflowClient
	configurationScope string
	pollInterval       time.Duration
	waitTimeout        time.Duration
}

// NewClient binds the bridge to the current shared configuration fingerprint.
// Core scopes durable workflow results to the instance's config epoch, so a
// different configuration must not replay a workflow started by another epoch.
func NewClient(workflows WorkflowClient, configurationScope string) (*Client, error) {
	if workflows == nil {
		return nil, ErrUnavailable
	}
	if !configurationScopePattern.MatchString(configurationScope) {
		return nil, ErrInvalid
	}
	return &Client{
		workflows:          workflows,
		configurationScope: configurationScope,
		pollInterval:       bridgePollInterval,
		waitTimeout:        bridgeWaitTimeout,
	}, nil
}

func (client *Client) idempotencyKey(kind string, parts ...string) string {
	key := "content-bridge/" + client.configurationScope + "/" + kind
	for _, part := range parts {
		key += "/" + part
	}
	return key
}

// Provisioning reads and read-only reads are distinct workflows: the root
// payload differs by allow_provisioning, and Core rejects a reused key with a
// different input.
func (client *Client) contentProjectionIdempotencyKey(request contracts.ContentProjectionRequest) string {
	key := client.idempotencyKey("projection", request.PageSnapshotID, request.Locale)
	if request.AllowProvisioning {
		key += "/provisioning"
	}
	return key
}

func (client *Client) ReadProjection(ctx context.Context, request contracts.ContentProjectionRequest) (contracts.ContentProjectionResponse, error) {
	if client == nil || client.workflows == nil {
		return contracts.ContentProjectionResponse{}, ErrUnavailable
	}
	if err := contracts.ValidateWebsiteProjectionPayload(request); err != nil {
		return contracts.ContentProjectionResponse{}, err
	}
	payload, err := json.Marshal(contracts.ContentProjectionWorkflowPayload{
		PageSnapshotID: request.PageSnapshotID, Locale: request.Locale,
		AllowProvisioning: request.AllowProvisioning,
	})
	if err != nil {
		return contracts.ContentProjectionResponse{}, ErrInvalid
	}
	idempotencyKey := client.contentProjectionIdempotencyKey(request)
	emission := contracts.ContentProjectionEmissionKey(request.PageSnapshotID, request.Locale)
	result, err := client.run(ctx, contracts.ContentProjectionOperationID, idempotencyKey, payload,
		emission, "", contracts.ContentProjectionWorkflowFormat, "engine.content", "engine.website", contracts.WebsiteProjectionOperationID)
	if err != nil {
		return contracts.ContentProjectionResponse{}, err
	}
	response, err := contracts.DecodePayload[contracts.ContentProjectionResponse](result)
	if err != nil || response.Format != contracts.WebsiteProjectionResultFormat ||
		response.Projection.Page.PageSnapshotID != request.PageSnapshotID || response.Projection.Locale != request.Locale {
		return contracts.ContentProjectionResponse{}, ErrInvalid
	}
	return response, nil
}

func (client *Client) ReadBuild(ctx context.Context, request contracts.ContentBuildProjectionRequest) (contracts.ContentBuildProjectionResponse, error) {
	if client == nil || client.workflows == nil {
		return contracts.ContentBuildProjectionResponse{}, ErrUnavailable
	}
	workflowPayload := contracts.ContentBuildWorkflowPayload{
		PageSnapshotID: request.PageSnapshotID, Locale: request.Locale,
		ExpectedPublicationRevision: request.ExpectedPublicationRevision,
	}
	if err := contracts.ValidateBuildWorkflowPayload(workflowPayload); err != nil {
		return contracts.ContentBuildProjectionResponse{}, err
	}
	payload, err := json.Marshal(workflowPayload)
	if err != nil {
		return contracts.ContentBuildProjectionResponse{}, ErrInvalid
	}
	idempotencyKey := client.idempotencyKey("build", request.PageSnapshotID, request.Locale)
	if request.ExpectedPublicationRevision > 0 {
		idempotencyKey += "/revision/" + strconv.FormatInt(request.ExpectedPublicationRevision, 10)
	}
	emission := contracts.ContentBuildEmissionKey(request.PageSnapshotID, request.Locale)
	projectionEmission := contracts.ContentBuildProjectionEmissionKey(request.PageSnapshotID, request.Locale)
	result, err := client.run(ctx, contracts.ContentBuildOperationID, idempotencyKey, payload,
		emission, projectionEmission, contracts.ContentBuildWorkflowFormat, "engine.website", "engine.content", contracts.ContentBuildResolveOperationID)
	if err != nil {
		return contracts.ContentBuildProjectionResponse{}, err
	}
	response, err := contracts.DecodePayload[contracts.ContentBuildProjectionResponse](result)
	if err != nil || response.PublicationRevision < 0 || !validSelectionDigest(response.SelectionDigest) {
		return contracts.ContentBuildProjectionResponse{}, ErrInvalid
	}
	if request.ExpectedPublicationRevision > 0 && response.PublicationRevision != request.ExpectedPublicationRevision {
		return contracts.ContentBuildProjectionResponse{}, ErrInvalid
	}
	return response, nil
}

func (client *Client) ReadLocale(ctx context.Context, request contracts.ContentLocaleCatalogRequest) (contracts.ContentLocaleCatalogResponse, error) {
	if client == nil || client.workflows == nil {
		return contracts.ContentLocaleCatalogResponse{}, ErrUnavailable
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return contracts.ContentLocaleCatalogResponse{}, ErrInvalid
	}
	result, err := client.run(ctx, contracts.ContentLocaleOperationID, client.idempotencyKey("locale/catalog"), payload,
		contracts.ContentLocaleEmissionKey, "", contracts.ContentLocaleWorkflowFormat, "engine.website", "engine.content", contracts.ContentLocaleReadOperationID)
	if err != nil {
		return contracts.ContentLocaleCatalogResponse{}, err
	}
	response, err := contracts.DecodePayload[contracts.ContentLocaleCatalogResponse](result)
	if err != nil || response.Revision < 1 || len(response.Locales) == 0 {
		return contracts.ContentLocaleCatalogResponse{}, ErrInvalid
	}
	return response, nil
}

func (client *Client) run(ctx context.Context, operationID, idempotencyKey string, payload json.RawMessage,
	emission, projectionEmission, format, rootTaskClass, childTaskClass, childOperationID string) (json.RawMessage, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !contracts.ValidContentBridgeEmissionKey(emission) || idempotencyKey == "" || len(payload) == 0 {
		return nil, ErrInvalid
	}
	receipt, err := client.workflows.Start(ctx, contracts.WorkflowStartInput{
		OperationID: operationID, ContractVersion: contracts.ContentBridgeRootVersion,
		IdempotencyKey: idempotencyKey, Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	if receipt.WorkflowID == uuid.Nil || receipt.RootTaskID == uuid.Nil {
		return nil, ErrUnavailable
	}

	waitCtx, cancel := context.WithTimeout(ctx, client.waitTimeout)
	defer cancel()
	for {
		workflow, err := client.workflows.Get(waitCtx, receipt.WorkflowID)
		if err != nil {
			return nil, err
		}
		if workflow.ID != uuid.Nil && workflow.ID != receipt.WorkflowID ||
			workflow.RootTaskID != uuid.Nil && workflow.RootTaskID != receipt.RootTaskID {
			return nil, ErrInvalid
		}
		switch workflow.State {
		case "queued", "running":
		case "succeeded":
			return client.childResult(waitCtx, receipt, emission, projectionEmission, format, operationID, rootTaskClass, childTaskClass, childOperationID)
		case "failed", "cancelled":
			return nil, ErrUnavailable
		default:
			return nil, ErrInvalid
		}
		select {
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		case <-time.After(client.pollInterval):
		}
	}
}

func (client *Client) childResult(ctx context.Context, receipt contracts.WorkflowStartReceipt,
	emission, projectionEmission, format, rootOperationID, rootTaskClass, childTaskClass, childOperationID string) (json.RawMessage, error) {
	root, err := client.workflows.GetTaskResult(ctx, receipt.WorkflowID, receipt.RootTaskID)
	if err != nil {
		return nil, err
	}
	if root.State != "succeeded" || root.Operation == nil || root.Operation.ClassID != rootTaskClass || root.Operation.ID != rootOperationID {
		return nil, ErrInvalid
	}
	rootResult, err := contracts.DecodePayload[contracts.ContentBridgeRootResult](root.Result)
	if err != nil || rootResult.Format != format || rootResult.EmissionKey != emission ||
		rootResult.ProjectionEmissionKey != projectionEmission {
		return nil, ErrInvalid
	}
	for {
		taskID, err := client.workflows.ResolveTaskID(ctx, receipt.WorkflowID, emission)
		if err == nil && taskID == uuid.Nil {
			err = contracts.ErrWorkflowTaskMissing
		}
		if err != nil && !errors.Is(err, contracts.ErrWorkflowTaskMissing) {
			return nil, err
		}
		if err == nil {
			result, err := client.workflows.GetTaskResult(ctx, receipt.WorkflowID, taskID)
			if err != nil {
				return nil, err
			}
			if result.TaskID != taskID || result.WorkflowID != receipt.WorkflowID ||
				result.State != "succeeded" || result.Operation == nil ||
				result.Operation.ClassID != childTaskClass || result.Operation.ID != childOperationID ||
				result.Operation.Version != contracts.ContentBridgeChildVersion {
				return nil, ErrInvalid
			}
			return result.Result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(client.pollInterval):
		}
	}
}
