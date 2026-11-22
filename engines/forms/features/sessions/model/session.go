package model

import "time"

const (
	StateActive    = "active"
	StateSubmitting = "submitting"
	StateSubmitted = "submitted"
	StateCancelled = "cancelled"
	MaxUploadsPerSession = 128
)

type Session struct {
	ID                     string
	FormID                 string
	SchemaVersion          int64
	State                  string
	CreatedAt              time.Time
	ExpiresAt              time.Time
	Uploads                []Upload
	SubmissionID           string
	SubmissionKeyHash      string
	SubmissionFingerprint  string
	SubmissionValues       map[string]any
	SubmissionAttachments  []string
	SubmissionCreatedAt    time.Time
	UsedUploadIDs          []string
}

type Upload struct {
	ID               string
	FieldID          string
	IdempotencyHash  string
	ContentType      string
	SHA256           string
	DeclaredSize     int64
	MaxBytes         int64
	AccessGroup      string
	FileID           string
	Size             int64
	Completed        bool
	Deleting         bool
}

type Stored struct {
	Session   Session
	Revision  int64
	RecordKey string
}
