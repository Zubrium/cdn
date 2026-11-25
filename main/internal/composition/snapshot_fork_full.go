//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

type snapshotForkWorkflowStarter struct{ dispatcher contracts.Dispatcher }

func (s snapshotForkWorkflowStarter) Start(ctx context.Context, request contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	if s.dispatcher == nil {
		return contracts.WorkflowStartReceipt{}, contracts.ErrInvalidOperation
	}
	return contracts.CallWorkflowStart(ctx, s.dispatcher, request)
}

type snapshotForkContentCopierHandle struct{ component *coreComponent }

func (h snapshotForkContentCopierHandle) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	payload, err := websiteworkflow.DecodeForkPayload(task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if err := h.CopySnapshotContent(ctx, payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	return contracts.OperationOutcome{Result: []byte(`{"format":"website.snapshot-fork-copy/v1"}`)}, nil
}

func (h snapshotForkContentCopierHandle) CopySnapshotContent(ctx context.Context, payload websiteapi.SnapshotForkWorkflowPayload) error {
	if h.component == nil {
		return errors.New("content fork dependency is not initialized")
	}
	content := h.component.contentHandle.Load()
	if content == nil || content.engine == nil || content.projections == nil {
		return errors.New("content fork dependency is not initialized")
	}
	return snapshotForkContentCopier{
		engine: content.engine, projections: content.projections,
		locale: content.config.ContentAPI.Locale, credential: content.config.ContentAPI.ServiceCredential,
	}.CopySnapshotContent(ctx, payload)
}

type snapshotForkWebsiteFinalizerHandle struct{ component *coreComponent }

func (h snapshotForkWebsiteFinalizerHandle) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	payload, err := contracts.DecodePayload[websiteworkflow.ForkFinalizePayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if _, err := uuid.Parse(payload.SnapshotID); err != nil || payload.ExpectedRevision < 1 {
		return contracts.OperationOutcome{}, websiteworkflow.ErrInvalidPayload
	}
	snapshot, err := h.MarkSnapshotReady(ctx, payload.SnapshotID, payload.ExpectedRevision)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	result, err := json.Marshal(websiteworkflow.ForkFinalizeResult{
		Format: websiteworkflow.ForkRootFormat, SnapshotID: payload.SnapshotID,
		Revision: snapshot.Revision,
	})
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	return contracts.OperationOutcome{Result: result}, nil
}

func (h snapshotForkWebsiteFinalizerHandle) MarkSnapshotReady(ctx context.Context, snapshotID string, expectedRevision int64) (websiteapi.Snapshot, error) {
	if h.component == nil {
		return websiteapi.Snapshot{}, errors.New("website fork dependency is not initialized")
	}
	website := h.component.websiteHandle.Load()
	if website == nil || website.service == nil {
		return websiteapi.Snapshot{}, errors.New("website fork dependency is not initialized")
	}
	credential := h.component.config.ContentAPI.ServiceCredential
	if credential == "" {
		return websiteapi.Snapshot{}, errors.New("website service credential is not configured")
	}
	authCtx, err := website.service.Authenticate(ctx, credential)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	return website.service.MarkSnapshotReady(authCtx, snapshotID, expectedRevision)
}

// snapshotForkContentPort and snapshotForkProjectionPort are narrow ports
// implemented by the compiled Content component. The worker handler holds only
// these ports; engine identity and credentials stay server-side.
type snapshotForkContentPort interface {
	Authenticate(context.Context, string) (context.Context, error)
	GetWorkspace(context.Context, contentapi.Projections) (contentapi.Workspace, error)
	CopySnapshotContent(context.Context, contentapi.CopySnapshotContentRequest) (contentapi.CopySnapshotContentResult, error)
}

type snapshotForkProjectionPort interface {
	GetContentProjectionsForLocale(context.Context, string, string) (contentapi.Projections, error)
	getContentProjectionsForLocaleAllowState(context.Context, string, string, bool) (contentapi.Projections, error)
}

type snapshotForkContentCopier struct {
	engine      snapshotForkContentPort
	projections snapshotForkProjectionPort
	locale      string
	credential  string
}

func snapshotForkStageError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("snapshot fork %s: %w", stage, err)
}

func (c snapshotForkContentCopier) CopySnapshotContent(ctx context.Context, payload websiteapi.SnapshotForkWorkflowPayload) error {
	if c.engine == nil || c.projections == nil || c.locale == "" || c.credential == "" {
		return errors.New("content fork dependency is not initialized")
	}
	authCtx, err := c.engine.Authenticate(ctx, c.credential)
	if err != nil {
		return snapshotForkStageError("authorize content", err)
	}
	sourceSchema, err := c.projections.GetContentProjectionsForLocale(authCtx, payload.SourceSnapshotID, c.locale)
	if err != nil {
		return snapshotForkStageError("load source projection", err)
	}
	targetSchema, err := c.projections.getContentProjectionsForLocaleAllowState(authCtx, payload.TargetSnapshotID, c.locale, true)
	if err != nil {
		return snapshotForkStageError("load target projection", err)
	}
	workspace, err := c.engine.GetWorkspace(authCtx, sourceSchema)
	if err != nil {
		return snapshotForkStageError("load source workspace", err)
	}
	_, err = c.engine.CopySnapshotContent(authCtx, contentapi.CopySnapshotContentRequest{
		SourceSchema:                    sourceSchema,
		TargetSchema:                    targetSchema,
		SourceSnapshotID:                payload.SourceSnapshotID,
		TargetSnapshotID:                payload.TargetSnapshotID,
		ExpectedSourceRevision:          workspace.Revision,
		ExpectedSourceSelectionRevision: workspace.SelectionRevision,
		SelectionPolicy:                 payload.SelectionPolicy,
		IdempotencyKey:                  payload.ContentCopyIdempotencyKey,
	})
	return snapshotForkStageError("copy source workspace", err)
}
