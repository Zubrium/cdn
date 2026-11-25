package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// Admin engine operations run on a dedicated DataBus. The dispatch identity is
// attached by composition from the compiled operation key; an HTTP request
// cannot name a module, engine, class, credential, or stream.

const AdminEngineCallerModuleID = "admin.api"

var (
	ErrAdminEngineUnavailable = errors.New("Admin engine is not configured")
)

type AdminContentEngine interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	GetLocaleConfig(context.Context) (contracts.AdminContentLocaleConfig, error)
	ReplaceLocaleConfig(context.Context, contracts.AdminContentReplaceLocaleConfigRequest) (contracts.AdminContentLocaleConfig, error)
	GetWorkspace(context.Context, contracts.AdminContentProjections) (contracts.AdminContentWorkspace, error)
	SaveWorkspaceDraft(context.Context, contracts.AdminContentProjections, contracts.AdminContentSaveWorkspaceDraftRequest) (contracts.AdminContentWorkspace, error)
	PublishWorkspace(context.Context, contracts.AdminContentProjections, contracts.AdminContentPublishWorkspaceRequest) (contracts.AdminContentWorkspace, error)
	ListHistory(context.Context, contracts.AdminContentProjections, string, int) (contracts.AdminContentHistoryPage, error)
	RestoreHistory(context.Context, contracts.AdminContentProjections, contracts.AdminContentRestoreHistoryRequest) (contracts.AdminContentWorkspace, error)
}

type AdminFormsEngine interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	ListSchemas(context.Context, string, int) (contracts.AdminFormsSchemaPage, error)
	GetSchema(context.Context, string) (contracts.AdminFormsSchema, error)
	SaveSchema(context.Context, contracts.AdminFormsSaveSchemaRequest) (contracts.AdminFormsSchema, error)
	ListSubmissions(context.Context, contracts.AdminFormsListSubmissionsRequest) (contracts.AdminFormsSubmissionPage, error)
	GetSubmission(context.Context, string) (contracts.AdminFormsSubmission, error)
	SetSubmissionStatus(context.Context, contracts.AdminFormsSetSubmissionStatusRequest) (contracts.AdminFormsSubmission, error)
	DeleteSubmission(context.Context, contracts.AdminFormsDeleteSubmissionRequest) error
}

type AdminWebsiteEngine interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	BeginFeatureImport(context.Context, contracts.AdminWebsiteImportFeatureRequest) (contracts.AdminWebsiteFeatureImport, error)
	GetFeatureImport(context.Context, string) (contracts.AdminWebsiteFeatureImport, error)
	ListFeatureCatalog(context.Context, string, int) (contracts.AdminWebsiteFeatureVersionPage, error)
	ListFeatureVersions(context.Context, string, string, int) (contracts.AdminWebsiteFeatureVersionPage, error)
	GetFeatureVersion(context.Context, string, string) (contracts.AdminWebsiteFeatureVersion, error)
	ListPages(context.Context, string, int) (contracts.AdminWebsitePagePage, error)
	GetPage(context.Context, string) (contracts.AdminWebsitePage, error)
	CreatePage(context.Context, contracts.AdminWebsiteCreatePageRequest) (contracts.AdminWebsitePage, error)
	ListPageTemplates(context.Context, string, int) (contracts.AdminWebsitePageTemplatePage, error)
	GetPageTemplate(context.Context, string) (contracts.AdminWebsitePageTemplate, error)
	CreatePageTemplate(context.Context, contracts.AdminWebsiteCreatePageTemplateRequest) (contracts.AdminWebsitePageTemplate, error)
	UpdatePageTemplate(context.Context, contracts.AdminWebsiteUpdatePageTemplateRequest) (contracts.AdminWebsitePageTemplate, error)
	DeletePageTemplate(context.Context, contracts.AdminWebsiteDeletePageTemplateRequest) error
	ApplyPageTemplate(context.Context, contracts.AdminWebsiteApplyPageTemplateRequest) (contracts.AdminWebsiteSnapshot, error)
	GetContentModel(context.Context) (contracts.AdminWebsiteContentModel, error)
	SaveContentModel(context.Context, contracts.AdminWebsiteSaveContentModelRequest) (contracts.AdminWebsiteContentModel, error)
	GetPagePlacementProjection(context.Context, string, string) (contracts.AdminWebsitePagePlacementProjection, error)
	GetSnapshot(context.Context, string) (contracts.AdminWebsiteSnapshot, error)
	ListSnapshots(context.Context, string, string, int) (contracts.AdminWebsiteSnapshotPage, error)
	CreateSnapshot(context.Context, contracts.AdminWebsiteCreateSnapshotRequest) (contracts.AdminWebsiteSnapshot, error)
	DeleteSnapshot(context.Context, contracts.AdminWebsiteDeleteSnapshotRequest) error
	BuildSnapshot(context.Context, contracts.AdminWebsiteBuildSnapshotRequest) (contracts.AdminWebsiteBuildJob, error)
	GetBuild(context.Context, string) (contracts.AdminWebsiteBuildJob, error)
	CreateMediaDerivative(context.Context, contracts.AdminWebsiteCreateDerivativeRequest) (contracts.AdminWebsiteMediaJob, error)
	GetMediaJob(context.Context, string) (contracts.AdminWebsiteMediaJob, error)
}

type AdminEngineProviders struct {
	Content func() AdminContentEngine
	Forms   func() AdminFormsEngine
	Website func() AdminWebsiteEngine
	Files   func() contracts.AdminFilesAPI
}

type adminEngineModuleDispatcher struct {
	dispatcher contracts.Dispatcher
	allowed    map[contracts.Key]struct{}
}

func NewAdminEngineDispatcher(dispatcher contracts.Dispatcher) contracts.Dispatcher {
	if dispatcher == nil {
		return nil
	}
	allowed := make(map[contracts.Key]struct{})
	for _, key := range AdminEngineOperationKeys() {
		allowed[key] = struct{}{}
	}
	return adminEngineModuleDispatcher{dispatcher: dispatcher, allowed: allowed}
}

func (dispatcher adminEngineModuleDispatcher) Dispatch(ctx context.Context, request contracts.Envelope) (contracts.Envelope, error) {
	key := request.Key()
	if _, allowed := dispatcher.allowed[key]; !allowed {
		return contracts.Envelope{}, ErrOperationNotAllowedForModule
	}
	if adminEngineTargetModule(key.ID) == "" {
		return contracts.Envelope{}, ErrInvalidModuleIdentity
	}
	return dispatcher.dispatcher.Dispatch(context.WithValue(ctx, moduleIdentityContextKey{}, AdminEngineCallerModuleID), request)
}

func adminEngineTargetModule(operationID string) string {
	switch {
	case strings.HasPrefix(operationID, "admin.content."):
		return "engine.content"
	case strings.HasPrefix(operationID, "admin.forms."):
		return "engine.forms"
	case strings.HasPrefix(operationID, "admin.website."):
		return "engine.website"
	case strings.HasPrefix(operationID, "admin.files."):
		return "engine.loader"
	default:
		return ""
	}
}

func AdminEngineOperationKeys() []contracts.Key {
	return append(append(append(append([]contracts.Key{},
		contracts.AdminContentOperationKeys()...),
		contracts.AdminFormsOperationKeys()...),
		contracts.AdminWebsiteOperationKeys()...),
		contracts.AdminFilesOperationKeys()...)
}

func NewAdminEngineBus(limits ConcurrencyLimits, providers AdminEngineProviders) (contracts.Dispatcher, error) {
	dispatcher, err := NewAllowlisted(limits, AdminEngineOperationKeys()...)
	if err != nil {
		return nil, err
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		return nil, err
	}
	if err := registerAdminContentEngine(dispatcher, providers.Content); err != nil {
		return nil, err
	}
	if err := registerAdminFormsEngine(dispatcher, providers.Forms); err != nil {
		return nil, err
	}
	if err := registerAdminWebsiteEngine(dispatcher, providers.Website); err != nil {
		return nil, err
	}
	if err := registerAdminFilesEngine(dispatcher, providers.Files); err != nil {
		return nil, err
	}
	if err := dispatcher.Seal(); err != nil {
		return nil, err
	}
	return NewAdminEngineDispatcher(dispatcher), nil
}

func adminEngineGuard(ctx context.Context) error {
	if ModuleIDFromContext(ctx) != AdminEngineCallerModuleID {
		return ErrInvalidModuleIdentity
	}
	return nil
}

func adminEngineSubjectContext(ctx context.Context, authenticate func(context.Context, string) (context.Context, error),
	authenticateSession func(context.Context, string) (context.Context, error), subject contracts.AuthorizationSubject) (context.Context, error) {
	if authenticate == nil || authenticateSession == nil {
		return nil, ErrAdminEngineUnavailable
	}
	if subject.Credential != "" && subject.SessionToken == "" {
		return authenticate(ctx, subject.Credential)
	}
	if subject.SessionToken != "" && subject.Credential == "" {
		return authenticateSession(ctx, subject.SessionToken)
	}
	return nil, contracts.ErrInvalidOperation
}

func registerAdminContentEngine(dispatcher *Dispatcher, resolve func() AdminContentEngine) error {
	engine := func() (AdminContentEngine, error) {
		if resolve == nil || resolve() == nil {
			return nil, ErrAdminEngineUnavailable
		}
		return resolve(), nil
	}
	auth := func(ctx context.Context, credential string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.Authenticate(ctx, credential)
	}
	authSession := func(ctx context.Context, token string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.AuthenticateSession(ctx, token)
	}
	if err := Register(dispatcher, contracts.AdminContentAuthenticateOperation(), func(ctx context.Context, request contracts.AdminAuthenticateRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := auth(ctx, request.Credential); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{Credential: request.Credential}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminContentAuthenticateSessionOperation(), func(ctx context.Context, request contracts.AdminAuthenticateSessionRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := authSession(ctx, request.Token); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{SessionToken: request.Token}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminContentGetLocaleConfigOperation(), func(ctx context.Context, request contracts.AdminContentSubjectRequest) (contracts.AdminContentLocaleConfigResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminContentLocaleConfigResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminContentLocaleConfigResponse{}, err
		}
		if _, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject); err != nil {
			return contracts.AdminContentLocaleConfigResponse{}, err
		}
		config, err := service.GetLocaleConfig(ctx)
		return contracts.AdminContentLocaleConfigResponse{Config: config}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminContentReplaceLocaleConfigOperation(), func(ctx context.Context, request contracts.AdminContentReplaceLocaleConfigRequestMessage) (contracts.AdminContentLocaleConfigResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminContentLocaleConfigResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminContentLocaleConfigResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminContentLocaleConfigResponse{}, err
		}
		input := request.Request
		input.IdempotencyKey = request.IdempotencyKey
		config, err := service.ReplaceLocaleConfig(authCtx, input)
		return contracts.AdminContentLocaleConfigResponse{Config: config}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminContentGetWorkspaceOperation(), func(ctx context.Context, request contracts.AdminContentWorkspaceRequest) (contracts.AdminContentWorkspaceResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		workspace, err := service.GetWorkspace(authCtx, request.Projections)
		return contracts.AdminContentWorkspaceResponse{Workspace: workspace}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminContentSaveWorkspaceDraftOperation(), func(ctx context.Context, request contracts.AdminContentSaveWorkspaceDraftRequestMessage) (contracts.AdminContentWorkspaceResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		input := request.Request
		input.SnapshotID = request.SnapshotID
		input.IdempotencyKey = request.IdempotencyKey
		workspace, err := service.SaveWorkspaceDraft(authCtx, request.Projections, input)
		return contracts.AdminContentWorkspaceResponse{Workspace: workspace}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminContentPublishWorkspaceOperation(), func(ctx context.Context, request contracts.AdminContentPublishWorkspaceRequestMessage) (contracts.AdminContentWorkspaceResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		input := request.Request
		input.SnapshotID = request.SnapshotID
		input.IdempotencyKey = request.IdempotencyKey
		workspace, err := service.PublishWorkspace(authCtx, request.Projections, input)
		return contracts.AdminContentWorkspaceResponse{Workspace: workspace}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminContentListHistoryOperation(), func(ctx context.Context, request contracts.AdminContentListHistoryRequest) (contracts.AdminContentHistoryResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminContentHistoryResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminContentHistoryResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminContentHistoryResponse{}, err
		}
		page, err := service.ListHistory(authCtx, request.Projections, request.Cursor, request.Limit)
		return contracts.AdminContentHistoryResponse{Page: page}, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.AdminContentRestoreHistoryOperation(), func(ctx context.Context, request contracts.AdminContentRestoreHistoryRequestMessage) (contracts.AdminContentWorkspaceResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminContentWorkspaceResponse{}, err
		}
		input := request.Request
		input.SnapshotID = request.SnapshotID
		input.HistoryID = request.HistoryID
		input.IdempotencyKey = request.IdempotencyKey
		workspace, err := service.RestoreHistory(authCtx, request.Projections, input)
		return contracts.AdminContentWorkspaceResponse{Workspace: workspace}, err
	})
}

func registerAdminFormsEngine(dispatcher *Dispatcher, resolve func() AdminFormsEngine) error {
	engine := func() (AdminFormsEngine, error) {
		if resolve == nil || resolve() == nil {
			return nil, ErrAdminEngineUnavailable
		}
		return resolve(), nil
	}
	auth := func(ctx context.Context, credential string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.Authenticate(ctx, credential)
	}
	authSession := func(ctx context.Context, token string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.AuthenticateSession(ctx, token)
	}
	if err := Register(dispatcher, contracts.AdminFormsAuthenticateOperation(), func(ctx context.Context, request contracts.AdminAuthenticateRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := auth(ctx, request.Credential); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{Credential: request.Credential}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFormsAuthenticateSessionOperation(), func(ctx context.Context, request contracts.AdminAuthenticateSessionRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := authSession(ctx, request.Token); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{SessionToken: request.Token}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFormsListSchemasOperation(), func(ctx context.Context, request contracts.AdminFormsSchemaListRequest) (contracts.AdminFormsSchemaPageResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminFormsSchemaPageResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminFormsSchemaPageResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminFormsSchemaPageResponse{}, err
		}
		page, err := service.ListSchemas(authCtx, request.Cursor, request.Limit)
		return contracts.AdminFormsSchemaPageResponse{Page: page}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFormsGetSchemaOperation(), func(ctx context.Context, request contracts.AdminFormsSchemaIDRequest) (contracts.AdminFormsSchemaResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminFormsSchemaResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminFormsSchemaResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminFormsSchemaResponse{}, err
		}
		schema, err := service.GetSchema(authCtx, request.ID)
		return contracts.AdminFormsSchemaResponse{Schema: schema}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFormsSaveSchemaOperation(), func(ctx context.Context, request contracts.AdminFormsSaveSchemaRequestMessage) (contracts.AdminFormsSchemaResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminFormsSchemaResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminFormsSchemaResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminFormsSchemaResponse{}, err
		}
		schema, err := service.SaveSchema(authCtx, request.Request)
		return contracts.AdminFormsSchemaResponse{Schema: schema}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFormsListSubmissionsOperation(), func(ctx context.Context, request contracts.AdminFormsListSubmissionsRequestMessage) (contracts.AdminFormsSubmissionPageResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminFormsSubmissionPageResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminFormsSubmissionPageResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminFormsSubmissionPageResponse{}, err
		}
		page, err := service.ListSubmissions(authCtx, request.Request)
		return contracts.AdminFormsSubmissionPageResponse{Page: page}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFormsGetSubmissionOperation(), func(ctx context.Context, request contracts.AdminFormsSchemaIDRequest) (contracts.AdminFormsSubmissionResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminFormsSubmissionResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminFormsSubmissionResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminFormsSubmissionResponse{}, err
		}
		submission, err := service.GetSubmission(authCtx, request.ID)
		return contracts.AdminFormsSubmissionResponse{Submission: submission}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFormsSetSubmissionStatusOperation(), func(ctx context.Context, request contracts.AdminFormsSetSubmissionStatusRequestMessage) (contracts.AdminFormsSubmissionResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminFormsSubmissionResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminFormsSubmissionResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminFormsSubmissionResponse{}, err
		}
		submission, err := service.SetSubmissionStatus(authCtx, request.Request)
		return contracts.AdminFormsSubmissionResponse{Submission: submission}, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.AdminFormsDeleteSubmissionOperation(), func(ctx context.Context, request contracts.AdminFormsDeleteSubmissionRequestMessage) (contracts.AdminEmptyResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminEmptyResponse{}, err
		}
		service, err := engine()
		if err != nil {
			return contracts.AdminEmptyResponse{}, err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, request.Subject)
		if err != nil {
			return contracts.AdminEmptyResponse{}, err
		}
		return contracts.AdminEmptyResponse{}, service.DeleteSubmission(authCtx, request.Request)
	})
}

func registerAdminWebsiteEngine(dispatcher *Dispatcher, resolve func() AdminWebsiteEngine) error {
	engine := func() (AdminWebsiteEngine, error) {
		if resolve == nil || resolve() == nil {
			return nil, ErrAdminEngineUnavailable
		}
		return resolve(), nil
	}
	auth := func(ctx context.Context, credential string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.Authenticate(ctx, credential)
	}
	authSession := func(ctx context.Context, token string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.AuthenticateSession(ctx, token)
	}
	call := func(ctx context.Context, subject contracts.AuthorizationSubject,
		invoke func(context.Context, AdminWebsiteEngine) error) error {
		if err := adminEngineGuard(ctx); err != nil {
			return err
		}
		service, err := engine()
		if err != nil {
			return err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, subject)
		if err != nil {
			return err
		}
		return invoke(authCtx, service)
	}
	if err := Register(dispatcher, contracts.AdminWebsiteAuthenticateOperation(), func(ctx context.Context, request contracts.AdminAuthenticateRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := auth(ctx, request.Credential); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{Credential: request.Credential}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteAuthenticateSessionOperation(), func(ctx context.Context, request contracts.AdminAuthenticateSessionRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := authSession(ctx, request.Token); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{SessionToken: request.Token}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteBeginFeatureImportOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteFeatureImportResponse, err error) {
		var input contracts.AdminWebsiteImportFeatureRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			job, callErr := service.BeginFeatureImport(authCtx, input)
			response.Job = job
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetFeatureImportOperation(), func(ctx context.Context, request contracts.AdminWebsiteIDRequest) (response contracts.AdminWebsiteFeatureImportResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			job, callErr := service.GetFeatureImport(authCtx, request.ID)
			response.Job = job
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteListFeatureCatalogOperation(), func(ctx context.Context, request contracts.AdminWebsiteCursorRequest) (response contracts.AdminWebsiteFeatureVersionPageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			page, callErr := service.ListFeatureCatalog(authCtx, request.Cursor, request.Limit)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteListFeatureVersionsOperation(), func(ctx context.Context, request contracts.AdminWebsiteVersionListRequest) (response contracts.AdminWebsiteFeatureVersionPageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			page, callErr := service.ListFeatureVersions(authCtx, request.FeatureID, request.Cursor, request.Limit)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetFeatureVersionOperation(), func(ctx context.Context, request contracts.AdminWebsiteVersionRequest) (response contracts.AdminWebsiteFeatureVersionResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			version, callErr := service.GetFeatureVersion(authCtx, request.FeatureID, request.Version)
			response.Version = version
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteListPagesOperation(), func(ctx context.Context, request contracts.AdminWebsiteCursorRequest) (response contracts.AdminWebsitePagePageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			page, callErr := service.ListPages(authCtx, request.Cursor, request.Limit)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetPageOperation(), func(ctx context.Context, request contracts.AdminWebsiteIDRequest) (response contracts.AdminWebsitePageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			page, callErr := service.GetPage(authCtx, request.ID)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteCreatePageOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsitePageResponse, err error) {
		var input contracts.AdminWebsiteCreatePageRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			page, callErr := service.CreatePage(authCtx, input)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteListPageTemplatesOperation(), func(ctx context.Context, request contracts.AdminWebsiteCursorRequest) (response contracts.AdminWebsiteTemplatePageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			page, callErr := service.ListPageTemplates(authCtx, request.Cursor, request.Limit)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetPageTemplateOperation(), func(ctx context.Context, request contracts.AdminWebsiteIDRequest) (response contracts.AdminWebsiteTemplateResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			template, callErr := service.GetPageTemplate(authCtx, request.ID)
			response.Template = template
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteCreatePageTemplateOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteTemplateResponse, err error) {
		var input contracts.AdminWebsiteCreatePageTemplateRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			template, callErr := service.CreatePageTemplate(authCtx, input)
			response.Template = template
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteUpdatePageTemplateOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteTemplateResponse, err error) {
		var input contracts.AdminWebsiteUpdatePageTemplateRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.TemplateID = request.PathID
		input.ExpectedRevision = request.ExpectedRevision
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			template, callErr := service.UpdatePageTemplate(authCtx, input)
			response.Template = template
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteDeletePageTemplateOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (contracts.AdminEmptyResponse, error) {
		var input contracts.AdminWebsiteDeletePageTemplateRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return contracts.AdminEmptyResponse{}, decodeErr
		}
		input.TemplateID = request.PathID
		input.ExpectedRevision = request.ExpectedRevision
		return contracts.AdminEmptyResponse{}, call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			return service.DeletePageTemplate(authCtx, input)
		})
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteApplyPageTemplateOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteSnapshotResponse, err error) {
		var input contracts.AdminWebsiteApplyPageTemplateRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.PageID = request.PageID
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			snapshot, callErr := service.ApplyPageTemplate(authCtx, input)
			response.Snapshot = snapshot
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetContentModelOperation(), func(ctx context.Context, request contracts.AdminWebsiteSubjectRequest) (response contracts.AdminWebsiteContentModelResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			model, callErr := service.GetContentModel(authCtx)
			response.Model = model
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteSaveContentModelOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteContentModelResponse, err error) {
		var input contracts.AdminWebsiteSaveContentModelRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			model, callErr := service.SaveContentModel(authCtx, input)
			response.Model = model
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetPagePlacementProjectionOperation(), func(ctx context.Context, request contracts.AdminWebsitePlacementRequest) (response contracts.AdminWebsitePlacementResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			projection, callErr := service.GetPagePlacementProjection(authCtx, request.PageID, request.SnapshotID)
			response.Projection = projection
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetSnapshotOperation(), func(ctx context.Context, request contracts.AdminWebsiteIDRequest) (response contracts.AdminWebsiteSnapshotResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			snapshot, callErr := service.GetSnapshot(authCtx, request.ID)
			response.Snapshot = snapshot
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteListSnapshotsOperation(), func(ctx context.Context, request contracts.AdminWebsiteSnapshotListRequest) (response contracts.AdminWebsiteSnapshotPageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			page, callErr := service.ListSnapshots(authCtx, request.PageID, request.Cursor, request.Limit)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteCreateSnapshotOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteSnapshotResponse, err error) {
		var input contracts.AdminWebsiteCreateSnapshotRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			snapshot, callErr := service.CreateSnapshot(authCtx, input)
			response.Snapshot = snapshot
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteDeleteSnapshotOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (contracts.AdminEmptyResponse, error) {
		var input contracts.AdminWebsiteDeleteSnapshotRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return contracts.AdminEmptyResponse{}, decodeErr
		}
		input.SnapshotID = request.PathID
		input.ExpectedRevision = request.ExpectedRevision
		return contracts.AdminEmptyResponse{}, call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			return service.DeleteSnapshot(authCtx, input)
		})
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteBuildSnapshotOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteBuildResponse, err error) {
		var input contracts.AdminWebsiteBuildSnapshotRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			job, callErr := service.BuildSnapshot(authCtx, input)
			response.Job = job
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteGetBuildOperation(), func(ctx context.Context, request contracts.AdminWebsiteIDRequest) (response contracts.AdminWebsiteBuildResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			job, callErr := service.GetBuild(authCtx, request.ID)
			response.Job = job
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminWebsiteCreateMediaDerivativeOperation(), func(ctx context.Context, request contracts.AdminWebsiteMessageRequest) (response contracts.AdminWebsiteMediaResponse, err error) {
		var input contracts.AdminWebsiteCreateDerivativeRequest
		if decodeErr := decodeAdminEngineJSON(request.Request, &input); decodeErr != nil {
			return response, decodeErr
		}
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			job, callErr := service.CreateMediaDerivative(authCtx, input)
			response.Job = job
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.AdminWebsiteGetMediaJobOperation(), func(ctx context.Context, request contracts.AdminWebsiteIDRequest) (response contracts.AdminWebsiteMediaResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service AdminWebsiteEngine) error {
			job, callErr := service.GetMediaJob(authCtx, request.ID)
			response.Job = job
			return callErr
		})
		return response, err
	})
}

func registerAdminFilesEngine(dispatcher *Dispatcher, resolve func() contracts.AdminFilesAPI) error {
	engine := func() (contracts.AdminFilesAPI, error) {
		if resolve == nil || resolve() == nil {
			return nil, ErrAdminEngineUnavailable
		}
		return resolve(), nil
	}
	auth := func(ctx context.Context, credential string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.Authenticate(ctx, credential)
	}
	authSession := func(ctx context.Context, token string) (context.Context, error) {
		service, err := engine()
		if err != nil {
			return nil, err
		}
		return service.AuthenticateSession(ctx, token)
	}
	call := func(ctx context.Context, subject contracts.AuthorizationSubject, invoke func(context.Context, contracts.AdminFilesAPI) error) error {
		if err := adminEngineGuard(ctx); err != nil {
			return err
		}
		service, err := engine()
		if err != nil {
			return err
		}
		authCtx, err := adminEngineSubjectContext(ctx, auth, authSession, subject)
		if err != nil {
			return err
		}
		return invoke(authCtx, service)
	}
	if err := Register(dispatcher, contracts.AdminFilesAuthenticateOperation(), func(ctx context.Context, request contracts.AdminAuthenticateRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := auth(ctx, request.Credential); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{Credential: request.Credential}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesAuthenticateSessionOperation(), func(ctx context.Context, request contracts.AdminAuthenticateSessionRequest) (contracts.AdminAuthenticateResponse, error) {
		if err := adminEngineGuard(ctx); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		if _, err := authSession(ctx, request.Token); err != nil {
			return contracts.AdminAuthenticateResponse{}, err
		}
		return contracts.AdminAuthenticateResponse{Subject: contracts.AuthorizationSubject{SessionToken: request.Token}}, nil
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesListFileSetsOperation(), func(ctx context.Context, request contracts.AdminFilesSetListRequest) (response contracts.AdminFilesSetPageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			page, callErr := service.ListFileSets(authCtx, request.Query)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesCreateFileSetOperation(), func(ctx context.Context, request contracts.AdminFilesCreateSetRequest) (response contracts.AdminFilesSetResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			set, callErr := service.CreateFileSet(authCtx, request.Input)
			response.Set = set
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesGetFileSetOperation(), func(ctx context.Context, request contracts.AdminFilesIDRequest) (response contracts.AdminFilesSetResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			set, callErr := service.GetFileSet(authCtx, request.ID)
			response.Set = set
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesUpdateFileSetOperation(), func(ctx context.Context, request contracts.AdminFilesSetUpdateRequest) (response contracts.AdminFilesSetResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			set, callErr := service.UpdateFileSet(authCtx, request.ID, request.ExpectedRevision, request.Patch)
			response.Set = set
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesDeleteFileSetOperation(), func(ctx context.Context, request contracts.AdminFilesRevisionRequest) (contracts.AdminEmptyResponse, error) {
		return contracts.AdminEmptyResponse{}, call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			return service.DeleteFileSet(authCtx, request.ID, request.ExpectedRevision)
		})
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesListSetFilesOperation(), func(ctx context.Context, request contracts.AdminFilesListRequest) (response contracts.AdminFilesFilePageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			page, callErr := service.ListSetFiles(authCtx, request.SetID, request.Query)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesAddToSetOperation(), func(ctx context.Context, request contracts.AdminFilesNamedRevisionRequest) (response contracts.AdminFilesSetResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			set, callErr := service.AddFileToSet(authCtx, request.ID, request.Name, request.ExpectedRevision)
			response.Set = set
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesRemoveFromSetOperation(), func(ctx context.Context, request contracts.AdminFilesNamedRevisionRequest) (response contracts.AdminFilesSetResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			set, callErr := service.RemoveFileFromSet(authCtx, request.ID, request.Name, request.ExpectedRevision)
			response.Set = set
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesBeginUploadOperation(), func(ctx context.Context, request contracts.AdminFilesBeginUploadRequest) (response contracts.AdminFilesUploadSessionResponse, err error) {
		input := request.Input
		input.IdempotencyKey = request.IdempotencyKey
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			session, callErr := service.BeginUpload(authCtx, input)
			response.Session = session
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesUploadStatusOperation(), func(ctx context.Context, request contracts.AdminFilesIDRequest) (response contracts.AdminFilesUploadSessionResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			session, callErr := service.UploadStatus(authCtx, request.ID)
			response.Session = session
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesPutUploadPartOperation(), func(ctx context.Context, request contracts.AdminFilesUploadPartRequest) (response contracts.AdminFilesUploadSessionResponse, err error) {
		capability, ok := contracts.StreamCapabilityFromContext(ctx)
		if !ok || capability == nil {
			return response, contracts.ErrStreamUnavailable
		}
		body, err := capability.OpenRead(ctx, request.Body)
		if err != nil {
			return response, mapAdminEngineStreamError(err)
		}
		defer body.Close()
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			session, callErr := service.PutUploadPart(authCtx, request.ID, request.Number, request.Size, request.SHA256, body)
			response.Session = session
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesCommitUploadOperation(), func(ctx context.Context, request contracts.AdminFilesIDRequest) (response contracts.AdminFilesFileResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			file, callErr := service.CommitUpload(authCtx, request.ID)
			response.File = file
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesAbortUploadOperation(), func(ctx context.Context, request contracts.AdminFilesIDRequest) (contracts.AdminEmptyResponse, error) {
		return contracts.AdminEmptyResponse{}, call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			return service.AbortUpload(authCtx, request.ID)
		})
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesListFilesOperation(), func(ctx context.Context, request contracts.AdminFilesListRequest) (response contracts.AdminFilesFilePageResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			page, callErr := service.ListFiles(authCtx, request.Query)
			response.Page = page
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesStatFileOperation(), func(ctx context.Context, request contracts.AdminFilesIDRequest) (response contracts.AdminFilesFileResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			file, callErr := service.StatFile(authCtx, request.ID)
			response.File = file
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesReadOperation(), func(ctx context.Context, request contracts.AdminFilesReadRequest) (response contracts.AdminFilesReadResponse, err error) {
		capability, ok := contracts.StreamCapabilityFromContext(ctx)
		if !ok || capability == nil {
			return response, contracts.ErrStreamUnavailable
		}
		writer, err := capability.OpenWrite(ctx, request.Body)
		if err != nil {
			return response, mapAdminEngineStreamError(err)
		}
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			var reader io.ReadCloser
			var file contracts.AdminFile
			var callErr error
			if request.Range {
				reader, file, callErr = service.ReadFileRange(authCtx, request.ID, request.Start, request.End)
			} else {
				reader, file, callErr = service.ReadFile(authCtx, request.ID)
			}
			if callErr != nil {
				_ = writer.Close()
				return callErr
			}
			_, copyErr := io.Copy(writer, reader)
			closeErr := reader.Close()
			writeErr := writer.Close()
			response.File = file
			return errors.Join(copyErr, closeErr, writeErr)
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesUpdateFileOperation(), func(ctx context.Context, request contracts.AdminFilesFileUpdateRequest) (response contracts.AdminFilesFileResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			file, callErr := service.UpdateFile(authCtx, request.ID, request.ExpectedRevision, request.Patch)
			response.File = file
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesDeleteFileOperation(), func(ctx context.Context, request contracts.AdminFilesRevisionRequest) (contracts.AdminEmptyResponse, error) {
		return contracts.AdminEmptyResponse{}, call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			return service.DeleteFile(authCtx, request.ID, request.ExpectedRevision)
		})
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminFilesAddToGroupOperation(), func(ctx context.Context, request contracts.AdminFilesNamedRevisionRequest) (response contracts.AdminFilesFileResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			file, callErr := service.AddFileToGroup(authCtx, request.ID, request.Name, request.ExpectedRevision)
			response.File = file
			return callErr
		})
		return response, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.AdminFilesRemoveFromGroupOperation(), func(ctx context.Context, request contracts.AdminFilesNamedRevisionRequest) (response contracts.AdminFilesFileResponse, err error) {
		err = call(ctx, request.Subject, func(authCtx context.Context, service contracts.AdminFilesAPI) error {
			file, callErr := service.RemoveFileFromGroup(authCtx, request.ID, request.Name, request.ExpectedRevision)
			response.File = file
			return callErr
		})
		return response, err
	})
}

func decodeAdminEngineJSON(raw []byte, target any) error {
	if len(raw) == 0 {
		return contracts.ErrInvalidOperation
	}
	return json.Unmarshal(raw, target)
}

func mapAdminEngineStreamError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", contracts.ErrStreamUnavailable, err)
}
