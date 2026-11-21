package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type scriptedExecutor struct {
	mu       sync.Mutex
	calls    []Operation
	contexts []FeatureContext
	blockers map[Operation]chan struct{}
	waitOnly map[Operation]bool
	failures map[string]error
	started  chan Operation
}

func (executor *scriptedExecutor) Execute(ctx context.Context, op Operation, featureContext FeatureContext) error {
	executor.mu.Lock()
	executor.calls = append(executor.calls, op)
	executor.contexts = append(executor.contexts, featureContext)
	blocker := executor.blockers[op]
	failure := executor.failures[featureContext.PlacementID]
	executor.mu.Unlock()
	if executor.started != nil {
		select {
		case executor.started <- op:
		default:
		}
	}
	if blocker != nil {
		if executor.waitOnly[op] {
			<-blocker
		} else {
			select {
			case <-blocker:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return failure
}

func (executor *scriptedExecutor) snapshot() ([]Operation, []FeatureContext) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]Operation(nil), executor.calls...), append([]FeatureContext(nil), executor.contexts...)
}

func featureContextFor(placementID string) FeatureContext {
	return FeatureContext{PlacementID: placementID, FeatureKey: "hero", Signal: Cancellation{ID: "signal-" + placementID}}
}

func waitForOperation(t *testing.T, started <-chan Operation, want Operation) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case op := <-started:
			if op == want {
				return
			}
		case <-timeout:
			t.Fatalf("operation %s never started", want)
		}
	}
}

func waitForPendingPlacement(t *testing.T, placement *Placement, executor *scriptedExecutor, placementID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		placement.mu.Lock()
		pending := placement.pending
		placement.mu.Unlock()
		if pending != nil && pending.context.PlacementID == placementID {
			return
		}
		time.Sleep(time.Millisecond)
	}
	placement.mu.Lock()
	active, pending := placement.active, placement.pending
	placement.mu.Unlock()
	operations, _ := executor.snapshot()
	t.Fatalf("update for %s never became pending: active=%v pending=%v operations=%v", placementID, active, pending, operations)
}

func TestPlacementAppliesOnlyNewestPendingUpdate(t *testing.T) {
	blocker := make(chan struct{})
	executor := &scriptedExecutor{
		blockers: map[Operation]chan struct{}{OpRender: blocker},
		waitOnly: map[Operation]bool{OpRender: true},
		started:  make(chan Operation, 8),
	}
	placement, err := NewPlacement(executor, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	renderDone := make(chan error, 1)
	go func() { renderDone <- placement.Render(context.Background(), featureContextFor("render")) }()
	waitForOperation(t, executor.started, OpRender)

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		firstDone <- placement.Update(context.Background(), featureContextFor("second"))
	}()
	waitForPendingPlacement(t, placement, executor, "second")
	go func() {
		secondDone <- placement.Update(context.Background(), featureContextFor("third"))
	}()
	waitForPendingPlacement(t, placement, executor, "third")

	close(blocker)
	if err := <-renderDone; err != nil {
		t.Fatalf("render failed: %v", err)
	}
	firstErr, secondErr := <-firstDone, <-secondDone
	if firstErr != nil && !errors.Is(firstErr, ErrSuperseded) {
		t.Fatalf("superseded update returned %v", firstErr)
	}
	if secondErr != nil && !errors.Is(secondErr, ErrSuperseded) {
		t.Fatalf("newest update returned %v", secondErr)
	}

	operations, contexts := executor.snapshot()
	if len(operations) != 2 || operations[0] != OpRender || operations[1] != OpUpdate {
		t.Fatalf("unexpected lifecycle operations: %v", operations)
	}
	if contexts[1].PlacementID != "third" {
		t.Fatalf("applied stale context: %+v", contexts[1])
	}
}

func TestPlacementDisposeCancelsWaitsAndRunsOnce(t *testing.T) {
	blocker := make(chan struct{})
	executor := &scriptedExecutor{blockers: map[Operation]chan struct{}{OpRender: blocker}, started: make(chan Operation, 8)}
	placement, err := NewPlacement(executor, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	renderDone := make(chan error, 1)
	go func() { renderDone <- placement.Render(context.Background(), featureContextFor("render")) }()
	waitForOperation(t, executor.started, OpRender)

	disposeDone := make(chan error, 1)
	go func() { disposeDone <- placement.Dispose(context.Background()) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		placement.mu.Lock()
		disposed := placement.disposed
		placement.mu.Unlock()
		if disposed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(blocker)
	<-renderDone
	if err := <-disposeDone; err != nil {
		t.Fatalf("dispose failed: %v", err)
	}
	if err := placement.Dispose(context.Background()); err != nil {
		t.Fatalf("repeated dispose failed: %v", err)
	}
	operations, _ := executor.snapshot()
	if len(operations) != 2 || operations[1] != OpDispose {
		t.Fatalf("unexpected lifecycle operations: %v", operations)
	}
	if _, _, disposed := placement.State(); !disposed {
		t.Fatal("placement state is not disposed")
	}
}

func TestPlacementRejectsOverlappingOperationsAndEnforcesTimeout(t *testing.T) {
	blocker := make(chan struct{})
	executor := &scriptedExecutor{blockers: map[Operation]chan struct{}{OpRender: blocker}, started: make(chan Operation, 8)}
	placement, err := NewPlacement(executor, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = placement.Render(context.Background(), featureContextFor("render")) }()
	waitForOperation(t, executor.started, OpRender)
	if err := placement.Render(context.Background(), featureContextFor("again")); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlapping render: err=%v", err)
	}
	if err := placement.Hydrate(context.Background(), featureContextFor("hydrate")); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlapping hydrate: err=%v", err)
	}
	close(blocker)

	timed, err := NewPlacement(&scriptedExecutor{blockers: map[Operation]chan struct{}{
		OpRender: make(chan struct{}),
	}}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := timed.Render(context.Background(), featureContextFor("timeout")); !errors.Is(err, ErrTimeout) {
		t.Fatalf("lifecycle timeout: err=%v", err)
	}
}

func TestSupervisorIsolatesPlacementFailures(t *testing.T) {
	executor := &scriptedExecutor{failures: map[string]error{"broken": errors.New("feature failed")}}
	supervisor := NewSupervisor(executor, time.Second)
	broken, err := supervisor.Placement("broken")
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := supervisor.Placement("healthy")
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Render(context.Background(), featureContextFor("broken")); err == nil {
		t.Fatal("failing placement reported success")
	}
	if err := healthy.Hydrate(context.Background(), featureContextFor("healthy")); err != nil {
		t.Fatalf("sibling placement was blocked: %v", err)
	}
	operations, _ := executor.snapshot()
	if len(operations) != 2 || operations[0] != OpRender || operations[1] != OpHydrate {
		t.Fatalf("unexpected operations: %v", operations)
	}
}
