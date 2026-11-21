package engine_data

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	domainengine "github.com/Zubrium/cdn/core/domain/engine_data"
)

func newGapsStore(repository domainengine.Repository) *Store {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20,
		MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	store, err := NewStore(repository, ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{{
		Name: "records", ScopeTypes: []string{"global"},
		Fields: map[string]SelectorField{"state": {Type: SelectorString}, "tags": {Type: SelectorStringArray, MaxValues: 4}},
	}}}, limits)
	if err != nil {
		panic(err)
	}
	return store
}

type malformedPageRepository struct {
	domainengine.Repository
	keys    []domainengine.Key
	records []domainengine.Record
	next    string
}

func (r *malformedPageRepository) List(_ context.Context, _, _, _, _, _ string, _ []domainengine.ContainmentSelector, _ int) ([]domainengine.Key, []domainengine.Record, string, error) {
	return r.keys, r.records, r.next, nil
}

func (r *malformedPageRepository) ListExpired(_ context.Context, _, _, _, _, _ string, _ int) ([]domainengine.Key, []domainengine.Record, string, error) {
	return r.keys, r.records, r.next, nil
}

type countingGapsRepository struct {
	domainengine.Repository
	getCalls      int
	putCalls      int
	deleteCalls   int
	listCalls     int
	expiredCalls  int
	batchCalls    int
	listAfter     string
	listNamespace string
	listScopeType string
	listScopeID   string
	listLimit     int
	batchErr      error
}

func (r *countingGapsRepository) Get(context.Context, string, domainengine.Key) (domainengine.Record, error) {
	r.getCalls++
	return domainengine.Record{}, ErrModuleDataNotFound
}

func (r *countingGapsRepository) GetExpired(context.Context, string, domainengine.Key) (domainengine.Record, error) {
	r.expiredCalls++
	return domainengine.Record{Revision: 77, Payload: json.RawMessage(`{"state":"expired"}`)}, nil
}

func (r *countingGapsRepository) Put(context.Context, string, domainengine.Key, domainengine.Value, int64, int, int) (domainengine.Record, error) {
	r.putCalls++
	return domainengine.Record{Revision: 42}, nil
}

func (r *countingGapsRepository) Delete(context.Context, string, domainengine.Key, int64) error {
	r.deleteCalls++
	return nil
}

func (r *countingGapsRepository) Batch(context.Context, string, []domainengine.Mutation, int, int, int, int) error {
	r.batchCalls++
	return r.batchErr
}

func (r *countingGapsRepository) List(_ context.Context, _, namespace, scopeType, scopeID, after string, _ []domainengine.ContainmentSelector, limit int) ([]domainengine.Key, []domainengine.Record, string, error) {
	r.listCalls++
	r.listNamespace, r.listScopeType, r.listScopeID, r.listAfter, r.listLimit = namespace, scopeType, scopeID, after, limit
	return nil, nil, "", nil
}

func (r *countingGapsRepository) ListExpired(_ context.Context, _, namespace, scopeType, scopeID, after string, limit int) ([]domainengine.Key, []domainengine.Record, string, error) {
	r.expiredCalls++
	r.listNamespace, r.listScopeType, r.listScopeID, r.listAfter, r.listLimit = namespace, scopeType, scopeID, after, limit
	return []domainengine.Key{{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: "expired"}},
		[]domainengine.Record{{Revision: 77}}, "", nil
}

func gapsKey(record string) ModuleDataKey {
	return ModuleDataKey{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: record}
}

func TestStoreListFailsClosedOnMalformedRepositoryPage(t *testing.T) {
	validKey := domainengine.Key{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "a"}
	validRecord := domainengine.Record{Payload: json.RawMessage(`{}`)}
	cases := []struct {
		name string
		repo *malformedPageRepository
	}{
		{"cursor with storage detail", &malformedPageRepository{keys: []domainengine.Key{validKey}, records: []domainengine.Record{validRecord}, next: "dsn=secret\n"}},
		{"cursor past key bound", &malformedPageRepository{keys: []domainengine.Key{validKey}, records: []domainengine.Record{validRecord}, next: strings.Repeat("x", 257)}},
		{"foreign scope key", &malformedPageRepository{
			keys:    []domainengine.Key{{Namespace: "other", ScopeType: "global", ScopeID: "global", RecordKey: "a"}},
			records: []domainengine.Record{validRecord},
		}},
		{"key and record count mismatch", &malformedPageRepository{keys: []domainengine.Key{validKey}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := newGapsStore(test.repo)
			keys, records, next, err := store.List(context.Background(), "records", "global", "global", "", nil, 10)
			if !errors.Is(err, ErrModuleDataUnavailable) || len(keys) != 0 || len(records) != 0 || next != "" {
				t.Fatalf("malformed page leaked through List: keys=%v records=%v cursor=%q err=%v", keys, records, next, err)
			}
			keys, records, next, err = store.ListExpired(context.Background(), "records", "global", "global", "", 10)
			if !errors.Is(err, ErrModuleDataUnavailable) || len(keys) != 0 || len(records) != 0 || next != "" {
				t.Fatalf("malformed page leaked through ListExpired: keys=%v records=%v cursor=%q err=%v", keys, records, next, err)
			}
		})
	}
}

func TestStoreCASAndDeleteValidateRevisionBeforeRepository(t *testing.T) {
	repository := &countingGapsRepository{}
	store := newGapsStore(repository)
	if _, err := store.Put(context.Background(), gapsKey("a"), ModuleDataValue{Payload: json.RawMessage(`{}`), PayloadVersion: 1}, -1); !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("negative CAS revision returned %v", err)
	}
	if repository.putCalls != 0 {
		t.Fatalf("negative CAS revision reached the repository %d times", repository.putCalls)
	}
	created, err := store.Put(context.Background(), gapsKey("a"), ModuleDataValue{Payload: json.RawMessage(`{}`), PayloadVersion: 1}, 0)
	if err != nil || created.Revision != 42 || repository.putCalls != 1 {
		t.Fatalf("create CAS did not pass through repository revision: record=%+v err=%v writes=%d", created, err, repository.putCalls)
	}
	if err := store.Delete(context.Background(), gapsKey("a"), 0); !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("delete without a positive revision returned %v", err)
	}
	if repository.deleteCalls != 0 {
		t.Fatalf("invalid delete reached the repository %d times", repository.deleteCalls)
	}
	if err := store.Delete(context.Background(), gapsKey("a"), created.Revision); err != nil || repository.deleteCalls != 1 {
		t.Fatalf("valid CAS delete did not reach the repository: err=%v deletes=%d", err, repository.deleteCalls)
	}
}

func TestStoreListValidatesCursorAndScopeBeforeRepository(t *testing.T) {
	repository := &countingGapsRepository{}
	store := newGapsStore(repository)
	for _, after := range []string{"bad\x00cursor", strings.Repeat("x", 257), string([]byte{0xff})} {
		if _, _, next, err := store.List(context.Background(), "records", "global", "global", after, nil, 10); !errors.Is(err, ErrModuleDataInvalid) || next != "" || repository.listCalls != 0 {
			t.Fatalf("malformed cursor %q was not rejected before the repository: cursor=%q calls=%d err=%v", after, next, repository.listCalls, err)
		}
	}
	if _, _, _, err := store.List(context.Background(), "foreign", "global", "global", "", nil, 10); !errors.Is(err, ErrModuleDataInvalid) || repository.listCalls != 0 {
		t.Fatalf("foreign namespace reached the repository: calls=%d err=%v", repository.listCalls, err)
	}
	keys, _, next, err := store.List(context.Background(), "records", "global", "global", "cursor:a", nil, 7)
	if err != nil || next != "" || len(keys) != 0 || repository.listCalls != 1 || repository.listAfter != "cursor:a" || repository.listLimit != 7 ||
		repository.listNamespace != "records" || repository.listScopeType != "global" || repository.listScopeID != "global" {
		t.Fatalf("valid cursor was not forwarded unchanged: keys=%v cursor=%q capture=%+v err=%v", keys, next, repository, err)
	}
}

func TestStoreBatchRejectsDuplicatesAndInvalidDeletesBeforeRepository(t *testing.T) {
	repository := &countingGapsRepository{batchErr: ErrModuleDataConflict}
	store := newGapsStore(repository)
	value := ModuleDataValue{Payload: json.RawMessage(`{}`), PayloadVersion: 1}
	if err := store.Batch(context.Background(), []ModuleDataMutation{
		{Key: gapsKey("a"), ExpectedRevision: 1, Value: &value},
		{Key: gapsKey("a"), ExpectedRevision: 2, Value: &value},
	}); !errors.Is(err, ErrModuleDataInvalid) || repository.batchCalls != 0 {
		t.Fatalf("duplicate batch keys reached the repository: calls=%d err=%v", repository.batchCalls, err)
	}
	if err := store.Batch(context.Background(), []ModuleDataMutation{{Key: gapsKey("a")}}); !errors.Is(err, ErrModuleDataInvalid) || repository.batchCalls != 0 {
		t.Fatalf("delete without a revision reached the repository: calls=%d err=%v", repository.batchCalls, err)
	}
	if err := store.Batch(context.Background(), []ModuleDataMutation{
		{Key: gapsKey("a"), ExpectedRevision: 1, Value: &value},
		{Key: gapsKey("b"), ExpectedRevision: 9},
	}); !errors.Is(err, ErrModuleDataConflict) || repository.batchCalls != 1 {
		t.Fatalf("stale batch mutation did not propagate as a conflict: calls=%d err=%v", repository.batchCalls, err)
	}
}

func TestStoreTTLUsesSeparateCleanupPaths(t *testing.T) {
	repository := &countingGapsRepository{}
	store := newGapsStore(repository)
	if _, err := store.Get(context.Background(), gapsKey("expired")); !errors.Is(err, ErrModuleDataNotFound) || repository.getCalls != 1 || repository.expiredCalls != 0 {
		t.Fatalf("normal read did not conceal the expired record: get=%d expired=%d err=%v", repository.getCalls, repository.expiredCalls, err)
	}
	expired, err := store.GetExpired(context.Background(), gapsKey("expired"))
	if err != nil || expired.Revision != 77 || repository.expiredCalls != 1 {
		t.Fatalf("cleanup read could not inspect the expired record: record=%+v err=%v reads=%d", expired, err, repository.expiredCalls)
	}
	if err := store.Delete(context.Background(), gapsKey("expired"), expired.Revision); err != nil || repository.deleteCalls != 1 {
		t.Fatalf("cleanup could not delete the expired revision: err=%v deletes=%d", err, repository.deleteCalls)
	}
	keys, records, _, err := store.List(context.Background(), "records", "global", "global", "", nil, 10)
	if err != nil || len(keys) != 0 || len(records) != 0 {
		t.Fatalf("normal list exposed an expired record: keys=%v records=%v err=%v", keys, records, err)
	}
	expiredKeys, expiredRecords, _, err := store.ListExpired(context.Background(), "records", "global", "global", "", 10)
	if err != nil || len(expiredKeys) != 1 || len(expiredRecords) != 1 || expiredKeys[0].RecordKey != "expired" || expiredRecords[0].Revision != 77 {
		t.Fatalf("cleanup list did not return the expired revision: keys=%v records=%v err=%v", expiredKeys, expiredRecords, err)
	}
}

func TestStoreRegistrationAndErrorsDoNotLeakIdentityOrPayload(t *testing.T) {
	if _, err := NewStore(nil, ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{{Name: "records", ScopeTypes: []string{"global"}}}}, ModuleDataLimits{
		MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20,
		MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10,
	}); !errors.Is(err, ErrModuleDataUnavailable) {
		t.Fatalf("missing repository did not fail closed: %v", err)
	}
	leaking := &leakingRepository{secret: "engine.example dsn=postgres://secret"}
	store := newGapsStore(leaking)
	if _, err := store.Get(context.Background(), gapsKey("a")); !errors.Is(err, ErrModuleDataUnavailable) || strings.Contains(err.Error(), leaking.secret) {
		t.Fatalf("storage error leaked through the capability: %v", err)
	}
	keys, records, next, err := store.List(context.Background(), "records", "global", "global", "", []ModuleDataSelector{{Field: "secret", Operator: SelectorEqual, Values: []any{"x"}}}, 10)
	if !errors.Is(err, ErrModuleDataInvalid) || len(keys) != 0 || len(records) != 0 || next != "" || leaking.calls != 0 {
		t.Fatalf("undeclared selector leaked an index projection: keys=%v records=%v cursor=%q calls=%d err=%v", keys, records, next, leaking.calls, err)
	}
}

type leakingRepository struct {
	domainengine.Repository
	secret string
	calls  int
}

func (r *leakingRepository) Get(context.Context, string, domainengine.Key) (domainengine.Record, error) {
	return domainengine.Record{}, errors.New(r.secret)
}

func (r *leakingRepository) List(_ context.Context, _, _, _, _, _ string, _ []domainengine.ContainmentSelector, _ int) ([]domainengine.Key, []domainengine.Record, string, error) {
	r.calls++
	return nil, nil, "", nil
}
