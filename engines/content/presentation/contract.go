// Package presentation exposes Content's typed facade. Content receives
// Website-owned schema projections from outer Presentation composition.
package presentation

import (
	"context"

	"github.com/Zubrium/cdn/engines/content/domain"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrUnauthorized = contracts.ErrAdminContentUnauthorized
	ErrForbidden    = contracts.ErrAdminContentForbidden
	ErrNotFound     = contracts.ErrAdminContentNotFound
	ErrConflict     = contracts.ErrAdminContentConflict
	ErrInvalid      = contracts.ErrAdminContentInvalid
	ErrTooLarge     = contracts.ErrAdminContentTooLarge
	ErrUnavailable  = contracts.ErrAdminContentUnavailable
)

type Contract interface {
	API
}

// API is the engine boundary. The two Website projections are supplied only
// by trusted outer Presentation composition and never decoded from a request.
type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	GetLocaleConfig(context.Context) (domain.LocaleConfig, error)
	ReplaceLocaleConfig(context.Context, domain.ReplaceLocaleConfigRequest) (domain.LocaleConfig, error)
	GetWorkspace(context.Context, Projections) (Workspace, error)
	SaveWorkspaceDraft(context.Context, Projections, SaveWorkspaceDraftRequest) (Workspace, error)
	PublishWorkspace(context.Context, Projections, PublishWorkspaceRequest) (Workspace, error)
	ListHistory(context.Context, Projections, string, int) (HistoryPage, error)
	RestoreHistory(context.Context, Projections, RestoreHistoryRequest) (Workspace, error)
	ReadPublishedContent(context.Context, Projections) (PublishedContent, error)
	CopySnapshotContent(context.Context, CopySnapshotContentRequest) (CopySnapshotContentResult, error)
	GetContentProjections(context.Context, string) (Projections, error)
}

// ProjectionsProvider is the trusted outer-Presentation capability that maps
// Website-owned snapshots into Content's read-only projections. Content never
// builds these values from request data.
type ProjectionsProvider interface {
	GetContentProjections(context.Context, string) (Projections, error)
}

const ProjectionVersion = 1

type LocaleConfig = contracts.AdminContentLocaleConfig
type ReplaceLocaleConfigRequest = contracts.AdminContentReplaceLocaleConfigRequest

type ContentModelProjection = contracts.AdminContentModelProjection
type PagePlacementProjection = contracts.AdminContentPagePlacementProjection
type Projections = contracts.AdminContentProjections

type Placement = contracts.AdminContentPlacement
type PlacementInput = contracts.AdminContentPlacementInput
type PlacementField = contracts.AdminContentPlacementField
type PlacementInputLookup = contracts.AdminContentPlacementInputLookup

type Workspace = contracts.AdminContentWorkspace
type Record = contracts.PublishedContentRecord
type Selection = contracts.PublishedContentSelection
type RecordInput = contracts.AdminContentRecordInput
type SaveWorkspaceDraftRequest = contracts.AdminContentSaveWorkspaceDraftRequest
type PublishWorkspaceRequest = contracts.AdminContentPublishWorkspaceRequest
type HistoryItem = contracts.AdminContentHistoryItem
type HistoryPage = contracts.AdminContentHistoryPage
type RestoreHistoryRequest = contracts.AdminContentRestoreHistoryRequest

type PublishedContent = contracts.PublishedContent

const (
	SelectionPolicyCopy  = "copy"
	SelectionPolicyReset = "reset"
)

func ValidSelectionPolicy(policy string) bool {
	return policy == SelectionPolicyCopy || policy == SelectionPolicyReset
}

// CopySnapshotContentRequest is a durable workflow task input. The selection
// policy controls only page-scoped draft and published selections.
type CopySnapshotContentRequest struct {
	SourceSchema                    Projections `json:"source_schema"`
	TargetSchema                    Projections `json:"target_schema"`
	SourceSnapshotID                string      `json:"source_snapshot_id"`
	TargetSnapshotID                string      `json:"target_snapshot_id"`
	ExpectedSourceRevision          int64       `json:"expected_source_revision"`
	ExpectedSourceSelectionRevision int64       `json:"expected_source_selection_revision"`
	SelectionPolicy                 string      `json:"selection_policy"`
	IdempotencyKey                  string      `json:"idempotency_key"`
}

type CopySnapshotContentResult struct {
	TargetSnapshotID string `json:"target_snapshot_id"`
	SourceRevision   int64  `json:"source_revision"`
	CopiedRecords    int    `json:"copied_records"`
	CopiedHistory    int    `json:"copied_history"`
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{ID: "engine.content", Kind: "engine", Version: 1, AccessGroups: []string{"content"}}
}
