package domain

import (
	"encoding/json"
	"time"
)

const PayloadVersion = 1

type Aggregate struct {
	SnapshotID        string           `json:"snapshot_id"`
	Revision          int64            `json:"revision"`
	PublishedRevision int64            `json:"published_revision"`
	Records           []Record         `json:"records"`
	Selections        []Selection      `json:"selections"`
	LastMutation      *MutationReceipt `json:"last_mutation,omitempty"`
}

type StoredWorkspace struct {
	Aggregate     Aggregate
	StoreRevision int64
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
	SnapshotID        string            `json:"snapshot_id"`
	DataModel         json.RawMessage   `json:"data_model"`
	Revision          int64             `json:"revision"`
	PublishedRevision int64             `json:"published_revision"`
	Records           []Record          `json:"records"`
	Selections        []Selection       `json:"selections"`
	Placements        []PlacementView   `json:"placements"`
	ClientRecordIDs   map[string]string `json:"client_record_ids,omitempty"`
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
	State      string
	DataModel  json.RawMessage
	Model      DataModel
	Placements []PlacementView
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
	ContentRevision int64
	Records         []Record
	Selections      []Selection
}
