package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
)

const (
	V2RunPayloadVersion = 2
	v2StateQueued       = "queued"
	v2StateProcessing   = "processing"
	v2StateStoring      = "storing"
	v2StateReady        = "ready"
	v2StateRetryFailed  = "retryable_failed"
)

// ProcessRunner is the narrow execution surface required by the v2 worker. A
// concrete runner must already have passed its in-container isolation probe.
type ProcessRunner interface {
	Isolated() bool
	Process(context.Context, mediaprofile.WorkRequest, mediaprofile.Profile, io.Reader) (ProcessedOutput, error)
}

type V2FileCapability interface{ recorddata.CoreFiles }

// V2Handler processes one canonical Website media derivative task. It never
// accepts a profile, preset, processor version, command or resource limit from
// the task payload; all of those are validated by the shared contract and
// derived from the manifest-pinned profile.
type V2Handler struct {
	files       V2FileCapability
	data        DataCapability
	runner      ProcessRunner
	semaphore   chan struct{}
	maxStaged   int64
	setPageSize int
}

// NewV2Handler refuses to use any runner without a verified OS isolation
// boundary. There is no non-isolated fallback and no trusted-config bypass.
func NewV2Handler(files V2FileCapability, data DataCapability, runner ProcessRunner, maxConcurrency int, maxStagedBytes int64) (*V2Handler, error) {
	if files == nil || data == nil || runner == nil || !runner.Isolated() || maxConcurrency < 1 || maxStagedBytes < 1 {
		return nil, ErrNotReady
	}
	return &V2Handler{files: files, data: data, runner: runner,
		semaphore: make(chan struct{}, maxConcurrency), maxStaged: maxStagedBytes, setPageSize: 100}, nil
}

// WorkerHandler strictly validates the versioned contract before any durable
// state or Core file operation. Task identity is authoritative; the request
// cannot supply it.
func (h *V2Handler) WorkerHandler(ctx context.Context, task recorddata.OperationTask) (recorddata.OperationOutcome, error) {
	if h == nil || ctx == nil || task.TaskID == "" || len(task.Payload) == 0 || len(task.Payload) > 1<<20 {
		return recorddata.OperationOutcome{}, ErrInvalidRequest
	}
	request, err := mediaprofile.ValidateWorkRequest(task.Payload)
	if err != nil {
		return recorddata.OperationOutcome{}, err
	}
	// The scheduler carries the workflow idempotency key on IdempotencyKey; the
	// per-rendition identity is the child task's emission key. Comparing against
	// the workflow key would reject every one of the Website job's children.
	if task.EmissionKey == "" || task.EmissionKey != request.IdempotencyKey {
		return recorddata.OperationOutcome{}, ErrInvalidRequest
	}
	result, err := h.Handle(ctx, task.TaskID, request)
	if err != nil {
		return recorddata.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return recorddata.OperationOutcome{}, ErrOutputInvalid
	}
	return recorddata.OperationOutcome{Result: encoded}, nil
}

// Handle executes or reconciles one derivative. Every Core write is keyed by
// the exact task identity, source checksum and canonical profile digest, so a
// retry cannot create a second immutable output or reuse a changed input.
func (h *V2Handler) Handle(ctx context.Context, taskID string, request mediaprofile.WorkRequest) (mediaprofile.WorkResult, error) {
	if h == nil || ctx == nil || h.files == nil || h.data == nil || h.runner == nil || !h.runner.Isolated() ||
		taskID == "" || request.Version != mediaprofile.WorkContractVersion {
		return mediaprofile.WorkResult{}, ErrNotReady
	}
	if len(request.ProfileDigest) != 64 || request.InputDigest == "" || request.SourceSHA256 == "" {
		return mediaprofile.WorkResult{}, ErrInvalidRequest
	}
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	case <-ctx.Done():
		return mediaprofile.WorkResult{}, ctx.Err()
	}

	key := v2RunKey(taskID)
	record, err := h.data.Get(ctx, key)
	if err != nil && !errors.Is(err, recorddata.ErrRecordNotFound) {
		return mediaprofile.WorkResult{}, err
	}
	if errors.Is(err, recorddata.ErrRecordNotFound) {
		err = nil
	}
	identity, err := v2RequestIdentity(request)
	if err != nil {
		return mediaprofile.WorkResult{}, err
	}
	if err == nil && record.Revision > 0 {
		var saved v2RunRecord
		if record.PayloadVersion != V2RunPayloadVersion || json.Unmarshal(record.Payload, &saved) != nil || saved.Identity != identity {
			return mediaprofile.WorkResult{}, ErrTaskConflict
		}
		if saved.State == v2StateReady && saved.Result != nil {
			return *saved.Result, h.verifyOutput(ctx, *saved.Result)
		}
		if saved.State != v2StateQueued && saved.State != v2StateProcessing && saved.State != v2StateStoring && saved.State != v2StateRetryFailed {
			return mediaprofile.WorkResult{}, ErrTaskConflict
		}
	}

	source, err := h.files.StatFile(ctx, request.SourceFileID)
	if err != nil {
		return mediaprofile.WorkResult{}, err
	}
	if source.ID != request.SourceFileID || source.SHA256 != request.SourceSHA256 || source.Size <= 0 ||
		source.Size > h.maxStaged || len(source.Groups) == 0 {
		return mediaprofile.WorkResult{}, ErrTaskConflict
	}
	expectedMIME, err := expectedOutputContentType(request.Profile, request.OutputName, source.ContentType)
	if err != nil {
		return mediaprofile.WorkResult{}, err
	}
	validSource, err := allowedInputContentType(request.Profile, source.ContentType)
	if err != nil || !validSource {
		return mediaprofile.WorkResult{}, ErrInvalidRequest
	}

	state := v2RunRecord{Version: V2RunPayloadVersion, TaskID: taskID, Identity: identity,
		Fingerprint: request.InputDigest, State: v2StateQueued, UpdatedAt: time.Now().UTC()}
	record, err = h.putState(ctx, key, state, record.Revision)
	if err != nil {
		return mediaprofile.WorkResult{}, err
	}
	body, actual, err := h.files.ReadFile(ctx, request.SourceFileID)
	if err != nil {
		return mediaprofile.WorkResult{}, err
	}
	defer body.Close()
	if actual.ID != source.ID || actual.SHA256 != source.SHA256 || actual.Size != source.Size ||
		actual.ContentType != source.ContentType || !sameGroups(actual.Groups, source.Groups) {
		return mediaprofile.WorkResult{}, ErrTaskConflict
	}

	state.State = v2StateProcessing
	state.UpdatedAt = time.Now().UTC()
	record, err = h.putState(ctx, key, state, record.Revision)
	if err != nil {
		return mediaprofile.WorkResult{}, err
	}
	output, err := h.runner.Process(ctx, request, request.Profile, body)
	if err != nil {
		state.State, state.ErrorCode, state.UpdatedAt = v2StateRetryFailed, workerErrorCode(err), time.Now().UTC()
		_, _ = h.putState(ctx, key, state, record.Revision)
		return mediaprofile.WorkResult{}, err
	}
	if output.Body == nil {
		return mediaprofile.WorkResult{}, ErrOutputInvalid
	}
	defer output.Body.Close()
	if output.Size <= 0 || output.Size > h.maxStaged || output.ContentType != expectedMIME || !sha256Token.MatchString(output.SHA256) {
		return mediaprofile.WorkResult{}, ErrOutputInvalid
	}

	state.State, state.UpdatedAt = v2StateStoring, time.Now().UTC()
	record, err = h.putState(ctx, key, state, record.Revision)
	if err != nil {
		return mediaprofile.WorkResult{}, err
	}
	groups := append([]string(nil), actual.Groups...)
	metadata := map[string]string{
		"worker": "media", "task_id": taskID, "job_id": request.JobID,
		"source_file_id": request.SourceFileID, "fingerprint": request.InputDigest,
		"profile_digest": request.ProfileDigest, "processor_version": request.ProcessorVersion,
		"output_name": request.OutputName,
	}
	requestKey := v2OutputRequestKey(taskID, request)
	outputID, err := h.files.CreateFileVerified(ctx, requestKey, groups, metadata,
		output.ContentType, output.Size, output.SHA256, output.Body)
	if err != nil {
		return mediaprofile.WorkResult{}, ErrOutputUncertain
	}
	if err := h.inheritFileSets(ctx, source.ID, outputID); err != nil {
		return mediaprofile.WorkResult{}, ErrOutputUncertain
	}
	result := mediaprofile.WorkResult{Version: mediaprofile.WorkContractVersion, Ready: true,
		OutputName: request.OutputName, FileID: outputID, Checksum: output.SHA256,
		ContentType: output.ContentType, Size: output.Size, ProfileDigest: request.ProfileDigest,
		ProcessorVersion: request.ProcessorVersion, Fingerprint: request.InputDigest}
	if err := h.verifyOutput(ctx, result); err != nil {
		return mediaprofile.WorkResult{}, err
	}
	state.State, state.Result, state.UpdatedAt = v2StateReady, &result, time.Now().UTC()
	if _, err := h.putState(ctx, key, state, record.Revision); err != nil {
		return mediaprofile.WorkResult{}, err
	}
	return result, nil
}

type v2RunRecord struct {
	Version     int                      `json:"version"`
	TaskID      string                   `json:"task_id"`
	Identity    string                   `json:"identity"`
	Fingerprint string                   `json:"fingerprint"`
	State       string                   `json:"state"`
	ErrorCode   string                   `json:"error_code,omitempty"`
	Result      *mediaprofile.WorkResult `json:"result,omitempty"`
	UpdatedAt   time.Time                `json:"updated_at"`
}

func (h *V2Handler) putState(ctx context.Context, key recorddata.RecordKey, state v2RunRecord, revision int64) (recorddata.Record, error) {
	payload, err := json.Marshal(state)
	if err != nil || len(payload) > 1<<16 {
		return recorddata.Record{}, ErrOutputInvalid
	}
	return h.data.Put(ctx, key, recorddata.RecordValue{Payload: payload, PayloadVersion: V2RunPayloadVersion,
		IndexData: json.RawMessage(`{}`)}, revision)
}

func (h *V2Handler) verifyOutput(ctx context.Context, expected mediaprofile.WorkResult) error {
	file, err := h.files.StatFile(ctx, expected.FileID)
	if err != nil || file.ID != expected.FileID || file.SHA256 != expected.Checksum ||
		file.ContentType != expected.ContentType || file.Size != expected.Size {
		return ErrOutputUncertain
	}
	return nil
}

// inheritFileSets copies only membership, never access rights. Core authorizes
// every set operation against the worker's service session.
func (h *V2Handler) inheritFileSets(ctx context.Context, sourceID, outputID string) error {
	cursor := ""
	for {
		sets, next, err := h.files.ListManagedFileSets(ctx, "", "", cursor, h.setPageSize)
		if err != nil {
			return err
		}
		for _, set := range sets {
			memberCursor := ""
			for {
				files, memberNext, err := h.files.ListFileSet(ctx, set.ID, memberCursor, h.setPageSize)
				if err != nil {
					return err
				}
				found := false
				for _, file := range files {
					if file.ID == sourceID {
						found = true
						break
					}
				}
				if found {
					if _, err := h.files.AddFileToManagedSet(ctx, set.ID, outputID, set.Revision); err != nil {
						return err
					}
				}
				if memberNext == "" {
					break
				}
				memberCursor = memberNext
			}
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

func v2RunKey(taskID string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: DataNamespace, ScopeType: DataScopeType, ScopeID: taskID, RecordKey: "v2"}
}

func v2RequestIdentity(request mediaprofile.WorkRequest) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > 1<<16 {
		return "", ErrInvalidRequest
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// v2OutputRequestKey is a deterministic UUIDv5-shaped request identity. Core
// requires every file creation request key to be a UUID; deriving it from the
// task identity makes a retry return the original immutable file instead of
// creating a second one.
func v2OutputRequestKey(taskID string, request mediaprofile.WorkRequest) string {
	sum := sha256.Sum256([]byte(taskID + "\x00" + request.IdempotencyKey + "\x00" + request.InputDigest + "\x00" + request.OutputName + "\x00" + request.ProfileDigest + "\x00" + request.ProcessorVersion))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func expectedOutputContentType(profile mediaprofile.Profile, outputName, sourceContentType string) (string, error) {
	if profile.Accept == "image" || profile.Accept == "file" {
		return sourceContentType, nil
	}
	if profile.Accept == "raster-image" {
		for _, rendition := range profile.Renditions {
			if rendition.Name == outputName {
				_, contentType, err := ffmpegArgs(profile, outputName)
				return contentType, err
			}
		}
		return "", ErrInvalidRequest
	}
	if profile.Accept == "video" {
		_, contentType, err := ffmpegArgs(profile, outputName)
		return contentType, err
	}
	return "", ErrInvalidRequest
}

func allowedInputContentType(profile mediaprofile.Profile, contentType string) (bool, error) {
	switch profile.Accept {
	case "raster-image", "image":
		switch contentType {
		case "image/jpeg", "image/png", "image/webp", "image/gif", "image/avif":
			return true, nil
		}
	case "video":
		switch contentType {
		case "video/mp4", "video/webm":
			return true, nil
		}
	case "file":
		switch contentType {
		case "image/jpeg", "image/png", "image/webp", "image/gif", "image/avif", "video/mp4", "video/webm":
			return true, nil
		}
	}
	return false, nil
}

func workerErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "media_timeout"
	case errors.Is(err, context.Canceled):
		return "media_cancelled"
	default:
		return "media_processing_failed"
	}
}
