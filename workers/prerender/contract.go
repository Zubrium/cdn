// Package prerender declares a bounded render-plan contract for the worker.
package prerender

import (
	"context"
	"errors"

	core "github.com/Zubrium/cdn/core/presentation"
)

var (
	ErrRenderUnavailable = errors.New("prerender_render_unavailable")
	ErrInvalidContext    = errors.New("prerender_invalid_context")
)

const (
	RenderPlanVersion = "render-plan/v1"
	DataModuleID      = "worker.prerender"
	OperationID       = "render-page"
)

type Request struct {
	JobID          string `json:"job_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Plan           []byte `json:"plan"`
	PlanDigest     string `json:"plan_digest"`
	InputDigest    string `json:"input_digest"`
}

type Artifact struct {
	FileID      string `json:"file_id"`
	Checksum    string `json:"checksum"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type Result struct {
	Manifest    Artifact   `json:"manifest"`
	InputDigest string     `json:"input_digest"`
	Artifacts   []Artifact `json:"artifacts"`
}

type Worker interface {
	Render(context.Context, Request) (Result, error)
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() core.ComponentDescriptor { return ComponentDescriptor() }

var _ core.ComponentDescriptorProvider = Descriptor{}

func ComponentDescriptor() core.ComponentDescriptor {
	return core.ComponentDescriptor{ID: DataModuleID, Kind: "worker", Version: 1,
		Operations: []core.ComponentOperation{{ID: OperationID, ContractVersion: 1}}}
}
