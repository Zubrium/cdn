package composition

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// adminContentAdapter is the only Admin Content engine dependency. Browser
// payloads never select an engine, module, class, or credential; the
// composition-authenticated subject travels outside the JSON envelope.
type adminContentAdapter struct {
	dispatcher contracts.Dispatcher
	schemas    interface {
		GetContentProjections(context.Context, string) (contracts.AdminContentProjections, error)
	}
}

func (adapter *adminContentAdapter) subject(ctx context.Context) (contracts.AuthorizationSubject, error) {
	subject, ok := contracts.AdminSubjectFromContext(ctx)
	if !ok {
		return contracts.AuthorizationSubject{}, contracts.ErrAdminContentUnauthorized
	}
	return subject, nil
}

func (adapter *adminContentAdapter) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentAuthenticateOperation(), contracts.AdminAuthenticateRequest{Credential: credential})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminContentAdapter) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentAuthenticateSessionOperation(), contracts.AdminAuthenticateSessionRequest{Token: token})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminContentAdapter) GetLocaleConfig(ctx context.Context) (contracts.AdminContentLocaleConfig, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminContentLocaleConfig{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminContentLocaleConfig{}, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentGetLocaleConfigOperation(), contracts.AdminContentSubjectRequest{Subject: subject})
	if err != nil {
		return contracts.AdminContentLocaleConfig{}, err
	}
	return response.Config, nil
}

func (adapter *adminContentAdapter) ReplaceLocaleConfig(ctx context.Context, request contracts.AdminContentReplaceLocaleConfigRequest) (contracts.AdminContentLocaleConfig, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminContentLocaleConfig{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminContentLocaleConfig{}, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentReplaceLocaleConfigOperation(), contracts.AdminContentReplaceLocaleConfigRequestMessage{Subject: subject, Request: request, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return contracts.AdminContentLocaleConfig{}, err
	}
	return response.Config, nil
}

func (adapter *adminContentAdapter) GetWorkspace(ctx context.Context, projections contracts.AdminContentProjections) (contracts.AdminContentWorkspace, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminContentWorkspace{}, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentGetWorkspaceOperation(), contracts.AdminContentWorkspaceRequest{Subject: subject, Projections: projections})
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	return response.Workspace, nil
}

func (adapter *adminContentAdapter) SaveWorkspaceDraft(ctx context.Context, projections contracts.AdminContentProjections, request contracts.AdminContentSaveWorkspaceDraftRequest) (contracts.AdminContentWorkspace, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminContentWorkspace{}, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentSaveWorkspaceDraftOperation(), contracts.AdminContentSaveWorkspaceDraftRequestMessage{Subject: subject, Projections: projections, Request: request, SnapshotID: request.SnapshotID, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	return response.Workspace, nil
}

func (adapter *adminContentAdapter) PublishWorkspace(ctx context.Context, projections contracts.AdminContentProjections, request contracts.AdminContentPublishWorkspaceRequest) (contracts.AdminContentWorkspace, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminContentWorkspace{}, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentPublishWorkspaceOperation(), contracts.AdminContentPublishWorkspaceRequestMessage{Subject: subject, Projections: projections, Request: request, SnapshotID: request.SnapshotID, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	return response.Workspace, nil
}

func (adapter *adminContentAdapter) ListHistory(ctx context.Context, projections contracts.AdminContentProjections, cursor string, limit int) (contracts.AdminContentHistoryPage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminContentHistoryPage{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminContentHistoryPage{}, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentListHistoryOperation(), contracts.AdminContentListHistoryRequest{Subject: subject, Projections: projections, Cursor: cursor, Limit: limit})
	if err != nil {
		return contracts.AdminContentHistoryPage{}, err
	}
	return response.Page, nil
}

func (adapter *adminContentAdapter) RestoreHistory(ctx context.Context, projections contracts.AdminContentProjections, request contracts.AdminContentRestoreHistoryRequest) (contracts.AdminContentWorkspace, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminContentWorkspace{}, contracts.ErrAdminContentUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminContentRestoreHistoryOperation(), contracts.AdminContentRestoreHistoryRequestMessage{Subject: subject, Projections: projections, Request: request, SnapshotID: request.SnapshotID, HistoryID: request.HistoryID, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return contracts.AdminContentWorkspace{}, err
	}
	return response.Workspace, nil
}

func (adapter *adminContentAdapter) GetContentProjections(ctx context.Context, pageSnapshotID string) (contracts.AdminContentProjections, error) {
	if adapter == nil || adapter.schemas == nil {
		return contracts.AdminContentProjections{}, contracts.ErrAdminContentUnavailable
	}
	return adapter.schemas.GetContentProjections(ctx, pageSnapshotID)
}

var _ contracts.AdminContentAPI = (*adminContentAdapter)(nil)
