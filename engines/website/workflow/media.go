package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/Zubrium/cdn/pkg/contracts"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/google/uuid"
)

const (
	MediaOperationID     = "media-derivative"
	MediaContractVersion = 1
	MediaRootFormat      = "website.media-root/v1"

	mediaWorkerOperation = "derivative"
)

var (
	errInvalidMediaTask = errors.New("website media workflow task is invalid")
	identifierToken     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// MediaRequest is the immutable root input created by Website application.
// Every identity and digest is server-generated; composition registers only
// this operation, and a workflow request cannot select a worker or preset.
type MediaRequest struct {
	Version          int             `json:"version"`
	JobID            uuid.UUID       `json:"job_id"`
	SnapshotID       uuid.UUID       `json:"snapshot_id"`
	PlacementID      string          `json:"placement_id"`
	FieldPath        string          `json:"field_path"`
	SourceFileID     uuid.UUID       `json:"source_file_id"`
	SourceSHA256     string          `json:"source_sha256"`
	ProfileDigest    string          `json:"profile_digest"`
	ProcessorVersion string          `json:"processor_version"`
	Profile          json.RawMessage `json:"profile"`
	Outputs          []string        `json:"outputs"`
	InputDigest      string          `json:"input_digest"`
}

// MediaRootResult contains only stable emission identities. Core assigns child
// task IDs after this result commits, so they cannot be embedded here.
type MediaRootResult struct {
	Format           string            `json:"format"`
	JobID            uuid.UUID         `json:"job_id"`
	ProfileDigest    string            `json:"profile_digest"`
	ProcessorVersion string            `json:"processor_version"`
	InputDigest      string            `json:"input_digest"`
	OutputNames      []string          `json:"output_names"`
	EmissionKeys     map[string]string `json:"emission_keys"`
}

// MediaWorkerHandler emits one bounded derivative child for every rendition
// declared by the manifest-pinned profile. It never processes bytes or writes
// files; the registered worker.media operation performs those effects.
func (h *Handler) MediaWorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if h == nil || ctx == nil || task.TaskID == "" {
		return contracts.OperationOutcome{}, errInvalidMediaTask
	}
	if err := ctx.Err(); err != nil {
		return contracts.OperationOutcome{}, err
	}
	if len(task.Payload) == 0 || len(task.Payload) > maxPayloadBytes {
		return contracts.OperationOutcome{}, errInvalidMediaTask
	}
	request, err := decodeMediaRequest(task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	profile, err := mediaprofile.ValidateProfile(request.Profile)
	if err != nil {
		return contracts.OperationOutcome{}, errInvalidMediaTask
	}
	outputs := mediaprofile.Outputs(profile)
	if len(outputs) != len(request.Outputs) || len(outputs) > mediaprofile.MaxRenditions+1 {
		return contracts.OperationOutcome{}, errInvalidMediaTask
	}
	for i, name := range outputs {
		if name != request.Outputs[i] {
			return contracts.OperationOutcome{}, errInvalidMediaTask
		}
	}
	emissionKeys := make(map[string]string, len(outputs))
	children := make([]contracts.OperationTaskChild, 0, len(outputs))
	for _, name := range outputs {
		emissionKey := "media:" + request.JobID.String() + ":" + name
		idempotencyKey := emissionKey
		if !emissionPattern.MatchString(emissionKey) || !emissionPattern.MatchString(idempotencyKey) {
			return contracts.OperationOutcome{}, errInvalidMediaTask
		}
		payload, err := json.Marshal(mediaprofile.WorkRequest{
			Version: mediaprofile.WorkContractVersion, JobID: request.JobID.String(),
			SnapshotID: request.SnapshotID.String(), PlacementID: request.PlacementID,
			FieldPath: request.FieldPath, SourceFileID: request.SourceFileID.String(),
			SourceSHA256: request.SourceSHA256, ProfileDigest: request.ProfileDigest,
			ProcessorVersion: request.ProcessorVersion, OutputName: name,
			IdempotencyKey: idempotencyKey, InputDigest: request.InputDigest, Profile: profile,
		})
		if err != nil || len(payload) == 0 || len(payload) > maxPayloadBytes {
			return contracts.OperationOutcome{}, errInvalidMediaTask
		}
		emissionKeys[name] = emissionKey
		children = append(children, contracts.OperationTaskChild{
			OperationID: mediaWorkerOperation, Version: mediaprofile.WorkContractVersion,
			EmissionKey: emissionKey, Payload: payload, MaxAttempts: h.config.MaxAttempts,
		})
	}
	root, err := json.Marshal(MediaRootResult{
		Format: MediaRootFormat, JobID: request.JobID, ProfileDigest: request.ProfileDigest,
		ProcessorVersion: request.ProcessorVersion, InputDigest: request.InputDigest,
		OutputNames: outputs, EmissionKeys: emissionKeys,
	})
	if err != nil || len(root) > maxPayloadBytes {
		return contracts.OperationOutcome{}, errInvalidMediaTask
	}
	return contracts.OperationOutcome{Result: root, Children: children}, nil
}

func decodeMediaRequest(raw json.RawMessage) (MediaRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request MediaRequest
	if err := decoder.Decode(&request); err != nil {
		return MediaRequest{}, errInvalidMediaTask
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return MediaRequest{}, errInvalidMediaTask
	}
	if request.Version != MediaContractVersion || request.JobID == uuid.Nil || request.SnapshotID == uuid.Nil ||
		request.SourceFileID == uuid.Nil || !identifierToken.MatchString(request.PlacementID) ||
		request.FieldPath == "" || strings.ContainsAny(request.FieldPath, "\x00\r\n") || len(request.FieldPath) > 512 ||
		!digestPattern.MatchString(request.SourceSHA256) || !digestPattern.MatchString(request.ProfileDigest) ||
		!digestPattern.MatchString(request.InputDigest) || !identifierToken.MatchString(request.ProcessorVersion) ||
		len(request.Outputs) == 0 || len(request.Outputs) > mediaprofile.MaxRenditions+1 || len(request.Profile) == 0 {
		return MediaRequest{}, errInvalidMediaTask
	}
	seen := make(map[string]struct{}, len(request.Outputs))
	for _, name := range request.Outputs {
		if _, exists := seen[name]; exists {
			return MediaRequest{}, errInvalidMediaTask
		}
		seen[name] = struct{}{}
	}
	return request, nil
}
