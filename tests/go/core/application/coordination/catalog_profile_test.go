package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
)

func taskHandlerForTest(context.Context, Task) (corecoord.TaskOutcome, error) {
	return corecoord.TaskOutcome{Result: json.RawMessage(`{"ok":true}`)}, nil
}

func TestBuildWorkerCatalogUsesOnlyGenericRegisteredComponentIDs(t *testing.T) {
	plan := BuildPlan{Components: []string{"core", "component.alpha", "component.beta"}}
	catalog, err := BuildWorkerCatalog(plan, []ComponentWorkerSet{
		{ComponentID: "component.alpha", Handlers: map[string]WorkerHandler{"prepare/v1": taskHandlerForTest}},
		{ComponentID: "component.beta", Handlers: map[string]WorkerHandler{"prepare/v1": taskHandlerForTest}},
	})
	if err != nil || catalog == nil {
		t.Fatalf("build generic task catalog: catalog=%v err=%v", catalog, err)
	}
	for _, componentID := range []string{"component.alpha", "component.beta"} {
		if catalog.workers[workerHandlerKey(componentID, "prepare", 1)].Handler == nil {
			t.Errorf("catalog omitted %s's independently scoped operation", componentID)
		}
	}
	if _, ok := catalog.definition("component.alpha", "prepare", 1); !ok {
		t.Fatal("catalog lost component.alpha's registered operation")
	}
	if _, ok := catalog.definition("component.beta", "prepare", 1); !ok {
		t.Fatal("catalog lost component.beta's registered operation")
	}
}

func TestBuildWorkerCatalogRejectsUnselectedAndDuplicateProviders(t *testing.T) {
	plan := BuildPlan{Components: []string{"core", "component.alpha"}}
	if _, err := BuildWorkerCatalog(plan, []ComponentWorkerSet{{ComponentID: "component.other", Handlers: map[string]WorkerHandler{"run/v1": taskHandlerForTest}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unselected task provider was accepted: %v", err)
	}
	providers := []ComponentWorkerSet{
		{ComponentID: "component.alpha", Handlers: map[string]WorkerHandler{"run/v1": taskHandlerForTest}},
		{ComponentID: "component.alpha", Handlers: map[string]WorkerHandler{"stop/v1": taskHandlerForTest}},
	}
	if _, err := BuildWorkerCatalog(plan, providers); !errors.Is(err, ErrWorkerHandlerConflict) {
		t.Fatalf("duplicate component provider was accepted: %v", err)
	}
	if catalog, err := BuildWorkerCatalog(plan, nil); err != nil || catalog != nil {
		t.Fatalf("profile without task handlers should not require a catalog: catalog=%v err=%v", catalog, err)
	}
}

func TestRuntimeRegistryCompilesAnImmutableSelectedWorkerCatalog(t *testing.T) {
	registry := NewRuntimeRegistry()
	original := map[string]WorkerHandler{"archive.inspect/v1": taskHandlerForTest}
	if err := registry.RegisterWorkerHandlers("component.archive", original); err != nil {
		t.Fatal(err)
	}
	original["archive.inspect/v1"] = nil
	plan := BuildPlan{Components: []string{"core", "component.archive"}}
	catalog, err := registry.CompileWorkerCatalog(plan)
	if err != nil || catalog == nil {
		t.Fatalf("compile task catalog: catalog=%v err=%v", catalog, err)
	}
	copyOfHandlers := catalog.handlers()
	copyOfHandlers[workerHandlerKey("component.archive", "archive.inspect", 1)] = nil
	if catalog.handlers()[workerHandlerKey("component.archive", "archive.inspect", 1)] == nil {
		t.Fatal("caller mutated the immutable task catalog")
	}
	if _, err := registry.CompileWorkerCatalog(BuildPlan{Components: []string{"core"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unselected task provider was admitted: %v", err)
	}
	if err := registry.RegisterWorkerHandlers("component.archive", map[string]WorkerHandler{"archive.scan/v1": taskHandlerForTest}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate component handler registration was accepted: %v", err)
	}
}

func TestRuntimeRegistryRejectsInvalidOperationContracts(t *testing.T) {
	registry := NewRuntimeRegistry()
	for _, handlers := range []map[string]WorkerHandler{
		{"task": taskHandlerForTest},
		{"task/v0": taskHandlerForTest},
		{"task/v65537": taskHandlerForTest},
		{"task/v1": nil},
	} {
		if err := registry.RegisterWorkerHandlers("component.worker", handlers); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid task registration was accepted: handlers=%v err=%v", handlers, err)
		}
	}
}
