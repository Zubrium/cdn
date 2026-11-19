package forms

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	schemaapp "github.com/Zubrium/cdn/engines/forms/features/schemas/application"
	schemamodel "github.com/Zubrium/cdn/engines/forms/features/schemas/model"
	schemarepo "github.com/Zubrium/cdn/engines/forms/features/schemas/repository"
	sessionapp "github.com/Zubrium/cdn/engines/forms/features/sessions/application"
	sessionrepo "github.com/Zubrium/cdn/engines/forms/features/sessions/repository"
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
	ListExpired(context.Context, string, string, string, string, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
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
	Identity          core.EngineIdentityCapability
	Sessions          core.SessionCapability
	Authorization     core.GroupAuthorizationCapability
	Files             FileOperations
	Data              DataStore
	FileSession       core.Session
	VisitorSessionTTL time.Duration
}

type Engine struct {
	formapi.Descriptor
	identity      core.EngineIdentityCapability
	sessions      core.SessionCapability
	authorization core.GroupAuthorizationCapability
	fileSession   core.Session
	schemas       *schemaapp.Service
	submissions   *submissionapp.Service
	visitors      *sessionapp.Service
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
	visitorRepository, err := sessionrepo.New(dependencies.Data)
	if err != nil {
		return nil, mapCoreError(err)
	}
	visitors, err := sessionapp.New(visitorRepository, schemas, submissions, dependencies.Files, dependencies.FileSession,
		dependencies.VisitorSessionTTL, dependencies.Data.MaxPageSize())
	if err != nil {
		return nil, mapCoreError(err)
	}
	return &Engine{
		identity: dependencies.Identity, sessions: dependencies.Sessions, authorization: dependencies.Authorization,
		fileSession: dependencies.FileSession,
		schemas:     schemas, submissions: submissions, visitors: visitors,
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

func (engine *Engine) authorize(ctx context.Context, group string, required core.AccessLevel) (authenticatedState, error) {
	if engine == nil || ctx == nil || nilCapability(engine.authorization) {
		return authenticatedState{}, formapi.ErrUnauthorized
	}
	state, ok := ctx.Value(authenticatedContextKey{}).(authenticatedState)
	if !ok || state.owner != engine || state.principal.ID == "" || !state.principal.Active {
		return authenticatedState{}, formapi.ErrUnauthorized
	}
	if err := engine.authorization.RequireGroupLevel(ctx, state.session, []string{group}, required); err != nil {
		return authenticatedState{}, mapCoreError(err)
	}
	return state, nil
}

func (engine *Engine) ListSchemas(ctx context.Context, snapshotID, cursor string, limit int) (formapi.SchemaPage, error) {
	if _, err := engine.authorize(ctx, formapi.SchemaAccessGroup, core.AccessLevelRead); err != nil {
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
	if _, err := engine.authorize(ctx, formapi.SchemaAccessGroup, core.AccessLevelRead); err != nil {
		return formapi.Schema{}, err
	}
	schema, revision, err := engine.schemas.Get(ctx, snapshotID, schemaID)
	if err != nil {
		return formapi.Schema{}, mapCoreError(err)
	}
	return formapi.Schema{ID: schema.ID, Key: schema.Key, Version: schema.Version, Fields: schema.Fields, Revision: revision}, nil
}

func (engine *Engine) SaveSchema(ctx context.Context, request formapi.SaveSchemaRequest) (formapi.Schema, error) {
	if _, err := engine.authorize(ctx, formapi.SchemaAccessGroup, core.AccessLevelWrite); err != nil {
		return formapi.Schema{}, err
	}
	schema, revision, err := engine.schemas.Save(ctx, request.SnapshotID, request.SchemaID, request.Key, request.Fields, request.ExpectedRevision)
	if err != nil {
		return formapi.Schema{}, mapCoreError(err)
	}
	_ = engine.visitors.CancelFormSessions(ctx, request.SnapshotID, schema.ID, schema.Version)
	return formapi.Schema{ID: schema.ID, Key: schema.Key, Version: schema.Version, Fields: schema.Fields, Revision: revision}, nil
}

func (engine *Engine) CreateVisitorSession(ctx context.Context, request formapi.CreateVisitorSessionRequest) (formapi.VisitorSessionCredential, error) {
	created, err := engine.visitors.Create(ctx, sessionapp.Binding{SnapshotID: request.SnapshotID, FormID: request.FormID, SchemaVersion: request.SchemaVersion})
	if err != nil {
		return formapi.VisitorSessionCredential{}, mapVisitorError(err)
	}
	return formapi.VisitorSessionCredential{Token: created.Token, Session: formapi.VisitorSession{
		ID: created.Session.ID, FormID: created.Session.FormID, SchemaVersion: created.Session.SchemaVersion,
		Fields: created.Session.Fields, ExpiresAt: created.Session.ExpiresAt, Completed: created.Session.Completed,
	}}, nil
}

func (engine *Engine) GetVisitorSession(ctx context.Context, request formapi.GetVisitorSessionRequest) (formapi.VisitorSession, error) {
	session, err := engine.visitors.Get(ctx, sessionapp.Binding{SnapshotID: request.SnapshotID, FormID: request.FormID, SchemaVersion: request.SchemaVersion}, request.Token)
	if err != nil {
		return formapi.VisitorSession{}, mapVisitorError(err)
	}
	return formapi.VisitorSession{ID: session.ID, FormID: session.FormID, SchemaVersion: session.SchemaVersion,
		Fields: session.Fields, ExpiresAt: session.ExpiresAt, Completed: session.Completed}, nil
}

func (engine *Engine) BeginVisitorUpload(ctx context.Context, request formapi.BeginVisitorUploadRequest) (formapi.VisitorUploadReservation, error) {
	reservation, err := engine.visitors.BeginUpload(ctx, sessionapp.BeginUploadInput{
		Binding: sessionapp.Binding{SnapshotID: request.SnapshotID, FormID: request.FormID, SchemaVersion: request.SchemaVersion},
		Token:   request.Token, FieldID: request.FieldID, IdempotencyKey: request.IdempotencyKey,
		ContentType: request.ContentType, DeclaredSize: request.DeclaredSize, RouteMaxBytes: request.RouteMaxBytes,
		RouteContentTypes: request.RouteContentTypes, AccessGroup: request.AccessGroup,
	})
	if err != nil {
		return formapi.VisitorUploadReservation{}, mapVisitorError(err)
	}
	return formapi.VisitorUploadReservation{ID: reservation.ID, FieldID: reservation.FieldID, MaxBytes: reservation.MaxBytes,
		Completed: reservation.Completed, FileID: reservation.FileID, SHA256: reservation.SHA256, AttachmentID: reservation.ID,
		ContentType: reservation.ContentType, Size: reservation.Size,
		AccessGroup: reservation.AccessGroup}, nil
}

func (engine *Engine) CompleteVisitorUpload(ctx context.Context, request formapi.CompleteVisitorUploadRequest) (formapi.VisitorAttachment, error) {
	attachment, err := engine.visitors.CompleteUpload(ctx, sessionapp.CompleteUploadInput{
		Binding: sessionapp.Binding{SnapshotID: request.SnapshotID, FormID: request.FormID, SchemaVersion: request.SchemaVersion},
		Token:   request.Token, ReservationID: request.ReservationID, FileID: request.FileID, SHA256: request.SHA256,
		ContentType: request.ContentType, Size: request.Size, AccessGroup: request.AccessGroup,
	})
	if err != nil {
		return formapi.VisitorAttachment{}, mapVisitorError(err)
	}
	return formapi.VisitorAttachment{ID: attachment.AttachmentID, FieldID: attachment.FieldID}, nil
}

func (engine *Engine) AbortVisitorUpload(ctx context.Context, request formapi.AbortVisitorUploadRequest) error {
	return mapVisitorError(engine.visitors.AbortUpload(ctx, sessionapp.AbortUploadInput{
		Binding: sessionapp.Binding{SnapshotID: request.SnapshotID, FormID: request.FormID, SchemaVersion: request.SchemaVersion},
		Token:   request.Token, ReservationID: request.ReservationID,
	}))
}

func (engine *Engine) SubmitVisitorResponse(ctx context.Context, request formapi.SubmitVisitorResponseRequest) (formapi.VisitorSubmissionReceipt, error) {
	receipt, err := engine.visitors.Submit(ctx, sessionapp.SubmitInput{
		Binding: sessionapp.Binding{SnapshotID: request.SnapshotID, FormID: request.FormID, SchemaVersion: request.SchemaVersion},
		Token:   request.Token, IdempotencyKey: request.IdempotencyKey, Values: request.Values,
	})
	if err != nil {
		return formapi.VisitorSubmissionReceipt{}, mapVisitorError(err)
	}
	return formapi.VisitorSubmissionReceipt{ID: receipt.ID, CreatedAt: receipt.CreatedAt}, nil
}

func (engine *Engine) CancelVisitorSession(ctx context.Context, request formapi.CancelVisitorSessionRequest) error {
	return mapVisitorError(engine.visitors.Cancel(ctx, sessionapp.Binding{SnapshotID: request.SnapshotID,
		FormID: request.FormID, SchemaVersion: request.SchemaVersion}, request.Token))
}

func (engine *Engine) RunVisitorSessionCleanup(ctx context.Context, interval time.Duration) <-chan error {
	errorsChannel := make(chan error, 1)
	go func() {
		defer close(errorsChannel)
		if interval <= 0 {
			errorsChannel <- formapi.ErrUnavailable
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := engine.visitors.Cleanup(ctx); err != nil {
					select {
					case errorsChannel <- mapVisitorError(err):
					default:
					}
				}
			}
		}
	}()
	return errorsChannel
}

func (engine *Engine) ListSubmissions(ctx context.Context, request formapi.ListSubmissionsRequest) (formapi.SubmissionPage, error) {
	if _, err := engine.authorize(ctx, formapi.SubmissionAccessGroup, core.AccessLevelRead); err != nil {
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
			ID: item.Submission.ID, FormID: item.Submission.FormID, SchemaVersion: item.Submission.SchemaVersion, State: item.Submission.State,
			Values: item.Submission.Values, CreatedAt: item.Submission.CreatedAt, Revision: item.Revision,
		}
	}
	page.NextCursor = encodeCursor(next)
	return page, nil
}

func (engine *Engine) GetSubmission(ctx context.Context, snapshotID, id string) (formapi.Submission, error) {
	if _, err := engine.authorize(ctx, formapi.SubmissionAccessGroup, core.AccessLevelRead); err != nil {
		return formapi.Submission{}, err
	}
	submission, revision, err := engine.submissions.Get(ctx, snapshotID, id)
	if err != nil {
		return formapi.Submission{}, mapCoreError(err)
	}
	return formapi.Submission{
		ID: submission.ID, FormID: submission.FormID, SchemaVersion: submission.SchemaVersion, State: submission.State,
		Values: submission.Values, CreatedAt: submission.CreatedAt, Revision: revision,
	}, nil
}

func (engine *Engine) SetSubmissionStatus(ctx context.Context, request formapi.SetSubmissionStatusRequest) (formapi.Submission, error) {
	if _, err := engine.authorize(ctx, formapi.SubmissionAccessGroup, core.AccessLevelWrite); err != nil {
		return formapi.Submission{}, err
	}
	submission, revision, err := engine.submissions.SetState(ctx, request.SnapshotID, request.SubmissionID, request.Status, request.ExpectedRevision)
	if err != nil {
		return formapi.Submission{}, mapCoreError(err)
	}
	return formapi.Submission{
		ID: submission.ID, FormID: submission.FormID, SchemaVersion: submission.SchemaVersion, State: submission.State,
		Values: submission.Values, CreatedAt: submission.CreatedAt, Revision: revision,
	}, nil
}

func (engine *Engine) DeleteSubmission(ctx context.Context, request formapi.DeleteSubmissionRequest) error {
	if _, err := engine.authorize(ctx, formapi.SubmissionAccessGroup, core.AccessLevelWrite); err != nil {
		return err
	}
	if err := engine.submissions.Delete(ctx, engine.fileSession, request.SnapshotID, request.SubmissionID, request.ExpectedRevision); err != nil {
		return mapCoreError(err)
	}
	return nil
}

// ValidatePublicForm validates a server-configured Router binding without
// granting access to visitor responses or exposing the schema to an arbitrary
// caller. Public operations call it only during route validation.
func (engine *Engine) ValidatePublicForm(ctx context.Context, binding formapi.PublicFormBinding) error {
	if engine == nil || ctx == nil || binding.Validate() != nil {
		return formapi.ErrInvalid
	}
	schema, _, err := engine.schemas.Get(ctx, binding.SnapshotID, binding.FormID)
	if err != nil {
		return mapCoreError(err)
	}
	if schema.Version != binding.SchemaVersion {
		return formapi.ErrConflict
	}
	if _, err := schemamodel.ParseFieldDefinitions(schema.Fields); err != nil {
		return formapi.ErrUnavailable
	}
	return nil
}

// ValidatePublicUploadRoute checks both the fixed form version and the file
// service account's write access to the route's private destination before a
// public upload route can be persisted.
func (engine *Engine) ValidatePublicUploadRoute(ctx context.Context, binding formapi.PublicUploadRouteBinding) error {
	if engine == nil || ctx == nil || binding.PublicFormBinding.Validate() != nil ||
		!core.ValidAccessGroupKey(binding.AccessGroup) || binding.MaxBytes <= 0 || len(binding.AllowedContentTypes) == 0 || len(binding.AllowedContentTypes) > 32 {
		return formapi.ErrInvalid
	}
	if err := engine.ValidatePublicForm(ctx, binding.PublicFormBinding); err != nil {
		return err
	}
	if err := engine.authorization.RequireGroupLevel(ctx, engine.fileSession, []string{binding.AccessGroup}, core.AccessLevelWrite); err != nil {
		return mapCoreError(err)
	}
	schema, _, err := engine.schemas.Get(ctx, binding.SnapshotID, binding.FormID)
	if err != nil {
		return mapCoreError(err)
	}
	definitions, err := schemamodel.ParseFieldDefinitions(schema.Fields)
	if err != nil {
		return formapi.ErrUnavailable
	}
	for _, field := range definitions {
		if field.Type != "file" || min(field.MaxFileBytes, binding.MaxBytes) <= 0 {
			continue
		}
		for _, allowed := range binding.AllowedContentTypes {
			if containsMediaType(field.AllowedContentTypes, allowed) {
				return nil
			}
		}
	}
	return formapi.ErrInvalid
}

func containsMediaType(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
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

func mapVisitorError(err error) error {
	switch {
	case errors.Is(err, sessionapp.ErrExpired):
		return formapi.ErrExpired
	case errors.Is(err, sessionapp.ErrMismatch):
		return formapi.ErrExpired
	default:
		return mapCoreError(err)
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
