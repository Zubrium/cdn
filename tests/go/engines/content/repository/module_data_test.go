package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/content/domain"
	"github.com/Zubrium/cdn/engines/content/repository"
)

func TestCommitStoresPublicationWithWorkspaceRevisionAtomically(t *testing.T) {
	store := newMemoryStore()
	repo, err := repository.New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	initial := domain.Aggregate{SnapshotID: "11111111-1111-4111-8111-111111111111", Revision: 0, PublishedRevision: 0, Records: []domain.Record{}, Selections: []domain.Selection{}}
	created, err := repo.Create(ctx, initial.SnapshotID, initial)
	if err != nil || created.StoreRevision != 1 {
		t.Fatalf("Create() = revision %d, err %v", created.StoreRevision, err)
	}

	next := initial
	next.Revision = 1
	next.PublishedRevision = 1
	publication := domain.Publication{
		ID: "22222222-2222-4222-8222-222222222222", DraftRevision: 0, ContentRevision: 1,
		CreatedAt:  time.Date(2026, time.November, 18, 10, 0, 0, 0, time.UTC),
		Records:    []domain.Record{{ID: "33333333-3333-4333-8333-333333333333", EntityID: "44444444-4444-4444-8444-444444444444", Values: map[string]any{"title": "Published"}}},
		Selections: []domain.Selection{},
	}
	committed, err := repo.Commit(ctx, initial.SnapshotID, created.StoreRevision, next, &publication)
	if err != nil || committed.StoreRevision != 2 {
		t.Fatalf("Commit() = revision %d, err %v", committed.StoreRevision, err)
	}
	history, err := repo.GetHistoryByID(ctx, initial.SnapshotID, publication.ID)
	if err != nil || history.ContentRevision != 1 || history.Records[0].Values["title"] != "Published" {
		t.Fatalf("GetHistoryByID() = %#v, err %v", history, err)
	}

	stale := next
	stale.Revision++
	if _, err := repo.Commit(ctx, initial.SnapshotID, created.StoreRevision, stale, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale Commit() error = %v, want ErrConflict", err)
	}
	current, err := repo.Get(ctx, initial.SnapshotID)
	if err != nil || current.Aggregate.Revision != 1 || current.Aggregate.PublishedRevision != 1 {
		t.Fatalf("stale commit changed workspace: %#v, err %v", current, err)
	}
}

func TestHistoryListUsesBoundedCursorAndReturnsImmutableRecords(t *testing.T) {
	store := newMemoryStore()
	repo, err := repository.New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	snapshotID := "11111111-1111-4111-8111-111111111111"
	created, err := repo.Create(ctx, snapshotID, domain.Aggregate{SnapshotID: snapshotID, Records: []domain.Record{}, Selections: []domain.Selection{}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for revision, id := range []string{"22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"} {
		publication := domain.Publication{ID: id, DraftRevision: int64(revision + 1), ContentRevision: int64(revision + 1), CreatedAt: time.Date(2026, time.November, 18, 10, revision, 0, 0, time.UTC), Records: []domain.Record{}, Selections: []domain.Selection{}}
		next := created.Aggregate
		next.Revision = int64(revision + 1)
		next.PublishedRevision = int64(revision + 1)
		created, err = repo.Commit(ctx, snapshotID, created.StoreRevision, next, &publication)
		if err != nil {
			t.Fatalf("Commit(%d) error = %v", revision, err)
		}
	}
	first, next, err := repo.ListHistory(ctx, snapshotID, "", 2)
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("first ListHistory() = %d items, cursor %q, err %v", len(first), next, err)
	}
	second, terminal, err := repo.ListHistory(ctx, snapshotID, next, 2)
	if err != nil || len(second) != 1 || terminal != "" {
		t.Fatalf("second ListHistory() = %d items, cursor %q, err %v", len(second), terminal, err)
	}
	for _, item := range append(first, second...) {
		loaded, err := repo.GetHistoryByContentRevision(ctx, snapshotID, item.ContentRevision)
		if err != nil || loaded.ID != item.ID {
			t.Fatalf("GetHistoryByContentRevision(%d) = %#v, err %v", item.ContentRevision, loaded, err)
		}
	}
}

type memoryStore struct {
	rows map[core.ModuleDataKey]core.ModuleDataRecord
}

func newMemoryStore() *memoryStore {
	return &memoryStore{rows: map[core.ModuleDataKey]core.ModuleDataRecord{}}
}
func (store *memoryStore) Get(_ context.Context, key core.ModuleDataKey) (core.ModuleDataRecord, error) {
	row, exists := store.rows[key]
	if !exists {
		return core.ModuleDataRecord{}, core.ErrModuleDataNotFound
	}
	return cloneRow(row), nil
}
func (store *memoryStore) Put(_ context.Context, key core.ModuleDataKey, value core.ModuleDataValue, expected int64) (core.ModuleDataRecord, error) {
	current, exists := store.rows[key]
	if (!exists && expected != 0) || (exists && current.Revision != expected) {
		return core.ModuleDataRecord{}, core.ErrModuleDataConflict
	}
	row := core.ModuleDataRecord{Payload: append(json.RawMessage(nil), value.Payload...), PayloadVersion: value.PayloadVersion, IndexData: append(json.RawMessage(nil), value.IndexData...), Revision: expected + 1}
	store.rows[key] = row
	return cloneRow(row), nil
}
func (store *memoryStore) Delete(_ context.Context, key core.ModuleDataKey, expected int64) error {
	row, exists := store.rows[key]
	if !exists || row.Revision != expected {
		return core.ErrModuleDataConflict
	}
	delete(store.rows, key)
	return nil
}
func (store *memoryStore) Batch(_ context.Context, mutations []core.ModuleDataMutation) error {
	for _, mutation := range mutations {
		current, exists := store.rows[mutation.Key]
		if (!exists && mutation.ExpectedRevision != 0) || (exists && current.Revision != mutation.ExpectedRevision) {
			return core.ErrModuleDataConflict
		}
	}
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(store.rows, mutation.Key)
			continue
		}
		store.rows[mutation.Key] = core.ModuleDataRecord{Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion, IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: mutation.ExpectedRevision + 1}
	}
	return nil
}
func (store *memoryStore) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []core.ModuleDataSelector, limit int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error) {
	keys := make([]core.ModuleDataKey, 0)
	for key, row := range store.rows {
		if key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after || !matches(row.IndexData, selectors) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	rows := make([]core.ModuleDataRecord, len(keys))
	for index, key := range keys {
		rows[index] = cloneRow(store.rows[key])
	}
	if more {
		return keys, rows, keys[len(keys)-1].RecordKey, nil
	}
	return keys, rows, "", nil
}
func (store *memoryStore) MaxPageSize() int     { return 100 }
func (store *memoryStore) MaxBatchSize() int    { return 256 }
func (store *memoryStore) MaxPayloadBytes() int { return 1 << 20 }
func (store *memoryStore) MaxIndexBytes() int   { return 1 << 16 }
func (store *memoryStore) MaxBatchBytes() int   { return 1 << 22 }

func matches(raw json.RawMessage, selectors []core.ModuleDataSelector) bool {
	var index map[string]any
	if err := json.Unmarshal(raw, &index); err != nil {
		return false
	}
	for _, selector := range selectors {
		if selector.Operator != core.SelectorEqual || len(selector.Values) != 1 {
			return false
		}
		actual, _ := json.Marshal(index[selector.Field])
		expected, _ := json.Marshal(selector.Values[0])
		if string(actual) != string(expected) {
			return false
		}
	}
	return true
}

func cloneRow(row core.ModuleDataRecord) core.ModuleDataRecord {
	row.Payload = append(json.RawMessage(nil), row.Payload...)
	row.IndexData = append(json.RawMessage(nil), row.IndexData...)
	return row
}
