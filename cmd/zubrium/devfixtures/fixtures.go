//go:build devfixtures

// Package devfixtures supplies read-only synthetic engine APIs for the
// explicitly selected development-fixtures profile. Mutations always fail.
package devfixtures

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	content "github.com/Zubrium/cdn/engines/content/presentation"
	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	website "github.com/Zubrium/cdn/engines/website/presentation"
)

const (
	websiteGroup = "features"
	contentGroup = "content"
	formsGroup   = "forms"

	syntheticSiteID     = "7d4e83e2-bb0e-40d8-a8be-2d197e95a001"
	syntheticSnapshotID = "7d4e83e2-bb0e-40d8-a8be-2d197e95a002"
	syntheticModelID    = "7d4e83e2-bb0e-40d8-a8be-2d197e95a003"
	syntheticRecordID   = "7d4e83e2-bb0e-40d8-a8be-2d197e95a004"
	syntheticSchemaID   = "7d4e83e2-bb0e-40d8-a8be-2d197e95a005"
	syntheticFormID     = "7d4e83e2-bb0e-40d8-a8be-2d197e95a006"
	syntheticEntryID    = "7d4e83e2-bb0e-40d8-a8be-2d197e95a007"
	syntheticFeatureID  = "demo-announcement"
	syntheticVersion    = "1.0.0"
)

type Core interface {
	EngineIdentity() core.EngineIdentityCapability
	Sessions() core.SessionCapability
	GroupAuthorization() core.GroupAuthorizationCapability
}

type Config struct {
	ConfiguredGroups map[string]core.AccessLevel
}

type Set struct {
	Website *Website
	Content *Content
	Forms   *Forms
}

func New(authority Core, config Config) (*Set, error) {
	if nilInterface(authority) || nilInterface(authority.EngineIdentity()) || nilInterface(authority.Sessions()) || nilInterface(authority.GroupAuthorization()) {
		return nil, errors.New("development fixtures require Core identity, sessions, and group authorization")
	}
	for _, group := range []string{websiteGroup, contentGroup, formsGroup} {
		if !core.ValidAccessGroupKey(group) {
			return nil, errors.New("development fixture access-group key is invalid")
		}
		if _, exists := config.ConfiguredGroups[group]; !exists {
			return nil, errors.New("development fixtures require all configured engine access groups")
		}
	}
	identity := authority.EngineIdentity()
	sessions := authority.Sessions()
	authorization := authority.GroupAuthorization()
	return &Set{
		Website: &Website{base: base{identity: identity, sessions: sessions, authorization: authorization, group: websiteGroup,
			unauthorized: website.ErrUnauthorized, forbidden: website.ErrForbidden, unavailable: website.ErrUnavailable}},
		Content: &Content{base: base{identity: identity, sessions: sessions, authorization: authorization, group: contentGroup,
			unauthorized: content.ErrUnauthorized, forbidden: content.ErrForbidden, unavailable: content.ErrUnavailable}},
		Forms: &Forms{base: base{identity: identity, sessions: sessions, authorization: authorization, group: formsGroup,
			unauthorized: forms.ErrUnauthorized, forbidden: forms.ErrForbidden, unavailable: forms.ErrUnavailable}},
	}, nil
}

type sessionContextKey struct{}

type base struct {
	identity      core.EngineIdentityCapability
	sessions      core.SessionCapability
	authorization core.GroupAuthorizationCapability
	group         string
	unauthorized  error
	forbidden     error
	unavailable   error
}

func (b base) authenticate(ctx context.Context, credential string) (context.Context, error) {
	session, _, err := b.identity.Authenticate(ctx, credential)
	if err != nil {
		return nil, b.mapError(err)
	}
	return context.WithValue(ctx, sessionContextKey{}, session), nil
}

func (b base) authenticateSession(ctx context.Context, token string) (context.Context, error) {
	session, _, err := b.sessions.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, b.mapError(err)
	}
	return context.WithValue(ctx, sessionContextKey{}, session), nil
}

func (b base) require(ctx context.Context, level core.AccessLevel) error {
	session, ok := ctx.Value(sessionContextKey{}).(core.Session)
	if !ok {
		return b.unauthorized
	}
	if err := b.authorization.RequireGroupLevel(ctx, session, []string{b.group}, level); err != nil {
		return b.mapError(err)
	}
	return nil
}

func (b base) mapError(err error) error {
	switch {
	case errors.Is(err, core.ErrUnauthorized):
		return b.unauthorized
	case errors.Is(err, core.ErrForbidden):
		return b.forbidden
	default:
		return b.unavailable
	}
}

type Website struct{ base }
type Content struct{ base }
type Forms struct{ base }

func (a *Website) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	return a.base.authenticate(ctx, credential)
}
func (a *Website) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	return a.base.authenticateSession(ctx, token)
}
func (a *Content) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	return a.base.authenticate(ctx, credential)
}
func (a *Content) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	return a.base.authenticateSession(ctx, token)
}
func (a *Forms) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	return a.base.authenticate(ctx, credential)
}
func (a *Forms) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	return a.base.authenticateSession(ctx, token)
}

func (a *Website) ListFeatureCatalog(ctx context.Context, _ string, _ int) (website.FeatureVersionPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.FeatureVersionPage{}, err
	}
	return website.FeatureVersionPage{Items: []website.FeatureVersion{demoFeature()}}, nil
}
func (a *Website) ListFeatureVersions(ctx context.Context, featureID, _ string, _ int) (website.FeatureVersionPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.FeatureVersionPage{}, err
	}
	if featureID != syntheticFeatureID {
		return website.FeatureVersionPage{}, website.ErrNotFound
	}
	return website.FeatureVersionPage{Items: []website.FeatureVersion{demoFeature()}}, nil
}
func (a *Website) GetFeatureVersion(ctx context.Context, featureID, version string) (website.FeatureVersion, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.FeatureVersion{}, err
	}
	if featureID != syntheticFeatureID || version != syntheticVersion {
		return website.FeatureVersion{}, website.ErrNotFound
	}
	return demoFeature(), nil
}
func (a *Website) BeginFeatureImport(ctx context.Context, _ website.ImportFeatureRequest) (website.FeatureImport, error) {
	return website.FeatureImport{}, a.writeUnavailable(ctx)
}
func (a *Website) GetFeatureImport(ctx context.Context, _ string) (website.FeatureImport, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.FeatureImport{}, err
	}
	return website.FeatureImport{}, website.ErrNotFound
}
func (a *Website) ListSnapshots(ctx context.Context, _ string, _ string, _ int) (website.SnapshotPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.SnapshotPage{}, err
	}
	return website.SnapshotPage{Items: []website.Snapshot{demoSnapshot()}}, nil
}
func (a *Website) GetSnapshot(ctx context.Context, id string) (website.Snapshot, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.Snapshot{}, err
	}
	if id != syntheticSnapshotID {
		return website.Snapshot{}, website.ErrNotFound
	}
	return demoSnapshot(), nil
}
func (a *Website) GetSnapshotDesignSystem(ctx context.Context, id string) (website.SnapshotDesignSystem, error) {
	snapshot, err := a.GetSnapshot(ctx, id)
	if err != nil {
		return website.SnapshotDesignSystem{}, err
	}
	return website.SnapshotDesignSystem{Snapshot: snapshot, DesignSystem: json.RawMessage(`{"tokens":{}}`), FeatureTokens: json.RawMessage(`{}`)}, nil
}
func (a *Website) CreateSnapshot(ctx context.Context, _ website.CreateSnapshotRequest) (website.Snapshot, error) {
	return website.Snapshot{}, a.writeUnavailable(ctx)
}
func (a *Website) DeleteSnapshot(ctx context.Context, _ website.DeleteSnapshotRequest) error {
	return a.writeUnavailable(ctx)
}
func (a *Website) PinFeatureVersion(ctx context.Context, _ website.PinFeatureVersionRequest) (website.Snapshot, error) {
	return website.Snapshot{}, a.writeUnavailable(ctx)
}
func (a *Website) ListBranches(ctx context.Context) (website.BranchPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.BranchPage{}, err
	}
	return website.BranchPage{Items: []website.Branch{}}, nil
}
func (a *Website) AssignBranch(ctx context.Context, _ website.AssignBranchRequest) (website.Branch, error) {
	return website.Branch{}, a.writeUnavailable(ctx)
}
func (a *Website) ListBranchEvents(ctx context.Context, _ string, _ string, _ int) (website.BranchEventPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.BranchEventPage{}, err
	}
	return website.BranchEventPage{Items: []website.BranchEvent{}}, nil
}
func (a *Website) BuildSnapshot(ctx context.Context, _ website.BuildSnapshotRequest) (website.BuildJob, error) {
	return website.BuildJob{}, a.writeUnavailable(ctx)
}
func (a *Website) GetBuild(ctx context.Context, _ string) (website.BuildJob, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.BuildJob{}, err
	}
	return website.BuildJob{}, website.ErrNotFound
}
func (a *Website) CreateMediaDerivative(ctx context.Context, _ website.CreateDerivativeRequest) (website.MediaJob, error) {
	return website.MediaJob{}, a.writeUnavailable(ctx)
}
func (a *Website) GetMediaJob(ctx context.Context, _ string) (website.MediaJob, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.MediaJob{}, err
	}
	return website.MediaJob{}, website.ErrNotFound
}

func (a *Content) ListModels(ctx context.Context, _ string, _ string, _ int) (content.ModelPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.ModelPage{}, err
	}
	return content.ModelPage{Items: []content.Model{demoModel()}}, nil
}
func (a *Content) GetModel(ctx context.Context, snapshotID, modelID string) (content.Model, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.Model{}, err
	}
	if snapshotID != syntheticSnapshotID || modelID != syntheticModelID {
		return content.Model{}, content.ErrNotFound
	}
	return demoModel(), nil
}
func (a *Content) SaveModel(ctx context.Context, _ content.SaveModelRequest) (content.Model, error) {
	return content.Model{}, a.writeUnavailable(ctx)
}
func (a *Content) ListRecords(ctx context.Context, request content.ListRecordsRequest) (content.RecordPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.RecordPage{}, err
	}
	if request.SnapshotID != syntheticSnapshotID || request.ModelID != syntheticModelID {
		return content.RecordPage{Items: []content.Record{}}, nil
	}
	return content.RecordPage{Items: []content.Record{demoRecord()}}, nil
}
func (a *Content) SaveRecordDraft(ctx context.Context, _ content.SaveRecordRequest) (content.Record, error) {
	return content.Record{}, a.writeUnavailable(ctx)
}
func (a *Content) PublishRecord(ctx context.Context, _ content.PublishRecordRequest) (content.Record, error) {
	return content.Record{}, a.writeUnavailable(ctx)
}
func (a *Content) RestoreRecord(ctx context.Context, _ content.RestoreRecordRequest) (content.Record, error) {
	return content.Record{}, a.writeUnavailable(ctx)
}
func (a *Content) GetBindings(ctx context.Context, snapshotID, featureID string) (content.Bindings, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.Bindings{}, err
	}
	if snapshotID != syntheticSnapshotID || featureID != syntheticFeatureID {
		return content.Bindings{}, content.ErrNotFound
	}
	return content.Bindings{SnapshotID: snapshotID, FeatureID: featureID, Values: map[string]any{}, Revision: 1}, nil
}
func (a *Content) SaveBindings(ctx context.Context, _ content.SaveBindingsRequest) (content.Bindings, error) {
	return content.Bindings{}, a.writeUnavailable(ctx)
}

func (a *Forms) ListSchemas(ctx context.Context, _ string, _ string, _ int) (forms.SchemaPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return forms.SchemaPage{}, err
	}
	return forms.SchemaPage{Items: []forms.Schema{demoSchema()}}, nil
}
func (a *Forms) GetSchema(ctx context.Context, snapshotID, schemaID string) (forms.Schema, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return forms.Schema{}, err
	}
	if snapshotID != syntheticSnapshotID || schemaID != syntheticSchemaID {
		return forms.Schema{}, forms.ErrNotFound
	}
	return demoSchema(), nil
}
func (a *Forms) SaveSchema(ctx context.Context, _ forms.SaveSchemaRequest) (forms.Schema, error) {
	return forms.Schema{}, a.writeUnavailable(ctx)
}
func (a *Forms) ListSubmissions(ctx context.Context, request forms.ListSubmissionsRequest) (forms.SubmissionPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return forms.SubmissionPage{}, err
	}
	if request.SnapshotID != syntheticSnapshotID || (request.FormID != "" && request.FormID != syntheticFormID) {
		return forms.SubmissionPage{Items: []forms.Submission{}}, nil
	}
	return forms.SubmissionPage{Items: []forms.Submission{demoSubmission()}}, nil
}
func (a *Forms) GetSubmission(ctx context.Context, snapshotID, submissionID string) (forms.Submission, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return forms.Submission{}, err
	}
	if snapshotID != syntheticSnapshotID || submissionID != syntheticEntryID {
		return forms.Submission{}, forms.ErrNotFound
	}
	return demoSubmission(), nil
}
func (a *Forms) SetSubmissionStatus(ctx context.Context, _ forms.SetSubmissionStatusRequest) (forms.Submission, error) {
	return forms.Submission{}, a.writeUnavailable(ctx)
}
func (a *Forms) DeleteSubmission(ctx context.Context, _ forms.DeleteSubmissionRequest) error {
	return a.writeUnavailable(ctx)
}

func (a *Website) writeUnavailable(ctx context.Context) error {
	if err := a.require(ctx, core.AccessLevelWrite); err != nil {
		return err
	}
	return website.ErrUnavailable
}
func (a *Content) writeUnavailable(ctx context.Context) error {
	if err := a.require(ctx, core.AccessLevelWrite); err != nil {
		return err
	}
	return content.ErrUnavailable
}
func (a *Forms) writeUnavailable(ctx context.Context) error {
	if err := a.require(ctx, core.AccessLevelWrite); err != nil {
		return err
	}
	return forms.ErrUnavailable
}

func demoFeature() website.FeatureVersion {
	return website.FeatureVersion{FeatureID: syntheticFeatureID, Version: syntheticVersion, Manifest: []byte(`{"feature_id":"demo-announcement","version":"1.0.0","name":"Demo announcement","synthetic":true}`),
		FileSetID: "7d4e83e2-bb0e-40d8-a8be-2d197e95a008", Files: map[string]string{"index.html": "synthetic/index.html"},
		Digest: "0000000000000000000000000000000000000000000000000000000000000000", ArtifactSize: 0, CreatedAt: demoTime, State: "ready"}
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() core.ComponentDescriptor {
	return core.ComponentDescriptor{ID: "development.fixtures", Kind: "development-fixture", Version: 1}
}

func (Descriptor) Operations() []core.Operation { return nil }

var _ core.Module = Descriptor{}

var demoTime = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

func demoSnapshot() website.Snapshot {
	return website.Snapshot{ID: syntheticSnapshotID, SiteID: syntheticSiteID, Name: "Synthetic demo", Sequence: 1, Revision: 1,
		ContentRevision: 1, CreatedAt: demoTime, FeaturePins: map[string]string{syntheticFeatureID: syntheticVersion}, State: "draft",
		Payload: &website.SnapshotPayload{Config: json.RawMessage(`{"pages":[],"routes":[]}`), Metadata: json.RawMessage(`{"synthetic":true}`), DesignSystem: json.RawMessage(`{"tokens":{}}`)}}
}

func demoModel() content.Model {
	return content.Model{ID: syntheticModelID, Key: "announcement", Name: "Announcement", Cardinality: "many", Revision: 1,
		Fields: []content.ModelField{{ID: "7d4e83e2-bb0e-40d8-a8be-2d197e95a009", Key: "title", Name: "Title", Type: "text", Required: true}}}
}

func demoRecord() content.Record {
	return content.Record{ID: syntheticRecordID, ModelID: syntheticModelID, Values: map[string]any{"title": "Synthetic content record"}, Revision: 1, PublishedRevision: 1, UpdatedAt: demoTime}
}

func demoSchema() forms.Schema {
	return forms.Schema{ID: syntheticSchemaID, Key: "contact", Version: 1, Revision: 1, Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}}}
}

func demoSubmission() forms.Submission {
	return forms.Submission{ID: syntheticEntryID, FormID: syntheticFormID, State: "new", Values: map[string]any{"email": "visitor@example.invalid"}, CreatedAt: demoTime, Revision: 1}
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var (
	_ website.API = (*Website)(nil)
	_ content.API = (*Content)(nil)
	_ forms.API   = (*Forms)(nil)
)
