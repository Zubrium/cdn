// Package prerender declares the independent two-stage rendering boundary.
package prerender

import "context"

type Stage string

const (
	StageBase    Stage = "base"
	StageContent Stage = "content"
)

type Request struct {
	JobID           string
	Stage           Stage
	InputFileIDs    []string
	SnapshotID      string
	ContentRevision int64
}
type Result struct{ OutputFileID, Digest string }
type Worker interface {
	Render(context.Context, Request) (Result, error)
}
