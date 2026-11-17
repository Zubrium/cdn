// Package presentation is Website's aggregate typed Go facade. HTTP and worker
// adapters depend on these contracts; features keep their own domain models.
package presentation

import (
	"context"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
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
	FeatureCatalogAPI
	SnapshotAPI
	BuildAPI
	MediaAPI
}

type FeatureCatalogAPI interface {
	BeginFeatureImport(context.Context, ImportFeatureRequest) (FeatureImport, error)
	GetFeatureImport(context.Context, string) (FeatureImport, error)
	ListFeatureVersions(context.Context, string, string, int) (FeatureVersionPage, error)
	GetFeatureVersion(context.Context, string, string) (FeatureVersion, error)
}

type SnapshotAPI interface {
	GetSnapshot(context.Context, string) (Snapshot, error)
	CreateSnapshot(context.Context, CreateSnapshotRequest) (Snapshot, error)
	PinFeatureVersion(context.Context, PinFeatureVersionRequest) (Snapshot, error)
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
	SourceFileID     string `json:"source_file_id"`
	IdempotencyKey   string `json:"-"`
	ExpectedSHA256   string `json:"expected_sha256,omitempty"`
}

type FeatureImport struct {
	ID             string    `json:"id"`
	SourceFileID   string    `json:"source_file_id"`
	FeatureID      string    `json:"feature_id,omitempty"`
	Version        string    `json:"version,omitempty"`
	FileSetID      string    `json:"file_set_id,omitempty"`
	State          string    `json:"state"`
	ErrorCode      string    `json:"error_code,omitempty"`
	SourceDeleted  bool      `json:"source_deleted"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FeatureVersion struct {
	FeatureID string            `json:"feature_id"`
	Version   string            `json:"version"`
	Manifest  []byte            `json:"manifest"`
	FileSetID string            `json:"file_set_id"`
	Files     map[string]string `json:"files"`
	State     string            `json:"state"`
}

type FeatureVersionPage struct {
	Items      []FeatureVersion `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type Snapshot struct {
	ID          string            `json:"id"`
	SiteID      string            `json:"site_id"`
	ParentID    string            `json:"parent_id,omitempty"`
	Revision    int64             `json:"revision"`
	FeaturePins map[string]string `json:"feature_pins"`
	State       string            `json:"state"`
}

type CreateSnapshotRequest struct {
	SiteID             string `json:"site_id"`
	ParentID           string `json:"parent_id,omitempty"`
	ExpectedRevision   int64  `json:"expected_revision"`
}

type PinFeatureVersionRequest struct {
	SnapshotID       string `json:"snapshot_id"`
	FeatureID        string `json:"feature_id"`
	Version          string `json:"version"`
	ExpectedRevision int64  `json:"expected_revision"`
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
	SourceFileID    string `json:"source_file_id"`
	FeatureID       string `json:"feature_id"`
	Profile         string `json:"profile"`
	IdempotencyKey  string `json:"-"`
}

type MediaJob struct {
	ID              string    `json:"id"`
	SourceFileID    string    `json:"source_file_id"`
	DerivativeID   string    `json:"derivative_file_id,omitempty"`
	Profile         string    `json:"profile"`
	State           string    `json:"state"`
	ErrorCode       string    `json:"error_code,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Descriptor struct{}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "feature-import.create", Action: access.Write, ResourceType: "feature-import"},
		{Name: "feature-import.read", Action: access.Read, ResourceType: "feature-import"},
		{Name: "feature-version.read", Action: access.Read, ResourceType: "feature-version"},
		{Name: "snapshot.read", Action: access.Read, ResourceType: "snapshot"},
		{Name: "snapshot.write", Action: access.Write, ResourceType: "snapshot"},
		{Name: "build.create", Action: access.Write, ResourceType: "build"},
		{Name: "build.read", Action: access.Read, ResourceType: "build"},
		{Name: "media-derivative.create", Action: access.Write, ResourceType: "media-derivative"},
		{Name: "media-job.read", Action: access.Read, ResourceType: "media-job"},
	}
}

var _ platform.Module = Descriptor{}
