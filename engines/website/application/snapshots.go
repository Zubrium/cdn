package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

const snapshotPageCursorVersion = 1

type snapshotPageCursor struct {
	Version   int    `json:"v"`
	SiteIndex int    `json:"s"`
	SiteID    string `json:"i"`
	PageID    string `json:"p"`
	After     string `json:"a,omitempty"`
}

func (s *Service) ListSnapshots(ctx context.Context, siteID, pageID, cursor string, limit int) (websiteapi.SnapshotPage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.SnapshotPage{}, err
	}
	if limit < 1 || limit > s.store.MaxPageSize() {
		return websiteapi.SnapshotPage{}, websiteapi.ErrTooLarge
	}
	if !identifierPattern.MatchString(pageID) {
		return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
	}
	pageID = strings.ToLower(pageID)
	// Validate the opaque cursor before any target lookup so a malformed cursor
	// is reported as an invalid request rather than as a missing site or page.
	var parsedCursor *snapshotPageCursor
	if cursor != "" {
		parsed, err := decodeSnapshotCursor(cursor, s.config.Sites)
		if err != nil || parsed.PageID != pageID {
			return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
		}
		parsedCursor = &parsed
	}
	if siteID != "" {
		if !identifierPattern.MatchString(siteID) {
			return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
		}
		if _, exists := s.sites[siteID]; !exists {
			return websiteapi.SnapshotPage{}, websiteapi.ErrNotFound
		}
		if _, _, err := s.loadPage(ctx, siteID, pageID); err != nil {
			return websiteapi.SnapshotPage{}, err
		}
	} else {
		configured := false
		for _, candidate := range s.config.Sites {
			if _, _, err := s.loadPage(ctx, candidate, pageID); err == nil {
				configured = true
				break
			} else if !errors.Is(err, websiteapi.ErrNotFound) {
				return websiteapi.SnapshotPage{}, err
			}
		}
		if !configured {
			return websiteapi.SnapshotPage{}, websiteapi.ErrNotFound
		}
	}
	if err := ctx.Err(); err != nil {
		return websiteapi.SnapshotPage{}, err
	}
	firstSite := 0
	lastSite := len(s.config.Sites)
	if siteID != "" {
		if !identifierPattern.MatchString(siteID) {
			return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
		}
		if _, exists := s.sites[siteID]; !exists {
			return websiteapi.SnapshotPage{}, websiteapi.ErrNotFound
		}
		firstSite = sortedStringIndex(s.config.Sites, siteID)
		lastSite = firstSite + 1
	}
	state := snapshotPageCursor{Version: snapshotPageCursorVersion, SiteIndex: firstSite, PageID: pageID}
	if firstSite < len(s.config.Sites) {
		state.SiteID = s.config.Sites[firstSite]
	}
	if parsedCursor != nil {
		if parsedCursor.SiteIndex < firstSite || parsedCursor.SiteIndex >= lastSite {
			return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
		}
		state = *parsedCursor
	}

	page := websiteapi.SnapshotPage{Items: make([]websiteapi.Snapshot, 0, limit)}
	selectors := []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}},
		{Field: "page_id", Operator: core.SelectorEqual, Values: []any{pageID}},
	}
	for state.SiteIndex < lastSite && len(page.Items) < limit {
		if err := ctx.Err(); err != nil {
			return websiteapi.SnapshotPage{}, err
		}
		site := s.config.Sites[state.SiteIndex]
		keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "site", site, state.After, selectors, limit-len(page.Items))
		if err != nil {
			return websiteapi.SnapshotPage{}, mapCoreError(err)
		}
		if len(keys) != len(records) {
			return websiteapi.SnapshotPage{}, websiteapi.ErrUnavailable
		}
		for i, record := range records {
			snapshot, err := decodeSnapshotRecord(site, keys[i], record)
			if err != nil || snapshot.PageID != pageID {
				return websiteapi.SnapshotPage{}, websiteapi.ErrUnavailable
			}
			page.Items = append(page.Items, snapshot)
		}
		if next != "" {
			page.NextCursor = encodeSnapshotCursor(snapshotPageCursor{Version: snapshotPageCursorVersion, SiteIndex: state.SiteIndex, SiteID: site, PageID: pageID, After: next})
			break
		}
		state.SiteIndex++
		state.After = ""
	}
	if page.NextCursor == "" && len(page.Items) == limit && state.SiteIndex < lastSite {
		for probe := state.SiteIndex; probe < lastSite; probe++ {
			keys, _, _, err := s.store.List(ctx, snapshotsNamespace, "site", s.config.Sites[probe], "", selectors, 1)
			if err != nil {
				return websiteapi.SnapshotPage{}, mapCoreError(err)
			}
			if len(keys) != 0 {
				page.NextCursor = encodeSnapshotCursor(snapshotPageCursor{Version: snapshotPageCursorVersion, SiteIndex: probe, SiteID: s.config.Sites[probe], PageID: pageID})
				break
			}
		}
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

// MarkSnapshotReady remains fail-closed until composition provides the durable
// child-fork workflow that is responsible for validating and finalizing a child.
// A revision check alone cannot prove that the fork actually completed.
func (s *Service) MarkSnapshotReady(ctx context.Context, snapshotID string, expectedRevision int64) (websiteapi.Snapshot, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.Snapshot{}, err
	}
	if expectedRevision < 1 {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	snapshot, _, record, err := s.findSnapshot(ctx, snapshotID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	if snapshot.State == "ready" {
		return snapshot, nil
	}
	if snapshot.State != "provisioning" || snapshot.ParentID == "" || record.Revision != expectedRevision {
		return websiteapi.Snapshot{}, websiteapi.ErrConflict
	}
	return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
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
	var found *websiteapi.Snapshot
	var foundKey core.ModuleDataKey
	var foundRecord core.ModuleDataRecord
	for _, siteID := range s.config.Sites {
		if err := ctx.Err(); err != nil {
			return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, err
		}
		keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "site", siteID, "", selectors, lookupLimit)
		if err != nil {
			return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, mapCoreError(err)
		}
		if len(keys) != len(records) || next != "" || len(keys) > 1 {
			return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
		}
		if len(keys) == 0 {
			continue
		}
		snapshot, err := decodeSnapshotRecord(siteID, keys[0], records[0])
		if err != nil || snapshot.ID != strings.ToLower(snapshotID) {
			return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
		}
		if found != nil {
			return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
		}
		found, foundKey, foundRecord = &snapshot, keys[0], records[0]
	}
	if found == nil {
		return websiteapi.Snapshot{}, core.ModuleDataKey{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	return *found, foundKey, foundRecord, nil
}

func (s *Service) snapshotInPage(ctx context.Context, siteID, pageID, snapshotID string) (websiteapi.Snapshot, core.ModuleDataRecord, error) {
	if _, exists := s.sites[siteID]; !exists || !identifierPattern.MatchString(pageID) || !snapshotIDPattern.MatchString(snapshotID) {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	key := snapshotKey(siteID, pageID, snapshotID)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, mapCoreError(err)
	}
	snapshot, err := decodeSnapshotRecord(siteID, key, record)
	if err != nil || snapshot.PageID != pageID {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	return snapshot, record, nil
}

func snapshotKey(siteID, pageID, snapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "site", ScopeID: siteID,
		RecordKey: "snapshot/" + strings.ToLower(pageID) + "/" + strings.ToLower(snapshotID)}
}

func decodeSnapshotRecord(siteID string, key core.ModuleDataKey, record core.ModuleDataRecord) (websiteapi.Snapshot, error) {
	var index struct {
		RecordType string `json:"record_type"`
		SnapshotID string `json:"snapshot_id"`
		PageID     string `json:"page_id"`
		State      string `json:"state"`
		Revision   int64  `json:"revision"`
		ParentID   string `json:"parent_id"`
	}
	if json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "snapshot" || key.Namespace != snapshotsNamespace || key.ScopeType != "site" || key.ScopeID != siteID || record.PayloadVersion != 1 || record.Revision < 1 {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	var snapshot websiteapi.Snapshot
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if key.RecordKey != "snapshot/"+snapshot.PageID+"/"+snapshot.ID || snapshot.SiteID != siteID || !identifierPattern.MatchString(snapshot.PageID) ||
		!snapshotIDPattern.MatchString(snapshot.ID) || index.SnapshotID != snapshot.ID || index.PageID != snapshot.PageID || index.State != snapshot.State || !validSnapshotState(snapshot.State) || index.Revision != record.Revision || index.ParentID != snapshot.ParentID ||
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

func sortedStringIndex(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func encodeSnapshotCursor(cursor snapshotPageCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeSnapshotCursor(value string, sites []string) (snapshotPageCursor, error) {
	if len(value) > 1024 {
		return snapshotPageCursor{}, fmt.Errorf("cursor too large")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return snapshotPageCursor{}, err
	}
	var cursor snapshotPageCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != snapshotPageCursorVersion || cursor.SiteIndex < 0 || cursor.SiteIndex >= len(sites) || cursor.SiteID != sites[cursor.SiteIndex] || !identifierPattern.MatchString(cursor.PageID) || len(cursor.After) > 256 || (cursor.After != "" && (!strings.HasPrefix(cursor.After, "snapshot/"+cursor.PageID+"/") || !snapshotIDPattern.MatchString(strings.TrimPrefix(cursor.After, "snapshot/"+cursor.PageID+"/")))) {
		return snapshotPageCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}
