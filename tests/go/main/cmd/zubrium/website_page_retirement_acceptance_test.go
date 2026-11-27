//go:build website_orchestration_acceptance

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	contentengine "github.com/Zubrium/cdn/engines/content"
	formsengine "github.com/Zubrium/cdn/engines/forms"
	routerengine "github.com/Zubrium/cdn/engines/router"
	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	routermodel "github.com/Zubrium/cdn/engines/router/features/routes/model"
	routesrepo "github.com/Zubrium/cdn/engines/router/features/routes/repository"
	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	"github.com/Zubrium/cdn/main/internal/composition"
	"github.com/Zubrium/cdn/main/internal/retirement"
	"github.com/Zubrium/cdn/pkg/contracts"
	routerpresent "github.com/Zubrium/cdn/presentation/router"
	"github.com/google/uuid"
)

const web022Locale = "en-US"

// TestWebsitePageRetirementAcceptance is the executable WEB-022 check. It
// runs a real durable workflow against disposable Core/PostgreSQL/S3, real
// Website page records and real Router CAS deletes. Only trusted composition
// selects the page and its registered route targets.
func TestWebsitePageRetirementAcceptance(t *testing.T) {
	t.Run("WEB-022", func(t *testing.T) {
		env := websitePersistenceEnvironment(t)
		websiteProxyPrivateS3Checks(t, &env)
		scenarioID := env.runID + "-" + uuid.NewString()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		bootstrap := websiteRunBootstrapSecret(env.runID)
		config := websiteRuntimeConfig(env, bootstrap)
		config.AccessGroups["features"] = core.AccessLevelWrite
		config.AccessGroups["router_routes"] = core.AccessLevelWrite
		branchID := env.runID + "-web022"
		runtime, secret, err := core.Start(ctx, config)
		if err != nil {
			t.Fatalf("start disposable Core: %v", err)
		}
		credential := secret
		if credential == "" {
			credential = bootstrap
		}
		t.Cleanup(func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer closeCancel()
			if err := runtime.CloseContext(closeCtx); err != nil {
				t.Errorf("close disposable Core: %v", err)
			}
		})

		for _, moduleID := range []string{
			websiteengine.RecordDescriptor().OwnerID,
			routerengine.RecordDescriptor().OwnerID,
			contentengine.RecordDescriptor().OwnerID,
			formsengine.RecordDescriptor().OwnerID,
			retirement.ModuleDataDescriptor().OwnerID,
		} {
			if err := runtime.Registry.Register(moduleID); err != nil {
				t.Fatalf("register %s data module: %v", moduleID, err)
			}
		}
		websiteData, err := websiteAcceptanceRecordStore(t, runtime, websiteengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Website module data: %v", err)
		}
		routerData, err := websiteAcceptanceRecordStore(t, runtime, routerengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Router module data: %v", err)
		}
		contentData, err := websiteAcceptanceRecordStore(t, runtime, contentengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Content module data: %v", err)
		}
		formsData, err := websiteAcceptanceRecordStore(t, runtime, formsengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Forms module data: %v", err)
		}
		retirementData, err := websiteAcceptanceRecordStore(t, runtime, retirement.ModuleDataDescriptor())
		if err != nil {
			t.Fatalf("register retirement module data: %v", err)
		}
		owned := &websiteOwnedKeys{store: websiteData}
		routerOwned := &websiteOwnedKeys{store: routerData}
		contentOwned := &websiteOwnedKeys{store: contentData}
		formsOwned := &websiteOwnedKeys{store: formsData}
		t.Cleanup(func() {
			owned.deleteAll(t)
			routerOwned.deleteAll(t)
			contentOwned.deleteAll(t)
			formsOwned.deleteAll(t)
		})

		identity := runtime.Identity()
		files := runtime.Files()
		serviceSession, _, err := identity.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate service identity: %v", err)
		}
		trackedFiles := &web021TrackedFiles{FilesCapability: files}
		// File cleanup is deliberately separate from record cleanup and is
		// checked for each file this run created.
		t.Cleanup(func() { trackedFiles.cleanup(t, serviceSession) })

		ports, err := composition.NewWebsiteCorePorts(runtime, credential, []string{"features"})
		if err != nil {
			t.Fatalf("build Website module DataBus ports: %v", err)
		}
		service, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
		}, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct Website service: %v", err)
		}
		websiteCtx, err := service.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate Website service: %v", err)
		}
		if _, err := service.GetContentModel(websiteCtx); err != nil {
			if _, err := service.SaveContentModel(websiteCtx, websiteapi.SaveContentModelRequest{
				ExpectedRevision: 0, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`),
			}); err != nil {
				t.Fatalf("seed scoped content model: %v", err)
			}
		}
		owned.add(contracts.RecordKey{Namespace: "website/content-models", ScopeType: "global", ScopeID: "global", RecordKey: "model"})

		page, err := service.CreatePage(websiteCtx, websiteapi.CreatePageRequest{
			Name: "WEB-022 retirement", IdempotencyKey: scenarioID + "-page",
		})
		if err != nil || page.ID == "" {
			t.Fatalf("create disposable page: %+v, %v", page, err)
		}
		owned.add(websiterepo.PageKey(page.ID))
		owned.add(websiterepo.PageCreateReceiptKey(scenarioID + "-page"))
		owned.add(contracts.RecordKey{Namespace: "website/snapshots", ScopeType: "global", ScopeID: "global", RecordKey: "registry/" + page.ID})
		owned.add(websiteSnapshotKey(page.ID, page.LatestSnapshotID))

		root, err := service.GetSnapshot(websiteCtx, page.LatestSnapshotID)
		if err != nil {
			t.Fatalf("read root snapshot: %v", err)
		}
		childConfig, err := json.Marshal(map[string]any{
			"format_version": 3,
			"page": map[string]any{
				"id": page.ID, "name": page.Name, "placements": []any{},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		child, err := service.CreateSnapshot(websiteCtx, websiteapi.CreateSnapshotRequest{
			PageID: page.ID, ParentID: root.ID, Name: "WEB-022 child",
			ExpectedRevision: root.Revision, ExpectedContentRevision: 0,
			IdempotencyKey: scenarioID + "-snapshot",
			Payload: websiteapi.SnapshotPayload{
				Config: childConfig, Metadata: json.RawMessage(`{"formatVersion":1,"siteName":"WEB-022"}`),
			},
		})
		if err != nil || child.ID == "" {
			t.Fatalf("create snapshot history: %+v, %v", child, err)
		}
		owned.add(websiteSnapshotKey(page.ID, child.ID))
		owned.add(contracts.RecordKey{Namespace: "website/snapshots", ScopeType: "global", ScopeID: "global",
			RecordKey: "idempotency/" + page.ID + "/" + web019SHA(scenarioID+"-snapshot")})

		// Content history, Forms resources and raw Core files predate the
		// retirement. None of them may be touched by route withdrawal.
		contentKey := contracts.RecordKey{Namespace: "content/workspaces", ScopeType: "page_snapshot", ScopeID: child.ID, RecordKey: "workspace"}
		contentOwned.add(contentKey)
		contentPayload := web020JSON(map[string]any{"record_type": "content_workspace", "page_id": page.ID, "snapshot_id": child.ID, "revision": 3})
		if _, err := contentData.Put(ctx, contentKey, contracts.RecordValue{
			Payload: contentPayload, PayloadVersion: 1,
			IndexData: web020JSON(map[string]any{"record_type": "content_workspace", "content_revision": 3}),
		}, 0); err != nil {
			t.Fatalf("seed Content history: %v", err)
		}
		// Forms namespaces use global/global scope; unique record keys keep this
		// acceptance run isolated without inventing unsupported scope types.
		formsKey := contracts.RecordKey{Namespace: "forms/schemas", ScopeType: "global", ScopeID: "global", RecordKey: "schema-id:retirement/" + scenarioID}
		submissionKey := contracts.RecordKey{Namespace: "forms/submissions", ScopeType: "global", ScopeID: "global", RecordKey: "retirement/" + scenarioID + "/submission/1"}
		formsOwned.add(formsKey, submissionKey)
		if _, err := formsData.Put(ctx, formsKey, contracts.RecordValue{Payload: web020JSON(map[string]any{
			"record_type": "form_schema", "form_key": "contact", "revision": 2,
		}), PayloadVersion: 1, IndexData: web020JSON(map[string]any{"record_type": "form_schema", "form_id": "contact", "revision": 2})}, 0); err != nil {
			t.Fatalf("seed Forms schema: %v", err)
		}
		if _, err := formsData.Put(ctx, submissionKey, contracts.RecordValue{Payload: web020JSON(map[string]any{
			"record_type": "form_submission", "state": "new",
		}), PayloadVersion: 1, IndexData: web020JSON(map[string]any{"form_id": "contact", "schema_version": 1, "state": "new"})}, 0); err != nil {
			t.Fatalf("seed Forms submission: %v", err)
		}
		preservedRevisions := make(map[contracts.RecordKey]int64)
		for _, key := range []contracts.RecordKey{contentKey, formsKey, submissionKey} {
			store := contentData
			if key.Namespace == "forms/schemas" || key.Namespace == "forms/submissions" {
				store = formsData
			}
			record, err := store.Get(ctx, key)
			if err != nil {
				t.Fatalf("read seeded preserved record %+v: %v", key, err)
			}
			preservedRevisions[key] = record.Revision
		}
		pageFileID, err := files.CreateFile(ctx, serviceSession, uuid.NewSHA1(uuid.NameSpaceURL, []byte(scenarioID+"/page-file")).String(),
			[]string{"features"}, map[string]string{"scenario": "WEB-022"}, "text/plain", 5, bytes.NewReader([]byte("keep!")))
		if err != nil {
			t.Fatalf("create page file: %v", err)
		}
		trackedFiles.track(pageFileID)
		beforeFile, err := files.StatFile(ctx, serviceSession, pageFileID)
		if err != nil {
			t.Fatalf("stat page file before retirement: %v", err)
		}

		routeStore, err := routesrepo.NewModuleDataRepository(routerData)
		if err != nil {
			t.Fatalf("construct Router repository: %v", err)
		}
		targetValidator, err := routerpresent.NewCoreTargetValidator(testCoreFilesAdapter{files: trackedFiles, session: serviceSession}, nil)
		if err != nil {
			t.Fatalf("construct Router target validator: %v", err)
		}
		coreAccess, err := composition.NewCoreAccess(runtime, credential, []string{"router_routes"})
		if err != nil {
			t.Fatalf("build Router module DataBus access client: %v", err)
		}
		routeService, err := routesapp.New(routeStore, coreAccess, coreAccess, targetValidator,
			routesapp.Config{BranchID: branchID, AccessGroup: "router_routes", DefaultPageSize: 50, MaxPageSize: 100})
		if err != nil {
			t.Fatalf("construct Router route service: %v", err)
		}
		routerAdapter, err := routerapi.NewAPIAdapter(routeService, nil, branchID)
		if err != nil {
			t.Fatalf("construct Router facade: %v", err)
		}
		routerCtx, err := routerAdapter.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate Router facade: %v", err)
		}
		router := &web022Router{SettingsAPI: routerAdapter, failDelete: map[string]bool{}}
		const targetCount = 3
		routeIDs := make([]string, 0, targetCount)
		for index := 0; index < targetCount; index++ {
			name := fmt.Sprintf("target-%d", index)
			target, err := routermodel.NewStaticArtifactTarget(web022Locale, []routermodel.LocaleArtifactTarget{{
				Locale: web022Locale, Revision: 1,
				ArtifactManifestFileID: web021StaticManifest(t, ctx, trackedFiles, serviceSession, scenarioID+"-"+name),
			}})
			if err != nil {
				t.Fatalf("build route target %s: %v", name, err)
			}
			key := scenarioID + "-route-" + name
			route, err := routerAdapter.CreateRoute(routerCtx, routerapi.CreateRouteRequest{
				BranchID: branchID, Host: fmt.Sprintf("web022-%d.example", index), Path: "/",
				Match: routermodel.MatchExact, Target: target, IdempotencyKey: key,
			})
			if err != nil || route.ID == "" {
				t.Fatalf("create route %s: %+v, %v", name, route, err)
			}
			routeIDs = append(routeIDs, route.ID)
			routerOwned.add(web021RouteKey(branchID, fmt.Sprintf("web022-%d.example", index), "/"))
			routerOwned.add(web021IdempotencyKey(branchID, key))
		}
		// The injected fault is on one trusted Router CAS withdrawal only.
		router.failDelete[routeIDs[1]] = true

		service2, err := retirement.NewService(retirement.Config{
			Store:   retirementData,
			Routes:  map[string][]string{page.ID: routeIDs},
			Website: service, WebsiteCredential: credential,
			Router: router, RouterCredential: credential,
		})
		if err != nil {
			t.Fatalf("construct retirement workflow: %v", err)
		}
		page, err = service.GetPage(websiteCtx, page.ID)
		if err != nil {
			t.Fatalf("read page before retirement: %v", err)
		}
		if page.LifecycleState != "active" || page.Revision < 1 {
			t.Fatalf("page must start active with a revision: %+v", page)
		}
		idempotency := scenarioID + "-retire"
		job, err := service2.RetirePage(ctx, contracts.RetirePageRequest{
			PageID: page.ID, IfMatch: strconv.Quote(strconv.FormatInt(page.Revision, 10)), IdempotencyKey: idempotency,
		})
		if err != nil {
			t.Fatalf("start retirement: %v", err)
		}
		if job.State != "retryable_failed" || len(job.Routes) != targetCount {
			t.Fatalf("partial retirement result: %+v", job)
		}
		for index, route := range job.Routes {
			want := "removed"
			if index == 1 {
				want = "retryable_failed"
			}
			if route.State != want {
				t.Fatalf("route %d state = %s, want %s: %+v", index, route.State, want, job.Routes)
			}
		}
		page, err = service.GetPage(websiteCtx, page.ID)
		if err != nil || page.LifecycleState != "retiring" || page.RetirementJobID != job.ID {
			t.Fatalf("page must stay retiring while a route remains: %+v, %v", page, err)
		}
		for index, routeID := range routeIDs {
			_, err := routerAdapter.GetRoute(routerCtx, routeID)
			if index == 1 {
				if errors.Is(err, routerapi.ErrNotFound) {
					t.Fatalf("failed route %s was withdrawn", routeID)
				}
				if err != nil {
					t.Fatalf("read retained route %s: %v", routeID, err)
				}
				continue
			}
			if !errors.Is(err, routerapi.ErrNotFound) {
				t.Fatalf("successful route %s was not withdrawn: %v", routeID, err)
			}
		}

		delete(router.failDelete, routeIDs[1])
		retried, err := service2.Retry(ctx, job.ID)
		if err != nil {
			t.Fatalf("retry retirement: %v", err)
		}
		if retried.ID != job.ID || retried.State != "succeeded" {
			t.Fatalf("retry result: %+v", retried)
		}
		for _, route := range retried.Routes {
			if route.State != "removed" {
				t.Fatalf("route was not withdrawn after retry: %+v", route)
			}
		}
		page, err = service.GetPage(websiteCtx, page.ID)
		if err != nil || page.LifecycleState != "retired" || page.RetirementJobID != job.ID {
			t.Fatalf("page must be retired only after every route: %+v, %v", page, err)
		}
		polled, err := service2.GetPageRetirement(ctx, contracts.PageRetirementQuery{PageID: page.ID, JobID: job.ID})
		if err != nil || polled.State != "succeeded" || polled.ID != job.ID {
			t.Fatalf("poll retirement: %+v, %v", polled, err)
		}
		repeated, err := service2.RetirePage(ctx, contracts.RetirePageRequest{
			PageID: page.ID, IfMatch: strconv.Quote(strconv.FormatInt(page.Revision, 10)), IdempotencyKey: idempotency,
		})
		if err != nil || repeated.ID != job.ID {
			t.Fatalf("identical retry must be idempotent: %+v, %v", repeated, err)
		}
		if _, err := service2.RetirePage(ctx, contracts.RetirePageRequest{
			PageID: page.ID, IfMatch: fmt.Sprintf("%d", page.Revision), IdempotencyKey: scenarioID + "-reuse",
		}); err == nil {
			t.Fatal("retired page ID was accepted for a new retirement job")
		}

		// A client cannot supply or reuse page_id: page creation always mints a
		// fresh server-owned identity and the retired identity stays retired.
		reuseAttempt, err := service.CreatePage(websiteCtx, websiteapi.CreatePageRequest{
			Name: "WEB-022 reuse attempt", IdempotencyKey: scenarioID + "-reuse-page",
		})
		if err != nil || reuseAttempt.ID == "" || reuseAttempt.ID == page.ID {
			t.Fatalf("page-ID reuse must be refused: %+v, %v", reuseAttempt, err)
		}
		owned.add(websiterepo.PageKey(reuseAttempt.ID))
		owned.add(websiterepo.PageCreateReceiptKey(scenarioID + "-reuse-page"))
		owned.add(contracts.RecordKey{Namespace: "website/snapshots", ScopeType: "global", ScopeID: "global", RecordKey: "registry/" + reuseAttempt.ID})
		owned.add(websiteSnapshotKey(reuseAttempt.ID, reuseAttempt.LatestSnapshotID))
		if after, err := service.GetPage(websiteCtx, page.ID); err != nil || after.LifecycleState != "retired" {
			t.Fatalf("retired identity was changed by reuse attempt: %+v, %v", after, err)
		}

		for _, key := range []contracts.RecordKey{contentKey, formsKey, submissionKey} {
			store := websiteData
			switch key.Namespace {
			case "content/workspaces":
				store = contentData
			case "forms/schemas", "forms/submissions":
				store = formsData
			}
			record, err := store.Get(ctx, key)
			if err != nil {
				t.Fatalf("retirement removed preserved record %+v: %v", key, err)
			}
			if want := preservedRevisions[key]; record.Revision != want {
				t.Fatalf("preserved record %+v revision = %d, want %d", key, record.Revision, want)
			}
		}
		if _, err := service.GetSnapshot(websiteCtx, root.ID); err != nil {
			t.Fatalf("root snapshot was removed: %v", err)
		}
		if _, err := service.GetSnapshot(websiteCtx, child.ID); err != nil {
			t.Fatalf("child snapshot was removed: %v", err)
		}
		afterFile, err := files.StatFile(ctx, serviceSession, pageFileID)
		if err != nil || afterFile.Revision != beforeFile.Revision {
			t.Fatalf("retirement modified Core file: before=%+v after=%+v err=%v", beforeFile, afterFile, err)
		}
	})
}

type web022Router struct {
	routerapi.SettingsAPI
	mu         sync.Mutex
	failDelete map[string]bool
}

func (r *web022Router) DeleteRoute(ctx context.Context, request routerapi.DeleteRouteRequest) error {
	r.mu.Lock()
	fail := r.failDelete[request.RouteID]
	delete(r.failDelete, request.RouteID)
	r.mu.Unlock()
	if fail {
		return routerapi.ErrUnavailable
	}
	return r.SettingsAPI.DeleteRoute(ctx, request)
}
