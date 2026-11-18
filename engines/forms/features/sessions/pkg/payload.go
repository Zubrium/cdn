package pkg

import "time"

const PayloadVersion = 1

// PayloadV1 stores one resumable visitor session; only the digest of its
// bearer token is included in the record key.
type PayloadV1 struct {
	ID                    string    `json:"id"`
	SnapshotID            string    `json:"snapshot_id"`
	FormID                string    `json:"form_id"`
	SchemaVersion         int64     `json:"schema_version"`
	State                 string    `json:"state"`
	CreatedAt             time.Time `json:"created_at"`
	ExpiresAt             time.Time `json:"expires_at"`
	Uploads               []Upload  `json:"uploads"`
	SubmissionID          string    `json:"submission_id,omitempty"`
	SubmissionKeyHash     string    `json:"submission_key_hash,omitempty"`
	SubmissionFingerprint string    `json:"submission_fingerprint,omitempty"`
	SubmissionValues      map[string]any `json:"submission_values,omitempty"`
	SubmissionAttachments []string  `json:"submission_attachments,omitempty"`
	SubmissionCreatedAt   time.Time `json:"submission_created_at,omitempty"`
	UsedUploadIDs         []string  `json:"used_upload_ids,omitempty"`
}

type Upload struct {
	ID              string `json:"id"`
	FieldID         string `json:"field_id"`
	IdempotencyHash string `json:"idempotency_hash"`
	ContentType     string `json:"content_type"`
	SHA256          string `json:"sha256,omitempty"`
	DeclaredSize    int64  `json:"declared_size"`
	MaxBytes        int64  `json:"max_bytes"`
	AccessGroup     string `json:"access_group"`
	FileID          string `json:"file_id,omitempty"`
	Size            int64  `json:"size,omitempty"`
	Completed       bool   `json:"completed"`
	Deleting        bool   `json:"deleting,omitempty"`
}
