// Package presentation is Website's aggregate typed Go facade. HTTP and worker
// adapters depend on these contracts; features keep their own domain models.
package presentation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrTooLarge     = errors.New("request too large")
	ErrUnavailable  = errors.New("Website dependency unavailable")
)

type Contract interface {
	platform.Module
	API
}

type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	FeatureCatalogAPI
	PageAPI
	SnapshotAPI
	ContentModelAPI
	ContentModelWriteAPI
	BuildAPI
	MediaAPI
}

// ContentModelAPI is Website's typed, Content-independent read facade.
// Outer Presentation maps these values into Content's projection types.
type PageAPI interface {
	ListPages(context.Context, string, int) (PagePage, error)
	GetPage(context.Context, string) (Page, error)
	CreatePage(context.Context, CreatePageRequest) (Page, error)
	ListPageTemplates(context.Context, string, int) (PageTemplatePage, error)
	GetPageTemplate(context.Context, string) (PageTemplate, error)
	CreatePageTemplate(context.Context, CreatePageTemplateRequest) (PageTemplate, error)
	UpdatePageTemplate(context.Context, UpdatePageTemplateRequest) (PageTemplate, error)
	DeletePageTemplate(context.Context, DeletePageTemplateRequest) error
	ApplyPageTemplate(context.Context, ApplyPageTemplateRequest) (Snapshot, error)
}

type ContentModelAPI interface {
	GetContentModel(context.Context) (ContentModel, error)
	GetPagePlacementProjection(context.Context, string, string) (PagePlacementProjection, error)
}

// ContentModelWriteAPI persists system model revisions separately from page snapshots.
type ContentModelWriteAPI interface {
	SaveContentModel(context.Context, SaveContentModelRequest) (ContentModel, error)
}

type FeatureCatalogAPI interface {
	BeginFeatureImport(context.Context, ImportFeatureRequest) (FeatureImport, error)
	GetFeatureImport(context.Context, string) (FeatureImport, error)
	ListFeatureCatalog(context.Context, string, int) (FeatureVersionPage, error)
	ListFeatureVersions(context.Context, string, string, int) (FeatureVersionPage, error)
	GetFeatureVersion(context.Context, string, string) (FeatureVersion, error)
}

type SnapshotAPI interface {
	ListSnapshots(context.Context, string, string, int) (SnapshotPage, error)
	GetSnapshot(context.Context, string) (Snapshot, error)
	CreateSnapshot(context.Context, CreateSnapshotRequest) (Snapshot, error)
	MarkSnapshotReady(context.Context, string, int64) (Snapshot, error)
	DeleteSnapshot(context.Context, DeleteSnapshotRequest) error
}

type BuildAPI interface {
	BuildSnapshot(context.Context, BuildSnapshotRequest) (BuildJob, error)
	GetBuild(context.Context, string) (BuildJob, error)
}

type MediaAPI interface {
	CreateMediaDerivative(context.Context, CreateDerivativeRequest) (MediaJob, error)
	GetMediaJob(context.Context, string) (MediaJob, error)
}

type ImportFeatureRequest struct {
	SourceFileID   string `json:"source_file_id"`
	IdempotencyKey string `json:"-"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}

type FeatureImport struct {
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

type FeatureVersion struct {
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

type FeatureVersionPage struct {
	Items      []FeatureVersion `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// Snapshot is one immutable page version. ParentID, when present, belongs to
// the same PageID tree.
type Snapshot struct {
	ID              string            `json:"id"`
	PageID          string            `json:"page_id"`
	ParentID        string            `json:"parent_id,omitempty"`
	ParentName      string            `json:"parent_name,omitempty"`
	ParentSequence  int64             `json:"parent_sequence,omitempty"`
	Name            string            `json:"name"`
	Sequence        int64             `json:"sequence"`
	Revision        int64             `json:"revision"`
	ContentRevision int64             `json:"content_revision,omitempty"`
	Digest          string            `json:"digest,omitempty"`
	CreatedAt       time.Time         `json:"created_at,omitempty"`
	FeaturePins     map[string]string `json:"feature_pins"`
	State           string            `json:"state"`
	Payload         *SnapshotPayload  `json:"payload,omitempty"`
}

// SnapshotPayload holds Website-owned immutable configuration. The HTTP
// adapter checks that each value is one bounded JSON object; Website validates
// the versioned domain schemas before it persists or publishes the payload.
type SnapshotPayload struct {
	Config   json.RawMessage `json:"config"`
	Metadata json.RawMessage `json:"metadata"`
}

const ContentProjectionVersion = 1

// ContentModel is Website's Content-independent system model projection.
type ContentModel struct {
	Version       int             `json:"version"`
	ModelRevision int64           `json:"model_revision"`
	Digest        string          `json:"digest"`
	DataModel     json.RawMessage `json:"data_model"`
}

type SaveContentModelRequest struct {
	ExpectedRevision int64           `json:"expected_revision"`
	DataModel        json.RawMessage `json:"data_model"`
}

// PagePlacementProjection carries the page snapshot identity and raw typed
// bindings. Outer Presentation joins bindings with the pinned feature manifest
// to produce Content's input-kind-aware projection without a Website->Content dependency.
type PagePlacementProjection struct {
	Version        int             `json:"version"`
	PageID         string          `json:"page_id"`
	PageSnapshotID string          `json:"page_snapshot_id"`
	Revision       int64           `json:"revision"`
	Digest         string          `json:"digest"`
	State          string          `json:"state"`
	Placements     []PagePlacement `json:"placements"`
}

type PagePlacement struct {
	ID          string             `json:"id"`
	PageID      string             `json:"page_id"`
	PageName    string             `json:"page_name"`
	FeatureKey  string             `json:"feature_key"`
	FeatureName string             `json:"feature_name"`
	Version     string             `json:"version"`
	Bindings    []PlacementBinding `json:"bindings"`
}

type PlacementBinding struct {
	InputKey  string                `json:"input_key"`
	EntityID  string                `json:"entity_id"`
	FieldID   string                `json:"field_id,omitempty"`
	Fields    []PlacementField      `json:"fields,omitempty"`
	Lookup    *PlacementInputLookup `json:"lookup,omitempty"`
	DetailSEO *PlacementDetailSEO   `json:"detail_seo,omitempty"`
}

type PlacementField struct {
	InputPath string `json:"input_path"`
	FieldID   string `json:"field_id"`
}

type PlacementInputLookup struct {
	Argument string `json:"argument"`
	FieldID  string `json:"field_id"`
}

type PlacementDetailSEO struct {
	TitleFieldID         string `json:"title_field_id,omitempty"`
	DescriptionFieldID   string `json:"description_field_id,omitempty"`
	OGTitleFieldID       string `json:"og_title_field_id,omitempty"`
	OGDescriptionFieldID string `json:"og_description_field_id,omitempty"`
	OGImageFieldID       string `json:"og_image_field_id,omitempty"`
}

func (schema ContentModel) ComputeDigest() (string, error) {
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

func (schema ContentModel) VerifyDigest() bool {
	if schema.Version != ContentProjectionVersion || schema.ModelRevision < 1 || schema.DataModel == nil || len(schema.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := schema.ComputeDigest()
	return err == nil && strings.EqualFold(digest, schema.Digest)
}

func (projection PagePlacementProjection) ComputeDigest() (string, error) {
	payload, err := json.Marshal(struct {
		Version        int             `json:"version"`
		PageID         string          `json:"page_id"`
		PageSnapshotID string          `json:"page_snapshot_id"`
		Revision       int64           `json:"revision"`
		State          string          `json:"state"`
		Placements     []PagePlacement `json:"placements"`
	}{projection.Version, projection.PageID, projection.PageSnapshotID, projection.Revision, projection.State, projection.Placements})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (projection PagePlacementProjection) VerifyDigest() bool {
	if projection.Version != ContentProjectionVersion || projection.PageID == "" || projection.PageSnapshotID == "" || projection.Revision < 1 || projection.Placements == nil || len(projection.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := projection.ComputeDigest()
	return err == nil && strings.EqualFold(digest, projection.Digest)
}

type SnapshotPage struct {
	Items      []Snapshot `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type Page struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	LatestSnapshotID    string    `json:"latest_snapshot_id"`
	LatestSnapshotState string    `json:"latest_snapshot_state"`
	CreatedAt           time.Time `json:"created_at"`
}

type PagePage struct {
	Items      []Page `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type CreatePageRequest struct {
	Name             string `json:"name"`
	TemplateID       string `json:"template_id,omitempty"`
	TemplateRevision int64  `json:"template_revision,omitempty"`
	IdempotencyKey   string `json:"-"`
}

type PageTemplateConfiguration struct {
	SchemaVersion  int               `json:"schema_version"`
	Payload        SnapshotPayload   `json:"payload"`
	FeaturePins    map[string]string `json:"feature_pins"`
	PlacementSlots []PlacementSlot   `json:"placement_slots,omitempty"`
}

type PlacementSlot struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type PageTemplate struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	Description   string                    `json:"description"`
	Revision      int64                     `json:"revision"`
	Digest        string                    `json:"digest"`
	Configuration PageTemplateConfiguration `json:"configuration"`
	UpdatedAt     time.Time                 `json:"updated_at"`
}

type PageTemplatePage struct {
	Items      []PageTemplate `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type CreatePageTemplateRequest struct {
	Name           string                    `json:"name"`
	Description    string                    `json:"description"`
	Configuration  PageTemplateConfiguration `json:"configuration"`
	IdempotencyKey string                    `json:"-"`
}

type UpdatePageTemplateRequest struct {
	TemplateID       string                    `json:"-"`
	ExpectedRevision int64                     `json:"-"`
	Name             string                    `json:"name"`
	Description      string                    `json:"description"`
	Configuration    PageTemplateConfiguration `json:"configuration"`
}

type DeletePageTemplateRequest struct {
	TemplateID       string `json:"-"`
	ExpectedRevision int64  `json:"-"`
}

type ApplyPageTemplateRequest struct {
	PageID                   string `json:"-"`
	TemplateID               string `json:"template_id"`
	TemplateRevision         int64  `json:"template_revision"`
	ExpectedSnapshotID       string `json:"expected_snapshot_id"`
	ExpectedSnapshotRevision int64  `json:"expected_snapshot_revision"`
	ExpectedContentRevision  int64  `json:"expected_content_revision"`
	Confirmed                bool   `json:"confirmed"`
	IdempotencyKey           string `json:"-"`
}

// CreateSnapshotRequest creates a root or child within one page tree.
type CreateSnapshotRequest struct {
	PageID                  string          `json:"page_id"`
	ParentID                string          `json:"parent_id,omitempty"`
	Name                    string          `json:"name"`
	ExpectedRevision        int64           `json:"expected_revision"`
	ExpectedContentRevision int64           `json:"expected_content_revision"`
	IdempotencyKey          string          `json:"-"`
	Payload                 SnapshotPayload `json:"payload"`
}

// SnapshotForkWorkflowPayload is the versioned input to Website's durable
// child-snapshot workflow. Content owns the source revision; Website owns the
// target lifecycle revision.
type SnapshotForkWorkflowPayload struct {
	SourceSnapshotID              string `json:"source_snapshot_id"`
	TargetSnapshotID              string `json:"target_snapshot_id"`
	ExpectedSourceContentRevision int64  `json:"expected_source_content_revision"`
	TargetSnapshotRevision        int64  `json:"target_snapshot_revision"`
	ContentCopyIdempotencyKey     string `json:"content_copy_idempotency_key"`
	SelectionPolicy               string `json:"selection_policy,omitempty"`
}

type DeleteSnapshotRequest struct {
	SnapshotID       string `json:"-"`
	ExpectedRevision int64  `json:"-"`
}

type BuildSnapshotRequest struct {
	SnapshotID     string `json:"snapshot_id"`
	Locale         string `json:"locale"`
	IdempotencyKey string `json:"-"`
}

type BuildJob struct {
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

// CreateDerivativeRequest is the strict OpenAPI request. A client can choose
// only the file and its pinned placement/field identity; the profile is always
// resolved by Website from the exact feature version in the snapshot.
type CreateDerivativeRequest struct {
	SnapshotID     string `json:"snapshot_id"`
	PlacementID    string `json:"placement_id"`
	FieldPath      string `json:"field_path"`
	SourceFileID   string `json:"source_file_id"`
	IdempotencyKey string `json:"-"`
}

type MediaJob struct {
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

// MediaSandboxState is the read-only result of the media runner's pre-flight
// proof. It is deliberately not part of a client request or MediaJob response.
type MediaSandboxState struct {
	Backend            string
	Image              string
	SeccompMode        int
	CapEff             string
	NoNewPrivs         bool
	CPUQuota           string
	MemoryLimitBytes   int64
	PIDsLimit          int64
	ReadOnlyRootFS     bool
	NetworkDenied      bool
	PrivateStaging0700 bool
	VerifiedAt         time.Time
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.website", Kind: "engine", Version: 1, AccessGroups: []string{"features"}}
}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "feature-import.create", Action: platform.AccessActionWrite, ResourceType: "feature-import"},
		{Name: "feature-import.read", Action: platform.AccessActionRead, ResourceType: "feature-import"},
		{Name: "feature-version.read", Action: platform.AccessActionRead, ResourceType: "feature-version"},
		{Name: "snapshot.read", Action: platform.AccessActionRead, ResourceType: "snapshot"},
		{Name: "snapshot.list", Action: platform.AccessActionRead, ResourceType: "snapshot"},
		{Name: "snapshot.write", Action: platform.AccessActionWrite, ResourceType: "snapshot"},
		{Name: "snapshot.delete", Action: platform.AccessActionDelete, ResourceType: "snapshot"},
		{Name: "content-model.read", Action: platform.AccessActionRead, ResourceType: "content-model"},
		{Name: "content-model.write", Action: platform.AccessActionWrite, ResourceType: "content-model"},
		{Name: "page-placement-projection.read", Action: platform.AccessActionRead, ResourceType: "page-placement-projection"},
		{Name: "build.create", Action: platform.AccessActionWrite, ResourceType: "build"},
		{Name: "build.read", Action: platform.AccessActionRead, ResourceType: "build"},
		{Name: "media-derivative.create", Action: platform.AccessActionWrite, ResourceType: "media-derivative"},
		{Name: "media-job.read", Action: platform.AccessActionRead, ResourceType: "media-job"},
	}
}

var _ platform.Module = Descriptor{}
