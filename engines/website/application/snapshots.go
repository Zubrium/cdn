package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
)

const snapshotPageCursorVersion = 1

type snapshotPageCursor struct {
	Version int    `json:"v"`
	PageID  string `json:"p"`
	After   string `json:"a,omitempty"`
}

func (s *Service) ListSnapshots(ctx context.Context, pageID, cursor string, limit int) (websiteapi.SnapshotPage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.SnapshotPage{}, err
	}
	if limit < 1 || limit > s.store.MaxPageSize() {
		return websiteapi.SnapshotPage{}, websiteapi.ErrTooLarge
	}
	if pageID != "" && !identifierPattern.MatchString(pageID) {
		return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
	}
	pageID = strings.ToLower(pageID)
	var parsedCursor *snapshotPageCursor
	if cursor != "" {
		parsed, err := decodeSnapshotCursor(cursor)
		if err != nil || parsed.PageID != pageID {
			return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
		}
		parsedCursor = &parsed
	}
	// An empty page_id is the unfiltered Admin listing contract. A page filter
	// must still resolve to a known Website page before records are listed.
	if pageID != "" {
		if _, _, err := s.loadPage(ctx, pageID); err != nil {
			return websiteapi.SnapshotPage{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return websiteapi.SnapshotPage{}, err
	}
	state := snapshotPageCursor{Version: snapshotPageCursorVersion, PageID: pageID}
	if parsedCursor != nil {
		state = *parsedCursor
	}
	selectors := []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}},
	}
	if pageID != "" {
		selectors = append(selectors, core.ModuleDataSelector{Field: "page_id", Operator: core.SelectorEqual, Values: []any{pageID}})
	}
	keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "global", "global", state.After, selectors, limit)
	if err != nil {
		return websiteapi.SnapshotPage{}, mapCoreError(err)
	}
	if len(keys) != len(records) {
		return websiteapi.SnapshotPage{}, websiteapi.ErrUnavailable
	}
	page := websiteapi.SnapshotPage{Items: make([]websiteapi.Snapshot, 0, len(keys))}
	for i, record := range records {
		snapshot, err := decodeSnapshotRecord(keys[i], record)
		if err != nil || (pageID != "" && snapshot.PageID != pageID) {
			return websiteapi.SnapshotPage{}, websiteapi.ErrUnavailable
		}
		page.Items = append(page.Items, snapshot)
	}
	if next != "" {
		page.NextCursor = encodeSnapshotCursor(snapshotPageCursor{Version: snapshotPageCursorVersion, PageID: pageID, After: next})
	}
	return page, nil
}

func (s *Service) GetSnapshot(ctx context.Context, snapshotID string) (websiteapi.Snapshot, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.Snapshot{}, err
	}
	snapshot, _, _, err := s.findSnapshot(ctx, snapshotID)
	return snapshot, err
}

// MarkSnapshotReady finalizes a provisioning child snapshot after the durable
// fork workflow has confirmed content copy. It is idempotent for an already-ready
// snapshot and refuses stale or non-provisioning transitions.
func (s *Service) MarkSnapshotReady(ctx context.Context, snapshotID string, expectedRevision int64) (websiteapi.Snapshot, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.Snapshot{}, err
	}
	if expectedRevision < 1 {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	snapshot, key, record, err := s.findSnapshot(ctx, snapshotID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	if snapshot.State == "ready" {
		return snapshot, nil
	}
	if snapshot.State != "provisioning" || snapshot.ParentID == "" || record.Revision != expectedRevision {
		return websiteapi.Snapshot{}, websiteapi.ErrConflict
	}
	if record.Revision == math.MaxInt64 {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	updated := snapshot
	updated.State = "ready"
	updated.Revision = record.Revision + 1
	updated.CreatedAt = snapshot.CreatedAt
	payload, err := json.Marshal(updated)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if len(payload) > s.store.MaxPayloadBytes() {
		return websiteapi.Snapshot{}, websiteapi.ErrTooLarge
	}
	index, err := json.Marshal(map[string]any{
		"record_type": "snapshot", "snapshot_id": updated.ID, "page_id": updated.PageID, "state": updated.State,
		"revision": updated.Revision, "parent_id": updated.ParentID,
	})
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	mutations := []core.ModuleDataMutation{{
		Key: key, ExpectedRevision: record.Revision,
		Value: &core.ModuleDataValue{Payload: payload, PayloadVersion: 1, IndexData: index},
	}}
	pageRecord, pageData, err := s.loadPage(ctx, updated.PageID)
	if err == nil && pageRecord.LatestSnapshotID == updated.ID && pageRecord.LatestSnapshotState != "ready" {
		pageRecord.LatestSnapshotState = "ready"
		pageValue, err := pageModuleDataValue(pageRecord, pageRecord.Revision+1)
		if err != nil {
			return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
		}
		mutations = append(mutations, core.ModuleDataMutation{Key: websiterepo.PageKey(updated.PageID), ExpectedRevision: pageData.Revision, Value: pageValue})
	} else if err != nil && !errors.Is(err, websiteapi.ErrNotFound) {
		return websiteapi.Snapshot{}, err
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		mapped := mapCoreError(err)
		if errors.Is(mapped, websiteapi.ErrConflict) {
			current, _, currentRecord, getErr := s.findSnapshot(ctx, snapshotID)
			if getErr == nil && current.State == "ready" {
				return current, nil
			}
			if getErr == nil && currentRecord.Revision != expectedRevision {
				return websiteapi.Snapshot{}, websiteapi.ErrConflict
			}
		}
		return websiteapi.Snapshot{}, mapped
	}
	return updated, nil
}

func (s *Service) findSnapshot(ctx context.Context, snapshotID string) (websiteapi.Snapshot, core.ModuleDataKey, core.ModuleDataRecord, error) {
	if !snapshotIDPattern.MatchString(snapshotID) {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	selectors := []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}},
		{Field: "snapshot_id", Operator: core.SelectorEqual, Values: []any{strings.ToLower(snapshotID)}},
	}
	lookupLimit := 2
	if s.store.MaxPageSize() < lookupLimit {
		lookupLimit = s.store.MaxPageSize()
	}
	if err := ctx.Err(); err != nil {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, err
	}
	keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "global", "global", "", selectors, lookupLimit)
	if err != nil {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, mapCoreError(err)
	}
	if len(keys) != len(records) || next != "" {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
	}
	if len(keys) == 0 {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	if len(keys) > 1 {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
	}
	snapshot, err := decodeSnapshotRecord(keys[0], records[0])
	if err != nil || snapshot.ID != strings.ToLower(snapshotID) {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
	}
	return snapshot, keys[0], records[0], nil
}

func (s *Service) snapshotInPage(ctx context.Context, pageID, snapshotID string) (websiteapi.Snapshot, core.ModuleDataRecord, error) {
	if !identifierPattern.MatchString(pageID) || !snapshotIDPattern.MatchString(snapshotID) {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	key := snapshotKey(pageID, snapshotID)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, mapCoreError(err)
	}
	snapshot, err := decodeSnapshotRecord(key, record)
	if err != nil || snapshot.PageID != pageID {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	return snapshot, record, nil
}

func snapshotKey(pageID, snapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "global", ScopeID: "global",
		RecordKey: "snapshot/" + strings.ToLower(pageID) + "/" + strings.ToLower(snapshotID)}
}

func decodeSnapshotRecord(key core.ModuleDataKey, record core.ModuleDataRecord) (websiteapi.Snapshot, error) {
	var index struct {
		RecordType string `json:"record_type"`
		SnapshotID string `json:"snapshot_id"`
		PageID     string `json:"page_id"`
		State      string `json:"state"`
		Revision   int64  `json:"revision"`
		ParentID   string `json:"parent_id"`
	}
	if json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "snapshot" || key.Namespace != snapshotsNamespace || key.ScopeType != "global" || key.ScopeID != "global" || record.PayloadVersion != 1 || record.Revision < 1 {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	var snapshot websiteapi.Snapshot
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if key.RecordKey != "snapshot/"+snapshot.PageID+"/"+snapshot.ID || !identifierPattern.MatchString(snapshot.PageID) ||
		!snapshotIDPattern.MatchString(snapshot.ID) || index.SnapshotID != snapshot.ID || index.PageID != snapshot.PageID || index.State != snapshot.State || !validSnapshotState(snapshot.State) || index.Revision < 1 || index.ParentID != snapshot.ParentID ||
		!validSnapshotName(snapshot.Name) || snapshot.Sequence < 1 || snapshot.Payload == nil || !validJSONObject(snapshot.Payload.Config) || !validJSONObject(snapshot.Payload.Metadata) {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if snapshot.ParentID != "" && !snapshotIDPattern.MatchString(snapshot.ParentID) || snapshot.ParentID == snapshot.ID {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if snapshot.FeaturePins == nil {
		snapshot.FeaturePins = map[string]string{}
	}
	page, err := snapshotPageFromConfig(snapshot.Payload.Config)
	if err != nil || (page.ID != "" && page.ID != snapshot.PageID) {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	derivedPins, err := featurePinsFromConfig(snapshot.Payload.Config)
	if err != nil || !sameFeaturePins(snapshot.FeaturePins, derivedPins) {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	for featureID, version := range snapshot.FeaturePins {
		if !identifierPattern.MatchString(featureID) || !featureVersionPattern.MatchString(version) {
			return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
		}
	}
	snapshot.Revision = record.Revision
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = record.CreatedAt
	}
	return snapshot, nil
}

func validSnapshotState(value string) bool {
	return value == "provisioning" || value == "ready" || value == "failed"
}

func encodeSnapshotCursor(cursor snapshotPageCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeSnapshotCursor(value string) (snapshotPageCursor, error) {
	if len(value) > 1024 {
		return snapshotPageCursor{}, fmt.Errorf("cursor too large")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return snapshotPageCursor{}, err
	}
	var cursor snapshotPageCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != snapshotPageCursorVersion ||
		(cursor.PageID != "" && !identifierPattern.MatchString(cursor.PageID)) || len(cursor.After) > 256 {
		return snapshotPageCursor{}, fmt.Errorf("invalid cursor")
	}
	if cursor.After != "" {
		parts := strings.Split(cursor.After, "/")
		if len(parts) != 3 || parts[0] != "snapshot" || !identifierPattern.MatchString(parts[1]) || !snapshotIDPattern.MatchString(parts[2]) ||
			(cursor.PageID != "" && parts[1] != cursor.PageID) {
			return snapshotPageCursor{}, fmt.Errorf("invalid cursor")
		}
	}
	return cursor, nil
}
