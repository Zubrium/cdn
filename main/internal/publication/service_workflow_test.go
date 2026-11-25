package publication

import (
	"context"
	"encoding/json"
	"testing"

	workflow "github.com/Zubrium/cdn/pkg/contracts"
)

type workflowStartFunc func(context.Context, workflow.WorkflowStartInput) (workflow.WorkflowStartReceipt, error)

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
