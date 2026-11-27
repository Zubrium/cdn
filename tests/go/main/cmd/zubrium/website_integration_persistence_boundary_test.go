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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	"github.com/Zubrium/cdn/main/internal/composition"
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
		ports, err := composition.NewWebsiteCorePorts(runtime, credential, []string{"features"})
		if err != nil {
			t.Fatalf("build Website module DataBus ports: %v", err)
		}
		service, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup:            "features",
			CoordinatorAccessGroup: "features",
		}, store, ports.Authority, ports.Sessions)
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
		if _, err := store.Put(ctx, rootKey, value, current.Revision-1); !errors.Is(err, contracts.ErrRecordConflict) {
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
		if _, err := store.Get(ctx, foreignNamespace); !errors.Is(err, contracts.ErrRecordInvalid) {
			t.Fatalf("foreign namespace read = %v, want Core ErrModuleDataInvalid", err)
		}
		if _, err := store.Put(ctx, foreignNamespace, contracts.RecordValue{
			Payload: json.RawMessage(`{}`), PayloadVersion: 1, IndexData: json.RawMessage(`{}`),
		}, 0); !errors.Is(err, contracts.ErrRecordInvalid) {
			t.Fatalf("foreign namespace write = %v, want Core ErrModuleDataInvalid", err)
		}
		if _, _, _, err := store.List(ctx, foreignNamespace.Namespace, foreignNamespace.ScopeType, foreignNamespace.ScopeID, "", nil, 10); !errors.Is(err, contracts.ErrRecordInvalid) {
			t.Fatalf("foreign namespace list = %v, want Core ErrModuleDataInvalid", err)
		}
		foreignScope := contracts.RecordKey{
			Namespace: websiterepo.SnapshotNamespace, ScopeType: "job", ScopeID: "web018", RecordKey: "web018-foreign",
		}
		if _, err := store.Get(ctx, foreignScope); !errors.Is(err, contracts.ErrRecordInvalid) {
			t.Fatalf("undeclared scope read = %v, want Core ErrModuleDataInvalid", err)
		}
		if err := store.Batch(ctx, []contracts.RecordMutation{{
			Key: foreignNamespace, ExpectedRevision: 0,
			Value: &contracts.RecordValue{Payload: json.RawMessage(`{}`), PayloadVersion: 1, IndexData: json.RawMessage(`{}`)},
		}}); !errors.Is(err, contracts.ErrRecordInvalid) {
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
		if errors.Is(err, contracts.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			t.Errorf("read run-owned record for cleanup %+v: %v", key, err)
			continue
		}
		if err := o.store.Delete(ctx, key, record.Revision); err != nil && !errors.Is(err, contracts.ErrRecordNotFound) {
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
	if _, err := store.Put(ctx, key, value, current.Revision-1); !errors.Is(err, contracts.ErrRecordConflict) {
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
