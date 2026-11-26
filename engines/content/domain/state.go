package domain

import (
	"encoding/json"
	"time"
)

const PayloadVersion = 1

// GlobalScopeID is the canonical ScopeID for system-wide Content records.
// Page-scoped selections use page snapshot IDs as their ScopeID instead.
const GlobalScopeID = "global"

// Aggregate holds the system-wide mutable state for exactly one configured locale.
// Locale identifies the catalog entry this aggregate belongs to. Records written
// before locale-scoped storage was introduced can carry an empty locale; its
// presence is not used as a compatibility gate on reads.
type Aggregate struct {
	SnapshotID        string           `json:"snapshot_id"`
	Locale            string           `json:"locale"`
	Revision          int64            `json:"revision"`
	PublishedRevision int64            `json:"published_revision"`
	Records           []Record         `json:"records"`
	LastMutation      *MutationReceipt `json:"last_mutation,omitempty"`
}

// StoredWorkspace is the repository-level unit of work. System-wide aggregate
// state and page-scoped selections live in separate Core records; both are
// hydrated together so callers never need to know the storage layout.
type StoredWorkspace struct {
	Aggregate     Aggregate
	StoreRevision int64
	// PageSelections is keyed by page snapshot ID and canonical locale.
	PageSelections map[string]StoredPageSelections
	// ChangedPageSelectionIDs is transient: repository writes only these pages.
	ChangedPageSelectionIDs []string
	// PublicationIntent is transient input to the atomic publish commit.
	PublicationIntent *PublicationIntent
}

// PublicationIntent is a Content-owned durable delivery record. Its key is
// derived from locale and publication revision; the workflow key is stable.
type PublicationIntent struct {
	Locale              string `json:"locale"`
	ContentRevision     int64  `json:"content_revision"`
	SystemModelRevision int64  `json:"system_model_revision"`
	WorkflowKey         string `json:"workflow_key"`
	State               string `json:"state"`
}

type StoredPageSelections struct {
	Draft         []Selection
	Published     []Selection
	StoreRevision int64
	LastMutation  *MutationReceipt
}

type MutationReceipt struct {
	Operation       string            `json:"operation"`
	KeyHash         string            `json:"key_hash"`
	Fingerprint     string            `json:"fingerprint"`
	ClientRecordIDs map[string]string `json:"client_record_ids,omitempty"`
	HistoryID       string            `json:"history_id,omitempty"`
}

type Publication struct {
	ID              string      `json:"id"`
	Locale          string      `json:"locale"`
	DraftRevision   int64       `json:"draft_revision"`
	ContentRevision int64       `json:"content_revision"`
	CreatedAt       time.Time   `json:"created_at"`
	Records         []Record    `json:"records"`
	Selections      []Selection `json:"selections"`
}

type CopyMarker struct {
	SourceSnapshotID string       `json:"source_snapshot_id"`
	TargetSnapshotID string       `json:"target_snapshot_id"`
	SourceRevision   int64        `json:"source_revision"`
	KeyHash          string       `json:"key_hash"`
	Fingerprint      string       `json:"fingerprint"`
	State            string       `json:"state"`
	Result           *CopyReceipt `json:"result,omitempty"`
}

type StoredCopyMarker struct {
	Marker        CopyMarker
	StoreRevision int64
}

type CopyReceipt struct {
	TargetSnapshotID string `json:"target_snapshot_id"`
	SourceRevision   int64  `json:"source_revision"`
	CopiedRecords    int    `json:"copied_records"`
	CopiedHistory    int    `json:"copied_history"`
}

type WorkspaceView struct {
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
	Placements            []PlacementView   `json:"placements"`
	ClientRecordIDs       map[string]string `json:"client_record_ids,omitempty"`
	ClientTranslationIDs  map[string]string `json:"client_translation_ids,omitempty"`
}

type PlacementView struct {
	ID          string           `json:"id"`
	PageID      string           `json:"page_id"`
	PageName    string           `json:"page_name"`
	FeatureKey  string           `json:"feature_key"`
	FeatureName string           `json:"feature_name"`
	Version     string           `json:"version"`
	Inputs      []PlacementInput `json:"inputs"`
}

type Schema struct {
	State                 string
	PageSnapshotID        string
	Locale                string
	LocaleCatalogRevision int64
	ModelRevision         int64
	ModelDigest           string
	PageRevision          int64
	PageDigest            string
	DataModel             json.RawMessage
	Model                 DataModel
	Placements            []PlacementView
}

type HistorySummary struct {
	ID              string
	DraftRevision   int64
	ContentRevision int64
	CreatedAt       time.Time
	RecordCount     int
}

type HistoryPage struct {
	Items      []HistorySummary
	NextCursor string
}

type PublishedContent struct {
	SnapshotID      string
	Locale          string
	ContentRevision int64
	Records         []Record
	Selections      []Selection
}
