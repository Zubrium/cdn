package prerender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
)

// ArtifactWriter is the Core-only storage capability used by the renderer.
// It receives no path, bucket, DSN, or arbitrary URL from a task.
type ArtifactWriter interface {
	WriteArtifact(ctx context.Context, requestKey, path, contentType string, size int64, checksum string, body io.Reader) (string, error)
}

// DockerWorker validates the complete trusted plan, renders it inside the
// verified sandbox, and persists every byte only through Core.
type DockerWorker struct {
	sandbox *DockerSandbox
	limits  Limits
	files   ArtifactWriter
}

// Config is trusted startup configuration; no field is task-controlled.
type Config struct {
	Sandbox DockerSandboxConfig
	Limits  Limits
	Files   ArtifactWriter
}

func NewDockerWorker(ctx context.Context, config Config) (*DockerWorker, error) {
	if !validInterface(config.Files) || config.Limits.Validate() != nil {
		return nil, ErrRenderUnavailable
	}
	sandbox, err := NewDockerSandbox(ctx, config.Sandbox)
	if err != nil {
		return nil, err
	}
	return &DockerWorker{sandbox: sandbox, limits: config.Limits, files: config.Files}, nil
}

func (w *DockerWorker) Isolated() bool { return w != nil && w.sandbox.Isolated() }

func (w *DockerWorker) SandboxState() SandboxState {
	if w == nil {
		return SandboxState{}
	}
	return w.sandbox.SandboxState()
}

func (w *DockerWorker) Render(ctx context.Context, request Request) (Result, error) {
	if w == nil || !w.Isolated() || ctx == nil {
		return Result{}, ErrRenderUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := ValidatePlan(request, w.limits); err != nil {
		return Result{}, err
	}
	var plan prerendercontract.Plan
	if err := json.Unmarshal(request.Plan, &plan); err != nil {
		return Result{}, ErrInvalidPlan
	}
	state := w.sandbox.SandboxState()
	marker := strings.ReplaceAll(plan.Input.Locale+":"+plan.Input.SnapshotDigest[:12], "\"", "")
	rendered, err := w.sandbox.Render(ctx, []byte(plan.Page.BodyHTML), marker)
	if err != nil {
		return Result{}, err
	}
	proof := prerendercontract.RenderProof{
		Backend: state.Backend, Image: state.Image, SeccompMode: state.SeccompMode,
		CapEff: state.CapEff, NoNewPrivs: state.NoNewPrivs, ReadOnlyRootFS: state.ReadOnlyRootFS,
		NetworkDenied: state.NetworkDenied, CPUQuota: state.CPUQuota,
		MemoryLimitBytes: state.MemoryLimitBytes, PIDsLimit: state.PIDsLimit,
		BrowserVersion: state.BrowserVersion, Locale: plan.Input.Locale, DOMMarker: marker,
	}
	artifacts, err := prerendercontract.BuildArtifactSet(plan, rendered, proof)
	if err != nil {
		return Result{}, err
	}
	document := artifacts.Manifest.Artifacts[0]
	documentID, err := w.files.WriteArtifact(ctx,
		request.TaskID+"/index.html", document.Path, document.MediaType, document.Size, document.SHA256,
		bytes.NewReader(artifacts.Document))
	if err != nil {
		return Result{}, err
	}
	artifacts.Manifest.Artifacts[0].FileID = documentID
	encoded, err := json.Marshal(artifacts.Manifest)
	if err != nil {
		return Result{}, err
	}
	manifestChecksum := prerendercontract.HexDigest(encoded)
	manifestID, err := w.files.WriteArtifact(ctx,
		request.TaskID+"/manifest.json", "manifest.json", prerendercontract.ArtifactMediaType,
		int64(len(encoded)), manifestChecksum, bytes.NewReader(encoded))
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Format: prerendercontract.BuildResultFormat, InputDigest: request.InputDigest,
		Manifest: Artifact{
			FileID: manifestID, Checksum: manifestChecksum,
			ContentType: prerendercontract.ArtifactMediaType, Size: int64(len(encoded)),
		},
		Artifacts: []Artifact{{
			FileID: documentID, Checksum: document.SHA256,
			ContentType: document.MediaType, Size: document.Size,
		}},
	}
	if err := ValidateResult(w.limits, request.InputDigest, result); err != nil {
		return Result{}, err
	}
	return result, nil
}

// WorkerHandler exposes only the statically registered render-page/v1 identity.
// Task.ID and the Core-issued idempotency key are authoritative; client fields
// cannot select an artifact, operation, route, or storage location.
func (w *DockerWorker) WorkerHandler(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
	if w == nil || ctx == nil || task.Kind != core.TaskKindWorker || task.Worker == nil ||
		task.Worker.ComponentID != DataModuleID || task.Worker.OperationID != OperationID ||
		task.Worker.ContractVersion != 1 || len(task.Payload) == 0 || len(task.Payload) > 16<<20 {
		return core.TaskOutcome{}, ErrInvalidPlan
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return core.TaskOutcome{}, ErrInvalidPlan
	}
	var trailing any
	// Core puts the workflow idempotency key on Task.IdempotencyKey, while the
	// per-render identity is the child task's emission key. Binding to the
	// emission key keeps retries stable and rejects payload substitution.
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		(request.TaskID != "" && request.TaskID != task.ID.String()) ||
		task.EmissionKey == "" || task.EmissionKey != request.IdempotencyKey {
		return core.TaskOutcome{}, ErrInvalidPlan
	}
	request.TaskID = task.ID.String()
	result, err := w.Render(ctx, request)
	if err != nil {
		return core.TaskOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return core.TaskOutcome{}, ErrInvalidResult
	}
	return core.TaskOutcome{Result: encoded}, nil
}

func validInterface(value any) bool {
	return value != nil
}
