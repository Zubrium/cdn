package workflow_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/pkg/contracts"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/google/uuid"
)

func web019MediaTask(t *testing.T) contracts.OperationTask {
	t.Helper()
	payload, err := json.Marshal(websiteworkflow.MediaRequest{
		Version: 1, JobID: uuid.New(), SnapshotID: uuid.New(),
		PlacementID: "hero-placement", FieldPath: "/hero/image", SourceFileID: uuid.New(),
		SourceSHA256: strings.Repeat("a", 64), ProfileDigest: strings.Repeat("b", 64),
		ProcessorVersion: "ffmpeg-7.1", InputDigest: strings.Repeat("c", 64),
		Profile: json.RawMessage(`{"accept":"raster-image","renditions":[{"name":"card","width":640,"height":480,"fit":"cover"},{"name":"full","width":1600,"height":1200,"fit":"contain"}]}`),
		Outputs: []string{"card", "full"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contracts.OperationTask{TaskID: uuid.NewString(), Payload: payload}
}

func TestMediaRootWorkflowEmitsOnlyPinnedRenditionChildren(t *testing.T) {
	handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	task := web019MediaTask(t)
	outcome, err := handler.MediaWorkerHandler(context.Background(), task)
	if err != nil {
		t.Fatalf("handle media root: %v", err)
	}
	if len(outcome.Children) != 2 {
		t.Fatalf("expected two children, got %d", len(outcome.Children))
	}
	for i, name := range []string{"card", "full"} {
		child := outcome.Children[i]
		if child.OperationID != "derivative" || child.Version != mediaprofile.WorkContractVersion {
			t.Fatalf("unexpected operation reference: %+v", child)
		}
		if child.EmissionKey == "" || !strings.Contains(child.EmissionKey, name) {
			t.Fatalf("unexpected emission key: %q", child.EmissionKey)
		}
		var request mediaprofile.WorkRequest
		decoder := json.NewDecoder(strings.NewReader(string(child.Payload)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			t.Fatalf("decode child request: %v", err)
		}
		if request.Version != mediaprofile.WorkContractVersion || request.OutputName != name || request.Profile.Accept != "raster-image" {
			t.Fatalf("unexpected child request: %+v", request)
		}
	}
	var root websiteworkflow.MediaRootResult
	if err := json.Unmarshal(outcome.Result, &root); err != nil || root.Format != websiteworkflow.MediaRootFormat || len(root.EmissionKeys) != 2 {
		t.Fatalf("unexpected root result: %+v (%v)", root, err)
	}
}

func TestMediaRootWorkflowRejectsUntrustedInput(t *testing.T) {
	handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	task := web019MediaTask(t)
	task.TaskID = ""
	if _, err := handler.MediaWorkerHandler(context.Background(), task); err == nil {
		t.Fatal("task without trusted identity must be rejected")
	}
	task = web019MediaTask(t)
	var request map[string]any
	if err := json.Unmarshal(task.Payload, &request); err != nil {
		t.Fatal(err)
	}
	request["preset"] = "attacker-controlled"
	task.Payload, _ = json.Marshal(request)
	if _, err := handler.MediaWorkerHandler(context.Background(), task); err == nil {
		t.Fatal("client-controlled preset override must be rejected")
	}
}
