package model

import "time"

// Submission contains accepted answer data. Answer values are immutable; only
// State may change through the administrative API.
type Submission struct {
	ID                     string
	FormID                 string
	FormKey                string
	SchemaVersion          int64
	State                  string
	Values                 map[string]any
	CreatedAt              time.Time
	IdempotencyKeyHash     string
	IdempotencyFingerprint string
}

type StoredSubmission struct {
	Submission                Submission
	Revision                  int64
	Attachments               []string
	DeletePending             bool
	DeleteStartedFromRevision int64
}
