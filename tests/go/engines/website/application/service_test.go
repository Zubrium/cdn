package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

const (
	siteA     = "site-a"
	siteB     = "site-b"
	branchA   = "production"
	snapshotA = "33333333-3333-4333-8333-333333333333"
	snapshotB = "44444444-4444-4444-8444-444444444444"
)

func TestListBranchesCombinesStaticTopologyWithPersistedAssignment(t *testing.T) {
	store := newMemoryStore()
	putRecord(t, store, core.ModuleDataKey{Namespace: "website/snapshots", ScopeType: "site", ScopeID: siteA, RecordKey: "branch/" + branchA}, map[string]any{
		"id": branchA, "snapshot_id": snapshotA,
	}, map[string]any{"record_type": "branch", "branch_id": branchA, "snapshot_id": snapshotA})
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.ListBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("branch count = %d, want 1", len(page.Items))
	}
	got := page.Items[0]
	if got.ID != branchA || got.SiteID != siteA || got.ListenAddress != "127.0.0.1:8081" || got.SnapshotID == nil || *got.SnapshotID != snapshotA || got.Revision != 1 {
		t.Fatalf("branch = %#v", got)
	}
	if !got.Indexable || !got.Enabled || got.Name != "Production" {
		t.Fatalf("static branch policy was not preserved: %#v", got)
	}
}

func TestAssignBranchUsesRevisionCASAndPersistsEventAtomically(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	assigned, err := service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: snapshotA, ExpectedRevision: 0, ExpectedSnapshotRevision: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if assigned.SnapshotID == nil || *assigned.SnapshotID != snapshotA || assigned.Revision != 1 {
		t.Fatalf("assignment = %#v", assigned)
	}

	events, err := service.ListBranchEvents(ctx, branchA, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Items) != 1 {
		t.Fatalf("event count = %d, want 1", len(events.Items))
	}
	event := events.Items[0]
	if event.BranchID != branchA || event.SnapshotID != snapshotA || event.SnapshotName != "Home" || event.SnapshotSequence != 7 || event.Action != "assigned" {
		t.Fatalf("event = %#v", event)
	}
	if event.CreatedAt.IsZero() {
		t.Fatal("assignment event must have a timestamp")
	}
}

func TestAssignBranchRejectsCrossSiteSnapshotWithoutPartialWrites(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteB, snapshotB, 2)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: snapshotB, ExpectedRevision: 0, ExpectedSnapshotRevision: 2,
	})
	if !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("cross-site snapshot assignment error = %v, want not found", err)
	}
	if len(store.records) != 1 {
		t.Fatalf("cross-site rejection left %d records; want only the original snapshot", len(store.records))
	}
}

func TestAssignBranchRejectsStaleSnapshotRevisionWithoutAssignmentOrEvent(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: snapshotA, ExpectedRevision: 0, ExpectedSnapshotRevision: 2,
	})
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale snapshot revision error = %v, want conflict", err)
	}
	if len(store.records) != 1 {
		t.Fatalf("stale assignment left %d records; want only the original snapshot", len(store.records))
	}
}

func TestAssignBranchRequiresWriteLevelBeforeLookingUpTarget(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: "missing", ExpectedRevision: 0, ExpectedSnapshotRevision: 0,
	})
	if !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("write without level 2 error = %v, want forbidden", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("denied assignment wrote %d records", len(store.records))
	}
}

func TestListSnapshotsPaginatesDeterministicallyAcrossConfiguredSites(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 2)
	putSnapshot(t, store, siteA, "55555555-5555-4555-8555-555555555555", 1)
	putSnapshot(t, store, siteB, snapshotB, 4)
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	cursor := ""
	for pages := 0; pages < 4; pages++ {
		page, err := service.ListSnapshots(ctx, "", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 {
			break
		}
		got = append(got, page.Items[0].ID)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	want := []string{"33333333-3333-4333-8333-333333333333", "55555555-5555-4555-8555-555555555555", "44444444-4444-4444-8444-444444444444"}
	if len(got) != len(want) {
		t.Fatalf("snapshot IDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("snapshot IDs = %v, want %v", got, want)
		}
	}
}

func TestGetSnapshotPreservesSnapshotScopedDataModel(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.GetSnapshot(ctx, snapshotA)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 3 || snapshot.Payload == nil {
		t.Fatalf("snapshot revision or payload missing: %#v", snapshot)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(snapshot.Payload.Config, &config); err != nil {
		t.Fatal(err)
	}
	if string(config["dataModel"]) != `[{"id":"product","cardinality":"many"}]` {
		t.Fatalf("snapshot config lost its dataModel: %s", snapshot.Payload.Config)
	}
}

func TestSnapshotReadRequiresReadAccessBeforeLookingUpTarget(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelNone, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.GetSnapshot(ctx, snapshotA)
	if !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("snapshot read without level 1 error = %v, want forbidden", err)
	}
}

func TestSnapshotListRejectsUnknownSiteAndMalformedCursor(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListSnapshots(ctx, "unknown", "", 10); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("unknown site error = %v, want not found", err)
	}
	if _, err := service.ListSnapshots(ctx, siteA, "not-a-cursor", 10); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("malformed cursor error = %v, want invalid", err)
	}
}

func newService(t *testing.T, store *memoryStore, level core.AccessLevel, config websiteapp.Config) *websiteapp.Service {
	t.Helper()
	authority := &fakeAuthority{level: level}
	service, err := websiteapp.NewService(config, store, authority, fakeSessions{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func oneBranchConfig() websiteapp.Config {
	return websiteapp.Config{
		AccessGroup: "features",
		Sites:       []string{siteA, siteB},
		Branches: []websiteapp.BranchConfig{{
			ID: branchA, Name: "Production", SiteID: siteA, ListenAddress: "127.0.0.1:8081",
			CORSOrigins: []string{"https://www.example.test"}, Indexable: true, Enabled: true,
		}},
	}
}

func putSnapshot(t *testing.T, store *memoryStore, siteID, id string, revision int64) {
	t.Helper()
	snapshot := websiteapi.Snapshot{
		ID: id, SiteID: siteID, Name: "Home", Sequence: 7, FeaturePins: map[string]string{}, State: "ready",
		Payload: &websiteapi.SnapshotPayload{Config: json.RawMessage(`{"dataModel":[{"id":"product","cardinality":"many"}]}`), Metadata: json.RawMessage(`{}`), DesignSystem: json.RawMessage(`{}`)},
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	key := core.ModuleDataKey{Namespace: "website/snapshots", ScopeType: "site", ScopeID: siteID, RecordKey: "snapshot/" + id}
	store.records[key] = core.ModuleDataRecord{Payload: payload, PayloadVersion: 1, IndexData: mustJSON(map[string]any{"record_type": "snapshot", "state": "ready", "revision": float64(revision)}), Revision: revision, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

func putRecord(t *testing.T, store *memoryStore, key core.ModuleDataKey, payload, index map[string]any) {
	t.Helper()
	store.records[key] = core.ModuleDataRecord{Payload: mustJSON(payload), PayloadVersion: 1, IndexData: mustJSON(index), Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

type fakeAuthority struct{ level core.AccessLevel }

func (a *fakeAuthority) Authenticate(_ context.Context, credential string) (core.Session, core.Principal, error) {
	if credential != "credential" {
		return core.Session{}, core.Principal{}, core.ErrUnauthorized
	}
	return core.Session{}, core.Principal{ID: "principal-1", Active: true}, nil
}

func (a *fakeAuthority) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, minimum core.AccessLevel) error {
	if len(groups) != 1 || groups[0] != "features" {
		return errors.New("unexpected access group")
	}
	if a.level < minimum {
		return core.ErrForbidden
	}
	return nil
}

type fakeSessions struct{}

func (fakeSessions) AuthenticateSession(_ context.Context, token string) (core.Session, core.Principal, error) {
	if token != "session" {
		return core.Session{}, core.Principal{}, core.ErrUnauthorized
	}
	return core.Session{}, core.Principal{ID: "principal-1", Active: true}, nil
}

type memoryStore struct {
	records     map[core.ModuleDataKey]core.ModuleDataRecord
	maxPageSize int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{records: map[core.ModuleDataKey]core.ModuleDataRecord{}, maxPageSize: 100}
}

func (s *memoryStore) MaxPageSize() int { return s.maxPageSize }

func (s *memoryStore) Get(_ context.Context, key core.ModuleDataKey) (core.ModuleDataRecord, error) {
	record, ok := s.records[key]
	if !ok {
		return core.ModuleDataRecord{}, core.ErrModuleDataNotFound
	}
	return record, nil
}

func (s *memoryStore) Batch(_ context.Context, mutations []core.ModuleDataMutation) error {
	if len(mutations) == 0 {
		return core.ErrModuleDataInvalid
	}
	first := mutations[0].Key
	seen := map[core.ModuleDataKey]struct{}{}
	for _, mutation := range mutations {
		if mutation.Key.Namespace != first.Namespace || mutation.Key.ScopeType != first.ScopeType || mutation.Key.ScopeID != first.ScopeID {
			return core.ErrModuleDataInvalid
		}
		if _, exists := seen[mutation.Key]; exists {
			return core.ErrModuleDataInvalid
		}
		seen[mutation.Key] = struct{}{}
		current := s.records[mutation.Key]
		if current.Revision != mutation.ExpectedRevision {
			return core.ErrModuleDataConflict
		}
		if mutation.Value == nil && mutation.ExpectedRevision == 0 {
			return core.ErrModuleDataInvalid
		}
	}
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(s.records, mutation.Key)
			continue
		}
		current := s.records[mutation.Key]
		s.records[mutation.Key] = core.ModuleDataRecord{
			Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion,
			IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: current.Revision + 1,
			CreatedAt: current.CreatedAt, UpdatedAt: time.Now().UTC(),
		}
		if current.Revision == 0 {
			s.records[mutation.Key] = core.ModuleDataRecord{
				Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion,
				IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: 1,
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}
		}
	}
	return nil
}

func (s *memoryStore) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []core.ModuleDataSelector, limit int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error) {
	if limit > s.maxPageSize || limit < 1 {
		return nil, nil, "", core.ErrModuleDataLimitExceeded
	}
	keys := make([]core.ModuleDataKey, 0)
	for key, record := range s.records {
		if key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after || !matchesSelectors(record.IndexData, selectors) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	if len(keys) <= limit {
		records := make([]core.ModuleDataRecord, len(keys))
		for i, key := range keys {
			records[i] = s.records[key]
		}
		return keys, records, "", nil
	}
	pageKeys := keys[:limit]
	records := make([]core.ModuleDataRecord, len(pageKeys))
	for i, key := range pageKeys {
		records[i] = s.records[key]
	}
	return pageKeys, records, pageKeys[len(pageKeys)-1].RecordKey, nil
}

func matchesSelectors(raw json.RawMessage, selectors []core.ModuleDataSelector) bool {
	var index map[string]any
	if json.Unmarshal(raw, &index) != nil {
		return false
	}
	for _, selector := range selectors {
		actual, ok := index[selector.Field]
		if !ok || selector.Operator != core.SelectorEqual || len(selector.Values) != 1 || actual != selector.Values[0] {
			return false
		}
	}
	return true
}
