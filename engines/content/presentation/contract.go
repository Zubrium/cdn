// Package presentation exposes Content's typed facade. Content receives
// Website-owned schema projections from outer Presentation composition.
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
	ErrConflict     = errors.New("revision conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrTooLarge     = errors.New("request too large")
	ErrUnavailable  = errors.New("Content dependency unavailable")
)

type Contract interface {
	platform.Module
	API
}

// API is the engine boundary. The two Website projections are supplied only
// by trusted outer Presentation composition and never decoded from a request.
type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	GetWorkspace(context.Context, Projections) (Workspace, error)
	SaveWorkspaceDraft(context.Context, Projections, SaveWorkspaceDraftRequest) (Workspace, error)
	PublishWorkspace(context.Context, Projections, PublishWorkspaceRequest) (Workspace, error)
	ListHistory(context.Context, Projections, string, int) (HistoryPage, error)
	RestoreHistory(context.Context, Projections, RestoreHistoryRequest) (Workspace, error)
	ReadPublishedContent(context.Context, Projections) (PublishedContent, error)
	CopySnapshotContent(context.Context, CopySnapshotContentRequest) (CopySnapshotContentResult, error)
}

const ProjectionVersion = 1

// SiteContentSchema contains the immutable site-wide model projection.
type SiteContentSchema struct {
	Version       int             `json:"version"`
	SiteID        string          `json:"site_id"`
	ModelRevision int64           `json:"model_revision"`
	Digest        string          `json:"digest"`
	DataModel     json.RawMessage `json:"data_model"`
}

func (schema SiteContentSchema) ComputeDigest() (string, error) {
	payload := struct {
		Version       int             `json:"version"`
		SiteID        string          `json:"site_id"`
		ModelRevision int64           `json:"model_revision"`
		DataModel     json.RawMessage `json:"data_model"`
	}{schema.Version, schema.SiteID, schema.ModelRevision, schema.DataModel}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (schema SiteContentSchema) VerifyDigest() bool {
	if schema.Version != ProjectionVersion || schema.SiteID == "" || schema.ModelRevision < 1 || schema.DataModel == nil || len(schema.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := schema.ComputeDigest()
	return err == nil && strings.EqualFold(digest, schema.Digest)
}

// PagePlacementProjection contains only page-scoped placement bindings.
type PagePlacementProjection struct {
	Version        int         `json:"version"`
	PageSnapshotID string      `json:"page_snapshot_id"`
	PageID         string      `json:"page_id"`
	SiteID         string      `json:"site_id"`
	Revision       int64       `json:"revision"`
	Digest         string      `json:"digest"`
	State          string      `json:"state"`
	Placements     []Placement `json:"placements"`
}

func (projection PagePlacementProjection) ComputeDigest() (string, error) {
	payload := struct {
		Version        int         `json:"version"`
		PageSnapshotID string      `json:"page_snapshot_id"`
		PageID         string      `json:"page_id"`
		SiteID         string      `json:"site_id"`
		Revision       int64       `json:"revision"`
		State          string      `json:"state"`
		Placements     []Placement `json:"placements"`
	}{projection.Version, projection.PageSnapshotID, projection.PageID, projection.SiteID, projection.Revision, projection.State, projection.Placements}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (projection PagePlacementProjection) VerifyDigest() bool {
	if projection.Version != ProjectionVersion || projection.PageSnapshotID == "" || projection.PageID == "" || projection.SiteID == "" || projection.Revision < 1 || projection.Placements == nil || len(projection.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := projection.ComputeDigest()
	return err == nil && strings.EqualFold(digest, projection.Digest)
}

type Projections struct {
	Site SiteContentSchema       `json:"site_content_schema"`
	Page PagePlacementProjection `json:"page_placement_projection"`
}

func (projections Projections) VerifyScope(pageSnapshotID string) bool {
	return projections.Site.VerifyDigest() && projections.Page.VerifyDigest() &&
		projections.Site.SiteID == projections.Page.SiteID && projections.Page.PageSnapshotID == pageSnapshotID
}

type Placement struct {
	ID          string           `json:"id"`
	PageID      string           `json:"page_id"`
	PageName    string           `json:"page_name"`
	FeatureKey  string           `json:"feature_key"`
	FeatureName string           `json:"feature_name"`
	Version     string           `json:"version"`
	Inputs      []PlacementInput `json:"inputs"`
}

type PlacementInput struct {
	InputKey string                `json:"input_key"`
	Kind     string                `json:"kind"`
	EntityID string                `json:"entity_id"`
	Fields   []PlacementField      `json:"fields"`
	Lookup   *PlacementInputLookup `json:"lookup,omitempty"`
}

type PlacementField struct {
	InputPath string `json:"input_path"`
	FieldID   string `json:"field_id"`
}

type PlacementInputLookup struct {
	Argument string `json:"argument"`
	FieldID  string `json:"field_id"`
}

// Workspace combines Website's read-only schema with Content-owned mutable
// draft/publication state. SnapshotID identifies the selected Website scope.
type Workspace struct {
	SnapshotID        string            `json:"snapshot_id"`
	SiteID            string            `json:"site_id"`
	PageSnapshotID    string            `json:"page_snapshot_id"`
	ModelRevision     int64             `json:"model_revision"`
	ModelDigest       string            `json:"model_digest"`
	PageRevision      int64             `json:"page_revision"`
	PageDigest        string            `json:"page_digest"`
	DataModel         json.RawMessage   `json:"data_model"`
	Revision          int64             `json:"revision"`
	SelectionRevision int64             `json:"selection_revision"`
	PublishedRevision int64             `json:"published_revision"`
	Records           []Record          `json:"records"`
	Selections        []Selection       `json:"selections"`
	Placements        []Placement       `json:"placements"`
	ClientRecordIDs   map[string]string `json:"client_record_ids,omitempty"`
}

type Record struct {
	ID        string         `json:"id"`
	EntityID  string         `json:"entity_id"`
	SortOrder int            `json:"sort_order"`
	Values    map[string]any `json:"values"`
}

type RecordInput struct {
	// ID is an ID previously issued by Content. ClientKey correlates a new
	// request-local row with selections; it is never persisted as the record ID.
	ID        string         `json:"id,omitempty"`
	ClientKey string         `json:"client_key,omitempty"`
	EntityID  string         `json:"entity_id"`
	SortOrder int            `json:"sort_order"`
	Values    map[string]any `json:"values"`
}

type Selection struct {
	PlacementID string   `json:"placement_id"`
	InputKey    string   `json:"input_key"`
	Mode        string   `json:"mode"`
	RecordIDs   []string `json:"record_ids,omitempty"`
}

type SaveWorkspaceDraftRequest struct {
	SnapshotID                string        `json:"-"`
	IdempotencyKey            string        `json:"-"`
	ExpectedRevision          int64         `json:"expected_revision"`
	ExpectedSelectionRevision int64         `json:"expected_selection_revision"`
	Records                   []RecordInput `json:"records"`
	Selections                []Selection   `json:"selections"`
}

type PublishWorkspaceRequest struct {
	SnapshotID                string `json:"-"`
	IdempotencyKey            string `json:"-"`
	ExpectedRevision          int64  `json:"expected_revision"`
	ExpectedSelectionRevision int64  `json:"expected_selection_revision"`
}

type HistoryItem struct {
	ID              string    `json:"id"`
	DraftRevision   int64     `json:"draft_revision"`
	ContentRevision int64     `json:"content_revision"`
	CreatedAt       time.Time `json:"created_at"`
	RecordCount     int       `json:"record_count"`
}

type HistoryPage struct {
	Items      []HistoryItem `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type RestoreHistoryRequest struct {
	SnapshotID                string `json:"-"`
	HistoryID                 string `json:"-"`
	IdempotencyKey            string `json:"-"`
	ExpectedRevision          int64  `json:"expected_revision"`
	ExpectedSelectionRevision int64  `json:"expected_selection_revision"`
}

type PublishedContent struct {
	SnapshotID      string      `json:"snapshot_id"`
	ContentRevision int64       `json:"content_revision"`
	Records         []Record    `json:"records"`
	Selections      []Selection `json:"selections"`
}

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

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.content", Kind: "engine", Version: 1, AccessGroups: []string{"content"}}
}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "workspace.read", Action: platform.AccessActionRead, ResourceType: "content-workspace"},
		{Name: "workspace.write", Action: platform.AccessActionWrite, ResourceType: "content-workspace"},
		{Name: "workspace.publish", Action: platform.AccessActionWrite, ResourceType: "content-workspace"},
		{Name: "workspace.history", Action: platform.AccessActionRead, ResourceType: "content-history"},
		{Name: "workspace.restore", Action: platform.AccessActionWrite, ResourceType: "content-workspace"},
		{Name: "snapshot.copy-content", Action: platform.AccessActionWrite, ResourceType: "content-workspace"},
	}
}

var _ platform.Module = Descriptor{}
