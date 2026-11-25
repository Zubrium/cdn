package contracts

// PublishedContent is the transport-neutral public content payload read by
// Router's content-runtime operation. The Content engine keeps aliases for its
// presentation DTOs, so both sides share one JSON definition.
type PublishedContent struct {
	SnapshotID      string                      `json:"snapshot_id"`
	Locale          string                      `json:"locale"`
	ContentRevision int64                       `json:"content_revision"`
	Records         []PublishedContentRecord    `json:"records"`
	Selections      []PublishedContentSelection `json:"selections"`
}

type PublishedContentRecord struct {
	ID            string         `json:"id"`
	TranslationID string         `json:"translation_id"`
	Locale        string         `json:"locale"`
	EntityID      string         `json:"entity_id"`
	SortOrder     int            `json:"sort_order"`
	Values        map[string]any `json:"values"`
}

type PublishedContentSelection struct {
	PlacementID string   `json:"placement_id"`
	InputKey    string   `json:"input_key"`
	Mode        string   `json:"mode"`
	RecordIDs   []string `json:"record_ids,omitempty"`
}
