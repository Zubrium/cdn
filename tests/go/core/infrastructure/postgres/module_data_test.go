package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	appengine "github.com/Zubrium/cdn/core/application/engine_data"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestModuleDataPayloadUsesJSONNumbersWithoutFloat64Loss(t *testing.T) {
	valid := ModuleDataValue{Payload: []byte(`{"amount":1e1000}`), PayloadVersion: 1}
	if err := validateModuleDataValue(valid, 4096, 1024); err != nil {
		t.Fatalf("valid bounded JSON number was rejected by generic Core payload validation: %v", err)
	}
	tooLarge := ModuleDataValue{Payload: []byte(`{"amount":1e10000}`), PayloadVersion: 1}
	if err := validateModuleDataValue(tooLarge, 4096, 1024); !errors.Is(err, ErrModuleDataLimit) {
		t.Fatalf("oversized normalized JSONB number returned %v", err)
	}
	for _, payload := range []string{`[]`, `null`, `"string"`} {
		if err := validateModuleDataValue(ModuleDataValue{Payload: []byte(payload), PayloadVersion: 1}, 4096, 1024); err == nil {
			t.Errorf("non-object module payload %s was accepted", payload)
		}
	}
}

func TestModuleDataRejectsInvalidJSONBUnicodeAndUnrepresentableVersions(t *testing.T) {
	for _, test := range []struct {
		name  string
		value ModuleDataValue
	}{
		{name: "invalid UTF-8 in payload", value: ModuleDataValue{Payload: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, PayloadVersion: 1}},
		{name: "NUL in payload", value: ModuleDataValue{Payload: []byte(`{"value":"\u0000"}`), PayloadVersion: 1}},
		{name: "unpaired surrogate in payload", value: ModuleDataValue{Payload: []byte(`{"value":"\uD800"}`), PayloadVersion: 1}},
		{name: "invalid UTF-8 in index", value: ModuleDataValue{Payload: []byte(`{"ok":true}`), IndexData: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, PayloadVersion: 1}},
		{name: "NUL in index", value: ModuleDataValue{Payload: []byte(`{"ok":true}`), IndexData: []byte(`{"value":"\u0000"}`), PayloadVersion: 1}},
		{name: "version exceeds PostgreSQL integer", value: ModuleDataValue{Payload: []byte(`{"ok":true}`), PayloadVersion: 1 << 31}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateModuleDataValue(test.value, 4096, 1024); !errors.Is(err, ErrModuleDataInvalid) {
				t.Fatalf("invalid JSONB value returned %v", err)
			}
		})
	}

	validPair := ModuleDataValue{Payload: []byte(`{"value":"\uD83D\uDE00"}`), PayloadVersion: 1}
	if err := validateModuleDataValue(validPair, 4096, 1024); err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
}

func TestModuleDataKeyValidationMatchesRegisteredDottedEngineIDs(t *testing.T) {
	key := ModuleDataKey{Namespace: "uploads", ScopeType: "global", ScopeID: "global", RecordKey: "contract"}
	for _, moduleID := range []string{"engine", "engine-example", "engine.example", "loader.v2"} {
		if !validModuleDataKey(moduleID, key) {
			t.Errorf("registered dotted engine ID %q was rejected by the PostgreSQL adapter", moduleID)
		}
	}
	for _, moduleID := range []string{"", "Engine.example", ".engine", "engine.example/x", strings.Repeat("a", 65)} {
		if validModuleDataKey(moduleID, key) {
			t.Errorf("unregistered engine ID %q was accepted by the PostgreSQL adapter", moduleID)
		}
	}
}

func TestDisposablePostgresEngineDataPersistenceVariants(t *testing.T) {
	const envName = "TEST_CORE_ENGINE_DATA_DATABASE_URL"
	dsn := os.Getenv(envName)
	if dsn == "" {
		t.Skipf("%s is not configured", envName)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(config.ConnConfig.Database, "zubrium_core_qa_") {
		t.Fatalf("refusing non-disposable database from %s", envName)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	var tableExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('engine_data.records') IS NOT NULL`).Scan(&tableExists); err != nil || !tableExists {
		t.Fatalf("%s does not expose the migrated engine_data schema: exists=%v err=%v", envName, tableExists, err)
	}
	const moduleID = "engine.example"
	const namespace = "loader/v2"
	const scopeType = "global"
	const scopeID = "global"
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanup, `DELETE FROM engine_data.records WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key LIKE $5`,
			moduleID, namespace, scopeType, scopeID, "qa-%")
	})
	limits := appengine.ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192,
		MaxPageBytes: 1 << 20, MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	store, err := appengine.NewStore(EngineDataRepository{Pool: pool}, appengine.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []appengine.ModuleDataNamespace{{
			Name: namespace, ScopeTypes: []string{scopeType},
			Fields: map[string]appengine.SelectorField{"state": {Type: appengine.SelectorString}, "tags": {Type: appengine.SelectorStringArray, MaxValues: 4}},
		}},
	}, limits)
	if err != nil {
		t.Fatalf("register persisted engine-data capability: %v", err)
	}
	qaKey := func(record string) appengine.ModuleDataKey {
		return appengine.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: "qa-" + record}
	}
	qaValue := func(state string, ttl time.Duration) appengine.ModuleDataValue {
		return appengine.ModuleDataValue{Payload: []byte(`{"state":"` + state + `"}`), PayloadVersion: 1,
			IndexData: []byte(`{"state":"` + state + `","tags":["alpha"]}`), TTL: ttl}
	}

	created, err := store.Put(ctx, qaKey("cas"), qaValue("one", 0), 0)
	if err != nil || created.Revision <= 0 {
		t.Fatalf("persisted create failed: record=%+v err=%v", created, err)
	}
	updated, err := store.Put(ctx, qaKey("cas"), qaValue("two", 0), created.Revision)
	if err != nil || updated.Revision <= created.Revision {
		t.Fatalf("persisted CAS update did not advance the revision: old=%d new=%d err=%v", created.Revision, updated.Revision, err)
	}
	if _, err := store.Put(ctx, qaKey("cas"), qaValue("stale", 0), created.Revision); !errors.Is(err, appengine.ErrModuleDataConflict) {
		t.Fatalf("persisted stale CAS update returned %v", err)
	}
	if err := store.Delete(ctx, qaKey("cas"), updated.Revision); err != nil {
		t.Fatalf("persisted CAS delete failed: %v", err)
	}
	recreated, err := store.Put(ctx, qaKey("cas"), qaValue("three", 0), 0)
	if err != nil || recreated.Revision <= updated.Revision {
		t.Fatalf("persisted recreate reused an old revision: old=%d new=%d err=%v", updated.Revision, recreated.Revision, err)
	}
	if _, err := store.Put(ctx, qaKey("cas"), qaValue("stale-recreate", 0), created.Revision); !errors.Is(err, appengine.ErrModuleDataConflict) {
		t.Fatalf("persisted ABA update after recreate returned %v", err)
	}
	if err := store.Delete(ctx, qaKey("cas"), created.Revision); !errors.Is(err, appengine.ErrModuleDataConflict) {
		t.Fatalf("persisted ABA delete after recreate returned %v", err)
	}

	expiredKey := qaKey("ttl-expired")
	activeKey := qaKey("ttl-active")
	if _, err := store.Put(ctx, expiredKey, qaValue("expired", 50*time.Millisecond), 0); err != nil {
		t.Fatalf("create persisted TTL fixture: %v", err)
	}
	if _, err := store.Put(ctx, activeKey, qaValue("active", 0), 0); err != nil {
		t.Fatalf("create persisted TTL neighbor: %v", err)
	}
	if _, err := store.Get(ctx, expiredKey); err != nil {
		t.Fatalf("persisted record was not visible before its TTL: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := store.Get(ctx, expiredKey); !errors.Is(err, appengine.ErrModuleDataNotFound) {
		t.Fatalf("persisted record stayed visible after its TTL: %v", err)
	}
	expiredRecord, err := store.GetExpired(ctx, expiredKey)
	if err != nil {
		t.Fatalf("persisted cleanup could not read the expired record: %v", err)
	}
	expiredKeys, _, _, err := store.ListExpired(ctx, namespace, scopeType, scopeID, "", 20)
	if err != nil {
		t.Fatalf("persisted cleanup could not list expired records: %v", err)
	}
	foundExpired := false
	for _, key := range expiredKeys {
		if key == activeKey {
			t.Fatalf("persisted cleanup listed an active record: %v", expiredKeys)
		}
		if key == expiredKey {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("persisted cleanup missed the expired record: %v", expiredKeys)
	}
	if err := store.Delete(ctx, expiredKey, expiredRecord.Revision); err != nil {
		t.Fatalf("persisted cleanup delete failed: %v", err)
	}
	if _, err := store.GetExpired(ctx, expiredKey); !errors.Is(err, appengine.ErrModuleDataNotFound) {
		t.Fatalf("persisted cleanup left an expired record: %v", err)
	}
	if _, err := store.Get(ctx, activeKey); err != nil {
		t.Fatalf("persisted cleanup removed an active record: %v", err)
	}

	batchKey := qaKey("batch")
	batchCreated, err := store.Put(ctx, batchKey, qaValue("before", 0), 0)
	if err != nil {
		t.Fatalf("create persisted batch fixture: %v", err)
	}
	batchChanged := qaValue("after", 0)
	if err := store.Batch(ctx, []appengine.ModuleDataMutation{
		{Key: batchKey, ExpectedRevision: batchCreated.Revision, Value: &batchChanged},
		{Key: qaKey("batch-missing"), ExpectedRevision: 9, Value: &batchChanged},
	}); !errors.Is(err, appengine.ErrModuleDataConflict) {
		t.Fatalf("persisted stale batch did not conflict: %v", err)
	}
	batchCurrent, err := store.Get(ctx, batchKey)
	var batchState struct {
		State string `json:"state"`
	}
	batchDecodeErr := json.Unmarshal(batchCurrent.Payload, &batchState)
	if err != nil || batchDecodeErr != nil || batchCurrent.Revision != batchCreated.Revision || batchState.State != "before" {
		t.Fatalf("persisted batch leaked a partial update: record=%+v err=%v decodeErr=%v", batchCurrent, err, batchDecodeErr)
	}
	if err := store.Batch(ctx, []appengine.ModuleDataMutation{
		{Key: batchKey, ExpectedRevision: batchCurrent.Revision, Value: &batchChanged},
		{Key: batchKey, ExpectedRevision: batchCurrent.Revision, Value: &batchChanged},
	}); !errors.Is(err, appengine.ErrModuleDataInvalid) {
		t.Fatalf("persisted duplicate batch keys were accepted: %v", err)
	}

	for _, key := range []appengine.ModuleDataKey{qaKey("page:a"), qaKey("page:b"), qaKey("page:c")} {
		if _, err := store.Put(ctx, key, qaValue("page", 0), 0); err != nil {
			t.Fatalf("create persisted pagination fixture: %v", err)
		}
	}
	selector := []appengine.ModuleDataSelector{{Field: "state", Operator: appengine.SelectorEqual, Values: []any{"page"}}}
	pageKeys, _, cursor, err := store.List(ctx, namespace, scopeType, scopeID, "", selector, 1)
	if err != nil || len(pageKeys) != 1 || cursor != pageKeys[0].RecordKey {
		t.Fatalf("persisted first keyset page was not bounded: keys=%v cursor=%q err=%v", pageKeys, cursor, err)
	}
	if _, err := store.Put(ctx, qaKey("page:0"), qaValue("page", 0), 0); err != nil {
		t.Fatalf("insert before the persisted cursor: %v", err)
	}
	seen := map[string]bool{pageKeys[0].RecordKey: true}
	nextKeys, _, nextCursor, err := store.List(ctx, namespace, scopeType, scopeID, cursor, selector, 20)
	if err != nil || nextCursor != "" {
		t.Fatalf("persisted second keyset page failed: keys=%v cursor=%q err=%v", nextKeys, nextCursor, err)
	}
	for _, key := range nextKeys {
		if seen[key.RecordKey] || key.RecordKey == "qa-page:0" {
			t.Fatalf("persisted keyset page repeated a row or exposed a pre-cursor insert: keys=%v", nextKeys)
		}
		seen[key.RecordKey] = true
	}
	if len(seen) != 3 {
		t.Fatalf("persisted keyset pagination lost stable rows: seen=%v", seen)
	}
	if _, _, malformedCursor, err := store.List(ctx, namespace, scopeType, scopeID, "bad\x00cursor", nil, 10); !errors.Is(err, appengine.ErrModuleDataInvalid) || malformedCursor != "" {
		t.Fatalf("persisted malformed cursor was accepted: cursor=%q err=%v", malformedCursor, err)
	}
}

func TestModuleDataBatchEnforcesAggregateNormalizedJSONBytes(t *testing.T) {
	mutations := []ModuleDataMutation{
		{Value: &ModuleDataValue{Payload: []byte(`{"value":1e10}`), PayloadVersion: 1}},
		{Value: &ModuleDataValue{Payload: []byte(`{"value":1e10}`), PayloadVersion: 1}},
	}
	if err := validateModuleDataBatchSize(mutations, 2, 40, 128, 32); !errors.Is(err, ErrModuleDataLimit) {
		t.Fatalf("batch above aggregate JSON budget returned %v", err)
	}
	if err := validateModuleDataBatchSize(mutations, 2, 128, 128, 32); err != nil {
		t.Fatalf("bounded aggregate batch rejected: %v", err)
	}
}
