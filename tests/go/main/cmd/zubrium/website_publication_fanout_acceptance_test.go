//go:build full && website_orchestration_acceptance

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	routerengine "github.com/Zubrium/cdn/engines/router"
	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	routermodel "github.com/Zubrium/cdn/engines/router/features/routes/model"
	routesrepo "github.com/Zubrium/cdn/engines/router/features/routes/repository"
	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/main/internal/composition"
	publication "github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
	routerpresent "github.com/Zubrium/cdn/presentation/router"
	workerprerender "github.com/Zubrium/cdn/workers/prerender"
	"github.com/google/uuid"
)

const web021Locale = "en-US"

// TestWebsitePublicationFanoutAcceptance is the executable WEB-021 check. It
// uses disposable Core/PostgreSQL/S3 persistence and the real Router CAS while
// injecting build, conflict and stale-input faults at trusted composition
// boundaries. No visitor input selects a route, page, locale or operation.
func TestWebsitePublicationFanoutAcceptance(t *testing.T) {
	t.Run("WEB-021", func(t *testing.T) {
		env := websitePersistenceEnvironment(t)
		websiteProxyPrivateS3Checks(t, &env)
		scenarioID := env.runID + "-" + uuid.NewString()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		bootstrap := websiteRunBootstrapSecret(env.runID)
		config := websiteRuntimeConfig(env, bootstrap)
		config.AccessGroups["features"] = core.AccessLevelWrite
		config.AccessGroups["router_routes"] = core.AccessLevelWrite

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

		if err := runtime.Registry.Register(websiteengine.RecordDescriptor().OwnerID); err != nil {
			t.Fatalf("register Website module: %v", err)
		}
		if err := runtime.Registry.Register(routerengine.RecordDescriptor().OwnerID); err != nil {
			t.Fatalf("register Router module: %v", err)
		}
		if err := runtime.Registry.Register(publication.ModuleDataDescriptor().OwnerID); err != nil {
			t.Fatalf("register publication module: %v", err)
		}
		websiteData, err := websiteAcceptanceRecordStore(t, runtime, websiteengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Website module data: %v", err)
		}
		routerData, err := websiteAcceptanceRecordStore(t, runtime, routerengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Router module data: %v", err)
		}
		publicationData, err := websiteAcceptanceRecordStore(t, runtime, publication.ModuleDataDescriptor())
		if err != nil {
			t.Fatalf("register publication module data: %v", err)
		}
		owned := &websiteOwnedKeys{store: websiteData}
		routerOwned := &websiteOwnedKeys{store: routerData}
		t.Cleanup(func() {
			owned.deleteAll(t)
			routerOwned.deleteAll(t)
		})

		identity := runtime.Identity()
		files := runtime.Files()
		serviceSession, _, err := identity.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate service identity: %v", err)
		}
		trackedFiles := &web021TrackedFiles{FilesCapability: files}
		t.Cleanup(func() { trackedFiles.cleanup(t, serviceSession) })

		ports, err := composition.NewWebsiteCorePorts(runtime, credential, []string{"features"})
		if err != nil {
			t.Fatalf("build Website module DataBus ports: %v", err)
		}
		base, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
		}, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct bootstrap Website service: %v", err)
		}
		baseCtx, err := base.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate bootstrap Website service: %v", err)
		}
		model, err := base.GetContentModel(baseCtx)
		if err != nil {
			if model, err = base.SaveContentModel(baseCtx, websiteapi.SaveContentModelRequest{
				ExpectedRevision: 0, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`),
			}); err != nil {
				t.Fatalf("seed scoped content model: %v", err)
			}
		}
		// This scenario owns the shared global model record for this disposable run.
		owned.add(contracts.RecordKey{Namespace: "website/content-models", ScopeType: "global", ScopeID: "global", RecordKey: "model"})

		featureManifest := web020JSON(map[string]any{
			"formatVersion": 2, "key": "web021page", "name": "WEB-021 Page",
			"version": "1.0.0", "engineApi": "v1", "slots": []string{"main"},
			"arguments": map[string]any{}, "dataInputs": []any{},
			"runtime": map[string]any{"abi": featureruntime.ABI, "entry": "assets/app.js", "styles": []string{}},
		})
		featureKey := websiterepo.FeatureVersionKey("web021page", "1.0.0")
		owned.add(featureKey)
		featurePayload := web020JSON(map[string]any{
			"record_type": "feature_version", "feature_id": "web021page", "version": "1.0.0",
			"manifest": featureManifest, "files": map[string]string{"assets/app.js": uuid.NewString()},
			"digest": prerendercontract.HexDigest(featureManifest), "artifact_size": len(featureManifest),
			"state": "ready", "created_at": time.Now().UTC(),
		})
		if _, err := websiteData.Put(ctx, featureKey, contracts.RecordValue{
			Payload: featurePayload, PayloadVersion: 1,
			IndexData: web020JSON(map[string]any{"record_type": "feature_version", "feature_id": "web021page", "version": "1.0.0", "state": "ready"}),
		}, 0); err != nil {
			t.Fatalf("seed pinned feature version: %v", err)
		}

		const targetCount = 3
		targets := make([]publication.Target, 0, targetCount)
		snapshots := make(map[string]string, targetCount)
		for index := 0; index < targetCount; index++ {
			pageID, snapshotID := uuid.NewString(), uuid.NewString()
			pageConfig := web020JSON(map[string]any{
				"format_version": 3,
				"page": map[string]any{
					"id": pageID, "name": fmt.Sprintf("WEB-021-%d", index),
					"placements": []any{map[string]any{
						"id": "main-placement", "slot": "main",
						"build":     map[string]any{"featureKey": "web021page", "version": "1.0.0"},
						"arguments": map[string]any{}, "dataBindings": []any{},
					}},
				},
			})
			snapshotKey := websiteSnapshotKey(pageID, snapshotID)
			owned.add(snapshotKey)
			payload := web020JSON(map[string]any{
				"id": snapshotID, "page_id": pageID, "name": fmt.Sprintf("WEB-021-%d", index), "sequence": 1,
				"revision": 1, "content_revision": 2, "digest": prerendercontract.HexDigest(pageConfig),
				"feature_pins": map[string]string{"web021page": "1.0.0"}, "state": "ready",
				"created_at": time.Now().UTC(),
				"payload": map[string]any{
					"config": json.RawMessage(pageConfig), "metadata": json.RawMessage(`{"formatVersion":1,"siteName":"WEB-021"}`),
				},
			})
			seedWebsiteRecord(t, ctx, websiteData, snapshotKey, payload, map[string]any{
				"record_type": "snapshot", "snapshot_id": snapshotID, "page_id": pageID,
				"state": "ready", "parent_id": "",
			})
			snapshots[fmt.Sprintf("target-%d", index)] = snapshotID
		}

		catalog := &web021LocaleCatalog{revision: atomic.Int64{}}
		catalog.revision.Store(7)
		projection := &web021ContentProjection{
			publicationRevision: atomic.Int64{}, selectionDigest: atomic.Value{},
		}
		projection.publicationRevision.Store(4)
		projection.selectionDigest.Store(strings.Repeat("a", 64))
		imageHash := strings.SplitN(web020ImageDigest, "@", 2)[1]
		resolver, err := websiteapp.NewModuleBuildInputResolver(websiteData, projection, imageHash)
		if err != nil {
			t.Fatalf("construct trusted build input resolver: %v", err)
		}
		workflow := &web021Workflow{
			writer:           web020CoreArtifactWriter{files: trackedFiles, session: serviceSession},
			failSnapshots:    map[string]bool{},
			states:           map[uuid.UUID]*web021WorkflowState{},
			results:          map[uuid.UUID]json.RawMessage{},
			buildsBySnapshot: map[string]int{},
		}
		websiteService, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
			BuildWorkflow: workflow, LocaleCatalog: catalog, BuildInputResolver: resolver,
			BuildFiles: ports.Files, BuildOperationID: websiteworkflow.OperationID,
			BuildContractVersion: websiteworkflow.ContractVersion, PrerenderImageDigest: web020ImageDigest,
		}, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct configured Website service: %v", err)
		}

		names := make([]string, 0, targetCount)
		seedManifests := make(map[string]string, targetCount)
		for index := 0; index < targetCount; index++ {
			name := fmt.Sprintf("target-%d", index)
			names = append(names, name)
			seedManifests[name] = web021StaticManifest(t, ctx, trackedFiles, serviceSession, scenarioID+"-seed-"+name)
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
			routesapp.Config{AccessGroup: "router_routes", DefaultPageSize: 50, MaxPageSize: 100})
		if err != nil {
			t.Fatalf("construct Router route service: %v", err)
		}
		routerAdapter, err := routerapi.NewAPIAdapter(routeService, nil)
		if err != nil {
			t.Fatalf("construct Router facade: %v", err)
		}
		trackedRouter := &web021Router{Contract: routerAdapter, casFail: map[string]bool{}}
		routerCtx, err := trackedRouter.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate Router facade: %v", err)
		}

		for index, name := range names {
			routeTarget, err := routermodel.NewStaticArtifactTarget(web021Locale, []routermodel.LocaleArtifactTarget{{
				Locale: web021Locale, Revision: 1, ArtifactManifestFileID: seedManifests[name],
			}})
			if err != nil {
				t.Fatalf("build initial route target %s: %v", name, err)
			}
			idempotency := scenarioID + "-route-" + name
			route, err := trackedRouter.CreateRoute(routerCtx, routerapi.CreateRouteRequest{
				Host: fmt.Sprintf("web021-%d.example", index), Path: "/",
				Match: routermodel.MatchExact, Target: routeTarget, IdempotencyKey: idempotency,
			})
			if err != nil || route.ID == "" {
				t.Fatalf("create route %s: %+v, %v", name, route, err)
			}
			targets = append(targets, publication.Target{RouteID: route.ID, PageSnapshotID: snapshots[name], Locale: web021Locale})
			routerOwned.add(web021RouteKey(fmt.Sprintf("web021-%d.example", index), "/"))
			routerOwned.add(web021IdempotencyKey(idempotency))
		}

		fixture := composition.OrchestrationAcceptance{
			Runtime: runtime, Website: websiteService, Router: trackedRouter,
			LocaleCatalog: catalog, BuildInputs: resolver,
			Files: trackedFiles, Session: serviceSession, Credential: credential,
		}
		publicationService, bindings, cleanup, err := fixture.Publication(ctx, publicationData, targets)
		if err != nil {
			t.Fatalf("construct publication fan-out: %v", err)
		}
		t.Cleanup(cleanup)
		websiteStartOrchestration(t, ctx, runtime, scenarioID, bindings, "engine.publication")

		// Fault injection affects only trusted composition boundaries: one real
		// build result fails, one real Router CAS is rejected, one target succeeds.
		workflow.failSnapshots[targets[1].PageSnapshotID] = true
		trackedRouter.casFail[targets[2].RouteID] = true
		trigger := publication.Trigger{
			Locale: web021Locale, ContentPublicationRevision: 4, SystemModelRevision: model.ModelRevision, IdempotencyKey: scenarioID,
		}
		publish := func() (publication.Result, error) { return publicationService.Publish(ctx, trigger) }
		result := web021AwaitPublication(t, ctx, publish)
		if result.State != "failed" || len(result.Targets) != targetCount {
			t.Fatalf("partial publication result: %+v", result)
		}
		if result.Targets[0].State != "success" || result.Targets[1].State != "failed" || result.Targets[2].State != "failed" {
			t.Fatalf("per-target outcomes: %+v", result.Targets)
		}
		beforeRetry, err := trackedRouter.GetRoute(routerCtx, targets[0].RouteID)
		if err != nil {
			t.Fatalf("read successful route: %v", err)
		}
		slot, _ := beforeRetry.Target.LocaleTarget(web021Locale)
		if slot.Revision != 2 || slot.ArtifactManifestFileID == seedManifests[names[0]] {
			t.Fatalf("successful target was not activated: %+v", slot)
		}
		for index := 1; index < targetCount; index++ {
			route, err := trackedRouter.GetRoute(routerCtx, targets[index].RouteID)
			if err != nil {
				t.Fatalf("read failed route %d: %v", index, err)
			}
			failedSlot, _ := route.Target.LocaleTarget(web021Locale)
			if failedSlot.Revision != 1 || failedSlot.ArtifactManifestFileID != seedManifests[names[index]] {
				t.Fatalf("failed target %d changed its prior artifact: %+v", index, failedSlot)
			}
		}
		buildsAfterFirst := workflow.buildCount(snapshots[names[0]])

		workflow.mu.Lock()
		delete(workflow.failSnapshots, targets[1].PageSnapshotID)
		workflow.mu.Unlock()
		trackedRouter.mu.Lock()
		delete(trackedRouter.casFail, targets[2].RouteID)
		trackedRouter.mu.Unlock()
		if _, err := publicationService.Retry(ctx, result.RunID); err != nil {
			t.Fatalf("retry failed targets: %v", err)
		}
		retried := web021AwaitPublication(t, ctx, publish)
		if retried.State != "success" || retried.Targets[0].BuildID != result.Targets[0].BuildID ||
			retried.Targets[1].State != "success" || retried.Targets[2].State != "success" {
			t.Fatalf("retry result: %+v", retried)
		}
		if workflow.buildCount(snapshots[names[0]]) != buildsAfterFirst {
			t.Fatal("retry rebuilt a target that had already succeeded")
		}

		// Advance a trusted input after one artifact is ready but before its
		// Router CAS. The old result must be discarded and no target changed.
		staleBefore, err := trackedRouter.GetRoute(routerCtx, targets[0].RouteID)
		if err != nil {
			t.Fatalf("read route before stale probe: %v", err)
		}
		staleSlot, _ := staleBefore.Target.LocaleTarget(web021Locale)
		workflow.mu.Lock()
		workflow.staleSnapshotID = targets[0].PageSnapshotID
		workflow.staleAdvance = func() { catalog.revision.Add(1) }
		workflow.mu.Unlock()
		projection.publicationRevision.Store(5)
		trigger.ContentPublicationRevision = 5
		trigger.IdempotencyKey = scenarioID + "-stale"
		stale := web021AwaitPublication(t, ctx, publish)
		if stale.Targets[0].State != "failed" {
			t.Fatalf("stale result was not rejected: %+v", stale.Targets[0])
		}
		staleAfter, err := trackedRouter.GetRoute(routerCtx, targets[0].RouteID)
		if err != nil {
			t.Fatalf("read route after stale probe: %v", err)
		}
		currentSlot, _ := staleAfter.Target.LocaleTarget(web021Locale)
		if currentSlot.Revision != staleSlot.Revision || currentSlot.ArtifactManifestFileID != staleSlot.ArtifactManifestFileID {
			t.Fatalf("stale output changed the Router target: %+v -> %+v", staleSlot, currentSlot)
		}
		if catalog.revision.Load() != 8 {
			t.Fatalf("trusted input revision was not advanced: %d", catalog.revision.Load())
		}
	})
}

type web021LocaleCatalog struct{ revision atomic.Int64 }

func web021AwaitPublication(t *testing.T, ctx context.Context, read func() (publication.Result, error)) publication.Result {
	t.Helper()
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := read()
		if err != nil {
			t.Fatalf("read publication: %v", err)
		}
		if result.State == "success" || result.State == "failed" {
			return result
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("publication did not settle: %+v", result)
		case <-ticker.C:
		}
	}
}

func (c *web021LocaleCatalog) LocaleCatalogRevision(context.Context, string) (int64, error) {
	return c.revision.Load(), nil
}

type web021ContentProjection struct {
	publicationRevision atomic.Int64
	selectionDigest     atomic.Value
}

func (p *web021ContentProjection) ResolveContentBuildProjection(context.Context, string, string) (websiteapp.ContentBuildProjection, error) {
	return websiteapp.ContentBuildProjection{
		PublicationRevision: p.publicationRevision.Load(),
		SelectionDigest:     p.selectionDigest.Load().(string),
	}, nil
}

type web021WorkflowState struct {
	buildID     string
	locale      string
	snapshotID  string
	inputDigest string
	taskID      uuid.UUID
	manifest    workerprerender.Artifact
}

type web021Workflow struct {
	mu               sync.Mutex
	writer           web020CoreArtifactWriter
	failSnapshots    map[string]bool
	states           map[uuid.UUID]*web021WorkflowState
	results          map[uuid.UUID]json.RawMessage
	buildsBySnapshot map[string]int
	staleSnapshotID  string
	staleAdvance     func()
}

func (w *web021Workflow) Start(ctx context.Context, request contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	var payload struct {
		BuildID     uuid.UUID                      `json:"build_id"`
		Input       prerendercontract.TrustedInput `json:"input"`
		Page        prerendercontract.PagePlan     `json:"page"`
		InputDigest string                         `json:"input_digest"`
	}
	if err := json.Unmarshal(request.Payload, &payload); err != nil ||
		prerendercontract.ValidateTrustedInput(payload.Input) != nil || payload.BuildID == uuid.Nil {
		return contracts.WorkflowStartReceipt{}, errors.New("invalid trusted build payload")
	}
	plan := prerendercontract.Plan{
		Version: prerendercontract.PlanVersion, InputDigest: payload.InputDigest,
		Input: payload.Input, Page: payload.Page,
	}
	artifacts, err := prerendercontract.BuildArtifactSet(plan, []byte("<html><body>web021</body></html>"), prerendercontract.RenderProof{
		Backend: "docker-linux", Image: web020ImageDigest, SeccompMode: 2,
		CapEff: "0000000000000000", NoNewPrivs: true, ReadOnlyRootFS: true, NetworkDenied: true,
		CPUQuota: "100000", MemoryLimitBytes: 268435456, PIDsLimit: 64,
		BrowserVersion: "test-browser", Locale: payload.Input.Locale, DOMMarker: "web021",
	})
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	workflowID, taskID := uuid.New(), uuid.New()
	documentID, err := w.writer.WriteArtifact(ctx, workflowID.String()+"/index.html", "index.html",
		prerendercontract.HTMLMediaType, int64(len(artifacts.Document)), prerendercontract.HexDigest(artifacts.Document),
		bytes.NewReader(artifacts.Document))
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	artifacts.Manifest.Artifacts[0].FileID = documentID
	manifestBytes, err := json.Marshal(artifacts.Manifest)
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	manifestID, err := w.writer.WriteArtifact(ctx, workflowID.String()+"/manifest.json", "manifest.json",
		prerendercontract.ArtifactMediaType, int64(len(manifestBytes)), prerendercontract.HexDigest(manifestBytes),
		bytes.NewReader(manifestBytes))
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	workerResult, err := json.Marshal(workerprerender.Result{
		Format: prerendercontract.BuildResultFormat, InputDigest: payload.InputDigest,
		Manifest: workerprerender.Artifact{
			FileID: manifestID, Checksum: prerendercontract.HexDigest(manifestBytes),
			ContentType: prerendercontract.ArtifactMediaType, Size: int64(len(manifestBytes)),
		},
		Artifacts: []workerprerender.Artifact{{
			FileID: documentID, Checksum: prerendercontract.HexDigest(artifacts.Document),
			ContentType: prerendercontract.HTMLMediaType, Size: int64(len(artifacts.Document)),
		}},
	})
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	w.mu.Lock()
	w.states[workflowID] = &web021WorkflowState{
		buildID: payload.BuildID.String(), locale: payload.Input.Locale,
		snapshotID: payload.Input.SnapshotID, inputDigest: payload.InputDigest, taskID: taskID,
		manifest: workerprerender.Artifact{FileID: manifestID, Checksum: prerendercontract.HexDigest(manifestBytes),
			ContentType: prerendercontract.ArtifactMediaType, Size: int64(len(manifestBytes))},
	}
	w.results[taskID] = workerResult
	w.buildsBySnapshot[payload.Input.SnapshotID]++
	if w.staleSnapshotID == payload.Input.SnapshotID {
		w.staleAdvance()
		w.staleSnapshotID = ""
	}
	w.mu.Unlock()
	return contracts.WorkflowStartReceipt{WorkflowID: workflowID, RootTaskID: taskID, State: "succeeded"}, nil
}

func (w *web021Workflow) Get(_ context.Context, id uuid.UUID) (contracts.WorkflowState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	state, found := w.states[id]
	if !found {
		return contracts.WorkflowState{}, contracts.ErrWorkflowTaskMissing
	}
	root, err := json.Marshal(struct {
		Format          string `json:"format"`
		BuildID         string `json:"build_id"`
		InputDigest     string `json:"input_digest"`
		PrerenderTaskID string `json:"prerender_task_id"`
	}{"website.build-root/v2", state.buildID, state.inputDigest, state.taskID.String()})
	if err != nil {
		return contracts.WorkflowState{}, err
	}
	return contracts.WorkflowState{ID: id, State: "succeeded", RootTaskID: state.taskID, Result: root}, nil
}

func (w *web021Workflow) GetTaskResult(_ context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	state, found := w.states[workflowID]
	if !found || state.taskID != taskID {
		return contracts.WorkflowTaskResult{}, contracts.ErrWorkflowTaskMissing
	}
	if w.failSnapshots[state.snapshotID] {
		return contracts.WorkflowTaskResult{}, errors.New("injected build failure")
	}
	return contracts.WorkflowTaskResult{TaskID: taskID, WorkflowID: workflowID, State: "succeeded",
		Operation: &contracts.WorkflowOperationRef{ClassID: "worker.prerender", ID: "render-page", Version: 1},
		Result:    w.results[taskID]}, nil
}

func (w *web021Workflow) ResolveTaskID(_ context.Context, _ uuid.UUID, _ string) (uuid.UUID, error) {
	return uuid.Nil, contracts.ErrWorkflowTaskMissing
}

func (w *web021Workflow) buildCount(snapshotID string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buildsBySnapshot[snapshotID]
}

type web021Router struct {
	contracts.Contract
	mu      sync.Mutex
	casFail map[string]bool
}

func (r *web021Router) ReplaceLocaleTarget(ctx context.Context, request routerapi.ReplaceLocaleTargetRequest) (routerapi.Route, error) {
	r.mu.Lock()
	fail := r.casFail[request.RouteID]
	r.mu.Unlock()
	if fail {
		return routerapi.Route{}, routerapi.ErrConflict
	}
	return r.Contract.ReplaceLocaleTarget(ctx, request)
}

func web021RouteKey(host, path string) contracts.RecordKey {
	digest := sha256.Sum256([]byte(host + "\x00" + path))
	return contracts.RecordKey{Namespace: "router/routes", ScopeType: "global", ScopeID: "global", RecordKey: "route:" + hex.EncodeToString(digest[:])}
}

func web021IdempotencyKey(value string) contracts.RecordKey {
	digest := sha256.Sum256([]byte(value))
	return contracts.RecordKey{Namespace: "router/routes", ScopeType: "global", ScopeID: "global", RecordKey: "idem:" + hex.EncodeToString(digest[:])}
}

type web021TrackedFiles struct {
	core.FilesCapability
	mu  sync.Mutex
	ids []string
}

func (t *web021TrackedFiles) CreateFile(ctx context.Context, session core.Session, key string, groups []string,
	metadata map[string]string, contentType string, size int64, body io.Reader) (string, error) {
	id, err := t.FilesCapability.CreateFile(ctx, session, key, groups, metadata, contentType, size, body)
	t.track(id)
	return id, err
}

func (t *web021TrackedFiles) CreateFileVerified(ctx context.Context, session core.Session, key string, groups []string,
	metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	id, err := t.FilesCapability.CreateFileVerified(ctx, session, key, groups, metadata, contentType, size, checksum, body)
	t.track(id)
	return id, err
}

func (t *web021TrackedFiles) track(id string) {
	if id == "" {
		return
	}
	t.mu.Lock()
	t.ids = append(t.ids, id)
	t.mu.Unlock()
}

func (tracker *web021TrackedFiles) cleanup(t *testing.T, session core.Session) {
	t.Helper()
	tracker.mu.Lock()
	ids := append([]string(nil), tracker.ids...)
	tracker.mu.Unlock()
	for _, id := range ids {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		current, err := tracker.FilesCapability.StatFile(cleanupCtx, session, id)
		if err == nil {
			_ = tracker.FilesCapability.DeleteFile(cleanupCtx, session, id, current.Revision)
		}
		cancel()
	}
}

func web021StaticManifest(t *testing.T, ctx context.Context, files core.FilesCapability,
	session core.Session, key string) string {
	t.Helper()
	document := []byte(`<!doctype html><html lang="en-US"><body>web021</body></html>`)
	documentID, err := files.CreateFile(ctx, session,
		uuid.NewSHA1(uuid.NameSpaceURL, []byte(key+"/document")).String(), []string{"features"},
		map[string]string{"artifact_path": "index.html"}, "text/html; charset=utf-8",
		int64(len(document)), bytes.NewReader(document))
	if err != nil {
		t.Fatalf("create seed document: %v", err)
	}
	manifest := featureruntime.ArtifactManifest{
		Format: "static-page-artifact/v1",
		Document: featureruntime.ArtifactFile{
			FileID: documentID, ContentType: "text/html; charset=utf-8",
			SHA256: prerendercontract.HexDigest(document), Size: int64(len(document)),
		},
		Assets: map[string]featureruntime.ArtifactFile{},
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal seed manifest: %v", err)
	}
	manifest.Digest, err = featureruntime.ArtifactDigest(payload)
	if err != nil {
		t.Fatalf("seed manifest digest: %v", err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode seed manifest: %v", err)
	}
	id, err := files.CreateFile(ctx, session,
		uuid.NewSHA1(uuid.NameSpaceURL, []byte(key+"/manifest")).String(), []string{"features"},
		map[string]string{"artifact_path": "manifest.json"}, "application/json",
		int64(len(encoded)), bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("create seed manifest: %v", err)
	}
	return id
}
