package presentation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/infrastructure/postgres"
	"github.com/Zubrium/cdn/core/testsupport/s3mock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// coreV2RuntimeContract is the frozen public Go contract for the Core v2
// acceptance suite. It is test-local so the tests compile while production
// code is being replaced; Runtime must satisfy it before acceptance runs.
type coreV2RuntimeContract interface {
	Authenticate(context.Context, string) (Session, error)
	CreateSession(context.Context, string) (CreatedSession, error)
	AuthenticateSession(context.Context, string) (Session, Principal, error)
	RevokeSession(context.Context, string) error
	CreatePrincipal(context.Context, Session, string, *time.Time) (string, string, error)
	CreatePrincipalWithAccess(context.Context, Session, string, *time.Time, bool, map[string]access.Level) (string, string, error)
	ListPrincipals(context.Context, Session) ([]Principal, error)
	SetPrincipalManagement(context.Context, Session, string, bool) error
	DeactivatePrincipal(context.Context, Session, string) error
	IssueCredential(context.Context, Session, string, *time.Time) (string, string, error)
	ListCredentials(context.Context, Session, string) ([]Credential, error)
	RevokeCredential(context.Context, Session, string) error
	SetGroupAccess(context.Context, Session, string, string, access.Level) error
	GroupAccess(context.Context, Session, string, string) (access.Level, error)
	ListAccessGroups(context.Context, Session) ([]Group, error)
	PutStagedObject(context.Context, Session, string, []string, int64, string, io.Reader) error
	ReadStagedObject(context.Context, Session, string) (io.ReadCloser, int64, string, error)
	DeleteStagedObject(context.Context, Session, string) error
	CreateFile(context.Context, Session, string, []string, map[string]string, string, int64, io.Reader) (string, error)
	CreateFileVerified(context.Context, Session, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
	ReadFile(context.Context, Session, string) (io.ReadCloser, File, error)
	ReadFileRange(context.Context, Session, string, int64, int64) (io.ReadCloser, File, error)
	StatFile(context.Context, Session, string) (File, error)
	ListFiles(context.Context, Session, string, string, int) ([]File, string, error)
	UpdateFile(context.Context, Session, string, int64, map[string]string) error
	AddFileToGroup(context.Context, Session, string, string, int64) error
	RemoveFileFromGroup(context.Context, Session, string, string, int64) error
	DeleteFile(context.Context, Session, string, int64) error
	CreateManagedFileSet(context.Context, Session, string, string, string) (FileSet, error)
	GetManagedFileSet(context.Context, Session, string) (FileSet, error)
	UpdateManagedFileSet(context.Context, Session, string, int64, string) (FileSet, error)
	DeleteManagedFileSet(context.Context, Session, string, int64) error
	AddFileToManagedSet(context.Context, Session, string, string, int64) (FileSet, error)
	RemoveFileFromManagedSet(context.Context, Session, string, string, int64) (FileSet, error)
	ListManagedFileSets(context.Context, Session, string, string, string, int) ([]FileSet, string, error)
	ListFileSet(context.Context, Session, string, string, int) ([]File, string, error)
	SearchFiles(context.Context, Session, string, string, string, string, int) ([]File, string, error)
}

func TestRuntimeImplementsFrozenCoreV2Contract(t *testing.T) {
	want := reflect.TypeOf((*coreV2RuntimeContract)(nil)).Elem()
	got := reflect.TypeOf((*Runtime)(nil))
	var missing []string
	for i := 0; i < want.NumMethod(); i++ {
		method := want.Method(i)
		implementation, ok := got.MethodByName(method.Name)
		if !ok || !sameMethodSignature(implementation.Type, method.Type) {
			missing = append(missing, method.Name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("Runtime does not implement frozen Core v2 contract methods: %s", strings.Join(missing, ", "))
	}
}

func TestRuntimeConfigHasExplicitV2StorageAndCacheSettings(t *testing.T) {
	typ := reflect.TypeOf(RuntimeConfig{})
	for name, want := range map[string]reflect.Type{
		"AccessGroups":         reflect.TypeOf(map[string]access.Level{}),
		"CacheMaxBytes":        reflect.TypeOf(int64(0)),
		"CacheTTL":             reflect.TypeOf(time.Duration(0)),
		"MaxMetadataBytes":     reflect.TypeOf(int64(0)),
		"MaxObjectBytes":       reflect.TypeOf(int64(0)),
		"MaxCachedObjectBytes": reflect.TypeOf(int64(0)),
		"PendingTTL":           reflect.TypeOf(time.Duration(0)),
		"SessionIdleTTL":       reflect.TypeOf(time.Duration(0)),
		"SessionAbsoluteTTL":   reflect.TypeOf(time.Duration(0)),
		"MaintenanceInterval":  reflect.TypeOf(time.Duration(0)),
		"MaintenanceTimeout":   reflect.TypeOf(time.Duration(0)),
		"MaintenanceBatchSize": reflect.TypeOf(int(0)),
		"ModuleData":           reflect.TypeOf(ModuleDataLimits{}),
	} {
		field, ok := typ.FieldByName(name)
		if !ok || field.Type != want {
			t.Errorf("RuntimeConfig.%s must have type %v", name, want)
		}
	}
	if _, ok := typ.FieldByName("DeletedRetention"); ok {
		t.Error("permanent deletion must not keep a deleted-file retention setting")
	}
}

func sameMethodSignature(implementation, contract reflect.Type) bool {
	if implementation.NumIn() != contract.NumIn()+1 || implementation.NumOut() != contract.NumOut() || implementation.IsVariadic() != contract.IsVariadic() {
		return false
	}
	if implementation.In(0) != reflect.TypeOf((*Runtime)(nil)) {
		return false
	}
	for i := 0; i < contract.NumIn(); i++ {
		if implementation.In(i+1) != contract.In(i) {
			return false
		}
	}
	for i := 0; i < contract.NumOut(); i++ {
		if implementation.Out(i) != contract.Out(i) {
			return false
		}
	}
	return true
}

func TestDisposableCoreV2FileGroupSetAndCacheAcceptance(t *testing.T) {
	dsn := os.Getenv("TEST_CORE_PLATFORM_DATABASE_URL")
	migrationDSN := os.Getenv("TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL")
	endpoint := os.Getenv("TEST_CORE_PLATFORM_S3_ENDPOINT")
	bucket := os.Getenv("TEST_CORE_PLATFORM_S3_BUCKET")
	accessKey := os.Getenv("TEST_CORE_PLATFORM_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_CORE_PLATFORM_S3_SECRET_KEY")
	if dsn == "" || migrationDSN == "" {
		t.Skip("disposable Core PostgreSQL migration/runtime users are not configured")
	}
	if endpoint == "" && bucket == "" && accessKey == "" && secretKey == "" {
		server := s3mock.New()
		t.Cleanup(server.Close)
		endpoint, bucket, accessKey, secretKey = server.URL(), "zubrium-core-qa-platform-unit", "test-access", "test-secret"
	} else if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Fatal("configure all TEST_CORE_PLATFORM_S3_* values or leave them all unset to use the isolated S3 test server")
	}
	assertIsolatedCoreS3Endpoint(t, endpoint)
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(parsed.ConnConfig.Database, "zubrium_core_qa_") || !strings.HasPrefix(bucket, "zubrium-core-qa-") {
		t.Fatal("refusing non-disposable database or bucket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	probe, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		t.Fatal(err)
	}
	var sessions int
	err = probe.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()`).Scan(&sessions)
	if err != nil || sessions != 0 {
		probe.Close()
		t.Fatalf("disposable database has other sessions: %d: %v", sessions, err)
	}
	var initialized bool
	if err := probe.QueryRow(ctx, `SELECT to_regclass('public.atlas_schema_revisions') IS NOT NULL OR to_regclass('core.atlas_schema_revisions') IS NOT NULL OR to_regclass('access.users') IS NOT NULL OR EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='core')`).Scan(&initialized); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	probe.Close()
	if initialized {
		t.Fatal("refusing a database that already contains a Core schema; provide a fresh disposable database")
	}
	cfg := newCoreV2TestConfig(t, dsn, migrationDSN, endpoint, bucket, accessKey, secretKey)
	cfg.Database.URL = withApplicationName(t, cfg.Database.URL, "core-v2-primary")
	first, managerSecret, err := Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	core, ok := any(first).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("Runtime does not implement the frozen Core v2 acceptance contract")
	}
	if err := first.Registry.Register("core-contract-test"); err != nil {
		t.Fatalf("register contract-test module: %v", err)
	}
	manager, err := core.Authenticate(ctx, managerSecret)
	if err != nil {
		t.Fatal(err)
	}
	groupAccess := first.GroupAuthorization()
	stagedFiles := first.StagedFiles()
	managerSession, managerPrincipal, err := first.GroupAuthorization().Authenticate(ctx, managerSecret)
	if err != nil || len(managerSession.hash) == 0 || managerPrincipal.Name != "Initial administrator" || !managerPrincipal.CanManageAdministrators {
		t.Fatalf("group-access capability authentication failed to return the authenticated principal in one capability call: principal=%+v err=%v", managerPrincipal, err)
	}
	if err := groupAccess.RequireGroupLevel(ctx, managerSession, []string{"assets", "content"}, access.LevelWrite); err != nil {
		t.Fatalf("Core capability could not check the current group level through Core: %v", err)
	}
	currentPrincipal, err := first.CurrentPrincipal(ctx, manager)
	if err != nil || currentPrincipal.Name != "Initial administrator" {
		t.Fatalf("current Core principal omitted its stored name: principal=%+v err=%v", currentPrincipal, err)
	}
	issuedSession, err := first.CreateSession(ctx, managerSecret)
	if err != nil || issuedSession.Token == "" || issuedSession.Principal.ID != managerPrincipal.ID {
		t.Fatalf("create credential-bound session: principal=%+v err=%v", issuedSession.Principal, err)
	}
	resolvedSession, resolvedPrincipal, err := first.AuthenticateSession(ctx, issuedSession.Token)
	if err != nil || resolvedPrincipal.ID != managerPrincipal.ID || !resolvedSession.persisted {
		t.Fatalf("authenticate persisted session: principal=%+v err=%v", resolvedPrincipal, err)
	}
	if err := first.RevokeSession(ctx, issuedSession.Token); err != nil {
		t.Fatalf("revoke persisted session: %v", err)
	}
	if _, _, err := first.AuthenticateSession(ctx, issuedSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked session remained usable: %v", err)
	}
	linkedPrincipalID, linkedCredential, err := first.CreatePrincipalWithAccess(ctx, manager, "Session credential test", nil, false, map[string]access.Level{"assets": access.LevelRead})
	if err != nil {
		t.Fatalf("create credential-bound session fixture: %v", err)
	}
	linkedCredentials, err := first.ListCredentials(ctx, manager, linkedPrincipalID)
	if err != nil || len(linkedCredentials) != 1 {
		t.Fatalf("list credential-bound session fixture credentials: count=%d err=%v", len(linkedCredentials), err)
	}
	linkedSession, err := first.CreateSession(ctx, linkedCredential)
	if err != nil {
		t.Fatalf("create linked session: %v", err)
	}
	if _, _, err := first.AuthenticateSession(ctx, linkedSession.Token); err != nil {
		t.Fatalf("authenticate linked session before credential revocation: %v", err)
	}
	if err := first.RevokeCredential(ctx, manager, linkedCredentials[0].ID); err != nil {
		t.Fatalf("revoke linked credential: %v", err)
	}
	if _, _, err := first.AuthenticateSession(ctx, linkedSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("session remained usable after its credential was revoked: %v", err)
	}
	moduleData, err := first.RegisterDataModule(ModuleDataDescriptor{ModuleID: "core-contract-test", Namespaces: []ModuleDataNamespace{{
		Name: "uploads", ScopeTypes: []string{"global"}, Fields: map[string]SelectorField{"state": {Type: SelectorString}},
	}}})
	if err != nil {
		t.Fatalf("register scoped module-data capability: %v", err)
	}
	if err := first.Registry.Register("media-contract-test"); err != nil {
		t.Fatalf("register second module-data consumer: %v", err)
	}
	mediaData, err := first.RegisterDataModule(ModuleDataDescriptor{ModuleID: "media-contract-test", Namespaces: []ModuleDataNamespace{{
		Name: "media", ScopeTypes: []string{"global"}, Fields: map[string]SelectorField{"state": {Type: SelectorString}},
	}}})
	if err != nil {
		t.Fatalf("register second scoped module-data capability: %v", err)
	}
	if _, err := first.RegisterDataModule(ModuleDataDescriptor{ModuleID: "core-contract-test", Namespaces: []ModuleDataNamespace{{
		Name: "uploads", ScopeTypes: []string{"global"},
	}}}); err == nil {
		t.Fatal("Core issued the same module-data capability twice")
	}
	moduleKey := ModuleDataKey{Namespace: "uploads", ScopeType: "global", ScopeID: "global", RecordKey: "contract"}
	ownerKey := ModuleDataKey{Namespace: "uploads", ScopeType: "global", ScopeID: "global", RecordKey: "owner"}
	rollbackKey := ModuleDataKey{Namespace: "uploads", ScopeType: "global", ScopeID: "global", RecordKey: "rollback"}
	abaKey := ModuleDataKey{Namespace: "uploads", ScopeType: "global", ScopeID: "global", RecordKey: "aba"}
	pageKeys := []ModuleDataKey{
		{Namespace: "uploads", ScopeType: "global", ScopeID: "global", RecordKey: "page:01"},
		{Namespace: "uploads", ScopeType: "global", ScopeID: "global", RecordKey: "page:02"},
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, key := range append([]ModuleDataKey{moduleKey, ownerKey, rollbackKey, abaKey}, pageKeys...) {
			record, err := moduleData.Get(cleanup, key)
			if err == nil {
				if err := moduleData.Delete(cleanup, key, record.Revision); err != nil && !errors.Is(err, ErrModuleDataNotFound) {
					t.Errorf("delete disposable module-data record: %v", err)
				}
			}
		}
	})
	moduleValue := func(state string, ttl time.Duration) ModuleDataValue {
		return ModuleDataValue{Payload: []byte(`{"state":"` + state + `"}`), PayloadVersion: 1,
			IndexData: []byte(`{"state":"` + state + `"}`), TTL: ttl}
	}
	if _, err := moduleData.Put(ctx, ownerKey, moduleValue("owner-a", 0), 0); err != nil {
		t.Fatalf("create module-data record owned by first capability: %v", err)
	}
	if _, err := mediaData.Get(ctx, ownerKey); !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("second capability read a guessed record in another module namespace: %v", err)
	}
	if _, err := mediaData.Put(ctx, ownerKey, moduleValue("forbidden", 0), 0); !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("second capability wrote into another module namespace: %v", err)
	}
	if err := mediaData.Delete(ctx, ownerKey, 1); !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("second capability deleted a guessed record in another module namespace: %v", err)
	}
	foreignSelector := []ModuleDataSelector{{Field: "state", Operator: SelectorEqual, Values: []any{"owner-a"}}}
	if _, _, _, err := mediaData.List(ctx, "uploads", "global", "global", "", foreignSelector, 10); !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("second capability listed another module namespace: %v", err)
	}
	ownedRecord, err := moduleData.Get(ctx, ownerKey)
	var ownerPayload struct {
		State string `json:"state"`
	}
	decodeOwnerErr := json.Unmarshal(ownedRecord.Payload, &ownerPayload)
	if err != nil || decodeOwnerErr != nil || ownerPayload.State != "owner-a" {
		t.Fatalf("foreign capability changed owner record: payload=%s err=%v decodeErr=%v", ownedRecord.Payload, err, decodeOwnerErr)
	}
	staleRecord, err := moduleData.Put(ctx, abaKey, moduleValue("before-delete", 0), 0)
	if err != nil {
		t.Fatalf("create ABA fixture: %v", err)
	}
	if err := moduleData.Delete(ctx, abaKey, staleRecord.Revision); err != nil {
		t.Fatalf("delete ABA fixture: %v", err)
	}
	recreatedRecord, err := moduleData.Put(ctx, abaKey, moduleValue("after-recreate", 0), 0)
	if err != nil || recreatedRecord.Revision <= staleRecord.Revision {
		t.Fatalf("recreate did not receive a fresh revision: old=%d new=%d err=%v", staleRecord.Revision, recreatedRecord.Revision, err)
	}
	if _, err := moduleData.Put(ctx, abaKey, moduleValue("stale-write", 0), staleRecord.Revision); !errors.Is(err, ErrModuleDataConflict) {
		t.Fatalf("stale CAS write after delete/recreate returned %v", err)
	}
	if err := moduleData.Delete(ctx, abaKey, staleRecord.Revision); !errors.Is(err, ErrModuleDataConflict) {
		t.Fatalf("stale CAS delete after delete/recreate returned %v", err)
	}
	currentABARecord, err := moduleData.Get(ctx, abaKey)
	var currentABAPayload struct {
		State string `json:"state"`
	}
	decodeABAErr := json.Unmarshal(currentABARecord.Payload, &currentABAPayload)
	if err != nil || decodeABAErr != nil || currentABARecord.Revision != recreatedRecord.Revision || currentABAPayload.State != "after-recreate" {
		t.Fatalf("stale ABA operation changed recreated record: record=%+v err=%v decodeErr=%v", currentABARecord, err, decodeABAErr)
	}
	createdModuleRecord, err := moduleData.Put(ctx, moduleKey, moduleValue("pending", time.Second), 0)
	if err != nil {
		t.Fatalf("create module-data record: %v", err)
	}
	for _, key := range pageKeys {
		if _, err := moduleData.Put(ctx, key, moduleValue("pending", 0), 0); err != nil {
			t.Fatalf("create module-data pagination fixture: %v", err)
		}
	}
	selector := []ModuleDataSelector{{Field: "state", Operator: SelectorEqual, Values: []any{"pending"}}}
	var listedKeys []ModuleDataKey
	cursor := ""
	pageCount := 0
	for page := 0; page < 3; page++ {
		keys, _, next, listErr := moduleData.List(ctx, "uploads", "global", "global", cursor, selector, 10)
		if listErr != nil {
			t.Fatalf("list module data by typed selector: %v", listErr)
		}
		if len(keys) > 10 {
			t.Fatalf("module-data list exceeded its requested row limit: got %d rows", len(keys))
		}
		pageCount++
		listedKeys = append(listedKeys, keys...)
		if next == "" {
			break
		}
		if next <= cursor {
			t.Fatalf("module-data cursor did not advance: %q -> %q", cursor, next)
		}
		cursor = next
	}
	if pageCount < 2 {
		t.Fatalf("module-data pages did not honor the configured byte budget: got %d page(s)", pageCount)
	}
	wantKeys := []ModuleDataKey{moduleKey, pageKeys[0], pageKeys[1]}
	if len(listedKeys) != len(wantKeys) {
		t.Fatalf("typed module-data selector lost rows across bounded pages: got %#v", listedKeys)
	}
	for i := range wantKeys {
		if listedKeys[i] != wantKeys[i] {
			t.Fatalf("typed module-data page %d returned %s; want %s", i, listedKeys[i].RecordKey, wantKeys[i].RecordKey)
		}
	}
	time.Sleep(time.Second + 50*time.Millisecond)
	if _, err := moduleData.Get(ctx, moduleKey); !errors.Is(err, ErrModuleDataNotFound) {
		t.Fatalf("expired module-data row remained visible to normal reads: %v", err)
	}
	if _, err := moduleData.GetExpired(ctx, moduleKey); err != nil {
		t.Fatalf("cleanup could not read an expired module-data row: %v", err)
	}
	expiredKeys, expiredRecords, expiredCursor, err := moduleData.ListExpired(ctx, "uploads", "global", "global", "", 10)
	if err != nil || expiredCursor != "" || len(expiredKeys) != 1 || expiredKeys[0] != moduleKey || expiredRecords[0].Revision != createdModuleRecord.Revision {
		t.Fatalf("expired module-data list did not return the bounded cleanup record: keys=%v records=%v cursor=%q err=%v", expiredKeys, expiredRecords, expiredCursor, err)
	}
	if _, err := moduleData.Put(ctx, moduleKey, moduleValue("ready", 0), 0); err != nil {
		t.Fatalf("expired row was not safely replaceable: %v", err)
	}
	currentModuleRecord, err := moduleData.Get(ctx, moduleKey)
	if err != nil || currentModuleRecord.Revision <= createdModuleRecord.Revision {
		t.Fatalf("module-data revision did not advance after expiry: created=%d current=%d err=%v", createdModuleRecord.Revision, currentModuleRecord.Revision, err)
	}
	rollbackValue := moduleValue("should-rollback", 0)
	changedValue := moduleValue("changed", 0)
	if err := moduleData.Batch(ctx, []ModuleDataMutation{
		{Key: moduleKey, ExpectedRevision: currentModuleRecord.Revision, Value: &changedValue},
		{Key: rollbackKey, ExpectedRevision: 9, Value: &rollbackValue},
	}); !errors.Is(err, ErrModuleDataConflict) {
		t.Fatalf("stale module-data batch did not conflict atomically: %v", err)
	}
	unchangedModuleRecord, err := moduleData.Get(ctx, moduleKey)
	var unchangedPayload struct {
		State string `json:"state"`
	}
	decodeErr := json.Unmarshal(unchangedModuleRecord.Payload, &unchangedPayload)
	if err != nil || decodeErr != nil || unchangedPayload.State != "ready" {
		t.Fatalf("module-data batch leaked a partial update: payload=%s err=%v decodeErr=%v", unchangedModuleRecord.Payload, err, decodeErr)
	}

	// Engine-data acceptance gaps on the same disposable PostgreSQL runtime.
	gapsModuleID := "engine-data-gaps"
	if err := first.Registry.Register(gapsModuleID); err != nil {
		t.Fatalf("register engine-data gap module: %v", err)
	}
	gapsData, err := first.RegisterDataModule(ModuleDataDescriptor{ModuleID: gapsModuleID, Namespaces: []ModuleDataNamespace{{
		Name: "records", ScopeTypes: []string{"global"},
		Fields: map[string]SelectorField{"state": {Type: SelectorString}, "tags": {Type: SelectorStringArray, MaxValues: 4}},
	}}})
	if err != nil {
		t.Fatalf("register engine-data gap capability: %v", err)
	}
	gapsValue := func(state string, ttl time.Duration) ModuleDataValue {
		return ModuleDataValue{Payload: []byte(`{"state":"` + state + `"}`), PayloadVersion: 1,
			IndexData: []byte(`{"state":"` + state + `","tags":["alpha"]}`), TTL: ttl}
	}
	gapsKeys := []ModuleDataKey{
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "bounds"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "ttl-expired"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "ttl-active"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "cas"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "batch"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "batch-stale"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "cursor:a"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "cursor:b"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "cursor:c"},
		{Namespace: "records", ScopeType: "global", ScopeID: "global", RecordKey: "cursor:0"},
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, key := range gapsKeys {
			record, getErr := gapsData.Get(cleanup, key)
			if errors.Is(getErr, ErrModuleDataNotFound) {
				record, getErr = gapsData.GetExpired(cleanup, key)
			}
			if getErr != nil {
				continue
			}
			if deleteErr := gapsData.Delete(cleanup, key, record.Revision); deleteErr != nil && !errors.Is(deleteErr, ErrModuleDataNotFound) && !errors.Is(deleteErr, ErrModuleDataConflict) {
				t.Errorf("delete disposable engine-data gap record %s: %v", key.RecordKey, deleteErr)
			}
		}
	})
	unknownCapability := ModuleDataDescriptor{ModuleID: "unknown-engine-data", Namespaces: []ModuleDataNamespace{{Name: "records", ScopeTypes: []string{"global"}}}}
	if issued, capErr := first.RegisterDataModule(unknownCapability); !errors.Is(capErr, ErrModuleDataInvalid) || issued != nil {
		t.Fatalf("unknown engine-data capability was issued: store=%v err=%v", issued, capErr)
	}
	oversizedIndex := ModuleDataValue{Payload: []byte(`{"state":"large"}`), PayloadVersion: 1,
		IndexData: []byte(`{"state":"` + strings.Repeat("x", 1<<16+1) + `"}`)}
	if _, putErr := gapsData.Put(ctx, gapsKeys[0], oversizedIndex, 0); !errors.Is(putErr, ErrModuleDataLimitExceeded) {
		t.Fatalf("oversized index projection was accepted: %v", putErr)
	}
	undeclaredIndex := ModuleDataValue{Payload: []byte(`{"state":"x"}`), PayloadVersion: 1, IndexData: []byte(`{"secret":"leak"}`)}
	if _, putErr := gapsData.Put(ctx, gapsKeys[0], undeclaredIndex, 0); !errors.Is(putErr, ErrModuleDataInvalid) {
		t.Fatalf("undeclared index field was accepted: %v", putErr)
	}
	if _, getErr := gapsData.Get(ctx, gapsKeys[0]); !errors.Is(getErr, ErrModuleDataNotFound) {
		t.Fatalf("rejected writes left a persisted record: %v", getErr)
	}
	stateSelector := []ModuleDataSelector{{Field: "state", Operator: SelectorEqual, Values: []any{"cursor"}}}
	for _, invalid := range []struct {
		name      string
		namespace string
		selectors []ModuleDataSelector
	}{
		{"unknown namespace", "foreign", stateSelector},
		{"undeclared selector", "records", []ModuleDataSelector{{Field: "secret", Operator: SelectorEqual, Values: []any{"x"}}}},
		{"wrong selector arity", "records", []ModuleDataSelector{{Field: "state", Operator: SelectorEqual, Values: []any{"a", "b"}}}},
	} {
		listKeys, listRecords, listCursor, listErr := gapsData.List(ctx, invalid.namespace, "global", "global", "", invalid.selectors, 10)
		if !errors.Is(listErr, ErrModuleDataInvalid) || len(listKeys) != 0 || len(listRecords) != 0 || listCursor != "" {
			t.Fatalf("%s leaked a page: keys=%v records=%v cursor=%q err=%v", invalid.name, listKeys, listRecords, listCursor, listErr)
		}
	}
	for _, after := range []string{"bad\x00cursor", strings.Repeat("x", 257)} {
		listKeys, listRecords, listCursor, listErr := gapsData.List(ctx, "records", "global", "global", after, nil, 10)
		if !errors.Is(listErr, ErrModuleDataInvalid) || len(listKeys) != 0 || len(listRecords) != 0 || listCursor != "" {
			t.Fatalf("malformed cursor %q was accepted: keys=%v records=%v cursor=%q err=%v", after, listKeys, listRecords, listCursor, listErr)
		}
	}
	for _, key := range gapsKeys[6:9] {
		if _, putErr := gapsData.Put(ctx, key, gapsValue("cursor", 0), 0); putErr != nil {
			t.Fatalf("create cursor stability fixture: %v", putErr)
		}
	}
	firstPageKeys, firstPageRecords, cursorAfterFirst, listErr := gapsData.List(ctx, "records", "global", "global", "", stateSelector, 1)
	if listErr != nil || len(firstPageKeys) != 1 || len(firstPageRecords) != 1 || cursorAfterFirst == "" || cursorAfterFirst != firstPageKeys[0].RecordKey {
		t.Fatalf("first keyset page did not return one bounded row: keys=%v records=%d cursor=%q err=%v", firstPageKeys, len(firstPageRecords), cursorAfterFirst, listErr)
	}
	if _, putErr := gapsData.Put(ctx, gapsKeys[9], gapsValue("cursor", 0), 0); putErr != nil {
		t.Fatalf("insert a record before the active cursor: %v", putErr)
	}
	seenCursorKeys := map[string]bool{firstPageKeys[0].RecordKey: true}
	secondPageKeys, _, cursorAfterSecond, listErr := gapsData.List(ctx, "records", "global", "global", cursorAfterFirst, stateSelector, 10)
	if listErr != nil || cursorAfterSecond != "" {
		t.Fatalf("second keyset page failed: keys=%v cursor=%q err=%v", secondPageKeys, cursorAfterSecond, listErr)
	}
	for _, key := range secondPageKeys {
		if seenCursorKeys[key.RecordKey] || key.RecordKey == gapsKeys[9].RecordKey {
			t.Fatalf("keyset page repeated a row or exposed a pre-cursor insert: keys=%v", secondPageKeys)
		}
		seenCursorKeys[key.RecordKey] = true
	}
	if len(seenCursorKeys) != 3 {
		t.Fatalf("keyset pagination lost stable rows: seen=%v", seenCursorKeys)
	}
	if _, putErr := gapsData.Put(ctx, gapsKeys[1], gapsValue("expired", 50*time.Millisecond), 0); putErr != nil {
		t.Fatalf("create TTL cleanup fixture: %v", putErr)
	}
	if _, putErr := gapsData.Put(ctx, gapsKeys[2], gapsValue("active", 0), 0); putErr != nil {
		t.Fatalf("create active TTL neighbor: %v", putErr)
	}
	time.Sleep(80 * time.Millisecond)
	if _, getErr := gapsData.Get(ctx, gapsKeys[1]); !errors.Is(getErr, ErrModuleDataNotFound) {
		t.Fatalf("expired engine record stayed visible: %v", getErr)
	}
	expiredGapRecord, getErr := gapsData.GetExpired(ctx, gapsKeys[1])
	if getErr != nil {
		t.Fatalf("cleanup could not inspect the expired engine record: %v", getErr)
	}
	expiredGapKeys, _, _, listErr := gapsData.ListExpired(ctx, "records", "global", "global", "", 10)
	if listErr != nil {
		t.Fatalf("cleanup could not list expired engine records: %v", listErr)
	}
	foundExpired := false
	for _, key := range expiredGapKeys {
		if key == gapsKeys[2] {
			t.Fatalf("TTL cleanup listed an active engine record: %v", expiredGapKeys)
		}
		if key == gapsKeys[1] {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("TTL cleanup did not list the expired engine record: %v", expiredGapKeys)
	}
	if deleteErr := gapsData.Delete(ctx, gapsKeys[1], expiredGapRecord.Revision); deleteErr != nil {
		t.Fatalf("TTL cleanup could not delete the expired revision: %v", deleteErr)
	}
	if _, getErr := gapsData.GetExpired(ctx, gapsKeys[1]); !errors.Is(getErr, ErrModuleDataNotFound) {
		t.Fatalf("TTL cleanup left the expired revision readable: %v", getErr)
	}
	if _, getErr := gapsData.Get(ctx, gapsKeys[2]); getErr != nil {
		t.Fatalf("TTL cleanup removed a live engine record: %v", getErr)
	}
	casGapRecord, putErr := gapsData.Put(ctx, gapsKeys[3], gapsValue("cas-1", 0), 0)
	if putErr != nil {
		t.Fatalf("create CAS revision fixture: %v", putErr)
	}
	var sequenceBefore, sequenceAfter int64
	if err := first.database.QueryRow(ctx, `SELECT last_value FROM engine_data.revision_seq`).Scan(&sequenceBefore); err != nil {
		t.Fatal(err)
	}
	rolledBackTx, txErr := first.database.Begin(ctx)
	if txErr != nil {
		t.Fatal(txErr)
	}
	if _, execErr := rolledBackTx.Exec(ctx, `UPDATE engine_data.records SET revision=nextval('engine_data.revision_seq')
		WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5 AND revision=$6`,
		gapsModuleID, "records", "global", "global", gapsKeys[3].RecordKey, casGapRecord.Revision); execErr != nil {
		_ = rolledBackTx.Rollback(ctx)
		t.Fatal(execErr)
	}
	if rollbackErr := rolledBackTx.Rollback(ctx); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if err := first.database.QueryRow(ctx, `SELECT last_value FROM engine_data.revision_seq`).Scan(&sequenceAfter); err != nil || sequenceAfter <= sequenceBefore {
		t.Fatalf("rolled-back revision was reused: before=%d after=%d err=%v", sequenceBefore, sequenceAfter, err)
	}
	casUpdated, putErr := gapsData.Put(ctx, gapsKeys[3], gapsValue("cas-2", 0), casGapRecord.Revision)
	if putErr != nil || casUpdated.Revision <= sequenceAfter {
		t.Fatalf("next successful CAS did not advance past the rolled-back revision: revision=%d sequence=%d err=%v", casUpdated.Revision, sequenceAfter, putErr)
	}
	if _, putErr := gapsData.Put(ctx, gapsKeys[3], gapsValue("stale", 0), casGapRecord.Revision); !errors.Is(putErr, ErrModuleDataConflict) {
		t.Fatalf("stale CAS update after rollback returned %v", putErr)
	}
	if deleteErr := gapsData.Delete(ctx, gapsKeys[3], casGapRecord.Revision); !errors.Is(deleteErr, ErrModuleDataConflict) {
		t.Fatalf("stale CAS delete after rollback returned %v", deleteErr)
	}
	batchGapRecord, putErr := gapsData.Put(ctx, gapsKeys[4], gapsValue("before", 0), 0)
	if putErr != nil {
		t.Fatalf("create batch atomicity fixture: %v", putErr)
	}
	batchChanged := gapsValue("after", 0)
	if batchErr := gapsData.Batch(ctx, []ModuleDataMutation{
		{Key: gapsKeys[4], ExpectedRevision: batchGapRecord.Revision, Value: &batchChanged},
		{Key: gapsKeys[5], ExpectedRevision: 9, Value: &batchChanged},
	}); !errors.Is(batchErr, ErrModuleDataConflict) {
		t.Fatalf("stale batch mutation did not conflict atomically: %v", batchErr)
	}
	batchCurrent, getErr := gapsData.Get(ctx, gapsKeys[4])
	var batchPayload struct {
		State string `json:"state"`
	}
	batchDecodeErr := json.Unmarshal(batchCurrent.Payload, &batchPayload)
	if getErr != nil || batchDecodeErr != nil || batchPayload.State != "before" || batchCurrent.Revision != batchGapRecord.Revision {
		t.Fatalf("failed batch leaked a partial update: payload=%s revision=%d err=%v decodeErr=%v", batchCurrent.Payload, batchCurrent.Revision, getErr, batchDecodeErr)
	}
	if batchErr := gapsData.Batch(ctx, []ModuleDataMutation{
		{Key: gapsKeys[4], ExpectedRevision: batchCurrent.Revision, Value: &batchChanged},
		{Key: gapsKeys[4], ExpectedRevision: batchCurrent.Revision, Value: &batchChanged},
	}); !errors.Is(batchErr, ErrModuleDataInvalid) {
		t.Fatalf("duplicate batch keys were accepted: %v", batchErr)
	}
	managerActor, err := postgres.Authenticate(ctx, first.database, managerSecret)
	if err != nil {
		t.Fatal(err)
	}
	principals, err := core.ListPrincipals(ctx, manager)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapID := ""
	for _, principal := range principals {
		if principalCanManage(t, principal) {
			if bootstrapID != "" {
				t.Fatal("fresh bootstrap created more than one management account")
			}
			bootstrapID = principal.ID
		}
	}
	if bootstrapID == "" {
		t.Fatal("fresh bootstrap account lacks explicit administrator-management permission")
	}
	auditorID, auditorCredential, err := core.CreatePrincipal(ctx, manager, "permission auditor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, auditorID, "content", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	auditor, err := core.Authenticate(ctx, auditorCredential)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ListAccessGroups(ctx, auditor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("principal without administrator-management permission listed configured groups: %v", err)
	}
	if err := core.SetPrincipalManagement(ctx, manager, auditorID, true); err != nil {
		t.Fatal(err)
	}
	auditor, err = core.Authenticate(ctx, auditorCredential)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := core.ListAccessGroups(ctx, auditor)
	if err != nil {
		t.Fatalf("administrator could not list configured groups: %v", err)
	}
	var contentLevel access.Level
	var contentFound bool
	for _, group := range groups {
		if group.Key == "content" {
			contentLevel, contentFound = group.Level, true
		}
	}
	if !contentFound || contentLevel != access.LevelWrite {
		t.Fatalf("administrator's group listing omitted its direct level: found=%t level=%d", contentFound, contentLevel)
	}
	if err := core.SetGroupAccess(ctx, manager, bootstrapID, "content", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	secondConfig := cfg
	secondConfig.Database.URL = withApplicationName(t, cfg.Database.URL, "core-v2-secondary")
	second, _, err := Start(ctx, secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	core2, ok := any(second).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("second Runtime does not implement the frozen Core v2 acceptance contract")
	}
	testCacheListenerReconnect(t, ctx, first, cfg, os.Getenv("TEST_CORE_PLATFORM_ADMIN_DATABASE_URL"))
	if level, err := core2.GroupAccess(ctx, auditor, bootstrapID, "content"); err != nil || level != access.LevelRead {
		t.Fatalf("restart overwrote an admin-managed group assignment: level=%d err=%v", level, err)
	}
	if err := core2.SetGroupAccess(ctx, auditor, bootstrapID, "content", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	stageBytes := []byte("opaque staged bytes")
	stageDigest := sha256.Sum256(stageBytes)
	stageID := uuid.NewString()
	if err := stagedFiles.PutStagedObject(ctx, managerSession, stageID, []string{"assets", "content"}, int64(len(stageBytes)), hex.EncodeToString(stageDigest[:]), bytes.NewReader(stageBytes)); err != nil {
		t.Fatalf("write Core staging object: %v", err)
	}
	if err := stagedFiles.PutStagedObject(ctx, managerSession, stageID, []string{"content", "assets"}, int64(len(stageBytes)), hex.EncodeToString(stageDigest[:]), bytes.NewReader(stageBytes)); err != nil {
		t.Fatalf("idempotent Core staging retry with reordered groups failed: %v", err)
	}
	if err := stagedFiles.PutStagedObject(ctx, managerSession, stageID, []string{"assets"}, int64(len(stageBytes)), hex.EncodeToString(stageDigest[:]), bytes.NewReader(stageBytes)); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("Core staging retry changed group scope without conflict: %v", err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := core.DeleteStagedObject(cleanup, manager, stageID); err != nil {
			t.Errorf("delete disposable staging object: %v", err)
		}
	})
	stagedReader, stagedSize, stagedChecksum, err := stagedFiles.ReadStagedObject(ctx, managerSession, stageID)
	if err != nil {
		t.Fatalf("read Core staging object: %v", err)
	}
	stagedBytes, readErr := io.ReadAll(stagedReader)
	closeErr := stagedReader.Close()
	if readErr != nil || closeErr != nil || stagedSize != int64(len(stageBytes)) || stagedChecksum != hex.EncodeToString(stageDigest[:]) || !bytes.Equal(stagedBytes, stageBytes) {
		t.Fatalf("staging roundtrip: size=%d checksum=%q body=%q read=%v close=%v", stagedSize, stagedChecksum, stagedBytes, readErr, closeErr)
	}
	if err := first.SetGroupAccess(ctx, manager, bootstrapID, "content", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := core.ReadStagedObject(ctx, manager, stageID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("staged bytes remained readable after write access was revoked: %v", err)
	}
	if err := first.SetGroupAccess(ctx, auditor, bootstrapID, "content", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE uploads.staged SET updated_at=now()-interval '24 hours' WHERE id=$1`, stageID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("recover old pending Core objects: %v", err)
	}
	stagedReader, _, _, err = core.ReadStagedObject(ctx, manager, stageID)
	if err != nil {
		t.Fatalf("Core TTL cleanup deleted a ready staged object: %v", err)
	}
	if _, err := io.Copy(io.Discard, stagedReader); err != nil {
		t.Fatalf("read staging object after recovery: %v", err)
	}
	if err := stagedReader.Close(); err != nil {
		t.Fatalf("close staging object after recovery: %v", err)
	}
	concurrentStageID := uuid.NewString()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := core.DeleteStagedObject(cleanup, manager, concurrentStageID); err != nil {
			t.Errorf("delete disposable concurrent staging object: %v", err)
		}
	})
	stageStarted, releaseStage := make(chan struct{}), make(chan struct{})
	var releaseStageOnce sync.Once
	releaseStagePut := func() { releaseStageOnce.Do(func() { close(releaseStage) }) }
	defer releaseStagePut()
	stageWriter := &controlledObjectWriter{ObjectWriter: first.objects, putStarted: stageStarted, releasePut: releaseStage}
	stagePutResult := make(chan error, 1)
	go func() {
		stagePutResult <- postgres.PutStagedObject(ctx, first.database, stageWriter, managerActor, concurrentStageID,
			[]string{"content"}, cfg.MaxObjectBytes, int64(len(stageBytes)), hex.EncodeToString(stageDigest[:]), bytes.NewReader(stageBytes))
	}()
	select {
	case <-stageStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("staged upload did not reach object storage")
	}
	if _, err := first.database.Exec(ctx, `UPDATE uploads.staged SET updated_at=now()-interval '2 hours' WHERE id=$1`, concurrentStageID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("run cleanup while staging upload is active: %v", err)
	}
	var concurrentStageState string
	if err := first.database.QueryRow(ctx, `SELECT state FROM uploads.staged WHERE id=$1`, concurrentStageID).Scan(&concurrentStageState); err != nil || concurrentStageState != "pending" {
		t.Fatalf("cleanup changed active staging reservation: state=%q err=%v", concurrentStageState, err)
	}
	releaseStagePut()
	select {
	case err := <-stagePutResult:
		if err != nil {
			t.Fatalf("finish active staging upload after cleanup pass: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("staged upload remained blocked after cleanup pass")
	}
	activeStageReader, activeStageSize, activeStageChecksum, err := core.ReadStagedObject(ctx, manager, concurrentStageID)
	if err != nil {
		t.Fatalf("active staging upload was deleted by cleanup: %v", err)
	}
	activeStageBytes, err := io.ReadAll(activeStageReader)
	closeErr = activeStageReader.Close()
	if err != nil || closeErr != nil || activeStageSize != int64(len(stageBytes)) || activeStageChecksum != hex.EncodeToString(stageDigest[:]) || !bytes.Equal(activeStageBytes, stageBytes) {
		t.Fatalf("active staging upload roundtrip: size=%d checksum=%q body=%q read=%v close=%v", activeStageSize, activeStageChecksum, activeStageBytes, err, closeErr)
	}
	if err := stagedFiles.PurgeStagedObject(ctx, concurrentStageID); err != nil {
		t.Fatalf("delete active staging test object: %v", err)
	}
	failedStageID := uuid.NewString()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := core.DeleteStagedObject(cleanup, manager, failedStageID); err != nil {
			t.Errorf("delete disposable failed staging object: %v", err)
		}
	})
	if err := core.PutStagedObject(ctx, manager, failedStageID, []string{"content"}, int64(len(stageBytes)), strings.Repeat("0", 64), bytes.NewReader(stageBytes)); err == nil {
		t.Fatal("staging accepted a mismatched checksum")
	}
	fileIDs := make([]string, 0, 8)
	setIDs := make([]string, 0, 1)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		for _, setID := range setIDs {
			set, err := core.GetManagedFileSet(cleanup, manager, setID)
			if errors.Is(err, ErrFileNotFound) {
				continue
			}
			if err != nil {
				t.Errorf("inspect disposable file set %s during cleanup: %v", setID, err)
				continue
			}
			if err := core.DeleteManagedFileSet(cleanup, manager, setID, set.Revision); err != nil {
				t.Errorf("delete disposable file set %s: %v", setID, err)
			}
		}
		for _, fileID := range fileIDs {
			file, err := core.StatFile(cleanup, manager, fileID)
			if errors.Is(err, ErrFileNotFound) {
				continue
			}
			if err != nil {
				t.Errorf("inspect disposable file %s during cleanup: %v", fileID, err)
				continue
			}
			if err := core.DeleteFile(cleanup, manager, fileID, file.Revision); err != nil {
				t.Errorf("delete disposable file %s: %v", fileID, err)
			}
		}
	})

	activeUploadKey := newRequestKey(t)
	activeUploadBody := []byte("protected active upload")
	uploadStarted, releaseUpload := make(chan struct{}), make(chan struct{})
	var releaseUploadOnce sync.Once
	releaseUploadPut := func() { releaseUploadOnce.Do(func() { close(releaseUpload) }) }
	defer releaseUploadPut()
	activeUploadWriter := &controlledObjectWriter{ObjectWriter: first.objects, putStarted: uploadStarted, releasePut: releaseUpload}
	activeUploadCreator := postgres.Creator{Pool: first.database, Store: activeUploadWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	activeUploadResult := make(chan struct {
		id  string
		err error
	}, 1)
	go func() {
		id, err := activeUploadCreator.Create(ctx, managerActor, activeUploadKey, []string{"assets"},
			map[string]string{"name": "active-upload.bin"}, "application/octet-stream", int64(len(activeUploadBody)), bytes.NewReader(activeUploadBody))
		activeUploadResult <- struct {
			id  string
			err error
		}{id: id, err: err}
	}()
	select {
	case <-uploadStarted:
	case result := <-activeUploadResult:
		t.Fatalf("file upload failed before object storage: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("file upload did not reach object storage")
	}
	var activeUploadID uuid.UUID
	if err := first.database.QueryRow(ctx, `SELECT object_id FROM uploads.requests WHERE user_id=$1 AND request_key=$2`, managerActor.ID, activeUploadKey).Scan(&activeUploadID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE files.objects SET updated_at=now()-interval '2 hours' WHERE id=$1`, activeUploadID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("run cleanup while file upload is active: %v", err)
	}
	var activeUploadState string
	if err := first.database.QueryRow(ctx, `SELECT state FROM files.objects WHERE id=$1`, activeUploadID).Scan(&activeUploadState); err != nil || activeUploadState != "pending" {
		t.Fatalf("cleanup changed active file reservation: state=%q err=%v", activeUploadState, err)
	}
	releaseUploadPut()
	select {
	case result := <-activeUploadResult:
		if result.err != nil || result.id != activeUploadID.String() {
			t.Fatalf("active file upload did not complete after cleanup pass: id=%s want=%s err=%v", result.id, activeUploadID, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("file upload remained blocked after cleanup pass")
	}
	fileIDs = append(fileIDs, activeUploadID.String())
	if _, err := core.StatFile(ctx, manager, activeUploadID.String()); err != nil {
		t.Fatalf("active file upload was deleted by cleanup: %v", err)
	}

	// A failed S3 write must never publish a file. The failed reservation stays
	// retryable with the same idempotency key and is removed by bounded recovery.
	retryKey := newRequestKey(t)
	retryMetadata := map[string]string{"name": "retry-after-storage-error.txt"}
	failedWriter := &controlledObjectWriter{ObjectWriter: first.objects, putFailure: errors.New("simulated S3 outage")}
	failedCreator := postgres.Creator{Pool: first.database, Store: failedWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	if _, err := failedCreator.Create(ctx, managerActor, retryKey, []string{"assets"}, retryMetadata, "text/plain", 5, strings.NewReader("retry")); err == nil {
		t.Fatal("Core reported success after the object store rejected a write")
	}
	var failedState string
	if err := first.database.QueryRow(ctx, `SELECT o.state FROM uploads.requests r JOIN files.objects o ON o.id=r.object_id
		WHERE r.user_id=$1 AND r.request_key=$2`, managerActor.ID, retryKey).Scan(&failedState); err != nil || failedState != "failed" {
		t.Fatalf("failed upload reservation state=%q err=%v", failedState, err)
	}
	uncertainKey := newRequestKey(t)
	writeCtx, cancelWrite := context.WithCancel(ctx)
	uncertainWriter := &controlledObjectWriter{ObjectWriter: first.objects, putFailure: errors.New("simulated canceled S3 request"), cancelOnPutFailure: cancelWrite}
	uncertainCreator := postgres.Creator{Pool: first.database, Store: uncertainWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	if _, err := uncertainCreator.Create(writeCtx, managerActor, uncertainKey, []string{"assets"}, retryMetadata,
		"text/plain", 5, strings.NewReader("retry")); err == nil {
		t.Fatal("Core reported success after the request was canceled")
	}
	var uncertainState string
	if err := first.database.QueryRow(ctx, `SELECT o.state FROM uploads.requests r JOIN files.objects o ON o.id=r.object_id
		WHERE r.user_id=$1 AND r.request_key=$2`, managerActor.ID, uncertainKey).Scan(&uncertainState); err != nil || uncertainState != "failed" {
		t.Fatalf("canceled upload reservation was not marked failed with a cleanup context: state=%q err=%v", uncertainState, err)
	}
	cancelWrite()
	retriedID, err := (postgres.Creator{Pool: first.database, Store: first.objects, MaxMetadataBytes: cfg.MaxMetadataBytes}).Create(ctx, managerActor, retryKey, []string{"assets"}, retryMetadata, "text/plain", 5, strings.NewReader("retry"))
	if err != nil {
		t.Fatalf("retry failed upload with the same idempotency key: %v", err)
	}
	fileIDs = append(fileIDs, retriedID)
	if _, err := core.StatFile(ctx, manager, retriedID); err != nil {
		t.Fatalf("successful upload retry was not visible: %v", err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE files.objects SET updated_at=now()-interval '2 hours' WHERE state='failed'`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("clean up failed upload reservation: %v", err)
	}

	// A failed S3 delete leaves an inaccessible, retryable deleting state. A
	// later cleanup safely repeats deletion and removes its metadata.
	retriedFile, err := core.StatFile(ctx, manager, retriedID)
	if err != nil {
		t.Fatal(err)
	}
	deleteFailureWriter := &controlledObjectWriter{ObjectWriter: first.objects, deleteFailure: errors.New("simulated S3 delete outage")}
	deleteFailureCreator := postgres.Creator{Pool: first.database, Store: deleteFailureWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	if err := deleteFailureCreator.Delete(ctx, managerActor, retriedID, retriedFile.Revision); err == nil {
		t.Fatal("Core reported success after the object store rejected deletion")
	}
	if _, err := core.StatFile(ctx, manager, retriedID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("file stayed visible after delete entered recovery: %v", err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE files.objects SET updated_at=now()-interval '2 hours' WHERE id=$1`, retriedID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("retry failed S3 deletion: %v", err)
	}
	var objectRemains bool
	if err := first.database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM files.objects WHERE id=$1)`, retriedID).Scan(&objectRemains); err != nil || objectRemains {
		t.Fatalf("recovery retained a deleted file row: exists=%v err=%v", objectRemains, err)
	}

	// If S3 deletion succeeds but PostgreSQL finalization is canceled, recovery
	// must tolerate the already-absent S3 key and finish the database cleanup.
	databaseFailureID, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "finalize-after-db-error.txt"}, "text/plain", 4, strings.NewReader("done"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, databaseFailureID)
	databaseFailureFile, err := core.StatFile(ctx, manager, databaseFailureID)
	if err != nil {
		t.Fatal(err)
	}
	deleteCtx, cancelDelete := context.WithCancel(ctx)
	databaseFailureWriter := &controlledObjectWriter{ObjectWriter: first.objects, afterSuccessfulDelete: cancelDelete}
	databaseFailureCreator := postgres.Creator{Pool: first.database, Store: databaseFailureWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	deleteErr := databaseFailureCreator.Delete(deleteCtx, managerActor, databaseFailureID, databaseFailureFile.Revision)
	cancelDelete()
	if deleteErr == nil {
		t.Fatal("Core reported a committed delete after PostgreSQL finalization was canceled")
	}
	if _, err := core.StatFile(ctx, manager, databaseFailureID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("file became visible after database finalization failed: %v", err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE files.objects SET updated_at=now()-interval '2 hours' WHERE id=$1`, databaseFailureID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("recover failed PostgreSQL finalization after S3 deletion: %v", err)
	}
	if err := first.database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM files.objects WHERE id=$1)`, databaseFailureID).Scan(&objectRemains); err != nil || objectRemains {
		t.Fatalf("post-failure recovery retained a deleted file row: exists=%v err=%v", objectRemains, err)
	}

	viewerID, viewerKey, err := core.CreatePrincipal(ctx, manager, "asset viewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := core.Authenticate(ctx, viewerKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := core.ReadStagedObject(ctx, viewer, stageID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("another principal read a staged object: %v", err)
	}
	if err := core.DeleteStagedObject(ctx, viewer, stageID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("another principal deleted a staged object: %v", err)
	}
	principals, err = core.ListPrincipals(ctx, manager)
	if err != nil {
		t.Fatal(err)
	}
	foundViewer := false
	for _, principal := range principals {
		if principal.ID == viewerID {
			foundViewer = principal.Active && !principalCanManage(t, principal)
		}
	}
	if !foundViewer {
		t.Fatal("new single-type user was not listed as active without administrator-management permission")
	}
	if level, err := core.GroupAccess(ctx, manager, viewerID, "assets"); err != nil || level != access.LevelNone {
		t.Fatalf("new user default group access: level=%d err=%v", level, err)
	}
	if err := core.SetGroupAccess(ctx, manager, viewerID, "assets", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	viewerSession, _, err := groupAccess.Authenticate(ctx, viewerKey)
	if err != nil {
		t.Fatalf("authenticate the reader through group-access capability: %v", err)
	}
	if err := groupAccess.RequireGroupLevel(ctx, viewerSession, []string{"assets"}, access.LevelRead); err != nil {
		t.Fatalf("Core capability denied the reader's configured read level: %v", err)
	}
	if err := groupAccess.RequireGroupLevel(ctx, viewerSession, []string{"assets"}, access.LevelWrite); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("Core capability granted write access to a read-only group assignment: %v", err)
	}
	sharedRequest := newRequestKey(t)
	sharedMetadata := map[string]string{"name": "shared.txt", "caption": "original"}
	managerFile, err := core.CreateFile(ctx, manager, sharedRequest, []string{"assets", "features"}, sharedMetadata, "text/plain", 3, strings.NewReader("one"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, managerFile)
	verifiedBytes := []byte("verified object")
	verifiedDigest := sha256.Sum256(verifiedBytes)
	verifiedID, err := core.CreateFileVerified(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "verified.txt"}, "text/plain", int64(len(verifiedBytes)), hex.EncodeToString(verifiedDigest[:]), bytes.NewReader(verifiedBytes))
	if err != nil {
		t.Fatalf("verified upload rejected: %v", err)
	}
	fileIDs = append(fileIDs, verifiedID)
	verifiedFile, err := core.StatFile(ctx, manager, verifiedID)
	if err != nil || verifiedFile.SHA256 != hex.EncodeToString(verifiedDigest[:]) {
		t.Fatalf("verified file checksum was not persisted: checksum=%q err=%v", verifiedFile.SHA256, err)
	}
	objectBytes := []byte("staged file capability")
	objectDigest := sha256.Sum256(objectBytes)
	requestKey := newRequestKey(t)
	stagedFileID, err := stagedFiles.CreateFileVerified(ctx, managerSession, requestKey, []string{"assets"},
		map[string]string{"name": "staged-file-capability.txt"}, "text/plain", int64(len(objectBytes)), hex.EncodeToString(objectDigest[:]), bytes.NewReader(objectBytes))
	if err != nil {
		t.Fatalf("staged-file capability could not create a verified Core object: %v", err)
	}
	fileIDs = append(fileIDs, stagedFileID)
	requestStatus, err := stagedFiles.ResolveUploadRequest(ctx, managerPrincipal.ID, requestKey)
	if err != nil || requestStatus.State != "ready" || requestStatus.FileID != stagedFileID {
		t.Fatalf("Core capability could not resolve its Core upload request: status=%+v err=%v", requestStatus, err)
	}
	stagedFile, err := stagedFiles.StatFile(ctx, managerSession, stagedFileID)
	if err != nil {
		t.Fatalf("Core capability could not stat its Core object: %v", err)
	}
	objectReader, _, err := stagedFiles.ReadFile(ctx, managerSession, stagedFileID)
	if err != nil {
		t.Fatalf("Core capability could not stream its Core object: %v", err)
	}
	objectBytesRead, readErr := io.ReadAll(objectReader)
	closeErr = objectReader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(objectBytesRead, objectBytes) {
		t.Fatalf("Core capability object stream differs: bytes=%q read=%v close=%v", objectBytesRead, readErr, closeErr)
	}
	rangeReader, _, err := stagedFiles.ReadFileRange(ctx, managerSession, stagedFileID, 1, 2)
	if err != nil {
		t.Fatalf("Core capability could not read a Core object range: %v", err)
	}
	rangeBytes, readErr := io.ReadAll(rangeReader)
	closeErr = rangeReader.Close()
	if readErr != nil || closeErr != nil || string(rangeBytes) != "ta" {
		t.Fatalf("Core capability byte range differs: bytes=%q read=%v close=%v", rangeBytes, readErr, closeErr)
	}
	fileSearch, _, err := stagedFiles.SearchFiles(ctx, managerSession, "staged-file-capability", "assets", "", "", 20)
	if err != nil || len(fileSearch) != 1 || fileSearch[0].ID != stagedFileID {
		t.Fatalf("Core capability Core file search did not return the matching object: files=%+v err=%v", fileSearch, err)
	}
	if err := stagedFiles.UpdateFileWithContentType(ctx, managerSession, stagedFileID, stagedFile.Revision,
		map[string]string{"name": "staged-file-capability.md"}, "text/markdown"); err != nil {
		t.Fatalf("Core capability could not update Core file metadata: %v", err)
	}
	stagedFile, err = stagedFiles.StatFile(ctx, managerSession, stagedFileID)
	if err != nil || stagedFile.ContentType != "text/markdown" || fileMetadataValue(t, stagedFile, "name") != "staged-file-capability.md" {
		t.Fatalf("Core capability metadata update did not persist: file=%+v err=%v", stagedFile, err)
	}
	if err := stagedFiles.AddFileToGroup(ctx, managerSession, stagedFileID, "content", stagedFile.Revision); err != nil {
		t.Fatalf("Core capability could not add a Core group: %v", err)
	}
	stagedFile, err = stagedFiles.StatFile(ctx, managerSession, stagedFileID)
	if err != nil {
		t.Fatalf("Core capability could not inspect group change: %v", err)
	}
	if err := stagedFiles.RemoveFileFromGroup(ctx, managerSession, stagedFileID, "content", stagedFile.Revision); err != nil {
		t.Fatalf("Core capability could not remove a Core group: %v", err)
	}
	stagedFile, err = stagedFiles.StatFile(ctx, managerSession, stagedFileID)
	if err != nil {
		t.Fatalf("Core capability could not inspect second group change: %v", err)
	}
	if err := stagedFiles.DeleteFile(ctx, managerSession, stagedFileID, stagedFile.Revision); err != nil {
		t.Fatalf("Core capability could not delete a Core object: %v", err)
	}
	if _, err := core.CreateFileVerified(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "bad-checksum.txt"}, "text/plain", int64(len(verifiedBytes)), strings.Repeat("0", 64), bytes.NewReader(verifiedBytes)); err == nil {
		t.Fatal("file with mismatched expected checksum was published")
	}
	replayedID, err := core.CreateFile(ctx, manager, sharedRequest, []string{"assets", "features"}, sharedMetadata, "text/plain", 3, strings.NewReader("one"))
	if err != nil || replayedID != managerFile {
		t.Fatalf("idempotent retry: id=%s want=%s err=%v", replayedID, managerFile, err)
	}
	if _, err := core.CreateFile(ctx, manager, sharedRequest, []string{"assets"}, map[string]string{"name": "different.txt"}, "text/plain", 3, strings.NewReader("one")); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("idempotency key reused for a different request: %v", err)
	}
	if _, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "short.txt"}, "text/plain", 4, strings.NewReader("one")); err == nil {
		t.Fatal("short upload was accepted")
	}
	if _, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "oversized-metadata", "notes": strings.Repeat("x", 1<<20)}, "text/plain", 0, strings.NewReader("")); err == nil {
		t.Fatal("metadata beyond the configured limit was accepted")
	}
	listed, _, err := core.ListFiles(ctx, manager, "assets", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed {
		if fileMetadataValue(t, item, "name") == "short.txt" || fileMetadataValue(t, item, "name") == "oversized-metadata" {
			t.Fatalf("failed upload became visible: %+v", item)
		}
	}
	if _, _, err := core.ReadFile(ctx, Session{}, managerFile); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("anonymous Core read: %v", err)
	}
	body, file, err := core2.ReadFile(ctx, viewer, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "one" || fileMetadataValue(t, file, "name") != "shared.txt" || fileMetadataValue(t, file, "caption") != "original" {
		t.Fatalf("read through an assigned group: bytes=%q file=%+v err=%v", data, file, err)
	}
	if !hasFileGroup(t, file, "assets") || hasFileGroup(t, file, "features") {
		t.Fatalf("file response disclosed a group the reader cannot access: %v", file.Groups)
	}
	visibleFiles, _, err := core.SearchFiles(ctx, viewer, "", "", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, visibleFile := range visibleFiles {
		if visibleFile.ID == managerFile && (!hasFileGroup(t, visibleFile, "assets") || hasFileGroup(t, visibleFile, "features")) {
			t.Fatalf("file search disclosed a group the reader cannot access: %v", visibleFile.Groups)
		}
	}
	largePayload := strings.Repeat("L", 16)
	largeFileID, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "streamed.bin"}, "application/octet-stream", int64(len(largePayload)), strings.NewReader(largePayload))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, largeFileID)
	largeBody, _, err := core2.ReadFile(ctx, manager, largeFileID)
	if err != nil {
		t.Fatal(err)
	}
	largeBytes, readErr := io.ReadAll(largeBody)
	_ = largeBody.Close()
	if readErr != nil || string(largeBytes) != largePayload {
		t.Fatalf("streaming object larger than the cache threshold: bytes=%q err=%v", largeBytes, readErr)
	}
	if err := core.UpdateFile(ctx, viewer, managerFile, file.Revision, map[string]string{"name": "forbidden.txt"}); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("read permission allowed metadata write: %v", err)
	}

	writerID, writerKey, err := core.CreatePrincipal(ctx, manager, "asset editor", nil)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := core.Authenticate(ctx, writerKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, writerID, "assets", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if err := core.SetPrincipalManagement(ctx, manager, writerID, true); err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, writer, viewerID, "assets", access.LevelWrite); err != nil {
		t.Fatalf("delegated manager with level 2 could not assign group access: %v", err)
	}
	if err := core.SetGroupAccess(ctx, writer, viewerID, "restricted", access.LevelRead); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("management right bypassed missing group level 2: %v", err)
	}
	if err := core.SetGroupAccess(ctx, manager, viewerID, "assets", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	if err := core.SetPrincipalManagement(ctx, manager, writerID, false); err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, writer, viewerID, "assets", access.LevelWrite); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("revoked management permission remained effective: %v", err)
	}
	if err := core.AddFileToGroup(ctx, writer, managerFile, "private", file.Revision); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("group extension without target group access: %v", err)
	}
	privateReaderID, privateReaderKey, err := core.CreatePrincipal(ctx, manager, "private reader", nil)
	if err != nil {
		t.Fatal(err)
	}
	privateReader, err := core.Authenticate(ctx, privateReaderKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, privateReaderID, "private", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, privateReaderID, "assets", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.AddFileToGroup(ctx, manager, managerFile, "private", file.Revision); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFileGroup(t, file, "private") || !hasFileGroup(t, file, "assets") {
		t.Fatalf("file did not retain multiple groups: %+v", file)
	}
	if err := core.RemoveFileFromGroup(ctx, manager, managerFile, "assets", file.Revision); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.RemoveFileFromGroup(ctx, manager, managerFile, "features", file.Revision); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.RemoveFileFromGroup(ctx, manager, managerFile, "private", file.Revision); err == nil {
		t.Fatal("file lost its last access group")
	}
	if _, _, err := core.ReadFile(ctx, privateReader, managerFile); err != nil {
		t.Fatalf("read via its remaining group: %v", err)
	}

	engineID, engineKey, err := core.CreatePrincipal(ctx, manager, "sample component", nil)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := core.Authenticate(ctx, engineKey)
	if err != nil {
		t.Fatal(err)
	}
	for group, level := range map[string]access.Level{"assets": access.LevelWrite, "private": access.LevelWrite, "restricted": access.LevelRead} {
		if err := core.SetGroupAccess(ctx, manager, engineID, group, level); err != nil {
			t.Fatal(err)
		}
	}
	engineSession, _, err := groupAccess.Authenticate(ctx, engineKey)
	if err != nil {
		t.Fatalf("authenticate the engine through group-access capability: %v", err)
	}
	set, err := stagedFiles.CreateManagedFileSet(ctx, engineSession, "library", "component-assets", "assets")
	if err != nil {
		t.Fatal(err)
	}
	setIDs = append(setIDs, set.ID)
	if _, err := core.CreateManagedFileSet(ctx, engine, "library", "component-assets", "assets"); !errors.Is(err, ErrFileSetConflict) {
		t.Fatalf("duplicate file set name returned %v", err)
	}
	set, err = stagedFiles.UpdateManagedFileSet(ctx, engineSession, set.ID, set.Revision, "component-assets-v2")
	if err != nil || set.Name != "component-assets-v2" || set.Revision != 2 {
		t.Fatalf("Core capability could not update Core file-set metadata: set=%+v err=%v", set, err)
	}
	listedSets, setCursor, err := stagedFiles.ListManagedFileSets(ctx, engineSession, "library", "component-assets", "", 10)
	if err != nil || setCursor != "" || len(listedSets) != 1 || listedSets[0].ID != set.ID {
		t.Fatalf("Core capability file-set listing did not return the authorized set: sets=%+v cursor=%q err=%v", listedSets, setCursor, err)
	}
	loadedSet, err := stagedFiles.GetManagedFileSet(ctx, engineSession, set.ID)
	if err != nil || loadedSet.Revision != set.Revision || loadedSet.Name != set.Name {
		t.Fatalf("Core capability could not read Core file-set metadata: set=%+v err=%v", loadedSet, err)
	}
	if _, err := core.AddFileToManagedSet(ctx, viewer, set.ID, managerFile, set.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a read-only group assignment changed set membership: %v", err)
	}
	if err := core.DeleteManagedFileSet(ctx, viewer, set.ID, set.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a read-only group assignment deleted a file set: %v", err)
	}
	if _, err := core.GetManagedFileSet(ctx, viewer, set.ID); err != nil {
		t.Fatalf("set-management read access was not honored: %v", err)
	}
	set, err = stagedFiles.AddFileToManagedSet(ctx, engineSession, set.ID, managerFile, set.Revision)
	if err != nil {
		t.Fatal(err)
	}
	set, err = stagedFiles.RemoveFileFromManagedSet(ctx, engineSession, set.ID, managerFile, set.Revision)
	if err != nil {
		t.Fatalf("Core capability could not remove Core file-set membership: %v", err)
	}
	set, err = stagedFiles.AddFileToManagedSet(ctx, engineSession, set.ID, managerFile, set.Revision)
	if err != nil {
		t.Fatalf("Core capability could not restore Core file-set membership: %v", err)
	}
	hiddenFile, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"restricted"}, map[string]string{"name": "internal.txt"}, "text/plain", 6, strings.NewReader("secret"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, hiddenFile)
	set, err = core.AddFileToManagedSet(ctx, manager, set.ID, hiddenFile, set.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.ReadFile(ctx, viewer, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("set membership granted file access: %v", err)
	}
	visible, next, err := stagedFiles.ListFileSet(ctx, viewer, set.ID, "", 20)
	if err != nil || len(visible) != 0 || next != "" {
		t.Fatalf("inaccessible files leaked through set: count=%d cursor=%q err=%v", len(visible), next, err)
	}
	visible, next, err = core.ListFileSet(ctx, privateReader, set.ID, "", 20)
	if err != nil || len(visible) != 1 || visible[0].ID != managerFile || next != "" {
		t.Fatalf("permitted set result: count=%d cursor=%q err=%v", len(visible), next, err)
	}
	search, searchNext, err := core.SearchFiles(ctx, privateReader, "", "", "", set.ID, 20)
	if err != nil || len(search) != 1 || search[0].ID != managerFile || searchNext != "" {
		t.Fatalf("filtered file search: count=%d cursor=%q err=%v", len(search), searchNext, err)
	}
	withoutPrivate := newCoreV2TestConfig(t, dsn, migrationDSN, endpoint, bucket, accessKey, secretKey)
	setRuntimeConfigField(t, &withoutPrivate, "AccessGroups", map[string]access.Level{
		"assets": access.LevelWrite, "features": access.LevelWrite, "restricted": access.LevelWrite, "content": access.LevelWrite,
	})
	third, _, err := Start(ctx, withoutPrivate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.Close() })
	core3, ok := any(third).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("third Runtime does not implement the frozen Core v2 acceptance contract")
	}
	if _, _, err := core2.ReadFile(ctx, privateReader, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("a group removed from startup config still granted read: %v", err)
	}
	visibleWhileRemoved, nextWhileRemoved, listErr := core3.ListFileSet(ctx, privateReader, set.ID, "", 20)
	if listErr != nil || len(visibleWhileRemoved) != 0 || nextWhileRemoved != "" {
		t.Fatalf("removed group leaked through a set: count=%d cursor=%q err=%v", len(visibleWhileRemoved), nextWhileRemoved, listErr)
	}
	var privateMembershipRetained bool
	if err := first.database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM files.memberships WHERE object_id=$1 AND group_key='private')`, managerFile).Scan(&privateMembershipRetained); err != nil || !privateMembershipRetained {
		t.Fatalf("removing a configured group deleted file membership: retained=%t err=%v", privateMembershipRetained, err)
	}
	fourth, _, err := Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fourth.Close() })
	core4, ok := any(fourth).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("fourth Runtime does not implement the frozen Core v2 acceptance contract")
	}
	if _, _, err := core4.ReadFile(ctx, privateReader, managerFile); err != nil {
		t.Fatalf("restoring a configured group did not restore its saved assignment: %v", err)
	}
	if _, _, err := core2.ReadFile(ctx, privateReader, managerFile); err != nil {
		t.Fatalf("existing instance did not observe restored group access: %v", err)
	}
	body, _, err = core.ReadFileRange(ctx, privateReader, managerFile, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	rangeBytes, readErr = io.ReadAll(body)
	_ = body.Close()
	if readErr != nil || string(rangeBytes) != "ne" {
		t.Fatalf("byte range: %q err=%v", rangeBytes, readErr)
	}
	if err := stagedFiles.DeleteManagedFileSet(ctx, engineSession, set.ID, set.Revision); err != nil {
		t.Fatal(err)
	}
	setIDs = setIDs[:len(setIDs)-1]
	if _, err := core.StatFile(ctx, manager, managerFile); err != nil {
		t.Fatalf("deleting a set deleted its file: %v", err)
	}
	deleteUserID, deleteUserKey, err := core.CreatePrincipal(ctx, manager, "asset deleter", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteUser, err := core.Authenticate(ctx, deleteUserKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, deleteUserID, "assets", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	deleteCandidate, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets", "restricted"}, map[string]string{"name": "delete-through-one-group.txt"}, "text/plain", 1, strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, deleteCandidate)
	if _, _, err := core.ReadFile(ctx, deleteUser, deleteCandidate); err != nil {
		t.Fatalf("level 2 in one file group did not grant read: %v", err)
	}
	deleteCandidateMetadata, err := core.StatFile(ctx, deleteUser, deleteCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.DeleteFile(ctx, deleteUser, deleteCandidate, deleteCandidateMetadata.Revision); err != nil {
		t.Fatalf("level 2 in one file group did not permit permanent deletion: %v", err)
	}
	if _, err := core.StatFile(ctx, manager, deleteCandidate); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("deleted file remained addressable: %v", err)
	}

	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.UpdateFile(ctx, manager, managerFile, file.Revision, map[string]string{"name": "renamed.txt", "caption": "updated"}); err != nil {
		t.Fatal(err)
	}
	if err := core.UpdateFile(ctx, manager, managerFile, file.Revision, map[string]string{"name": "stale.txt"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale metadata update: %v", err)
	}
	body, renamed, err := core.ReadFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "one" || fileMetadataValue(t, renamed, "name") != "renamed.txt" || fileMetadataValue(t, renamed, "caption") != "updated" {
		t.Fatalf("metadata update changed immutable bytes: bytes=%q file=%+v err=%v", data, renamed, err)
	}
	newID, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "renamed.txt"}, "text/plain", 3, strings.NewReader("two"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, newID)
	if newID == managerFile {
		t.Fatal("new bytes reused the old file UUID")
	}

	reader2, err := core2.Authenticate(ctx, privateReaderKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, reader2, managerFile); err != nil {
		t.Fatalf("second instance initial read: %v", err)
	}
	if err := core.SetGroupAccess(ctx, manager, privateReaderID, "private", access.LevelNone); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, reader2, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("second instance returned bytes after access revocation: %v", err)
	}
	if files, _, err := core.SearchFiles(ctx, reader2, "private", "", "", "", 20); err != nil || len(files) != 0 {
		t.Fatalf("search leaked revoked file: count=%d err=%v", len(files), err)
	}
	if err := core.SetGroupAccess(ctx, manager, privateReaderID, "private", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, reader2, managerFile); err != nil {
		t.Fatalf("read after permission restoration: %v", err)
	}
	credentials, err := core.ListCredentials(ctx, manager, viewerID)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("viewer credentials: count=%d err=%v", len(credentials), err)
	}
	newCredentialID, newCredential, err := core.IssueCredential(ctx, manager, viewerID, nil)
	if err != nil {
		t.Fatal(err)
	}
	newViewerSession, err := core2.Authenticate(ctx, newCredential)
	if err != nil {
		t.Fatalf("newly issued credential did not authenticate on another instance: %v", err)
	}
	if err := core.RevokeCredential(ctx, manager, credentials[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := core2.Authenticate(ctx, viewerKey); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("revoked credential remained valid on a second instance: %v", err)
	}
	if _, _, err := core2.ReadFile(ctx, viewer, managerFile); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("existing session survived credential revocation: %v", err)
	}
	if _, _, err := core2.ReadFile(ctx, newViewerSession, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("newly issued credential has unexpected file access: %v", err)
	}
	if err := core.RevokeCredential(ctx, manager, newCredentialID); err != nil {
		t.Fatal(err)
	}
	if err := core.DeactivatePrincipal(ctx, manager, viewerID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, newViewerSession, managerFile); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("deactivated account retained an existing session: %v", err)
	}
	authorizationFailureCtx, cancelAuthorizationFailure := context.WithCancel(ctx)
	cancelAuthorizationFailure()
	if _, _, err := core2.ReadFile(authorizationFailureCtx, reader2, managerFile); err == nil {
		t.Fatal("Core returned cached bytes without a successful database permission check")
	}

	if err := core.DeleteFile(ctx, manager, managerFile, renamed.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := core.StatFile(ctx, manager, managerFile); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("permanently deleted file remained addressable: %v", err)
	}

	if err := first.Registry.Register("coordinator"); err != nil {
		t.Fatalf("register Core Coordinator descriptor: %v", err)
	}
	if err := first.Registry.Register("engine.example"); err != nil {
		t.Fatalf("register opaque workflow owner: %v", err)
	}
	operationStarted := make(chan struct{}, 1)
	executor := OperationExecutorFunc(func(taskCtx context.Context, _ Task) (TaskOutcome, error) {
		select {
		case operationStarted <- struct{}{}:
		default:
		}
		<-taskCtx.Done()
		return TaskOutcome{}, taskCtx.Err()
	})
	if err := first.StartCoordinator(ctx, CoordinatorRuntimeConfig{
		Fingerprint: strings.Repeat("c", 64), InstanceLease: 6 * time.Second, HeartbeatInterval: time.Second,
		RetryBase: time.Second, RetryMaximum: 5 * time.Second,
		MaxAttempts: 2, MaxWorkflowTasks: 10, MaxChildrenPerTask: 2,
		WorkflowRetention: time.Hour, ShutdownTimeout: 10 * time.Second, TaskClasses: []string{"engine.example"},
		Pools: []TaskPoolConfig{{ClassID: "engine.example", Enabled: true, Concurrency: 1,
			LeaseDuration: 4 * time.Second, TaskTimeout: 15 * time.Second, PollInterval: 100 * time.Millisecond, Executor: executor}},
	}); err != nil {
		t.Fatalf("start Core-owned Coordinator: %v", err)
	}
	if first.CoordinatorReady() {
		t.Fatal("Coordinator became ready before the composition activated its workers")
	}
	if err := first.CoordinatorStartupReady(ctx); err != nil {
		t.Fatalf("Coordinator lease was not ready before worker activation: %v", err)
	}
	if err := first.ActivateCoordinator(ctx); err != nil {
		t.Fatalf("activate Core Coordinator workers: %v", err)
	}
	if !first.CoordinatorReady() {
		t.Fatal("Coordinator did not become ready after successful activation")
	}
	workflowClient, err := first.NewWorkflowClient("engine.example")
	if err != nil {
		t.Fatalf("create component-scoped workflow capability: %v", err)
	}
	workflowControl, err := first.NewWorkflowControl()
	if err != nil {
		t.Fatalf("create Coordinator workflow-control capability: %v", err)
	}
	receipt, err := workflowClient.Start(ctx, WorkflowStartRequest{
		OperationID: "process", ContractVersion: 1, IdempotencyKey: "core-runtime-workflow",
		Payload: json.RawMessage(`{"action":"hold"}`),
	})
	if err != nil || !receipt.Created || receipt.State != "queued" || receipt.WorkflowID == uuid.Nil {
		t.Fatalf("submit workflow through the public Core capability: receipt=%+v err=%v", receipt, err)
	}
	select {
	case <-operationStarted:
	case <-ctx.Done():
		t.Fatalf("registered worker did not receive its durable task: %v", ctx.Err())
	}
	workflows, err := workflowControl.List(ctx, WorkflowListFilter{Limit: 10})
	if err != nil || len(workflows.Items) != 1 || workflows.Items[0].State != "running" {
		t.Fatalf("authorized workflow-control capability did not return the running workflow summary: workflows=%+v err=%v", workflows, err)
	}
	workflow, err := workflowClient.Get(ctx, receipt.WorkflowID)
	if err != nil || workflow.State != "running" {
		t.Fatalf("scoped Core workflow lookup returned the wrong state: workflow=%+v err=%v", workflow, err)
	}
	if err := workflowClient.Cancel(ctx, receipt.WorkflowID); err != nil {
		t.Fatalf("cancel workflow through its scoped Core capability: %v", err)
	}
	workflow, err = workflowClient.Get(ctx, receipt.WorkflowID)
	if err != nil || workflow.State != "cancelled" {
		t.Fatalf("Core workflow cancellation did not persist: workflow=%+v err=%v", workflow, err)
	}
	if err := first.StopCoordinator(ctx); err != nil {
		t.Fatalf("stop Core Coordinator before closing storage: %v", err)
	}
	if first.CoordinatorReady() {
		t.Fatal("stopped Coordinator remained ready")
	}
	if _, err := first.NewWorkflowClient("engine.example"); !errors.Is(err, ErrCoordinatorNotReady) {
		t.Fatalf("stopped Coordinator still issued a workflow capability: %v", err)
	}
}

func newCoreV2TestConfig(t *testing.T, dsn, migrationDSN, endpoint, bucket, accessKey, secretKey string) RuntimeConfig {
	t.Helper()
	cfg := RuntimeConfig{
		Database:             DatabaseConfig{URL: dsn, MinConnections: 1, MaxConnections: 5, ConnectTimeout: 5 * time.Second, MaxLifetime: time.Hour, MaxIdleTime: time.Minute, HealthInterval: time.Second},
		MigrationDatabaseURL: migrationDSN,
		S3:                   S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey, PathStyle: true, AllowInsecureHTTP: true},
		AtlasBinary:          os.Getenv("TEST_ATLAS_BINARY"), MigrationLockTimeout: 30 * time.Second,
	}
	if cfg.AtlasBinary == "" {
		cfg.AtlasBinary = "atlas"
	}
	values := map[string]any{
		"AccessGroups":         map[string]access.Level{"assets": access.LevelWrite, "features": access.LevelWrite, "private": access.LevelWrite, "restricted": access.LevelWrite, "content": access.LevelWrite},
		"CacheMaxBytes":        int64(1 << 20),
		"CacheTTL":             time.Minute,
		"MaxCachedObjectBytes": int64(8),
		"MaxMetadataBytes":     int64(64 << 10),
		"MaxObjectBytes":       int64(16 << 20),
		"MaintenanceInterval":  time.Hour,
		"MaintenanceTimeout":   10 * time.Second,
		"PendingTTL":           time.Hour,
		"MaintenanceBatchSize": 100,
		"SessionIdleTTL":       8 * time.Hour,
		"SessionAbsoluteTTL":   7 * 24 * time.Hour,
		"ModuleData":           ModuleDataLimits{MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20, MaxPageBytes: 3 << 20, MaxSelectors: 16, MaxSelectorValues: 64, MaxPageSize: 100, MaxBatchOperations: 64},
	}
	for name, data := range values {
		setRuntimeConfigField(t, &cfg, name, data)
	}
	return cfg
}

func withApplicationName(t *testing.T, dsn, name string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("application_name", name)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func testCacheListenerReconnect(t *testing.T, ctx context.Context, runtime *Runtime, cfg RuntimeConfig, adminDSN string) {
	t.Helper()
	if adminDSN == "" {
		t.Fatal("TEST_CORE_PLATFORM_ADMIN_DATABASE_URL must be a disposable database administrator login for listener recovery acceptance")
	}
	adminConfig, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		t.Fatal(err)
	}
	if adminConfig.ConnConfig.Database != runtimeConfig.ConnConfig.Database ||
		adminConfig.ConnConfig.Host != runtimeConfig.ConnConfig.Host || adminConfig.ConnConfig.Port != runtimeConfig.ConnConfig.Port ||
		adminConfig.ConnConfig.User == runtimeConfig.ConnConfig.User ||
		!strings.HasPrefix(adminConfig.ConnConfig.Database, "zubrium_core_qa_") {
		t.Fatal("cache-listener administrator DSN must use a separate login on the same disposable Core database")
	}
	adminConfig.MaxConns = 2
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)

	listenerPID := waitForCacheListener(t, ctx, adminPool, "core-v2-primary", 0)
	probeKey := "listener-reconnect-probe"
	runtime.byteCache.Put(probeKey, []byte("cached"), time.Minute)
	var terminated bool
	if err := adminPool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, listenerPID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate cache-listener connection %d: terminated=%v err=%v", listenerPID, terminated, err)
	}
	waitForCacheProbe(t, ctx, runtime, probeKey, true)
	waitForCacheListener(t, ctx, adminPool, "core-v2-primary", listenerPID)

	runtime.byteCache.Put(probeKey, []byte("cached after reconnect"), time.Minute)
	if _, err := adminPool.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		t.Fatalf("publish invalidation after listener reconnect: %v", err)
	}
	waitForCacheProbe(t, ctx, runtime, probeKey, true)
}

func waitForCacheListener(t *testing.T, ctx context.Context, pool *pgxpool.Pool, application string, previousPID int32) int32 {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int32
		err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity
			WHERE datname=current_database() AND application_name=$1 AND state='idle'
			AND upper(query) LIKE 'LISTEN CORE_CACHE_INVALIDATION%' ORDER BY backend_start LIMIT 1`, application).Scan(&pid)
		if err == nil && pid != previousPID {
			return pid
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for cache listener %q: %v", application, ctx.Err())
		case <-deadline.C:
			t.Fatalf("cache listener %q did not reconnect after pid %d (last query error: %v)", application, previousPID, err)
		case <-ticker.C:
		}
	}
}

func waitForCacheProbe(t *testing.T, ctx context.Context, runtime *Runtime, key string, expectMissing bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, exists := runtime.byteCache.Get(key)
		if exists != expectMissing {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for cache invalidation: %v", ctx.Err())
		case <-deadline.C:
			t.Fatal("cache invalidation listener did not clear the local cache")
		case <-ticker.C:
		}
	}
}

func assertIsolatedCoreS3Endpoint(t *testing.T, endpoint string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		t.Fatal("invalid disposable Core S3 endpoint")
	}
	if parsed.Scheme == "https" {
		return
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		t.Fatal("refusing non-loopback HTTP S3 endpoint for a test carrying credentials")
	}
}

func setRuntimeConfigField(t *testing.T, cfg *RuntimeConfig, name string, data any) {
	t.Helper()
	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() || !field.CanSet() || !reflect.ValueOf(data).Type().AssignableTo(field.Type()) {
		t.Fatalf("RuntimeConfig.%s does not match the frozen Core v2 config contract", name)
	}
	field.Set(reflect.ValueOf(data))
}

func newRequestKey(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

type controlledObjectWriter struct {
	postgres.ObjectWriter
	putFailure            error
	cancelOnPutFailure    context.CancelFunc
	deleteFailure         error
	afterSuccessfulDelete func()
	putStarted            chan struct{}
	releasePut            <-chan struct{}
}

func (w *controlledObjectWriter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if w.putFailure != nil {
		if w.cancelOnPutFailure != nil {
			w.cancelOnPutFailure()
		}
		return w.putFailure
	}
	if w.putStarted != nil {
		close(w.putStarted)
	}
	if w.releasePut != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.releasePut:
		}
	}
	return w.ObjectWriter.Put(ctx, key, body, size, contentType)
}

func (w *controlledObjectWriter) Delete(ctx context.Context, key string) error {
	if w.deleteFailure != nil {
		return w.deleteFailure
	}
	if err := w.ObjectWriter.Delete(ctx, key); err != nil {
		return err
	}
	if w.afterSuccessfulDelete != nil {
		w.afterSuccessfulDelete()
	}
	return nil
}

func principalCanManage(t *testing.T, principal Principal) bool {
	t.Helper()
	field := reflect.ValueOf(principal).FieldByName("CanManageAdministrators")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		t.Fatal("principal contract is missing CanManageAdministrators")
	}
	return field.Bool()
}

func fileMetadataValue(t *testing.T, file File, key string) string {
	t.Helper()
	metadata := reflect.ValueOf(file).FieldByName("Metadata")
	if !metadata.IsValid() || metadata.Kind() != reflect.Map || metadata.Type().Key().Kind() != reflect.String || metadata.Type().Elem().Kind() != reflect.String {
		t.Fatal("File.Metadata must be a string-to-string map")
	}
	value := metadata.MapIndex(reflect.ValueOf(key))
	if !value.IsValid() {
		return ""
	}
	return value.String()
}

func hasFileGroup(t *testing.T, file File, want string) bool {
	t.Helper()
	field := reflect.ValueOf(file).FieldByName("Groups")
	if !field.IsValid() || field.Kind() != reflect.Slice {
		t.Fatal("File contract does not contain a Groups slice")
	}
	for i := 0; i < field.Len(); i++ {
		if field.Index(i).String() == want {
			return true
		}
	}
	return false
}
