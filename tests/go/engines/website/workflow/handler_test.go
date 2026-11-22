//go:build website_acceptance
// +build website_acceptance

package workflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/google/uuid"
)

func buildTask() core.Task {
	return core.Task{
		ID:     uuid.MustParse("99999999-9999-4999-8999-999999999999"),
		Kind:   core.TaskKindWorker,
		Worker: &core.WorkerRef{ComponentID: "engine.website", OperationID: websiteworkflow.OperationID, ContractVersion: 1},
	}
}

func buildPayload(t *testing.T) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(websiteworkflow.Request{
		Version:               1,
		BuildID:               uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		SnapshotID:            uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		SnapshotRevision:      3,
		SnapshotDigest:        strings.Repeat("a", 64),
		ContentRevision:       2,
		Locale:                "en-US",
		LocaleCatalogRevision: 7,
		InputDigest:           strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestWebsiteBuildPageHandlerEmitsPinnedPrerenderChild(t *testing.T) {
	handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	task := buildTask()
	task.Payload = buildPayload(t)

	outcome, err := handler.WorkerHandler(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Children) != 1 {
		t.Fatalf("children = %d, want 1", len(outcome.Children))
	}
	child := outcome.Children[0]
	if child.Kind != core.TaskKindWorker || child.Worker == nil || child.Worker.ComponentID != "worker.prerender" ||
		child.Worker.OperationID != "render-page" || child.Worker.ContractVersion != 1 {
		t.Fatalf("child identity = %#v", child)
	}
	if child.MaxAttempts != 3 || child.EmissionKey == "" || len(child.Payload) == 0 {
		t.Fatalf("child scheduling = %#v", child)
	}
	if len(child.DependsOn)+len(child.DependsOnEmissions) != 0 {
		t.Fatalf("child must use the implicit parent dependency: %#v", child)
	}
	var childRequest struct {
		JobID          *string `json:"job_id"`
		IdempotencyKey string  `json:"idempotency_key"`
		Plan           []byte  `json:"plan"`
		PlanDigest     string  `json:"plan_digest"`
		InputDigest    string  `json:"input_digest"`
	}
	decoder := json.NewDecoder(bytes.NewReader(child.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&childRequest); err != nil {
		t.Fatal(err)
	}
	if childRequest.JobID != nil || childRequest.IdempotencyKey == "" || len(childRequest.Plan) == 0 ||
		len(childRequest.PlanDigest) != 64 || childRequest.InputDigest != strings.Repeat("b", 64) {
		t.Fatalf("child payload = %#v", childRequest)
	}
	if websiteworkflow.PlanDigest(childRequest.Plan) != childRequest.PlanDigest {
		t.Fatal("child plan digest does not match its plan bytes")
	}
	var plan map[string]json.RawMessage
	if err := json.Unmarshal(childRequest.Plan, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 || string(plan["version"]) != `"render-plan/v1"` || string(plan["input_digest"]) != `"`+childRequest.InputDigest+`"` {
		t.Fatalf("render plan = %s", childRequest.Plan)
	}

	var root websiteworkflow.RootResult
	if err := json.Unmarshal(outcome.Result, &root); err != nil {
		t.Fatal(err)
	}
	if root.Format != websiteworkflow.RootFormat || root.BuildID == uuid.Nil || root.InputDigest != strings.Repeat("b", 64) ||
		root.PrerenderEmissionKey != child.EmissionKey {
		t.Fatalf("root result = %#v", root)
	}
}

func TestWebsiteBuildPageHandlerRejectsInvalidTaskAndPayload(t *testing.T) {
	handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	valid := buildTask()
	valid.Payload = buildPayload(t)

	wrongWorker := valid
	wrongWorker.Worker = &core.WorkerRef{ComponentID: "engine.website", OperationID: websiteworkflow.OperationID, ContractVersion: 2}
	if _, err := handler.WorkerHandler(ctx, wrongWorker); err == nil {
		t.Fatal("wrong contract version was accepted")
	}

	wrongKind := valid
	wrongKind.Kind = core.TaskKindJoin
	if _, err := handler.WorkerHandler(ctx, wrongKind); err == nil {
		t.Fatal("join task was accepted")
	}

	for name, payload := range map[string]json.RawMessage{
		"unknown field":    json.RawMessage(`{"version":1,"build_id":"11111111-1111-4111-8111-111111111111","snapshot_id":"22222222-2222-4222-8222-222222222222","snapshot_revision":3,"snapshot_digest":"` + strings.Repeat("a", 64) + `","content_revision":2,"locale":"en-US","locale_catalog_revision":7,"input_digest":"` + strings.Repeat("b", 64) + `","route":"/"}`),
		"malformed digest": json.RawMessage(strings.ReplaceAll(string(buildPayload(t)), strings.Repeat("b", 64), "short")),
		"empty build id":   json.RawMessage(strings.ReplaceAll(string(buildPayload(t)), "11111111-1111-4111-8111-111111111111", uuid.Nil.String())),
		"invalid locale":   json.RawMessage(strings.ReplaceAll(string(buildPayload(t)), `"en-US"`, `"/"`)),
		"non-object":       json.RawMessage(`[]`),
	} {
		task := valid
		task.Payload = payload
		if _, err := handler.WorkerHandler(ctx, task); err == nil {
			t.Fatalf("%s payload was accepted", name)
		}
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := handler.WorkerHandler(canceled, valid); err == nil {
		t.Fatal("canceled context was accepted")
	}
}

func TestWebsiteBuildPageHandlerRequiresExplicitAttemptLimit(t *testing.T) {
	if _, err := websiteworkflow.NewHandler(websiteworkflow.Config{}); err == nil {
		t.Fatal("missing max attempts was accepted")
	}
}
