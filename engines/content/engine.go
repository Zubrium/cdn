package content

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	contentapp "github.com/Zubrium/cdn/engines/content/application"
	"github.com/Zubrium/cdn/engines/content/domain"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/engines/content/repository"
)

const accessGroup = "content"

type DataStore interface {
	Get(context.Context, recorddata.RecordKey) (recorddata.Record, error)
	Put(context.Context, recorddata.RecordKey, recorddata.RecordValue, int64) (recorddata.Record, error)
	Batch(context.Context, []recorddata.RecordMutation) error
	List(context.Context, string, string, string, string, []recorddata.RecordSelector, int) ([]recorddata.RecordKey, []recorddata.Record, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}

type FileOperations interface {
	StatFile(context.Context, recorddata.AuthorizationSubject, string) (recorddata.File, error)
}

type Dependencies struct {
	Identity      Identity
	Sessions      Sessions
	Authorization Authorization
	Files         FileOperations
	Data          DataStore
	Projections   contentapi.ProjectionsProvider
}

type Engine struct {
	contentapi.Descriptor
	identity      Identity
	sessions      Sessions
	authorization Authorization
	projections   contentapi.ProjectionsProvider
	service       *contentapp.Service
	repository    *repository.Repository
}

type authenticatedContextKey struct{}

type authenticatedState struct {
	owner     *Engine
	session   recorddata.AuthorizationSubject
	principal recorddata.Principal
}

func New(dependencies Dependencies) (*Engine, error) {
	if nilCapability(dependencies.Identity) || nilCapability(dependencies.Sessions) || nilCapability(dependencies.Authorization) ||
		nilCapability(dependencies.Files) || nilCapability(dependencies.Data) || nilCapability(dependencies.Projections) {
		return nil, contentapi.ErrUnavailable
	}
	repo, err := repository.New(dependencies.Data)
	if err != nil {
		return nil, mapError(err)
	}
	service, err := contentapp.New(repo, dependencies.Files)
	if err != nil {
		return nil, mapError(err)
	}
	return &Engine{
		identity: dependencies.Identity, sessions: dependencies.Sessions, authorization: dependencies.Authorization,
		projections: dependencies.Projections, service: service, repository: repo,
	}, nil
}

func (engine *Engine) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if engine == nil || ctx == nil || credential == "" || nilCapability(engine.identity) {
		return nil, contentapi.ErrUnauthorized
	}
	session, principal, err := engine.identity.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapError(err)
	}
	return engine.authenticatedContext(ctx, session, principal)
}

func (engine *Engine) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if engine == nil || ctx == nil || token == "" || nilCapability(engine.sessions) {
		return nil, contentapi.ErrUnauthorized
	}
	session, principal, err := engine.sessions.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, mapError(err)
	}
	return engine.authenticatedContext(ctx, session, principal)
}

func (engine *Engine) authenticatedContext(ctx context.Context, session recorddata.AuthorizationSubject, principal recorddata.Principal) (context.Context, error) {
	if ctx == nil || principal.ID == "" || !principal.Active {
		return nil, contentapi.ErrUnauthorized
	}
	state := authenticatedState{owner: engine, session: session, principal: principal}
	return context.WithValue(ctx, authenticatedContextKey{}, state), nil
}

func (engine *Engine) authorize(ctx context.Context, required recorddata.AccessLevel) (authenticatedState, error) {
	if engine == nil || ctx == nil || nilCapability(engine.authorization) {
		return authenticatedState{}, contentapi.ErrUnauthorized
	}
	state, ok := ctx.Value(authenticatedContextKey{}).(authenticatedState)
	if !ok || state.owner != engine || state.principal.ID == "" || !state.principal.Active {
		return authenticatedState{}, contentapi.ErrUnauthorized
	}
	if err := engine.authorization.RequireGroupLevel(ctx, state.session, []string{accessGroup}, required); err != nil {
		return authenticatedState{}, mapError(err)
	}
	return state, nil
}

func (engine *Engine) GetContentProjections(ctx context.Context, pageSnapshotID string) (contentapi.Projections, error) {
	if _, err := engine.authorize(ctx, recorddata.AccessLevelRead); err != nil {
		return contentapi.Projections{}, err
	}
	if nilCapability(engine.projections) {
		return contentapi.Projections{}, contentapi.ErrUnavailable
	}
	projections, err := engine.projections.GetContentProjections(ctx, pageSnapshotID)
	if err != nil {
		return contentapi.Projections{}, mapError(err)
	}
	if !projections.VerifyScope(pageSnapshotID) {
		return contentapi.Projections{}, contentapi.ErrUnavailable
	}
	return projections, nil
}

func (engine *Engine) GetLocaleConfig(ctx context.Context) (domain.LocaleConfig, error) {
	if _, err := engine.authorize(ctx, recorddata.AccessLevelRead); err != nil {
		return domain.LocaleConfig{}, err
	}
	config, err := engine.service.GetLocaleConfig(ctx)
	if err != nil {
		return domain.LocaleConfig{}, mapError(err)
	}
	return config, nil
}

func (engine *Engine) ReplaceLocaleConfig(ctx context.Context, request domain.ReplaceLocaleConfigRequest) (domain.LocaleConfig, error) {
	if _, err := engine.authorize(ctx, recorddata.AccessLevelWrite); err != nil {
		return domain.LocaleConfig{}, err
	}
	config, err := engine.service.ReplaceLocaleConfig(ctx, request)
	if err != nil {
		return domain.LocaleConfig{}, mapError(err)
	}
	return config, nil
}

func (engine *Engine) GetWorkspace(ctx context.Context, projection contentapi.Projections) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, recorddata.AccessLevelRead)
	if err != nil {
		return contentapi.Workspace{}, fmt.Errorf("authorize content workspace: %w", err)
	}
	schema, err := engine.resolveSchema(ctx, projection, projection.Page.PageSnapshotID, false)
	if err != nil {
		return contentapi.Workspace{}, fmt.Errorf("resolve content workspace projection: %w", err)
	}
	workspace, err := engine.service.GetWorkspace(ctx, state.session, schema, projection.Page.PageSnapshotID)
	if err != nil {
		return contentapi.Workspace{}, fmt.Errorf("load content workspace data: %w", mapError(err))
	}
	return toAPIWorkspace(workspace), nil
}

func (engine *Engine) SaveWorkspaceDraft(ctx context.Context, projection contentapi.Projections, request contentapi.SaveWorkspaceDraftRequest) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, recorddata.AccessLevelWrite)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	schema, err := engine.resolveSchema(ctx, projection, request.SnapshotID, false)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	workspace, err := engine.service.SaveWorkspaceDraft(ctx, state.session, schema, request)
	if err != nil {
		return contentapi.Workspace{}, mapError(err)
	}
	return toAPIWorkspace(workspace), nil
}

func (engine *Engine) PublishWorkspace(ctx context.Context, projection contentapi.Projections, request contentapi.PublishWorkspaceRequest) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, recorddata.AccessLevelWrite)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	schema, err := engine.resolveSchema(ctx, projection, request.SnapshotID, false)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	workspace, err := engine.service.PublishWorkspace(ctx, state.session, schema, request)
	if err != nil {
		return contentapi.Workspace{}, mapError(err)
	}
	return toAPIWorkspace(workspace), nil
}

func (engine *Engine) ListHistory(ctx context.Context, projection contentapi.Projections, cursor string, limit int) (contentapi.HistoryPage, error) {
	if _, err := engine.authorize(ctx, recorddata.AccessLevelRead); err != nil {
		return contentapi.HistoryPage{}, err
	}
	schema, err := engine.resolveSchema(ctx, projection, projection.Page.PageSnapshotID, false)
	if err != nil {
		return contentapi.HistoryPage{}, err
	}
	page, err := engine.service.ListHistory(ctx, schema, projection.Page.PageSnapshotID, cursor, limit)
	if err != nil {
		return contentapi.HistoryPage{}, mapError(err)
	}
	result := contentapi.HistoryPage{Items: make([]contentapi.HistoryItem, len(page.Items)), NextCursor: page.NextCursor}
	for index, item := range page.Items {
		result.Items[index] = contentapi.HistoryItem{
			ID: item.ID, DraftRevision: item.DraftRevision, ContentRevision: item.ContentRevision,
			CreatedAt: item.CreatedAt, RecordCount: item.RecordCount,
		}
	}
	return result, nil
}

func (engine *Engine) RestoreHistory(ctx context.Context, projection contentapi.Projections, request contentapi.RestoreHistoryRequest) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, recorddata.AccessLevelWrite)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	schema, err := engine.resolveSchema(ctx, projection, request.SnapshotID, false)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	workspace, err := engine.service.RestoreHistory(ctx, state.session, schema, request)
	if err != nil {
		return contentapi.Workspace{}, mapError(err)
	}
	return toAPIWorkspace(workspace), nil
}

// ReadPublishedContent is an in-process projection for trusted runtime
// composition. Anonymous HTTP delivery is owned by Router and its route table.
func (engine *Engine) ReadPublishedContent(ctx context.Context, projection contentapi.Projections) (contentapi.PublishedContent, error) {
	schema, err := engine.resolveSchema(ctx, projection, projection.Page.PageSnapshotID, false)
	if err != nil {
		return contentapi.PublishedContent{}, err
	}
	content, err := engine.service.ReadPublishedContent(ctx, schema, projection.Page.PageSnapshotID)
	if err != nil {
		return contentapi.PublishedContent{}, mapError(err)
	}
	return contentapi.PublishedContent{
		SnapshotID: content.SnapshotID, Locale: content.Locale, ContentRevision: content.ContentRevision,
		Records: toAPIRecords(content.Records), Selections: toAPISelections(content.Selections),
	}, nil
}

func (engine *Engine) CopySnapshotContent(ctx context.Context, request contentapi.CopySnapshotContentRequest) (contentapi.CopySnapshotContentResult, error) {
	if !contentapi.ValidSelectionPolicy(request.SelectionPolicy) {
		return contentapi.CopySnapshotContentResult{}, contentapi.ErrInvalid
	}
	state, err := engine.authorize(ctx, recorddata.AccessLevelWrite)
	if err != nil {
		return contentapi.CopySnapshotContentResult{}, err
	}
	if request.SourceSnapshotID == "" || request.TargetSnapshotID == "" || request.SourceSnapshotID == request.TargetSnapshotID || request.ExpectedSourceRevision < 0 ||
		request.SourceSchema.Page.PageSnapshotID != request.SourceSnapshotID || request.TargetSchema.Page.PageSnapshotID != request.TargetSnapshotID {
		return contentapi.CopySnapshotContentResult{}, contentapi.ErrInvalid
	}
	source, err := engine.resolveSchema(ctx, request.SourceSchema, request.SourceSnapshotID, false)
	if err != nil {
		return contentapi.CopySnapshotContentResult{}, err
	}
	target, err := engine.resolveSchema(ctx, request.TargetSchema, request.TargetSnapshotID, true)
	if err != nil {
		return contentapi.CopySnapshotContentResult{}, err
	}
	result, err := engine.service.CopySnapshotContent(ctx, state.session, source, target, request)
	if err != nil {
		mapped := mapError(err)
		if errors.Is(err, domain.ErrConflict) {
			return contentapi.CopySnapshotContentResult{}, fmt.Errorf("%w: %s", mapped, err)
		}
		return contentapi.CopySnapshotContentResult{}, mapped
	}
	return contentapi.CopySnapshotContentResult{
		TargetSnapshotID: result.TargetSnapshotID, SourceRevision: result.SourceRevision,
		CopiedRecords: result.CopiedRecords, CopiedHistory: result.CopiedHistory,
	}, nil
}

func (engine *Engine) resolveSchema(ctx context.Context, projections contentapi.Projections, pageSnapshotID string, allowProvisioning bool) (domain.Schema, error) {
	page := projections.Page
	if engine == nil || !domain.ValidID(pageSnapshotID) || !projections.VerifyScope(pageSnapshotID) {
		return domain.Schema{}, contentapi.ErrInvalid
	}
	if page.State != "ready" && !(allowProvisioning && page.State == "provisioning") {
		return domain.Schema{}, contentapi.ErrNotFound
	}
	locale, localeErr := domain.CanonicalLocale(projections.Locale)
	if localeErr != nil || locale != projections.Locale {
		return domain.Schema{}, contentapi.ErrInvalid
	}
	localeConfig, err := engine.service.GetLocaleConfig(ctx)
	if err != nil {
		return domain.Schema{}, mapError(err)
	}
	localeConfigured := false
	for _, configured := range localeConfig.Locales {
		if configured == locale {
			localeConfigured = true
			break
		}
	}
	if !localeConfigured {
		return domain.Schema{}, contentapi.ErrInvalid
	}
	dataModel := append([]byte(nil), projections.ContentModel.DataModel...)
	model, err := domain.ParseDataModel(dataModel)
	if err != nil {
		return domain.Schema{}, contentapi.ErrInvalid
	}
	placements := make([]domain.PlacementView, len(page.Placements))
	for index, placement := range page.Placements {
		inputs := make([]domain.PlacementInput, len(placement.Inputs))
		for inputIndex, input := range placement.Inputs {
			fields := make([]domain.PlacementField, len(input.Fields))
			for fieldIndex, field := range input.Fields {
				fields[fieldIndex] = domain.PlacementField{InputPath: field.InputPath, FieldID: field.FieldID, MediaAccept: field.MediaAccept}
			}
			var lookup *domain.PlacementInputLookup
			if input.Lookup != nil {
				lookup = &domain.PlacementInputLookup{Argument: input.Lookup.Argument, FieldID: input.Lookup.FieldID}
			}
			inputs[inputIndex] = domain.PlacementInput{InputKey: input.InputKey, Kind: input.Kind, EntityID: input.EntityID, Fields: fields, Lookup: lookup}
		}
		placements[index] = domain.PlacementView{ID: placement.ID, PageID: placement.PageID, FeatureKey: placement.FeatureKey, FeatureName: placement.FeatureName, Version: placement.Version, Inputs: inputs}
	}
	return domain.Schema{State: page.State, PageSnapshotID: pageSnapshotID, Locale: locale, LocaleCatalogRevision: localeConfig.Revision,
		ModelRevision: projections.ContentModel.ModelRevision, ModelDigest: projections.ContentModel.Digest,
		PageRevision: page.Revision, PageDigest: page.Digest, DataModel: dataModel, Model: model, Placements: placements}, nil
}

func toAPIWorkspace(workspace domain.WorkspaceView) contentapi.Workspace {
	placements := make([]contentapi.Placement, len(workspace.Placements))
	for index, placement := range workspace.Placements {
		inputs := make([]contentapi.PlacementInput, len(placement.Inputs))
		for inputIndex, input := range placement.Inputs {
			fields := make([]contentapi.PlacementField, len(input.Fields))
			for fieldIndex, field := range input.Fields {
				fields[fieldIndex] = contentapi.PlacementField{InputPath: field.InputPath, FieldID: field.FieldID, MediaAccept: field.MediaAccept}
			}
			var lookup *contentapi.PlacementInputLookup
			if input.Lookup != nil {
				lookup = &contentapi.PlacementInputLookup{Argument: input.Lookup.Argument, FieldID: input.Lookup.FieldID}
			}
			inputs[inputIndex] = contentapi.PlacementInput{InputKey: input.InputKey, Kind: input.Kind, EntityID: input.EntityID, Fields: fields, Lookup: lookup}
		}
		placements[index] = contentapi.Placement{
			ID: placement.ID, PageID: placement.PageID, PageName: placement.PageName,
			FeatureKey: placement.FeatureKey, FeatureName: placement.FeatureName, Version: placement.Version, Inputs: inputs,
		}
	}
	return contentapi.Workspace{
		SnapshotID: workspace.SnapshotID, PageSnapshotID: workspace.PageSnapshotID, Locale: workspace.Locale, LocaleCatalogRevision: workspace.LocaleCatalogRevision,
		ModelRevision: workspace.ModelRevision, ModelDigest: workspace.ModelDigest, PageRevision: workspace.PageRevision, PageDigest: workspace.PageDigest,
		DataModel: append([]byte(nil), workspace.DataModel...),
		Revision:  workspace.Revision, SelectionRevision: workspace.SelectionRevision, PublishedRevision: workspace.PublishedRevision,
		Records: toAPIRecords(workspace.Records), Selections: toAPISelections(workspace.Selections),
		Placements: placements, ClientRecordIDs: cloneStringMap(workspace.ClientRecordIDs), ClientTranslationIDs: cloneStringMap(workspace.ClientTranslationIDs),
	}
}

func toAPIRecords(records []domain.Record) []contentapi.Record {
	result := make([]contentapi.Record, len(records))
	for index, record := range records {
		values := make(map[string]any, len(record.Values))
		for key, value := range record.Values {
			values[key] = value
		}
		result[index] = contentapi.Record{ID: record.ID, TranslationID: record.TranslationID, Locale: record.Locale, EntityID: record.EntityID, SortOrder: record.SortOrder, Values: values}
	}
	return result
}

func toAPISelections(selections []domain.Selection) []contentapi.Selection {
	result := make([]contentapi.Selection, len(selections))
	for index, selection := range selections {
		result[index] = contentapi.Selection{PlacementID: selection.PlacementID, InputKey: selection.InputKey, Mode: selection.Mode, RecordIDs: append([]string(nil), selection.RecordIDs...)}
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, contentapi.ErrUnauthorized), errors.Is(err, recorddata.ErrAccessUnauthorized):
		return contentapi.ErrUnauthorized
	case errors.Is(err, contentapi.ErrForbidden), errors.Is(err, recorddata.ErrAccessForbidden):
		return contentapi.ErrForbidden
	case errors.Is(err, contentapi.ErrNotFound), errors.Is(err, domain.ErrNotFound), errors.Is(err, recorddata.ErrFileNotFound):
		return contentapi.ErrNotFound
	case errors.Is(err, contentapi.ErrConflict), errors.Is(err, domain.ErrConflict), errors.Is(err, recorddata.ErrRecordConflict), errors.Is(err, recorddata.ErrFileRevisionConflict):
		return contentapi.ErrConflict
	case errors.Is(err, contentapi.ErrInvalid), errors.Is(err, domain.ErrInvalid), errors.Is(err, recorddata.ErrRecordInvalid):
		return contentapi.ErrInvalid
	case errors.Is(err, contentapi.ErrTooLarge), errors.Is(err, domain.ErrTooLarge), errors.Is(err, recorddata.ErrRecordLimit):
		return contentapi.ErrTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return contentapi.ErrUnavailable
	}
}

func nilCapability(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ contentapi.Contract = (*Engine)(nil)
