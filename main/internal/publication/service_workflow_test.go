package publication

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	workflow "github.com/Zubrium/cdn/pkg/contracts"
)

func TestFinishBuildRejectsConflictingExistingArtifactWithoutChangingTarget(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(Config{BranchID: testBranchID, Store: newMemoryRecordStore(), Starter: &recordingWorkflowStarter{}, MaxAttempts: 3,
		Targets: []Target{{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Publish(ctx, Trigger{Locale: testLocale, ContentPublicationRevision: 4, IdempotencyKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	payload := workflow.PublicationBuildPayload{RunID: run.RunID, RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale, ContentPublicationRevision: 4, IdempotencyKey: testKey}
	if err := service.BeginBuild(ctx, payload); err != nil {
		t.Fatal(err)
	}
	artifact := artifactRecord{Kind: recordTypeArtifact, RunID: run.RunID, RouteID: testRouteA, Locale: testLocale,
		SnapshotID: testSnapshotID, InputDigest: "old-digest", ArtifactManifestFileID: workflow.PublicationArtifactFileID(run.RunID, testRouteA, testLocale)}
	value, err := encodeModuleData(artifact, artifactIndex(artifact))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.config.Store.Put(ctx, artifactDataKey(testBranchID, run.RunID, testRouteA, testLocale), *value, 0); err != nil {
		t.Fatal(err)
	}
	result := workflow.PublicationBuildResult{Format: workflow.PublicationBuildResultFormat, RunID: run.RunID, RouteID: testRouteA,
		Locale: testLocale, SnapshotID: testSnapshotID, InputDigest: "new-digest", ArtifactManifestFileID: artifact.ArtifactManifestFileID}
	if err := service.FinishBuild(ctx, payload, result); !errors.Is(err, ErrStale) {
		t.Fatalf("conflicting artifact accepted: %v", err)
	}
	_, target, err := service.loadTarget(ctx, targetDataKey(testBranchID, run.RunID, testRouteA, testLocale), run.RunID)
	if err != nil || target.State != stateRunning || target.InputDigest != "" {
		t.Fatalf("target changed on conflict: %+v %v", target, err)
	}
}

type workflowStartFunc func(context.Context, workflow.WorkflowStartInput) (workflow.WorkflowStartReceipt, error)

type concurrentPublicationStore struct{ *memoryRecordStore }

func (store *concurrentPublicationStore) Batch(ctx context.Context, mutations []workflow.RecordMutation) error {
	if err := store.memoryRecordStore.Batch(ctx, mutations); err != nil {
		return err
	}
	// Model a competing request committing the same run before this call's CAS.
	return workflow.ErrRecordConflict
}

func TestPublishRecoversConcurrentIdenticalRunCreation(t *testing.T) {
	starter := &recordingWorkflowStarter{}
	service, err := NewService(Config{BranchID: testBranchID, Store: &concurrentPublicationStore{newMemoryRecordStore()}, Starter: starter, MaxAttempts: 3,
		Targets: []Target{{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale}}})
	if err != nil {
		t.Fatal(err)
	}
	trigger := Trigger{Locale: testLocale, ContentPublicationRevision: 4, SystemModelRevision: 9, IdempotencyKey: testKey}
	result, err := service.Publish(context.Background(), trigger)
	if err != nil || result.RunID == "" || result.State != stateRunning || len(starter.inputs) != 1 {
		t.Fatalf("concurrent replay = %+v, starts=%d, error=%v", result, len(starter.inputs), err)
	}
}

type rejectBuildBatchStore struct {
	*memoryRecordStore
	reject bool
}

func (store *rejectBuildBatchStore) Batch(ctx context.Context, mutations []workflow.RecordMutation) error {
	if store.reject {
		return workflow.ErrRecordConflict
	}
	return store.memoryRecordStore.Batch(ctx, mutations)
}

func TestFinishBuildLeavesTargetUnchangedWhenAtomicBatchFails(t *testing.T) {
	ctx := context.Background()
	store := &rejectBuildBatchStore{memoryRecordStore: newMemoryRecordStore()}
	service, err := NewService(Config{BranchID: testBranchID, Store: store, Starter: &recordingWorkflowStarter{}, MaxAttempts: 3,
		Targets: []Target{{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Publish(ctx, Trigger{Locale: testLocale, ContentPublicationRevision: 4, IdempotencyKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	payload := workflow.PublicationBuildPayload{RunID: run.RunID, RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale, ContentPublicationRevision: 4, IdempotencyKey: testKey}
	if err := service.BeginBuild(ctx, payload); err != nil {
		t.Fatal(err)
	}
	store.reject = true
	result := workflow.PublicationBuildResult{Format: workflow.PublicationBuildResultFormat, RunID: run.RunID, RouteID: testRouteA,
		Locale: testLocale, SnapshotID: testSnapshotID, InputDigest: "digest", ArtifactManifestFileID: workflow.PublicationArtifactFileID(run.RunID, testRouteA, testLocale)}
	if err := service.FinishBuild(ctx, payload, result); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	_, target, err := service.loadTarget(ctx, targetDataKey(testBranchID, run.RunID, testRouteA, testLocale), run.RunID)
	if err != nil || target.State != stateRunning || target.InputDigest != "" {
		t.Fatalf("target changed on failed batch: %+v %v", target, err)
	}
	if _, err := service.loadArtifact(ctx, run.RunID, testRouteA, testLocale); !errors.Is(err, workflow.ErrRecordNotFound) {
		t.Fatalf("artifact unexpectedly stored: %v", err)
	}
}

func (f workflowStartFunc) Start(ctx context.Context, input workflow.WorkflowStartInput) (workflow.WorkflowStartReceipt, error) {
	return f(ctx, input)
}

func TestPublishReportsCurrentStateWhenWorkflowCompletesDuringStart(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "completed"}[complete], func(t *testing.T) {
			var service *Service
			starter := workflowStartFunc(func(ctx context.Context, input workflow.WorkflowStartInput) (workflow.WorkflowStartReceipt, error) {
				if complete {
					var payload workflow.PublicationWorkflowPayload
					if err := json.Unmarshal(input.Payload, &payload); err != nil {
						t.Fatal(err)
					}
					if err := service.updateTarget(ctx, payload.RunID, testRouteA, testLocale, func(target *targetRecord) { target.State = stateSuccess }); err != nil {
						t.Fatal(err)
					}
					if _, err := service.FinalizeRun(ctx, payload.RunID); err != nil {
						t.Fatal(err)
					}
				}
				return workflow.WorkflowStartReceipt{State: "running"}, nil
			})
			var err error
			service, err = NewService(Config{BranchID: testBranchID, Store: newMemoryRecordStore(), Starter: starter, MaxAttempts: 3,
				Targets: []Target{{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Publish(context.Background(), Trigger{Locale: testLocale, ContentPublicationRevision: 4, IdempotencyKey: testKey})
			if err != nil {
				t.Fatal(err)
			}
			want := stateRunning
			if complete {
				want = stateSuccess
			}
			_, _, stored, err := service.loadRun(context.Background(), result.RunID)
			if err != nil || result.State != want || stored.State != want {
				t.Fatalf("response=%s stored=%s want=%s err=%v", result.State, stored.State, want, err)
			}
		})
	}
}

func TestBuildAndActivationRejectSubstitutedServerOwnedValues(t *testing.T) {
	store := newMemoryRecordStore()
	service, err := NewService(Config{
		BranchID: testBranchID, Store: store, Starter: &recordingWorkflowStarter{}, MaxAttempts: 3,
		Targets: []Target{{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale}},
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger := Trigger{Locale: testLocale, ContentPublicationRevision: 4, SystemModelRevision: 9, IdempotencyKey: testKey}
	result, err := service.Publish(context.Background(), trigger)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	build := workflow.PublicationBuildPayload{
		RunID: result.RunID, RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale,
		ContentPublicationRevision: 4, SystemModelRevision: 9, IdempotencyKey: testKey,
	}
	substituted := build
	substituted.PageSnapshotID = "55555555-5555-4555-8555-555555555555"
	if err := service.BeginBuild(context.Background(), substituted); err == nil {
		t.Fatal("build accepted a substituted snapshot")
	}
	substituted = build
	substituted.RouteID = testRouteB
	if err := service.BeginBuild(context.Background(), substituted); err == nil {
		t.Fatal("build accepted a substituted route")
	}
	substituted = build
	substituted.Locale = "fr-FR"
	if err := service.BeginBuild(context.Background(), substituted); err == nil {
		t.Fatal("build accepted a substituted locale")
	}
	if err := service.BeginBuild(context.Background(), build); err != nil {
		t.Fatalf("BeginBuild: %v", err)
	}
	artifactID := workflow.PublicationArtifactFileID(result.RunID, testRouteA, testLocale)
	buildResult := workflow.PublicationBuildResult{
		Format: workflow.PublicationBuildResultFormat, RunID: result.RunID, RouteID: testRouteA,
		Locale: testLocale, SnapshotID: testSnapshotID, InputDigest: "digest", ArtifactManifestFileID: artifactID,
	}
	if err := service.FinishBuild(context.Background(), build, buildResult); err != nil {
		t.Fatalf("FinishBuild: %v", err)
	}
	if err := service.FinishBuild(context.Background(), build, buildResult); err != nil {
		t.Fatalf("FinishBuild replay: %v", err)
	}
	activation := workflow.PublicationActivatePayload{
		RunID: result.RunID, RouteID: testRouteA, Locale: testLocale,
		ArtifactManifestFileID: artifactID, IdempotencyKey: testKey,
	}
	substitutedActivation := activation
	substitutedActivation.ArtifactManifestFileID = "66666666-6666-4666-8666-666666666666"
	if err := service.BeginActivation(context.Background(), substitutedActivation); err == nil {
		t.Fatal("activation accepted a substituted artifact")
	}
	if err := service.BeginActivation(context.Background(), activation); err != nil {
		t.Fatalf("BeginActivation: %v", err)
	}
	if err := service.FinishActivation(context.Background(), activation, workflow.PublicationActivateResult{
		Format: workflow.PublicationActivateResultFormat, RunID: result.RunID, RouteID: testRouteA,
		Locale: testLocale, ArtifactManifestFileID: artifactID, Revision: 2,
	}); err != nil {
		t.Fatalf("FinishActivation: %v", err)
	}
	final, err := service.FinalizeRun(context.Background(), result.RunID)
	if err != nil || final.State != stateSuccess {
		t.Fatalf("FinalizeRun = %+v, %v", final, err)
	}
}
