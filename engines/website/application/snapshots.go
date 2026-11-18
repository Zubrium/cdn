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
	After     string `json:"a,omitempty"`
}

func (s *Service) ListSnapshots(ctx context.Context, siteID, cursor string, limit int) (websiteapi.SnapshotPage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.SnapshotPage{}, err
	}
	if limit < 1 || limit > s.store.MaxPageSize() {
		return websiteapi.SnapshotPage{}, websiteapi.ErrTooLarge
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
	state := snapshotPageCursor{Version: snapshotPageCursorVersion, SiteIndex: firstSite}
	if firstSite < len(s.config.Sites) {
		state.SiteID = s.config.Sites[firstSite]
	}
	if cursor != "" {
		parsed, err := decodeSnapshotCursor(cursor, s.config.Sites)
		if err != nil || parsed.SiteIndex < firstSite || parsed.SiteIndex >= lastSite {
			return websiteapi.SnapshotPage{}, websiteapi.ErrInvalid
		}
		state = parsed
	}

	page := websiteapi.SnapshotPage{Items: make([]websiteapi.Snapshot, 0, limit)}
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}}}
	for state.SiteIndex < lastSite && len(page.Items) < limit {
		if err := ctx.Err(); err != nil {
			return websiteapi.SnapshotPage{}, err
		}
		site := s.config.Sites[state.SiteIndex]
		keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "site", site, state.After, selectors, limit-len(page.Items))
		if err != nil {
			return websiteapi.SnapshotPage{}, mapCoreError(err)
		}
		for i, record := range records {
			snapshot, err := decodeSnapshotRecord(site, keys[i], record)
			if err != nil {
				return websiteapi.SnapshotPage{}, err
			}
			page.Items = append(page.Items, snapshot)
		}
		if next != "" {
			page.NextCursor = encodeSnapshotCursor(snapshotPageCursor{Version: snapshotPageCursorVersion, SiteIndex: state.SiteIndex, SiteID: site, After: next})
			break
		}
		state.SiteIndex++
		state.After = ""
	}
	if page.NextCursor == "" && len(page.Items) == limit && state.SiteIndex < lastSite {
		// A full page ended exactly at a site boundary. Probe subsequent scopes
		// with a single row so the API does not emit an empty terminal page.
		for probe := state.SiteIndex; probe < lastSite; probe++ {
			keys, _, _, err := s.store.List(ctx, snapshotsNamespace, "site", s.config.Sites[probe], "", selectors, 1)
			if err != nil {
				return websiteapi.SnapshotPage{}, mapCoreError(err)
			}
			if len(keys) != 0 {
				page.NextCursor = encodeSnapshotCursor(snapshotPageCursor{Version: snapshotPageCursorVersion, SiteIndex: probe, SiteID: s.config.Sites[probe]})
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
	if !snapshotIDPattern.MatchString(snapshotID) {
		return websiteapi.Snapshot{}, websiteapi.ErrNotFound
	}
	var found *websiteapi.Snapshot
	for _, siteID := range s.config.Sites {
		if err := ctx.Err(); err != nil {
			return websiteapi.Snapshot{}, err
		}
		key := snapshotKey(siteID, snapshotID)
		record, err := s.store.Get(ctx, key)
		if err != nil {
			if errors.Is(err, core.ErrModuleDataNotFound) {
				continue
			}
			return websiteapi.Snapshot{}, mapCoreError(err)
		}
		snapshot, err := decodeSnapshotRecord(siteID, key, record)
		if err != nil {
			return websiteapi.Snapshot{}, err
		}
		if found != nil {
			return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
		}
		found = &snapshot
	}
	if found == nil {
		return websiteapi.Snapshot{}, websiteapi.ErrNotFound
	}
	return *found, nil
}

func (s *Service) GetSnapshotDesignSystem(ctx context.Context, snapshotID string) (websiteapi.SnapshotDesignSystem, error) {
	snapshot, err := s.GetSnapshot(ctx, snapshotID)
	if err != nil {
		return websiteapi.SnapshotDesignSystem{}, err
	}
	if snapshot.Payload == nil {
		return websiteapi.SnapshotDesignSystem{}, websiteapi.ErrUnavailable
	}
	// Feature-token definitions depend on immutable catalog manifests and are
	// added by the import/catalog worker. Until that capability is available,
	// return the valid empty collection rather than inventing definitions.
	return websiteapi.SnapshotDesignSystem{
		Snapshot: snapshot, DesignSystem: append(json.RawMessage(nil), snapshot.Payload.DesignSystem...),
		FeatureTokens: json.RawMessage("[]"),
	}, nil
}

func (s *Service) snapshotInSite(ctx context.Context, siteID, snapshotID string) (websiteapi.Snapshot, core.ModuleDataRecord, error) {
	if _, exists := s.sites[siteID]; !exists || !snapshotIDPattern.MatchString(snapshotID) {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, websiteapi.ErrNotFound
	}
	key := snapshotKey(siteID, snapshotID)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return websiteapi.Snapshot{}, core.ModuleDataRecord{}, mapCoreError(err)
	}
	snapshot, err := decodeSnapshotRecord(siteID, key, record)
	return snapshot, record, err
}

func snapshotKey(siteID, snapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "site", ScopeID: siteID, RecordKey: "snapshot/" + strings.ToLower(snapshotID)}
}

func decodeSnapshotRecord(siteID string, key core.ModuleDataKey, record core.ModuleDataRecord) (websiteapi.Snapshot, error) {
	var index struct {
		RecordType string `json:"record_type"`
	}
	if json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "snapshot" || key.Namespace != snapshotsNamespace || key.ScopeType != "site" || key.ScopeID != siteID || record.PayloadVersion != 1 || record.Revision < 1 || !strings.HasPrefix(key.RecordKey, "snapshot/") {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	var snapshot websiteapi.Snapshot
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if key.RecordKey != "snapshot/"+snapshot.ID || snapshot.SiteID != siteID || !snapshotIDPattern.MatchString(snapshot.ID) ||
		strings.TrimSpace(snapshot.Name) != snapshot.Name || len([]rune(snapshot.Name)) < 1 || len([]rune(snapshot.Name)) > 120 || strings.ContainsAny(snapshot.Name, "\r\n\t") ||
		snapshot.Sequence < 1 || len(snapshot.State) < 1 || len(snapshot.State) > 32 || strings.ContainsAny(snapshot.State, "\r\n\t") || snapshot.Payload == nil || !validJSONObject(snapshot.Payload.Config) ||
		!validJSONObject(snapshot.Payload.Metadata) || !validJSONObject(snapshot.Payload.DesignSystem) {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if snapshot.ParentID != "" && !snapshotIDPattern.MatchString(snapshot.ParentID) || snapshot.ParentID == snapshot.ID {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if snapshot.FeaturePins == nil {
		snapshot.FeaturePins = map[string]string{}
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

func validJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return len(trimmed) >= 2 && trimmed[0] == '{' && json.Valid(raw)
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
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != snapshotPageCursorVersion || cursor.SiteIndex < 0 || cursor.SiteIndex >= len(sites) || cursor.SiteID != sites[cursor.SiteIndex] || len(cursor.After) > 256 || (cursor.After != "" && strings.ContainsRune(cursor.After, '\x00')) {
		return snapshotPageCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}
