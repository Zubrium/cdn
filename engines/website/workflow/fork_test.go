package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type fakeForkCopier struct {
	payload websiteapi.SnapshotForkWorkflowPayload
	calls   int
	err     error
}

func (f *fakeForkCopier) CopySnapshotContent(_ context.Context, payload websiteapi.SnapshotForkWorkflowPayload) error {
	f.calls++
	f.payload = payload
	return f.err
}

type fakeForkFinalizer struct {
	snapshotID string
	revision   int64
	calls      int
	snapshot   websiteapi.Snapshot
	err        error
}

func (f *fakeForkFinalizer) MarkSnapshotReady(_ context.Context, snapshotID string, expectedRevision int64) (websiteapi.Snapshot, error) {
	f.calls++
	f.snapshotID = snapshotID
	f.revision = expectedRevision
	if f.err != nil {
		return websiteapi.Snapshot{}, f.err
	}
	if f.snapshot.ID != "" {
		return f.snapshot, nil
	}
	return websiteapi.Snapshot{ID: snapshotID, State: "ready", Revision: expectedRevision + 1}, nil
}

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

func TestForkHandlerCopiesThenFinalizes(t *testing.T) {
	copier := &fakeForkCopier{}
	finalizer := &fakeForkFinalizer{}
	handler, err := NewForkHandler(copier, finalizer, 3)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := handler.WorkerHandler(context.Background(), forkTask(validForkPayload()))
	if err != nil {
		t.Fatalf("WorkerHandler: %v", err)
	}
	if copier.calls != 1 || finalizer.calls != 1 {
		t.Fatalf("calls copier=%d finalizer=%d", copier.calls, finalizer.calls)
	}
	if finalizer.snapshotID != validForkPayload().TargetSnapshotID || finalizer.revision != 3 {
		t.Fatalf("finalizer got %s rev %d", finalizer.snapshotID, finalizer.revision)
	}
	var result ForkResult
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Format != ForkRootFormat || result.TargetRevision != 4 {
		t.Fatalf("result=%#v", result)
	}
}

func TestForkHandlerRejectsInvalidPayload(t *testing.T) {
	handler, err := NewForkHandler(&fakeForkCopier{}, &fakeForkFinalizer{}, 3)
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

func TestForkHandlerPropagatesCopyError(t *testing.T) {
	want := errors.New("copy failed")
	copier := &fakeForkCopier{err: want}
	finalizer := &fakeForkFinalizer{}
	handler, err := NewForkHandler(copier, finalizer, 3)
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.WorkerHandler(context.Background(), forkTask(validForkPayload()))
	if !errors.Is(err, want) {
		t.Fatalf("err=%v want %v", err, want)
	}
	if !strings.Contains(err.Error(), "copy snapshot content") {
		t.Fatalf("copy failure lacks workflow stage context: %v", err)
	}
	if finalizer.calls != 0 {
		t.Fatalf("finalizer should not run on copy failure, calls=%d", finalizer.calls)
	}
}

func TestForkHandlerPropagatesFinalizeErrorAfterCopy(t *testing.T) {
	want := errors.New("finalize failed")
	copier := &fakeForkCopier{}
	finalizer := &fakeForkFinalizer{err: want}
	handler, err := NewForkHandler(copier, finalizer, 3)
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.WorkerHandler(context.Background(), forkTask(validForkPayload()))
	if !errors.Is(err, want) {
		t.Fatalf("err=%v want %v", err, want)
	}
	if !strings.Contains(err.Error(), "finalize snapshot") {
		t.Fatalf("finalization failure lacks workflow stage context: %v", err)
	}
	if copier.calls != 1 || finalizer.calls != 1 {
		t.Fatalf("calls copier=%d finalizer=%d", copier.calls, finalizer.calls)
	}
}

func TestForkHandlerRejectsMissingTaskID(t *testing.T) {
	handler, err := NewForkHandler(&fakeForkCopier{}, &fakeForkFinalizer{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	task := forkTask(validForkPayload())
	task.TaskID = ""
	if _, err := handler.WorkerHandler(context.Background(), task); !errors.Is(err, errInvalidForkTask) {
		t.Fatalf("err=%v want invalid fork task", err)
	}
}
