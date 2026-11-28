package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zubrium/cdn/pkg/contracts"
)

const dataVersion = 1

type runRecord struct {
	Attempt                    uint64                                `json:"attempt,omitempty"`
	AttemptTargets             []contracts.PublicationWorkflowTarget `json:"attempt_targets,omitempty"`
	Kind                       string                                `json:"kind"`
	RunID                      string                                `json:"run_id"`
	Locale                     string                                `json:"locale"`
	ContentPublicationRevision int64                                 `json:"content_publication_revision"`
	SystemModelRevision        int64                                 `json:"system_model_revision"`
	IdempotencyKey             string                                `json:"idempotency_key"`
	State                      string                                `json:"state"`
	ErrorCode                  string                                `json:"error_code,omitempty"`
	TargetKeys                 []string                              `json:"target_keys"`
	CreatedAt                  string                                `json:"created_at"`
	UpdatedAt                  string                                `json:"updated_at"`
}

type targetRecord struct {
	Attempt                    uint64 `json:"attempt,omitempty"`
	CASAttempt                 uint64 `json:"cas_attempt,omitempty"`
	Kind                       string `json:"kind"`
	RunID                      string `json:"run_id"`
	RouteID                    string `json:"route_id"`
	PageSnapshotID             string `json:"page_snapshot_id"`
	Locale                     string `json:"locale"`
	ContentPublicationRevision int64  `json:"content_publication_revision"`
	SystemModelRevision        int64  `json:"system_model_revision"`
	State                      string `json:"state"`
	Revision                   int64  `json:"revision"`
	InputDigest                string `json:"input_digest,omitempty"`
	BuildID                    string `json:"build_id,omitempty"`
	ArtifactManifestFileID     string `json:"artifact_manifest_file_id,omitempty"`
	ActivationExpectedRevision int64  `json:"activation_expected_revision,omitempty"`
	ErrorCode                  string `json:"error_code,omitempty"`
	CreatedAt                  string `json:"created_at"`
	UpdatedAt                  string `json:"updated_at"`
}

type artifactRecord struct {
	Kind                   string `json:"kind"`
	RunID                  string `json:"run_id"`
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	SnapshotID             string `json:"snapshot_id"`
	InputDigest            string `json:"input_digest"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
}

func runDataKey(runID string) contracts.RecordKey {
	return contracts.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: "run/" + runID}
}

func targetDataKey(runID, routeID, locale string) contracts.RecordKey {
	return contracts.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: "target/" + runID + "/" + routeID + "/" + locale}
}

func artifactDataKey(runID, routeID, locale string) contracts.RecordKey {
	return contracts.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: "artifact/" + runID + "/" + routeID + "/" + locale}
}

func runIndex(run runRecord) []byte {
	value, _ := json.Marshal(map[string]any{
		"record_type": recordTypeRun, "run_id": run.RunID, "state": run.State, "locale": run.Locale,
	})
	return value
}

func targetIndex(target targetRecord) []byte {
	value, _ := json.Marshal(map[string]any{
		"record_type": recordTypeTarget, "run_id": target.RunID, "state": target.State, "locale": target.Locale,
	})
	return value
}

func artifactIndex(artifact artifactRecord) []byte {
	value, _ := json.Marshal(map[string]any{
		"record_type": recordTypeArtifact, "run_id": artifact.RunID, "state": "ready", "locale": artifact.Locale,
	})
	return value
}

func encodeModuleData(payload any, index []byte) (*contracts.RecordValue, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode publication record: %v", ErrInvalid, err)
	}
	return &contracts.RecordValue{Payload: encoded, PayloadVersion: dataVersion, IndexData: index}, nil
}

func (s *Service) loadRun(ctx context.Context, runID string) (contracts.RecordKey, contracts.Record, runRecord, error) {
	key := runDataKey(runID)
	record, err := s.config.Store.Get(ctx, key)
	if err != nil {
		return contracts.RecordKey{}, contracts.Record{}, runRecord{}, mapStoreError(err)
	}
	var run runRecord
	if record.PayloadVersion != dataVersion || record.Revision < 1 || json.Unmarshal(record.Payload, &run) != nil ||
		run.Kind != recordTypeRun || run.RunID != runID || len(run.TargetKeys) == 0 {
		return contracts.RecordKey{}, contracts.Record{}, runRecord{}, fmt.Errorf("%w: invalid publication run record", ErrInvalid)
	}
	return key, record, run, nil
}

func (s *Service) loadTarget(ctx context.Context, key contracts.RecordKey, runID string) (contracts.Record, targetRecord, error) {
	record, err := s.config.Store.Get(ctx, key)
	if err != nil {
		return contracts.Record{}, targetRecord{}, mapStoreError(err)
	}
	var target targetRecord
	if record.PayloadVersion != dataVersion || record.Revision < 1 || json.Unmarshal(record.Payload, &target) != nil ||
		target.Kind != recordTypeTarget || target.RunID != runID || target.RouteID == "" || target.PageSnapshotID == "" ||
		target.Locale == "" || target.Revision < 1 {
		return contracts.Record{}, targetRecord{}, fmt.Errorf("%w: invalid publication target record", ErrInvalid)
	}
	return record, target, nil
}

func (s *Service) putRun(ctx context.Context, key contracts.RecordKey, expected int64, run runRecord) error {
	value, err := encodeModuleData(run, runIndex(run))
	if err != nil {
		return err
	}
	if _, err := s.config.Store.Put(ctx, key, *value, expected); err != nil {
		return mapStoreError(err)
	}
	return nil
}

func (s *Service) putTarget(ctx context.Context, key contracts.RecordKey, expected int64, target targetRecord) error {
	value, err := encodeModuleData(target, targetIndex(target))
	if err != nil {
		return err
	}
	if _, err := s.config.Store.Put(ctx, key, *value, expected); err != nil {
		return mapStoreError(err)
	}
	return nil
}

func (s *Service) loadArtifact(ctx context.Context, runID, routeID, locale string) (artifactRecord, error) {
	record, err := s.config.Store.Get(ctx, artifactDataKey(runID, routeID, locale))
	if err != nil {
		return artifactRecord{}, mapStoreError(err)
	}
	var artifact artifactRecord
	if record.PayloadVersion != dataVersion || json.Unmarshal(record.Payload, &artifact) != nil || artifact.Kind != recordTypeArtifact ||
		artifact.RunID != runID || artifact.RouteID != routeID || artifact.Locale != locale ||
		artifact.ArtifactManifestFileID == "" || artifact.InputDigest == "" {
		return artifactRecord{}, fmt.Errorf("%w: invalid publication artifact record", ErrInvalid)
	}
	return artifact, nil
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, contracts.ErrRecordConflict):
		return ErrConflict
	case errors.Is(err, contracts.ErrRecordInvalid), errors.Is(err, contracts.ErrRecordNotFound):
		return err
	default:
		return ErrUnavailable
	}
}
