package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"io"
	"strings"
	"time"

	platform "github.com/Zubrium/cdn/engines/website/application/platform"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
	"github.com/google/uuid"
)

const (
	buildPayloadVersion    = 1
	workflowPayloadVersion = 2
	buildStateQueued       = "queued"
	buildStateResolving    = "resolving"
	buildStatePrerender    = "prerendering"
	buildStatePackaging    = "packaging"
	buildStateVerifying    = "verifying"
	buildStateReady        = "ready"
	buildStateFailed       = "failed"
	buildRootFormat        = "website.build-root/v2"
	buildManifestType      = prerendercontract.ArtifactMediaType
	buildPrerenderWorker   = "worker.prerender"
	buildPrerenderOp       = "render-page"
)

var (
	errBuildInvalidResult = errors.New("invalid build workflow result")
	errBuildStaleInput    = errors.New("stale build input digest")
)

// buildRecord is the versioned Website-owned job payload. Workflow identities
// are pinned as strings; the public BuildJob intentionally does not expose them.
type buildRecord struct {
	ID                     string                         `json:"id"`
	SnapshotID             string                         `json:"snapshot_id"`
	Locale                 string                         `json:"locale"`
	LocaleCatalogRevision  int64                          `json:"locale_catalog_revision"`
	InputDigest            string                         `json:"input_digest"`
	TrustedInput           prerendercontract.TrustedInput `json:"trusted_input"`
	State                  string                         `json:"state"`
	WorkflowID             string                         `json:"workflow_id"`
	RootTaskID             string                         `json:"root_task_id"`
	PrerenderTaskID        string                         `json:"prerender_task_id"`
	ArtifactManifestFileID string                         `json:"artifact_manifest_file_id"`
	ErrorCode              string                         `json:"error_code"`
	CreatedAt              time.Time                      `json:"created_at"`
	UpdatedAt              time.Time                      `json:"updated_at"`
}

type buildState struct {
	record   buildRecord
	key      recorddata.RecordKey
	revision int64
}

type buildReceipt struct {
	RecordType  string `json:"record_type"`
	BuildID     string `json:"build_id"`
	InputDigest string `json:"input_digest"`
	State       string `json:"state"`
}

type buildIndex struct {
	RecordType string `json:"record_type"`
	State      string `json:"state"`
	SnapshotID string `json:"snapshot_id"`
	Revision   int64  `json:"revision"`
}

// buildWorkflowPayload contains only immutable, pinned build inputs. Operation
// selection belongs to Config and is passed as a typed Core start field.
type buildWorkflowPayload struct {
	Version     int                            `json:"version"`
	BuildID     string                         `json:"build_id"`
	Input       prerendercontract.TrustedInput `json:"input"`
	Page        prerendercontract.PagePlan     `json:"page"`
	InputDigest string                         `json:"input_digest"`
}

type buildRootResult struct {
	Format               string `json:"format"`
	BuildID              string `json:"build_id"`
	PrerenderTaskID      string `json:"prerender_task_id"`
	PrerenderEmissionKey string `json:"prerender_emission_key"`
	InputDigest          string `json:"input_digest"`
}

type buildTaskResult struct {
	Format      string            `json:"format"`
	InputDigest string            `json:"input_digest"`
	Manifest    buildManifestFile `json:"manifest"`
}

type buildManifestFile struct {
	FileID      string `json:"file_id"`
	Checksum    string `json:"checksum"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// BuildSnapshot pins one immutable render input and starts the registered
// composition-owned workflow. All durable writes use Core's scoped engine-data
// batch with CAS create/update; no HTTP request can select an operation, owner,
// route, branch, record value or storage path.
func (s *Service) BuildSnapshot(ctx context.Context, request websiteapi.BuildSnapshotRequest) (websiteapi.BuildJob, error) {
	if err := s.requireLevel(ctx, platform.AccessLevelWrite); err != nil {
		return websiteapi.BuildJob{}, err
	}
	snapshot, err := s.GetSnapshot(ctx, request.SnapshotID)
	if err != nil {
		return websiteapi.BuildJob{}, err
	}
	if snapshot.State != "ready" {
		return websiteapi.BuildJob{}, websiteapi.ErrConflict
	}
	if err := s.requireCoordinatorWrite(ctx); err != nil {
		return websiteapi.BuildJob{}, err
	}
	if !validBuildLocale(request.Locale) || !snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		return websiteapi.BuildJob{}, websiteapi.ErrInvalid
	}
	if !validInterface(s.config.BuildWorkflow) || !validInterface(s.config.LocaleCatalog) ||
		!validInterface(s.config.BuildInputResolver) || !validInterface(s.config.BuildFiles) ||
		!identifierPattern.MatchString(s.config.BuildOperationID) || s.config.BuildContractVersion < 1 {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}

	catalogRevision, err := s.config.LocaleCatalog.LocaleCatalogRevision(ctx, request.Locale)
	if err != nil || catalogRevision < 0 {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	var trustedInput prerendercontract.TrustedInput
	if request.ExpectedContentRevision > 0 {
		pinned, ok := s.config.BuildInputResolver.(PinnedBuildInputResolver)
		if !ok {
			return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
		}
		trustedInput, err = pinned.ResolveBuildInputPinned(ctx, snapshot, request.Locale, catalogRevision, request.ExpectedContentRevision)
	} else {
		trustedInput, err = s.config.BuildInputResolver.ResolveBuildInput(ctx, snapshot, request.Locale, catalogRevision)
	}
	if err != nil {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	inputDigest, err := prerendercontract.InputDigest(trustedInput)
	if err != nil {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	normalizedKey := strings.Join([]string{snapshot.ID, request.Locale, inputDigest, request.IdempotencyKey}, "\x00")
	combinedKey := websiterepo.BuildIdempotencyKey(normalizedKey)
	callerKey := websiterepo.BuildIdempotencyKey("caller/" + request.IdempotencyKey)
	if existingBuildID, found, err := s.readBuildReceipt(ctx, combinedKey, inputDigest); err != nil {
		return websiteapi.BuildJob{}, err
	} else if found {
		return s.loadBuildJob(ctx, existingBuildID)
	}
	if _, found, err := s.readBuildReceipt(ctx, callerKey, inputDigest); err != nil {
		return websiteapi.BuildJob{}, err
	} else if found {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	if s.store.MaxBatchSize() < 3 {
		return websiteapi.BuildJob{}, websiteapi.ErrTooLarge
	}

	buildID, err := randomUUID()
	if err != nil {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	now := time.Now().UTC()
	record := buildRecord{
		ID: buildID, SnapshotID: snapshot.ID, Locale: request.Locale,
		LocaleCatalogRevision: catalogRevision, InputDigest: inputDigest, TrustedInput: trustedInput,
		State: buildStateQueued, CreatedAt: now, UpdatedAt: now,
	}
	buildKey := websiterepo.BuildKey(buildID)
	payload, index, err := buildModuleDataValue(record, 1)
	if err != nil {
		return websiteapi.BuildJob{}, err
	}
	combinedReceipt, err := buildReceiptValue(inputDigest, buildID, snapshot.ID)
	if err != nil {
		return websiteapi.BuildJob{}, err
	}
	callerReceipt, err := buildReceiptValue(inputDigest, buildID, snapshot.ID)
	if err != nil {
		return websiteapi.BuildJob{}, err
	}
	mutations := []recorddata.RecordMutation{
		{Key: buildKey, ExpectedRevision: 0, Value: &recorddata.RecordValue{Payload: payload, PayloadVersion: buildPayloadVersion, IndexData: index}},
		{Key: combinedKey, ExpectedRevision: 0, Value: combinedReceipt},
		{Key: callerKey, ExpectedRevision: 0, Value: callerReceipt},
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		mapped := mapCoreError(err)
		if errors.Is(mapped, websiteapi.ErrConflict) {
			if existingBuildID, found, readErr := s.readBuildReceipt(ctx, combinedKey, inputDigest); readErr == nil && found {
				return s.loadBuildJob(ctx, existingBuildID)
			}
		}
		return websiteapi.BuildJob{}, mapped
	}
	// Core revisions come from a database-wide sequence, not a per-record
	// counter. Read the authoritative revision before any CAS update.
	current, err := s.loadBuildState(ctx, buildID)
	if err != nil {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}

	page, err := RenderPagePlan(trustedInput, snapshot.Payload.Config)
	if err != nil {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	startPayload, err := json.Marshal(buildWorkflowPayload{
		Version: workflowPayloadVersion, BuildID: buildID,
		Input: trustedInput, Page: page, InputDigest: inputDigest,
	})
	if err != nil || len(startPayload) > s.store.MaxPayloadBytes() {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	receipt, err := s.config.BuildWorkflow.Start(ctx, platform.WorkflowStartRequest{
		OperationID: s.config.BuildOperationID, ContractVersion: s.config.BuildContractVersion,
		IdempotencyKey: "build/" + buildID, Payload: startPayload,
	})
	if err != nil {
		job, _ := s.failBuild(ctx, current, "build_workflow_start_failed")
		return job, websiteapi.ErrUnavailable
	}
	if receipt.WorkflowID == uuid.Nil || receipt.RootTaskID == uuid.Nil {
		job, _ := s.failBuild(ctx, current, "invalid_workflow_receipt")
		return job, websiteapi.ErrUnavailable
	}

	record.State = buildStateResolving
	record.WorkflowID = receipt.WorkflowID.String()
	record.RootTaskID = receipt.RootTaskID.String()
	record.UpdatedAt = time.Now().UTC()
	if err := s.putBuildRecord(ctx, current.key, current.revision, record); err != nil {
		return buildJobFromRecord(current.record), nil
	}
	return buildJobFromRecord(record), nil
}

// GetBuild reconciles one durable build. Terminal jobs are returned as stored;
// active jobs advance one recorded transition at a time. A child task result is
// activated only after its neutral envelope and pinned input are verified.
func (s *Service) GetBuild(ctx context.Context, buildID string) (websiteapi.BuildJob, error) {
	if err := s.requireLevel(ctx, platform.AccessLevelRead); err != nil {
		return websiteapi.BuildJob{}, err
	}
	if !validInterface(s.config.BuildWorkflow) {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}
	if !snapshotIDPattern.MatchString(buildID) {
		return websiteapi.BuildJob{}, websiteapi.ErrInvalid
	}
	current, err := s.loadBuildState(ctx, buildID)
	if err != nil {
		return websiteapi.BuildJob{}, err
	}
	if current.record.State == buildStateReady || current.record.State == buildStateFailed ||
		current.record.WorkflowID == "" || current.record.RootTaskID == "" {
		return buildJobFromRecord(current.record), nil
	}

	workflowID, err := uuid.Parse(current.record.WorkflowID)
	if err != nil || workflowID == uuid.Nil {
		return s.failBuild(ctx, current, "invalid_workflow_identity")
	}
	rootID, err := uuid.Parse(current.record.RootTaskID)
	if err != nil || rootID == uuid.Nil {
		return s.failBuild(ctx, current, "invalid_workflow_identity")
	}
	workflow, err := s.config.BuildWorkflow.Get(ctx, workflowID)
	if err != nil {
		return s.failBuild(ctx, current, buildWorkflowErrorCode(err))
	}
	if workflow.ID != uuid.Nil && workflow.ID != workflowID ||
		workflow.RootTaskID != uuid.Nil && workflow.RootTaskID != rootID {
		return s.failBuild(ctx, current, "workflow_identity_mismatch")
	}
	switch workflow.State {
	case "queued", "running", "succeeded":
	case "failed":
		return s.failBuild(ctx, current, "workflow_failed")
	case "cancelled":
		return s.failBuild(ctx, current, "workflow_cancelled")
	default:
		return s.failBuild(ctx, current, "invalid_workflow_state")
	}

	if current.record.PrerenderTaskID == "" {
		selector, err := parseBuildRootResult(workflow.Result, current.record.ID, current.record.InputDigest, s.store.MaxPayloadBytes())
		if err != nil {
			if errors.Is(err, errBuildStaleInput) || workflow.State == "succeeded" {
				code := "invalid_root_result"
				if errors.Is(err, errBuildStaleInput) {
					code = "stale_input_digest"
				}
				return s.failBuild(ctx, current, code)
			}
			return buildJobFromRecord(current.record), nil
		}
		prerenderTaskID, err := s.resolvePrerenderTask(ctx, workflowID, selector)
		if err != nil {
			if errors.Is(err, platform.ErrTaskMissing) && workflow.State != "succeeded" {
				return buildJobFromRecord(current.record), nil
			}
			return s.failBuild(ctx, current, buildTaskErrorCode(err))
		}
		if current.record.State == buildStateQueued || current.record.State == "" {
			current, err = s.advanceBuild(ctx, current, buildStateResolving, nil)
			if err != nil {
				return buildJobFromRecord(current.record), err
			}
		}
		current, err = s.advanceBuild(ctx, current, buildStatePrerender, func(record *buildRecord) {
			record.PrerenderTaskID = prerenderTaskID.String()
		})
		if err != nil {
			return buildJobFromRecord(current.record), err
		}
		return buildJobFromRecord(current.record), nil
	}

	prerenderTaskID, err := uuid.Parse(current.record.PrerenderTaskID)
	if err != nil || prerenderTaskID == uuid.Nil {
		return s.failBuild(ctx, current, "invalid_prerender_task_identity")
	}
	taskResult, err := s.config.BuildWorkflow.GetTaskResult(ctx, workflowID, prerenderTaskID)
	if err != nil {
		if errors.Is(err, platform.ErrTaskMissing) && workflow.State != "succeeded" {
			return buildJobFromRecord(current.record), nil
		}
		return s.failBuild(ctx, current, buildTaskErrorCode(err))
	}
	if taskResult.WorkflowID != uuid.Nil && taskResult.WorkflowID != workflowID ||
		taskResult.TaskID != uuid.Nil && taskResult.TaskID != prerenderTaskID ||
		taskResult.State != "" && taskResult.State != "succeeded" ||
		taskResult.Operation != nil && (taskResult.Operation.ClassID != buildPrerenderWorker ||
			taskResult.Operation.ID != buildPrerenderOp || taskResult.Operation.Version != 1) {
		return s.failBuild(ctx, current, "invalid_task_result")
	}
	manifestFileID, err := s.parseBuildTaskResult(ctx, taskResult.Result, current.record)
	if err != nil {
		code := "invalid_task_result"
		if errors.Is(err, errBuildStaleInput) {
			code = "stale_input_digest"
		}
		return s.failBuild(ctx, current, code)
	}

	if current.record.State == buildStateResolving {
		current, err = s.advanceBuild(ctx, current, buildStatePrerender, nil)
		if err != nil {
			return buildJobFromRecord(current.record), err
		}
	}
	for _, next := range []string{buildStatePackaging, buildStateVerifying} {
		current, err = s.advanceBuild(ctx, current, next, nil)
		if err != nil {
			return buildJobFromRecord(current.record), err
		}
	}
	current, err = s.advanceBuild(ctx, current, buildStateReady, func(record *buildRecord) {
		record.ArtifactManifestFileID = manifestFileID
		record.ErrorCode = ""
	})
	if err != nil {
		return buildJobFromRecord(current.record), err
	}
	return buildJobFromRecord(current.record), nil
}

// resolvePrerenderTask accepts either a pinned task ID from an older root
// envelope or the stable emission key emitted by the composition-owned root
// handler. Core creates child task IDs only when the root result commits, so
// the emission key is the authoritative selector for new workflows.
func (s *Service) resolvePrerenderTask(ctx context.Context, workflowID uuid.UUID, selector string) (uuid.UUID, error) {
	if taskID, err := uuid.Parse(selector); err == nil && taskID != uuid.Nil {
		return taskID, nil
	}
	if s == nil || !validInterface(s.config.BuildWorkflow) {
		return uuid.Nil, platform.ErrUnavailable
	}
	taskID, err := s.config.BuildWorkflow.ResolveTaskID(ctx, workflowID, selector)
	if err != nil || taskID == uuid.Nil {
		if err == nil {
			err = platform.ErrTaskMissing
		}
		return uuid.Nil, err
	}
	return taskID, nil
}

func buildModuleDataValue(record buildRecord, revision int64) ([]byte, []byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, nil, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(buildIndex{RecordType: "build", State: record.State, SnapshotID: record.SnapshotID, Revision: revision})
	if err != nil {
		return nil, nil, websiteapi.ErrUnavailable
	}
	return payload, index, nil
}

func buildReceiptValue(inputDigest, buildID, snapshotID string) (*recorddata.RecordValue, error) {
	receipt := buildReceipt{RecordType: "build_idempotency", BuildID: buildID, InputDigest: inputDigest, State: "created"}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(buildIndex{RecordType: "build_idempotency", State: "created", SnapshotID: snapshotID, Revision: 1})
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	return &recorddata.RecordValue{Payload: payload, PayloadVersion: buildPayloadVersion, IndexData: index}, nil
}

func (s *Service) readBuildReceipt(ctx context.Context, key recorddata.RecordKey, inputDigest string) (string, bool, error) {
	record, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, recorddata.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, mapCoreError(err)
	}
	if record.PayloadVersion != buildPayloadVersion || record.Revision < 1 {
		return "", false, websiteapi.ErrUnavailable
	}
	var receipt buildReceipt
	var index buildIndex
	if json.Unmarshal(record.Payload, &receipt) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		receipt.RecordType != "build_idempotency" || index.RecordType != "build_idempotency" ||
		index.State != "created" || index.Revision < 1 ||
		!snapshotIDPattern.MatchString(receipt.BuildID) || len(receipt.InputDigest) != sha256.Size*2 {
		return "", false, websiteapi.ErrUnavailable
	}
	if receipt.InputDigest != inputDigest {
		return "", false, websiteapi.ErrConflict
	}
	return receipt.BuildID, true, nil
}

func (s *Service) putBuildRecord(ctx context.Context, key recorddata.RecordKey, expectedRevision int64, record buildRecord) error {
	payload, index, err := buildModuleDataValue(record, expectedRevision+1)
	if err != nil {
		return err
	}
	if len(payload) > s.store.MaxPayloadBytes() {
		return websiteapi.ErrTooLarge
	}
	return mapCoreError(s.store.Batch(ctx, []recorddata.RecordMutation{{
		Key: key, ExpectedRevision: expectedRevision,
		Value: &recorddata.RecordValue{Payload: payload, PayloadVersion: buildPayloadVersion, IndexData: index},
	}}))
}

func (s *Service) loadBuildState(ctx context.Context, buildID string) (buildState, error) {
	key := websiterepo.BuildKey(buildID)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, recorddata.ErrRecordNotFound) {
			return buildState{}, websiteapi.ErrNotFound
		}
		return buildState{}, mapCoreError(err)
	}
	if record.PayloadVersion != buildPayloadVersion || record.Revision < 1 {
		return buildState{}, websiteapi.ErrUnavailable
	}
	var stored buildRecord
	var index buildIndex
	if json.Unmarshal(record.Payload, &stored) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "build" || index.Revision < 1 || index.State != stored.State ||
		index.SnapshotID != stored.SnapshotID || stored.ID != strings.ToLower(buildID) ||
		!snapshotIDPattern.MatchString(stored.SnapshotID) || !validBuildLocale(stored.Locale) ||
		len(stored.InputDigest) != sha256.Size*2 || !validBuildState(stored.State) {
		return buildState{}, websiteapi.ErrUnavailable
	}
	return buildState{record: stored, key: key, revision: record.Revision}, nil
}

func (s *Service) loadBuildJob(ctx context.Context, buildID string) (websiteapi.BuildJob, error) {
	state, err := s.loadBuildState(ctx, buildID)
	if err != nil {
		return websiteapi.BuildJob{}, err
	}
	return buildJobFromRecord(state.record), nil
}

func buildJobFromRecord(record buildRecord) websiteapi.BuildJob {
	return websiteapi.BuildJob{
		ID: record.ID, SnapshotID: record.SnapshotID, Locale: record.Locale,
		LocaleCatalogRevision: record.LocaleCatalogRevision, InputDigest: record.InputDigest,
		State: record.State, ArtifactManifestFileID: record.ArtifactManifestFileID,
		ErrorCode: record.ErrorCode, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func (s *Service) advanceBuild(ctx context.Context, current buildState, next string, mutate func(*buildRecord)) (buildState, error) {
	if current.record.State == next {
		return current, nil
	}
	if !validBuildTransition(current.record.State, next) {
		return current, websiteapi.ErrConflict
	}
	updated := current.record
	if mutate != nil {
		mutate(&updated)
	}
	updated.State = next
	updated.UpdatedAt = time.Now().UTC()
	if err := s.putBuildRecord(ctx, current.key, current.revision, updated); err != nil {
		return current, err
	}
	// Core assigns revisions from a database-wide sequence. Re-read the
	// authoritative revision after the CAS instead of assuming the record's
	// next revision equals its previous revision plus one.
	refreshed, err := s.loadBuildState(ctx, current.record.ID)
	if err != nil {
		return current, err
	}
	return refreshed, nil
}

func (s *Service) failBuild(ctx context.Context, current buildState, code string) (websiteapi.BuildJob, error) {
	if current.record.State == buildStateFailed {
		return buildJobFromRecord(current.record), nil
	}
	updated := current.record
	updated.State = buildStateFailed
	updated.ErrorCode = code
	updated.ArtifactManifestFileID = ""
	updated.UpdatedAt = time.Now().UTC()
	if err := s.putBuildRecord(ctx, current.key, current.revision, updated); err != nil {
		return buildJobFromRecord(current.record), err
	}
	return buildJobFromRecord(updated), nil
}

func validBuildState(state string) bool {
	switch state {
	case buildStateQueued, buildStateResolving, buildStatePrerender, buildStatePackaging,
		buildStateVerifying, buildStateReady, buildStateFailed:
		return true
	default:
		return false
	}
}

func validBuildTransition(from, to string) bool {
	if to == buildStateFailed {
		return from != buildStateFailed
	}
	switch from {
	case buildStateQueued:
		return to == buildStateResolving
	case buildStateResolving:
		return to == buildStatePrerender
	case buildStatePrerender:
		return to == buildStatePackaging
	case buildStatePackaging:
		return to == buildStateVerifying
	case buildStateVerifying:
		return to == buildStateReady
	default:
		return false
	}
}

func parseBuildRootResult(raw json.RawMessage, expectedBuildID, expectedDigest string, maxBytes int) (string, error) {
	if len(raw) == 0 || len(raw) > maxBytes {
		return "", errBuildInvalidResult
	}
	var result buildRootResult
	if err := decodeStrictBuildJSON(raw, &result); err != nil {
		return "", errBuildInvalidResult
	}
	if result.Format != buildRootFormat || result.BuildID != strings.ToLower(expectedBuildID) {
		return "", errBuildInvalidResult
	}
	if result.InputDigest != expectedDigest {
		return "", errBuildStaleInput
	}
	if result.PrerenderTaskID != "" && result.PrerenderEmissionKey != "" {
		return "", errBuildInvalidResult
	}
	if result.PrerenderTaskID != "" {
		taskID, err := uuid.Parse(result.PrerenderTaskID)
		if err != nil || taskID == uuid.Nil {
			return "", errBuildInvalidResult
		}
		return taskID.String(), nil
	}
	if result.PrerenderEmissionKey == "" || len(result.PrerenderEmissionKey) > 256 ||
		strings.TrimSpace(result.PrerenderEmissionKey) != result.PrerenderEmissionKey ||
		strings.ContainsAny(result.PrerenderEmissionKey, "\x00\r\n") {
		return "", errBuildInvalidResult
	}
	return result.PrerenderEmissionKey, nil
}

func (s *Service) parseBuildTaskResult(ctx context.Context, raw json.RawMessage, record buildRecord) (string, error) {
	maxBytes := s.store.MaxPayloadBytes()
	if len(raw) == 0 || len(raw) > maxBytes {
		return "", errBuildInvalidResult
	}
	result := struct {
		Format      string              `json:"format"`
		InputDigest string              `json:"input_digest"`
		Manifest    buildManifestFile   `json:"manifest"`
		Artifacts   []buildManifestFile `json:"artifacts"`
	}{}
	if err := decodeStrictBuildJSON(raw, &result); err != nil {
		return "", errBuildInvalidResult
	}
	if result.Format != prerendercontract.BuildResultFormat {
		return "", errBuildInvalidResult
	}
	if result.InputDigest != record.InputDigest || prerendercontract.ValidateTrustedInput(record.TrustedInput) != nil {
		return "", errBuildStaleInput
	}
	expected, err := prerendercontract.InputDigest(record.TrustedInput)
	if err != nil || expected != record.InputDigest {
		return "", errBuildStaleInput
	}
	if !snapshotIDPattern.MatchString(result.Manifest.FileID) || !sha256HexPattern.MatchString(result.Manifest.Checksum) ||
		result.Manifest.ContentType != buildManifestType || result.Manifest.Size < 1 || result.Manifest.Size > int64(maxBytes) {
		return "", errBuildInvalidResult
	}
	session, ok := buildActorSession(ctx)
	if !ok || !validInterface(s.config.BuildFiles) {
		return "", errBuildInvalidResult
	}
	reader, _, err := s.config.BuildFiles.ReadFile(ctx, session, result.Manifest.FileID)
	if err != nil {
		return "", errBuildInvalidResult
	}
	defer reader.Close()
	manifestBytes, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil || int64(len(manifestBytes)) > int64(maxBytes) || len(manifestBytes) != int(result.Manifest.Size) ||
		prerendercontract.HexDigest(manifestBytes) != result.Manifest.Checksum {
		return "", errBuildInvalidResult
	}
	manifest, err := prerendercontract.VerifyManifest(manifestBytes, record.InputDigest)
	if err != nil || manifest.Locale != record.Locale || manifest.Render.Locale != record.Locale {
		return "", errBuildInvalidResult
	}
	return strings.ToLower(result.Manifest.FileID), nil
}

func buildActorSession(ctx context.Context) (platform.Session, bool) {
	if ctx == nil {
		return platform.Session{}, false
	}
	actor, ok := ctx.Value(actorContextKey{}).(actor)
	return actor.session, ok
}

func decodeStrictBuildJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errBuildInvalidResult
	}
	return nil
}

func buildWorkflowErrorCode(err error) string {
	switch {
	case errors.Is(err, platform.ErrTaskMissing), errors.Is(err, recorddata.ErrRecordNotFound):
		return "workflow_not_found"
	case errors.Is(err, platform.ErrUnauthorized), errors.Is(err, platform.ErrForbidden), errors.Is(err, platform.ErrTaskNotOwned):
		return "workflow_access_denied"
	default:
		return "workflow_unavailable"
	}
}

func buildTaskErrorCode(err error) string {
	switch {
	case errors.Is(err, platform.ErrTaskMissing), errors.Is(err, recorddata.ErrRecordNotFound):
		return "task_result_missing"
	case errors.Is(err, platform.ErrUnauthorized), errors.Is(err, platform.ErrForbidden), errors.Is(err, platform.ErrTaskNotOwned):
		return "task_result_access_denied"
	default:
		return "task_result_unavailable"
	}
}
