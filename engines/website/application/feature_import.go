package application

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	"github.com/google/uuid"
)

const (
	importPayloadVersion     = 1
	importStateQueued        = "queued"
	importStateRunning       = "running"
	importStateReady         = "ready"
	importStateFailed        = "failed"
	importDefaultOperationID = "feature-import"
	importContractVersion    = 1
	importLeaseDuration      = time.Minute
	importManifestLimit      = 1 << 16
	importSourceReadLimit    = 1 << 20
	importWorkerComponentID  = "worker.archive"
	importWorkerOperationID  = "feature-import"
	importWorkerContractVer  = 1
)

var errImportInvalidResult = errors.New("invalid feature import workflow result")

// importRecord is the Website-owned durable job. Workflow identities, target
// FileSet and operation selection are pinned by trusted configuration; a client
// request supplies only an opaque source file ID and an idempotency key.
type importRecord struct {
	ID            string      `json:"id"`
	SourceFileID  string      `json:"source_file_id"`
	Fingerprint   string      `json:"fingerprint"`
	SourceDigest  string      `json:"source_digest,omitempty"`
	FeatureID     string      `json:"feature_id,omitempty"`
	Version       string      `json:"version,omitempty"`
	FileSetID     string      `json:"file_set_id,omitempty"`
	EntryPaths    []string    `json:"entry_paths,omitempty"`
	Manifest      []byte      `json:"manifest,omitempty"`
	State         string      `json:"state"`
	WorkflowID    string      `json:"workflow_id,omitempty"`
	RootTaskID    string      `json:"root_task_id,omitempty"`
	StepIDs       []string    `json:"step_ids,omitempty"`
	Lease         importLease `json:"lease"`
	ArtifactFiles []string    `json:"artifact_files,omitempty"`
	ErrorCode     string      `json:"error_code,omitempty"`
	SourceDeleted bool        `json:"source_deleted"`
	Revision      int64       `json:"revision"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type importLease struct {
	Worker    string    `json:"worker,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type importIndex struct {
	RecordType   string `json:"record_type"`
	State        string `json:"state"`
	SourceFileID string `json:"source_file_id,omitempty"`
	FeatureID    string `json:"feature_id,omitempty"`
	Revision     int64  `json:"revision"`
}

type importReceipt struct {
	RecordType  string `json:"record_type"`
	JobID       string `json:"job_id"`
	Fingerprint string `json:"fingerprint"`
	State       string `json:"state"`
}

// importWorkflowPayload deliberately carries only the opaque Core file ID.
// The registered archive worker derives its task identity from its own durable
// task and rejects any additional wire fields.
type importWorkflowPayload struct {
	SourceFileID string `json:"source_file_id"`
}

type importWorkerResult struct {
	Ready   bool     `json:"ready"`
	FileIDs []string `json:"file_ids,omitempty"`
}

type importState struct {
	record   importRecord
	key      core.ModuleDataKey
	revision int64
}

type featureVersionRecord struct {
	RecordType   string            `json:"record_type"`
	FeatureID    string            `json:"feature_id"`
	Version      string            `json:"version"`
	Manifest     []byte            `json:"manifest"`
	FileSetID    string            `json:"file_set_id,omitempty"`
	Files        map[string]string `json:"files"`
	Digest       string            `json:"digest"`
	ArtifactSize int64             `json:"artifact_size"`
	State        string            `json:"state"`
	CreatedAt    time.Time         `json:"created_at"`
}

// BeginFeatureImport validates the request, commits one CAS-protected job with
// its idempotency receipt, and starts the composition-owned archive workflow.
func (s *Service) BeginFeatureImport(ctx context.Context, request websiteapi.ImportFeatureRequest) (websiteapi.FeatureImport, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.FeatureImport{}, err
	}
	if err := s.requireCoordinatorWrite(ctx); err != nil {
		return websiteapi.FeatureImport{}, err
	}
	if !validInterface(s.config.Files) || !validInterface(s.config.Workflow) ||
		!identifierPattern.MatchString(s.config.FeatureImportOperationID) || s.config.FeatureImportContractVersion < 1 {
		return websiteapi.FeatureImport{}, websiteapi.ErrUnavailable
	}

	sourceID := strings.ToLower(strings.TrimSpace(request.SourceFileID))
	parsedSource, err := uuid.Parse(sourceID)
	if err != nil || parsedSource == uuid.Nil || !identifierPattern.MatchString(request.IdempotencyKey) {
		return websiteapi.FeatureImport{}, websiteapi.ErrInvalid
	}
	digest := strings.ToLower(strings.TrimSpace(request.ExpectedSHA256))
	if digest != "" && !sha256HexPattern.MatchString(digest) {
		return websiteapi.FeatureImport{}, websiteapi.ErrInvalid
	}
	fingerprint := importFingerprint(sourceID, digest)

	receiptKey := websiterepo.FeatureImportReceiptKey(request.IdempotencyKey)
	if existingID, found, err := s.readImportReceipt(ctx, receiptKey, fingerprint); err != nil {
		return websiteapi.FeatureImport{}, err
	} else if found {
		return s.loadFeatureImport(ctx, existingID)
	}
	session, ok := importSession(ctx)
	if !ok {
		return websiteapi.FeatureImport{}, websiteapi.ErrUnauthorized
	}
	source, err := s.config.Files.StatFile(ctx, session, sourceID)
	if err != nil {
		return websiteapi.FeatureImport{}, mapCoreError(err)
	}
	if source.ID != sourceID || source.SHA256 == "" || source.Size <= 0 {
		return websiteapi.FeatureImport{}, websiteapi.ErrInvalid
	}
	if digest != "" && !strings.EqualFold(source.SHA256, digest) {
		return websiteapi.FeatureImport{}, websiteapi.ErrInvalid
	}
	readLimit := int64(s.store.MaxPayloadBytes())
	if readLimit > importSourceReadLimit {
		readLimit = importSourceReadLimit
	}
	featureID, version, manifest, entryPaths := s.inspectImportSource(ctx, session, sourceID, readLimit)
	if s.store.MaxBatchSize() < 2 {
		return websiteapi.FeatureImport{}, websiteapi.ErrTooLarge
	}

	jobID, err := randomUUID()
	if err != nil {
		return websiteapi.FeatureImport{}, websiteapi.ErrUnavailable
	}
	now := time.Now().UTC()
	record := importRecord{
		ID: jobID, SourceFileID: sourceID, Fingerprint: fingerprint, SourceDigest: strings.ToLower(source.SHA256),
		FeatureID: featureID, Version: version, FileSetID: s.config.FeatureImportFileSetID,
		EntryPaths: entryPaths, Manifest: manifest, State: importStateQueued,
		Lease:    importLease{ExpiresAt: now.Add(importLeaseDuration)},
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	jobKey := websiterepo.FeatureImportKey(jobID)
	payload, index, err := importModuleDataValue(record)
	if err != nil {
		return websiteapi.FeatureImport{}, err
	}
	receiptValue, err := importReceiptValue(record, fingerprint)
	if err != nil {
		return websiteapi.FeatureImport{}, err
	}
	mutations := []core.ModuleDataMutation{
		{Key: jobKey, ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: payload, PayloadVersion: importPayloadVersion, IndexData: index}},
		{Key: receiptKey, ExpectedRevision: 0, Value: receiptValue},
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		mapped := mapCoreError(err)
		if errors.Is(mapped, websiteapi.ErrConflict) {
			if existingID, found, readErr := s.readImportReceipt(ctx, receiptKey, fingerprint); readErr == nil && found {
				return s.loadFeatureImport(ctx, existingID)
			}
		}
		return websiteapi.FeatureImport{}, mapped
	}

	// Core revisions are global sequence values. Read the committed envelope
	// back so the workflow transition CAS uses the real revision, not the local
	// transition counter carried in the payload.
	state, err := s.loadImportState(ctx, jobID)
	if err != nil {
		return websiteapi.FeatureImport{}, err
	}
	record = state.record

	startPayload, err := json.Marshal(importWorkflowPayload{SourceFileID: sourceID})
	if err != nil || len(startPayload) > s.store.MaxPayloadBytes() {
		job, _ := s.failImport(ctx, state, "invalid_workflow_payload")
		return job, websiteapi.ErrUnavailable
	}
	receipt, err := s.config.Workflow.Start(ctx, core.WorkflowStartRequest{
		OperationID: s.config.FeatureImportOperationID, ContractVersion: s.config.FeatureImportContractVersion,
		IdempotencyKey: "feature-import/" + jobID, Payload: startPayload,
	})
	if err != nil {
		job, _ := s.failImport(ctx, state, "import_workflow_start_failed")
		return job, websiteapi.ErrUnavailable
	}
	if receipt.WorkflowID == uuid.Nil || receipt.RootTaskID == uuid.Nil {
		job, _ := s.failImport(ctx, state, "invalid_workflow_receipt")
		return job, websiteapi.ErrUnavailable
	}

	record.State = importStateRunning
	record.WorkflowID = receipt.WorkflowID.String()
	record.RootTaskID = receipt.RootTaskID.String()
	record.StepIDs = []string{receipt.RootTaskID.String()}
	record.Lease = importLease{Worker: receipt.WorkflowID.String(), ExpiresAt: now.Add(importLeaseDuration)}
	record.Revision = state.record.Revision + 1
	record.UpdatedAt = time.Now().UTC()
	if err := s.putImportRecord(ctx, state.key, state.revision, record); err != nil {
		current, loadErr := s.loadImportState(ctx, jobID)
		if loadErr != nil {
			return importJobFromRecord(record), nil
		}
		return importJobFromRecord(current.record), nil
	}
	return importJobFromRecord(record), nil
}

// GetFeatureImport reconciles one durable import. A catalog version is created
// only after the archive task proves every output and the source cleanup are
// complete; transient coordinator reads leave the record untouched.
func (s *Service) GetFeatureImport(ctx context.Context, jobID string) (websiteapi.FeatureImport, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.FeatureImport{}, err
	}
	if !validInterface(s.config.Workflow) {
		return websiteapi.FeatureImport{}, websiteapi.ErrUnavailable
	}
	state, err := s.loadImportState(ctx, jobID)
	if err != nil {
		return websiteapi.FeatureImport{}, err
	}
	if state.record.State == importStateReady || state.record.State == importStateFailed {
		return importJobFromRecord(state.record), nil
	}
	workflowID, err := uuid.Parse(state.record.WorkflowID)
	if err != nil || workflowID == uuid.Nil {
		return s.failImport(ctx, state, "invalid_workflow_identity")
	}
	rootID, err := uuid.Parse(state.record.RootTaskID)
	if err != nil || rootID == uuid.Nil {
		return s.failImport(ctx, state, "invalid_task_identity")
	}
	workflow, err := s.config.Workflow.Get(ctx, workflowID)
	if err != nil {
		return s.failImport(ctx, state, importWorkflowErrorCode(err))
	}
	if workflow.ID != uuid.Nil && workflow.ID != workflowID || workflow.RootTaskID != uuid.Nil && workflow.RootTaskID != rootID {
		return s.failImport(ctx, state, "workflow_identity_mismatch")
	}

	switch workflow.State {
	case "queued", "running":
		result, err := s.config.Workflow.GetTaskResult(ctx, workflowID, rootID)
		if err != nil {
			if errors.Is(err, core.ErrTaskMissing) || errors.Is(err, core.ErrModuleDataNotFound) {
				return importJobFromRecord(state.record), nil
			}
			return s.failImport(ctx, state, importTaskErrorCode(err))
		}
		return s.reconcileImportResult(ctx, state, workflowID, rootID, result, false)
	case "succeeded":
		result, err := s.config.Workflow.GetTaskResult(ctx, workflowID, rootID)
		if err != nil {
			return s.failImport(ctx, state, "import_result_missing")
		}
		return s.reconcileImportResult(ctx, state, workflowID, rootID, result, true)
	case "failed":
		return s.failImport(ctx, state, "import_workflow_failed")
	case "cancelled":
		return s.failImport(ctx, state, "import_workflow_cancelled")
	default:
		return s.failImport(ctx, state, "invalid_workflow_state")
	}
}

func (s *Service) reconcileImportResult(ctx context.Context, state importState, workflowID, rootID uuid.UUID, result core.TaskResult, terminal bool) (websiteapi.FeatureImport, error) {
	// A retried durable task can leave a failed attempt result while the
	// workflow is still active. That result is not an import decision.
	if !terminal && result.State != "" && result.State != "succeeded" {
		return importJobFromRecord(state.record), nil
	}
	if result.WorkflowID != uuid.Nil && result.WorkflowID != workflowID ||
		result.TaskID != uuid.Nil && result.TaskID != rootID ||
		result.State != "" && result.State != "succeeded" ||
		result.Worker != nil && (result.Worker.ComponentID != importWorkerComponentID ||
			result.Worker.OperationID != importWorkerOperationID || result.Worker.ContractVersion != importWorkerContractVer) {
		return s.failImport(ctx, state, "invalid_task_result")
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Result))
	decoder.DisallowUnknownFields()
	var decoded importWorkerResult
	if err := decoder.Decode(&decoded); err != nil {
		return s.failImport(ctx, state, "invalid_task_result")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return s.failImport(ctx, state, "invalid_task_result")
	}
	if !decoded.Ready || len(decoded.FileIDs) == 0 {
		if terminal {
			return s.failImport(ctx, state, "incomplete_import_result")
		}
		return importJobFromRecord(state.record), nil
	}
	seen := make(map[string]struct{}, len(decoded.FileIDs))
	ordered := make([]string, 0, len(decoded.FileIDs))
	for _, value := range decoded.FileIDs {
		id := strings.ToLower(strings.TrimSpace(value))
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return s.failImport(ctx, state, "invalid_task_result")
		}
		if _, exists := seen[id]; exists {
			return s.failImport(ctx, state, "invalid_task_result")
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}

	session, ok := importSession(ctx)
	if !ok {
		return websiteapi.FeatureImport{}, websiteapi.ErrUnauthorized
	}
	if _, err := s.config.Files.StatFile(ctx, session, state.record.SourceFileID); err == nil {
		// The durable worker sets Ready only after deletion, but a stale read
		// must never publish a version while the source is still present.
		return importJobFromRecord(state.record), nil
	} else if !errors.Is(err, core.ErrFileNotFound) {
		return importJobFromRecord(state.record), nil
	}
	if state.record.FeatureID == "" || state.record.Version == "" {
		return s.failImport(ctx, state, "invalid_feature_identity")
	}
	if err := s.publishFeatureVersion(ctx, state.record, ordered); err != nil {
		if errors.Is(err, websiteapi.ErrConflict) {
			return s.failImport(ctx, state, "feature_version_conflict")
		}
		return importJobFromRecord(state.record), nil
	}

	updated := state.record
	updated.State = importStateReady
	updated.SourceDeleted = true
	updated.ArtifactFiles = ordered
	updated.ErrorCode = ""
	updated.Lease = importLease{}
	updated.Revision = state.record.Revision + 1
	updated.UpdatedAt = time.Now().UTC()
	if err := s.putImportRecord(ctx, state.key, state.revision, updated); err != nil {
		current, loadErr := s.loadImportState(ctx, state.record.ID)
		if loadErr == nil {
			return importJobFromRecord(current.record), nil
		}
		return importJobFromRecord(updated), nil
	}
	return importJobFromRecord(updated), nil
}

func (s *Service) publishFeatureVersion(ctx context.Context, record importRecord, fileIDs []string) error {
	key := websiterepo.FeatureVersionKey(record.FeatureID, record.Version)
	files := make(map[string]string, len(fileIDs))
	for index, id := range fileIDs {
		if index < len(record.EntryPaths) {
			files[record.EntryPaths[index]] = id
		} else {
			files[id] = id
		}
	}
	value := featureVersionRecord{
		RecordType: "feature_version", FeatureID: record.FeatureID, Version: record.Version,
		Manifest: append([]byte(nil), record.Manifest...), FileSetID: record.FileSetID, Files: files,
		Digest: record.SourceDigest, ArtifactSize: int64(len(record.Manifest)), State: importStateReady, CreatedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(struct {
		RecordType string `json:"record_type"`
		FeatureID  string `json:"feature_id"`
		Version    string `json:"version"`
		State      string `json:"state"`
	}{value.RecordType, value.FeatureID, value.Version, value.State})
	if err != nil {
		return websiteapi.ErrUnavailable
	}
	if len(payload) > s.store.MaxPayloadBytes() {
		return websiteapi.ErrTooLarge
	}
	candidate := &core.ModuleDataValue{Payload: payload, PayloadVersion: importPayloadVersion, IndexData: index}
	if err := s.store.Batch(ctx, []core.ModuleDataMutation{{Key: key, ExpectedRevision: 0, Value: candidate}}); err != nil {
		mapped := mapCoreError(err)
		if !errors.Is(mapped, websiteapi.ErrConflict) {
			return mapped
		}
		stored, getErr := s.store.Get(ctx, key)
		if getErr != nil || stored.PayloadVersion != importPayloadVersion {
			return websiteapi.ErrUnavailable
		}
		var existing featureVersionRecord
		if json.Unmarshal(stored.Payload, &existing) != nil || existing.FeatureID != value.FeatureID || existing.Version != value.Version || existing.Digest != value.Digest {
			return websiteapi.ErrConflict
		}
	}
	return nil
}

func (s *Service) failImport(ctx context.Context, state importState, code string) (websiteapi.FeatureImport, error) {
	if state.record.State == importStateFailed {
		return importJobFromRecord(state.record), nil
	}
	updated := state.record
	updated.State = importStateFailed
	updated.ErrorCode = code
	updated.Lease = importLease{}
	updated.Revision = state.record.Revision + 1
	updated.UpdatedAt = time.Now().UTC()
	if err := s.putImportRecord(ctx, state.key, state.revision, updated); err != nil {
		return importJobFromRecord(state.record), err
	}
	return importJobFromRecord(updated), nil
}

func (s *Service) readImportReceipt(ctx context.Context, key core.ModuleDataKey, fingerprint string) (string, bool, error) {
	record, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return "", false, nil
		}
		return "", false, mapCoreError(err)
	}
	if record.PayloadVersion != importPayloadVersion || record.Revision < 1 {
		return "", false, websiteapi.ErrUnavailable
	}
	var receipt importReceipt
	if json.Unmarshal(record.Payload, &receipt) != nil || receipt.RecordType != "feature_import_idempotency" ||
		!snapshotIDPattern.MatchString(receipt.JobID) || len(receipt.Fingerprint) != sha256.Size*2 {
		return "", false, websiteapi.ErrUnavailable
	}
	if receipt.Fingerprint != fingerprint {
		return "", false, websiteapi.ErrConflict
	}
	return receipt.JobID, true, nil
}

func (s *Service) loadImportState(ctx context.Context, jobID string) (importState, error) {
	id := strings.ToLower(strings.TrimSpace(jobID))
	if !snapshotIDPattern.MatchString(id) {
		return importState{}, websiteapi.ErrInvalid
	}
	key := websiterepo.FeatureImportKey(id)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return importState{}, websiteapi.ErrNotFound
		}
		return importState{}, mapCoreError(err)
	}
	if record.PayloadVersion != importPayloadVersion || record.Revision < 1 {
		return importState{}, websiteapi.ErrUnavailable
	}
	var stored importRecord
	var index importIndex
	if json.Unmarshal(record.Payload, &stored) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "feature_import" || index.Revision < 1 || index.Revision != stored.Revision ||
		index.State != stored.State || index.SourceFileID != stored.SourceFileID || index.FeatureID != stored.FeatureID ||
		stored.ID != id || len(stored.Fingerprint) != sha256.Size*2 ||
		stored.SourceFileID == "" || !validImportState(stored.State) {
		return importState{}, websiteapi.ErrUnavailable
	}
	return importState{record: stored, key: key, revision: record.Revision}, nil
}

func (s *Service) loadFeatureImport(ctx context.Context, jobID string) (websiteapi.FeatureImport, error) {
	state, err := s.loadImportState(ctx, jobID)
	if err != nil {
		return websiteapi.FeatureImport{}, err
	}
	return importJobFromRecord(state.record), nil
}

func (s *Service) putImportRecord(ctx context.Context, key core.ModuleDataKey, expectedRevision int64, record importRecord) error {
	payload, index, err := importModuleDataValue(record)
	if err != nil {
		return err
	}
	if len(payload) > s.store.MaxPayloadBytes() {
		return websiteapi.ErrTooLarge
	}
	return mapCoreError(s.store.Batch(ctx, []core.ModuleDataMutation{{
		Key: key, ExpectedRevision: expectedRevision,
		Value: &core.ModuleDataValue{Payload: payload, PayloadVersion: importPayloadVersion, IndexData: index},
	}}))
}

func importModuleDataValue(record importRecord) ([]byte, []byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, nil, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(importIndex{RecordType: "feature_import", State: record.State,
		SourceFileID: record.SourceFileID, FeatureID: record.FeatureID, Revision: record.Revision})
	if err != nil {
		return nil, nil, websiteapi.ErrUnavailable
	}
	return payload, index, nil
}

func importReceiptValue(record importRecord, fingerprint string) (*core.ModuleDataValue, error) {
	payload, err := json.Marshal(importReceipt{RecordType: "feature_import_idempotency", JobID: record.ID,
		Fingerprint: fingerprint, State: "created"})
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(importIndex{RecordType: "feature_import_idempotency", State: "created",
		SourceFileID: record.SourceFileID, FeatureID: record.FeatureID, Revision: 1})
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	return &core.ModuleDataValue{Payload: payload, PayloadVersion: importPayloadVersion, IndexData: index}, nil
}

func importFingerprint(sourceFileID, digest string) string {
	sum := sha256.Sum256([]byte(sourceFileID + "\x00" + digest))
	return hex.EncodeToString(sum[:])
}

func importSession(ctx context.Context) (core.Session, bool) {
	current, ok := ctx.Value(actorContextKey{}).(actor)
	return current.session, ok
}

// inspectImportSource extracts only the catalog identity and entry ordering.
// The archive worker remains authoritative for full ZIP and manifest
// verification; a malformed source simply leaves this metadata empty.
func (s *Service) inspectImportSource(ctx context.Context, session core.Session, sourceID string, limit int64) (string, string, []byte, []string) {
	if limit <= 0 {
		return "", "", nil, nil
	}
	reader, _, err := s.config.Files.ReadFile(ctx, session, sourceID)
	if err != nil || reader == nil {
		return "", "", nil, nil
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return "", "", nil, nil
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", nil, nil
	}
	var manifest []byte
	paths := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		paths = append(paths, entry.Name)
		if entry.Name != "manifest.json" || entry.UncompressedSize64 > importManifestLimit {
			continue
		}
		content, err := entry.Open()
		if err != nil {
			continue
		}
		value, readErr := io.ReadAll(io.LimitReader(content, importManifestLimit+1))
		closeErr := content.Close()
		if readErr == nil && closeErr == nil && int64(len(value)) <= importManifestLimit {
			manifest = value
		}
	}
	sort.Strings(paths)
	var identity struct {
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	if len(manifest) == 0 || json.Unmarshal(manifest, &identity) != nil ||
		!identifierPattern.MatchString(identity.Key) || !featureVersionPattern.MatchString(identity.Version) {
		return "", "", nil, paths
	}
	return identity.Key, identity.Version, manifest, paths
}

func validImportState(state string) bool {
	switch state {
	case importStateQueued, importStateRunning, importStateReady, importStateFailed:
		return true
	default:
		return false
	}
}

func importJobFromRecord(record importRecord) websiteapi.FeatureImport {
	return websiteapi.FeatureImport{
		ID: record.ID, SourceFileID: record.SourceFileID, FeatureID: record.FeatureID, Version: record.Version,
		FileSetID: record.FileSetID, State: record.State, ErrorCode: record.ErrorCode,
		SourceDeleted: record.SourceDeleted, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func importWorkflowErrorCode(err error) string {
	switch {
	case errors.Is(err, core.ErrTaskMissing), errors.Is(err, core.ErrModuleDataNotFound):
		return "workflow_not_found"
	case errors.Is(err, core.ErrUnauthorized), errors.Is(err, core.ErrForbidden), errors.Is(err, core.ErrTaskNotOwned):
		return "workflow_access_denied"
	default:
		return "workflow_unavailable"
	}
}

func importTaskErrorCode(err error) string {
	switch {
	case errors.Is(err, core.ErrTaskMissing), errors.Is(err, core.ErrModuleDataNotFound):
		return "task_result_missing"
	case errors.Is(err, core.ErrUnauthorized), errors.Is(err, core.ErrForbidden), errors.Is(err, core.ErrTaskNotOwned):
		return "task_result_access_denied"
	default:
		return "task_result_unavailable"
	}
}
