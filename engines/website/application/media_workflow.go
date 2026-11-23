package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"io"
	"strings"
	"time"

	platform "github.com/Zubrium/cdn/engines/website/application/platform"
	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/google/uuid"
)

const (
	mediaPayloadVersion   = 1
	mediaStateQueued      = "queued"
	mediaStateRunning     = "running"
	mediaStateReady       = "ready"
	mediaStateRetryFailed = "retryable_failed"
	mediaStateRejected    = "rejected"
	mediaStateCancelled   = "cancelled"

	mediaRootFormat      = "website.media-root/v1"
	mediaWorkerResultVer = 2
	mediaWorkerComponent = "worker.media"
	mediaWorkerOperation = "derivative"
	mediaMaxTaskResults  = 16
)

var errMediaInvalidResult = errors.New("invalid media workflow result")

type mediaOutputRecord struct {
	FileID      string `json:"file_id"`
	Checksum    string `json:"checksum"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type mediaJobRecord struct {
	ID               string                       `json:"id"`
	SnapshotID       string                       `json:"snapshot_id"`
	PlacementID      string                       `json:"placement_id"`
	FieldPath        string                       `json:"field_path"`
	SourceFileID     string                       `json:"source_file_id"`
	SourceSHA256     string                       `json:"source_sha256"`
	FeatureID        string                       `json:"feature_id"`
	Version          string                       `json:"version"`
	ProfileDigest    string                       `json:"profile_digest"`
	ProcessorVersion string                       `json:"processor_version"`
	Profile          json.RawMessage              `json:"profile"`
	Outputs          []string                     `json:"outputs"`
	Fingerprint      string                       `json:"fingerprint"`
	State            string                       `json:"state"`
	WorkflowID       string                       `json:"workflow_id,omitempty"`
	RootTaskID       string                       `json:"root_task_id,omitempty"`
	TaskIDs          map[string]string            `json:"task_ids,omitempty"`
	ReadyOutputs     map[string]mediaOutputRecord `json:"ready_outputs,omitempty"`
	ErrorCode        string                       `json:"error_code,omitempty"`
	Revision         int64                        `json:"revision"`
	CreatedAt        time.Time                    `json:"created_at"`
	UpdatedAt        time.Time                    `json:"updated_at"`
}

type mediaState struct {
	record   mediaJobRecord
	key      recorddata.RecordKey
	revision int64
}

type mediaIndex struct {
	RecordType       string `json:"record_type"`
	State            string `json:"state"`
	SourceFileID     string `json:"source_file_id,omitempty"`
	ProfileDigest    string `json:"profile_digest,omitempty"`
	ProcessorVersion string `json:"processor_version,omitempty"`
	Revision         int64  `json:"revision"`
}

type mediaReceipt struct {
	RecordType       string `json:"record_type"`
	JobID            string `json:"job_id"`
	Fingerprint      string `json:"fingerprint"`
	ProfileDigest    string `json:"profile_digest,omitempty"`
	ProcessorVersion string `json:"processor_version,omitempty"`
	State            string `json:"state"`
}

type mediaWorkflowPayload struct {
	Version          int             `json:"version"`
	JobID            string          `json:"job_id"`
	SnapshotID       string          `json:"snapshot_id"`
	PlacementID      string          `json:"placement_id"`
	FieldPath        string          `json:"field_path"`
	SourceFileID     string          `json:"source_file_id"`
	SourceSHA256     string          `json:"source_sha256"`
	ProfileDigest    string          `json:"profile_digest"`
	ProcessorVersion string          `json:"processor_version"`
	Profile          json.RawMessage `json:"profile"`
	Outputs          []string        `json:"outputs"`
	InputDigest      string          `json:"input_digest"`
}

type mediaRootResult struct {
	Format           string            `json:"format"`
	JobID            string            `json:"job_id"`
	ProfileDigest    string            `json:"profile_digest"`
	ProcessorVersion string            `json:"processor_version"`
	InputDigest      string            `json:"input_digest"`
	OutputNames      []string          `json:"output_names"`
	EmissionKeys     map[string]string `json:"emission_keys"`
}

type mediaWorkerResult struct {
	Version          int    `json:"version"`
	Ready            bool   `json:"ready"`
	OutputName       string `json:"output_name"`
	FileID           string `json:"file_id"`
	Checksum         string `json:"checksum"`
	ContentType      string `json:"content_type"`
	Size             int64  `json:"size"`
	ProfileDigest    string `json:"profile_digest"`
	ProcessorVersion string `json:"processor_version"`
	Fingerprint      string `json:"fingerprint"`
}

// CreateMediaDerivative resolves the pinned x-media declaration from the exact
// feature manifest version bound to a ready snapshot. Client input contains no
// profile, preset, processor version or processing argument.
func (s *Service) CreateMediaDerivative(ctx context.Context, request websiteapi.CreateDerivativeRequest) (websiteapi.MediaJob, error) {
	if err := s.requireLevel(ctx, platform.AccessLevelWrite); err != nil {
		return websiteapi.MediaJob{}, err
	}
	if err := s.requireCoordinatorWrite(ctx); err != nil {
		return websiteapi.MediaJob{}, err
	}
	if _, err := s.MediaSandbox(); err != nil {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	if !validInterface(s.config.MediaWorkflow) || !validInterface(s.config.MediaFiles) ||
		!identifierPattern.MatchString(s.config.MediaOperationID) || s.config.MediaContractVersion < 1 ||
		!identifierPattern.MatchString(s.config.MediaProcessorVersion) {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}

	snapshotID := strings.ToLower(strings.TrimSpace(request.SnapshotID))
	placementID := strings.ToLower(strings.TrimSpace(request.PlacementID))
	sourceID := strings.ToLower(strings.TrimSpace(request.SourceFileID))
	if !snapshotIDPattern.MatchString(snapshotID) || !identifierPattern.MatchString(placementID) ||
		!validMediaFieldPath(request.FieldPath) || !snapshotIDPattern.MatchString(sourceID) ||
		!snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		return websiteapi.MediaJob{}, websiteapi.ErrInvalid
	}
	snapshot, err := s.GetSnapshot(ctx, snapshotID)
	if err != nil {
		return websiteapi.MediaJob{}, err
	}
	if snapshot.State != "ready" || snapshot.Payload == nil {
		return websiteapi.MediaJob{}, websiteapi.ErrConflict
	}
	page, err := snapshotPageFromConfig(snapshot.Payload.Config)
	if err != nil {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	placementIndex := -1
	for i := range page.Placements {
		if strings.EqualFold(page.Placements[i].ID, placementID) {
			placementIndex = i
			break
		}
	}
	if placementIndex < 0 || page.Placements[placementIndex].Build == nil ||
		!identifierPattern.MatchString(page.Placements[placementIndex].Build.FeatureKey) ||
		!featureVersionPattern.MatchString(page.Placements[placementIndex].Build.Version) {
		return websiteapi.MediaJob{}, websiteapi.ErrInvalid
	}
	featureID := page.Placements[placementIndex].Build.FeatureKey
	version := page.Placements[placementIndex].Build.Version
	if pinned, ok := snapshot.FeaturePins[featureID]; ok && !strings.EqualFold(pinned, version) {
		return websiteapi.MediaJob{}, websiteapi.ErrConflict
	}
	featureRecord, err := s.store.Get(ctx, websiterepo.FeatureVersionKey(featureID, version))
	if err != nil {
		return websiteapi.MediaJob{}, mapCoreError(err)
	}
	var catalog featureVersionRecord
	if featureRecord.PayloadVersion != importPayloadVersion || json.Unmarshal(featureRecord.Payload, &catalog) != nil ||
		catalog.FeatureID != featureID || catalog.Version != version || catalog.State != importStateReady || len(catalog.Manifest) == 0 {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	files := make(map[string]struct{}, len(catalog.Files))
	for name := range catalog.Files {
		files[name] = struct{}{}
	}
	profile, err := featureruntime.ResolveMediaProfile(catalog.Manifest, files, request.FieldPath)
	if err != nil {
		return websiteapi.MediaJob{}, websiteapi.ErrInvalid
	}
	profileDigest, err := mediaprofile.Digest(profile, s.config.MediaProcessorVersion)
	if err != nil || !sha256HexPattern.MatchString(profileDigest) {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	outputNames := mediaprofile.Outputs(profile)
	if len(outputNames) < 1 || len(outputNames) > mediaprofile.MaxRenditions+1 || len(outputNames) > mediaMaxTaskResults {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	profileJSON, err := json.Marshal(profile)
	if err != nil || len(profileJSON) == 0 {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}

	session, ok := importSession(ctx)
	if !ok {
		return websiteapi.MediaJob{}, websiteapi.ErrUnauthorized
	}
	source, err := s.config.MediaFiles.StatFile(ctx, session, sourceID)
	if err != nil {
		return websiteapi.MediaJob{}, mapCoreError(err)
	}
	if source.ID != sourceID || !sha256HexPattern.MatchString(source.SHA256) || source.Size <= 0 {
		return websiteapi.MediaJob{}, websiteapi.ErrInvalid
	}
	fingerprint, err := mediaFingerprint(sourceID, strings.ToLower(source.SHA256), snapshotID, placementID, request.FieldPath, profileDigest, s.config.MediaProcessorVersion)
	if err != nil {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	fingerprintKey := websiterepo.MediaIdempotencyKey("fingerprint/" + fingerprint)
	callerKey := websiterepo.MediaIdempotencyKey("caller/" + request.IdempotencyKey)
	if existingID, found, err := s.readMediaReceipt(ctx, fingerprintKey, fingerprint, profileDigest, s.config.MediaProcessorVersion); err != nil {
		return websiteapi.MediaJob{}, err
	} else if found {
		return s.loadMediaJob(ctx, existingID)
	}
	if _, found, err := s.readMediaReceipt(ctx, callerKey, "", profileDigest, s.config.MediaProcessorVersion); err != nil {
		return websiteapi.MediaJob{}, err
	} else if found {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	if s.store.MaxBatchSize() < 3 {
		return websiteapi.MediaJob{}, websiteapi.ErrTooLarge
	}

	jobID := uuid.NewString()
	now := time.Now().UTC()
	record := mediaJobRecord{
		ID: jobID, SnapshotID: snapshotID, PlacementID: placementID, FieldPath: request.FieldPath,
		SourceFileID: sourceID, SourceSHA256: strings.ToLower(source.SHA256), FeatureID: featureID, Version: version,
		ProfileDigest: profileDigest, ProcessorVersion: s.config.MediaProcessorVersion, Profile: profileJSON,
		Outputs: outputNames, Fingerprint: fingerprint, State: mediaStateQueued, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	jobKey := websiterepo.MediaJobKey(jobID)
	payload, index, err := mediaModuleDataValue(record, 1)
	if err != nil {
		return websiteapi.MediaJob{}, err
	}
	fingerprintReceipt, err := mediaReceiptValue(fingerprint, jobID, profileDigest, s.config.MediaProcessorVersion)
	if err != nil {
		return websiteapi.MediaJob{}, err
	}
	callerReceipt, err := mediaReceiptValue("", jobID, profileDigest, s.config.MediaProcessorVersion)
	if err != nil {
		return websiteapi.MediaJob{}, err
	}
	mutations := []recorddata.RecordMutation{
		{Key: jobKey, ExpectedRevision: 0, Value: &recorddata.RecordValue{Payload: payload, PayloadVersion: mediaPayloadVersion, IndexData: index}},
		{Key: fingerprintKey, ExpectedRevision: 0, Value: fingerprintReceipt},
		{Key: callerKey, ExpectedRevision: 0, Value: callerReceipt},
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		mapped := mapCoreError(err)
		if errors.Is(mapped, websiteapi.ErrConflict) {
			if existingID, found, readErr := s.readMediaReceipt(ctx, fingerprintKey, fingerprint, profileDigest, s.config.MediaProcessorVersion); readErr == nil && found {
				return s.loadMediaJob(ctx, existingID)
			}
		}
		return websiteapi.MediaJob{}, mapped
	}
	// Core assigns an opaque revision to every committed record. The first CAS
	// after creation must use that revision, never the payload's initial value.
	stored, err := s.store.Get(ctx, jobKey)
	if err != nil || stored.Revision < 1 {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	revision := stored.Revision
	record.Revision = revision

	startPayload, err := json.Marshal(mediaWorkflowPayload{
		Version: mediaPayloadVersion, JobID: jobID, SnapshotID: snapshotID, PlacementID: placementID,
		FieldPath: request.FieldPath, SourceFileID: sourceID, SourceSHA256: strings.ToLower(source.SHA256),
		ProfileDigest: profileDigest, ProcessorVersion: s.config.MediaProcessorVersion,
		Profile: profileJSON, Outputs: outputNames, InputDigest: fingerprint,
	})
	if err != nil || len(startPayload) > s.store.MaxPayloadBytes() {
		job, _ := s.failMedia(ctx, mediaState{record: record, key: jobKey, revision: revision}, "invalid_workflow_payload")
		return job, websiteapi.ErrUnavailable
	}
	receipt, err := s.config.MediaWorkflow.Start(ctx, platform.WorkflowStartRequest{
		OperationID: s.config.MediaOperationID, ContractVersion: s.config.MediaContractVersion,
		IdempotencyKey: "media/" + jobID, Payload: startPayload,
	})
	if err != nil {
		job, _ := s.failMedia(ctx, mediaState{record: record, key: jobKey, revision: revision}, "media_workflow_start_failed")
		return job, websiteapi.ErrUnavailable
	}
	if receipt.WorkflowID == uuid.Nil || receipt.RootTaskID == uuid.Nil {
		job, _ := s.failMedia(ctx, mediaState{record: record, key: jobKey, revision: revision}, "invalid_workflow_receipt")
		return job, websiteapi.ErrUnavailable
	}
	record.State = mediaStateRunning
	record.WorkflowID = receipt.WorkflowID.String()
	record.RootTaskID = receipt.RootTaskID.String()
	record.UpdatedAt = time.Now().UTC()
	if err := s.putMediaRecord(ctx, jobKey, revision, record); err != nil {
		current, loadErr := s.loadMediaState(ctx, jobID)
		if loadErr != nil {
			return mediaJobFromRecord(record), nil
		}
		return mediaJobFromRecord(current.record), nil
	}
	return mediaJobFromRecord(record), nil
}

// GetMediaJob reconciles one durable media job. Outputs are exposed only after
// every child task has been verified; transient coordinator reads never mutate
// the job or its previous field value.
func (s *Service) GetMediaJob(ctx context.Context, jobID string) (websiteapi.MediaJob, error) {
	if err := s.requireLevel(ctx, platform.AccessLevelRead); err != nil {
		return websiteapi.MediaJob{}, err
	}
	if !validInterface(s.config.MediaWorkflow) || !validInterface(s.config.MediaFiles) {
		return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
	}
	state, err := s.loadMediaState(ctx, jobID)
	if err != nil {
		return websiteapi.MediaJob{}, err
	}
	switch state.record.State {
	case mediaStateReady, mediaStateRejected, mediaStateCancelled, mediaStateRetryFailed:
		return mediaJobFromRecord(state.record), nil
	}
	workflowID, err := uuid.Parse(state.record.WorkflowID)
	if err != nil || workflowID == uuid.Nil {
		return s.failMedia(ctx, state, "invalid_workflow_identity")
	}
	rootID, err := uuid.Parse(state.record.RootTaskID)
	if err != nil || rootID == uuid.Nil {
		return s.failMedia(ctx, state, "invalid_task_identity")
	}
	workflow, err := s.config.MediaWorkflow.Get(ctx, workflowID)
	if err != nil {
		return s.failMedia(ctx, state, mediaErrorCode(err))
	}
	if workflow.ID != uuid.Nil && workflow.ID != workflowID || workflow.RootTaskID != uuid.Nil && workflow.RootTaskID != rootID {
		return s.failMedia(ctx, state, "workflow_identity_mismatch")
	}
	switch workflow.State {
	case "queued", "running", "succeeded":
	case "failed":
		return s.failMedia(ctx, state, "media_workflow_failed")
	case "cancelled":
		updated := state.record
		updated.State = mediaStateCancelled
		updated.ErrorCode = "media_workflow_cancelled"
		updated.Revision = state.revision
		updated.UpdatedAt = time.Now().UTC()
		if err := s.putMediaRecord(ctx, state.key, state.revision, updated); err != nil {
			return mediaJobFromRecord(state.record), nil
		}
		return mediaJobFromRecord(updated), nil
	default:
		return s.failMedia(ctx, state, "invalid_workflow_state")
	}

	if len(state.record.TaskIDs) == 0 {
		selector, err := parseMediaRootResult(workflow.Result, state.record, s.store.MaxPayloadBytes())
		if err != nil {
			if errors.Is(err, errMediaInvalidResult) && workflow.State != "succeeded" {
				return mediaJobFromRecord(state.record), nil
			}
			return s.failMedia(ctx, state, "invalid_root_result")
		}
		taskIDs := make(map[string]string, len(selector.EmissionKeys))
		for _, name := range selector.OutputNames {
			emissionKey, ok := selector.EmissionKeys[name]
			if !ok || emissionKey == "" {
				return s.failMedia(ctx, state, "invalid_root_result")
			}
			taskID, err := s.config.MediaWorkflow.ResolveTaskID(ctx, workflowID, emissionKey)
			if err != nil {
				if errors.Is(err, platform.ErrTaskMissing) && workflow.State != "succeeded" {
					return mediaJobFromRecord(state.record), nil
				}
				return s.failMedia(ctx, state, "media_task_missing")
			}
			if taskID == uuid.Nil {
				return s.failMedia(ctx, state, "invalid_task_identity")
			}
			taskIDs[name] = taskID.String()
		}
		updated := state.record
		updated.TaskIDs = taskIDs
		updated.State = mediaStateRunning
		updated.Revision = state.revision
		updated.UpdatedAt = time.Now().UTC()
		if err := s.putMediaRecord(ctx, state.key, state.revision, updated); err != nil {
			current, loadErr := s.loadMediaState(ctx, state.record.ID)
			if loadErr == nil {
				return mediaJobFromRecord(current.record), nil
			}
		} else {
			state.record = updated
			state.revision = state.record.Revision
		}
	}

	if len(state.record.TaskIDs) != len(state.record.Outputs) {
		return s.failMedia(ctx, state, "invalid_task_graph")
	}
	session, ok := importSession(ctx)
	if !ok {
		return websiteapi.MediaJob{}, websiteapi.ErrUnauthorized
	}
	ready := make(map[string]mediaOutputRecord, len(state.record.Outputs))
	for _, name := range state.record.Outputs {
		taskID, err := uuid.Parse(state.record.TaskIDs[name])
		if err != nil || taskID == uuid.Nil {
			return s.failMedia(ctx, state, "invalid_task_identity")
		}
		result, err := s.config.MediaWorkflow.GetTaskResult(ctx, workflowID, taskID)
		if err != nil {
			if errors.Is(err, platform.ErrTaskMissing) && workflow.State != "succeeded" {
				return mediaJobFromRecord(state.record), nil
			}
			return s.failMedia(ctx, state, "media_result_missing")
		}
		decoded, err := parseMediaWorkerResult(result, state.record, name, workflowID, taskID, s.store.MaxPayloadBytes())
		if err != nil {
			if errors.Is(err, errMediaInvalidResult) && workflow.State != "succeeded" && result.State != "succeeded" {
				return mediaJobFromRecord(state.record), nil
			}
			return s.failMedia(ctx, state, "invalid_task_result")
		}
		file, err := s.config.MediaFiles.StatFile(ctx, session, decoded.FileID)
		if err != nil || file.ID != decoded.FileID || !strings.EqualFold(file.SHA256, decoded.Checksum) ||
			file.ContentType != decoded.ContentType || file.Size != decoded.Size {
			return s.failMedia(ctx, state, "invalid_output_file")
		}
		ready[name] = mediaOutputRecord{FileID: decoded.FileID, Checksum: strings.ToLower(decoded.Checksum),
			ContentType: decoded.ContentType, Size: decoded.Size}
	}
	updated := state.record
	updated.State = mediaStateReady
	updated.ReadyOutputs = ready
	updated.ErrorCode = ""
	updated.Revision = state.revision
	updated.UpdatedAt = time.Now().UTC()
	if err := s.putMediaRecord(ctx, state.key, state.revision, updated); err != nil {
		current, loadErr := s.loadMediaState(ctx, state.record.ID)
		if loadErr == nil {
			return mediaJobFromRecord(current.record), nil
		}
	}
	return mediaJobFromRecord(updated), nil
}

// validMediaFieldPath matches the declaration path accepted by the manifest
// resolver: /input or /input/field, without traversal or control characters.
func validMediaFieldPath(path string) bool {
	if path == "" || len(path) > 512 || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 1 || len(segments) > 2 {
		return false
	}
	for _, segment := range segments {
		if !featureruntime.ValidMediaFieldSegment(segment) {
			return false
		}
	}
	return true
}

func mediaFingerprint(values ...string) (string, error) {
	for _, value := range values {
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return "", websiteapi.ErrInvalid
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func parseMediaRootResult(raw []byte, record mediaJobRecord, limit int) (mediaRootResult, error) {
	if len(raw) == 0 || limit <= 0 || len(raw) > limit {
		return mediaRootResult{}, errMediaInvalidResult
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result mediaRootResult
	if err := decoder.Decode(&result); err != nil {
		return mediaRootResult{}, errMediaInvalidResult
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return mediaRootResult{}, errMediaInvalidResult
	}
	if result.Format != mediaRootFormat || result.JobID != record.ID || result.ProfileDigest != record.ProfileDigest ||
		result.ProcessorVersion != record.ProcessorVersion || result.InputDigest != record.Fingerprint ||
		len(result.OutputNames) != len(record.Outputs) || len(result.EmissionKeys) != len(record.Outputs) {
		return mediaRootResult{}, errMediaInvalidResult
	}
	for i, name := range result.OutputNames {
		if name != record.Outputs[i] || !identifierPattern.MatchString(name) {
			return mediaRootResult{}, errMediaInvalidResult
		}
	}
	return result, nil
}

func parseMediaWorkerResult(result platform.TaskResult, record mediaJobRecord, outputName string, workflowID, taskID uuid.UUID, limit int) (mediaWorkerResult, error) {
	if result.WorkflowID != uuid.Nil && result.WorkflowID != workflowID ||
		result.TaskID != uuid.Nil && result.TaskID != taskID ||
		result.State != "" && result.State != "succeeded" ||
		result.Operation != nil && (result.Operation.ClassID != mediaWorkerComponent ||
			result.Operation.ID != mediaWorkerOperation || result.Operation.Version != mediaprofile.WorkContractVersion) ||
		len(result.Result) == 0 || len(result.Result) > limit {
		return mediaWorkerResult{}, errMediaInvalidResult
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Result))
	decoder.DisallowUnknownFields()
	var decoded mediaWorkerResult
	if err := decoder.Decode(&decoded); err != nil {
		return mediaWorkerResult{}, errMediaInvalidResult
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return mediaWorkerResult{}, errMediaInvalidResult
	}
	if decoded.Version != mediaWorkerResultVer || !decoded.Ready || decoded.OutputName != outputName ||
		!snapshotIDPattern.MatchString(decoded.FileID) || !sha256HexPattern.MatchString(decoded.Checksum) ||
		decoded.ContentType == "" || decoded.Size <= 0 || decoded.ProfileDigest != record.ProfileDigest ||
		decoded.ProcessorVersion != record.ProcessorVersion || decoded.Fingerprint != record.Fingerprint {
		return mediaWorkerResult{}, errMediaInvalidResult
	}
	return decoded, nil
}

func mediaErrorCode(err error) string {
	switch {
	case errors.Is(err, platform.ErrTaskMissing):
		return "media_task_missing"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "media_timeout"
	default:
		return "media_workflow_unavailable"
	}
}

func (s *Service) failMedia(ctx context.Context, state mediaState, code string) (websiteapi.MediaJob, error) {
	if state.record.State == mediaStateRetryFailed || state.record.State == mediaStateRejected || state.record.State == mediaStateCancelled {
		return mediaJobFromRecord(state.record), nil
	}
	updated := state.record
	updated.State = mediaStateRetryFailed
	updated.ErrorCode = code
	updated.Revision = state.revision
	updated.UpdatedAt = time.Now().UTC()
	if err := s.putMediaRecord(ctx, state.key, state.revision, updated); err != nil {
		return mediaJobFromRecord(state.record), err
	}
	return mediaJobFromRecord(updated), nil
}

func (s *Service) readMediaReceipt(ctx context.Context, key recorddata.RecordKey, fingerprint, profileDigest, processorVersion string) (string, bool, error) {
	record, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, recorddata.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, mapCoreError(err)
	}
	if record.PayloadVersion != mediaPayloadVersion || record.Revision < 1 {
		return "", false, websiteapi.ErrUnavailable
	}
	var receipt mediaReceipt
	if json.Unmarshal(record.Payload, &receipt) != nil || receipt.RecordType != "media_job_idempotency" ||
		!snapshotIDPattern.MatchString(receipt.JobID) || !sha256HexPattern.MatchString(receipt.ProfileDigest) ||
		receipt.ProcessorVersion != processorVersion {
		return "", false, websiteapi.ErrUnavailable
	}
	if receipt.ProfileDigest != profileDigest {
		return "", false, websiteapi.ErrConflict
	}
	if fingerprint != "" && receipt.Fingerprint != fingerprint {
		return "", false, websiteapi.ErrConflict
	}
	return receipt.JobID, true, nil
}

func (s *Service) loadMediaState(ctx context.Context, jobID string) (mediaState, error) {
	id := strings.ToLower(strings.TrimSpace(jobID))
	if !snapshotIDPattern.MatchString(id) {
		return mediaState{}, websiteapi.ErrInvalid
	}
	key := websiterepo.MediaJobKey(id)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, recorddata.ErrRecordNotFound) {
			return mediaState{}, websiteapi.ErrNotFound
		}
		return mediaState{}, mapCoreError(err)
	}
	if record.PayloadVersion != mediaPayloadVersion || record.Revision < 1 {
		return mediaState{}, websiteapi.ErrUnavailable
	}
	var stored mediaJobRecord
	var index mediaIndex
	if json.Unmarshal(record.Payload, &stored) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "media_job" || index.Revision < 1 || index.State != stored.State ||
		index.SourceFileID != stored.SourceFileID || index.ProfileDigest != stored.ProfileDigest ||
		stored.ID != id || !sha256HexPattern.MatchString(stored.Fingerprint) ||
		!sha256HexPattern.MatchString(stored.ProfileDigest) || stored.ProcessorVersion == "" || len(stored.Outputs) == 0 {
		return mediaState{}, websiteapi.ErrUnavailable
	}
	stored.Revision = record.Revision
	return mediaState{record: stored, key: key, revision: record.Revision}, nil
}

func (s *Service) loadMediaJob(ctx context.Context, jobID string) (websiteapi.MediaJob, error) {
	state, err := s.loadMediaState(ctx, jobID)
	if err != nil {
		return websiteapi.MediaJob{}, err
	}
	return mediaJobFromRecord(state.record), nil
}

func mediaJobFromRecord(record mediaJobRecord) websiteapi.MediaJob {
	outputs := make(map[string]string, len(record.ReadyOutputs))
	if record.State == mediaStateReady {
		for name, output := range record.ReadyOutputs {
			outputs[name] = output.FileID
		}
	}
	return websiteapi.MediaJob{
		ID: record.ID, SourceFileID: record.SourceFileID, ProfileDigest: record.ProfileDigest,
		ProcessorVersion: record.ProcessorVersion, State: record.State, Outputs: outputs,
		ErrorCode: record.ErrorCode, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func mediaModuleDataValue(record mediaJobRecord, revision int64) ([]byte, []byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, nil, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(mediaIndex{RecordType: "media_job", State: record.State,
		SourceFileID: record.SourceFileID, ProfileDigest: record.ProfileDigest,
		ProcessorVersion: record.ProcessorVersion, Revision: revision})
	if err != nil {
		return nil, nil, websiteapi.ErrUnavailable
	}
	if len(payload) > 1<<20 || len(index) > 1<<16 {
		return nil, nil, websiteapi.ErrTooLarge
	}
	return payload, index, nil
}

func mediaReceiptValue(fingerprint, jobID, profileDigest, processorVersion string) (*recorddata.RecordValue, error) {
	payload, err := json.Marshal(mediaReceipt{RecordType: "media_job_idempotency", JobID: jobID,
		Fingerprint: fingerprint, ProfileDigest: profileDigest, ProcessorVersion: processorVersion, State: "accepted"})
	if err != nil {
		return nil, err
	}
	return &recorddata.RecordValue{Payload: payload, PayloadVersion: mediaPayloadVersion,
		IndexData: json.RawMessage(`{}`)}, nil
}

func (s *Service) putMediaRecord(ctx context.Context, key recorddata.RecordKey, expected int64, record mediaJobRecord) error {
	payload, index, err := mediaModuleDataValue(record, record.Revision)
	if err != nil {
		return err
	}
	return s.store.Batch(ctx, []recorddata.RecordMutation{{
		Key:              key,
		ExpectedRevision: expected,
		Value:            &recorddata.RecordValue{Payload: payload, PayloadVersion: mediaPayloadVersion, IndexData: index},
	}})
}
