//go:build website_acceptance

package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
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
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/google/uuid"
)

const (
	web019FeatureID        = "web019hero"
	web019FeatureVersion   = "1.0.0"
	web019PlacementID      = "hero-placement"
	web019FieldPath        = "/hero/image"
	web019ProcessorVersion = "ffmpeg-7.1-web019"
	web019ImageDigest      = "jrottenberg/ffmpeg@sha256:8ec1ee1f6a0fcd37c97725827b6b7832795c9596e3439b8da56d7700d61ae778"
	web019RenditionSmall   = "thumb"
	web019RenditionLarge   = "large"
)

// TestWebsiteMediaDerivativeAcceptance is the executable WEB-019 check. Every
// resource is run-scoped and disposable; a missing prerequisite fails the test.
func TestWebsiteMediaDerivativeAcceptance(t *testing.T) {
	t.Run("WEB-019", func(t *testing.T) {
		env := websitePersistenceEnvironment(t)
		scenarioID := env.runID + "-" + uuid.NewString()
		websiteProxyPrivateS3Checks(t, &env)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
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
		if err := runtime.Registry.Register(media.DataModuleID); err != nil {
			t.Fatalf("register media worker module: %v", err)
		}
		websiteData, err := websiteAcceptanceRecordStore(t, runtime, websiteengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Website module data: %v", err)
		}
		mediaData, err := websiteAcceptanceRecordStore(t, runtime, media.DataDescriptor())
		if err != nil {
			t.Fatalf("register media module data: %v", err)
		}
		owned := &websiteOwnedKeys{store: websiteData}
		t.Cleanup(func() { owned.deleteAll(t) })

		identity := runtime.Identity()
		serviceSession, _, err := identity.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate service identity: %v", err)
		}
		files := runtime.Files()

		// The DTO and the canonical worker contract must refuse client-supplied
		// processing choices before any durable state is touched.
		web019AssertClientOverridesRejected(t)
		manifest, profile, profileJSON := web019Manifest(t)
		web019AssertWorkerContractTamperingRejected(t, profile, profileJSON)

		// Without a verified sandbox capability the Website path fails closed.
		ports := composition.NewWebsiteCorePorts(runtime)
		unavailable, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
		}, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct fail-closed Website service: %v", err)
		}
		unavailableCtx, err := unavailable.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate fail-closed Website service: %v", err)
		}
		if _, err := unavailable.CreateMediaDerivative(unavailableCtx, websiteapi.CreateDerivativeRequest{}); !errors.Is(err, websiteapi.ErrUnavailable) {
			t.Fatalf("Website without media capability = %v, want ErrUnavailable", err)
		}
		web019AssertNoRunnerFallback(t, files, serviceSession, mediaData)

		staging := t.TempDir()
		if err := os.Chmod(staging, 0o700); err != nil {
			t.Fatalf("secure run-owned media staging: %v", err)
		}
		image := os.Getenv("WEBSITE_TEST_MEDIA_IMAGE")
		if image == "" {
			image = web019ImageDigest
		}
		dockerBinary := os.Getenv("WEBSITE_TEST_DOCKER_BINARY")
		if dockerBinary == "" {
			dockerBinary = "docker"
		}
		const (
			maxInputBytes  = int64(4 << 20)
			maxOutputBytes = int64(4 << 20)
		)
		sandboxConfig := media.DockerSandboxConfig{
			Image: image, DockerBinary: dockerBinary, StagingRoot: staging,
			CPUs: "1", MemoryLimitBytes: 268435456, PIDsLimit: 16,
			MaxInputBytes: maxInputBytes, MaxOutputBytes: maxOutputBytes, Timeout: 60 * time.Second,
		}
		sandbox, err := media.NewDockerSandbox(ctx, sandboxConfig)
		if err != nil {
			t.Fatalf("construct verified Docker/Linux media sandbox: %v", err)
		}
		web019AssertSandboxState(t, ctx, sandbox, sandboxConfig)
		badConfig := sandboxConfig
		badConfig.StagingRoot = "/"
		if _, err := media.NewDockerSandbox(ctx, badConfig); err == nil {
			t.Fatal("insecure staging root was accepted by the media sandbox")
		}

		runner := &web019Runner{inner: sandbox, blockUntilNano: atomic.Int64{}}
		runner.blockUntilNano.Store(time.Now().Add(2 * time.Second).UnixNano())
		mediaHandler, err := media.NewV2Handler(testCoreFilesAdapter{files: files, session: serviceSession}, mediaData, runner, 2, maxInputBytes+maxOutputBytes)
		if err != nil {
			t.Fatalf("construct v2 media worker: %v", err)
		}
		workflowHandler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: 3})
		if err != nil {
			t.Fatalf("construct Website media workflow handler: %v", err)
		}
		workflowKey := contracts.Key{ID: websiteworkflow.MediaOperationID, Version: 1}
		mediaKey := contracts.Key{ID: media.OperationID, Version: 2}
		bindings := map[contracts.Key]composition.TaskOperationBinding{
			workflowKey: {TaskClass: websiteworkflow.ComponentID, Handler: workflowHandler.MediaWorkerHandler},
			mediaKey:    {TaskClass: media.DataModuleID, Handler: mediaHandler.WorkerHandler},
		}
		dispatcher := websiteAcceptanceExecutor(t, bindings)
		fingerprint := sha256.Sum256([]byte("web019-" + scenarioID))
		if err := runtime.StartCoordinator(ctx, core.CoordinatorRuntimeConfig{
			Fingerprint: hex.EncodeToString(fingerprint[:]), InstanceLease: 30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
			RetryBase:         50 * time.Millisecond, RetryMaximum: 250 * time.Millisecond,
			MaxAttempts: 3, MaxWorkflowTasks: 32, MaxChildrenPerTask: 8,
			WorkflowRetention: 24 * time.Hour, ShutdownTimeout: 15 * time.Second,
			TaskClasses: []string{websiteworkflow.ComponentID, media.DataModuleID},
			Pools: websiteAcceptancePools(t, dispatcher, bindings,
				composition.TaskPoolSpec{ClassID: websiteworkflow.ComponentID, Concurrency: 2, LeaseDuration: 20 * time.Second, TaskTimeout: 90 * time.Second, PollInterval: 25 * time.Millisecond},
				composition.TaskPoolSpec{ClassID: media.DataModuleID, Concurrency: 2, LeaseDuration: 20 * time.Second, TaskTimeout: 90 * time.Second, PollInterval: 25 * time.Millisecond}),
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
		probeReceipt, err := workflowClient.Start(ctx, core.WorkflowStartRequest{
			OperationID: websiteworkflow.MediaOperationID, ContractVersion: websiteworkflow.MediaContractVersion,
			IdempotencyKey: scenarioID + "-workflow-probe", Payload: []byte(`{"probe":true}`),
		})
		if err != nil {
			t.Fatalf("Website media workflow operation is unavailable in the disposable catalog: %v", err)
		}
		if probeReceipt.WorkflowID == uuid.Nil || probeReceipt.RootTaskID == uuid.Nil {
			t.Fatalf("workflow probe returned an incomplete receipt: %+v", probeReceipt)
		}
		workflowControl, err := runtime.NewWorkflowControl()
		if err != nil {
			t.Fatalf("issue workflow task-graph capability: %v", err)
		}
		workflow := &web019WorkflowClient{
			client:     composition.NewWebsiteWorkflowClient(runtime, websiteworkflow.ComponentID),
			coreClient: workflowClient, control: workflowControl, maxPages: 4,
		}

		serviceConfig := websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
			MediaWorkflow: workflow, MediaFiles: ports.Files,
			MediaOperationID: websiteworkflow.MediaOperationID, MediaContractVersion: websiteworkflow.MediaContractVersion,
			MediaProcessorVersion: web019ProcessorVersion,
			MediaSandbox:          web019SandboxProbe{state: web019WebsiteSandboxState(sandbox.SandboxState())},
		}
		service, err := websiteapp.NewService(serviceConfig, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct configured Website service: %v", err)
		}
		writeCtx, err := service.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate Website service: %v", err)
		}

		// Seed a ready feature version and a ready snapshot that pins it. The
		// x-media declaration is reachable only through that exact pin.
		featureKey := websiterepo.FeatureVersionKey(web019FeatureID, web019FeatureVersion)
		owned.add(featureKey)
		featureRecord := map[string]any{
			"record_type": "feature_version", "feature_id": web019FeatureID, "version": web019FeatureVersion,
			"manifest": manifest, "files": map[string]string{"assets/app.js": uuid.NewString()},
			"digest": web019Digest(manifest), "artifact_size": len(manifest),
			"state": "ready", "created_at": time.Now().UTC(),
		}
		payload, err := json.Marshal(featureRecord)
		if err != nil {
			t.Fatalf("marshal feature fixture: %v", err)
		}
		if _, err := websiteData.Put(ctx, featureKey, contracts.RecordValue{
			Payload: payload, PayloadVersion: 1,
			IndexData: web019JSON(map[string]any{"record_type": "feature_version", "feature_id": web019FeatureID,
				"version": web019FeatureVersion, "state": "ready"}),
		}, 0); err != nil {
			t.Fatalf("seed pinned feature version: %v", err)
		}

		pageID := uuid.NewString()
		snapshotID := uuid.NewString()
		configJSON := web019JSON(map[string]any{
			"format_version": 3,
			"page": map[string]any{
				"id": pageID, "name": "WEB-019",
				"placements": []any{map[string]any{
					"id": web019PlacementID, "slot": "main",
					"build":     map[string]any{"featureKey": web019FeatureID, "version": web019FeatureVersion},
					"arguments": map[string]any{}, "dataBindings": []any{},
				}},
			},
		})
		snapshotKey := websiteSnapshotKey(pageID, snapshotID)
		owned.add(snapshotKey)
		snapshotRecord := map[string]any{
			"id": snapshotID, "page_id": pageID, "name": "WEB-019", "sequence": 1,
			"feature_pins": map[string]string{web019FeatureID: web019FeatureVersion}, "state": "ready",
			"payload": map[string]any{"config": json.RawMessage(configJSON), "metadata": json.RawMessage(`{"formatVersion":1,"siteName":"WEB-019"}`)},
		}
		snapshotPayload, err := json.Marshal(snapshotRecord)
		if err != nil {
			t.Fatalf("marshal snapshot fixture: %v", err)
		}
		seedWebsiteRecord(t, ctx, websiteData, snapshotKey, snapshotPayload, map[string]any{
			"record_type": "snapshot", "snapshot_id": snapshotID, "page_id": pageID,
			"state": "ready", "parent_id": "",
		})
		snapshotAfterSeed, err := websiteData.Get(ctx, snapshotKey)
		if err != nil {
			t.Fatalf("read seeded snapshot: %v", err)
		}
		snapshotBefore := append(json.RawMessage(nil), snapshotAfterSeed.Payload...)

		source := web019CreateFile(t, ctx, files, serviceSession, scenarioID+"-source", web019PNG(200), "image/png")
		setName := "web019-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
		sourceSet, err := files.CreateManagedFileSet(ctx, serviceSession, setName, "media", "features")
		if err != nil {
			t.Fatalf("create run-owned FileSet: %v", err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			current, err := files.GetManagedFileSet(cleanupCtx, serviceSession, sourceSet.ID)
			if err == nil {
				_ = files.DeleteManagedFileSet(cleanupCtx, serviceSession, sourceSet.ID, current.Revision)
			}
		})
		if _, err := files.AddFileToManagedSet(ctx, serviceSession, sourceSet.ID, source, sourceSet.Revision); err != nil {
			t.Fatalf("add source to run-owned FileSet: %v", err)
		}
		sourceBefore, err := files.StatFile(ctx, serviceSession, source)
		if err != nil {
			t.Fatalf("read source before media jobs: %v", err)
		}

		baseRequest := websiteapi.CreateDerivativeRequest{
			SnapshotID: snapshotID, PlacementID: web019PlacementID,
			FieldPath: web019FieldPath, SourceFileID: source,
		}
		web019PreflightMediaInputs(t, ctx, writeCtx, service, websiteData, files, serviceSession,
			snapshotID, web019FeatureID, web019FeatureVersion, web019FieldPath, source,
			web019ProcessorVersion, scenarioID+"-cancel-key")
		expectedDigest, err := mediaprofile.Digest(profile, web019ProcessorVersion)
		if err != nil {
			t.Fatalf("compute expected profile digest: %v", err)
		}

		// Cancellation must not publish outputs or change the pinned field.
		cancelRequest := baseRequest
		cancelRequest.IdempotencyKey = scenarioID + "-cancel-key"
		cancelJob, err := service.CreateMediaDerivative(writeCtx, cancelRequest)
		if err != nil || cancelJob.ID == "" {
			t.Fatalf("CreateMediaDerivative(cancel) = %+v, %v", cancelJob, err)
		}
		cancelWorkflowID, err := web019JobWorkflowID(ctx, websiteData, cancelJob.ID)
		if err != nil {
			t.Fatalf("read cancel workflow identity: %v", err)
		}
		if err := workflow.Cancel(ctx, cancelWorkflowID); err != nil {
			t.Fatalf("cancel media workflow: %v", err)
		}
		cancelled := web019AwaitMediaState(t, ctx, service, writeCtx, cancelJob.ID, "cancelled")
		if len(cancelled.Outputs) != 0 {
			t.Fatalf("cancelled job published outputs: %+v", cancelled.Outputs)
		}
		web019AssertPinnedValueUnchanged(t, ctx, websiteData, snapshotKey, snapshotBefore, files, serviceSession, source, sourceBefore)
		runner.blockUntilNano.Store(0)

		// A source whose declared MIME type is not accepted by the manifest must
		// fail before any derivative becomes visible.
		invalidSource := web019CreateFile(t, ctx, files, serviceSession, scenarioID+"-mime", web019PNG(40), "application/octet-stream")
		invalidRequest := baseRequest
		invalidRequest.SourceFileID = invalidSource
		invalidRequest.IdempotencyKey = scenarioID + "-mime-key"
		invalidJob, err := service.CreateMediaDerivative(writeCtx, invalidRequest)
		if err != nil || invalidJob.ID == "" {
			t.Fatalf("CreateMediaDerivative(invalid MIME) = %+v, %v", invalidJob, err)
		}
		rejected := web019AwaitMediaState(t, ctx, service, writeCtx, invalidJob.ID, "retryable_failed")
		if len(rejected.Outputs) != 0 || rejected.ErrorCode == "" {
			t.Fatalf("invalid MIME job published outputs or had no error: %+v", rejected)
		}
		setFiles, _, err := files.ListFileSet(ctx, serviceSession, sourceSet.ID, "", 100)
		if err != nil || len(setFiles) != 1 || setFiles[0].ID != source {
			t.Fatalf("failed media jobs leaked FileSet outputs: count=%d err=%v", len(setFiles), err)
		}
		web019AssertPinnedValueUnchanged(t, ctx, websiteData, snapshotKey, snapshotBefore, files, serviceSession, source, sourceBefore)

		// The positive path uses the real Docker/Linux sandbox and a transient
		// runner failure on every rendition. Retry must still publish one file
		// per rendition and no partial output before the job is ready. A fresh
		// source gives this job a distinct content fingerprint from the cancelled
		// and rejected jobs, so an idempotent receipt cannot replay them.
		validSource := web019CreateFile(t, ctx, files, serviceSession, scenarioID+"-valid", web019PNG(180), "image/png")
		validSet, err := files.GetManagedFileSet(ctx, serviceSession, sourceSet.ID)
		if err != nil {
			t.Fatalf("read run-owned FileSet before valid source: %v", err)
		}
		if _, err := files.AddFileToManagedSet(ctx, serviceSession, sourceSet.ID, validSource, validSet.Revision); err != nil {
			t.Fatalf("add valid source to run-owned FileSet: %v", err)
		}
		validSourceBefore, err := files.StatFile(ctx, serviceSession, validSource)
		if err != nil {
			t.Fatalf("read valid source before media jobs: %v", err)
		}
		runner.failOnce.Store(web019RenditionSmall, struct{}{})
		runner.failOnce.Store(web019RenditionLarge, struct{}{})
		validRequest := baseRequest
		validRequest.SourceFileID = validSource
		validRequest.IdempotencyKey = scenarioID + "-valid-key"
		validJob, err := service.CreateMediaDerivative(writeCtx, validRequest)
		if err != nil || validJob.ID == "" {
			t.Fatalf("CreateMediaDerivative(valid) = %+v, %v", validJob, err)
		}
		if validJob.ProfileDigest != expectedDigest || validJob.ProcessorVersion != web019ProcessorVersion {
			t.Fatalf("job identity = digest %s processor %s, want %s / %s", validJob.ProfileDigest,
				validJob.ProcessorVersion, expectedDigest, web019ProcessorVersion)
		}
		ready := web019AwaitReady(t, ctx, service, writeCtx, validJob.ID)
		if runner.failures.Load() < 2 {
			t.Fatalf("expected transient runner failures to be retried, count=%d", runner.failures.Load())
		}
		if len(ready.Outputs) != 2 {
			t.Fatalf("ready job has %d outputs, want 2: %+v", len(ready.Outputs), ready.Outputs)
		}
		for _, name := range []string{web019RenditionSmall, web019RenditionLarge} {
			outputID, ok := ready.Outputs[name]
			if !ok || outputID == "" {
				t.Fatalf("ready job is missing rendition %q: %+v", name, ready.Outputs)
			}
			web019TrackFile(t, files, serviceSession, outputID)
			output, err := files.StatFile(ctx, serviceSession, outputID)
			if err != nil {
				t.Fatalf("stat rendition %s: %v", name, err)
			}
			if output.ContentType != "image/webp" || output.Size <= 0 || len(output.SHA256) != 64 {
				t.Fatalf("rendition %s has invalid Core metadata: %+v", name, output)
			}
			if len(output.Groups) != 1 || output.Groups[0] != "features" {
				t.Fatalf("rendition %s groups = %v, want [features]", name, output.Groups)
			}
			body, _, err := files.ReadFile(ctx, serviceSession, outputID)
			if err != nil {
				t.Fatalf("read rendition %s: %v", name, err)
			}
			signature := make([]byte, 12)
			_, readErr := io.ReadFull(body, signature)
			_ = body.Close()
			if readErr != nil || !bytes.Equal(signature[:4], []byte("RIFF")) || !bytes.Equal(signature[8:12], []byte("WEBP")) {
				t.Fatalf("rendition %s is not a WebP image: sig=%q err=%v", name, signature, readErr)
			}
		}
		setFiles, _, err = files.ListFileSet(ctx, serviceSession, sourceSet.ID, "", 100)
		if err != nil {
			t.Fatalf("list FileSet after ready job: %v", err)
		}
		members := map[string]bool{}
		for _, file := range setFiles {
			members[file.ID] = true
		}
		for name, outputID := range ready.Outputs {
			if !members[outputID] {
				t.Fatalf("rendition %s did not inherit source FileSet membership", name)
			}
		}
		web019AssertPinnedValueUnchanged(t, ctx, websiteData, snapshotKey, snapshotBefore, files, serviceSession, validSource, validSourceBefore)

		// A fresh service over the same durable store sees the same job and the
		// same idempotent fingerprint receipt.
		restarted, err := websiteapp.NewService(serviceConfig, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("restart Website service: %v", err)
		}
		restartedCtx, err := restarted.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate restarted Website service: %v", err)
		}
		replayed, err := restarted.CreateMediaDerivative(restartedCtx, validRequest)
		if err != nil || replayed.ID != validJob.ID || replayed.State != "ready" || len(replayed.Outputs) != 2 {
			t.Fatalf("restart replay = %+v, %v; want same ready job", replayed, err)
		}

		// Same caller key with a different source conflicts; a different key is
		// a separate job. Neither changes the pinned snapshot or original file.
		changedSource := web019CreateFile(t, ctx, files, serviceSession, scenarioID+"-changed", web019PNG(90), "image/png")
		conflictRequest := validRequest
		conflictRequest.SourceFileID = changedSource
		if _, err := service.CreateMediaDerivative(writeCtx, conflictRequest); !errors.Is(err, websiteapi.ErrUnavailable) &&
			!errors.Is(err, websiteapi.ErrConflict) {
			t.Fatalf("same key with changed source = %v, want idempotency conflict", err)
		}
		splitRequest := conflictRequest
		splitRequest.IdempotencyKey = scenarioID + "-split-key"
		splitJob, err := service.CreateMediaDerivative(writeCtx, splitRequest)
		if err != nil || splitJob.ID == validJob.ID {
			t.Fatalf("changed source with new key = %+v, %v; want a separate job", splitJob, err)
		}
		splitReady := web019AwaitReady(t, ctx, service, writeCtx, splitJob.ID)
		for _, outputID := range splitReady.Outputs {
			web019TrackFile(t, files, serviceSession, outputID)
		}
		web019AssertPinnedValueUnchanged(t, ctx, websiteData, snapshotKey, snapshotBefore, files, serviceSession, source, sourceBefore)

		web019AssertImmutableDerivative(t, ctx, mediaHandler, files, serviceSession, source, snapshotID, profile, expectedDigest)
		web019AssertWorkerRejectsBadMetadata(t, ctx, mediaHandler, files, serviceSession, source, snapshotID, profile,
			expectedDigest, maxInputBytes+maxOutputBytes)
		web019AssertPinnedValueUnchanged(t, ctx, websiteData, snapshotKey, snapshotBefore, files, serviceSession, source, sourceBefore)
	})
}

type web019SandboxProbe struct{ state websiteapi.MediaSandboxState }

func (p web019SandboxProbe) MediaSandboxState() websiteapi.MediaSandboxState { return p.state }

type web019WorkflowClient struct {
	client     websiteapp.BuildWorkflowClient
	coreClient *core.WorkflowClient
	control    *core.WorkflowControl
	maxPages   int
}

func (c *web019WorkflowClient) Start(ctx context.Context, request contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	return c.client.Start(ctx, request)
}

func (c *web019WorkflowClient) Get(ctx context.Context, id uuid.UUID) (contracts.WorkflowState, error) {
	return c.client.Get(ctx, id)
}

func (c *web019WorkflowClient) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	return c.client.GetTaskResult(ctx, workflowID, taskID)
}

func (c *web019WorkflowClient) Cancel(ctx context.Context, id uuid.UUID) error {
	return c.coreClient.Cancel(ctx, id)
}

func (c *web019WorkflowClient) ResolveTaskID(ctx context.Context, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	var before *core.TaskCursor
	for page := 0; page < c.maxPages; page++ {
		detail, err := c.control.Get(ctx, workflowID, core.WorkflowDetailFilter{Limit: 100, Before: before})
		if err != nil {
			return uuid.Nil, err
		}
		if detail.Workflow.ID != uuid.Nil && detail.Workflow.ID != workflowID ||
			detail.Workflow.OwnerClassID != "" && detail.Workflow.OwnerClassID != websiteworkflow.ComponentID {
			return uuid.Nil, contracts.ErrWorkflowTaskMissing
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

type web019Runner struct {
	inner          *media.DockerSandbox
	blockUntilNano atomic.Int64
	failOnce       sync.Map
	failures       atomic.Int64
}

func (r *web019Runner) Isolated() bool { return r != nil && r.inner.Isolated() }

func (r *web019Runner) Process(ctx context.Context, request mediaprofile.WorkRequest, profile mediaprofile.Profile, source io.Reader) (media.ProcessedOutput, error) {
	if r == nil || r.inner == nil {
		return media.ProcessedOutput{}, media.ErrNotReady
	}
	if until := r.blockUntilNano.Load(); until > time.Now().UnixNano() {
		for time.Now().UnixNano() < until {
			select {
			case <-ctx.Done():
				return media.ProcessedOutput{}, ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	if _, injected := r.failOnce.Load(request.OutputName); injected {
		r.failOnce.Delete(request.OutputName)
		r.failures.Add(1)
		return media.ProcessedOutput{}, errors.New("web019 injected transient processing failure")
	}
	return r.inner.Process(ctx, request, profile, source)
}

type web019NonIsolatedRunner struct{}

func (web019NonIsolatedRunner) Isolated() bool { return false }
func (web019NonIsolatedRunner) Process(context.Context, mediaprofile.WorkRequest, mediaprofile.Profile, io.Reader) (media.ProcessedOutput, error) {
	return media.ProcessedOutput{}, media.ErrNotReady
}

type web019V2Files struct {
	testCoreFilesAdapter
	size        *int64
	checksum    string
	contentType string
}

func (f *web019V2Files) StatFile(ctx context.Context, id string) (contracts.File, error) {
	file, err := f.testCoreFilesAdapter.StatFile(ctx, id)
	if err != nil {
		return contracts.File{}, err
	}
	if f.size != nil {
		file.Size = *f.size
	}
	if f.checksum != "" {
		file.SHA256 = f.checksum
	}
	if f.contentType != "" {
		file.ContentType = f.contentType
	}
	return file, nil
}

func web019AssertClientOverridesRejected(t *testing.T) {
	t.Helper()
	base := `"snapshot_id":"00000000-0000-4000-8000-000000000001","placement_id":"hero","field_path":"/hero/image","source_file_id":"00000000-0000-4000-8000-000000000002"`
	overrides := []string{
		`"profile_digest":"0000000000000000000000000000000000000000000000000000000000000000"`,
		`"processor_version":"attacker-processor"`,
		`"preset":"ultra"`,
		`"profile":{"x-media":{"accept":"image"}}`,
	}
	for _, extra := range overrides {
		raw := []byte("{" + base + "," + extra + "}")
		if err := httpjson.DecodeBytes(raw, &websiteapi.CreateDerivativeRequest{}); err == nil {
			t.Fatalf("client override accepted by strict DTO: %s", extra)
		}
	}
}

func web019AssertWorkerContractTamperingRejected(t *testing.T, profile mediaprofile.Profile, _ []byte) {
	t.Helper()
	valid := web019WorkRequest(uuid.NewString(), uuid.NewString(), uuid.NewString(),
		strings.Repeat("a", 64), strings.Repeat("b", 64), web019ProcessorVersion, web019RenditionSmall, profile)
	if _, err := mediaprofile.ValidateWorkRequest(web019JSON(valid)); err != nil {
		t.Fatalf("canonical work request was rejected: %v", err)
	}
	tampered := valid
	tampered.Profile = profile
	tampered.Profile.Renditions = append([]mediaprofile.Rendition(nil), profile.Renditions...)
	tampered.Profile.Renditions[0].Width++
	if _, err := mediaprofile.ValidateWorkRequest(web019JSON(tampered)); err == nil {
		t.Fatal("changed rendition dimensions were accepted under the original digest")
	}
	tampered = valid
	tampered.ProcessorVersion = "different-processor"
	if _, err := mediaprofile.ValidateWorkRequest(web019JSON(tampered)); err == nil {
		t.Fatal("changed processor version was accepted under the original digest")
	}
	tampered = valid
	tampered.ProfileDigest = strings.Repeat("c", 64)
	if _, err := mediaprofile.ValidateWorkRequest(web019JSON(tampered)); err == nil {
		t.Fatal("mismatched profile digest was accepted")
	}
	withOverride := web019JSON(valid)
	withOverride = []byte(strings.TrimSuffix(string(withOverride), "}") + `,"preset":"attacker"}`)
	if _, err := mediaprofile.ValidateWorkRequest(withOverride); err == nil {
		t.Fatal("client-style preset override was accepted by the worker contract")
	}
}

func web019AssertNoRunnerFallback(t *testing.T, files core.FilesCapability, session core.Session, data media.DataCapability) {
	t.Helper()
	if _, err := media.NewV2Handler(testCoreFilesAdapter{files: files, session: session}, data, web019NonIsolatedRunner{}, 1, 1); !errors.Is(err, media.ErrNotReady) {
		t.Fatalf("v2 worker accepted a non-isolated runner: %v", err)
	}
}

func web019AssertSandboxState(t *testing.T, ctx context.Context, sandbox *media.DockerSandbox, config media.DockerSandboxConfig) {
	t.Helper()
	state, err := sandbox.Probe(ctx)
	if err != nil {
		t.Fatalf("in-container sandbox preflight: %v", err)
	}
	if state.SeccompMode < 2 || state.CapEff != "0000000000000000" || !state.NoNewPrivs ||
		!state.ReadOnlyRootFS || !state.NetworkDenied || !state.PrivateStaging0700 ||
		state.CPUQuota == "" || state.MemoryLimitBytes != config.MemoryLimitBytes || state.PIDsLimit != config.PIDsLimit ||
		state.VerifiedAt.IsZero() {
		t.Fatalf("sandbox preflight did not prove effective kernel limits: %+v", state)
	}
	if !sandbox.Isolated() {
		t.Fatal("sandbox reported isolated without a successful preflight")
	}
}

func web019AwaitMediaState(t *testing.T, ctx context.Context, service *websiteapp.Service, auth context.Context, jobID, expected string) websiteapi.MediaJob {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		job, err := service.GetMediaJob(auth, jobID)
		if err != nil {
			t.Fatalf("GetMediaJob(%s): %v", jobID, err)
		}
		if job.State == expected {
			return job
		}
		if job.State == "queued" || job.State == "running" || job.State == "retryable_failed" {
			if time.Now().After(deadline) {
				t.Fatalf("media job %s did not reach %s: %+v", jobID, expected, job)
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		t.Fatalf("media job %s reached %s, want %s: %+v", jobID, job.State, expected, job)
	}
}

func web019AwaitReady(t *testing.T, ctx context.Context, service *websiteapp.Service, auth context.Context, jobID string) websiteapi.MediaJob {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		job, err := service.GetMediaJob(auth, jobID)
		if err != nil {
			t.Fatalf("GetMediaJob(%s): %v", jobID, err)
		}
		if job.State == "ready" {
			return job
		}
		if len(job.Outputs) != 0 {
			t.Fatalf("media job %s published outputs before ready: %+v", jobID, job.Outputs)
		}
		if job.State != "queued" && job.State != "running" {
			t.Fatalf("media job %s reached %s before ready: %+v", jobID, job.State, job)
		}
		if time.Now().After(deadline) {
			t.Fatalf("media job %s did not become ready: %+v", jobID, job)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func web019AssertPinnedValueUnchanged(t *testing.T, ctx context.Context, store contracts.RecordDataStore, key contracts.RecordKey,
	before []byte, files core.FilesCapability, session core.Session, sourceID string, source core.File) {
	t.Helper()
	current, err := store.Get(ctx, key)
	if err != nil || !bytes.Equal(current.Payload, before) {
		t.Fatalf("pinned snapshot value changed: err=%v", err)
	}
	actual, err := files.StatFile(ctx, session, sourceID)
	if err != nil || actual.SHA256 != source.SHA256 || actual.Size != source.Size || actual.Revision != source.Revision {
		t.Fatalf("original media source changed: before=%+v after=%+v err=%v", source, actual, err)
	}
}

func web019AssertImmutableDerivative(t *testing.T, ctx context.Context, handler *media.V2Handler,
	files core.FilesCapability, session core.Session, sourceID, snapshotID string,
	profile mediaprofile.Profile, profileDigest string) {
	t.Helper()
	sourceFile, err := files.StatFile(ctx, session, sourceID)
	if err != nil {
		t.Fatalf("stat direct immutability source: %v", err)
	}
	taskID := uuid.NewString()
	request := web019WorkRequest(taskID, snapshotID, sourceID, sourceFile.SHA256, web019SHA(taskID),
		web019ProcessorVersion, web019RenditionSmall, profile)
	request.ProfileDigest = profileDigest
	first, err := handler.Handle(ctx, taskID, request)
	if err != nil {
		t.Fatalf("direct derivative create: %v", err)
	}
	second, err := handler.Handle(ctx, taskID, request)
	if err != nil {
		t.Fatalf("direct derivative replay: %v", err)
	}
	if first.FileID != second.FileID || first.Checksum != second.Checksum || first.Size != second.Size {
		t.Fatalf("replay did not return the immutable output: first=%+v second=%+v", first, second)
	}
	web019TrackFile(t, files, session, first.FileID)
	before, err := files.StatFile(ctx, session, first.FileID)
	if err != nil {
		t.Fatalf("stat immutable output: %v", err)
	}
	body, _, err := files.ReadFile(ctx, session, first.FileID)
	if err != nil {
		t.Fatalf("read immutable output: %v", err)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, body)
	_ = body.Close()
	if err != nil || size != before.Size || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(before.SHA256) {
		t.Fatalf("immutable output bytes changed: size=%d err=%v", size, err)
	}
}

func web019AssertWorkerRejectsBadMetadata(t *testing.T, ctx context.Context, handler *media.V2Handler,
	files core.FilesCapability, session core.Session, sourceID, snapshotID string,
	profile mediaprofile.Profile, profileDigest string, maxStaged int64) {
	t.Helper()
	sourceFile, err := files.StatFile(ctx, session, sourceID)
	if err != nil {
		t.Fatalf("stat reject-case source: %v", err)
	}
	base := web019WorkRequest(uuid.NewString(), snapshotID, sourceID, sourceFile.SHA256, web019SHA("input"),
		web019ProcessorVersion, web019RenditionSmall, profile)
	base.ProfileDigest = profileDigest
	tests := []struct {
		name string
		wrap func() *web019V2Files
		err  error
	}{
		{"oversize source", func() *web019V2Files {
			size := maxStaged + 1
			return &web019V2Files{testCoreFilesAdapter: testCoreFilesAdapter{files: files, session: session}, size: &size}
		}, media.ErrTaskConflict},
		{"checksum mismatch", func() *web019V2Files {
			return &web019V2Files{testCoreFilesAdapter: testCoreFilesAdapter{files: files, session: session}, checksum: strings.Repeat("0", 64)}
		}, media.ErrTaskConflict},
		{"MIME mismatch", func() *web019V2Files {
			return &web019V2Files{testCoreFilesAdapter: testCoreFilesAdapter{files: files, session: session}, contentType: "application/octet-stream"}
		}, media.ErrInvalidRequest},
	}
	for _, test := range tests {
		rejecting, err := media.NewV2Handler(test.wrap(), web019HandlerData(handler), web019IsolatedProbeRunner{}, 1, maxStaged)
		if err != nil {
			t.Fatalf("construct reject-case worker for %s: %v", test.name, err)
		}
		request := base
		request.JobID = uuid.NewString()
		if _, err := rejecting.Handle(ctx, uuid.NewString(), request); !errors.Is(err, test.err) {
			t.Fatalf("%s = %v, want %v", test.name, err, test.err)
		}
	}
}

// web019IsolatedProbeRunner satisfies construction only; the negative cases
// reject source metadata before execution, so Process is never called.
type web019IsolatedProbeRunner struct{}

func (web019IsolatedProbeRunner) Isolated() bool { return true }
func (web019IsolatedProbeRunner) Process(context.Context, mediaprofile.WorkRequest, mediaprofile.Profile, io.Reader) (media.ProcessedOutput, error) {
	return media.ProcessedOutput{}, errors.New("web019 unexpected processing")
}

// web019HandlerData is intentionally unused for reject cases: each case uses a
// fresh task identity and never reaches a durable state write.
func web019HandlerData(*media.V2Handler) media.DataCapability { return web019RejectData{} }

type web019RejectData struct{}

func (web019RejectData) Get(context.Context, contracts.RecordKey) (contracts.Record, error) {
	return contracts.Record{}, contracts.ErrRecordNotFound
}
func (web019RejectData) Put(context.Context, contracts.RecordKey, contracts.RecordValue, int64) (contracts.Record, error) {
	return contracts.Record{}, contracts.ErrRecordInvalid
}

func web019WorkRequest(jobID, snapshotID, sourceID, sourceSHA, inputDigest, processorVersion, outputName string, profile mediaprofile.Profile) mediaprofile.WorkRequest {
	digest, _ := mediaprofile.Digest(profile, processorVersion)
	return mediaprofile.WorkRequest{
		Version: mediaprofile.WorkContractVersion, JobID: jobID, SnapshotID: snapshotID,
		PlacementID: web019PlacementID, FieldPath: web019FieldPath, SourceFileID: sourceID,
		SourceSHA256: sourceSHA, ProfileDigest: digest, ProcessorVersion: processorVersion,
		OutputName: outputName, IdempotencyKey: "media:direct:" + jobID,
		InputDigest: inputDigest, Profile: profile,
	}
}

func web019Manifest(t *testing.T) ([]byte, mediaprofile.Profile, []byte) {
	t.Helper()
	xMedia := map[string]any{
		"accept": "raster-image",
		"renditions": []any{
			map[string]any{"name": web019RenditionSmall, "width": 240, "height": 240, "fit": "cover"},
			map[string]any{"name": web019RenditionLarge, "width": 480, "height": 480, "fit": "contain"},
		},
	}
	raw, err := json.Marshal(xMedia)
	if err != nil {
		t.Fatalf("marshal x-media declaration: %v", err)
	}
	profile, err := mediaprofile.ValidateProfile(raw)
	if err != nil {
		t.Fatalf("invalid x-media fixture: %v", err)
	}
	manifest := web019JSON(map[string]any{
		"formatVersion": 2, "key": web019FeatureID, "name": "WEB-019 Hero",
		"version": web019FeatureVersion, "engineApi": "v1", "slots": []string{"main"},
		"arguments": map[string]any{},
		"dataInputs": []any{map[string]any{
			"key": "hero", "kind": "record", "type": "media", "profile": map[string]any{},
			"fields": []any{map[string]any{
				"path": "/image", "type": "media", "profile": map[string]any{"x-media": json.RawMessage(raw)},
			}},
		}},
		"runtime": map[string]any{"abi": featureruntime.ABI, "entry": "assets/app.js", "styles": []string{}},
	})
	files := map[string]struct{}{"assets/app.js": {}}
	if _, err := featureruntime.ResolveMediaProfile(manifest, files, web019FieldPath); err != nil {
		t.Fatalf("manifest fixture has no pinned media profile: %v", err)
	}
	return manifest, profile, raw
}

func web019CreateFile(t *testing.T, ctx context.Context, files core.FilesCapability, session core.Session, name string, body []byte, contentType string) string {
	t.Helper()
	id, err := files.CreateFile(ctx, session, uuid.NewString(), []string{"features"},
		map[string]string{"web019_file": name}, contentType, int64(len(body)), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create run-owned source file %s: %v", name, err)
	}
	web019TrackFile(t, files, session, id)
	return id
}

func web019TrackFile(t *testing.T, files core.FilesCapability, session core.Session, id string) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		current, err := files.StatFile(cleanupCtx, session, id)
		if err == nil {
			_ = files.DeleteFile(cleanupCtx, session, id, current.Revision)
		}
	})
}

func web019PNG(color byte) []byte {
	const width = 32
	const height = 32
	raw := make([]byte, 0, height*(1+width*3))
	for y := 0; y < height; y++ {
		raw = append(raw, 0)
		for x := 0; x < width; x++ {
			raw = append(raw, color, uint8(x*4), uint8(y*4))
		}
	}
	chunk := func(kind string, data []byte) []byte {
		out := make([]byte, 12+len(data))
		binary.BigEndian.PutUint32(out[:4], uint32(len(data)))
		copy(out[4:], kind)
		copy(out[8:], data)
		crc := crc32.ChecksumIEEE(out[4 : 8+len(data)])
		binary.BigEndian.PutUint32(out[8+len(data):], crc)
		return out
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 2, 0, 0, 0
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	_, _ = writer.Write(raw)
	_ = writer.Close()
	result := []byte("\x89PNG\r\n\x1a\n")
	result = append(result, chunk("IHDR", ihdr)...)
	result = append(result, chunk("IDAT", compressed.Bytes())...)
	return append(result, chunk("IEND", nil)...)
}

func web019SHA(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func web019Digest(value []byte) string { return web019SHA(string(value)) }

func web019PreflightMediaInputs(t *testing.T, ctx, writeCtx context.Context, service *websiteapp.Service, store contracts.RecordDataStore,
	files core.FilesCapability, session core.Session, snapshotID, featureID, version, fieldPath, sourceID, processorVersion, idempotencyKey string) {
	t.Helper()
	snapshot, err := service.GetSnapshot(writeCtx, snapshotID)
	if err != nil || snapshot.State != "ready" || snapshot.Payload == nil {
		t.Fatalf("preflight seeded snapshot = %+v, %v", snapshot, err)
	}
	var config struct {
		FormatVersion int `json:"format_version"`
		Page          struct {
			ID         string `json:"id"`
			Placements []struct {
				ID    string `json:"id"`
				Build *struct {
					FeatureKey string `json:"featureKey"`
					Version    string `json:"version"`
				} `json:"build"`
			} `json:"placements"`
		} `json:"page"`
	}
	if err := json.Unmarshal(snapshot.Payload.Config, &config); err != nil || config.FormatVersion != 3 ||
		len(config.Page.Placements) != 1 || config.Page.Placements[0].Build == nil ||
		config.Page.Placements[0].Build.FeatureKey != featureID || config.Page.Placements[0].Build.Version != version {
		t.Fatalf("preflight snapshot config: config=%s err=%v", snapshot.Payload.Config, err)
	}
	record, err := store.Get(ctx, websiterepo.FeatureVersionKey(featureID, version))
	if err != nil {
		t.Fatalf("preflight pinned feature record: %v", err)
	}
	var catalog struct {
		FeatureID string            `json:"feature_id"`
		Version   string            `json:"version"`
		Manifest  []byte            `json:"manifest"`
		Files     map[string]string `json:"files"`
		State     string            `json:"state"`
	}
	if err := json.Unmarshal(record.Payload, &catalog); err != nil || catalog.FeatureID != featureID ||
		catalog.Version != version || catalog.State != "ready" || len(catalog.Manifest) == 0 {
		t.Fatalf("preflight pinned feature payload: %s err=%v", record.Payload, err)
	}
	fileNames := make(map[string]struct{}, len(catalog.Files))
	for name := range catalog.Files {
		fileNames[name] = struct{}{}
	}
	profile, err := featureruntime.ResolveMediaProfile(catalog.Manifest, fileNames, fieldPath)
	if err != nil {
		t.Fatalf("preflight pinned media profile: %v", err)
	}
	if _, err := mediaprofile.Digest(profile, processorVersion); err != nil {
		t.Fatalf("preflight canonical digest: %v", err)
	}
	probe, err := files.StatFile(ctx, session, sourceID)
	if err != nil || probe.ID != sourceID {
		t.Fatalf("preflight source file = %+v, %v", probe, err)
	}
	shaPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	idPattern := regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	keyPattern := regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
	if len(probe.SHA256) != 64 || !shaPattern.MatchString(probe.SHA256) || probe.Size <= 0 {
		t.Fatalf("preflight source metadata = %+v", probe)
	}
	if !idPattern.MatchString(snapshotID) || !idPattern.MatchString(sourceID) || !keyPattern.MatchString(idempotencyKey) {
		t.Fatalf("preflight identifier patterns failed: snapshot=%s source=%s key=%q len=%d", snapshotID, sourceID, idempotencyKey, len(idempotencyKey))
	}
	for _, segment := range strings.Split(strings.TrimPrefix(fieldPath, "/"), "/") {
		if !featureruntime.ValidMediaFieldSegment(segment) {
			t.Fatalf("preflight field path segment invalid: %q", segment)
		}
	}
}

func web019JobWorkflowID(ctx context.Context, store contracts.RecordDataStore, jobID string) (uuid.UUID, error) {
	record, err := store.Get(ctx, websiterepo.MediaJobKey(jobID))
	if err != nil {
		return uuid.Nil, err
	}
	var payload struct {
		WorkflowID string `json:"workflow_id"`
	}
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(payload.WorkflowID)
}

func web019WebsiteSandboxState(state media.SandboxState) websiteapi.MediaSandboxState {
	return websiteapi.MediaSandboxState{
		Backend: state.Backend, Image: state.Image, SeccompMode: state.SeccompMode,
		CapEff: state.CapEff, NoNewPrivs: state.NoNewPrivs, CPUQuota: state.CPUQuota,
		MemoryLimitBytes: state.MemoryLimitBytes, PIDsLimit: state.PIDsLimit,
		ReadOnlyRootFS: state.ReadOnlyRootFS, NetworkDenied: state.NetworkDenied,
		PrivateStaging0700: state.PrivateStaging0700, VerifiedAt: state.VerifiedAt,
	}
}

func web019JSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
