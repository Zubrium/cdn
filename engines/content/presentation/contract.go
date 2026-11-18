// Package presentation exposes Content's typed facade to Presentation and to
// other in-process capabilities. Content reads Website structure through an
// injected resolver and never accesses Website storage directly.
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
	ErrConflict     = errors.New("revision conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrTooLarge     = errors.New("request too large")
	ErrUnavailable  = errors.New("Content dependency unavailable")
)

type Contract interface {
	platform.Module
	API
}

// API is the engine boundary. Website schema is read-only; all writes concern
// snapshot-scoped Content data. Admin transport is supplied by Presentation.
type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	GetWorkspace(context.Context, string) (Workspace, error)
	SaveWorkspaceDraft(context.Context, SaveWorkspaceDraftRequest) (Workspace, error)
	PublishWorkspace(context.Context, PublishWorkspaceRequest) (Workspace, error)
	ListHistory(context.Context, string, string, int) (HistoryPage, error)
	RestoreHistory(context.Context, RestoreHistoryRequest) (Workspace, error)
	ReadPublishedContent(context.Context, string) (PublishedContent, error)
	CopySnapshotContent(context.Context, CopySnapshotContentRequest) (CopySnapshotContentResult, error)
}

// SchemaResolver is provided by the composition root. Its implementation may
// call Website's in-process typed facade, but must not use HTTP or storage.
type SchemaResolver interface {
	ResolveContentSchema(context.Context, string) (SnapshotSchema, error)
}

type SnapshotSchema struct {
	State      string          `json:"state"`
	DataModel  json.RawMessage `json:"data_model"`
	Placements []Placement     `json:"placements"`
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
	DataModel         json.RawMessage   `json:"data_model"`
	Revision          int64             `json:"revision"`
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
	SnapshotID       string        `json:"-"`
	IdempotencyKey   string        `json:"-"`
	ExpectedRevision int64         `json:"expected_revision"`
	Records          []RecordInput `json:"records"`
	Selections       []Selection   `json:"selections"`
}

type PublishWorkspaceRequest struct {
	SnapshotID       string `json:"-"`
	IdempotencyKey   string `json:"-"`
	ExpectedRevision int64  `json:"expected_revision"`
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
	SnapshotID       string `json:"-"`
	HistoryID        string `json:"-"`
	IdempotencyKey   string `json:"-"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type PublishedContent struct {
	SnapshotID      string      `json:"snapshot_id"`
	ContentRevision int64       `json:"content_revision"`
	Records         []Record    `json:"records"`
	Selections      []Selection `json:"selections"`
}

// CopySnapshotContentRequest is a durable workflow task input. The copy is
// idempotent for the target snapshot and rejects a changed parent revision.
type CopySnapshotContentRequest struct {
	SourceSnapshotID       string `json:"source_snapshot_id"`
	TargetSnapshotID       string `json:"target_snapshot_id"`
	ExpectedSourceRevision int64  `json:"expected_source_revision"`
	IdempotencyKey         string `json:"idempotency_key"`
}

type CopySnapshotContentResult struct {
	TargetSnapshotID string `json:"target_snapshot_id"`
	SourceRevision   int64  `json:"source_revision"`
	CopiedRecords    int    `json:"copied_records"`
	CopiedHistory    int    `json:"copied_history"`
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.content", Kind: "engine", Version: 1}
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
