package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

const (
	ForkOperationID          = "snapshot.fork.start"
	ForkContractVersion      = 1
	ForkCopyOperationID      = "snapshot.fork.copy-content"
	ForkFinalizeOperationID  = "snapshot.fork.finalize"
	ForkChildContractVersion = 1
	ForkRootFormat           = "website.snapshot-fork/v1"
)

var errInvalidForkTask = errors.New("website snapshot fork task is invalid")

// ForkFinalizePayload is the isolated input of the Website-owned finalize
// task. It cannot select an operation, task class or engine identity.
type ForkFinalizePayload struct {
	SnapshotID       string `json:"snapshot_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ForkHandler struct {
	maxAttempts int
}

func NewForkHandler(maxAttempts int) (*ForkHandler, error) {
	if maxAttempts < 1 || maxAttempts > 100 {
		return nil, ErrInvalidConfiguration
	}
	return &ForkHandler{maxAttempts: maxAttempts}, nil
}

type ForkResult struct {
	Format              string `json:"format"`
	SourceSnapshotID    string `json:"source_snapshot_id"`
	TargetSnapshotID    string `json:"target_snapshot_id"`
	CopyEmissionKey     string `json:"copy_emission_key"`
	FinalizeEmissionKey string `json:"finalize_emission_key"`
}

// ForkFinalizeResult is the stable outcome of the isolated Website finalize
// task. It intentionally carries no engine-internal snapshot representation.
type ForkFinalizeResult struct {
	Format     string `json:"format"`
	SnapshotID string `json:"snapshot_id"`
	Revision   int64  `json:"revision"`
}

// WorkerHandler only declares the durable child tasks. Content copying and
// snapshot finalization execute in separate, class-isolated workers; this
// handler never calls another engine or component directly.
func (h *ForkHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if h == nil || ctx == nil || task.TaskID == "" {
		return contracts.OperationOutcome{}, errInvalidForkTask
	}
	if err := ctx.Err(); err != nil {
		return contracts.OperationOutcome{}, err
	}
	if len(task.Payload) == 0 || len(task.Payload) > maxPayloadBytes {
		return contracts.OperationOutcome{}, errInvalidForkTask
	}
	payload, err := decodeForkPayload(task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	copyEmissionKey := forkEmissionKey("fork-copy", payload.TargetSnapshotID)
	finalizeEmissionKey := forkEmissionKey("fork-finalize", payload.TargetSnapshotID)
	if !emissionPattern.MatchString(copyEmissionKey) || !emissionPattern.MatchString(finalizeEmissionKey) {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}
	copyPayload, err := json.Marshal(payload)
	if err != nil || len(copyPayload) == 0 || len(copyPayload) > maxPayloadBytes {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}
	finalizePayload, err := json.Marshal(ForkFinalizePayload{
		SnapshotID: payload.TargetSnapshotID, ExpectedRevision: payload.TargetSnapshotRevision,
	})
	if err != nil || len(finalizePayload) == 0 || len(finalizePayload) > maxPayloadBytes {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}
	result, err := json.Marshal(ForkResult{
		Format: ForkRootFormat, SourceSnapshotID: payload.SourceSnapshotID,
		TargetSnapshotID: payload.TargetSnapshotID,
		CopyEmissionKey:  copyEmissionKey, FinalizeEmissionKey: finalizeEmissionKey,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}
	return contracts.OperationOutcome{
		Result: result,
		Children: []contracts.OperationTaskChild{
			{
				OperationID: ForkCopyOperationID, Version: ForkChildContractVersion,
				EmissionKey: copyEmissionKey, Payload: copyPayload, MaxAttempts: h.maxAttempts,
			},
			{
				OperationID: ForkFinalizeOperationID, Version: ForkChildContractVersion,
				EmissionKey: finalizeEmissionKey, Payload: finalizePayload, MaxAttempts: h.maxAttempts,
				DependsOnEmissions: []string{copyEmissionKey},
			},
		},
	}, nil
}

func forkEmissionKey(prefix, snapshotID string) string {
	return prefix + ":" + snapshotID
}

// DecodeForkPayload validates the immutable payload shared by fork child tasks.
func DecodeForkPayload(raw json.RawMessage) (websiteapi.SnapshotForkWorkflowPayload, error) {
	return decodeForkPayload(raw)
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
