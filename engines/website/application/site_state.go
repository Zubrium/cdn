package application

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"context"
	"encoding/json"
	"errors"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"strings"
)

type pageTreeRegistry struct {
	LastSequence int64 `json:"last_sequence"`
}

// loadPageTreeRegistry reads the page-tree serialization point used by compound
// Website snapshot writes. Each page maintains an independent sequence.
func (s *Service) loadPageTreeRegistry(ctx context.Context, pageID string) (recorddata.Record, pageTreeRegistry, error) {
	record, err := s.store.Get(ctx, pageTreeRegistryKey(pageID))
	if err != nil {
		if errors.Is(err, recorddata.ErrRecordNotFound) {
			return recorddata.Record{}, pageTreeRegistry{}, nil
		}
		return recorddata.Record{}, pageTreeRegistry{}, mapCoreError(err)
	}
	var state pageTreeRegistry
	var index struct {
		RecordType string `json:"record_type"`
		Revision   int64  `json:"revision"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &state) != nil || state.LastSequence < 0 ||
		json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "page_tree_registry" || index.Revision < 1 {
		return recorddata.Record{}, pageTreeRegistry{}, websiteapi.ErrUnavailable
	}
	return record, state, nil
}

func (s *Service) ensurePageTreeSequence(ctx context.Context, pageID string, state pageTreeRegistry) (int64, error) {
	if state.LastSequence > 0 {
		return state.LastSequence, nil
	}
	return s.maxSnapshotSequence(ctx, pageID)
}

func pageTreeRegistryKey(pageID string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: snapshotsNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "registry/" + strings.ToLower(pageID)}
}

func pageTreeRegistryMutation(pageID string, current recorddata.Record, lastSequence int64) (recorddata.RecordMutation, error) {
	if lastSequence < 0 {
		return recorddata.RecordMutation{}, websiteapi.ErrUnavailable
	}
	payload, err := json.Marshal(pageTreeRegistry{LastSequence: lastSequence})
	if err != nil {
		return recorddata.RecordMutation{}, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(map[string]any{"record_type": "page_tree_registry", "revision": current.Revision + 1})
	if err != nil {
		return recorddata.RecordMutation{}, websiteapi.ErrUnavailable
	}
	return recorddata.RecordMutation{
		Key: pageTreeRegistryKey(pageID), ExpectedRevision: current.Revision,
		Value: &recorddata.RecordValue{Payload: payload, PayloadVersion: 1, IndexData: index},
	}, nil
}
