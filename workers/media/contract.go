// Package media declares bounded, deterministic Media worker contracts.
package media

import "context"

// Request refers to an immutable Core file and a server-configured preset.
// It intentionally contains no command line, paths, codec options, or limits.
type Request struct {
	TaskID         string `json:"job_id"` // The v1 wire key is retained; Task.ID is authoritative.
	SourceFileID   string `json:"source_file_id"`
	Profile        string `json:"profile"`
	ProfileVersion string `json:"profile_version"`
	IdempotencyKey string `json:"idempotency_key"`
}

type Result struct {
	OutputFileID string `json:"output_file_id"`
	Checksum     string `json:"checksum"`
	ContentType  string `json:"content_type"`
	Size         int64  `json:"size"`
}

type Worker interface {
	Process(context.Context, Request) (Result, error)
}
