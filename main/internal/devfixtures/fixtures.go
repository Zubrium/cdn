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
	websiteGroup         = "features"
	contentGroup         = "content"
	formsSchemaGroup     = forms.SchemaAccessGroup
	formsSubmissionGroup = forms.SubmissionAccessGroup

	syntheticSnapshotID = "7d4e83e2-bb0e-40d8-a8be-2d197e95a002"
	syntheticPageID     = "7d4e83e2-bb0e-40d8-a8be-2d197e95a003"
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
	for _, group := range []string{websiteGroup, contentGroup, formsSchemaGroup, formsSubmissionGroup} {
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
		Forms: &Forms{base: base{identity: identity, sessions: sessions, authorization: authorization, group: formsSchemaGroup,
			unauthorized: forms.ErrUnauthorized, forbidden: forms.ErrForbidden, unavailable: forms.ErrUnavailable}, submissionGroup: formsSubmissionGroup},
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
	return b.requireGroup(ctx, b.group, level)
}

func (b base) requireGroup(ctx context.Context, group string, level core.AccessLevel) error {
	session, ok := ctx.Value(sessionContextKey{}).(core.Session)
	if !ok {
		return b.unauthorized
	}
	if err := b.authorization.RequireGroupLevel(ctx, session, []string{group}, level); err != nil {
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
type Forms struct {
	base
	submissionGroup string
}

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

func (a *Website) ListPages(ctx context.Context, _ string, _ int) (website.PagePage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.PagePage{}, err
	}
	return website.PagePage{Items: []website.Page{demoPage()}}, nil
}
func (a *Website) GetPage(ctx context.Context, pageID string) (website.Page, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.Page{}, err
	}
	if pageID != syntheticPageID {
		return website.Page{}, website.ErrNotFound
	}
	return demoPage(), nil
}
func (a *Website) CreatePage(ctx context.Context, _ website.CreatePageRequest) (website.Page, error) {
	return website.Page{}, a.writeUnavailable(ctx)
}
func (a *Website) ListPageTemplates(ctx context.Context, _ string, _ int) (website.PageTemplatePage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.PageTemplatePage{}, err
	}
	return website.PageTemplatePage{Items: []website.PageTemplate{}}, nil
}
func (a *Website) GetPageTemplate(ctx context.Context, _ string) (website.PageTemplate, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.PageTemplate{}, err
	}
	return website.PageTemplate{}, website.ErrNotFound
}
func (a *Website) CreatePageTemplate(ctx context.Context, _ website.CreatePageTemplateRequest) (website.PageTemplate, error) {
	return website.PageTemplate{}, a.writeUnavailable(ctx)
}
func (a *Website) UpdatePageTemplate(ctx context.Context, _ website.UpdatePageTemplateRequest) (website.PageTemplate, error) {
	return website.PageTemplate{}, a.writeUnavailable(ctx)
}
func (a *Website) DeletePageTemplate(ctx context.Context, _ website.DeletePageTemplateRequest) error {
	return a.writeUnavailable(ctx)
}
func (a *Website) ApplyPageTemplate(ctx context.Context, _ website.ApplyPageTemplateRequest) (website.Snapshot, error) {
	return website.Snapshot{}, a.writeUnavailable(ctx)
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
func (a *Website) ListSnapshots(ctx context.Context, pageID, _ string, _ int) (website.SnapshotPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.SnapshotPage{}, err
	}
	if pageID != syntheticPageID {
		return website.SnapshotPage{}, website.ErrNotFound
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
func (a *Website) GetContentModel(ctx context.Context) (website.ContentModel, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.ContentModel{}, err
	}
	model := demoContentModel()
	if model.Digest == "" {
		return website.ContentModel{}, website.ErrUnavailable
	}
	return model, nil
}
func (a *Website) GetPagePlacementProjection(ctx context.Context, pageID, snapshotID string) (website.PagePlacementProjection, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return website.PagePlacementProjection{}, err
	}
	if pageID != syntheticPageID || snapshotID != syntheticSnapshotID {
		return website.PagePlacementProjection{}, website.ErrNotFound
	}
	projection := demoPagePlacementProjection()
	if projection.Digest == "" {
		return website.PagePlacementProjection{}, website.ErrUnavailable
	}
	return projection, nil
}
func (a *Website) SaveContentModel(ctx context.Context, _ website.SaveContentModelRequest) (website.ContentModel, error) {
	return website.ContentModel{}, a.writeUnavailable(ctx)
}
func (a *Website) CreateSnapshot(ctx context.Context, _ website.CreateSnapshotRequest) (website.Snapshot, error) {
	return website.Snapshot{}, a.writeUnavailable(ctx)
}
func (a *Website) MarkSnapshotReady(ctx context.Context, _ string, _ int64) (website.Snapshot, error) {
	return website.Snapshot{}, a.writeUnavailable(ctx)
}
func (a *Website) DeleteSnapshot(ctx context.Context, _ website.DeleteSnapshotRequest) error {
	return a.writeUnavailable(ctx)
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

func (a *Content) GetContentProjections(ctx context.Context, pageSnapshotID string) (content.Projections, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.Projections{}, err
	}
	if pageSnapshotID != syntheticSnapshotID {
		return content.Projections{}, content.ErrNotFound
	}
	model := demoContentModel()
	pageSchema := demoPagePlacementProjection()
	projections := content.Projections{
		ContentModel: content.ContentModelProjection{Version: model.Version, ModelRevision: model.ModelRevision,
			Digest: model.Digest, DataModel: model.DataModel},
		Page: content.PagePlacementProjection{Version: pageSchema.Version, PageSnapshotID: pageSchema.PageSnapshotID, PageID: pageSchema.PageID,
			Revision: pageSchema.Revision, State: pageSchema.State, Placements: []content.Placement{}},
	}
	digest, err := projections.Page.ComputeDigest()
	if err != nil {
		return content.Projections{}, content.ErrUnavailable
	}
	projections.Page.Digest = digest
	if !projections.VerifyScope(pageSnapshotID) {
		return content.Projections{}, content.ErrUnavailable
	}
	return projections, nil
}

func (a *Content) GetLocaleConfig(ctx context.Context) (content.LocaleConfig, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.LocaleConfig{}, err
	}
	return content.LocaleConfig{Revision: 1, Locales: []string{"en-US"}, DefaultLocale: "en-US"}, nil
}
func (a *Content) ReplaceLocaleConfig(ctx context.Context, _ content.ReplaceLocaleConfigRequest) (content.LocaleConfig, error) {
	return content.LocaleConfig{}, a.writeUnavailable(ctx)
}

func (a *Content) GetWorkspace(ctx context.Context, projections content.Projections) (content.Workspace, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.Workspace{}, err
	}
	if !validContentProjections(projections) {
		return content.Workspace{}, content.ErrNotFound
	}
	return demoWorkspace(projections), nil
}
func (a *Content) SaveWorkspaceDraft(ctx context.Context, _ content.Projections, _ content.SaveWorkspaceDraftRequest) (content.Workspace, error) {
	return content.Workspace{}, a.writeUnavailable(ctx)
}
func (a *Content) PublishWorkspace(ctx context.Context, _ content.Projections, _ content.PublishWorkspaceRequest) (content.Workspace, error) {
	return content.Workspace{}, a.writeUnavailable(ctx)
}
func (a *Content) ListHistory(ctx context.Context, projections content.Projections, _ string, _ int) (content.HistoryPage, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.HistoryPage{}, err
	}
	if !validContentProjections(projections) {
		return content.HistoryPage{}, content.ErrNotFound
	}
	return content.HistoryPage{Items: []content.HistoryItem{}}, nil
}
func (a *Content) RestoreHistory(ctx context.Context, _ content.Projections, _ content.RestoreHistoryRequest) (content.Workspace, error) {
	return content.Workspace{}, a.writeUnavailable(ctx)
}
func (a *Content) ReadPublishedContent(ctx context.Context, projections content.Projections) (content.PublishedContent, error) {
	if err := a.require(ctx, core.AccessLevelRead); err != nil {
		return content.PublishedContent{}, err
	}
	if !validContentProjections(projections) {
		return content.PublishedContent{}, content.ErrNotFound
	}
	workspace := demoWorkspace(projections)
	return content.PublishedContent{SnapshotID: projections.Page.PageSnapshotID, ContentRevision: workspace.PublishedRevision, Records: workspace.Records, Selections: workspace.Selections}, nil
}
func (a *Content) CopySnapshotContent(ctx context.Context, _ content.CopySnapshotContentRequest) (content.CopySnapshotContentResult, error) {
	return content.CopySnapshotContentResult{}, a.writeUnavailable(ctx)
}

func (a *Forms) ListSchemas(ctx context.Context, _ string, _ string, _ int) (forms.SchemaPage, error) {
	if err := a.requireGroup(ctx, formsSchemaGroup, core.AccessLevelRead); err != nil {
		return forms.SchemaPage{}, err
	}
	return forms.SchemaPage{Items: []forms.Schema{demoSchema()}}, nil
}
func (a *Forms) GetSchema(ctx context.Context, snapshotID, schemaID string) (forms.Schema, error) {
	if err := a.requireGroup(ctx, formsSchemaGroup, core.AccessLevelRead); err != nil {
		return forms.Schema{}, err
	}
	if snapshotID != syntheticSnapshotID || schemaID != syntheticSchemaID {
		return forms.Schema{}, forms.ErrNotFound
	}
	return demoSchema(), nil
}
func (a *Forms) SaveSchema(ctx context.Context, _ forms.SaveSchemaRequest) (forms.Schema, error) {
	return forms.Schema{}, a.writeUnavailableGroup(ctx, formsSchemaGroup)
}
func (a *Forms) ListSubmissions(ctx context.Context, request forms.ListSubmissionsRequest) (forms.SubmissionPage, error) {
	if err := a.requireGroup(ctx, formsSubmissionGroup, core.AccessLevelRead); err != nil {
		return forms.SubmissionPage{}, err
	}
	if request.SnapshotID != syntheticSnapshotID || (request.FormID != "" && request.FormID != syntheticFormID) {
		return forms.SubmissionPage{Items: []forms.Submission{}}, nil
	}
	return forms.SubmissionPage{Items: []forms.Submission{demoSubmission()}}, nil
}
func (a *Forms) GetSubmission(ctx context.Context, snapshotID, submissionID string) (forms.Submission, error) {
	if err := a.requireGroup(ctx, formsSubmissionGroup, core.AccessLevelRead); err != nil {
		return forms.Submission{}, err
	}
	if snapshotID != syntheticSnapshotID || submissionID != syntheticEntryID {
		return forms.Submission{}, forms.ErrNotFound
	}
	return demoSubmission(), nil
}
func (a *Forms) SetSubmissionStatus(ctx context.Context, _ forms.SetSubmissionStatusRequest) (forms.Submission, error) {
	return forms.Submission{}, a.writeUnavailableGroup(ctx, formsSubmissionGroup)
}
func (a *Forms) DeleteSubmission(ctx context.Context, _ forms.DeleteSubmissionRequest) error {
	return a.writeUnavailableGroup(ctx, formsSubmissionGroup)
}
func (*Forms) ValidatePublicForm(context.Context, forms.PublicFormBinding) error {
	return forms.ErrUnavailable
}
func (*Forms) ValidatePublicUploadRoute(context.Context, forms.PublicUploadRouteBinding) error {
	return forms.ErrUnavailable
}
func (*Forms) CreateVisitorSession(context.Context, forms.CreateVisitorSessionRequest) (forms.VisitorSessionCredential, error) {
	return forms.VisitorSessionCredential{}, forms.ErrUnavailable
}
func (*Forms) GetVisitorSession(context.Context, forms.GetVisitorSessionRequest) (forms.VisitorSession, error) {
	return forms.VisitorSession{}, forms.ErrUnavailable
}
func (*Forms) BeginVisitorUpload(context.Context, forms.BeginVisitorUploadRequest) (forms.VisitorUploadReservation, error) {
	return forms.VisitorUploadReservation{}, forms.ErrUnavailable
}
func (*Forms) CompleteVisitorUpload(context.Context, forms.CompleteVisitorUploadRequest) (forms.VisitorAttachment, error) {
	return forms.VisitorAttachment{}, forms.ErrUnavailable
}
func (*Forms) AbortVisitorUpload(context.Context, forms.AbortVisitorUploadRequest) error {
	return forms.ErrUnavailable
}
func (*Forms) SubmitVisitorResponse(context.Context, forms.SubmitVisitorResponseRequest) (forms.VisitorSubmissionReceipt, error) {
	return forms.VisitorSubmissionReceipt{}, forms.ErrUnavailable
}
func (*Forms) CancelVisitorSession(context.Context, forms.CancelVisitorSessionRequest) error {
	return forms.ErrUnavailable
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
func (a *Forms) writeUnavailableGroup(ctx context.Context, group string) error {
	if err := a.requireGroup(ctx, group, core.AccessLevelWrite); err != nil {
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

func demoPage() website.Page {
	return website.Page{ID: syntheticPageID, Name: "Synthetic demo",
		LatestSnapshotID: syntheticSnapshotID, LatestSnapshotState: "ready", CreatedAt: demoTime}
}

func demoSnapshot() website.Snapshot {
	return website.Snapshot{ID: syntheticSnapshotID, PageID: syntheticPageID, Name: "Synthetic demo", Sequence: 1, Revision: 1,
		ContentRevision: 1, CreatedAt: demoTime, FeaturePins: map[string]string{syntheticFeatureID: syntheticVersion}, State: "ready",
		Payload: &website.SnapshotPayload{Config: json.RawMessage(`{"format_version":2,"page":{"id":"7d4e83e2-bb0e-40d8-a8be-2d197e95a003","name":"Synthetic demo","placements":[]}}`), Metadata: json.RawMessage(`{"synthetic":true}`)}}
}

func demoDataModel() json.RawMessage {
	return json.RawMessage(`{"formatVersion":1,"entities":[{"id":"7d4e83e2-bb0e-40d8-a8be-2d197e95a008","key":"announcement","name":"Announcement","cardinality":"many","fields":[{"id":"7d4e83e2-bb0e-40d8-a8be-2d197e95a009","key":"title","name":"Title","type":"text"}]}]}`)
}

func demoContentModel() website.ContentModel {
	model := website.ContentModel{Version: website.ContentProjectionVersion, ModelRevision: 1, DataModel: demoDataModel()}
	model.Digest, _ = model.ComputeDigest()
	return model
}

func demoPagePlacementProjection() website.PagePlacementProjection {
	projection := website.PagePlacementProjection{Version: website.ContentProjectionVersion, PageID: syntheticPageID,
		PageSnapshotID: syntheticSnapshotID, Revision: 1, State: "ready", Placements: []website.PagePlacement{}}
	projection.Digest, _ = projection.ComputeDigest()
	return projection
}

func validContentProjections(projections content.Projections) bool {
	return projections.VerifyScope(syntheticSnapshotID) && projections.Page.PageID == syntheticPageID
}

func demoWorkspace(projections content.Projections) content.Workspace {
	return content.Workspace{SnapshotID: projections.Page.PageSnapshotID,
		PageSnapshotID: projections.Page.PageSnapshotID, ModelRevision: projections.ContentModel.ModelRevision, ModelDigest: projections.ContentModel.Digest,
		PageRevision: projections.Page.Revision, PageDigest: projections.Page.Digest, DataModel: projections.ContentModel.DataModel,
		Revision: 1, SelectionRevision: 1, PublishedRevision: 1,
		Records:    []content.Record{{ID: syntheticRecordID, EntityID: "7d4e83e2-bb0e-40d8-a8be-2d197e95a008", SortOrder: 0, Values: map[string]any{"title": "Synthetic content record"}}},
		Selections: []content.Selection{}, Placements: []content.Placement{}}
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
