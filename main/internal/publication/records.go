package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core "github.com/Zubrium/cdn/core/presentation"
)

const dataVersion = 1

type runRecord struct {
	Kind                       string   `json:"kind"`
	RunID                      string   `json:"run_id"`
	BranchID                   string   `json:"branch_id"`
	Locale                     string   `json:"locale"`
	ContentPublicationRevision int64    `json:"content_publication_revision"`
	SystemModelRevision        int64    `json:"system_model_revision"`
	State                      string   `json:"state"`
	TargetKeys                 []string `json:"target_keys"`
	CreatedAt                  string   `json:"created_at"`
	UpdatedAt                  string   `json:"updated_at"`
}

type targetRecord struct {
	Kind                   string `json:"kind"`
	RunID                  string `json:"run_id"`
	RouteID                string `json:"route_id"`
	PageSnapshotID         string `json:"page_snapshot_id"`
	Locale                 string `json:"locale"`
	State                  string `json:"state"`
	Revision               int64  `json:"revision"`
	InputDigest            string `json:"input_digest"`
	BuildID                string `json:"build_id,omitempty"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id,omitempty"`
	ErrorCode              string `json:"error_code,omitempty"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

func runDataKey(branchID, runID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: branchID, RecordKey: "run/" + runID}
}

func targetDataKey(branchID, runID, routeID, locale string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: branchID, RecordKey: "target/" + runID + "/" + routeID + "/" + locale}
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

func encodeModuleData(payload any, index []byte) (*core.ModuleDataValue, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode publication record: %v", ErrInvalid, err)
	}
	return &core.ModuleDataValue{Payload: encoded, PayloadVersion: dataVersion, IndexData: index}, nil
}

func (s *Service) loadRun(ctx context.Context, runID string) (core.ModuleDataKey, core.ModuleDataRecord, runRecord, error) {
	key := runDataKey(s.config.BranchID, runID)
	record, err := s.config.Store.Get(ctx, key)
	if err != nil {
		return core.ModuleDataKey{}, core.ModuleDataRecord{}, runRecord{}, mapStoreError(err)
	}
	var run runRecord
	if record.PayloadVersion != dataVersion || record.Revision < 1 || json.Unmarshal(record.Payload, &run) != nil ||
		run.Kind != recordTypeRun || run.RunID != runID || run.BranchID != s.config.BranchID || len(run.TargetKeys) == 0 {
		return core.ModuleDataKey{}, core.ModuleDataRecord{}, runRecord{}, fmt.Errorf("%w: invalid publication run record", ErrInvalid)
	}
	return key, record, run, nil
}

func (s *Service) loadTarget(ctx context.Context, key core.ModuleDataKey, runID string) (core.ModuleDataRecord, targetRecord, error) {
	record, err := s.config.Store.Get(ctx, key)
	if err != nil {
		return core.ModuleDataRecord{}, targetRecord{}, mapStoreError(err)
	}
	var target targetRecord
	if record.PayloadVersion != dataVersion || record.Revision < 1 || json.Unmarshal(record.Payload, &target) != nil ||
		target.Kind != recordTypeTarget || target.RunID != runID || target.RouteID == "" || target.Locale == "" || target.Revision < 1 {
		return core.ModuleDataRecord{}, targetRecord{}, fmt.Errorf("%w: invalid publication target record", ErrInvalid)
	}
	return record, target, nil
}

func (s *Service) putRun(ctx context.Context, key core.ModuleDataKey, expected int64, run runRecord) error {
	value, err := encodeModuleData(run, runIndex(run))
	if err != nil {
		return err
	}
	if _, err := s.config.Store.Put(ctx, key, *value, expected); err != nil {
		return mapStoreError(err)
	}
	return nil
}

func (s *Service) putTarget(ctx context.Context, key core.ModuleDataKey, expected int64, target targetRecord) error {
	value, err := encodeModuleData(target, targetIndex(target))
	if err != nil {
		return err
	}
	if _, err := s.config.Store.Put(ctx, key, *value, expected); err != nil {
		return mapStoreError(err)
	}
	return nil
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrModuleDataConflict):
		return ErrConflict
	case errors.Is(err, core.ErrModuleDataInvalid), errors.Is(err, core.ErrModuleDataNotFound):
		return ErrInvalid
	default:
		return ErrUnavailable
	}
}
