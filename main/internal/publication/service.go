package publication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
	"github.com/google/uuid"
)

// Publish creates a durable run from the trusted registry and advances every
// affected target. Successful targets stay activated when another target fails.
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
		return s.result(ctx, existing)
	} else if !errors.Is(err, core.ErrModuleDataNotFound) && !errors.Is(err, ErrInvalid) {
		return Result{}, err
	}

	websiteCtx, err := s.config.Website.Authenticate(ctx, s.config.WebsiteCredential)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	routerCtx, err := s.config.Router.Authenticate(ctx, s.config.RouterCredential)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	run := runRecord{
		Kind: recordTypeRun, RunID: runID, BranchID: s.config.BranchID, Locale: locale,
		ContentPublicationRevision: trigger.ContentPublicationRevision, SystemModelRevision: trigger.SystemModelRevision,
		State: stateRunning, CreatedAt: now, UpdatedAt: now,
	}
	mutations := make([]core.ModuleDataMutation, 0, maxTargets+1)
	runValue, err := encodeModuleData(run, runIndex(run))
	if err != nil {
		return Result{}, err
	}
	mutations = append(mutations, core.ModuleDataMutation{
		Key: runDataKey(s.config.BranchID, runID), ExpectedRevision: 0, Value: runValue,
	})
	for _, target := range s.target {
		if target.Locale != locale {
			continue
		}
		digest, err := s.resolveInputDigest(websiteCtx, target, locale, trigger)
		if err != nil {
			return Result{}, err
		}
		record := targetRecord{
			Kind: recordTypeTarget, RunID: runID, RouteID: target.RouteID,
			PageSnapshotID: target.PageSnapshotID, Locale: locale, State: statePending, Revision: 1,
			InputDigest: digest, CreatedAt: now, UpdatedAt: now,
		}
		value, err := encodeModuleData(record, targetIndex(record))
		if err != nil {
			return Result{}, err
		}
		key := targetDataKey(s.config.BranchID, runID, target.RouteID, locale)
		run.TargetKeys = append(run.TargetKeys, key.RecordKey)
		mutations = append(mutations, core.ModuleDataMutation{Key: key, ExpectedRevision: 0, Value: value})
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
		return Result{}, mapStoreError(err)
	}
	return s.advance(ctx, websiteCtx, routerCtx, runID)
}

// Retry advances only the failed targets of an existing durable run. Successes
// are never rebuilt or re-activated.
func (s *Service) Retry(ctx context.Context, runID string) (Result, error) {
	if s == nil || ctx == nil || !runKeyPattern.MatchString(runID) {
		return Result{}, ErrInvalid
	}
	if _, _, _, err := s.loadRun(ctx, runID); err != nil {
		return Result{}, err
	}
	websiteCtx, err := s.config.Website.Authenticate(ctx, s.config.WebsiteCredential)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	routerCtx, err := s.config.Router.Authenticate(ctx, s.config.RouterCredential)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	return s.advance(ctx, websiteCtx, routerCtx, runID)
}

func (s *Service) advance(ctx, websiteCtx, routerCtx context.Context, runID string) (Result, error) {
	runKey, runRecord, run, err := s.loadRun(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	for _, recordKey := range run.TargetKeys {
		key := core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: s.config.BranchID, RecordKey: recordKey}
		current, target, err := s.loadTarget(ctx, key, runID)
		if err != nil {
			return Result{}, err
		}
		if target.State == stateSuccess {
			continue
		}
		// Core's record revision is opaque and global. Publication keeps its own
		// monotonic target revision in the durable payload and uses only the
		// observed Core revision as the write CAS token.
		target.State = stateRunning
		target.Revision++
		target.ErrorCode = ""
		target.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.putTarget(ctx, key, current.Revision, target); err != nil {
			return Result{}, err
		}
		if err := s.processTarget(ctx, websiteCtx, routerCtx, &target); err != nil {
			target.State = stateFailed
			target.ErrorCode = errorCode(err)
		} else {
			target.State = stateSuccess
			target.ErrorCode = ""
		}
		runningRecord, running, err := s.loadTarget(ctx, key, runID)
		if err != nil {
			return Result{}, err
		}
		target.Revision = running.Revision + 1
		target.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.putTarget(ctx, key, runningRecord.Revision, target); err != nil {
			return Result{}, err
		}
	}
	loaded := run
	loaded.State = stateSuccess
	for _, recordKey := range loaded.TargetKeys {
		key := core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: s.config.BranchID, RecordKey: recordKey}
		_, target, err := s.loadTarget(ctx, key, runID)
		if err != nil {
			return Result{}, err
		}
		if target.State != stateSuccess {
			loaded.State = stateFailed
		}
	}
	loaded.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.putRun(ctx, runKey, runRecord.Revision, loaded); err != nil {
		return Result{}, err
	}
	return s.result(ctx, loaded)
}

func (s *Service) processTarget(ctx, websiteCtx, routerCtx context.Context, target *targetRecord) error {
	trigger := Trigger{Locale: target.Locale}
	digest, err := s.resolveInputDigest(websiteCtx, trustedTargetFromRecord(target), target.Locale, trigger)
	if err != nil {
		return err
	}
	if digest != target.InputDigest {
		return ErrStale
	}
	if target.BuildID == "" || target.ArtifactManifestFileID == "" {
		key, err := activationKeyWithRevision("build", target.RunID, target.RouteID, target.Locale, target.Revision)
		if err != nil {
			return err
		}
		job, err := s.config.Website.BuildSnapshot(websiteCtx, websiteapi.BuildSnapshotRequest{
			SnapshotID: target.PageSnapshotID, Locale: target.Locale, IdempotencyKey: key,
		})
		if err != nil || job.ID == "" {
			if err == nil {
				err = ErrUnavailable
			}
			return fmt.Errorf("build: %w", err)
		}
		if job.InputDigest != target.InputDigest {
			return ErrStale
		}
		target.BuildID = job.ID
		ready, err := s.awaitBuild(websiteCtx, job.ID)
		if err != nil {
			return err
		}
		if ready.State == "failed" || ready.ErrorCode != "" || ready.ArtifactManifestFileID == "" {
			target.BuildID, target.ArtifactManifestFileID = "", ""
			return fmt.Errorf("build: %s", valueOr(ready.ErrorCode, "not_ready"))
		}
		if ready.InputDigest != target.InputDigest || ready.SnapshotID != target.PageSnapshotID || ready.Locale != target.Locale {
			return ErrStale
		}
		packaged, err := s.packageArtifact(ctx, target.RunID, target.RouteID, target.Locale,
			ready.ArtifactManifestFileID, target.InputDigest)
		if err != nil {
			target.BuildID, target.ArtifactManifestFileID = "", ""
			return err
		}
		target.ArtifactManifestFileID = packaged
	}
	// A build that became ready is activated only if every trusted input still
	// resolves to the exact digest pinned when the fan-out record was created.
	fresh, err := s.resolveInputDigest(websiteCtx, trustedTargetFromRecord(target), target.Locale, trigger)
	if err != nil {
		return err
	}
	if fresh != target.InputDigest {
		return ErrStale
	}
	route, err := s.config.Router.GetRoute(routerCtx, target.RouteID)
	if err != nil {
		return fmt.Errorf("route read: %w", err)
	}
	// Re-check immediately before the CAS: a revision advanced while the
	// artifact was being built must invalidate this result rather than let an
	// old build activate against a newer trusted input.
	currentDigest, err := s.resolveInputDigest(websiteCtx, trustedTargetFromRecord(target), target.Locale, Trigger{})
	if err != nil {
		return err
	}
	if currentDigest != target.InputDigest {
		return ErrStale
	}
	slot, found := route.Target.LocaleTarget(target.Locale)
	if !found || slot.Revision < 1 {
		return fmt.Errorf("route locale: %w", ErrInvalid)
	}
	key, err := activationKey("cas", target.RunID, target.RouteID, target.Locale)
	if err != nil {
		return err
	}
	if _, err := s.config.Router.ReplaceLocaleTarget(routerCtx, routerapi.ReplaceLocaleTargetRequest{
		RouteID: target.RouteID, Locale: target.Locale, ArtifactManifestFileID: target.ArtifactManifestFileID,
		ExpectedRevision: slot.Revision, IdempotencyKey: key,
	}); err != nil {
		if errors.Is(err, routerapi.ErrConflict) {
			return fmt.Errorf("router cas: %w", ErrConflict)
		}
		return fmt.Errorf("router cas: %w", err)
	}
	return nil
}

func trustedTargetFromRecord(target *targetRecord) Target {
	return Target{RouteID: target.RouteID, PageSnapshotID: target.PageSnapshotID, Locale: target.Locale}
}

func (s *Service) awaitBuild(ctx context.Context, buildID string) (websiteapi.BuildJob, error) {
	deadline := time.Now().Add(s.config.BuildTimeout)
	for {
		job, err := s.config.Website.GetBuild(ctx, buildID)
		if err != nil {
			return websiteapi.BuildJob{}, fmt.Errorf("build status: %w", err)
		}
		if job.State == "ready" || job.State == "failed" {
			return job, nil
		}
		if time.Now().After(deadline) {
			return websiteapi.BuildJob{}, fmt.Errorf("build: %w", context.DeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			return websiteapi.BuildJob{}, ctx.Err()
		case <-time.After(s.config.PollInterval):
		}
	}
}

func (s *Service) resolveTrustedInput(ctx context.Context, target Target) (*prerendercontract.TrustedInput, error) {
	snapshot, err := s.config.Website.GetSnapshot(ctx, target.PageSnapshotID)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	if snapshot.State != "ready" || snapshot.ID != target.PageSnapshotID {
		return nil, fmt.Errorf("snapshot: %w", ErrInvalid)
	}
	revision, err := s.config.LocaleCatalog.LocaleCatalogRevision(ctx, target.Locale)
	if err != nil || revision < 0 {
		return nil, ErrUnavailable
	}
	input, err := s.config.InputResolver.ResolveBuildInput(ctx, snapshot, target.Locale, revision)
	if err != nil {
		return nil, fmt.Errorf("trusted input: %w", err)
	}
	if err := prerendercontract.ValidateTrustedInput(input); err != nil {
		return nil, fmt.Errorf("trusted input: %w", err)
	}
	return &input, nil
}

func (s *Service) resolveInputDigest(ctx context.Context, target Target, locale string, trigger Trigger) (string, error) {
	input, err := s.resolveTrustedInput(ctx, target)
	if err != nil {
		return "", err
	}
	digest, err := prerendercontract.InputDigest(*input)
	if err != nil {
		return "", fmt.Errorf("trusted input: %w", err)
	}
	return digest, nil
}

func (s *Service) result(ctx context.Context, run runRecord) (Result, error) {
	result := Result{RunID: run.RunID, State: run.State, Targets: make([]TargetResult, 0, len(run.TargetKeys))}
	for _, recordKey := range run.TargetKeys {
		key := core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: s.config.BranchID, RecordKey: recordKey}
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

func activationKeyWithRevision(kind, runID, routeID, locale string, revision int64) (string, error) {
	return activationKey(kind+"-"+strconv.FormatInt(revision, 10), runID, routeID, locale)
}

func activationKey(kind, runID, routeID, locale string) (string, error) {
	sum := sha256.Sum256([]byte(strings.Join([]string{kind, runID, routeID, locale}, "\x00")))
	return "pub-" + hex.EncodeToString(sum[:])[:32], nil
}

// packageArtifact converts a verified WEB-020 prerender manifest into the
// static-page-artifact/v1 manifest consumed by Router. Both the source and the
// packaged file are written only through Core capabilities; no build result can
// choose a route, locale, access group or Core file identity.
func (s *Service) packageArtifact(ctx context.Context, runID, routeID, locale, prerenderFileID, inputDigest string) (string, error) {
	session, _, err := s.config.FileIdentity.Authenticate(ctx, s.config.FileCredential)
	if err != nil {
		return "", fmt.Errorf("artifact identity: %w", err)
	}
	reader, _, err := s.config.Files.ReadFile(ctx, session, prerenderFileID)
	if err != nil {
		return "", fmt.Errorf("artifact read: %w", err)
	}
	defer reader.Close()
	limit := int64(s.config.Store.MaxPayloadBytes())
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return "", fmt.Errorf("artifact read: %w", ErrInvalid)
	}
	source, err := prerendercontract.VerifyManifest(data, inputDigest)
	if err != nil || source.Locale != locale || len(source.Artifacts) == 0 {
		return "", ErrStale
	}
	manifest := featureruntime.ArtifactManifest{
		Format: "static-page-artifact/v1", Assets: map[string]featureruntime.ArtifactFile{},
	}
	for _, artifact := range source.Artifacts {
		parsed, err := uuid.Parse(artifact.FileID)
		if err != nil || parsed == uuid.Nil || parsed.String() != artifact.FileID || artifact.SHA256 == "" || artifact.Size < 1 {
			return "", ErrInvalid
		}
		entry := featureruntime.ArtifactFile{
			FileID: artifact.FileID, ContentType: artifact.MediaType, SHA256: artifact.SHA256, Size: artifact.Size,
		}
		if artifact.Path == "index.html" {
			if manifest.Document.FileID != "" {
				return "", ErrInvalid
			}
			manifest.Document = entry
			continue
		}
		if artifact.Path == "" || strings.Contains(artifact.Path, "..") {
			return "", ErrInvalid
		}
		manifest.Assets[artifact.Path] = entry
	}
	if manifest.Document.FileID == "" {
		return "", ErrInvalid
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	manifest.Digest, err = featureruntime.ArtifactDigest(payload)
	if err != nil {
		return "", fmt.Errorf("artifact digest: %w", err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	key, err := activationKey("artifact", runID, routeID, locale)
	if err != nil {
		return "", err
	}
	fileID, err := s.config.Files.CreateFile(ctx, session,
		uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String(), []string{"features"},
		map[string]string{"artifact_path": "manifest.json", "record_type": "publication_artifact"},
		"application/json", int64(len(encoded)), bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("artifact create: %w", err)
	}
	return fileID, nil
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrStale):
		return "stale_build_input"
	case errors.Is(err, ErrConflict):
		return "router_cas_conflict"
	case errors.Is(err, context.DeadlineExceeded):
		return "build_timeout"
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

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
