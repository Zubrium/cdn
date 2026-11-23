//go:build website_acceptance

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zubrium/cdn/pkg/contracts"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	"github.com/google/uuid"
)

// TestWebsiteIntegrationPersistenceBoundary is an executable WEB-018 check.
// It never skips: every disposable resource input is required, guarded, and
// fails the test before any connection is opened when it is missing or unsafe.
func TestWebsiteIntegrationPersistenceBoundary(t *testing.T) {
	t.Run("WEB-018", func(t *testing.T) {
		env := websitePersistenceEnvironment(t)
		if os.Getenv("WEBSITE_TEST_S3_MOCK") == "1" {
			// The in-process loopback server is used only when explicitly
			// requested. It implements exactly the four read-only S3 responses
			// required by Core RequirePrivate; PostgreSQL remains a real
			// disposable database in every mode.
			mock := httptest.NewServer(http.HandlerFunc(websitePrivateS3Handler))
			t.Cleanup(mock.Close)
			env.s3Endpoint = mock.URL
			env.s3AccessKey = "test-access"
			env.s3SecretKey = "test-secret"
			env.insecureS3HTTP = true
		} else {
			// Real disposable MinIO stores objects but does not implement the
			// four read-only bucket safety APIs Core checks at startup. Answer
			// those locally and forward every object operation to MinIO.
			websiteProxyPrivateS3Checks(t, &env)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		bootstrapSecret := newWebsitePersistenceBootstrapSecret(t)
		runtimeConfig := websiteRuntimeConfig(env, bootstrapSecret)
		runtime, bootstrapFromCore, err := core.Start(ctx, runtimeConfig)
		if err != nil {
			t.Fatalf("start Core on disposable PostgreSQL and S3: %v", err)
		}
		credential := bootstrapFromCore
		if credential == "" {
			credential = bootstrapSecret
		}
		t.Cleanup(func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer closeCancel()
			if err := runtime.CloseContext(closeCtx); err != nil {
				t.Errorf("close disposable Core runtime: %v", err)
			}
		})

		// Core exposes only registered modules to the data-capability factory.
		if err := runtime.Registry.Register(websiteengine.RecordDescriptor().OwnerID); err != nil {
			t.Fatalf("register Website module in disposable Core registry: %v", err)
		}
		store, err := websiteAcceptanceRecordStore(t, runtime, websiteengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("issue scoped Website module-data capability: %v", err)
		}

		// Build and locale capabilities are deliberately nil: WEB-018 does not
		// start a durable build, and the service fails closed if build paths are
		// reached. Only group and session capabilities are real Core capabilities.
		service, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup:            "features",
			CoordinatorAccessGroup: "features",
		}, store, runtime.GroupAuthorization(), runtime.Sessions())
		if err != nil {
			t.Fatalf("construct Website service with real Core capabilities: %v", err)
		}

		owned := &websiteOwnedKeys{store: store}
		t.Cleanup(func() { owned.deleteAll(t) })

		writeCtx, err := service.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate Website service with disposable bootstrap credential: %v", err)
		}

		// Seed a page and its root snapshot directly through the real scoped
		// Core capability. The index projection carries the opaque Core CAS
		// revision returned by the write, which is the invariant Website's
		// readers require. This exercises the same persistence substrate the
		// Website service uses, without depending on its write-side lifecycle.
		pageName := "WEB-018 " + env.runID
		pageID := uuid.NewString()
		rootID := uuid.NewString()
		config := []byte(`{"format_version":3,"page":{"id":"` + pageID + `","name":"WEB-018","placements":[]}}`)
		pageKey := websiterepo.PageKey(pageID)
		rootKey := websiteSnapshotKey(pageID, rootID)
		owned.add(pageKey, rootKey, websiteSnapshotRegistryKey(pageID))

		pagePayload, _ := json.Marshal(map[string]any{"page": map[string]any{
			"id": pageID, "name": pageName, "created_at": time.Now().UTC(),
			"latest_snapshot_id": rootID, "latest_snapshot_state": "ready",
		}})
		seedWebsiteRecord(t, ctx, store, pageKey, pagePayload, map[string]any{
			"record_type": "page", "page_id": pageID, "snapshot_id": rootID, "state": "ready",
		})
		snapshotPayload, _ := json.Marshal(map[string]any{
			"id": rootID, "page_id": pageID, "parent_id": "", "name": pageName,
			"sequence": 1, "revision": 1, "feature_pins": map[string]string{}, "state": "ready",
			"created_at": time.Now().UTC(),
			"payload":    map[string]any{"config": json.RawMessage(config), "metadata": json.RawMessage(`{}`)},
		})
		seedWebsiteRecord(t, ctx, store, rootKey, snapshotPayload, map[string]any{
			"record_type": "snapshot", "snapshot_id": rootID, "page_id": pageID, "state": "ready", "parent_id": "",
		})

		page, err := service.GetPage(writeCtx, pageID)
		if err != nil || page.ID != pageID || page.LatestSnapshotID != rootID {
			t.Fatalf("read seeded Website page = %+v, %v", page, err)
		}
		rootSnapshot, err := service.GetSnapshot(writeCtx, rootID)
		if err != nil || rootSnapshot.ID != rootID || rootSnapshot.State != "ready" {
			t.Fatalf("read seeded root snapshot = %+v, %v", rootSnapshot, err)
		}
		snapshotsBefore, err := service.ListSnapshots(writeCtx, pageID, "", 100)
		if err != nil || len(snapshotsBefore.Items) != 1 {
			t.Fatalf("list seeded snapshots: count=%d err=%v", len(snapshotsBefore.Items), err)
		}

		// A stale CAS revision must be rejected without mutating the record.
		current, err := store.Get(ctx, rootKey)
		if err != nil {
			t.Fatalf("read root record for replay probe: %v", err)
		}
		if current.Revision < 2 {
			t.Fatalf("root record has no stale revision to probe: %d", current.Revision)
		}
		value := contracts.RecordValue{
			Payload: current.Payload, PayloadVersion: current.PayloadVersion, IndexData: current.IndexData,
		}
		if _, err := store.Put(ctx, rootKey, value, current.Revision-1); !errors.Is(err, core.ErrModuleDataConflict) {
			t.Fatalf("stale exact-key replay = %v, want Core ErrModuleDataConflict", err)
		}
		afterReplay, err := store.Get(ctx, rootKey)
		if err != nil || afterReplay.Revision != current.Revision {
			t.Fatalf("replay probe changed root revision: before=%d after=%d err=%v", current.Revision, afterReplay.Revision, err)
		}

		// Core-level CAS on both a page and a snapshot record: the stale write
		// must conflict and must not advance the persisted revision.
		assertCoreCASConflict(t, ctx, store, pageKey)
		assertCoreCASConflict(t, ctx, store, rootKey)

		// A Website capability must not read, write, or list an undeclared
		// namespace or an undeclared scope of a declared namespace.
		foreignNamespace := contracts.RecordKey{
			Namespace: "content/records", ScopeType: "global", ScopeID: "global", RecordKey: "web018-foreign",
		}
		if _, err := store.Get(ctx, foreignNamespace); !errors.Is(err, core.ErrModuleDataInvalid) {
			t.Fatalf("foreign namespace read = %v, want Core ErrModuleDataInvalid", err)
		}
		if _, err := store.Put(ctx, foreignNamespace, contracts.RecordValue{
			Payload: json.RawMessage(`{}`), PayloadVersion: 1, IndexData: json.RawMessage(`{}`),
		}, 0); !errors.Is(err, core.ErrModuleDataInvalid) {
			t.Fatalf("foreign namespace write = %v, want Core ErrModuleDataInvalid", err)
		}
		if _, _, _, err := store.List(ctx, foreignNamespace.Namespace, foreignNamespace.ScopeType, foreignNamespace.ScopeID, "", nil, 10); !errors.Is(err, core.ErrModuleDataInvalid) {
			t.Fatalf("foreign namespace list = %v, want Core ErrModuleDataInvalid", err)
		}
		foreignScope := contracts.RecordKey{
			Namespace: websiterepo.SnapshotNamespace, ScopeType: "job", ScopeID: "web018", RecordKey: "web018-foreign",
		}
		if _, err := store.Get(ctx, foreignScope); !errors.Is(err, core.ErrModuleDataInvalid) {
			t.Fatalf("undeclared scope read = %v, want Core ErrModuleDataInvalid", err)
		}
		if err := store.Batch(ctx, []contracts.RecordMutation{{
			Key: foreignNamespace, ExpectedRevision: 0,
			Value: &contracts.RecordValue{Payload: json.RawMessage(`{}`), PayloadVersion: 1, IndexData: json.RawMessage(`{}`)},
		}}); !errors.Is(err, core.ErrModuleDataInvalid) {
			t.Fatalf("foreign namespace batch = %v, want Core ErrModuleDataInvalid", err)
		}

		snapshotsAfterFailures, err := service.ListSnapshots(writeCtx, pageID, "", 100)
		if err != nil || len(snapshotsAfterFailures.Items) != len(snapshotsBefore.Items) {
			t.Fatalf("failed boundary probes became observable: before=%d after=%d err=%v",
				len(snapshotsBefore.Items), len(snapshotsAfterFailures.Items), err)
		}

		// Session revocation must fail closed for both new authentication and
		// an already-resolved Website request context.
		session, err := runtime.Sessions().CreateSession(ctx, credential)
		if err != nil {
			t.Fatalf("create disposable Core session: %v", err)
		}
		sessionCtx, err := service.AuthenticateSession(ctx, session.Token)
		if err != nil {
			t.Fatalf("authenticate Website with created Core session: %v", err)
		}
		if _, err := service.GetPage(sessionCtx, pageID); err != nil {
			t.Fatalf("read run-owned page before session revocation: %v", err)
		}
		if err := runtime.Sessions().RevokeSession(ctx, session.Token); err != nil {
			t.Fatalf("revoke disposable Core session: %v", err)
		}
		if _, err := service.AuthenticateSession(ctx, session.Token); !errors.Is(err, websiteapi.ErrUnauthorized) {
			t.Fatalf("revoked session authentication = %v, want Website ErrUnauthorized", err)
		}
		if _, err := service.GetPage(sessionCtx, pageID); !errors.Is(err, websiteapi.ErrUnauthorized) {
			t.Fatalf("stale session context read = %v, want Website ErrUnauthorized", err)
		}
	})
}

type websitePersistenceEnv struct {
	runID          string
	runtimeDSN     string
	migrationDSN   string
	s3Endpoint     string
	s3Bucket       string
	s3AccessKey    string
	s3SecretKey    string
	insecureS3HTTP bool
}

func websitePersistenceEnvironment(t *testing.T) websitePersistenceEnv {
	t.Helper()
	// Core Start deliberately separates runtime DML and Atlas DDL identities on
	// the same disposable database, so both DSNs are explicit run inputs.
	names := []string{
		"WEBSITE_TEST_DISPOSABLE",
		"WEBSITE_TEST_RUN_ID",
		"WEBSITE_TEST_POSTGRES_DSN",
		"WEBSITE_TEST_POSTGRES_MIGRATION_DSN",
		"WEBSITE_TEST_S3_BUCKET",
	}
	if os.Getenv("WEBSITE_TEST_S3_MOCK") != "1" {
		names = append(names,
			"WEBSITE_TEST_S3_ENDPOINT",
			"WEBSITE_TEST_S3_ACCESS_KEY",
			"WEBSITE_TEST_S3_SECRET_KEY",
		)
	}
	missing := make([]string, 0, len(names))
	values := make(map[string]string, len(names))
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = value
	}
	if len(missing) != 0 {
		t.Fatalf("missing required disposable WEB-018 environment: %s", strings.Join(missing, ", "))
	}
	if values["WEBSITE_TEST_DISPOSABLE"] != "1" {
		t.Fatal("WEBSITE_TEST_DISPOSABLE must equal 1 for WEB-018")
	}
	runID := values["WEBSITE_TEST_RUN_ID"]
	if !websiteRunIDPattern.MatchString(runID) {
		t.Fatal("WEBSITE_TEST_RUN_ID must use only A-Za-z0-9._-")
	}

	runtimeDSN := assertDisposableDatabase(t, "WEBSITE_TEST_POSTGRES_DSN", values["WEBSITE_TEST_POSTGRES_DSN"])
	migrationDSN := assertDisposableDatabase(t, "WEBSITE_TEST_POSTGRES_MIGRATION_DSN", values["WEBSITE_TEST_POSTGRES_MIGRATION_DSN"])
	if err := core.ValidateDatabaseURLs(values["WEBSITE_TEST_POSTGRES_DSN"], values["WEBSITE_TEST_POSTGRES_MIGRATION_DSN"]); err != nil {
		t.Fatalf("Core runtime and Atlas migration DSNs must use separate users on the same disposable database: %v", err)
	}
	if runtimeDSN.Hostname() != migrationDSN.Hostname() || runtimeDSN.Port() != migrationDSN.Port() {
		t.Fatal("Core runtime and Atlas migration DSNs must target the same PostgreSQL server")
	}

	bucket := values["WEBSITE_TEST_S3_BUCKET"]
	if !hasWebsitePrefix(bucket, "zubrium-core-qa-", "zubrium-e2e-") {
		t.Fatalf("refusing S3 bucket without a disposable WEB-018 prefix: %q", bucket)
	}
	insecureS3HTTP := false
	if os.Getenv("WEBSITE_TEST_S3_MOCK") != "1" {
		endpoint, err := url.Parse(values["WEBSITE_TEST_S3_ENDPOINT"])
		if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			t.Fatalf("invalid disposable S3 endpoint: %q", values["WEBSITE_TEST_S3_ENDPOINT"])
		}
		host := endpoint.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			t.Fatalf("refusing non-loopback S3 endpoint for WEB-018: %q", host)
		}
		insecureS3HTTP = endpoint.Scheme == "http"
	}

	return websitePersistenceEnv{
		runID:          runID,
		runtimeDSN:     values["WEBSITE_TEST_POSTGRES_DSN"],
		migrationDSN:   values["WEBSITE_TEST_POSTGRES_MIGRATION_DSN"],
		s3Endpoint:     values["WEBSITE_TEST_S3_ENDPOINT"],
		s3Bucket:       bucket,
		s3AccessKey:    values["WEBSITE_TEST_S3_ACCESS_KEY"],
		s3SecretKey:    values["WEBSITE_TEST_S3_SECRET_KEY"],
		insecureS3HTTP: insecureS3HTTP,
	}
}

var websiteRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func assertDisposableDatabase(t *testing.T, name, dsn string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		t.Fatalf("%s must be a URL-form PostgreSQL DSN", name)
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if !hasWebsitePrefix(database, "zubrium_core_qa_", "zubrium_e2e_") {
		t.Fatalf("refusing non-disposable database from %s: %q", name, database)
	}
	return parsed
}

func hasWebsitePrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func newWebsitePersistenceBootstrapSecret(t *testing.T) string {
	t.Helper()
	runID := strings.TrimSpace(os.Getenv("WEBSITE_TEST_RUN_ID"))
	if runID == "" {
		t.Fatal("WEBSITE_TEST_RUN_ID is required for the disposable Core bootstrap identity")
	}
	return websiteRunBootstrapSecret(runID)
}

func websiteRuntimeConfig(env websitePersistenceEnv, bootstrapSecret string) core.RuntimeConfig {
	atlas := strings.TrimSpace(os.Getenv("TEST_ATLAS_BINARY"))
	if atlas == "" {
		atlas = "atlas"
	}
	return core.RuntimeConfig{
		Database: core.DatabaseConfig{
			URL: env.runtimeDSN, MinConnections: 1, MaxConnections: 5,
			ConnectTimeout: 5 * time.Second, MaxLifetime: time.Hour,
			MaxIdleTime: time.Minute, HealthInterval: time.Second,
		},
		MigrationDatabaseURL: env.migrationDSN,
		S3: core.S3Config{
			Endpoint: env.s3Endpoint, Region: "us-east-1", Bucket: env.s3Bucket,
			AccessKey: env.s3AccessKey, SecretKey: env.s3SecretKey,
			PathStyle: true, AllowInsecureHTTP: env.insecureS3HTTP,
		},
		BootstrapSecret:      bootstrapSecret,
		AtlasBinary:          atlas,
		MigrationLockTimeout: 30 * time.Second,
		AccessGroups: map[string]core.AccessLevel{
			"features": core.AccessLevelWrite,
		},
		CacheMaxBytes:        1 << 20,
		CacheTTL:             time.Minute,
		MaxMetadataBytes:     64 << 10,
		MaxObjectBytes:       16 << 20,
		MaxCachedObjectBytes: 8,
		MaintenanceInterval:  time.Hour,
		MaintenanceTimeout:   10 * time.Second,
		PendingTTL:           time.Hour,
		MaintenanceBatchSize: 100,
		SessionIdleTTL:       8 * time.Hour,
		SessionAbsoluteTTL:   7 * 24 * time.Hour,
		ModuleData: core.ModuleDataLimits{
			MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20,
			MaxPageBytes: 3 << 20, MaxSelectors: 16, MaxSelectorValues: 64,
			MaxPageSize: 100, MaxBatchOperations: 64,
		},
	}
}

func websitePrivateS3Handler(w http.ResponseWriter, r *http.Request) {
	const xmlns = `http://s3.amazonaws.com/doc/2006-03-01/`
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case r.URL.Query().Has("publicAccessBlock"):
		_, _ = fmt.Fprintf(w, `<PublicAccessBlockConfiguration xmlns=%q><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`, xmlns)
	case r.URL.Query().Has("versioning"):
		_, _ = fmt.Fprintf(w, `<VersioningConfiguration xmlns=%q></VersioningConfiguration>`, xmlns)
	case r.URL.Query().Has("acl"):
		_, _ = fmt.Fprintf(w, `<AccessControlPolicy xmlns=%q><Owner><ID>test-owner</ID></Owner><AccessControlList><Grant><Grantee><ID>test-owner</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList></AccessControlPolicy>`, xmlns)
	case r.URL.Query().Has("policyStatus"):
		_, _ = fmt.Fprintf(w, `<PolicyStatus xmlns=%q><IsPublic>false</IsPublic></PolicyStatus>`, xmlns)
	default:
		http.Error(w, `<Error><Code>NotImplemented</Code></Error>`, http.StatusNotImplemented)
	}
}

// websiteProxyPrivateS3Checks supplies the immutable bucket-policy answers
// Core requires at startup while forwarding all object operations to the real
// disposable S3 stack. It is run-scoped and never changes bucket contents.
func websiteProxyPrivateS3Checks(t *testing.T, env *websitePersistenceEnv) {
	t.Helper()
	target, err := url.Parse(env.s3Endpoint)
	if err != nil || target.Scheme == "" || target.Host == "" {
		t.Fatalf("invalid disposable S3 endpoint: %q", env.s3Endpoint)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	privateCheck := func(r *http.Request) bool {
		if r.URL.Path != "/"+env.s3Bucket {
			return false
		}
		for _, key := range []string{"publicAccessBlock", "versioning", "acl", "policyStatus"} {
			if r.URL.Query().Has(key) {
				return true
			}
		}
		return false
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if privateCheck(r) {
			websitePrivateS3Handler(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	env.s3Endpoint = server.URL
	env.insecureS3HTTP = true
}

type websiteOwnedKeys struct {
	store contracts.RecordDataStore
	keys  map[contracts.RecordKey]struct{}
}

func (o *websiteOwnedKeys) add(keys ...contracts.RecordKey) {
	if o.keys == nil {
		o.keys = make(map[contracts.RecordKey]struct{}, len(keys))
	}
	for _, key := range keys {
		o.keys[key] = struct{}{}
	}
}

func (o *websiteOwnedKeys) deleteAll(t *testing.T) {
	t.Helper()
	if o == nil || o.store == nil || len(o.keys) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for key := range o.keys {
		record, err := o.store.Get(ctx, key)
		if errors.Is(err, core.ErrModuleDataNotFound) {
			continue
		}
		if err != nil {
			t.Errorf("read run-owned record for cleanup %+v: %v", key, err)
			continue
		}
		if err := o.store.Delete(ctx, key, record.Revision); err != nil && !errors.Is(err, core.ErrModuleDataNotFound) {
			t.Errorf("delete run-owned record %+v: %v", key, err)
		}
	}
}

func seedWebsiteRecord(t *testing.T, ctx context.Context, store contracts.RecordDataStore, key contracts.RecordKey, payload []byte, index map[string]any) {
	t.Helper()
	if _, err := store.Put(ctx, key, contracts.RecordValue{
		Payload: payload, PayloadVersion: 1, IndexData: mustWebsiteJSON(index),
	}, 0); err != nil {
		t.Fatalf("seed Website record %+v: %v", key, err)
	}
	stored, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("read seeded Website record %+v: %v", key, err)
	}
	// Core assigns a fresh opaque revision on every write, including the index
	// projection update, so the second write must use the next sequence value.
	index["revision"] = stored.Revision + 1
	if _, err := store.Put(ctx, key, contracts.RecordValue{
		Payload: stored.Payload, PayloadVersion: stored.PayloadVersion, IndexData: mustWebsiteJSON(index),
	}, stored.Revision); err != nil {
		t.Fatalf("set Website record index revision %+v: %v", key, err)
	}
}

func mustWebsiteJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func assertCoreCASConflict(t *testing.T, ctx context.Context, store contracts.RecordDataStore, key contracts.RecordKey) {
	t.Helper()
	current, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("read record for Core CAS check %+v: %v", key, err)
	}
	if current.Revision < 1 {
		t.Fatalf("record %+v has invalid revision %d", key, current.Revision)
	}
	value := contracts.RecordValue{
		Payload:        append(json.RawMessage(nil), current.Payload...),
		PayloadVersion: current.PayloadVersion,
		IndexData:      append(json.RawMessage(nil), current.IndexData...),
	}
	if _, err := store.Put(ctx, key, value, current.Revision-1); !errors.Is(err, core.ErrModuleDataConflict) {
		t.Fatalf("stale Core CAS on %+v = %v, want Core ErrModuleDataConflict", key, err)
	}
	after, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("reread record after failed Core CAS %+v: %v", key, err)
	}
	if after.Revision != current.Revision || string(after.Payload) != string(current.Payload) {
		t.Fatalf("failed Core CAS mutated record %+v: before revision=%d payload=%s, after revision=%d payload=%s",
			key, current.Revision, current.Payload, after.Revision, after.Payload)
	}
}

func websiteSnapshotKey(pageID, snapshotID string) contracts.RecordKey {
	return contracts.RecordKey{
		Namespace: websiterepo.SnapshotNamespace, ScopeType: "global", ScopeID: "global",
		RecordKey: "snapshot/" + strings.ToLower(pageID) + "/" + strings.ToLower(snapshotID),
	}
}

func websiteSnapshotReceiptKey(pageID, idempotencyKey string) contracts.RecordKey {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return contracts.RecordKey{
		Namespace: websiterepo.SnapshotNamespace, ScopeType: "global", ScopeID: "global",
		RecordKey: fmt.Sprintf("idempotency/%s/%s", strings.ToLower(pageID), hex.EncodeToString(digest[:])),
	}
}

func websiteSnapshotRegistryKey(pageID string) contracts.RecordKey {
	return contracts.RecordKey{
		Namespace: websiterepo.SnapshotNamespace, ScopeType: "global", ScopeID: "global",
		RecordKey: "registry/" + strings.ToLower(pageID),
	}
}
