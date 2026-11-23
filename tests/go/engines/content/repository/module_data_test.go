package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"reflect"
	"sort"
	"testing"
	"time"

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
	initial := domain.StoredWorkspace{Aggregate: domain.Aggregate{SnapshotID: domain.GlobalScopeID, Locale: "en-US", Revision: 0, PublishedRevision: 0, Records: []domain.Record{}}}
	created, err := repo.Create(ctx, initial)
	if err != nil || created.StoreRevision != 1 {
		t.Fatalf("Create() = revision %d, err %v", created.StoreRevision, err)
	}

	next := cloneRepositoryStored(created)
	next.Aggregate.Revision = 1
	next.Aggregate.PublishedRevision = 1
	publication := domain.Publication{
		ID: "22222222-2222-4222-8222-222222222222", Locale: "en-US", DraftRevision: 0, ContentRevision: 1,
		CreatedAt:  time.Date(2026, time.November, 18, 10, 0, 0, 0, time.UTC),
		Records:    []domain.Record{{ID: "33333333-3333-4333-8333-333333333333", Locale: "en-US", EntityID: "44444444-4444-4444-8444-444444444444", Values: map[string]any{"title": "Published"}}},
		Selections: []domain.Selection{},
	}
	committed, err := repo.Commit(ctx, created.StoreRevision, next, &publication)
	if err != nil || committed.StoreRevision != 2 {
		t.Fatalf("Commit() = revision %d, err %v", committed.StoreRevision, err)
	}
	history, err := repo.GetHistoryByID(ctx, "en-US", publication.ID)
	if err != nil || history.ContentRevision != 1 || history.Records[0].Values["title"] != "Published" {
		t.Fatalf("GetHistoryByID() = %#v, err %v", history, err)
	}

	stale := cloneRepositoryStored(committed)
	stale.Aggregate.Revision++
	if _, err := repo.Commit(ctx, created.StoreRevision, stale, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale Commit() error = %v, want ErrConflict", err)
	}
	current, err := repo.Get(ctx, "en-US")
	if err != nil || current.Aggregate.Revision != 1 || current.Aggregate.PublishedRevision != 1 {
		t.Fatalf("stale commit changed workspace: %#v, err %v", current, err)
	}
}

func TestGlobalWorkspaceAndPageSelectionsUseDistinctCoreScopes(t *testing.T) {
	store := newMemoryStore()
	repo, err := repository.New(store)
	if err != nil {
		t.Fatal(err)
	}
	pageID := "22222222-2222-4222-8222-222222222222"
	_, err = repo.Create(context.Background(), domain.StoredWorkspace{
		Aggregate: domain.Aggregate{SnapshotID: domain.GlobalScopeID, Locale: "en-US", Records: []domain.Record{}},
		PageSelections: map[string]domain.StoredPageSelections{
			pageID: {Draft: []domain.Selection{}, Published: []domain.Selection{}},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	globalKey := recorddata.RecordKey{Namespace: "content/workspaces", ScopeType: "global", ScopeID: domain.GlobalScopeID, RecordKey: "workspace/en-US"}
	pageKey := recorddata.RecordKey{Namespace: "content/workspaces", ScopeType: "page_snapshot", ScopeID: pageID, RecordKey: "page-selections/en-US"}
	if _, ok := store.rows[globalKey]; !ok {
		t.Fatal("missing globally scoped workspace record")
	}
	if _, ok := store.rows[pageKey]; !ok {
		t.Fatal("missing page-snapshot-scoped selections record")
	}
	stored, err := repo.Get(context.Background(), "en-US")
	pageState, exists := stored.PageSelections[pageID]
	if err != nil || !exists || pageState.Draft == nil || pageState.Published == nil || pageState.StoreRevision != 1 {
		t.Fatalf("Get() did not hydrate page-scoped selections: %#v, err %v", stored, err)
	}
}

func TestRepositoryReturnsOpaqueStoreRevisionsFromBatchWrites(t *testing.T) {
	store := &sequenceRevisionStore{memoryStore: newMemoryStore(), sequence: 30}
	repo, err := repository.New(store)
	if err != nil {
		t.Fatal(err)
	}
	pageID := "22222222-2222-4222-8222-222222222222"
	created, err := repo.Create(context.Background(), domain.StoredWorkspace{
		Aggregate: domain.Aggregate{SnapshotID: domain.GlobalScopeID, Locale: "en-US", Records: []domain.Record{}},
		PageSelections: map[string]domain.StoredPageSelections{
			pageID: {Draft: []domain.Selection{}, Published: []domain.Selection{}},
		},
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if created.StoreRevision != 31 || created.PageSelections[pageID].StoreRevision != 32 {
		t.Fatalf("Create() revisions = workspace %d, selection %d; want Core-issued 31 and 32", created.StoreRevision, created.PageSelections[pageID].StoreRevision)
	}
	next := cloneRepositoryStored(created)
	next.Aggregate.Revision = 1
	committed, err := repo.Commit(context.Background(), created.StoreRevision, next, nil)
	if err != nil || committed.StoreRevision != 34 || committed.PageSelections[pageID].StoreRevision != 35 {
		t.Fatalf("Commit() revisions = workspace %d, selection %d, err %v; want Core-issued 34 and 35", committed.StoreRevision, committed.PageSelections[pageID].StoreRevision, err)
	}
	updated, err := repo.CommitPageSelections(context.Background(), pageID, "en-US", 35, []domain.Selection{}, []domain.Selection{}, nil)
	if err != nil || updated.StoreRevision != 36 {
		t.Fatalf("CommitPageSelections() revision = %d, err %v; want Core-issued revision 36", updated.StoreRevision, err)
	}
}

func TestIndependentPageSelectionCASDoesNotAdvanceGlobalWorkspaceRevision(t *testing.T) {
	store := newMemoryStore()
	repo, err := repository.New(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pageA := "22222222-2222-4222-8222-222222222222"
	pageB := "33333333-3333-4333-8333-333333333333"
	created, err := repo.Create(ctx, domain.StoredWorkspace{Aggregate: domain.Aggregate{SnapshotID: domain.GlobalScopeID, Locale: "en-US", Records: []domain.Record{}}})
	if err != nil {
		t.Fatal(err)
	}
	selectionA := []domain.Selection{{PlacementID: "44444444-4444-4444-8444-444444444444", InputKey: "items", Mode: "all"}}
	if _, err := repo.CommitPageSelections(ctx, pageA, "en-US", 0, selectionA, []domain.Selection{}, nil); err != nil {
		t.Fatalf("write page A: %v", err)
	}
	selectionB := []domain.Selection{{PlacementID: "55555555-5555-4555-8555-555555555555", InputKey: "items", Mode: "all"}}
	if _, err := repo.CommitPageSelections(ctx, pageB, "en-US", 0, selectionB, []domain.Selection{}, nil); err != nil {
		t.Fatalf("unrelated page B write conflicted: %v", err)
	}
	latest, err := repo.Get(ctx, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if latest.StoreRevision != created.StoreRevision {
		t.Fatalf("page selection writes advanced global workspace revision: %d -> %d", created.StoreRevision, latest.StoreRevision)
	}
	if latest.PageSelections[pageA].StoreRevision != 1 || latest.PageSelections[pageB].StoreRevision != 1 {
		t.Fatalf("page-scoped revisions = %#v, want both 1", latest.PageSelections)
	}
	if _, err := repo.CommitPageSelections(ctx, pageA, "en-US", 0, []domain.Selection{}, []domain.Selection{}, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale same-page write error = %v, want conflict", err)
	}
}

func TestCommitUpdatesOnlyChangedPageSelectionRecords(t *testing.T) {
	store := newMemoryStore()
	repo, err := repository.New(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pageA := "22222222-2222-4222-8222-222222222222"
	pageB := "33333333-3333-4333-8333-333333333333"
	created, err := repo.Create(ctx, domain.StoredWorkspace{
		Aggregate: domain.Aggregate{SnapshotID: domain.GlobalScopeID, Locale: "en-US", Records: []domain.Record{}},
		PageSelections: map[string]domain.StoredPageSelections{
			pageA: {Draft: []domain.Selection{}, Published: []domain.Selection{}},
			pageB: {Draft: []domain.Selection{}, Published: []domain.Selection{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := cloneRepositoryStored(created)
	changed.Aggregate.Revision++
	changed.ChangedPageSelectionIDs = []string{pageA}
	changedDraft := []domain.Selection{{PlacementID: "44444444-4444-4444-8444-444444444444", InputKey: "items", Mode: "all"}}
	changed.PageSelections[pageA] = domain.StoredPageSelections{Draft: changedDraft, Published: []domain.Selection{}, StoreRevision: 1}

	committed, err := repo.Commit(ctx, created.StoreRevision, changed, nil)
	if err != nil || committed.StoreRevision != created.StoreRevision+1 {
		t.Fatalf("Commit() = %#v, err %v", committed, err)
	}
	if committed.PageSelections[pageA].StoreRevision != 2 || !reflect.DeepEqual(committed.PageSelections[pageA].Draft, changedDraft) {
		t.Fatalf("changed page not advanced: %#v", committed.PageSelections[pageA])
	}
	if committed.PageSelections[pageB].StoreRevision != 1 {
		t.Fatalf("unchanged page revision = %d, want 1", committed.PageSelections[pageB].StoreRevision)
	}
	pageRecord, err := store.Get(ctx, recorddata.RecordKey{Namespace: "content/workspaces", ScopeType: "page_snapshot", ScopeID: pageB, RecordKey: "page-selections/en-US"})
	if err != nil || pageRecord.Revision != 1 {
		t.Fatalf("unchanged page record = %#v, err %v", pageRecord, err)
	}
	stale := cloneRepositoryStored(created)
	stale.Aggregate.Revision++
	stale.ChangedPageSelectionIDs = []string{pageA}
	stale.PageSelections[pageA] = domain.StoredPageSelections{Draft: changedDraft, Published: []domain.Selection{}, StoreRevision: 1}
	if _, err := repo.Commit(ctx, created.StoreRevision, stale, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale page CAS error = %v, want conflict", err)
	}
}

func TestReplaceLocaleConfigCanonicalizesAndGuardsCatalog(t *testing.T) {
	store := newMemoryStore()
	repo, err := repository.New(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := repo.GetLocaleConfig(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetLocaleConfig() before initialization error = %v, want not found", err)
	}
	request := domain.ReplaceLocaleConfigRequest{ExpectedRevision: 0, Locales: []string{"EN-us", "fr"}, DefaultLocale: "en-us"}
	config, err := repo.ReplaceLocaleConfig(ctx, request, "key-one", "fingerprint-one")
	if err != nil || config.Revision != 1 || !reflect.DeepEqual(config.Locales, []string{"en-US", "fr"}) || config.DefaultLocale != "en-US" {
		t.Fatalf("ReplaceLocaleConfig() = %#v, err %v", config, err)
	}
	replayed, err := repo.ReplaceLocaleConfig(ctx, request, "key-one", "fingerprint-one")
	if err != nil || !reflect.DeepEqual(replayed, config) {
		t.Fatalf("idempotent ReplaceLocaleConfig() = %#v, err %v", replayed, err)
	}
	if _, err := repo.ReplaceLocaleConfig(ctx, request, "key-one", "fingerprint-two"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same key with changed payload error = %v, want conflict", err)
	}
	duplicate := domain.ReplaceLocaleConfigRequest{ExpectedRevision: 1, Locales: []string{"en-US", "EN-us"}, DefaultLocale: "en"}
	if _, err := repo.ReplaceLocaleConfig(ctx, duplicate, "key-two", "fingerprint-two"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("duplicate canonical locale error = %v, want invalid", err)
	}
	shrink := domain.ReplaceLocaleConfigRequest{ExpectedRevision: 1, Locales: []string{"en-US"}, DefaultLocale: "en-US"}
	if _, err := repo.ReplaceLocaleConfig(ctx, shrink, "key-three", "fingerprint-three"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("shrinking catalog error = %v, want conflict", err)
	}
	loaded, err := repo.GetLocaleConfig(ctx)
	if err != nil || !reflect.DeepEqual(loaded, config) {
		t.Fatalf("GetLocaleConfig() = %#v, err %v", loaded, err)
	}
}

func TestHistoryListUsesBoundedCursorAndReturnsImmutableRecords(t *testing.T) {
	store := newMemoryStore()
	repo, err := repository.New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	created, err := repo.Create(ctx, domain.StoredWorkspace{Aggregate: domain.Aggregate{SnapshotID: domain.GlobalScopeID, Locale: "en-US", Records: []domain.Record{}}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for revision, id := range []string{"22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"} {
		publication := domain.Publication{ID: id, Locale: "en-US", DraftRevision: int64(revision + 1), ContentRevision: int64(revision + 1), CreatedAt: time.Date(2026, time.November, 18, 10, revision, 0, 0, time.UTC), Records: []domain.Record{}, Selections: []domain.Selection{}}
		next := cloneRepositoryStored(created)
		next.Aggregate.Revision = int64(revision + 1)
		next.Aggregate.PublishedRevision = int64(revision + 1)
		created, err = repo.Commit(ctx, created.StoreRevision, next, &publication)
		if err != nil {
			t.Fatalf("Commit(%d) error = %v", revision, err)
		}
	}
	first, next, err := repo.ListHistory(ctx, "en-US", "", 2)
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("first ListHistory() = %d items, cursor %q, err %v", len(first), next, err)
	}
	second, terminal, err := repo.ListHistory(ctx, "en-US", next, 2)
	if err != nil || len(second) != 1 || terminal != "" {
		t.Fatalf("second ListHistory() = %d items, cursor %q, err %v", len(second), terminal, err)
	}
	for _, item := range append(first, second...) {
		loaded, err := repo.GetHistoryByContentRevision(ctx, "en-US", item.ContentRevision)
		if err != nil || loaded.ID != item.ID {
			t.Fatalf("GetHistoryByContentRevision(%d) = %#v, err %v", item.ContentRevision, loaded, err)
		}
	}
}

func cloneRepositoryStored(value domain.StoredWorkspace) domain.StoredWorkspace {
	value.Aggregate.Records = append(make([]domain.Record, 0, len(value.Aggregate.Records)), value.Aggregate.Records...)
	pages := make(map[string]domain.StoredPageSelections, len(value.PageSelections))
	for pageID, page := range value.PageSelections {
		page.Draft = append(make([]domain.Selection, 0, len(page.Draft)), page.Draft...)
		page.Published = append(make([]domain.Selection, 0, len(page.Published)), page.Published...)
		pages[pageID] = page
	}
	value.PageSelections = pages
	value.ChangedPageSelectionIDs = append([]string(nil), value.ChangedPageSelectionIDs...)
	return value
}

type memoryStore struct {
	rows map[recorddata.RecordKey]recorddata.Record
}

type sequenceRevisionStore struct {
	*memoryStore
	sequence int64
}

func (store *sequenceRevisionStore) Batch(_ context.Context, mutations []recorddata.RecordMutation) error {
	for _, mutation := range mutations {
		current, exists := store.rows[mutation.Key]
		if (!exists && mutation.ExpectedRevision != 0) || (exists && current.Revision != mutation.ExpectedRevision) {
			return recorddata.ErrRecordConflict
		}
	}
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(store.rows, mutation.Key)
			continue
		}
		store.sequence++
		store.rows[mutation.Key] = recorddata.Record{Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion, IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: store.sequence}
	}
	return nil
}

func newMemoryStore() *memoryStore {
	return &memoryStore{rows: map[recorddata.RecordKey]recorddata.Record{}}
}
func (store *memoryStore) Get(_ context.Context, key recorddata.RecordKey) (recorddata.Record, error) {
	row, exists := store.rows[key]
	if !exists {
		return recorddata.Record{}, recorddata.ErrRecordNotFound
	}
	return cloneRow(row), nil
}
func (store *memoryStore) Put(_ context.Context, key recorddata.RecordKey, value recorddata.RecordValue, expected int64) (recorddata.Record, error) {
	current, exists := store.rows[key]
	if (!exists && expected != 0) || (exists && current.Revision != expected) {
		return recorddata.Record{}, recorddata.ErrRecordConflict
	}
	row := recorddata.Record{Payload: append(json.RawMessage(nil), value.Payload...), PayloadVersion: value.PayloadVersion, IndexData: append(json.RawMessage(nil), value.IndexData...), Revision: expected + 1}
	store.rows[key] = row
	return cloneRow(row), nil
}
func (store *memoryStore) Delete(_ context.Context, key recorddata.RecordKey, expected int64) error {
	row, exists := store.rows[key]
	if !exists || row.Revision != expected {
		return recorddata.ErrRecordConflict
	}
	delete(store.rows, key)
	return nil
}
func (store *memoryStore) Batch(_ context.Context, mutations []recorddata.RecordMutation) error {
	for _, mutation := range mutations {
		current, exists := store.rows[mutation.Key]
		if (!exists && mutation.ExpectedRevision != 0) || (exists && current.Revision != mutation.ExpectedRevision) {
			return recorddata.ErrRecordConflict
		}
	}
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(store.rows, mutation.Key)
			continue
		}
		store.rows[mutation.Key] = recorddata.Record{Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion, IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: mutation.ExpectedRevision + 1}
	}
	return nil
}
func (store *memoryStore) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []recorddata.RecordSelector, limit int) ([]recorddata.RecordKey, []recorddata.Record, string, error) {
	keys := make([]recorddata.RecordKey, 0)
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
	rows := make([]recorddata.Record, len(keys))
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

func matches(raw json.RawMessage, selectors []recorddata.RecordSelector) bool {
	var index map[string]any
	if err := json.Unmarshal(raw, &index); err != nil {
		return false
	}
	for _, selector := range selectors {
		if selector.Operator != recorddata.RecordSelectorEqual || len(selector.Values) != 1 {
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

func cloneRow(row recorddata.Record) recorddata.Record {
	row.Payload = append(json.RawMessage(nil), row.Payload...)
	row.IndexData = append(json.RawMessage(nil), row.IndexData...)
	return row
}
