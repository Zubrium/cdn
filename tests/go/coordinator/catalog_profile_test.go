package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func taskHandlerForTest(context.Context, CoreJob) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func TestBuildTaskCatalogMatchesSelectedWorkersAndAutomation(t *testing.T) {
	plan := BuildPlan{Components: []string{"coordinator", "core", "worker.archive"}}
	catalog, err := BuildTaskCatalog(plan, []WorkerTaskSet{{
		ComponentID: "worker.archive",
		Handlers:    map[string]TaskHandler{"archive.inspect/v1": taskHandlerForTest},
	}}, nil)
	if err != nil || catalog == nil {
		t.Fatalf("build selected worker catalog: catalog=%v err=%v", catalog, err)
	}
	types := catalog.JobTypes()
	if len(types) != 1 || types[0] != "archive.inspect/v1" {
		t.Fatalf("catalog contains unexpected tasks: %v", types)
	}

	publicationPlan := BuildPlan{Components: []string{
		"core", "coordinator", "engine.website", "engine.content", "engine.router", "worker.prerender", "automation.site-publication",
	}}
	publication, err := BuildTaskCatalog(publicationPlan, []WorkerTaskSet{{
		ComponentID: "worker.prerender",
		Handlers:    map[string]TaskHandler{"prerender.site/v1": taskHandlerForTest},
	}}, taskHandlerForTest)
	if err != nil {
		t.Fatalf("build publication task catalog: %v", err)
	}
	if got := publication.JobTypes(); len(got) != 2 || got[0] != "prerender.site/v1" || got[1] != SitePublicationJobType {
		t.Fatalf("publication catalog is incomplete or non-deterministic: %v", got)
	}
	if got := publication.SubmissionTypes(); len(got) != 1 || got[0] != "prerender.site/v1" {
		t.Fatalf("engine scheduler can submit reserved publication task: %v", got)
	}
}

func TestBuildTaskCatalogRejectsMissingOrUnselectedProviders(t *testing.T) {
	plan := BuildPlan{Components: []string{"core", "coordinator", "worker.archive"}}
	if _, err := BuildTaskCatalog(plan, nil, nil); !errors.Is(err, ErrTaskProviderUnavailable) {
		t.Fatalf("selected worker without handler provider was accepted: %v", err)
	}
	if _, err := BuildTaskCatalog(BuildPlan{Components: []string{"core", "coordinator"}}, []WorkerTaskSet{{
		ComponentID: "worker.archive",
		Handlers:    map[string]TaskHandler{"archive.inspect/v1": taskHandlerForTest},
	}}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unselected worker provider was accepted: %v", err)
	}
	if catalog, err := BuildTaskCatalog(BuildPlan{Components: []string{"core", "coordinator"}}, nil, nil); err != nil || catalog != nil {
		t.Fatalf("profile without workers unexpectedly requires a catalog: catalog=%v err=%v", catalog, err)
	}
}

func TestBuildTaskCatalogRejectsDuplicateHandlersAndPublicationMismatch(t *testing.T) {
	plan := BuildPlan{Components: []string{"core", "coordinator", "worker.archive", "worker.media"}}
	sets := []WorkerTaskSet{
		{ComponentID: "worker.archive", Handlers: map[string]TaskHandler{"asset.build/v1": taskHandlerForTest}},
		{ComponentID: "worker.media", Handlers: map[string]TaskHandler{"asset.build/v1": taskHandlerForTest}},
	}
	if _, err := BuildTaskCatalog(plan, sets, nil); !errors.Is(err, ErrTaskHandlerConflict) {
		t.Fatalf("duplicate task type across worker modules was accepted: %v", err)
	}
	publicationPlan := BuildPlan{Components: []string{
		"core", "coordinator", "engine.website", "engine.content", "engine.router", "worker.prerender", "automation.site-publication",
	}}
	if _, err := BuildTaskCatalog(publicationPlan, []WorkerTaskSet{{
		ComponentID: "worker.prerender", Handlers: map[string]TaskHandler{"site-publication/v1": taskHandlerForTest},
	}}, taskHandlerForTest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("worker was allowed to replace reserved publication handler: %v", err)
	}
	if _, err := BuildTaskCatalog(publicationPlan, []WorkerTaskSet{{
		ComponentID: "worker.prerender", Handlers: map[string]TaskHandler{"prerender.site/v1": taskHandlerForTest},
	}}, nil); !errors.Is(err, ErrTaskProviderUnavailable) {
		t.Fatalf("publication profile without publication handler was accepted: %v", err)
	}
}

func TestRuntimeRegistryCompilesAnImmutableProfileBoundCatalog(t *testing.T) {
	registry := NewRuntimeRegistry()
	original := map[string]TaskHandler{"archive.inspect/v1": taskHandlerForTest}
	if err := registry.RegisterWorkerTasks("worker.archive", original); err != nil {
		t.Fatal(err)
	}
	original["archive.inspect/v1"] = nil
	plan := BuildPlan{Components: []string{"core", "coordinator", "worker.archive"}}
	catalog, err := registry.CompileTaskCatalog(plan)
	if err != nil || catalog == nil {
		t.Fatalf("compile registered catalog: catalog=%v err=%v", catalog, err)
	}
	copyOfHandlers := catalog.WorkerHandlers()
	copyOfHandlers["archive.inspect/v1"] = nil
	if catalog.WorkerHandlers()["archive.inspect/v1"] == nil {
		t.Fatal("caller mutated the immutable profile catalog")
	}
	if _, err := registry.CompileTaskCatalog(BuildPlan{Components: []string{"core", "coordinator"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unselected registered worker was admitted to another profile: %v", err)
	}
}

func TestRuntimeRegistryRejectsDuplicateAndReservedWorkerRegistration(t *testing.T) {
	registry := NewRuntimeRegistry()
	if err := registry.RegisterWorkerTasks("worker.archive", map[string]TaskHandler{"site-publication/v1": taskHandlerForTest}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("worker registered Coordinator's reserved task type: %v", err)
	}
	if err := registry.RegisterWorkerTasks("worker.archive", map[string]TaskHandler{"archive.inspect/v1": taskHandlerForTest}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterWorkerTasks("worker.archive", map[string]TaskHandler{"archive.scan/v1": taskHandlerForTest}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate worker provider registration was accepted: %v", err)
	}
	if err := registry.RegisterPublicationHandler(taskHandlerForTest); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterPublicationHandler(taskHandlerForTest); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate publication provider was accepted: %v", err)
	}
}
