package forms

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"

	core "github.com/Zubrium/cdn/core/presentation"
	schemaapp "github.com/Zubrium/cdn/engines/forms/features/schemas/application"
	schemarepo "github.com/Zubrium/cdn/engines/forms/features/schemas/repository"
	submissionapp "github.com/Zubrium/cdn/engines/forms/features/submissions/application"
	submissionrepo "github.com/Zubrium/cdn/engines/forms/features/submissions/repository"
	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
)

type DataStore interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
	Delete(context.Context, core.ModuleDataKey, int64) error
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
	DeleteFile(context.Context, core.Session, string, int64) error
}

type Dependencies struct {
	Identity      core.EngineIdentityCapability
	Sessions      core.SessionCapability
	Authorization core.GroupAuthorizationCapability
	Files         FileOperations
	Data          DataStore
}

type Engine struct {
	formapi.Descriptor
	identity      core.EngineIdentityCapability
	sessions      core.SessionCapability
	authorization core.GroupAuthorizationCapability
	schemas       *schemaapp.Service
	submissions   *submissionapp.Service
}

type authenticatedContextKey struct{}

type authenticatedState struct {
	owner     *Engine
	session   core.Session
	principal core.Principal
}

func New(dependencies Dependencies) (*Engine, error) {
	if nilCapability(dependencies.Identity) || nilCapability(dependencies.Sessions) || nilCapability(dependencies.Authorization) ||
		nilCapability(dependencies.Files) || nilCapability(dependencies.Data) {
		return nil, formapi.ErrUnavailable
	}
	schemaRepository, err := schemarepo.New(dependencies.Data)
	if err != nil {
		return nil, mapCoreError(err)
	}
	submissionRepository, err := submissionrepo.New(dependencies.Data)
	if err != nil {
		return nil, mapCoreError(err)
	}
	schemas, err := schemaapp.New(schemaRepository, dependencies.Data.MaxPageSize())
	if err != nil {
		return nil, mapCoreError(err)
	}
	submissions, err := submissionapp.New(submissionRepository, dependencies.Files, dependencies.Data.MaxPageSize())
	if err != nil {
		return nil, mapCoreError(err)
	}
	return &Engine{
		identity: dependencies.Identity, sessions: dependencies.Sessions, authorization: dependencies.Authorization,
		schemas: schemas, submissions: submissions,
	}, nil
}

func (engine *Engine) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if engine == nil || ctx == nil || nilCapability(engine.identity) || credential == "" {
		return nil, formapi.ErrUnauthorized
	}
	session, principal, err := engine.identity.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return engine.authenticatedContext(ctx, session, principal)
}

func (engine *Engine) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if engine == nil || ctx == nil || nilCapability(engine.sessions) || token == "" {
		return nil, formapi.ErrUnauthorized
	}
	session, principal, err := engine.sessions.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return engine.authenticatedContext(ctx, session, principal)
}

func (engine *Engine) authenticatedContext(ctx context.Context, session core.Session, principal core.Principal) (context.Context, error) {
	if ctx == nil || principal.ID == "" || !principal.Active {
		return nil, formapi.ErrUnauthorized
	}
	return context.WithValue(ctx, authenticatedContextKey{}, authenticatedState{owner: engine, session: session, principal: principal}), nil
}

func (engine *Engine) authorize(ctx context.Context, required core.AccessLevel) (authenticatedState, error) {
	if engine == nil || ctx == nil || nilCapability(engine.authorization) {
		return authenticatedState{}, formapi.ErrUnauthorized
	}
	state, ok := ctx.Value(authenticatedContextKey{}).(authenticatedState)
	if !ok || state.owner != engine || state.principal.ID == "" || !state.principal.Active {
		return authenticatedState{}, formapi.ErrUnauthorized
	}
	if err := engine.authorization.RequireGroupLevel(ctx, state.session, []string{"forms"}, required); err != nil {
		return authenticatedState{}, mapCoreError(err)
	}
	return state, nil
}

func (engine *Engine) ListSchemas(ctx context.Context, snapshotID, cursor string, limit int) (formapi.SchemaPage, error) {
	if _, err := engine.authorize(ctx, core.AccessLevelRead); err != nil {
		return formapi.SchemaPage{}, err
	}
	after, err := decodeCursor(cursor)
	if err != nil {
		return formapi.SchemaPage{}, formapi.ErrInvalid
	}
	items, next, err := engine.schemas.List(ctx, snapshotID, after, limit)
	if err != nil {
		return formapi.SchemaPage{}, mapCoreError(err)
	}
	page := formapi.SchemaPage{Items: make([]formapi.Schema, len(items))}
	for i, item := range items {
		page.Items[i] = formapi.Schema{ID: item.Schema.ID, Key: item.Schema.Key, Version: item.Schema.Version, Fields: item.Schema.Fields, Revision: item.Revision}
	}
	page.NextCursor = encodeCursor(next)
	return page, nil
}

func (engine *Engine) GetSchema(ctx context.Context, snapshotID, schemaID string) (formapi.Schema, error) {
	if _, err := engine.authorize(ctx, core.AccessLevelRead); err != nil {
		return formapi.Schema{}, err
	}
	schema, revision, err := engine.schemas.Get(ctx, snapshotID, schemaID)
	if err != nil {
		return formapi.Schema{}, mapCoreError(err)
	}
	return formapi.Schema{ID: schema.ID, Key: schema.Key, Version: schema.Version, Fields: schema.Fields, Revision: revision}, nil
}

func (engine *Engine) SaveSchema(ctx context.Context, request formapi.SaveSchemaRequest) (formapi.Schema, error) {
	if _, err := engine.authorize(ctx, core.AccessLevelWrite); err != nil {
		return formapi.Schema{}, err
	}
	schema, revision, err := engine.schemas.Save(ctx, request.SnapshotID, request.SchemaID, request.Key, request.Fields, request.ExpectedRevision)
	if err != nil {
		return formapi.Schema{}, mapCoreError(err)
	}
	return formapi.Schema{ID: schema.ID, Key: schema.Key, Version: schema.Version, Fields: schema.Fields, Revision: revision}, nil
}

func (engine *Engine) ListSubmissions(ctx context.Context, request formapi.ListSubmissionsRequest) (formapi.SubmissionPage, error) {
	if _, err := engine.authorize(ctx, core.AccessLevelRead); err != nil {
		return formapi.SubmissionPage{}, err
	}
	after, err := decodeCursor(request.Cursor)
	if err != nil {
		return formapi.SubmissionPage{}, formapi.ErrInvalid
	}
	items, next, err := engine.submissions.List(ctx, request.SnapshotID, request.FormID, request.State, after, request.Limit)
	if err != nil {
		return formapi.SubmissionPage{}, mapCoreError(err)
	}
	page := formapi.SubmissionPage{Items: make([]formapi.Submission, len(items))}
	for i, item := range items {
		page.Items[i] = formapi.Submission{
			ID: item.Submission.ID, FormID: item.Submission.FormID, State: item.Submission.State,
			Values: item.Submission.Values, CreatedAt: item.Submission.CreatedAt, Revision: item.Revision,
		}
	}
	page.NextCursor = encodeCursor(next)
	return page, nil
}

func (engine *Engine) GetSubmission(ctx context.Context, snapshotID, id string) (formapi.Submission, error) {
	if _, err := engine.authorize(ctx, core.AccessLevelRead); err != nil {
		return formapi.Submission{}, err
	}
	submission, revision, err := engine.submissions.Get(ctx, snapshotID, id)
	if err != nil {
		return formapi.Submission{}, mapCoreError(err)
	}
	return formapi.Submission{
		ID: submission.ID, FormID: submission.FormID, State: submission.State,
		Values: submission.Values, CreatedAt: submission.CreatedAt, Revision: revision,
	}, nil
}

func (engine *Engine) SetSubmissionStatus(ctx context.Context, request formapi.SetSubmissionStatusRequest) (formapi.Submission, error) {
	if _, err := engine.authorize(ctx, core.AccessLevelWrite); err != nil {
		return formapi.Submission{}, err
	}
	submission, revision, err := engine.submissions.SetState(ctx, request.SnapshotID, request.SubmissionID, request.Status, request.ExpectedRevision)
	if err != nil {
		return formapi.Submission{}, mapCoreError(err)
	}
	return formapi.Submission{
		ID: submission.ID, FormID: submission.FormID, State: submission.State,
		Values: submission.Values, CreatedAt: submission.CreatedAt, Revision: revision,
	}, nil
}

func (engine *Engine) DeleteSubmission(ctx context.Context, request formapi.DeleteSubmissionRequest) error {
	state, err := engine.authorize(ctx, core.AccessLevelWrite)
	if err != nil {
		return err
	}
	if err := engine.submissions.Delete(ctx, state.session, request.SnapshotID, request.SubmissionID, request.ExpectedRevision); err != nil {
		return mapCoreError(err)
	}
	return nil
}

func encodeCursor(value string) string {
	if value == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeCursor(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > 1024 {
		return "", formapi.ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > 256 {
		return "", formapi.ErrInvalid
	}
	return string(decoded), nil
}

func mapCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrUnauthorized):
		return formapi.ErrUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return formapi.ErrForbidden
	case errors.Is(err, core.ErrModuleDataNotFound), errors.Is(err, core.ErrFileNotFound):
		return formapi.ErrNotFound
	case errors.Is(err, core.ErrModuleDataConflict), errors.Is(err, core.ErrRevisionConflict):
		return formapi.ErrConflict
	case errors.Is(err, core.ErrModuleDataInvalid):
		return formapi.ErrInvalid
	case errors.Is(err, core.ErrModuleDataLimitExceeded):
		return formapi.ErrTooLarge
	default:
		return formapi.ErrUnavailable
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

var _ formapi.Contract = (*Engine)(nil)
var _ core.Module = (*Engine)(nil)
