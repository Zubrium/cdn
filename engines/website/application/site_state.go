package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

type siteRegistry struct {
	LastSequence int64 `json:"last_sequence"`
}

// loadSiteRegistry reads the page-tree serialization point used by compound
// Website snapshot writes. Each page maintains an independent sequence.
func (s *Service) loadSiteRegistry(ctx context.Context, siteID, pageID string) (core.ModuleDataRecord, siteRegistry, error) {
	record, err := s.store.Get(ctx, siteRegistryKey(siteID, pageID))
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return core.ModuleDataRecord{}, siteRegistry{}, nil
		}
		return core.ModuleDataRecord{}, siteRegistry{}, mapCoreError(err)
	}
	var state siteRegistry
	var index struct {
		RecordType string `json:"record_type"`
		Revision   int64  `json:"revision"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &state) != nil || state.LastSequence < 0 ||
		json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "site_registry" || index.Revision != record.Revision {
		return core.ModuleDataRecord{}, siteRegistry{}, websiteapi.ErrUnavailable
	}
	return record, state, nil
}

func (s *Service) ensureSiteSequence(ctx context.Context, siteID, pageID string, state siteRegistry) (int64, error) {
	if state.LastSequence > 0 {
		return state.LastSequence, nil
	}
	return s.maxSnapshotSequence(ctx, siteID, pageID)
}

func siteRegistryKey(siteID, pageID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "site", ScopeID: siteID, RecordKey: "registry/" + strings.ToLower(pageID)}
}

func siteRegistryMutation(siteID, pageID string, current core.ModuleDataRecord, lastSequence int64) (core.ModuleDataMutation, error) {
	if lastSequence < 0 {
		return core.ModuleDataMutation{}, websiteapi.ErrUnavailable
	}
	payload, err := json.Marshal(siteRegistry{LastSequence: lastSequence})
	if err != nil {
		return core.ModuleDataMutation{}, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(map[string]any{"record_type": "site_registry", "revision": current.Revision + 1})
	if err != nil {
		return core.ModuleDataMutation{}, websiteapi.ErrUnavailable
	}
	return core.ModuleDataMutation{
		Key: siteRegistryKey(siteID, pageID), ExpectedRevision: current.Revision,
		Value: &core.ModuleDataValue{Payload: payload, PayloadVersion: 1, IndexData: index},
	}, nil
}
