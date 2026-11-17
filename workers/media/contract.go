// Package media declares the boundary of the independently selected media worker.
package media

import "context"

// Request refers to an immutable Core file and a configured processing profile.
type Request struct{ JobID, SourceFileID, Profile, ProfileVersion string }
type Result struct {
	OutputFileID, Checksum, ContentType string
	Size                                int64
}
type Worker interface {
	Process(context.Context, Request) (Result, error)
}
