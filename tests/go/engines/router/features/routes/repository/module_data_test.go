package repository

import (
	"context"
	"encoding/json"
	"errors"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"sort"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/router/features/routes/model"
)

type memoryModuleData struct {
	values map[recorddata.RecordKey]recorddata.Record
}

func newMemoryModuleData() *memoryModuleData {
	return &memoryModuleData{values: make(map[recorddata.RecordKey]recorddata.Record)}
}

func (store *memoryModuleData) Get(_ context.Context, key recorddata.RecordKey) (recorddata.Record, error) {
	value, ok := store.values[key]
	if !ok {
		return recorddata.Record{}, recorddata.ErrRecordNotFound
	}
	return value, nil
}

func (store *memoryModuleData) Put(_ context.Context, key recorddata.RecordKey, value recorddata.RecordValue, expected int64) (recorddata.Record, error) {
	current, ok := store.values[key]
	if (!ok && expected != 0) || (ok && current.Revision != expected) {
		return recorddata.Record{}, recorddata.ErrRecordConflict
	}
	revision := expected + 1
	now := time.Now().UTC()
	record := recorddata.Record{Payload: append(json.RawMessage(nil), value.Payload...), IndexData: append(json.RawMessage(nil), value.IndexData...),
		PayloadVersion: value.PayloadVersion, Revision: revision, CreatedAt: now, UpdatedAt: now}
	if ok {
		record.CreatedAt = current.CreatedAt
	}
	store.values[key] = record
	return record, nil
}

func (store *memoryModuleData) Delete(_ context.Context, key recorddata.RecordKey, expected int64) error {
	current, ok := store.values[key]
	if !ok {
		return recorddata.ErrRecordNotFound
	}
	if current.Revision != expected {
		return recorddata.ErrRecordConflict
	}
	delete(store.values, key)
	return nil
}

func (store *memoryModuleData) Batch(_ context.Context, mutations []recorddata.RecordMutation) error {
	for _, mutation := range mutations {
		current, ok := store.values[mutation.Key]
		if (!ok && mutation.ExpectedRevision != 0) || (ok && current.Revision != mutation.ExpectedRevision) {
			return recorddata.ErrRecordConflict
		}
	}
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(store.values, mutation.Key)
			continue
		}
		if _, err := store.Put(context.Background(), mutation.Key, *mutation.Value, mutation.ExpectedRevision); err != nil {
			return err
		}
	}
	return nil
}

func (store *memoryModuleData) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []recorddata.RecordSelector, limit int) ([]recorddata.RecordKey, []recorddata.Record, string, error) {
	keys := make([]recorddata.RecordKey, 0)
	for key, value := range store.values {
		if key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after || !matchesIndex(value.IndexData, selectors) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	var next string
	if len(keys) > limit {
		next = keys[limit-1].RecordKey
		keys = keys[:limit]
	}
	records := make([]recorddata.Record, 0, len(keys))
	for _, key := range keys {
		records = append(records, store.values[key])
	}
	return keys, records, next, nil
}

func (*memoryModuleData) MaxPageSize() int { return 100 }

func matchesIndex(raw json.RawMessage, selectors []recorddata.RecordSelector) bool {
	var index map[string]any
	if json.Unmarshal(raw, &index) != nil {
		return false
	}
	for _, selector := range selectors {
		value, exists := index[selector.Field]
		if !exists {
			return false
		}
		switch selector.Operator {
		case recorddata.RecordSelectorEqual:
			if len(selector.Values) != 1 || value != selector.Values[0] {
				return false
			}
		case recorddata.RecordSelectorIn:
			matched := false
			for _, candidate := range selector.Values {
				if value == candidate {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func testRoute(t *testing.T, id, host, path string) model.Route {
	return testRouteWithMatch(t, id, host, path, model.MatchExact)
}

func testRouteWithMatch(t *testing.T, id, host, path, match string) model.Route {
	t.Helper()
	target := model.Target{Kind: model.TargetCoreFile, FileID: "01938f7a-68af-7c10-a51d-78b6ca125301"}
	if match == model.MatchPrefix {
		var err error
		target, err = model.NewStaticArtifactTarget("en-US", []model.LocaleArtifactTarget{
			{Locale: "en-US", ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125301", Revision: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	route, err := model.NewRoute(id, "main", host, path, match, target, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func TestFindMatchChoosesExactThenLongestSegmentPrefix(t *testing.T) {
	ctx := context.Background()
	repo, err := NewModuleDataRepository(newMemoryModuleData())
	if err != nil {
		t.Fatal(err)
	}
	routes := []model.Route{
		testRouteWithMatch(t, "01938f7a-68af-7c10-a51d-78b6ca125310", "example.com", "/assets", model.MatchPrefix),
		testRouteWithMatch(t, "01938f7a-68af-7c10-a51d-78b6ca125311", "example.com", "/assets/icons/", model.MatchPrefix),
		testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125312", "example.com", "/assets/icons/logo.svg"),
	}
	for index, route := range routes {
		if _, err := repo.Create(ctx, route, "router-route-find-00"+string(rune('1'+index)), "fingerprint-"+string(rune('1'+index))); err != nil {
			t.Fatal(err)
		}
	}
	exact, err := repo.FindMatch(ctx, "main", "example.com", "/assets/icons/logo.svg")
	if err != nil || exact.ID != routes[2].ID {
		t.Fatalf("exact route = %+v, %v; want %q", exact, err, routes[2].ID)
	}
	longestPrefix, err := repo.FindMatch(ctx, "main", "example.com", "/assets/icons/other.svg")
	if err != nil || longestPrefix.ID != routes[1].ID {
		t.Fatalf("longest prefix route = %+v, %v; want %q", longestPrefix, err, routes[1].ID)
	}
	shorterPrefix, err := repo.FindMatch(ctx, "main", "example.com", "/assets/other.svg")
	if err != nil || shorterPrefix.ID != routes[0].ID {
		t.Fatalf("shorter prefix route = %+v, %v; want %q", shorterPrefix, err, routes[0].ID)
	}
	if _, err := repo.FindMatch(ctx, "main", "example.com", "/assets-old/other.svg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial segment path = %v, want not found", err)
	}
}

func TestFindMatchSkipsSiblingsThatArePrefixesOfTheRequest(t *testing.T) {
	ctx := context.Background()
	repo, err := NewModuleDataRepository(newMemoryModuleData())
	if err != nil {
		t.Fatal(err)
	}
	session := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125320", "forms.example.test", "/forms/contact")
	submit := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125321", "forms.example.test", "/forms/contact/submit")
	for index, route := range []model.Route{session, submit} {
		if _, err := repo.Create(ctx, route, "router-route-form-00"+string(rune('1'+index)), "fingerprint-form-"+string(rune('1'+index))); err != nil {
			t.Fatal(err)
		}
	}
	matched, err := repo.FindMatch(ctx, "main", "forms.example.test", "/forms/contact/submit")
	if err != nil || matched.ID != submit.ID {
		t.Fatalf("submit route = %+v, %v; want %q", matched, err, submit.ID)
	}
}

func TestCreateIsAtomicAndIdempotentButRejectsKeyReuse(t *testing.T) {
	ctx := context.Background()
	data := newMemoryModuleData()
	repo, err := NewModuleDataRepository(data)
	if err != nil {
		t.Fatal(err)
	}
	route := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125302", "example.com", "/a")
	created, err := repo.Create(ctx, route, "route-create-idem-0001", "fingerprint-a")
	if err != nil || created.ID != route.ID {
		t.Fatalf("Create() = %+v, %v", created, err)
	}
	replay := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125303", "example.com", "/a")
	replayed, err := repo.Create(ctx, replay, "route-create-idem-0001", "fingerprint-a")
	if err != nil || replayed.ID != route.ID {
		t.Fatalf("idempotent replay = %+v, %v", replayed, err)
	}
	if _, err := repo.Create(ctx, replay, "route-create-idem-0001", "different-payload"); !errors.Is(err, ErrConflict) {
		t.Fatalf("same idempotency key with a different request = %v, want conflict", err)
	}
	other := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125304", "example.com", "/a")
	if _, err := repo.Create(ctx, other, "route-create-idem-0002", "fingerprint-a"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate host/path = %v, want conflict", err)
	}
	if got := len(data.values); got != 2 {
		t.Fatalf("expected exactly route and idempotency records after conflict, got %d", got)
	}
}

func TestRouteListFiltersHostAndCursorAndCASProtectsUpdates(t *testing.T) {
	ctx := context.Background()
	repo, err := NewModuleDataRepository(newMemoryModuleData())
	if err != nil {
		t.Fatal(err)
	}
	first := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125302", "a.example", "/a")
	second := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125303", "a.example", "/b")
	third := testRoute(t, "01938f7a-68af-7c10-a51d-78b6ca125304", "b.example", "/c")
	for i, route := range []model.Route{first, second, third} {
		if _, err := repo.Create(ctx, route, "route-create-idem-00"+string(rune('1'+i)), "fingerprint-"+string(rune('1'+i))); err != nil {
			t.Fatal(err)
		}
	}
	items, cursor, err := repo.List(ctx, "main", "a.example", "", 1)
	if err != nil || len(items) != 1 || cursor == "" {
		t.Fatalf("first filtered page = %d items, cursor=%q, err=%v", len(items), cursor, err)
	}
	page, next, err := repo.List(ctx, "main", "a.example", cursor, 1)
	if err != nil || len(page) != 1 || next != "" || page[0].ID == items[0].ID {
		t.Fatalf("second filtered page = %+v cursor=%q err=%v", page, next, err)
	}
	updated, err := repo.ReplaceTarget(ctx, "main", first.ID, model.Target{Kind: model.TargetCoreFile, FileID: "01938f7a-68af-7c10-a51d-78b6ca125399"}, 1, time.Date(2026, 11, 18, 12, 1, 0, 0, time.UTC))
	if err != nil || updated.Revision != 2 {
		t.Fatalf("target replacement = %+v, %v", updated, err)
	}
	if _, err := repo.ReplaceTarget(ctx, "main", first.ID, first.Target, 1, time.Now().UTC()); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale target replacement = %v, want conflict", err)
	}
	if err := repo.Delete(ctx, "main", first.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete = %v, want conflict", err)
	}
	if err := repo.Delete(ctx, "main", first.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(ctx, "main", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted route lookup = %v, want not found", err)
	}
}
