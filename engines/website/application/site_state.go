package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

type pageTreeRegistry struct {
	LastSequence int64 `json:"last_sequence"`
}

// loadPageTreeRegistry reads the page-tree serialization point used by compound
// Website snapshot writes. Each page maintains an independent sequence.
func (s *Service) loadPageTreeRegistry(ctx context.Context, pageID string) (core.ModuleDataRecord, pageTreeRegistry, error) {
	record, err := s.store.Get(ctx, pageTreeRegistryKey(pageID))
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return core.ModuleDataRecord{}, pageTreeRegistry{}, nil
		}
		return core.ModuleDataRecord{}, pageTreeRegistry{}, mapCoreError(err)
	}
	var state pageTreeRegistry
	var index struct {
		RecordType string `json:"record_type"`
		Revision   int64  `json:"revision"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &state) != nil || state.LastSequence < 0 ||
		json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "page_tree_registry" || index.Revision < 1 {
		return core.ModuleDataRecord{}, pageTreeRegistry{}, websiteapi.ErrUnavailable
	}
	return record, state, nil
}

func (s *Service) ensurePageTreeSequence(ctx context.Context, pageID string, state pageTreeRegistry) (int64, error) {
	if state.LastSequence > 0 {
		return state.LastSequence, nil
	}
	return s.maxSnapshotSequence(ctx, pageID)
}

func pageTreeRegistryKey(pageID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "registry/" + strings.ToLower(pageID)}
}

func pageTreeRegistryMutation(pageID string, current core.ModuleDataRecord, lastSequence int64) (core.ModuleDataMutation, error) {
	if lastSequence < 0 {
		return core.ModuleDataMutation{}, websiteapi.ErrUnavailable
	}
	payload, err := json.Marshal(pageTreeRegistry{LastSequence: lastSequence})
	if err != nil {
		return core.ModuleDataMutation{}, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(map[string]any{"record_type": "page_tree_registry", "revision": current.Revision + 1})
	if err != nil {
		return core.ModuleDataMutation{}, websiteapi.ErrUnavailable
	}
	return core.ModuleDataMutation{
		Key: pageTreeRegistryKey(pageID), ExpectedRevision: current.Revision,
		Value: &core.ModuleDataValue{Payload: payload, PayloadVersion: 1, IndexData: index},
	}, nil
}
