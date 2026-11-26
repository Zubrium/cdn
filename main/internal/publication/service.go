package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	workflow "github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

// Publish creates a durable run from the trusted registry and starts the
// class-scoped workflow. Successful targets stay activated when another target
// fails; no engine is called by this package.
func (s *Service) Publish(ctx context.Context, trigger Trigger) (Result, error) {
	if s == nil || ctx == nil {
		return Result{}, ErrUnavailable
	}
	locale, err := validateTrigger(trigger)
	if err != nil {
		return Result{}, err
	}
	runID, err := deterministicRunID(trigger, locale)
	if err != nil {
		return Result{}, err
	}
	if _, _, existing, err := s.loadRun(ctx, runID); err == nil {
		return s.resumePublishedRun(ctx, existing, trigger, locale)
	} else if !errors.Is(err, workflow.ErrRecordNotFound) {
		return Result{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	run := runRecord{
		Kind: recordTypeRun, RunID: runID, BranchID: s.config.BranchID, Locale: locale,
		ContentPublicationRevision: trigger.ContentPublicationRevision, SystemModelRevision: trigger.SystemModelRevision,
		IdempotencyKey: trigger.IdempotencyKey, State: stateStarting, CreatedAt: now, UpdatedAt: now,
	}
	mutations := make([]workflow.RecordMutation, 0, maxTargets+1)
	runValue, err := encodeModuleData(run, runIndex(run))
	if err != nil {
		return Result{}, err
	}
	mutations = append(mutations, workflow.RecordMutation{
		Key: runDataKey(s.config.BranchID, runID), ExpectedRevision: 0, Value: runValue,
	})
	for _, target := range s.target {
		if target.Locale != locale {
			continue
		}
		record := targetRecord{
			Kind: recordTypeTarget, RunID: runID, RouteID: target.RouteID,
			PageSnapshotID: target.PageSnapshotID, Locale: locale,
			ContentPublicationRevision: trigger.ContentPublicationRevision, SystemModelRevision: trigger.SystemModelRevision,
			State: statePending, Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
		value, err := encodeModuleData(record, targetIndex(record))
		if err != nil {
			return Result{}, err
		}
		key := targetDataKey(s.config.BranchID, runID, target.RouteID, locale)
		run.TargetKeys = append(run.TargetKeys, key.RecordKey)
		mutations = append(mutations, workflow.RecordMutation{Key: key, ExpectedRevision: 0, Value: value})
	}
	if len(run.TargetKeys) == 0 {
		return Result{}, fmt.Errorf("%w: no trusted target for locale %s", ErrInvalid, locale)
	}
	runValue, err = encodeModuleData(run, runIndex(run))
	if err != nil {
		return Result{}, err
	}
	mutations[0].Value = runValue
	if err := s.config.Store.Batch(ctx, mutations); err != nil {
		if errors.Is(err, workflow.ErrRecordConflict) {
			_, _, existing, readErr := s.loadRun(ctx, runID)
			if readErr == nil {
				return s.resumePublishedRun(ctx, existing, trigger, locale)
			}
			if !errors.Is(readErr, workflow.ErrRecordNotFound) {
				return Result{}, readErr
			}
		}
		return Result{}, mapStoreError(err)
	}
	if err := s.startWorkflow(ctx, run); err != nil {
		return Result{}, err
	}
	_, _, current, err := s.loadRun(ctx, run.RunID)
	if err != nil {
		return Result{}, err
	}
	return s.result(ctx, current)
}

func (s *Service) resumePublishedRun(ctx context.Context, run runRecord, trigger Trigger, locale string) (Result, error) {
	if run.Locale != locale || run.ContentPublicationRevision != trigger.ContentPublicationRevision ||
		run.SystemModelRevision != trigger.SystemModelRevision || run.IdempotencyKey != trigger.IdempotencyKey {
		return Result{}, ErrConflict
	}
	if run.State == stateStarting || run.State == stateFailed {
		return s.restart(ctx, run)
	}
	return s.result(ctx, run)
}

// Retry resumes an existing durable run with the identical workflow start
// input. Core deduplicates the idempotency key, so a retry cannot start a
// second workflow for the same run.
func (s *Service) Retry(ctx context.Context, runID string) (Result, error) {
	if s == nil || ctx == nil || !runKeyPattern.MatchString(runID) {
		return Result{}, ErrInvalid
	}
	_, _, run, err := s.loadRun(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	if run.State == stateSuccess {
		return s.result(ctx, run)
	}
	return s.restart(ctx, run)
}

func (s *Service) restart(ctx context.Context, run runRecord) (Result, error) {
	if err := s.startWorkflow(ctx, run); err != nil {
		return Result{}, err
	}
	_, _, current, err := s.loadRun(ctx, run.RunID)
	if err != nil {
		return Result{}, err
	}
	return s.result(ctx, current)
}

func (s *Service) startWorkflow(ctx context.Context, run runRecord) error {
	if s.config.Starter == nil {
		return ErrUnavailable
	}
	trigger := Trigger{Locale: run.Locale, ContentPublicationRevision: run.ContentPublicationRevision,
		SystemModelRevision: run.SystemModelRevision, IdempotencyKey: run.IdempotencyKey}
	targets := make([]workflow.PublicationWorkflowTarget, 0, len(run.TargetKeys))
	for _, recordKey := range run.TargetKeys {
		key := workflow.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: s.config.BranchID, RecordKey: recordKey}
		_, target, err := s.loadTarget(ctx, key, run.RunID)
		if err != nil {
			return err
		}
		targets = append(targets, workflow.PublicationWorkflowTarget{
			RouteID: target.RouteID, PageSnapshotID: target.PageSnapshotID, Locale: target.Locale,
		})
	}
	input := workflowStartInput(s.config.BranchID, trigger, run.Locale, run.RunID, targets)
	key, record, current, err := s.loadRun(ctx, run.RunID)
	if err != nil {
		return err
	}
	if current.State == stateSuccess || current.State != run.State {
		return nil
	}
	if _, err := s.config.Starter.Start(ctx, input); err != nil {
		_ = s.markStartOutcome(ctx, key, record.Revision, current, stateFailed, "workflow_start_failed")
		return err
	}
	return s.markStartOutcome(ctx, key, record.Revision, current, stateRunning, "")
}

// A worker may finish before Start returns. The start response must not
// overwrite a state written after the pre-start revision was read.
func (s *Service) markStartOutcome(ctx context.Context, key workflow.RecordKey, revision int64, run runRecord, state, code string) error {
	run.State, run.ErrorCode = state, code
	run.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.putRun(ctx, key, revision, run); err != nil {
		if errors.Is(err, ErrConflict) {
			_, _, _, readErr := s.loadRun(ctx, run.RunID)
			return readErr
		}
		return err
	}
	return nil
}

func workflowStartInput(branchID string, trigger Trigger, locale, runID string, targets []workflow.PublicationWorkflowTarget) workflow.WorkflowStartInput {
	payload, _ := json.Marshal(workflow.PublicationWorkflowPayload{
		RunID: runID, BranchID: branchID, Locale: locale,
		ContentPublicationRevision: trigger.ContentPublicationRevision, SystemModelRevision: trigger.SystemModelRevision,
		IdempotencyKey: trigger.IdempotencyKey, Targets: targets,
	})
	return workflow.WorkflowStartInput{
		OperationID:     workflow.PublicationStartOperationID,
		ContractVersion: workflow.PublicationStartContractVersion,
		IdempotencyKey:  "publication:" + runID + ":" + trigger.IdempotencyKey,
		Payload:         payload,
	}
}

func (s *Service) markRunState(ctx context.Context, runID, state, errorCode string) error {
	return s.updateRun(ctx, runID, func(run *runRecord) { run.State, run.ErrorCode = state, errorCode })
}

func (s *Service) updateRun(ctx context.Context, runID string, mutate func(*runRecord)) error {
	key, record, run, err := s.loadRun(ctx, runID)
	if err != nil {
		return err
	}
	mutate(&run)
	run.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.putRun(ctx, key, record.Revision, run)
}

// BeginBuild reserves one target for an isolated Website build. The child
// payload is checked against the durable target record before any engine work.
func (s *Service) BeginBuild(ctx context.Context, payload workflow.PublicationBuildPayload) error {
	if err := workflow.ValidatePublicationBuildPayload(payload); err != nil {
		return err
	}
	key := targetDataKey(s.config.BranchID, payload.RunID, payload.RouteID, payload.Locale)
	current, target, err := s.loadTarget(ctx, key, payload.RunID)
	if err != nil {
		return err
	}
	if target.PageSnapshotID != payload.PageSnapshotID || target.Locale != payload.Locale ||
		target.ContentPublicationRevision != payload.ContentPublicationRevision ||
		target.SystemModelRevision != payload.SystemModelRevision {
		return fmt.Errorf("%w: build payload does not match durable target", ErrInvalid)
	}
	target.State = stateRunning
	target.Revision++
	target.ErrorCode = ""
	target.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.putTarget(ctx, key, current.Revision, target)
}

// FinishBuild stores the verified artifact identity and the digest it was
// built from. The artifact record is the only channel activation may read.
func (s *Service) FinishBuild(ctx context.Context, payload workflow.PublicationBuildPayload, result workflow.PublicationBuildResult) error {
	if err := workflow.ValidatePublicationBuildPayload(payload); err != nil {
		return err
	}
	if result.Format != workflow.PublicationBuildResultFormat || result.RunID != payload.RunID ||
		result.RouteID != payload.RouteID || result.Locale != payload.Locale ||
		result.SnapshotID != payload.PageSnapshotID || result.InputDigest == "" ||
		result.ArtifactManifestFileID == "" || !runKeyPattern.MatchString(result.ArtifactManifestFileID) {
		return fmt.Errorf("%w: invalid build result", ErrInvalid)
	}
	key := targetDataKey(s.config.BranchID, payload.RunID, payload.RouteID, payload.Locale)
	current, target, err := s.loadTarget(ctx, key, payload.RunID)
	if err != nil {
		return err
	}
	if target.PageSnapshotID != payload.PageSnapshotID || target.Locale != payload.Locale ||
		target.ContentPublicationRevision != payload.ContentPublicationRevision || target.SystemModelRevision != payload.SystemModelRevision {
		return fmt.Errorf("%w: build payload does not match durable target", ErrInvalid)
	}
	if target.InputDigest != "" && target.InputDigest != result.InputDigest {
		return ErrStale
	}
	artifact := artifactRecord{
		Kind: recordTypeArtifact, RunID: payload.RunID, RouteID: payload.RouteID, Locale: payload.Locale,
		SnapshotID: payload.PageSnapshotID, InputDigest: result.InputDigest, ArtifactManifestFileID: result.ArtifactManifestFileID,
	}
	existing, err := s.loadArtifact(ctx, payload.RunID, payload.RouteID, payload.Locale)
	if err == nil && existing != artifact {
		return ErrStale
	}
	if err != nil && !errors.Is(err, workflow.ErrRecordNotFound) {
		return err
	}
	artifactExists := err == nil
	target.State = stateBuilt
	target.Revision++
	target.InputDigest = result.InputDigest
	target.BuildID = workflow.PublicationBuildIdempotencyKey(payload)
	target.ArtifactManifestFileID = result.ArtifactManifestFileID
	target.ErrorCode = ""
	target.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if artifactExists {
		return s.putTarget(ctx, key, current.Revision, target)
	}
	targetValue, err := encodeModuleData(target, targetIndex(target))
	if err != nil {
		return err
	}
	artifactValue, err := encodeModuleData(artifact, artifactIndex(artifact))
	if err != nil {
		return err
	}
	return mapStoreError(s.config.Store.Batch(ctx, []workflow.RecordMutation{
		{Key: key, ExpectedRevision: current.Revision, Value: targetValue},
		{Key: artifactDataKey(s.config.BranchID, artifact.RunID, artifact.RouteID, artifact.Locale), ExpectedRevision: 0, Value: artifactValue},
	}))
}

// TargetSnapshot returns the server-owned snapshot bound to one run slot. The
// Router activation adapter uses it only to recompute the trusted input digest.
func (s *Service) TargetSnapshot(ctx context.Context, runID, routeID, locale string) (string, error) {
	if !runKeyPattern.MatchString(runID) || !runKeyPattern.MatchString(routeID) || workflow.ValidateWorkflowLocale(locale) != nil {
		return "", ErrInvalid
	}
	key := targetDataKey(s.config.BranchID, runID, routeID, locale)
	_, target, err := s.loadTarget(ctx, key, runID)
	if err != nil {
		return "", err
	}
	return target.PageSnapshotID, nil
}

// ArtifactDigest returns the input digest pinned when the verified build was
// stored. The Router activation adapter recomputes the current trusted digest
// and rejects the activation if it no longer matches.
func (s *Service) ArtifactDigest(ctx context.Context, runID, routeID, locale string) (string, error) {
	if !runKeyPattern.MatchString(runID) || !runKeyPattern.MatchString(routeID) || workflow.ValidateWorkflowLocale(locale) != nil {
		return "", ErrInvalid
	}
	artifact, err := s.loadArtifact(ctx, runID, routeID, locale)
	if err != nil {
		return "", err
	}
	return artifact.InputDigest, nil
}

// BeginActivation checks that the artifact belongs to the exact server-owned
// slot before Router is allowed to switch its locale target.
func (s *Service) BeginActivation(ctx context.Context, payload workflow.PublicationActivatePayload) error {
	if err := workflow.ValidatePublicationActivatePayload(payload); err != nil {
		return err
	}
	artifact, err := s.loadArtifact(ctx, payload.RunID, payload.RouteID, payload.Locale)
	if err != nil {
		return err
	}
	if artifact.ArtifactManifestFileID != payload.ArtifactManifestFileID {
		return fmt.Errorf("%w: activation artifact substitution", ErrInvalid)
	}
	key := targetDataKey(s.config.BranchID, payload.RunID, payload.RouteID, payload.Locale)
	current, target, err := s.loadTarget(ctx, key, payload.RunID)
	if err != nil {
		return err
	}
	if (target.State != stateBuilt && target.State != stateRunning && target.State != stateSuccess && target.State != stateFailed) ||
		target.ArtifactManifestFileID != artifact.ArtifactManifestFileID {
		return fmt.Errorf("%w: target is not built for this artifact", ErrStale)
	}
	target.State = stateRunning
	target.Revision++
	target.ErrorCode = ""
	target.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.putTarget(ctx, key, current.Revision, target)
}

// FinishActivation records a successful Router CAS. The run state is computed
// only by FinalizeRun after every activation emission has settled.
func (s *Service) FinishActivation(ctx context.Context, payload workflow.PublicationActivatePayload, result workflow.PublicationActivateResult) error {
	if err := workflow.ValidatePublicationActivatePayload(payload); err != nil {
		return err
	}
	if result.Format != workflow.PublicationActivateResultFormat || result.RunID != payload.RunID ||
		result.RouteID != payload.RouteID || result.Locale != payload.Locale ||
		result.ArtifactManifestFileID != payload.ArtifactManifestFileID || result.Revision < 1 {
		return fmt.Errorf("%w: invalid activation result", ErrInvalid)
	}
	return s.updateTarget(ctx, payload.RunID, payload.RouteID, payload.Locale, func(target *targetRecord) {
		target.State = stateSuccess
		target.Revision++
		target.ErrorCode = ""
	})
}

// FailTarget records a bounded failure code without discarding a ready build.
func (s *Service) FailTarget(ctx context.Context, runID, routeID, locale string, err error) error {
	if !runKeyPattern.MatchString(runID) || !runKeyPattern.MatchString(routeID) || workflow.ValidateWorkflowLocale(locale) != nil {
		return ErrInvalid
	}
	return s.updateTarget(ctx, runID, routeID, locale, func(target *targetRecord) {
		target.State = stateFailed
		target.Revision++
		target.ErrorCode = errorCode(err)
	})
}

func (s *Service) updateTarget(ctx context.Context, runID, routeID, locale string, mutate func(*targetRecord)) error {
	key := targetDataKey(s.config.BranchID, runID, routeID, locale)
	current, target, err := s.loadTarget(ctx, key, runID)
	if err != nil {
		return err
	}
	mutate(&target)
	target.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.putTarget(ctx, key, current.Revision, target)
}

// FinalizeRun computes the durable run state from per-target outcomes. It is
// the only task allowed to close a run and never re-runs engine work.
func (s *Service) FinalizeRun(ctx context.Context, runID string) (Result, error) {
	if s == nil || ctx == nil || !runKeyPattern.MatchString(runID) {
		return Result{}, ErrInvalid
	}
	_, _, run, err := s.loadRun(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	state := stateSuccess
	for _, recordKey := range run.TargetKeys {
		key := workflow.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: s.config.BranchID, RecordKey: recordKey}
		_, target, err := s.loadTarget(ctx, key, runID)
		if err != nil {
			return Result{}, err
		}
		if target.State != stateSuccess {
			state = stateFailed
		}
	}
	if err := s.markRunState(ctx, runID, state, ""); err != nil {
		return Result{}, err
	}
	_, _, run, err = s.loadRun(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	return s.result(ctx, run)
}

func (s *Service) result(ctx context.Context, run runRecord) (Result, error) {
	result := Result{RunID: run.RunID, State: run.State, Targets: make([]TargetResult, 0, len(run.TargetKeys))}
	for _, recordKey := range run.TargetKeys {
		key := workflow.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: s.config.BranchID, RecordKey: recordKey}
		_, target, err := s.loadTarget(ctx, key, run.RunID)
		if err != nil {
			return Result{}, err
		}
		result.Targets = append(result.Targets, TargetResult{
			RouteID: target.RouteID, Locale: target.Locale, State: target.State,
			BuildID: target.BuildID, ArtifactManifestFileID: target.ArtifactManifestFileID, ErrorCode: target.ErrorCode,
		})
	}
	return result, nil
}

func deterministicRunID(trigger Trigger, locale string) (string, error) {
	encoded, err := json.Marshal(struct {
		Locale      string `json:"locale"`
		Content     int64  `json:"content_revision"`
		System      int64  `json:"system_revision"`
		Idempotency string `json:"idempotency_key"`
	}{locale, trigger.ContentPublicationRevision, trigger.SystemModelRevision, trigger.IdempotencyKey})
	if err != nil {
		return "", ErrInvalid
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, encoded).String(), nil
}

func errorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrStale):
		return "stale_build_input"
	case errors.Is(err, ErrConflict):
		return "router_cas_conflict"
	case errors.Is(err, ErrInvalid):
		return "invalid_target"
	default:
		message := err.Error()
		if index := strings.Index(message, ":"); index > 0 {
			message = strings.TrimSpace(message[:index])
		}
		return message
	}
}
