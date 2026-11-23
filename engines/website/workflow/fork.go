package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/google/uuid"
)

const (
	ForkOperationID     = "snapshot.fork.start"
	ForkContractVersion = 1
	ForkRootFormat      = "website.snapshot-fork/v1"
)

var errInvalidForkTask = errors.New("website snapshot fork task is invalid")

type ForkContentCopier interface {
	CopySnapshotContent(context.Context, websiteapi.SnapshotForkWorkflowPayload) error
}

type ForkSnapshotFinalizer interface {
	MarkSnapshotReady(context.Context, string, int64) (websiteapi.Snapshot, error)
}

type ForkHandler struct {
	content     ForkContentCopier
	website     ForkSnapshotFinalizer
	maxAttempts int
}

func NewForkHandler(content ForkContentCopier, website ForkSnapshotFinalizer, maxAttempts int) (*ForkHandler, error) {
	if content == nil || website == nil || maxAttempts < 1 || maxAttempts > 100 {
		return nil, ErrInvalidConfiguration
	}
	return &ForkHandler{content: content, website: website, maxAttempts: maxAttempts}, nil
}

type ForkResult struct {
	Format           string `json:"format"`
	SourceSnapshotID string `json:"source_snapshot_id"`
	TargetSnapshotID string `json:"target_snapshot_id"`
	TargetRevision   int64  `json:"target_revision"`
}

func (h *ForkHandler) WorkerHandler(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
	if h == nil || ctx == nil {
		return core.TaskOutcome{}, errInvalidForkTask
	}
	if err := ctx.Err(); err != nil {
		return core.TaskOutcome{}, err
	}
	if task.Kind != core.TaskKindWorker || task.Worker == nil ||
		task.Worker.ComponentID != ComponentID || task.Worker.OperationID != ForkOperationID ||
		task.Worker.ContractVersion != ForkContractVersion || len(task.Payload) == 0 || len(task.Payload) > maxPayloadBytes {
		return core.TaskOutcome{}, errInvalidForkTask
	}
	payload, err := decodeForkPayload(task.Payload)
	if err != nil {
		return core.TaskOutcome{}, err
	}
	if err := h.content.CopySnapshotContent(ctx, payload); err != nil {
		return core.TaskOutcome{}, err
	}
	snapshot, err := h.website.MarkSnapshotReady(ctx, payload.TargetSnapshotID, payload.TargetSnapshotRevision)
	if err != nil {
		return core.TaskOutcome{}, err
	}
	result, err := json.Marshal(ForkResult{
		Format: ForkRootFormat, SourceSnapshotID: payload.SourceSnapshotID,
		TargetSnapshotID: payload.TargetSnapshotID, TargetRevision: snapshot.Revision,
	})
	if err != nil {
		return core.TaskOutcome{}, ErrInvalidPayload
	}
	return core.TaskOutcome{Result: result}, nil
}

func decodeForkPayload(raw json.RawMessage) (websiteapi.SnapshotForkWorkflowPayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var payload websiteapi.SnapshotForkWorkflowPayload
	if err := decoder.Decode(&payload); err != nil {
		return websiteapi.SnapshotForkWorkflowPayload{}, ErrInvalidPayload
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return websiteapi.SnapshotForkWorkflowPayload{}, ErrInvalidPayload
	}
	if _, err := uuid.Parse(payload.SourceSnapshotID); err != nil {
		return websiteapi.SnapshotForkWorkflowPayload{}, ErrInvalidPayload
	}
	if _, err := uuid.Parse(payload.TargetSnapshotID); err != nil {
		return websiteapi.SnapshotForkWorkflowPayload{}, ErrInvalidPayload
	}
	if payload.ExpectedSourceContentRevision < 0 || payload.TargetSnapshotRevision < 1 || payload.ContentCopyIdempotencyKey == "" || len(payload.ContentCopyIdempotencyKey) > 256 {
		return websiteapi.SnapshotForkWorkflowPayload{}, ErrInvalidPayload
	}
	switch payload.SelectionPolicy {
	case "copy", "reset", "":
	default:
		return websiteapi.SnapshotForkWorkflowPayload{}, ErrInvalidPayload
	}
	if payload.SelectionPolicy == "" {
		payload.SelectionPolicy = "copy"
	}
	return payload, nil
}
