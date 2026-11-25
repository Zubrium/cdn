//go:build website_acceptance

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
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/main/internal/composition"
	"github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
	"github.com/Zubrium/cdn/workers/prerender"
	"github.com/google/uuid"
)

const (
	web020ImageDigest    = "zenika/alpine-chrome@sha256:1727e672844c93a15b5f32991febcf67d2d4f23ac4dbc2dec2f310c582aa36a2"
	web020FeatureID      = "web020page"
	web020FeatureVersion = "1.0.0"
	web020Locale         = "en-US"
)

// TestWebsitePrerenderAcceptance is the executable WEB-020 check. It starts a
// disposable Core over run-scoped PostgreSQL and S3, preflights the exact
// Docker/Linux image used for rendering, runs the real Chromium render and
// verifies that Website activates only a fully verified, Core-owned artifact.
func TestWebsitePrerenderAcceptance(t *testing.T) {
	t.Run("WEB-020", func(t *testing.T) {
		env := websitePersistenceEnvironment(t)
		websiteProxyPrivateS3Checks(t, &env)
		scenarioID := env.runID + "-" + uuid.NewString()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()

		bootstrap := websiteRunBootstrapSecret(env.runID)
		config := websiteRuntimeConfig(env, bootstrap)
		config.AccessGroups["features"] = core.AccessLevelWrite
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

		if err := runtime.Registry.Register("coordinator"); err != nil {
			t.Fatalf("register disposable Coordinator: %v", err)
		}
		if err := runtime.Registry.Register(websiteengine.RecordDescriptor().OwnerID); err != nil {
			t.Fatalf("register Website module: %v", err)
		}
		if err := runtime.Registry.Register(prerender.DataModuleID); err != nil {
			t.Fatalf("register prerender worker module: %v", err)
		}
		websiteData, err := websiteAcceptanceRecordStore(t, runtime, websiteengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Website module data: %v", err)
		}
		owned := &websiteOwnedKeys{store: websiteData}
		t.Cleanup(func() { owned.deleteAll(t) })

		identity := runtime.Identity()
		serviceSession, _, err := identity.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate service identity: %v", err)
		}
		files := runtime.Files()
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
		if _, err := base.GetContentModel(baseCtx); err != nil {
			if _, err := base.SaveContentModel(baseCtx, websiteapi.SaveContentModelRequest{
				ExpectedRevision: 0, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`),
			}); err != nil {
				t.Fatalf("seed scoped content model: %v", err)
			}
		}

		featureManifest := web020JSON(map[string]any{
			"formatVersion": 2, "key": web020FeatureID, "name": "WEB-020 Page",
			"version": web020FeatureVersion, "engineApi": "v1", "slots": []string{"main"},
			"arguments": map[string]any{}, "dataInputs": []any{},
			"runtime": map[string]any{"abi": featureruntime.ABI, "entry": "assets/app.js", "styles": []string{}},
		})
		featureKey := websiterepo.FeatureVersionKey(web020FeatureID, web020FeatureVersion)
		owned.add(featureKey)
		featurePayload := web020JSON(map[string]any{
			"record_type": "feature_version", "feature_id": web020FeatureID,
			"version": web020FeatureVersion, "manifest": featureManifest,
			"files":  map[string]string{"assets/app.js": uuid.NewString()},
			"digest": prerendercontract.HexDigest(featureManifest), "artifact_size": len(featureManifest),
			"state": "ready", "created_at": time.Now().UTC(),
		})
		if _, err := websiteData.Put(ctx, featureKey, contracts.RecordValue{
			Payload: featurePayload, PayloadVersion: 1,
			IndexData: web020JSON(map[string]any{"record_type": "feature_version", "feature_id": web020FeatureID,
				"version": web020FeatureVersion, "state": "ready"}),
		}, 0); err != nil {
			t.Fatalf("seed pinned feature version: %v", err)
		}

		pageID, snapshotID := uuid.NewString(), uuid.NewString()
		pageConfig := web020JSON(map[string]any{
			"format_version": 3,
			"page": map[string]any{
				"id": pageID, "name": "WEB-020",
				"placements": []any{map[string]any{
					"id": "main-placement", "slot": "main",
					"build":     map[string]any{"featureKey": web020FeatureID, "version": web020FeatureVersion},
					"arguments": map[string]any{}, "dataBindings": []any{},
				}},
			},
		})
		snapshotKey := websiteSnapshotKey(pageID, snapshotID)
		owned.add(snapshotKey)
		snapshotPayload := web020JSON(map[string]any{
			"id": snapshotID, "page_id": pageID, "name": "WEB-020", "sequence": 1,
			"revision": 1, "content_revision": 2, "digest": prerendercontract.HexDigest(pageConfig),
			"feature_pins": map[string]string{web020FeatureID: web020FeatureVersion}, "state": "ready",
			"created_at": time.Now().UTC(),
			"payload": map[string]any{
				"config": json.RawMessage(pageConfig), "metadata": json.RawMessage(`{"formatVersion":1,"siteName":"WEB-020"}`),
			},
		})
		seedWebsiteRecord(t, ctx, websiteData, snapshotKey, snapshotPayload, map[string]any{
			"record_type": "snapshot", "snapshot_id": snapshotID, "page_id": pageID,
			"state": "ready", "parent_id": "",
		})
		snapshot, err := base.GetSnapshot(baseCtx, snapshotID)
		if err != nil || snapshot.State != "ready" || snapshot.Revision < 1 {
			t.Fatalf("read seeded snapshot: %+v, %v", snapshot, err)
		}

		// The full digest must change when any trusted locale-bound input
		// changes. These values are assembled only from Core-backed records and
		// composition-owned resolvers, never from the build request.
		catalog := &web020LocaleCatalog{revision: atomic.Int64{}}
		catalog.revision.Store(7)
		projection := &web020ContentProjection{
			publicationRevision: atomic.Int64{}, selectionDigest: atomic.Value{},
		}
		projection.publicationRevision.Store(4)
		projection.selectionDigest.Store(strings.Repeat("a", 64))
		imageHash := strings.SplitN(web020ImageDigest, "@", 2)[1]
		resolver, err := websiteapp.NewModuleBuildInputResolver(websiteData, projection, imageHash)
		if err != nil {
			t.Fatalf("construct trusted build input resolver: %v", err)
		}
		input, err := resolver.ResolveBuildInput(ctx, snapshot, web020Locale, 7)
		if err != nil {
			t.Fatalf("resolve trusted build input: %v", err)
		}
		digest, err := prerendercontract.InputDigest(input)
		if err != nil {
			t.Fatalf("compute trusted input digest: %v", err)
		}
		if _, err := resolver.ResolveBuildInput(ctx, snapshot, "fr-FR", 7); err != nil {
			t.Fatalf("resolve alternate locale input: %v", err)
		}
		alternateLocale, err := resolver.ResolveBuildInput(ctx, snapshot, "fr-FR", 7)
		if err != nil {
			t.Fatal(err)
		}
		alternateCatalog, err := resolver.ResolveBuildInput(ctx, snapshot, web020Locale, 8)
		if err != nil {
			t.Fatal(err)
		}
		projection.publicationRevision.Store(9)
		alternateContent, err := resolver.ResolveBuildInput(ctx, snapshot, web020Locale, 7)
		if err != nil {
			t.Fatal(err)
		}
		projection.publicationRevision.Store(4)
		projection.selectionDigest.Store(strings.Repeat("b", 64))
		alternateSelection, err := resolver.ResolveBuildInput(ctx, snapshot, web020Locale, 7)
		if err != nil {
			t.Fatal(err)
		}
		projection.selectionDigest.Store(strings.Repeat("a", 64))
		for name, variant := range map[string]prerendercontract.TrustedInput{
			"locale": alternateLocale, "locale catalog revision": alternateCatalog,
			"content publication revision": alternateContent, "locale selection digest": alternateSelection,
		} {
			variantDigest, err := prerendercontract.InputDigest(variant)
			if err != nil || variantDigest == digest {
				t.Fatalf("trusted digest did not bind %s: variant=%s base=%s err=%v", name, variantDigest, digest, err)
			}
		}
		for name, mutate := range map[string]func(*prerendercontract.TrustedInput){
			"snapshot revision":     func(v *prerendercontract.TrustedInput) { v.SnapshotRevision++ },
			"snapshot digest":       func(v *prerendercontract.TrustedInput) { v.SnapshotDigest = strings.Repeat("c", 64) },
			"system model revision": func(v *prerendercontract.TrustedInput) { v.SystemModelRevision++ },
			"system model digest":   func(v *prerendercontract.TrustedInput) { v.SystemModelDigest = strings.Repeat("d", 64) },
			"feature package":       func(v *prerendercontract.TrustedInput) { v.FeaturePackageDigest = strings.Repeat("e", 64) },
			"dependency lock":       func(v *prerendercontract.TrustedInput) { v.DependencyLockDigest = strings.Repeat("f", 64) },
		} {
			variant := input
			mutate(&variant)
			variantDigest, err := prerendercontract.InputDigest(variant)
			if err != nil || variantDigest == digest {
				t.Fatalf("trusted digest did not bind %s: variant=%s base=%s err=%v", name, variantDigest, digest, err)
			}
		}
		for name, mutate := range map[string]func(*prerendercontract.TrustedInput){
			"runtime ABI": func(v *prerendercontract.TrustedInput) { v.RuntimeABI = "other" },
			"runtime API": func(v *prerendercontract.TrustedInput) { v.RuntimeAPIVersion = "other" },
		} {
			variant := input
			mutate(&variant)
			if _, err := prerendercontract.InputDigest(variant); !errors.Is(err, prerendercontract.ErrInvalidInput) {
				t.Fatalf("untrusted %s was accepted: %v", name, err)
			}
		}

		// The fail-closed path must remain unavailable when a trusted resolver,
		// workflow, or artifact reader was not supplied by composition.
		unavailable, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
		}, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct fail-closed Website service: %v", err)
		}
		unavailableCtx, err := unavailable.Authenticate(ctx, credential)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := unavailable.BuildSnapshot(unavailableCtx, websiteapi.BuildSnapshotRequest{
			SnapshotID: snapshotID, Locale: web020Locale, IdempotencyKey: scenarioID + "-closed",
		}); !errors.Is(err, websiteapi.ErrUnavailable) {
			t.Fatalf("build without trusted capabilities = %v, want ErrUnavailable", err)
		}

		// Explicit host-side OS-policy probe. The production sandbox performs
		// the same checks from inside the exact image before every render.
		dockerBinary := os.Getenv("WEBSITE_TEST_DOCKER_BINARY")
		if dockerBinary == "" {
			dockerBinary = "docker"
		}
		web020AssertNetworkPolicy(t, ctx, dockerBinary)

		staging := t.TempDir()
		if err := os.Chmod(staging, 0o700); err != nil {
			t.Fatalf("secure run-owned prerender staging: %v", err)
		}
		sandboxConfig := prerender.DockerSandboxConfig{
			Image: web020ImageDigest, Platform: "linux/arm64", DockerBinary: dockerBinary,
			StagingRoot: staging, CPUs: "1", MemoryLimitBytes: 268435456, PIDsLimit: 64,
			MaxDocumentBytes: 1 << 20, Timeout: 90 * time.Second,
		}
		workerLimits := prerender.Limits{
			MaxPlanBytes: 1 << 20, MaxArtifacts: 4, MaxArtifactBytes: 2 << 20, MaxTotalBytes: 4 << 20,
		}
		worker, err := prerender.NewDockerWorker(ctx, prerender.Config{
			Sandbox: sandboxConfig, Limits: workerLimits,
			Files: web020CoreArtifactWriter{files: files, session: serviceSession},
		})
		if err != nil {
			t.Fatalf("construct verified prerender worker: %v", err)
		}
		if !worker.Isolated() {
			t.Fatal("prerender worker is not kernel-isolated")
		}
		state := worker.SandboxState()
		if state.Backend != "docker-linux" || state.Image != web020ImageDigest || state.SeccompMode < 2 ||
			!strings.EqualFold(state.CapEff, "0000000000000000") || !state.NoNewPrivs ||
			!state.ReadOnlyRootFS || !state.NetworkDenied || state.CPUQuota == "" ||
			state.MemoryLimitBytes != 268435456 || state.PIDsLimit != 64 || state.BrowserVersion == "" {
			t.Fatalf("in-container isolation proof incomplete: %+v", state)
		}
		t.Logf("WEB-020 in-container proof: seccomp=%d cap_eff=%s no_new_privs=%t cpu=%s memory=%d pids=%d rootfs=ro network=denied browser=%q",
			state.SeccompMode, state.CapEff, state.NoNewPrivs, state.CPUQuota, state.MemoryLimitBytes,
			state.PIDsLimit, state.BrowserVersion)

		plan, err := prerendercontract.MarshalPlan(prerendercontract.Plan{
			Version: prerendercontract.PlanVersion, InputDigest: digest, Input: input,
			Page: prerendercontract.PagePlan{Title: web020Locale, BodyHTML: "<html><body>web020</body></html>"},
		})
		if err != nil {
			t.Fatalf("marshal contract plan: %v", err)
		}
		web020AssertPlanRejections(t, plan, digest, workerLimits)

		cancelledCtx, cancelWork := context.WithCancel(ctx)
		cancelWork()
		if _, err := worker.Render(cancelledCtx, web020PlanRequest("cancel-task", "cancel-key", plan, digest)); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled render = %v, want context.Canceled", err)
		}
		timeoutConfig := sandboxConfig
		timeoutConfig.Timeout = time.Nanosecond
		timeoutWorker, err := prerender.NewDockerWorker(ctx, prerender.Config{
			Sandbox: timeoutConfig, Limits: workerLimits,
			Files: web020CoreArtifactWriter{files: files, session: serviceSession},
		})
		if err != nil {
			t.Fatalf("construct timeout prerender worker: %v", err)
		}
		if _, err := timeoutWorker.Render(ctx, web020PlanRequest("timeout-task", "timeout-key", plan, digest)); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timed-out render = %v, want context.DeadlineExceeded", err)
		}

		workflowHandler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: 3})
		if err != nil {
			t.Fatalf("construct Website build workflow handler: %v", err)
		}
		workflowKey := contracts.Key{ID: websiteworkflow.OperationID, Version: 1}
		prerenderKey := contracts.Key{ID: prerender.OperationID, Version: 1}
		bindings := map[contracts.Key]composition.TaskOperationBinding{
			workflowKey:  {TaskClass: websiteworkflow.ComponentID, Handler: workflowHandler.WorkerHandler},
			prerenderKey: {TaskClass: prerender.DataModuleID, Handler: worker.WorkerHandler},
		}
		dispatcher := websiteAcceptanceExecutor(t, bindings)
		fingerprint := sha256.Sum256([]byte("web020-" + scenarioID))
		if err := runtime.StartCoordinator(ctx, core.CoordinatorRuntimeConfig{
			Fingerprint: hex.EncodeToString(fingerprint[:]), InstanceLease: 30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
			RetryBase:         50 * time.Millisecond, RetryMaximum: 250 * time.Millisecond,
			MaxAttempts: 3, MaxWorkflowTasks: 32, MaxChildrenPerTask: 8,
			WorkflowRetention: 24 * time.Hour, ShutdownTimeout: 15 * time.Second,
			TaskClasses: []string{websiteworkflow.ComponentID, prerender.DataModuleID},
			Pools: websiteAcceptancePools(t, dispatcher, bindings,
				composition.TaskPoolSpec{ClassID: websiteworkflow.ComponentID, Concurrency: 2, LeaseDuration: 20 * time.Second, TaskTimeout: 90 * time.Second, PollInterval: 25 * time.Millisecond},
				composition.TaskPoolSpec{ClassID: prerender.DataModuleID, Concurrency: 2, LeaseDuration: 20 * time.Second, TaskTimeout: 90 * time.Second, PollInterval: 25 * time.Millisecond}),
		}); err != nil {
			t.Fatalf("start disposable Coordinator: %v", err)
		}
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer stopCancel()
			if err := runtime.StopCoordinator(stopCtx); err != nil {
				t.Errorf("stop disposable Coordinator: %v", err)
			}
		})
		if err := runtime.ActivateCoordinator(ctx); err != nil {
			t.Fatalf("activate disposable Coordinator: %v", err)
		}
		workflowClient, err := runtime.NewWorkflowClient(websiteworkflow.ComponentID)
		if err != nil {
			t.Fatalf("issue owner-scoped Website workflow client: %v", err)
		}
		workflowControl, err := runtime.NewWorkflowControl()
		if err != nil {
			t.Fatalf("issue workflow task-graph capability: %v", err)
		}
		tracking := &web020WorkflowClient{
			client:     composition.NewWebsiteWorkflowClient(runtime, websiteworkflow.ComponentID, credential, []string{"features"}),
			coreClient: workflowClient, control: workflowControl, maxPages: 4,
			started: map[uuid.UUID]web020StartedWorkflow{},
		}

		serviceConfig := websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
			BuildWorkflow: tracking, LocaleCatalog: catalog, BuildInputResolver: resolver,
			BuildFiles: ports.Files, BuildOperationID: websiteworkflow.OperationID,
			BuildContractVersion: websiteworkflow.ContractVersion, PrerenderImageDigest: web020ImageDigest,
		}
		service, err := websiteapp.NewService(serviceConfig, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct configured Website service: %v", err)
		}
		writeCtx, err := service.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate configured Website service: %v", err)
		}
		buildRequest := websiteapi.BuildSnapshotRequest{
			SnapshotID: snapshotID, Locale: web020Locale, IdempotencyKey: scenarioID + "-build",
		}
		job, err := service.BuildSnapshot(writeCtx, buildRequest)
		if errors.Is(err, websiteapi.ErrUnavailable) {
			t.Fatalf("WEB-020 RED: trusted prerender build capability is unavailable: %v", err)
		}
		if err != nil || job.ID == "" || job.InputDigest != digest {
			t.Fatalf("BuildSnapshot = %+v, %v; want input digest %s", job, err, digest)
		}
		owned.add(websiterepo.BuildKey(job.ID))
		ready := web020AwaitBuild(t, writeCtx, service, job.ID)
		if ready.State != "ready" || ready.ArtifactManifestFileID == "" || ready.ErrorCode != "" {
			t.Fatalf("build did not become ready: %+v", ready)
		}
		if ready.LocaleCatalogRevision != 7 || ready.Locale != web020Locale || ready.InputDigest != digest {
			t.Fatalf("ready build identity = %+v", ready)
		}

		manifestBytes := web020ReadCoreFile(t, ctx, files, serviceSession, ready.ArtifactManifestFileID, 1<<20)
		manifest, err := prerendercontract.VerifyManifest(manifestBytes, digest)
		if err != nil {
			t.Fatalf("verify Core-stored manifest: %v", err)
		}
		if manifest.Locale != web020Locale || manifest.Render.Locale != web020Locale ||
			manifest.Render.Image != web020ImageDigest || manifest.Render.SeccompMode < 2 ||
			!strings.EqualFold(manifest.Render.CapEff, "0000000000000000") || !manifest.Render.NoNewPrivs ||
			!manifest.Render.ReadOnlyRootFS || !manifest.Render.NetworkDenied || manifest.Render.CPUQuota == "" ||
			manifest.Render.MemoryLimitBytes != 268435456 || manifest.Render.PIDsLimit != 64 ||
			manifest.Render.BrowserVersion == "" || manifest.Render.DOMMarker == "" {
			t.Fatalf("artifact render proof is incomplete: %+v", manifest.Render)
		}
		if len(manifest.Artifacts) != 1 || manifest.Artifacts[0].Path != "index.html" ||
			manifest.Artifacts[0].MediaType != prerendercontract.HTMLMediaType {
			t.Fatalf("artifact set is not the single verified document: %+v", manifest.Artifacts)
		}
		documentID := manifest.Artifacts[0].FileID
		owned.add(websiterepo.BuildIdempotencyKey(strings.Join([]string{snapshot.ID, web020Locale, digest, buildRequest.IdempotencyKey}, "\x00")))
		owned.add(websiterepo.BuildIdempotencyKey("caller/" + buildRequest.IdempotencyKey))
		web020TrackFile(t, files, serviceSession, documentID)
		web020TrackFile(t, files, serviceSession, ready.ArtifactManifestFileID)
		document := web020ReadCoreFile(t, ctx, files, serviceSession, documentID, 1<<20)
		if !bytes.Contains(document, []byte(manifest.Render.DOMMarker)) ||
			!bytes.Contains(document, []byte(`data-rendered="`+web020Locale+`"`)) {
			t.Fatalf("artifact is not a real Chromium-rendered locale document: %s", document)
		}
		storedManifest, err := files.StatFile(ctx, serviceSession, ready.ArtifactManifestFileID)
		if err != nil || storedManifest.SHA256 != prerendercontract.HexDigest(manifestBytes) ||
			storedManifest.ContentType != prerendercontract.ArtifactMediaType ||
			len(storedManifest.Groups) != 1 || storedManifest.Groups[0] != "features" {
			t.Fatalf("manifest is not an immutable Core-owned artifact: %+v, %v", storedManifest, err)
		}
		storedDocument, err := files.StatFile(ctx, serviceSession, documentID)
		if err != nil || storedDocument.SHA256 != manifest.Artifacts[0].SHA256 || storedDocument.Size != manifest.Artifacts[0].Size {
			t.Fatalf("document checksum does not match manifest: %+v, %v", storedDocument, err)
		}

		// The child task identity is stable across retries and restart. Replaying
		// the exact Task.ID and emission key must return the same Core files.
		started, found := tracking.startedFor(ready.ID)
		if !found {
			t.Fatal("workflow start payload was not recorded")
		}
		rootTask := contracts.OperationTask{
			TaskID: started.receipt.RootTaskID.String(), WorkflowID: started.receipt.WorkflowID.String(),
			IdempotencyKey: "build/" + ready.ID, EmissionKey: "root", Payload: started.payload,
		}
		outcome, err := workflowHandler.WorkerHandler(ctx, rootTask)
		if err != nil || len(outcome.Children) != 1 ||
			outcome.Children[0].OperationID != prerender.OperationID ||
			outcome.Children[0].EmissionKey != "prerender:"+ready.ID {
			t.Fatalf("root workflow child = %+v, %v", outcome, err)
		}
		childTaskID, err := tracking.ResolveTaskID(ctx, started.receipt.WorkflowID, outcome.Children[0].EmissionKey)
		if err != nil || childTaskID == uuid.Nil {
			t.Fatalf("resolve child task by emission key: %v", err)
		}
		var childRequest prerender.Request
		if err := json.Unmarshal(outcome.Children[0].Payload, &childRequest); err != nil {
			t.Fatalf("decode child request: %v", err)
		}
		childRequest.TaskID = childTaskID.String()
		replayTask := contracts.OperationTask{
			TaskID: childTaskID.String(), WorkflowID: started.receipt.WorkflowID.String(),
			IdempotencyKey: "build/" + ready.ID, EmissionKey: outcome.Children[0].EmissionKey,
			Payload: outcome.Children[0].Payload,
		}
		replayOutcome, err := worker.WorkerHandler(ctx, replayTask)
		if err != nil {
			t.Fatalf("replay child task by stable identity: %v", err)
		}
		var replayResult prerender.Result
		if err := json.Unmarshal(replayOutcome.Result, &replayResult); err != nil ||
			replayResult.InputDigest != digest || replayResult.Manifest.FileID != ready.ArtifactManifestFileID {
			t.Fatalf("replayed artifact identity changed: %+v, %v", replayResult, err)
		}

		restarted, err := websiteapp.NewService(serviceConfig, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("restart Website service: %v", err)
		}
		restartedCtx, err := restarted.Authenticate(ctx, credential)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := restarted.BuildSnapshot(restartedCtx, buildRequest)
		if err != nil || replayed.ID != ready.ID || replayed.State != "ready" ||
			replayed.ArtifactManifestFileID != ready.ArtifactManifestFileID {
			t.Fatalf("restart replay = %+v, %v; want the same ready build", replayed, err)
		}

		// A stale child result cannot activate a different artifact, and the
		// previously ready artifact remains untouched and readable.
		staleServiceConfig := serviceConfig
		staleServiceConfig.BuildWorkflow = &web020StaleWorkflow{web020WorkflowClient: tracking, inputDigest: strings.Repeat("0", 64)}
		staleService, err := websiteapp.NewService(staleServiceConfig, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct stale-result Website service: %v", err)
		}
		staleCtx, err := staleService.Authenticate(ctx, credential)
		if err != nil {
			t.Fatal(err)
		}
		staleRequest := buildRequest
		staleRequest.IdempotencyKey = scenarioID + "-stale"
		staleJob, err := staleService.BuildSnapshot(staleCtx, staleRequest)
		if err != nil || staleJob.ID == "" {
			t.Fatalf("start stale-result build: %+v, %v", staleJob, err)
		}
		owned.add(websiterepo.BuildKey(staleJob.ID))
		staleReady := web020AwaitBuild(t, staleCtx, staleService, staleJob.ID)
		if staleReady.State != "failed" || staleReady.ErrorCode != "stale_input_digest" || staleReady.ArtifactManifestFileID != "" {
			t.Fatalf("stale child result was accepted: %+v", staleReady)
		}
		afterFailure, err := service.GetBuild(writeCtx, ready.ID)
		if err != nil || afterFailure.State != "ready" || afterFailure.ArtifactManifestFileID != ready.ArtifactManifestFileID {
			t.Fatalf("prior ready artifact was changed by failed build: %+v, %v", afterFailure, err)
		}
		if _, err := files.StatFile(ctx, serviceSession, ready.ArtifactManifestFileID); err != nil {
			t.Fatalf("prior manifest vanished after failed build: %v", err)
		}
	})
}

type web020LocaleCatalog struct{ revision atomic.Int64 }

func (c *web020LocaleCatalog) LocaleCatalogRevision(_ context.Context, locale string) (int64, error) {
	if locale == "" {
		return 0, websiteapi.ErrUnavailable
	}
	return c.revision.Load(), nil
}

type web020ContentProjection struct {
	publicationRevision atomic.Int64
	selectionDigest     atomic.Value
}

func (p *web020ContentProjection) ResolveContentBuildProjection(_ context.Context, pageSnapshotID, locale string) (websiteapp.ContentBuildProjection, error) {
	if pageSnapshotID == "" || locale == "" {
		return websiteapp.ContentBuildProjection{}, websiteapi.ErrUnavailable
	}
	digest, _ := p.selectionDigest.Load().(string)
	return websiteapp.ContentBuildProjection{PublicationRevision: p.publicationRevision.Load(), SelectionDigest: digest}, nil
}

type web020CoreArtifactWriter struct {
	files   core.FilesCapability
	session core.Session
}

func (w web020CoreArtifactWriter) WriteArtifact(ctx context.Context, requestKey, path, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	key := uuid.NewSHA1(uuid.NameSpaceURL, []byte("web020/"+requestKey)).String()
	return w.files.CreateFileVerified(ctx, w.session, key, []string{"features"},
		map[string]string{"artifact_path": path, "scenario": "WEB-020"}, contentType, size, checksum, body)
}

type web020StartedWorkflow struct {
	receipt contracts.WorkflowStartReceipt
	payload json.RawMessage
}

type web020WorkflowClient struct {
	client     websiteapp.BuildWorkflowClient
	coreClient *core.WorkflowClient
	control    *core.WorkflowControl
	maxPages   int
	started    map[uuid.UUID]web020StartedWorkflow
	startOrder []uuid.UUID
}

func (c *web020WorkflowClient) Start(ctx context.Context, request contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	receipt, err := c.client.Start(ctx, request)
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	var payload struct {
		BuildID uuid.UUID `json:"build_id"`
	}
	if json.Unmarshal(request.Payload, &payload) == nil && payload.BuildID != uuid.Nil && c.started != nil {
		if _, exists := c.started[payload.BuildID]; !exists {
			c.started[payload.BuildID] = web020StartedWorkflow{receipt: receipt, payload: append(json.RawMessage(nil), request.Payload...)}
			c.startOrder = append(c.startOrder, payload.BuildID)
		}
	}
	return receipt, nil
}

func (c *web020WorkflowClient) Get(ctx context.Context, id uuid.UUID) (contracts.WorkflowState, error) {
	return c.client.Get(ctx, id)
}

func (c *web020WorkflowClient) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	return c.client.GetTaskResult(ctx, workflowID, taskID)
}

func (c *web020WorkflowClient) ResolveTaskID(ctx context.Context, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	var before *core.TaskCursor
	for page := 0; page < c.maxPages; page++ {
		detail, err := c.control.Get(ctx, workflowID, core.WorkflowDetailFilter{Limit: 100, Before: before})
		if err != nil {
			return uuid.Nil, err
		}
		for _, task := range detail.TaskPage.Items {
			if task.EmissionKey == emissionKey && task.ID != uuid.Nil {
				return task.ID, nil
			}
		}
		if detail.TaskPage.Next == nil {
			break
		}
		cursor := *detail.TaskPage.Next
		if cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
			return uuid.Nil, contracts.ErrInvalidOperation
		}
		before = &cursor
	}
	return uuid.Nil, contracts.ErrWorkflowTaskMissing
}

func (c *web020WorkflowClient) startedFor(buildID string) (web020StartedWorkflow, bool) {
	parsed, err := uuid.Parse(buildID)
	if err != nil {
		return web020StartedWorkflow{}, false
	}
	started, found := c.started[parsed]
	return started, found
}

type web020StaleWorkflow struct {
	*web020WorkflowClient
	inputDigest string
}

func (c *web020StaleWorkflow) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	result, err := c.web020WorkflowClient.GetTaskResult(ctx, workflowID, taskID)
	if err != nil {
		return contracts.WorkflowTaskResult{}, err
	}
	result.Result = web020JSON(map[string]any{
		"format": "website.build-result/v2", "input_digest": c.inputDigest,
		"manifest": map[string]any{
			"file_id": uuid.NewString(), "checksum": strings.Repeat("a", 64),
			"content_type": prerendercontract.ArtifactMediaType, "size": 32,
		},
	})
	return result, nil
}

func web020AwaitBuild(t *testing.T, ctx context.Context, service *websiteapp.Service, buildID string) websiteapi.BuildJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		job, err := service.GetBuild(ctx, buildID)
		if err != nil {
			t.Fatalf("poll build %s: %v", buildID, err)
		}
		if job.State == "ready" || job.State == "failed" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("build %s did not reach a terminal state: %+v", buildID, job)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("build wait cancelled: %v", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func web020PlanRequest(taskID, idempotencyKey string, plan []byte, inputDigest string) prerender.Request {
	sum := sha256.Sum256(plan)
	return prerender.Request{
		TaskID: taskID, IdempotencyKey: idempotencyKey, Plan: plan,
		PlanDigest: hex.EncodeToString(sum[:]), InputDigest: inputDigest,
	}
}

func web020AssertPlanRejections(t *testing.T, plan []byte, inputDigest string, limits prerender.Limits) {
	t.Helper()
	if err := prerender.ValidatePlan(web020PlanRequest("task-1", "key-1", plan, inputDigest), limits); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	tampered := append([]byte(nil), plan...)
	tampered[len(tampered)-1] = ' '
	tamperedRequest := web020PlanRequest("task-1", "key-1", plan, inputDigest)
	tamperedRequest.Plan = tampered
	if err := prerender.ValidatePlan(tamperedRequest, limits); !errors.Is(err, prerender.ErrPlanDigestMismatch) {
		t.Fatalf("tampered plan accepted: %v", err)
	}
	if err := prerender.ValidatePlan(web020PlanRequest("task-1", "key-1", plan, strings.Repeat("0", 64)), limits); !errors.Is(err, prerender.ErrInvalidPlan) {
		t.Fatalf("stale input digest accepted: %v", err)
	}
	duplicated := string(plan)
	duplicated = duplicated[:len(duplicated)-1] + `,"version":"render-plan/v2"}`
	if err := prerender.ValidatePlan(web020PlanRequest("task-1", "key-1", []byte(duplicated), inputDigest), limits); !errors.Is(err, prerender.ErrInvalidPlan) {
		t.Fatalf("duplicate plan key accepted: %v", err)
	}
}

func web020AssertNetworkPolicy(t *testing.T, ctx context.Context, dockerBinary string) {
	t.Helper()
	script := `set -eu
for target in http://127.0.0.1:9/ http://127.0.0.1/ http://10.0.0.1/ http://169.254.169.254/latest/meta-data/ http://[::1]/ http://192.168.1.1/ http://172.16.0.1/; do
  if wget -q -T 1 -O /dev/null "$target" 2>/dev/null; then
    echo "forbidden target reachable: $target" >&2
    exit 1
  fi
done
printf 'network-policy-denied\n'
`
	name := "zubrium-web020-policy-" + uuid.NewString()
	args := []string{
		"run", "--rm", "--name", name, "--platform", "linux/arm64", "--network", "none", "--read-only",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=16m,mode=1777", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true", "--security-opt", "apparmor=docker-default",
		"--cpus", "1", "--memory", "268435456", "--pids-limit", "64",
		"--entrypoint", "/bin/sh", web020ImageDigest, "-c", script,
	}
	command := exec.CommandContext(ctx, dockerBinary, args...)
	command.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("explicit OS network policy probe failed: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "network-policy-denied") {
		t.Fatalf("OS network policy probe produced no denial proof: %s", output)
	}
}

func web020ReadCoreFile(t *testing.T, ctx context.Context, files core.FilesCapability, session core.Session, fileID string, limit int64) []byte {
	t.Helper()
	reader, _, err := files.ReadFile(ctx, session, fileID)
	if err != nil {
		t.Fatalf("read Core file %s: %v", fileID, err)
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil || len(body) == 0 || int64(len(body)) >= limit {
		t.Fatalf("bounded read of Core file %s: bytes=%d err=%v", fileID, len(body), err)
	}
	return body
}

func web020TrackFile(t *testing.T, files core.FilesCapability, session core.Session, fileID string) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		current, err := files.StatFile(cleanupCtx, session, fileID)
		if err == nil {
			_ = files.DeleteFile(cleanupCtx, session, fileID, current.Revision)
		}
	})
}

func web020JSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal WEB-020 fixture: %v", err))
	}
	return encoded
}
