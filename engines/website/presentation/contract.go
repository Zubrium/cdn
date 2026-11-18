// Package presentation is Website's aggregate typed Go facade. HTTP and worker
// adapters depend on these contracts; features keep their own domain models.
package presentation

import (
	"context"
	"encoding/json"
	"errors"
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
	SnapshotAPI
	BranchAPI
	BuildAPI
	MediaAPI
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
	GetSnapshotDesignSystem(context.Context, string) (SnapshotDesignSystem, error)
	CreateSnapshot(context.Context, CreateSnapshotRequest) (Snapshot, error)
	MarkSnapshotReady(context.Context, string, int64) (Snapshot, error)
	DeleteSnapshot(context.Context, DeleteSnapshotRequest) error
}

// BranchAPI exposes statically configured branch topology and its mutable
// snapshot assignment. Topology is never changed by these operations.
type BranchAPI interface {
	ListBranches(context.Context) (BranchPage, error)
	AssignBranch(context.Context, AssignBranchRequest) (Branch, error)
	ListBranchEvents(context.Context, string, string, int) (BranchEventPage, error)
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

type Snapshot struct {
	ID              string            `json:"id"`
	SiteID          string            `json:"site_id"`
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
	Config       json.RawMessage `json:"config"`
	Metadata     json.RawMessage `json:"metadata"`
	DesignSystem json.RawMessage `json:"design_system"`
}

type SnapshotDesignSystem struct {
	Snapshot      Snapshot        `json:"snapshot"`
	DesignSystem  json.RawMessage `json:"design_system"`
	FeatureTokens json.RawMessage `json:"feature_tokens"`
}

type SnapshotPage struct {
	Items      []Snapshot `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type Branch struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	SiteID        string    `json:"site_id"`
	ListenAddress string    `json:"listen_address"`
	CORSOrigins   []string  `json:"cors_origins"`
	Indexable     bool      `json:"indexable"`
	SnapshotID    *string   `json:"snapshot_id"`
	Revision      int64     `json:"revision"`
	Enabled       bool      `json:"enabled"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type BranchPage struct {
	Items []Branch `json:"items"`
}

type AssignBranchRequest struct {
	BranchID                 string `json:"-"`
	SnapshotID               string `json:"snapshot_id"`
	ExpectedRevision         int64  `json:"expected_revision"`
	ExpectedSnapshotRevision int64  `json:"expected_snapshot_revision"`
}

type BranchEvent struct {
	ID               string    `json:"id"`
	BranchID         string    `json:"branch_id"`
	SnapshotID       string    `json:"snapshot_id"`
	SnapshotName     string    `json:"snapshot_name"`
	SnapshotSequence int64     `json:"snapshot_sequence"`
	Action           string    `json:"action"`
	CreatedAt        time.Time `json:"created_at"`
}

type BranchEventPage struct {
	Items      []BranchEvent `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type CreateSnapshotRequest struct {
	SiteID                  string          `json:"site_id"`
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
}

type DeleteSnapshotRequest struct {
	SnapshotID       string `json:"-"`
	ExpectedRevision int64  `json:"-"`
}

type BuildSnapshotRequest struct {
	SnapshotID     string `json:"snapshot_id"`
	IdempotencyKey string `json:"-"`
}

type BuildJob struct {
	ID                     string    `json:"id"`
	SnapshotID             string    `json:"snapshot_id"`
	State                  string    `json:"state"`
	ArtifactManifestFileID string    `json:"artifact_manifest_file_id,omitempty"`
	ErrorCode              string    `json:"error_code,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type CreateDerivativeRequest struct {
	SourceFileID   string `json:"source_file_id"`
	FeatureID      string `json:"feature_id"`
	Profile        string `json:"profile"`
	IdempotencyKey string `json:"-"`
}

type MediaJob struct {
	ID           string    `json:"id"`
	SourceFileID string    `json:"source_file_id"`
	DerivativeID string    `json:"derivative_file_id,omitempty"`
	Profile      string    `json:"profile"`
	State        string    `json:"state"`
	ErrorCode    string    `json:"error_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.website", Kind: "engine", Version: 1}
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
		{Name: "branch.list", Action: platform.AccessActionRead, ResourceType: "branch"},
		{Name: "branch.assign", Action: platform.AccessActionWrite, ResourceType: "branch"},
		{Name: "branch.events", Action: platform.AccessActionRead, ResourceType: "branch-event"},
		{Name: "build.create", Action: platform.AccessActionWrite, ResourceType: "build"},
		{Name: "build.read", Action: platform.AccessActionRead, ResourceType: "build"},
		{Name: "media-derivative.create", Action: platform.AccessActionWrite, ResourceType: "media-derivative"},
		{Name: "media-job.read", Action: platform.AccessActionRead, ResourceType: "media-job"},
	}
}

var _ platform.Module = Descriptor{}
