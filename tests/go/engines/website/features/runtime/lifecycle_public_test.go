package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

type lifecycleExecutor struct {
	operations []runtime.Operation
	contexts   []runtime.FeatureContext
	fail       map[string]bool
}

func (executor *lifecycleExecutor) Execute(_ context.Context, op runtime.Operation, featureContext runtime.FeatureContext) error {
	executor.operations = append(executor.operations, op)
	executor.contexts = append(executor.contexts, featureContext)
	if executor.fail[featureContext.PlacementID] {
		return errors.New("feature failed")
	}
	return nil
}

func TestLifecycleExportsRenderHydrateUpdateAndDispose(t *testing.T) {
	executor := &lifecycleExecutor{}
	placement, err := runtime.NewPlacement(executor, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first := runtime.FeatureContext{PlacementID: "p", FeatureKey: "hero", Signal: runtime.Cancellation{ID: "s1"}}
	second := runtime.FeatureContext{PlacementID: "p", FeatureKey: "hero", Signal: runtime.Cancellation{ID: "s2"}}
	if err := placement.Render(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := placement.Hydrate(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := placement.Update(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if err := placement.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []runtime.Operation{runtime.OpRender, runtime.OpHydrate, runtime.OpUpdate, runtime.OpDispose}
	if len(executor.operations) != len(want) {
		t.Fatalf("operations=%v, want %v", executor.operations, want)
	}
	for i := range want {
		if executor.operations[i] != want[i] {
			t.Fatalf("operations=%v, want %v", executor.operations, want)
		}
	}
	if executor.contexts[2].Signal.ID != "s2" {
		t.Fatalf("update did not receive the newest context: %+v", executor.contexts[2])
	}
	if executor.contexts[3].PlacementID != "" {
		t.Fatal("dispose received a feature context")
	}
}

func TestLifecycleFailureInOnePlacementDoesNotBlockSiblings(t *testing.T) {
	executor := &lifecycleExecutor{fail: map[string]bool{"broken": true}}
	supervisor := runtime.NewSupervisor(executor, time.Second)
	broken, err := supervisor.Placement("broken")
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := supervisor.Placement("healthy")
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Render(context.Background(), runtime.FeatureContext{PlacementID: "broken"}); err == nil {
		t.Fatal("failing placement reported success")
	}
	if err := healthy.Render(context.Background(), runtime.FeatureContext{PlacementID: "healthy"}); err != nil {
		t.Fatalf("sibling placement was blocked: %v", err)
	}
}

func web027LifecyclePhasesAndIsolationAreEnforced(t *testing.T) {
	TestLifecycleExportsRenderHydrateUpdateAndDispose(t)
	TestLifecycleFailureInOnePlacementDoesNotBlockSiblings(t)
	TestBuildContextIsSecretFreeAndSerializable(t)
	TestBuildContextRejectsCredentialsAndInvalidTokens(t)
}

func web029SupervisedExecutorIsFailClosed(t *testing.T) {
	TestCommandExecutorIsFailClosedOnConfiguration(t)
	TestCommandConfigAcceptsSupervisedAbsoluteBinary(t)
	TestEmbeddedBrowserHostExposesLifecycleGuarantees(t)
}
