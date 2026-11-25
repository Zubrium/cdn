package composition

import (
	"context"
	"encoding/json"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type adminWebsiteAdapter struct {
	dispatcher contracts.Dispatcher
}

func (adapter *adminWebsiteAdapter) subject(ctx context.Context) (contracts.AuthorizationSubject, error) {
	subject, ok := contracts.AdminSubjectFromContext(ctx)
	if !ok {
		return contracts.AuthorizationSubject{}, contracts.ErrAdminWebsiteUnauthorized
	}
	return subject, nil
}

func (adapter *adminWebsiteAdapter) message(ctx context.Context, value any) (contracts.AdminWebsiteMessageRequest, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteMessageRequest{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return contracts.AdminWebsiteMessageRequest{}, contracts.ErrAdminWebsiteInvalid
	}
	return contracts.AdminWebsiteMessageRequest{Subject: subject, Request: raw}, nil
}

func (adapter *adminWebsiteAdapter) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminWebsiteUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteAuthenticateOperation(), contracts.AdminAuthenticateRequest{Credential: credential})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminWebsiteAdapter) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminWebsiteUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteAuthenticateSessionOperation(), contracts.AdminAuthenticateSessionRequest{Token: token})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminWebsiteAdapter) BeginFeatureImport(ctx context.Context, request contracts.AdminWebsiteImportFeatureRequest) (contracts.AdminWebsiteFeatureImport, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsiteFeatureImport{}, err
	}
	message.IdempotencyKey = request.IdempotencyKey
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteBeginFeatureImportOperation(), message)
	if err != nil {
		return contracts.AdminWebsiteFeatureImport{}, err
	}
	return response.Job, nil
}

func (adapter *adminWebsiteAdapter) GetFeatureImport(ctx context.Context, id string) (contracts.AdminWebsiteFeatureImport, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteFeatureImport{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetFeatureImportOperation(), contracts.AdminWebsiteIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminWebsiteFeatureImport{}, err
	}
	return response.Job, nil
}

func (adapter *adminWebsiteAdapter) ListFeatureCatalog(ctx context.Context, cursor string, limit int) (contracts.AdminWebsiteFeatureVersionPage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteFeatureVersionPage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteListFeatureCatalogOperation(), contracts.AdminWebsiteCursorRequest{Subject: subject, Cursor: cursor, Limit: limit})
	if err != nil {
		return contracts.AdminWebsiteFeatureVersionPage{}, err
	}
	return response.Page, nil
}

func (adapter *adminWebsiteAdapter) ListFeatureVersions(ctx context.Context, featureID, cursor string, limit int) (contracts.AdminWebsiteFeatureVersionPage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteFeatureVersionPage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteListFeatureVersionsOperation(), contracts.AdminWebsiteVersionListRequest{Subject: subject, FeatureID: featureID, Cursor: cursor, Limit: limit})
	if err != nil {
		return contracts.AdminWebsiteFeatureVersionPage{}, err
	}
	return response.Page, nil
}

func (adapter *adminWebsiteAdapter) GetFeatureVersion(ctx context.Context, featureID, version string) (contracts.AdminWebsiteFeatureVersion, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteFeatureVersion{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetFeatureVersionOperation(), contracts.AdminWebsiteVersionRequest{Subject: subject, FeatureID: featureID, Version: version})
	if err != nil {
		return contracts.AdminWebsiteFeatureVersion{}, err
	}
	return response.Version, nil
}

func (adapter *adminWebsiteAdapter) ListPages(ctx context.Context, cursor string, limit int) (contracts.AdminWebsitePagePage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsitePagePage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteListPagesOperation(), contracts.AdminWebsiteCursorRequest{Subject: subject, Cursor: cursor, Limit: limit})
	if err != nil {
		return contracts.AdminWebsitePagePage{}, err
	}
	return response.Page, nil
}

func (adapter *adminWebsiteAdapter) GetPage(ctx context.Context, id string) (contracts.AdminWebsitePage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsitePage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetPageOperation(), contracts.AdminWebsiteIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminWebsitePage{}, err
	}
	return response.Page, nil
}

func (adapter *adminWebsiteAdapter) CreatePage(ctx context.Context, request contracts.AdminWebsiteCreatePageRequest) (contracts.AdminWebsitePage, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsitePage{}, err
	}
	message.IdempotencyKey = request.IdempotencyKey
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteCreatePageOperation(), message)
	if err != nil {
		return contracts.AdminWebsitePage{}, err
	}
	return response.Page, nil
}

func (adapter *adminWebsiteAdapter) ListPageTemplates(ctx context.Context, cursor string, limit int) (contracts.AdminWebsitePageTemplatePage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsitePageTemplatePage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteListPageTemplatesOperation(), contracts.AdminWebsiteCursorRequest{Subject: subject, Cursor: cursor, Limit: limit})
	if err != nil {
		return contracts.AdminWebsitePageTemplatePage{}, err
	}
	return response.Page, nil
}

func (adapter *adminWebsiteAdapter) GetPageTemplate(ctx context.Context, id string) (contracts.AdminWebsitePageTemplate, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsitePageTemplate{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetPageTemplateOperation(), contracts.AdminWebsiteIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminWebsitePageTemplate{}, err
	}
	return response.Template, nil
}

func (adapter *adminWebsiteAdapter) CreatePageTemplate(ctx context.Context, request contracts.AdminWebsiteCreatePageTemplateRequest) (contracts.AdminWebsitePageTemplate, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsitePageTemplate{}, err
	}
	message.IdempotencyKey = request.IdempotencyKey
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteCreatePageTemplateOperation(), message)
	if err != nil {
		return contracts.AdminWebsitePageTemplate{}, err
	}
	return response.Template, nil
}

func (adapter *adminWebsiteAdapter) UpdatePageTemplate(ctx context.Context, request contracts.AdminWebsiteUpdatePageTemplateRequest) (contracts.AdminWebsitePageTemplate, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsitePageTemplate{}, err
	}
	message.PathID = request.TemplateID
	message.ExpectedRevision = request.ExpectedRevision
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteUpdatePageTemplateOperation(), message)
	if err != nil {
		return contracts.AdminWebsitePageTemplate{}, err
	}
	return response.Template, nil
}

func (adapter *adminWebsiteAdapter) DeletePageTemplate(ctx context.Context, request contracts.AdminWebsiteDeletePageTemplateRequest) error {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return err
	}
	message.PathID = request.TemplateID
	message.ExpectedRevision = request.ExpectedRevision
	_, err = contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteDeletePageTemplateOperation(), message)
	return err
}

func (adapter *adminWebsiteAdapter) ApplyPageTemplate(ctx context.Context, request contracts.AdminWebsiteApplyPageTemplateRequest) (contracts.AdminWebsiteSnapshot, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsiteSnapshot{}, err
	}
	message.PageID = request.PageID
	message.IdempotencyKey = request.IdempotencyKey
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteApplyPageTemplateOperation(), message)
	if err != nil {
		return contracts.AdminWebsiteSnapshot{}, err
	}
	return response.Snapshot, nil
}

func (adapter *adminWebsiteAdapter) GetContentModel(ctx context.Context) (contracts.AdminWebsiteContentModel, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteContentModel{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetContentModelOperation(), contracts.AdminWebsiteSubjectRequest{Subject: subject})
	if err != nil {
		return contracts.AdminWebsiteContentModel{}, err
	}
	return response.Model, nil
}

func (adapter *adminWebsiteAdapter) SaveContentModel(ctx context.Context, request contracts.AdminWebsiteSaveContentModelRequest) (contracts.AdminWebsiteContentModel, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsiteContentModel{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteSaveContentModelOperation(), message)
	if err != nil {
		return contracts.AdminWebsiteContentModel{}, err
	}
	return response.Model, nil
}

func (adapter *adminWebsiteAdapter) GetPagePlacementProjection(ctx context.Context, pageID, snapshotID string) (contracts.AdminWebsitePagePlacementProjection, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsitePagePlacementProjection{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetPagePlacementProjectionOperation(), contracts.AdminWebsitePlacementRequest{Subject: subject, PageID: pageID, SnapshotID: snapshotID})
	if err != nil {
		return contracts.AdminWebsitePagePlacementProjection{}, err
	}
	return response.Projection, nil
}

func (adapter *adminWebsiteAdapter) GetSnapshot(ctx context.Context, id string) (contracts.AdminWebsiteSnapshot, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteSnapshot{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetSnapshotOperation(), contracts.AdminWebsiteIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminWebsiteSnapshot{}, err
	}
	return response.Snapshot, nil
}

func (adapter *adminWebsiteAdapter) ListSnapshots(ctx context.Context, pageID, cursor string, limit int) (contracts.AdminWebsiteSnapshotPage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteSnapshotPage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteListSnapshotsOperation(), contracts.AdminWebsiteSnapshotListRequest{Subject: subject, PageID: pageID, Cursor: cursor, Limit: limit})
	if err != nil {
		return contracts.AdminWebsiteSnapshotPage{}, err
	}
	return response.Page, nil
}

func (adapter *adminWebsiteAdapter) CreateSnapshot(ctx context.Context, request contracts.AdminWebsiteCreateSnapshotRequest) (contracts.AdminWebsiteSnapshot, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsiteSnapshot{}, err
	}
	message.IdempotencyKey = request.IdempotencyKey
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteCreateSnapshotOperation(), message)
	if err != nil {
		return contracts.AdminWebsiteSnapshot{}, err
	}
	return response.Snapshot, nil
}

func (adapter *adminWebsiteAdapter) DeleteSnapshot(ctx context.Context, request contracts.AdminWebsiteDeleteSnapshotRequest) error {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return err
	}
	message.PathID = request.SnapshotID
	message.ExpectedRevision = request.ExpectedRevision
	_, err = contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteDeleteSnapshotOperation(), message)
	return err
}

func (adapter *adminWebsiteAdapter) BuildSnapshot(ctx context.Context, request contracts.AdminWebsiteBuildSnapshotRequest) (contracts.AdminWebsiteBuildJob, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsiteBuildJob{}, err
	}
	message.IdempotencyKey = request.IdempotencyKey
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteBuildSnapshotOperation(), message)
	if err != nil {
		return contracts.AdminWebsiteBuildJob{}, err
	}
	return response.Job, nil
}

func (adapter *adminWebsiteAdapter) GetBuild(ctx context.Context, id string) (contracts.AdminWebsiteBuildJob, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteBuildJob{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetBuildOperation(), contracts.AdminWebsiteIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminWebsiteBuildJob{}, err
	}
	return response.Job, nil
}

func (adapter *adminWebsiteAdapter) CreateMediaDerivative(ctx context.Context, request contracts.AdminWebsiteCreateDerivativeRequest) (contracts.AdminWebsiteMediaJob, error) {
	message, err := adapter.message(ctx, request)
	if err != nil {
		return contracts.AdminWebsiteMediaJob{}, err
	}
	message.IdempotencyKey = request.IdempotencyKey
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteCreateMediaDerivativeOperation(), message)
	if err != nil {
		return contracts.AdminWebsiteMediaJob{}, err
	}
	return response.Job, nil
}

func (adapter *adminWebsiteAdapter) GetMediaJob(ctx context.Context, id string) (contracts.AdminWebsiteMediaJob, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminWebsiteMediaJob{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminWebsiteGetMediaJobOperation(), contracts.AdminWebsiteIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminWebsiteMediaJob{}, err
	}
	return response.Job, nil
}

var _ contracts.AdminWebsiteAPI = (*adminWebsiteAdapter)(nil)
