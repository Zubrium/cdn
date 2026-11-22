package composition

import (
	"context"
	"testing"
)

func TestFailClosedWorkerDoesNotReportAFatalFailureStream(t *testing.T) {
	component, err := newWorkerComponent(context.Background(), "worker.prerender")
	if err != nil {
		t.Fatal(err)
	}
	worker, ok := component.(*workerComponent)
	if !ok {
		t.Fatalf("unexpected worker component type %T", component)
	}
	assertSilentFailureStream(t, worker.Errors())
	assertSilentFailureStream(t, idleFailureStream())
	if err := worker.Ready(context.Background()); err != nil {
		t.Fatalf("compiled worker is not ready: %v", err)
	}
}
