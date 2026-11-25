package contracts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrAdminWebsiteUnauthorized = errors.New("unauthorized")
	ErrAdminWebsiteForbidden    = errors.New("forbidden")
	ErrAdminWebsiteNotFound     = errors.New("not found")
	ErrAdminWebsiteConflict     = errors.New("conflict")
	ErrAdminWebsiteInvalid      = errors.New("invalid request")
	ErrAdminWebsiteTooLarge     = errors.New("request too large")
	ErrAdminWebsiteUnavailable  = errors.New("Website dependency unavailable")
)

type AdminWebsiteImportFeatureRequest struct {
	SourceFileID   string `json:"source_file_id"`
	IdempotencyKey string `json:"-"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}

type AdminWebsiteFeatureImport struct {
	ID            string    `json:"id"`
	SourceFileID  string    `json:"source_file_id"`
	FeatureID     string    `json:"feature_id,omitempty"`
	Version       string    `json:"version,omitempty"`
	FileSetID     string    `json:"file_set_id,omitempty"`
	State         string    `json:"state"`
	ErrorCode     string    `json:"error_code,omitempty"`
	SourceDeleted bool      `json:"source_deleted"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type AdminWebsiteFeatureVersion struct {
	FeatureID    string            `json:"feature_id"`
	Version      string            `json:"version"`
	Manifest     []byte            `json:"manifest"`
	FileSetID    string            `json:"file_set_id"`
	Files        map[string]string `json:"files"`
	Digest       string            `json:"digest"`
	ArtifactSize int64             `json:"artifact_size"`
	CreatedAt    time.Time         `json:"created_at"`
	State        string            `json:"state"`
}

type AdminWebsiteFeatureVersionPage struct {
	Items      []AdminWebsiteFeatureVersion `json:"items"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

type AdminWebsiteSnapshot struct {
	ID              string                       `json:"id"`
	PageID          string                       `json:"page_id"`
	ParentID        string                       `json:"parent_id,omitempty"`
	ParentName      string                       `json:"parent_name,omitempty"`
	ParentSequence  int64                        `json:"parent_sequence,omitempty"`
	Name            string                       `json:"name"`
	Sequence        int64                        `json:"sequence"`
	Revision        int64                        `json:"revision"`
	ContentRevision int64                        `json:"content_revision,omitempty"`
	Digest          string                       `json:"digest,omitempty"`
	CreatedAt       time.Time                    `json:"created_at,omitempty"`
	FeaturePins     map[string]string            `json:"feature_pins"`
	State           string                       `json:"state"`
	Payload         *AdminWebsiteSnapshotPayload `json:"payload,omitempty"`
}

type AdminWebsiteSnapshotPayload struct {
	Config   json.RawMessage `json:"config"`
	Metadata json.RawMessage `json:"metadata"`
}

type AdminWebsiteContentModel struct {
	Version       int             `json:"version"`
	ModelRevision int64           `json:"model_revision"`
	Digest        string          `json:"digest"`
	DataModel     json.RawMessage `json:"data_model"`
}

func (schema AdminWebsiteContentModel) ComputeDigest() (string, error) {
	payload, err := json.Marshal(struct {
		Version       int             `json:"version"`
		ModelRevision int64           `json:"model_revision"`
		DataModel     json.RawMessage `json:"data_model"`
	}{schema.Version, schema.ModelRevision, schema.DataModel})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (schema AdminWebsiteContentModel) VerifyDigest() bool {
	if schema.Version != 1 || schema.ModelRevision < 1 || schema.DataModel == nil || len(schema.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := schema.ComputeDigest()
	return err == nil && strings.EqualFold(digest, schema.Digest)
}

type AdminWebsiteSaveContentModelRequest struct {
	ExpectedRevision int64           `json:"expected_revision"`
	DataModel        json.RawMessage `json:"data_model"`
}

type AdminWebsitePagePlacementProjection struct {
	Version        int                         `json:"version"`
	PageID         string                      `json:"page_id"`
	PageSnapshotID string                      `json:"page_snapshot_id"`
	Revision       int64                       `json:"revision"`
	Digest         string                      `json:"digest"`
	State          string                      `json:"state"`
	Placements     []AdminWebsitePagePlacement `json:"placements"`
}

type AdminWebsitePagePlacement struct {
	ID          string                         `json:"id"`
	PageID      string                         `json:"page_id"`
	PageName    string                         `json:"page_name"`
	FeatureKey  string                         `json:"feature_key"`
	FeatureName string                         `json:"feature_name"`
	Version     string                         `json:"version"`
	Bindings    []AdminWebsitePlacementBinding `json:"bindings"`
}

type AdminWebsitePlacementBinding struct {
	InputKey  string                            `json:"input_key"`
	EntityID  string                            `json:"entity_id"`
	FieldID   string                            `json:"field_id,omitempty"`
	Fields    []AdminWebsitePlacementField      `json:"fields,omitempty"`
	Lookup    *AdminWebsitePlacementInputLookup `json:"lookup,omitempty"`
	DetailSEO *AdminWebsitePlacementDetailSEO   `json:"detail_seo,omitempty"`
}

type AdminWebsitePlacementField struct {
	InputPath string `json:"input_path"`
	FieldID   string `json:"field_id"`
}

type AdminWebsitePlacementInputLookup struct {
	Argument string `json:"argument"`
	FieldID  string `json:"field_id"`
}

type AdminWebsitePlacementDetailSEO struct {
	TitleFieldID         string `json:"title_field_id,omitempty"`
	DescriptionFieldID   string `json:"description_field_id,omitempty"`
	OGTitleFieldID       string `json:"og_title_field_id,omitempty"`
	OGDescriptionFieldID string `json:"og_description_field_id,omitempty"`
	OGImageFieldID       string `json:"og_image_field_id,omitempty"`
}

func (projection AdminWebsitePagePlacementProjection) ComputeDigest() (string, error) {
	payload, err := json.Marshal(struct {
		Version        int                         `json:"version"`
		PageID         string                      `json:"page_id"`
		PageSnapshotID string                      `json:"page_snapshot_id"`
		Revision       int64                       `json:"revision"`
		State          string                      `json:"state"`
		Placements     []AdminWebsitePagePlacement `json:"placements"`
	}{projection.Version, projection.PageID, projection.PageSnapshotID, projection.Revision, projection.State, projection.Placements})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (projection AdminWebsitePagePlacementProjection) VerifyDigest() bool {
	if projection.Version != 1 || projection.PageID == "" || projection.PageSnapshotID == "" || projection.Revision < 1 || projection.Placements == nil || len(projection.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := projection.ComputeDigest()
	return err == nil && strings.EqualFold(digest, projection.Digest)
}

type AdminWebsiteSnapshotPage struct {
	Items      []AdminWebsiteSnapshot `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type AdminWebsitePage struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Revision            int64     `json:"revision"`
	LifecycleState      string    `json:"lifecycle_state"`
	RetirementJobID     string    `json:"retirement_job_id,omitempty"`
	LatestSnapshotID    string    `json:"latest_snapshot_id"`
	LatestSnapshotState string    `json:"latest_snapshot_state"`
	CreatedAt           time.Time `json:"created_at"`
}

type AdminWebsitePagePage struct {
	Items      []AdminWebsitePage `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type AdminWebsiteCreatePageRequest struct {
	Name             string `json:"name"`
	TemplateID       string `json:"template_id,omitempty"`
	TemplateRevision int64  `json:"template_revision,omitempty"`
	IdempotencyKey   string `json:"-"`
}

type AdminWebsitePageTemplateConfiguration struct {
	SchemaVersion  int                         `json:"schema_version"`
	Payload        AdminWebsiteSnapshotPayload `json:"payload"`
	FeaturePins    map[string]string           `json:"feature_pins"`
	PlacementSlots []AdminWebsitePlacementSlot `json:"placement_slots,omitempty"`
}

type AdminWebsitePlacementSlot struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type AdminWebsitePageTemplate struct {
	ID            string                                `json:"id"`
	Name          string                                `json:"name"`
	Description   string                                `json:"description"`
	Revision      int64                                 `json:"revision"`
	Digest        string                                `json:"digest"`
	Configuration AdminWebsitePageTemplateConfiguration `json:"configuration"`
	UpdatedAt     time.Time                             `json:"updated_at"`
}

type AdminWebsitePageTemplatePage struct {
	Items      []AdminWebsitePageTemplate `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type AdminWebsiteCreatePageTemplateRequest struct {
	Name           string                                `json:"name"`
	Description    string                                `json:"description"`
	Configuration  AdminWebsitePageTemplateConfiguration `json:"configuration"`
	IdempotencyKey string                                `json:"-"`
}

type AdminWebsiteUpdatePageTemplateRequest struct {
	TemplateID       string                                `json:"-"`
	ExpectedRevision int64                                 `json:"-"`
	Name             string                                `json:"name"`
	Description      string                                `json:"description"`
	Configuration    AdminWebsitePageTemplateConfiguration `json:"configuration"`
}

type AdminWebsiteDeletePageTemplateRequest struct {
	TemplateID       string `json:"-"`
	ExpectedRevision int64  `json:"-"`
}

type AdminWebsiteApplyPageTemplateRequest struct {
	PageID                   string `json:"-"`
	TemplateID               string `json:"template_id"`
	TemplateRevision         int64  `json:"template_revision"`
	ExpectedSnapshotID       string `json:"expected_snapshot_id"`
	ExpectedSnapshotRevision int64  `json:"expected_snapshot_revision"`
	ExpectedContentRevision  int64  `json:"expected_content_revision"`
	Confirmed                bool   `json:"confirmed"`
	IdempotencyKey           string `json:"-"`
}

type AdminWebsiteCreateSnapshotRequest struct {
	PageID                  string                      `json:"page_id"`
	ParentID                string                      `json:"parent_id,omitempty"`
	Name                    string                      `json:"name"`
	ExpectedRevision        int64                       `json:"expected_revision"`
	ExpectedContentRevision int64                       `json:"expected_content_revision"`
	IdempotencyKey          string                      `json:"-"`
	Payload                 AdminWebsiteSnapshotPayload `json:"payload"`
}

type AdminWebsiteSnapshotForkWorkflowPayload struct {
	SourceSnapshotID              string `json:"source_snapshot_id"`
	TargetSnapshotID              string `json:"target_snapshot_id"`
	ExpectedSourceContentRevision int64  `json:"expected_source_content_revision"`
	TargetSnapshotRevision        int64  `json:"target_snapshot_revision"`
	ContentCopyIdempotencyKey     string `json:"content_copy_idempotency_key"`
	SelectionPolicy               string `json:"selection_policy,omitempty"`
}

type AdminWebsiteDeleteSnapshotRequest struct {
	SnapshotID       string `json:"-"`
	ExpectedRevision int64  `json:"-"`
}

type AdminWebsiteBuildSnapshotRequest struct {
	SnapshotID     string `json:"snapshot_id"`
	Locale         string `json:"locale"`
	IdempotencyKey string `json:"-"`
}

type AdminWebsiteBuildJob struct {
	ID                     string    `json:"id"`
	SnapshotID             string    `json:"snapshot_id"`
	Locale                 string    `json:"locale"`
	LocaleCatalogRevision  int64     `json:"locale_catalog_revision"`
	InputDigest            string    `json:"input_digest"`
	State                  string    `json:"state"`
	ArtifactManifestFileID string    `json:"artifact_manifest_file_id,omitempty"`
	ErrorCode              string    `json:"error_code,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type AdminWebsiteCreateDerivativeRequest struct {
	SnapshotID     string `json:"snapshot_id"`
	PlacementID    string `json:"placement_id"`
	SchemaKind     string `json:"schema_kind"`
	FieldPath      string `json:"field_path"`
	SourceFileID   string `json:"source_file_id"`
	IdempotencyKey string `json:"-"`
}

type AdminWebsiteMediaJob struct {
	ID               string            `json:"id"`
	SourceFileID     string            `json:"source_file_id"`
	ProfileDigest    string            `json:"profile_digest"`
	ProcessorVersion string            `json:"processor_version"`
	State            string            `json:"state"`
	Outputs          map[string]string `json:"outputs,omitempty"`
	ErrorCode        string            `json:"error_code,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type AdminWebsiteAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	BeginFeatureImport(context.Context, AdminWebsiteImportFeatureRequest) (AdminWebsiteFeatureImport, error)
	GetFeatureImport(context.Context, string) (AdminWebsiteFeatureImport, error)
	ListFeatureCatalog(context.Context, string, int) (AdminWebsiteFeatureVersionPage, error)
	ListFeatureVersions(context.Context, string, string, int) (AdminWebsiteFeatureVersionPage, error)
	GetFeatureVersion(context.Context, string, string) (AdminWebsiteFeatureVersion, error)
	ListPages(context.Context, string, int) (AdminWebsitePagePage, error)
	GetPage(context.Context, string) (AdminWebsitePage, error)
	CreatePage(context.Context, AdminWebsiteCreatePageRequest) (AdminWebsitePage, error)
	ListPageTemplates(context.Context, string, int) (AdminWebsitePageTemplatePage, error)
	GetPageTemplate(context.Context, string) (AdminWebsitePageTemplate, error)
	CreatePageTemplate(context.Context, AdminWebsiteCreatePageTemplateRequest) (AdminWebsitePageTemplate, error)
	UpdatePageTemplate(context.Context, AdminWebsiteUpdatePageTemplateRequest) (AdminWebsitePageTemplate, error)
	DeletePageTemplate(context.Context, AdminWebsiteDeletePageTemplateRequest) error
	ApplyPageTemplate(context.Context, AdminWebsiteApplyPageTemplateRequest) (AdminWebsiteSnapshot, error)
	GetContentModel(context.Context) (AdminWebsiteContentModel, error)
	SaveContentModel(context.Context, AdminWebsiteSaveContentModelRequest) (AdminWebsiteContentModel, error)
	GetPagePlacementProjection(context.Context, string, string) (AdminWebsitePagePlacementProjection, error)
	GetSnapshot(context.Context, string) (AdminWebsiteSnapshot, error)
	ListSnapshots(context.Context, string, string, int) (AdminWebsiteSnapshotPage, error)
	CreateSnapshot(context.Context, AdminWebsiteCreateSnapshotRequest) (AdminWebsiteSnapshot, error)
	DeleteSnapshot(context.Context, AdminWebsiteDeleteSnapshotRequest) error
	BuildSnapshot(context.Context, AdminWebsiteBuildSnapshotRequest) (AdminWebsiteBuildJob, error)
	GetBuild(context.Context, string) (AdminWebsiteBuildJob, error)
	CreateMediaDerivative(context.Context, AdminWebsiteCreateDerivativeRequest) (AdminWebsiteMediaJob, error)
	GetMediaJob(context.Context, string) (AdminWebsiteMediaJob, error)
}

type AdminWebsiteSubjectRequest struct {
	Subject AuthorizationSubject `json:"subject"`
}

type AdminWebsiteIDRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	ID      string               `json:"id"`
}

type AdminWebsiteCursorRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	Cursor  string               `json:"cursor"`
	Limit   int                  `json:"limit"`
}

type AdminWebsiteVersionListRequest struct {
	Subject   AuthorizationSubject `json:"subject"`
	FeatureID string               `json:"feature_id"`
	Cursor    string               `json:"cursor"`
	Limit     int                  `json:"limit"`
}

type AdminWebsiteVersionRequest struct {
	Subject   AuthorizationSubject `json:"subject"`
	FeatureID string               `json:"feature_id"`
	Version   string               `json:"version"`
}

type AdminWebsitePlacementRequest struct {
	Subject    AuthorizationSubject `json:"subject"`
	PageID     string               `json:"page_id"`
	SnapshotID string               `json:"snapshot_id"`
}

type AdminWebsiteSnapshotListRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	PageID  string               `json:"page_id"`
	Cursor  string               `json:"cursor"`
	Limit   int                  `json:"limit"`
}

type AdminWebsiteFeatureImportResponse struct {
	Job AdminWebsiteFeatureImport `json:"job"`
}

type AdminWebsiteFeatureVersionPageResponse struct {
	Page AdminWebsiteFeatureVersionPage `json:"page"`
}

type AdminWebsiteFeatureVersionResponse struct {
	Version AdminWebsiteFeatureVersion `json:"version"`
}

type AdminWebsitePageResponse struct {
	Page AdminWebsitePage `json:"page"`
}

type AdminWebsitePagePageResponse struct {
	Page AdminWebsitePagePage `json:"page"`
}

type AdminWebsiteTemplateResponse struct {
	Template AdminWebsitePageTemplate `json:"template"`
}

type AdminWebsiteTemplatePageResponse struct {
	Page AdminWebsitePageTemplatePage `json:"page"`
}

type AdminWebsiteSnapshotResponse struct {
	Snapshot AdminWebsiteSnapshot `json:"snapshot"`
}

type AdminWebsiteSnapshotPageResponse struct {
	Page AdminWebsiteSnapshotPage `json:"page"`
}

type AdminWebsiteContentModelResponse struct {
	Model AdminWebsiteContentModel `json:"model"`
}

type AdminWebsitePlacementResponse struct {
	Projection AdminWebsitePagePlacementProjection `json:"projection"`
}

type AdminWebsiteBuildResponse struct {
	Job AdminWebsiteBuildJob `json:"job"`
}

type AdminWebsiteMediaResponse struct {
	Job AdminWebsiteMediaJob `json:"job"`
}

type AdminWebsiteMessageRequest struct {
	Subject          AuthorizationSubject `json:"subject"`
	Request          json.RawMessage      `json:"request"`
	IdempotencyKey   string               `json:"idempotency_key,omitempty"`
	PathID           string               `json:"path_id,omitempty"`
	PageID           string               `json:"page_id,omitempty"`
	ExpectedRevision int64                `json:"expected_revision,omitempty"`
}

func AdminWebsiteAuthenticateOperation() Operation[AdminAuthenticateRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateRequest, AdminAuthenticateResponse]("admin.website.authenticate", func(request AdminAuthenticateRequest) error {
		if request.Credential == "" {
			return ErrAdminWebsiteInvalid
		}
		return nil
	})
}

func AdminWebsiteAuthenticateSessionOperation() Operation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse]("admin.website.authenticate-session", func(request AdminAuthenticateSessionRequest) error {
		if request.Token == "" {
			return ErrAdminWebsiteInvalid
		}
		return nil
	})
}

func adminWebsiteValidID(request AdminWebsiteIDRequest) error {
	if validAdminSubject(request.Subject) != nil || request.ID == "" {
		return ErrAdminWebsiteInvalid
	}
	return nil
}

func adminWebsiteValidCursor(request AdminWebsiteCursorRequest) error {
	if validAdminSubject(request.Subject) != nil || request.Limit < 1 {
		return ErrAdminWebsiteInvalid
	}
	return nil
}

func AdminWebsiteBeginFeatureImportOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteFeatureImportResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteFeatureImportResponse]("admin.website.feature-import.begin", func(request AdminWebsiteMessageRequest) error {
		if validAdminSubject(request.Subject) != nil || len(request.Request) == 0 {
			return ErrAdminWebsiteInvalid
		}
		var typed AdminWebsiteImportFeatureRequest
		return decodeAdminJSON(request.Request, &typed)
	})
}

func AdminWebsiteGetFeatureImportOperation() Operation[AdminWebsiteIDRequest, AdminWebsiteFeatureImportResponse] {
	return adminOperation[AdminWebsiteIDRequest, AdminWebsiteFeatureImportResponse]("admin.website.feature-import.get", adminWebsiteValidID)
}

func AdminWebsiteListFeatureCatalogOperation() Operation[AdminWebsiteCursorRequest, AdminWebsiteFeatureVersionPageResponse] {
	return adminOperation[AdminWebsiteCursorRequest, AdminWebsiteFeatureVersionPageResponse]("admin.website.catalog.list", adminWebsiteValidCursor)
}

func AdminWebsiteListFeatureVersionsOperation() Operation[AdminWebsiteVersionListRequest, AdminWebsiteFeatureVersionPageResponse] {
	return adminOperation[AdminWebsiteVersionListRequest, AdminWebsiteFeatureVersionPageResponse]("admin.website.versions.list", func(request AdminWebsiteVersionListRequest) error {
		if validAdminSubject(request.Subject) != nil || request.FeatureID == "" || request.Limit < 1 {
			return ErrAdminWebsiteInvalid
		}
		return nil
	})
}

func AdminWebsiteGetFeatureVersionOperation() Operation[AdminWebsiteVersionRequest, AdminWebsiteFeatureVersionResponse] {
	return adminOperation[AdminWebsiteVersionRequest, AdminWebsiteFeatureVersionResponse]("admin.website.version.get", func(request AdminWebsiteVersionRequest) error {
		if validAdminSubject(request.Subject) != nil || request.FeatureID == "" || request.Version == "" {
			return ErrAdminWebsiteInvalid
		}
		return nil
	})
}

func AdminWebsiteListPagesOperation() Operation[AdminWebsiteCursorRequest, AdminWebsitePagePageResponse] {
	return adminOperation[AdminWebsiteCursorRequest, AdminWebsitePagePageResponse]("admin.website.pages.list", adminWebsiteValidCursor)
}

func AdminWebsiteGetPageOperation() Operation[AdminWebsiteIDRequest, AdminWebsitePageResponse] {
	return adminOperation[AdminWebsiteIDRequest, AdminWebsitePageResponse]("admin.website.page.get", adminWebsiteValidID)
}

func AdminWebsiteCreatePageOperation() Operation[AdminWebsiteMessageRequest, AdminWebsitePageResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsitePageResponse]("admin.website.page.create", validAdminWebsiteMessage)
}

func AdminWebsiteListPageTemplatesOperation() Operation[AdminWebsiteCursorRequest, AdminWebsiteTemplatePageResponse] {
	return adminOperation[AdminWebsiteCursorRequest, AdminWebsiteTemplatePageResponse]("admin.website.templates.list", adminWebsiteValidCursor)
}

func AdminWebsiteGetPageTemplateOperation() Operation[AdminWebsiteIDRequest, AdminWebsiteTemplateResponse] {
	return adminOperation[AdminWebsiteIDRequest, AdminWebsiteTemplateResponse]("admin.website.template.get", adminWebsiteValidID)
}

func AdminWebsiteCreatePageTemplateOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteTemplateResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteTemplateResponse]("admin.website.template.create", validAdminWebsiteMessage)
}

func AdminWebsiteUpdatePageTemplateOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteTemplateResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteTemplateResponse]("admin.website.template.update", validAdminWebsiteMessage)
}

func AdminWebsiteDeletePageTemplateOperation() Operation[AdminWebsiteMessageRequest, AdminEmptyResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminEmptyResponse]("admin.website.template.delete", validAdminWebsiteMessage)
}

func AdminWebsiteApplyPageTemplateOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteSnapshotResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteSnapshotResponse]("admin.website.template.apply", validAdminWebsiteMessage)
}

func AdminWebsiteGetContentModelOperation() Operation[AdminWebsiteSubjectRequest, AdminWebsiteContentModelResponse] {
	return adminOperation[AdminWebsiteSubjectRequest, AdminWebsiteContentModelResponse]("admin.website.model.get", func(request AdminWebsiteSubjectRequest) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminWebsiteInvalid
		}
		return nil
	})
}

func AdminWebsiteSaveContentModelOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteContentModelResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteContentModelResponse]("admin.website.model.save", validAdminWebsiteMessage)
}

func AdminWebsiteGetPagePlacementProjectionOperation() Operation[AdminWebsitePlacementRequest, AdminWebsitePlacementResponse] {
	return adminOperation[AdminWebsitePlacementRequest, AdminWebsitePlacementResponse]("admin.website.placement.get", func(request AdminWebsitePlacementRequest) error {
		if validAdminSubject(request.Subject) != nil || request.PageID == "" || request.SnapshotID == "" {
			return ErrAdminWebsiteInvalid
		}
		return nil
	})
}

func AdminWebsiteGetSnapshotOperation() Operation[AdminWebsiteIDRequest, AdminWebsiteSnapshotResponse] {
	return adminOperation[AdminWebsiteIDRequest, AdminWebsiteSnapshotResponse]("admin.website.snapshot.get", adminWebsiteValidID)
}

func AdminWebsiteListSnapshotsOperation() Operation[AdminWebsiteSnapshotListRequest, AdminWebsiteSnapshotPageResponse] {
	return adminOperation[AdminWebsiteSnapshotListRequest, AdminWebsiteSnapshotPageResponse]("admin.website.snapshots.list", func(request AdminWebsiteSnapshotListRequest) error {
		if validAdminSubject(request.Subject) != nil || request.Limit < 1 {
			return ErrAdminWebsiteInvalid
		}
		return nil
	})
}

func AdminWebsiteCreateSnapshotOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteSnapshotResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteSnapshotResponse]("admin.website.snapshot.create", validAdminWebsiteMessage)
}

func AdminWebsiteDeleteSnapshotOperation() Operation[AdminWebsiteMessageRequest, AdminEmptyResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminEmptyResponse]("admin.website.snapshot.delete", validAdminWebsiteMessage)
}

func AdminWebsiteBuildSnapshotOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteBuildResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteBuildResponse]("admin.website.build.start", validAdminWebsiteMessage)
}

func AdminWebsiteGetBuildOperation() Operation[AdminWebsiteIDRequest, AdminWebsiteBuildResponse] {
	return adminOperation[AdminWebsiteIDRequest, AdminWebsiteBuildResponse]("admin.website.build.get", adminWebsiteValidID)
}

func AdminWebsiteCreateMediaDerivativeOperation() Operation[AdminWebsiteMessageRequest, AdminWebsiteMediaResponse] {
	return adminOperation[AdminWebsiteMessageRequest, AdminWebsiteMediaResponse]("admin.website.media.derivative", validAdminWebsiteMessage)
}

func AdminWebsiteGetMediaJobOperation() Operation[AdminWebsiteIDRequest, AdminWebsiteMediaResponse] {
	return adminOperation[AdminWebsiteIDRequest, AdminWebsiteMediaResponse]("admin.website.media.get", adminWebsiteValidID)
}

func validAdminWebsiteMessage(request AdminWebsiteMessageRequest) error {
	if validAdminSubject(request.Subject) != nil || len(request.Request) == 0 {
		return ErrAdminWebsiteInvalid
	}
	return nil
}

func AdminWebsiteOperationKeys() []Key {
	return []Key{
		AdminWebsiteAuthenticateOperation().Key,
		AdminWebsiteAuthenticateSessionOperation().Key,
		AdminWebsiteBeginFeatureImportOperation().Key,
		AdminWebsiteGetFeatureImportOperation().Key,
		AdminWebsiteListFeatureCatalogOperation().Key,
		AdminWebsiteListFeatureVersionsOperation().Key,
		AdminWebsiteGetFeatureVersionOperation().Key,
		AdminWebsiteListPagesOperation().Key,
		AdminWebsiteGetPageOperation().Key,
		AdminWebsiteCreatePageOperation().Key,
		AdminWebsiteListPageTemplatesOperation().Key,
		AdminWebsiteGetPageTemplateOperation().Key,
		AdminWebsiteCreatePageTemplateOperation().Key,
		AdminWebsiteUpdatePageTemplateOperation().Key,
		AdminWebsiteDeletePageTemplateOperation().Key,
		AdminWebsiteApplyPageTemplateOperation().Key,
		AdminWebsiteGetContentModelOperation().Key,
		AdminWebsiteSaveContentModelOperation().Key,
		AdminWebsiteGetPagePlacementProjectionOperation().Key,
		AdminWebsiteGetSnapshotOperation().Key,
		AdminWebsiteListSnapshotsOperation().Key,
		AdminWebsiteCreateSnapshotOperation().Key,
		AdminWebsiteDeleteSnapshotOperation().Key,
		AdminWebsiteBuildSnapshotOperation().Key,
		AdminWebsiteGetBuildOperation().Key,
		AdminWebsiteCreateMediaDerivativeOperation().Key,
		AdminWebsiteGetMediaJobOperation().Key,
	}
}
