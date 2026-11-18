package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

const snapshotsNamespace = "website/snapshots"

type branchState struct {
	ID         string    `json:"id"`
	SnapshotID string    `json:"snapshot_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Service) ListBranches(ctx context.Context) (websiteapi.BranchPage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.BranchPage{}, err
	}
	page := websiteapi.BranchPage{Items: make([]websiteapi.Branch, 0, len(s.branchOrder))}
	for _, id := range s.branchOrder {
		configured := s.branches[id]
		state, record, err := s.loadBranchState(ctx, configured)
		if err != nil {
			return websiteapi.BranchPage{}, err
		}
		branch := branchFromConfig(configured)
		branch.Revision = record.Revision
		branch.UpdatedAt = record.UpdatedAt
		if state.SnapshotID != "" {
			snapshotID := state.SnapshotID
			branch.SnapshotID = &snapshotID
		}
		page.Items = append(page.Items, branch)
	}
	return page, nil
}

func (s *Service) AssignBranch(ctx context.Context, request websiteapi.AssignBranchRequest) (websiteapi.Branch, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.Branch{}, err
	}
	if !identifierPattern.MatchString(request.BranchID) || !snapshotIDPattern.MatchString(request.SnapshotID) || request.ExpectedRevision < 0 || request.ExpectedSnapshotRevision < 1 {
		return websiteapi.Branch{}, websiteapi.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return websiteapi.Branch{}, err
	}
	configured, exists := s.branches[request.BranchID]
	if !exists {
		return websiteapi.Branch{}, websiteapi.ErrNotFound
	}
	snapshot, snapshotRecord, err := s.snapshotInSite(ctx, configured.SiteID, request.SnapshotID)
	if err != nil {
		return websiteapi.Branch{}, err
	}
	if snapshotRecord.Revision != request.ExpectedSnapshotRevision {
		return websiteapi.Branch{}, websiteapi.ErrConflict
	}

	key := branchStateKey(configured)
	state, currentRecord, err := s.loadBranchState(ctx, configured)
	if err != nil {
		return websiteapi.Branch{}, err
	}
	if currentRecord.Revision != request.ExpectedRevision {
		return websiteapi.Branch{}, websiteapi.ErrConflict
	}
	if state.SnapshotID == request.SnapshotID {
		return branchFromAssignment(configured, request.SnapshotID, currentRecord.Revision, currentRecord.UpdatedAt), nil
	}

	now := time.Now().UTC()
	eventID, err := randomUUID()
	if err != nil {
		return websiteapi.Branch{}, websiteapi.ErrUnavailable
	}
	event := websiteapi.BranchEvent{
		ID: eventID, BranchID: configured.ID, SnapshotID: snapshot.ID, SnapshotName: snapshot.Name,
		SnapshotSequence: snapshot.Sequence, Action: "assigned", CreatedAt: now,
	}
	newState := branchState{ID: configured.ID, SnapshotID: snapshot.ID, UpdatedAt: now}
	statePayload, err := json.Marshal(newState)
	if err != nil {
		return websiteapi.Branch{}, websiteapi.ErrUnavailable
	}
	eventPayload, err := json.Marshal(event)
	if err != nil {
		return websiteapi.Branch{}, websiteapi.ErrUnavailable
	}
	branchIndex := map[string]any{"record_type": "branch", "branch_id": configured.ID, "snapshot_id": snapshot.ID, "state": "assigned", "revision": float64(currentRecord.Revision + 1)}
	eventIndex := map[string]any{"record_type": "branch_event", "branch_id": configured.ID, "snapshot_id": snapshot.ID, "action": event.Action}
	branchIndexJSON, err := json.Marshal(branchIndex)
	if err != nil {
		return websiteapi.Branch{}, websiteapi.ErrUnavailable
	}
	eventIndexJSON, err := json.Marshal(eventIndex)
	if err != nil {
		return websiteapi.Branch{}, websiteapi.ErrUnavailable
	}
	eventKey := core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "site", ScopeID: configured.SiteID,
		RecordKey: fmt.Sprintf("event/%s/%s/%s", configured.ID, now.Format("20060102T150405.000000000Z"), eventID)}
	mutations := []core.ModuleDataMutation{
		{Key: key, ExpectedRevision: currentRecord.Revision, Value: &core.ModuleDataValue{Payload: statePayload, PayloadVersion: 1, IndexData: branchIndexJSON}},
		{Key: eventKey, ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: eventPayload, PayloadVersion: 1, IndexData: eventIndexJSON}},
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		return websiteapi.Branch{}, mapCoreError(err)
	}
	return branchFromAssignment(configured, snapshot.ID, currentRecord.Revision+1, now), nil
}

func (s *Service) ListBranchEvents(ctx context.Context, branchID, cursor string, limit int) (websiteapi.BranchEventPage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.BranchEventPage{}, err
	}
	configured, exists := s.branches[branchID]
	if !exists {
		return websiteapi.BranchEventPage{}, websiteapi.ErrNotFound
	}
	if limit < 1 || limit > s.store.MaxPageSize() {
		return websiteapi.BranchEventPage{}, websiteapi.ErrTooLarge
	}
	after, err := decodeCursor(cursor)
	if err != nil || (after != "" && !strings.HasPrefix(after, "event/"+configured.ID+"/")) {
		return websiteapi.BranchEventPage{}, websiteapi.ErrInvalid
	}
	selectors := []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"branch_event"}},
		{Field: "branch_id", Operator: core.SelectorEqual, Values: []any{configured.ID}},
	}
	keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "site", configured.SiteID, after, selectors, limit)
	if err != nil {
		return websiteapi.BranchEventPage{}, mapCoreError(err)
	}
	page := websiteapi.BranchEventPage{Items: make([]websiteapi.BranchEvent, 0, len(records))}
	for i, record := range records {
		var event websiteapi.BranchEvent
		var index struct {
			RecordType string `json:"record_type"`
			BranchID   string `json:"branch_id"`
			SnapshotID string `json:"snapshot_id"`
			Action     string `json:"action"`
		}
		if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &event) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
			index.RecordType != "branch_event" || index.BranchID != configured.ID || index.SnapshotID != event.SnapshotID || index.Action != event.Action ||
			!snapshotIDPattern.MatchString(event.ID) || event.BranchID != configured.ID || !snapshotIDPattern.MatchString(event.SnapshotID) || event.SnapshotSequence < 1 ||
			event.SnapshotName == "" || event.Action != "assigned" || event.CreatedAt.IsZero() || !strings.HasPrefix(keys[i].RecordKey, "event/"+configured.ID+"/") || !strings.HasSuffix(keys[i].RecordKey, "/"+event.ID) {
			return websiteapi.BranchEventPage{}, websiteapi.ErrUnavailable
		}
		page.Items = append(page.Items, event)
	}
	if next != "" {
		page.NextCursor = encodeCursor(next)
	}
	return page, nil
}

func (s *Service) loadBranchState(ctx context.Context, configured BranchConfig) (branchState, core.ModuleDataRecord, error) {
	record, err := s.store.Get(ctx, branchStateKey(configured))
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			state := branchState{ID: configured.ID}
			if configured.InitialSnapshotID != nil {
				state.SnapshotID = *configured.InitialSnapshotID
			}
			return state, core.ModuleDataRecord{}, nil
		}
		return branchState{}, core.ModuleDataRecord{}, mapCoreError(err)
	}
	var state branchState
	var index struct {
		RecordType string `json:"record_type"`
		BranchID   string `json:"branch_id"`
		SnapshotID string `json:"snapshot_id"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &state) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "branch" || index.BranchID != configured.ID || index.SnapshotID != state.SnapshotID || state.ID != configured.ID || !snapshotIDPattern.MatchString(state.SnapshotID) || state.UpdatedAt.IsZero() {
		return branchState{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
	}
	return state, record, nil
}

func branchStateKey(configured BranchConfig) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "site", ScopeID: configured.SiteID, RecordKey: "branch/" + configured.ID}
}

func branchFromConfig(configured BranchConfig) websiteapi.Branch {
	return websiteapi.Branch{ID: configured.ID, Name: configured.Name, SiteID: configured.SiteID, ListenAddress: configured.ListenAddress,
		CORSOrigins: append([]string(nil), configured.CORSOrigins...), Indexable: configured.Indexable, Enabled: configured.Enabled,
		SnapshotID: cloneString(configured.InitialSnapshotID)}
}

func branchFromAssignment(configured BranchConfig, snapshotID string, revision int64, updatedAt time.Time) websiteapi.Branch {
	branch := branchFromConfig(configured)
	branch.SnapshotID = &snapshotID
	branch.Revision = revision
	branch.UpdatedAt = updatedAt
	return branch
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	var text [36]byte
	hex.Encode(text[0:8], value[0:4])
	text[8] = '-'
	hex.Encode(text[9:13], value[4:6])
	text[13] = '-'
	hex.Encode(text[14:18], value[6:8])
	text[18] = '-'
	hex.Encode(text[19:23], value[8:10])
	text[23] = '-'
	hex.Encode(text[24:36], value[10:16])
	return string(text[:]), nil
}

func encodeCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeCursor(value string) (string, error) {
	if len(value) > 1024 {
		return "", errors.New("cursor too large")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) > 256 || strings.ContainsRune(string(decoded), '\x00') {
		return "", errors.New("invalid cursor")
	}
	return string(decoded), nil
}
