// Package presentation exposes Content's typed facade. Content receives
// Website-owned schema projections from outer Presentation composition.
package presentation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Zubrium/cdn/pkg/contracts"
	"strings"
	"time"

	"github.com/Zubrium/cdn/engines/content/domain"
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

type LocaleConfig = domain.LocaleConfig
type ReplaceLocaleConfigRequest = domain.ReplaceLocaleConfigRequest

// ContentModelProjection contains the immutable system-wide model projection.
type ContentModelProjection struct {
	Version       int             `json:"version"`
	ModelRevision int64           `json:"model_revision"`
	Digest        string          `json:"digest"`
	DataModel     json.RawMessage `json:"data_model"`
}

func (schema ContentModelProjection) ComputeDigest() (string, error) {
	payload := struct {
		Version       int             `json:"version"`
		ModelRevision int64           `json:"model_revision"`
		DataModel     json.RawMessage `json:"data_model"`
	}{schema.Version, schema.ModelRevision, schema.DataModel}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (schema ContentModelProjection) VerifyDigest() bool {
	if schema.Version != ProjectionVersion || schema.ModelRevision < 1 || schema.DataModel == nil || len(schema.Digest) != sha256.Size*2 {
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
		Revision       int64       `json:"revision"`
		State          string      `json:"state"`
		Placements     []Placement `json:"placements"`
	}{projection.Version, projection.PageSnapshotID, projection.PageID, projection.Revision, projection.State, projection.Placements}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (projection PagePlacementProjection) VerifyDigest() bool {
	if projection.Version != ProjectionVersion || projection.PageSnapshotID == "" || projection.PageID == "" || projection.Revision < 1 || projection.Placements == nil || len(projection.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := projection.ComputeDigest()
	return err == nil && strings.EqualFold(digest, projection.Digest)
}

type Projections struct {
	ContentModel ContentModelProjection  `json:"content_model"`
	Page         PagePlacementProjection `json:"page_placement_projection"`
	Locale       string                  `json:"locale"`
}

func (projections Projections) VerifyScope(pageSnapshotID string) bool {
	return projections.ContentModel.VerifyDigest() && projections.Page.VerifyDigest() &&
		projections.Page.PageSnapshotID == pageSnapshotID
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
// draft/publication state. SnapshotID identifies the selected page snapshot.
type Workspace struct {
	SnapshotID            string            `json:"snapshot_id"`
	PageSnapshotID        string            `json:"page_snapshot_id"`
	Locale                string            `json:"locale"`
	LocaleCatalogRevision int64             `json:"locale_catalog_revision"`
	ModelRevision         int64             `json:"model_revision"`
	ModelDigest           string            `json:"model_digest"`
	PageRevision          int64             `json:"page_revision"`
	PageDigest            string            `json:"page_digest"`
	DataModel             json.RawMessage   `json:"data_model"`
	Revision              int64             `json:"revision"`
	SelectionRevision     int64             `json:"selection_revision"`
	PublishedRevision     int64             `json:"published_revision"`
	Records               []Record          `json:"records"`
	Selections            []Selection       `json:"selections"`
	Placements            []Placement       `json:"placements"`
	ClientRecordIDs       map[string]string `json:"client_record_ids,omitempty"`
	ClientTranslationIDs  map[string]string `json:"client_translation_ids,omitempty"`
}

type Record struct {
	ID            string         `json:"id"`
	TranslationID string         `json:"translation_id"`
	Locale        string         `json:"locale"`
	EntityID      string         `json:"entity_id"`
	SortOrder     int            `json:"sort_order"`
	Values        map[string]any `json:"values"`
}

type RecordInput struct {
	ID            string         `json:"id,omitempty"`
	ClientKey     string         `json:"client_key,omitempty"`
	TranslationID string         `json:"translation_id,omitempty"`
	EntityID      string         `json:"entity_id"`
	SortOrder     int            `json:"sort_order"`
	Values        map[string]any `json:"values"`
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
	Locale          string      `json:"locale"`
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

func (Descriptor) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{ID: "engine.content", Kind: "engine", Version: 1, AccessGroups: []string{"content"}}
}
