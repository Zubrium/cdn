//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	contentengine "github.com/Zubrium/cdn/engines/content"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
)

type snapshotForkWorkflowStarter struct{ core *coreComponent }

func (s snapshotForkWorkflowStarter) Start(ctx context.Context, request platform.WorkflowStartRequest) (platform.WorkflowReceipt, error) {
	if s.core == nil || s.core.runtime == nil {
		return platform.WorkflowReceipt{}, platform.ErrCoordinatorNotReady
	}
	client, err := s.core.runtime.NewWorkflowClient(websiteworkflow.ComponentID)
	if err != nil {
		return platform.WorkflowReceipt{}, err
	}
	return client.Start(ctx, request)
}

type snapshotForkContentCopierHandle struct{ component *coreComponent }

func (h snapshotForkContentCopierHandle) CopySnapshotContent(ctx context.Context, payload websiteapi.SnapshotForkWorkflowPayload) error {
	if h.component == nil {
		return errors.New("content fork dependency is not initialized")
	}
	content := h.component.contentHandle.Load()
	if content == nil || content.engine == nil || content.projections == nil {
		return errors.New("content fork dependency is not initialized")
	}
	credential := content.config.ContentAPI.ServiceCredential
	if credential == "" {
		return errors.New("content service credential is not configured")
	}
	authCtx, err := content.engine.Authenticate(ctx, credential)
	if err != nil {
		return err
	}
	return snapshotForkContentCopier{engine: content.engine, projections: content.projections}.CopySnapshotContent(authCtx, payload)
}

type snapshotForkWebsiteFinalizerHandle struct{ component *coreComponent }

func (h snapshotForkWebsiteFinalizerHandle) MarkSnapshotReady(ctx context.Context, snapshotID string, expectedRevision int64) (websiteapi.Snapshot, error) {
	if h.component == nil {
		return websiteapi.Snapshot{}, errors.New("website fork dependency is not initialized")
	}
	website := h.component.websiteHandle.Load()
	if website == nil || website.service == nil {
		return websiteapi.Snapshot{}, errors.New("website fork dependency is not initialized")
	}
	return website.service.MarkSnapshotReady(ctx, snapshotID, expectedRevision)
}

type snapshotForkContentCopier struct {
	engine      *contentengine.Engine
	projections *contentProjectionProvider
}

func (c snapshotForkContentCopier) CopySnapshotContent(ctx context.Context, payload websiteapi.SnapshotForkWorkflowPayload) error {
	if c.engine == nil || c.projections == nil {
		return errors.New("content fork dependency is not initialized")
	}
	sourceSchema, err := c.projections.GetContentProjectionsForLocale(ctx, payload.SourceSnapshotID, c.projections.locale)
	if err != nil {
		return err
	}
	targetSchema, err := c.projections.getContentProjectionsForLocaleAllowState(ctx, payload.TargetSnapshotID, c.projections.locale, true)
	if err != nil {
		return err
	}
	workspace, err := c.engine.GetWorkspace(ctx, sourceSchema)
	if err != nil {
		return err
	}
	if workspace.Revision != payload.ExpectedSourceContentRevision {
		return contentapi.ErrConflict
	}
	_, err = c.engine.CopySnapshotContent(ctx, contentapi.CopySnapshotContentRequest{
		SourceSchema:                    sourceSchema,
		TargetSchema:                    targetSchema,
		SourceSnapshotID:                payload.SourceSnapshotID,
		TargetSnapshotID:                payload.TargetSnapshotID,
		ExpectedSourceRevision:          workspace.Revision,
		ExpectedSourceSelectionRevision: workspace.SelectionRevision,
		SelectionPolicy:                 payload.SelectionPolicy,
		IdempotencyKey:                  payload.ContentCopyIdempotencyKey,
	})
	return err
}
