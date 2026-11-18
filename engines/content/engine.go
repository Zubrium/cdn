package content

import (
	"bytes"
	"context"
	"errors"
	"reflect"

	core "github.com/Zubrium/cdn/core/presentation"
	contentapp "github.com/Zubrium/cdn/engines/content/application"
	"github.com/Zubrium/cdn/engines/content/domain"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/engines/content/repository"
)

const accessGroup = "content"

type DataStore interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
	Batch(context.Context, []core.ModuleDataMutation) error
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}

type FileOperations interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
}

type Dependencies struct {
	Identity      core.EngineIdentityCapability
	Sessions      core.SessionCapability
	Authorization core.GroupAuthorizationCapability
	Files         FileOperations
	Data          DataStore
	Schema        contentapi.SchemaResolver
}

type Engine struct {
	contentapi.Descriptor
	identity      core.EngineIdentityCapability
	sessions      core.SessionCapability
	authorization core.GroupAuthorizationCapability
	resolver      contentapi.SchemaResolver
	service       *contentapp.Service
}

type authenticatedContextKey struct{}

type authenticatedState struct {
	owner     *Engine
	session   core.Session
	principal core.Principal
}

func New(dependencies Dependencies) (*Engine, error) {
	if nilCapability(dependencies.Identity) || nilCapability(dependencies.Sessions) || nilCapability(dependencies.Authorization) ||
		nilCapability(dependencies.Files) || nilCapability(dependencies.Data) || nilCapability(dependencies.Schema) {
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
		resolver: dependencies.Schema, service: service,
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

func (engine *Engine) authenticatedContext(ctx context.Context, session core.Session, principal core.Principal) (context.Context, error) {
	if ctx == nil || principal.ID == "" || !principal.Active {
		return nil, contentapi.ErrUnauthorized
	}
	state := authenticatedState{owner: engine, session: session, principal: principal}
	return context.WithValue(ctx, authenticatedContextKey{}, state), nil
}

func (engine *Engine) authorize(ctx context.Context, required core.AccessLevel) (authenticatedState, error) {
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

func (engine *Engine) GetWorkspace(ctx context.Context, snapshotID string) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, core.AccessLevelRead)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	schema, err := engine.resolveSchema(ctx, snapshotID)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	workspace, err := engine.service.GetWorkspace(ctx, state.session, schema, snapshotID)
	if err != nil {
		return contentapi.Workspace{}, mapError(err)
	}
	return toAPIWorkspace(workspace), nil
}

func (engine *Engine) SaveWorkspaceDraft(ctx context.Context, request contentapi.SaveWorkspaceDraftRequest) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, core.AccessLevelWrite)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	schema, err := engine.resolveSchema(ctx, request.SnapshotID)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	workspace, err := engine.service.SaveWorkspaceDraft(ctx, state.session, schema, request)
	if err != nil {
		return contentapi.Workspace{}, mapError(err)
	}
	return toAPIWorkspace(workspace), nil
}

func (engine *Engine) PublishWorkspace(ctx context.Context, request contentapi.PublishWorkspaceRequest) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, core.AccessLevelWrite)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	schema, err := engine.resolveSchema(ctx, request.SnapshotID)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	workspace, err := engine.service.PublishWorkspace(ctx, state.session, schema, request)
	if err != nil {
		return contentapi.Workspace{}, mapError(err)
	}
	return toAPIWorkspace(workspace), nil
}

func (engine *Engine) ListHistory(ctx context.Context, snapshotID, cursor string, limit int) (contentapi.HistoryPage, error) {
	if _, err := engine.authorize(ctx, core.AccessLevelRead); err != nil {
		return contentapi.HistoryPage{}, err
	}
	schema, err := engine.resolveSchema(ctx, snapshotID)
	if err != nil {
		return contentapi.HistoryPage{}, err
	}
	page, err := engine.service.ListHistory(ctx, schema, snapshotID, cursor, limit)
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

func (engine *Engine) RestoreHistory(ctx context.Context, request contentapi.RestoreHistoryRequest) (contentapi.Workspace, error) {
	state, err := engine.authorize(ctx, core.AccessLevelWrite)
	if err != nil {
		return contentapi.Workspace{}, err
	}
	schema, err := engine.resolveSchema(ctx, request.SnapshotID)
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
func (engine *Engine) ReadPublishedContent(ctx context.Context, snapshotID string) (contentapi.PublishedContent, error) {
	schema, err := engine.resolveSchema(ctx, snapshotID)
	if err != nil {
		return contentapi.PublishedContent{}, err
	}
	content, err := engine.service.ReadPublishedContent(ctx, schema, snapshotID)
	if err != nil {
		return contentapi.PublishedContent{}, mapError(err)
	}
	return contentapi.PublishedContent{
		SnapshotID: content.SnapshotID, ContentRevision: content.ContentRevision,
		Records: toAPIRecords(content.Records), Selections: toAPISelections(content.Selections),
	}, nil
}

func (engine *Engine) CopySnapshotContent(ctx context.Context, request contentapi.CopySnapshotContentRequest) (contentapi.CopySnapshotContentResult, error) {
	state, err := engine.authorize(ctx, core.AccessLevelWrite)
	if err != nil {
		return contentapi.CopySnapshotContentResult{}, err
	}
	source, err := engine.resolveSchema(ctx, request.SourceSnapshotID)
	if err != nil {
		return contentapi.CopySnapshotContentResult{}, err
	}
	target, err := engine.resolveSchema(ctx, request.TargetSnapshotID)
	if err != nil {
		return contentapi.CopySnapshotContentResult{}, err
	}
	result, err := engine.service.CopySnapshotContent(ctx, state.session, source, target, request)
	if err != nil {
		return contentapi.CopySnapshotContentResult{}, mapError(err)
	}
	return contentapi.CopySnapshotContentResult{
		TargetSnapshotID: result.TargetSnapshotID, SourceRevision: result.SourceRevision,
		CopiedRecords: result.CopiedRecords, CopiedHistory: result.CopiedHistory,
	}, nil
}

func (engine *Engine) resolveSchema(ctx context.Context, snapshotID string) (domain.Schema, error) {
	if engine == nil || ctx == nil || !domain.ValidID(snapshotID) || nilCapability(engine.resolver) {
		return domain.Schema{}, contentapi.ErrNotFound
	}
	resolved, err := engine.resolver.ResolveContentSchema(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return domain.Schema{}, err
		}
		return domain.Schema{}, mapError(err)
	}
	dataModel := append([]byte(nil), resolved.DataModel...)
	if len(bytes.TrimSpace(dataModel)) == 0 {
		dataModel = []byte(`{"formatVersion":1,"entities":[]}`)
	}
	model, err := domain.ParseDataModel(dataModel)
	if err != nil {
		return domain.Schema{}, contentapi.ErrUnavailable
	}
	placements := make([]domain.PlacementView, len(resolved.Placements))
	for index, placement := range resolved.Placements {
		inputs := make([]domain.PlacementInput, len(placement.Inputs))
		for inputIndex, input := range placement.Inputs {
			fields := make([]domain.PlacementField, len(input.Fields))
			for fieldIndex, field := range input.Fields {
				fields[fieldIndex] = domain.PlacementField{InputPath: field.InputPath, FieldID: field.FieldID}
			}
			var lookup *domain.PlacementInputLookup
			if input.Lookup != nil {
				lookup = &domain.PlacementInputLookup{Argument: input.Lookup.Argument, FieldID: input.Lookup.FieldID}
			}
			inputs[inputIndex] = domain.PlacementInput{InputKey: input.InputKey, Kind: input.Kind, EntityID: input.EntityID, Fields: fields, Lookup: lookup}
		}
		placements[index] = domain.PlacementView{
			ID: placement.ID, PageID: placement.PageID, PageName: placement.PageName,
			FeatureKey: placement.FeatureKey, FeatureName: placement.FeatureName, Version: placement.Version,
			Inputs: inputs,
		}
	}
	return domain.Schema{State: resolved.State, DataModel: dataModel, Model: model, Placements: placements}, nil
}

func toAPIWorkspace(workspace domain.WorkspaceView) contentapi.Workspace {
	placements := make([]contentapi.Placement, len(workspace.Placements))
	for index, placement := range workspace.Placements {
		inputs := make([]contentapi.PlacementInput, len(placement.Inputs))
		for inputIndex, input := range placement.Inputs {
			fields := make([]contentapi.PlacementField, len(input.Fields))
			for fieldIndex, field := range input.Fields {
				fields[fieldIndex] = contentapi.PlacementField{InputPath: field.InputPath, FieldID: field.FieldID}
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
		SnapshotID: workspace.SnapshotID, DataModel: append([]byte(nil), workspace.DataModel...),
		Revision: workspace.Revision, PublishedRevision: workspace.PublishedRevision,
		Records: toAPIRecords(workspace.Records), Selections: toAPISelections(workspace.Selections),
		Placements: placements, ClientRecordIDs: cloneStringMap(workspace.ClientRecordIDs),
	}
}

func toAPIRecords(records []domain.Record) []contentapi.Record {
	result := make([]contentapi.Record, len(records))
	for index, record := range records {
		values := make(map[string]any, len(record.Values))
		for key, value := range record.Values {
			values[key] = value
		}
		result[index] = contentapi.Record{ID: record.ID, EntityID: record.EntityID, SortOrder: record.SortOrder, Values: values}
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
	case errors.Is(err, contentapi.ErrUnauthorized), errors.Is(err, core.ErrUnauthorized):
		return contentapi.ErrUnauthorized
	case errors.Is(err, contentapi.ErrForbidden), errors.Is(err, core.ErrForbidden):
		return contentapi.ErrForbidden
	case errors.Is(err, contentapi.ErrNotFound), errors.Is(err, domain.ErrNotFound), errors.Is(err, core.ErrFileNotFound):
		return contentapi.ErrNotFound
	case errors.Is(err, contentapi.ErrConflict), errors.Is(err, domain.ErrConflict), errors.Is(err, core.ErrModuleDataConflict), errors.Is(err, core.ErrRevisionConflict):
		return contentapi.ErrConflict
	case errors.Is(err, contentapi.ErrInvalid), errors.Is(err, domain.ErrInvalid), errors.Is(err, core.ErrModuleDataInvalid):
		return contentapi.ErrInvalid
	case errors.Is(err, contentapi.ErrTooLarge), errors.Is(err, domain.ErrTooLarge), errors.Is(err, core.ErrModuleDataLimitExceeded):
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
var _ core.Module = (*Engine)(nil)
