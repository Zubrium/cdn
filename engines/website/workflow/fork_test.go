package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func forkTask(payload websiteapi.SnapshotForkWorkflowPayload) contracts.OperationTask {
	raw, _ := json.Marshal(payload)
	return contracts.OperationTask{TaskID: "task-1", Payload: raw}
}

func validForkPayload() websiteapi.SnapshotForkWorkflowPayload {
	return websiteapi.SnapshotForkWorkflowPayload{
		SourceSnapshotID:              "11111111-1111-4111-8111-111111111111",
		TargetSnapshotID:              "22222222-2222-4222-8222-222222222222",
		ExpectedSourceContentRevision: 7,
		TargetSnapshotRevision:        3,
		ContentCopyIdempotencyKey:     "fork-key-1",
		SelectionPolicy:               "copy",
	}
}

func TestForkHandlerEmitsIndependentClassIsolatedTasks(t *testing.T) {
	handler, err := NewForkHandler(3)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := handler.WorkerHandler(context.Background(), forkTask(validForkPayload()))
	if err != nil {
		t.Fatalf("WorkerHandler: %v", err)
	}
	if len(outcome.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(outcome.Children))
	}
	copyChild, finalizeChild := outcome.Children[0], outcome.Children[1]
	if copyChild.OperationID != ForkCopyOperationID || copyChild.Version != ForkChildContractVersion {
		t.Fatalf("copy task = %s/v%d", copyChild.OperationID, copyChild.Version)
	}
	if finalizeChild.OperationID != ForkFinalizeOperationID || finalizeChild.Version != ForkChildContractVersion {
		t.Fatalf("finalize task = %s/v%d", finalizeChild.OperationID, finalizeChild.Version)
	}
	if copyChild.EmissionKey == "" || finalizeChild.EmissionKey == "" || copyChild.EmissionKey == finalizeChild.EmissionKey {
		t.Fatalf("emission keys = %q, %q", copyChild.EmissionKey, finalizeChild.EmissionKey)
	}
	if len(finalizeChild.DependsOnEmissions) != 1 || finalizeChild.DependsOnEmissions[0] != copyChild.EmissionKey {
		t.Fatalf("finalize dependencies = %v, want [%s]", finalizeChild.DependsOnEmissions, copyChild.EmissionKey)
	}
	if copyChild.MaxAttempts != 3 || finalizeChild.MaxAttempts != 3 {
		t.Fatalf("max attempts = %d, %d", copyChild.MaxAttempts, finalizeChild.MaxAttempts)
	}
	var copyPayload websiteapi.SnapshotForkWorkflowPayload
	if err := json.Unmarshal(copyChild.Payload, &copyPayload); err != nil {
		t.Fatal(err)
	}
	if copyPayload != validForkPayload() {
		t.Fatalf("copy payload = %#v, want %#v", copyPayload, validForkPayload())
	}
	var finalizePayload ForkFinalizePayload
	if err := json.Unmarshal(finalizeChild.Payload, &finalizePayload); err != nil {
		t.Fatal(err)
	}
	if finalizePayload.SnapshotID != validForkPayload().TargetSnapshotID || finalizePayload.ExpectedRevision != 3 {
		t.Fatalf("finalize payload = %#v", finalizePayload)
	}
	var result ForkResult
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Format != ForkRootFormat || result.TargetSnapshotID != validForkPayload().TargetSnapshotID ||
		result.CopyEmissionKey != copyChild.EmissionKey || result.FinalizeEmissionKey != finalizeChild.EmissionKey {
		t.Fatalf("result=%#v", result)
	}
}

func TestForkHandlerRejectsInvalidPayload(t *testing.T) {
	handler, err := NewForkHandler(3)
	if err != nil {
		t.Fatal(err)
	}
	payload := validForkPayload()
	payload.TargetSnapshotRevision = 0
	_, err = handler.WorkerHandler(context.Background(), forkTask(payload))
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("err=%v want invalid payload", err)
	}
}

func TestForkHandlerRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewForkHandler(0); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("err=%v want invalid configuration", err)
	}
}
