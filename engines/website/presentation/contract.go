// Package presentation is Website's aggregate typed Go facade. HTTP and worker
// adapters depend on these contracts; features keep their own domain models.
package presentation

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrUnauthorized = contracts.ErrAdminWebsiteUnauthorized
	ErrForbidden    = contracts.ErrAdminWebsiteForbidden
	ErrNotFound     = contracts.ErrAdminWebsiteNotFound
	ErrConflict     = contracts.ErrAdminWebsiteConflict
	ErrInvalid      = contracts.ErrAdminWebsiteInvalid
	ErrTooLarge     = contracts.ErrAdminWebsiteTooLarge
	ErrUnavailable  = contracts.ErrAdminWebsiteUnavailable
)

type Contract interface {
	API
}

const ContentProjectionVersion = 1

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
	RetirePage(context.Context, RetirePageRequest) (Page, error)
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

type ImportFeatureRequest = contracts.AdminWebsiteImportFeatureRequest
type FeatureImport = contracts.AdminWebsiteFeatureImport
type FeatureVersion = contracts.AdminWebsiteFeatureVersion
type FeatureVersionPage = contracts.AdminWebsiteFeatureVersionPage
type Snapshot = contracts.AdminWebsiteSnapshot
type SnapshotPayload = contracts.AdminWebsiteSnapshotPayload
type ContentModel = contracts.AdminWebsiteContentModel
type SaveContentModelRequest = contracts.AdminWebsiteSaveContentModelRequest
type PagePlacementProjection = contracts.AdminWebsitePagePlacementProjection
type PagePlacement = contracts.AdminWebsitePagePlacement
type PlacementBinding = contracts.AdminWebsitePlacementBinding
type PlacementField = contracts.AdminWebsitePlacementField
type PlacementInputLookup = contracts.AdminWebsitePlacementInputLookup
type PlacementDetailSEO = contracts.AdminWebsitePlacementDetailSEO
type SnapshotPage = contracts.AdminWebsiteSnapshotPage
type Page = contracts.AdminWebsitePage
type PagePage = contracts.AdminWebsitePagePage
type CreatePageRequest = contracts.AdminWebsiteCreatePageRequest
type PageTemplateConfiguration = contracts.AdminWebsitePageTemplateConfiguration
type PlacementSlot = contracts.AdminWebsitePlacementSlot
type PageTemplate = contracts.AdminWebsitePageTemplate
type PageTemplatePage = contracts.AdminWebsitePageTemplatePage
type CreatePageTemplateRequest = contracts.AdminWebsiteCreatePageTemplateRequest
type UpdatePageTemplateRequest = contracts.AdminWebsiteUpdatePageTemplateRequest
type DeletePageTemplateRequest = contracts.AdminWebsiteDeletePageTemplateRequest
type ApplyPageTemplateRequest = contracts.AdminWebsiteApplyPageTemplateRequest
type CreateSnapshotRequest = contracts.AdminWebsiteCreateSnapshotRequest
type SnapshotForkWorkflowPayload = contracts.AdminWebsiteSnapshotForkWorkflowPayload
type DeleteSnapshotRequest = contracts.AdminWebsiteDeleteSnapshotRequest
type BuildSnapshotRequest = contracts.AdminWebsiteBuildSnapshotRequest
type BuildJob = contracts.AdminWebsiteBuildJob
type CreateDerivativeRequest = contracts.AdminWebsiteCreateDerivativeRequest
type MediaJob = contracts.AdminWebsiteMediaJob

// RetirePageRequest advances only the Website-owned page lifecycle. The
// trusted outer composition owns route withdrawal and supplies the job
// identity; Website never selects or calls a Router route.
type RetirePageRequest struct {
	PageID           string `json:"page_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	LifecycleState   string `json:"lifecycle_state"`
	RetirementJobID  string `json:"retirement_job_id"`
	IdempotencyKey   string `json:"-"`
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

func (Descriptor) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{ID: "engine.website", Kind: "engine", Version: 1, AccessGroups: []string{"features"}}
}
