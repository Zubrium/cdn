package pkg

import "time"

const PayloadVersion = 1

// PayloadV1 stores one accepted submission and its engine-owned cleanup state.
type PayloadV1 struct {
	ID                        string         `json:"id"`
	FormID                    string         `json:"form_id"`
	State                     string         `json:"state"`
	Values                    map[string]any `json:"values"`
	CreatedAt                 time.Time      `json:"created_at"`
	AttachmentIDs             []string       `json:"attachment_ids,omitempty"`
	DeletePending             bool           `json:"delete_pending,omitempty"`
	DeleteStartedFromRevision int64          `json:"delete_started_from_revision,omitempty"`
}
