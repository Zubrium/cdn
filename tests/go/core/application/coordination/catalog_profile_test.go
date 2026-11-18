package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
)

func taskHandlerForTest(context.Context, CoreJob) (corecoord.CoordinatorTaskOutcome, error) {
	return corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"ok":true}`)}, nil
}

func TestBuildTaskCatalogUsesOnlyGenericRegisteredComponentIDs(t *testing.T) {
	plan := BuildPlan{Components: []string{"core", "component.alpha", "component.beta"}}
	catalog, err := BuildTaskCatalog(plan, []ComponentTaskSet{
		{ComponentID: "component.alpha", Handlers: map[string]TaskHandler{"prepare/v1": taskHandlerForTest}},
		{ComponentID: "component.beta", Handlers: map[string]TaskHandler{"prepare/v1": taskHandlerForTest}},
	})
	if err != nil || catalog == nil {
		t.Fatalf("build generic task catalog: catalog=%v err=%v", catalog, err)
	}
	for _, componentID := range []string{"component.alpha", "component.beta"} {
		if catalog.handlers[taskHandlerKey(componentID, "prepare", 1)] == nil {
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

func TestBuildTaskCatalogRejectsUnselectedAndDuplicateProviders(t *testing.T) {
	plan := BuildPlan{Components: []string{"core", "component.alpha"}}
	if _, err := BuildTaskCatalog(plan, []ComponentTaskSet{{ComponentID: "component.other", Handlers: map[string]TaskHandler{"run/v1": taskHandlerForTest}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unselected task provider was accepted: %v", err)
	}
	providers := []ComponentTaskSet{
		{ComponentID: "component.alpha", Handlers: map[string]TaskHandler{"run/v1": taskHandlerForTest}},
		{ComponentID: "component.alpha", Handlers: map[string]TaskHandler{"stop/v1": taskHandlerForTest}},
	}
	if _, err := BuildTaskCatalog(plan, providers); !errors.Is(err, ErrTaskHandlerConflict) {
		t.Fatalf("duplicate component provider was accepted: %v", err)
	}
	if catalog, err := BuildTaskCatalog(plan, nil); err != nil || catalog != nil {
		t.Fatalf("profile without task handlers should not require a catalog: catalog=%v err=%v", catalog, err)
	}
}

func TestRuntimeRegistryCompilesAnImmutableSelectedTaskCatalog(t *testing.T) {
	registry := NewRuntimeRegistry()
	original := map[string]TaskHandler{"archive.inspect/v1": taskHandlerForTest}
	if err := registry.RegisterTaskHandlers("component.archive", original); err != nil {
		t.Fatal(err)
	}
	original["archive.inspect/v1"] = nil
	plan := BuildPlan{Components: []string{"core", "component.archive"}}
	catalog, err := registry.CompileTaskCatalog(plan)
	if err != nil || catalog == nil {
		t.Fatalf("compile task catalog: catalog=%v err=%v", catalog, err)
	}
	copyOfHandlers := catalog.WorkerHandlers()
	copyOfHandlers[taskHandlerKey("component.archive", "archive.inspect", 1)] = nil
	if catalog.WorkerHandlers()[taskHandlerKey("component.archive", "archive.inspect", 1)] == nil {
		t.Fatal("caller mutated the immutable task catalog")
	}
	if _, err := registry.CompileTaskCatalog(BuildPlan{Components: []string{"core"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unselected task provider was admitted: %v", err)
	}
	if err := registry.RegisterTaskHandlers("component.archive", map[string]TaskHandler{"archive.scan/v1": taskHandlerForTest}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate component handler registration was accepted: %v", err)
	}
}

func TestRuntimeRegistryRejectsInvalidOperationContracts(t *testing.T) {
	registry := NewRuntimeRegistry()
	for _, handlers := range []map[string]TaskHandler{
		{"task": taskHandlerForTest},
		{"task/v0": taskHandlerForTest},
		{"task/v65537": taskHandlerForTest},
		{"task/v1": nil},
	} {
		if err := registry.RegisterTaskHandlers("component.worker", handlers); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid task registration was accepted: handlers=%v err=%v", handlers, err)
		}
	}
}
