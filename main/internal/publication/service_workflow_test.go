package publication

import (
	"context"
	"testing"

	workflow "github.com/Zubrium/cdn/pkg/contracts"
)

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
