package contentbridge

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func workflowTask(payload any) contracts.OperationTask {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return contracts.OperationTask{TaskID: "root", WorkflowID: "workflow", Payload: raw}
}

func assertSingleChild(t *testing.T, outcome contracts.OperationOutcome, operationID string, version uint32, emission string, wantPayload any) {
	t.Helper()
	if len(outcome.Children) != 1 {
		t.Fatalf("children = %d, want exactly 1", len(outcome.Children))
	}
	child := outcome.Children[0]
	if child.OperationID != operationID || child.Version != version || child.EmissionKey != emission || child.MaxAttempts != 3 {
		t.Fatalf("child = %+v", child)
	}
	var got, want any
	if err := json.Unmarshal(child.Payload, &got); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(wantPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("child payload = %+v, want %+v", got, want)
	}
}

func TestProjectionRootEmitsExactWebsiteChild(t *testing.T) {
	handler, err := NewProjectionStartHandler(Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.ContentProjectionWorkflowPayload{PageSnapshotID: "11111111-1111-4111-8111-111111111111", Locale: "en-US", AllowProvisioning: true}
	outcome, err := handler.WorkerHandler(context.Background(), workflowTask(request))
	if err != nil {
		t.Fatal(err)
	}
	emission := contracts.ContentProjectionEmissionKey(request.PageSnapshotID, request.Locale)
	assertSingleChild(t, outcome, contracts.WebsiteProjectionOperationID, contracts.ContentBridgeChildVersion, emission, contracts.ContentProjectionRequest(request))
	result, err := contracts.DecodePayload[contracts.ContentBridgeRootResult](outcome.Result)
	if err != nil || result.Format != contracts.ContentProjectionWorkflowFormat || result.EmissionKey != emission {
		t.Fatalf("root result = %+v, %v", result, err)
	}
}

func findChild(t *testing.T, outcome contracts.OperationOutcome, operationID string) contracts.OperationTaskChild {
	t.Helper()
	for _, child := range outcome.Children {
		if child.OperationID == operationID {
			return child
		}
	}
	t.Fatalf("child %s was not declared", operationID)
	return contracts.OperationTaskChild{}
}

func TestBuildAndLocaleRootsEmitExactContentChildren(t *testing.T) {
	build, err := NewBuildStartHandler(Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.ContentBuildWorkflowPayload{PageSnapshotID: "22222222-2222-4222-8222-222222222222", Locale: "ru-RU"}
	outcome, err := build.WorkerHandler(context.Background(), workflowTask(request))
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Children) != 2 {
		t.Fatalf("build children = %d, want 2", len(outcome.Children))
	}
	projectionEmission := contracts.ContentBuildProjectionEmissionKey(request.PageSnapshotID, request.Locale)
	buildEmission := contracts.ContentBuildEmissionKey(request.PageSnapshotID, request.Locale)
	projectionChild := findChild(t, outcome, contracts.ContentBuildProjectionOperationID)
	if projectionChild.Version != contracts.ContentBridgeChildVersion || projectionChild.EmissionKey != projectionEmission ||
		projectionChild.MaxAttempts != 3 || len(projectionChild.DependsOnEmissions) != 0 {
		t.Fatalf("projection child = %+v", projectionChild)
	}
	buildChild := findChild(t, outcome, contracts.ContentBuildResolveOperationID)
	if buildChild.Version != contracts.ContentBridgeChildVersion || buildChild.EmissionKey != buildEmission ||
		buildChild.MaxAttempts != 3 || !reflect.DeepEqual(buildChild.DependsOnEmissions, []string{projectionEmission}) {
		t.Fatalf("build child = %+v", buildChild)
	}
	var projectionPayload contracts.ContentProjectionRequest
	if err := json.Unmarshal(projectionChild.Payload, &projectionPayload); err != nil ||
		projectionPayload.PageSnapshotID != request.PageSnapshotID || projectionPayload.Locale != request.Locale || projectionPayload.AllowProvisioning {
		t.Fatalf("projection child payload = %+v, %v", projectionPayload, err)
	}
	var buildPayload contracts.ContentBuildProjectionRequest
	if err := json.Unmarshal(buildChild.Payload, &buildPayload); err != nil ||
		buildPayload.PageSnapshotID != request.PageSnapshotID || buildPayload.Locale != request.Locale {
		t.Fatalf("build child payload = %+v, %v", buildPayload, err)
	}
	rootResult, err := contracts.DecodePayload[contracts.ContentBridgeRootResult](outcome.Result)
	if err != nil || rootResult.Format != contracts.ContentBuildWorkflowFormat ||
		rootResult.EmissionKey != buildEmission || rootResult.ProjectionEmissionKey != projectionEmission {
		t.Fatalf("build root result = %+v, %v", rootResult, err)
	}

	locale, err := NewLocaleStartHandler(Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err = locale.WorkerHandler(context.Background(), workflowTask(contracts.ContentLocaleWorkflowPayload{}))
	if err != nil {
		t.Fatal(err)
	}
	assertSingleChild(t, outcome, contracts.ContentLocaleReadOperationID, contracts.ContentBridgeChildVersion,
		contracts.ContentLocaleEmissionKey, contracts.ContentLocaleCatalogRequest{})
}

func TestRootsRejectSubstitutedScopeAndForeignFields(t *testing.T) {
	projection, err := NewProjectionStartHandler(Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	build, err := NewBuildStartHandler(Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	validSnapshot := "33333333-3333-4333-8333-333333333333"
	cases := []struct {
		name    string
		task    contracts.OperationTask
		handler func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error)
	}{
		{name: "non-canonical snapshot", handler: projection.WorkerHandler,
			task: workflowTask(map[string]any{"page_snapshot_id": "{33333333-3333-4333-8333-333333333333}", "locale": "en-US"})},
		{name: "malformed snapshot", handler: projection.WorkerHandler,
			task: workflowTask(contracts.ContentProjectionWorkflowPayload{PageSnapshotID: "not-a-uuid", Locale: "en-US"})},
		{name: "malformed locale", handler: build.WorkerHandler,
			task: workflowTask(contracts.ContentBuildWorkflowPayload{PageSnapshotID: validSnapshot, Locale: "fr FR"})},
		{name: "injected operation", handler: build.WorkerHandler,
			task: workflowTask(map[string]any{"page_snapshot_id": validSnapshot, "locale": "en-US", "operation_id": "engine.website.other"})},
		{name: "injected task class", handler: build.WorkerHandler,
			task: workflowTask(map[string]any{"page_snapshot_id": validSnapshot, "locale": "en-US", "task_class": "engine.website"})},
		{name: "injected module", handler: build.WorkerHandler,
			task: workflowTask(map[string]any{"page_snapshot_id": validSnapshot, "locale": "en-US", "module_id": "engine.content"})},
		{name: "injected task id", handler: build.WorkerHandler,
			task: workflowTask(map[string]any{"page_snapshot_id": validSnapshot, "locale": "en-US", "task_id": "00000000-0000-4000-8000-000000000000"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.handler(ctx, tc.task); err == nil {
				t.Fatal("foreign or substituted root payload was accepted")
			}
		})
	}
}

func TestRootConfigFailsClosed(t *testing.T) {
	if _, err := NewProjectionStartHandler(Config{}); err == nil {
		t.Fatal("projection root accepted invalid retry configuration")
	}
	if _, err := NewBuildStartHandler(Config{MaxAttempts: 101}); err == nil {
		t.Fatal("build root accepted invalid retry configuration")
	}
}
