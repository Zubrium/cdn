//go:build full && !devfixtures && !router

package composition

import (
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

func TestFullProfileRegistersEngineContractsAndOperations(t *testing.T) {
	r := platform.NewRegistry()
	if err := RegisterProfile(r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"router", "website", "content", "forms"} {
		module, err := r.Lookup(name)
		if err != nil || len(module.Operations()) == 0 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	groups := map[string]bool{}
	for _, descriptor := range r.ComponentDescriptors() {
		for _, group := range descriptor.AccessGroups {
			groups[group] = true
		}
	}
	for _, group := range []string{"features", "content", "router_routes", "forms_schemas", "forms_submissions"} {
		if !groups[group] {
			t.Errorf("full profile did not declare engine access group %q: %v", group, groups)
		}
	}
}

func TestFullProfileCompilesWebsiteBuildAndPrerenderHandlers(t *testing.T) {
	registry := platform.NewRuntimeRegistry()
	selected := map[string]struct{}{
		"core": {}, "engine.website": {}, "worker.prerender": {},
	}
	config := appconfig.Resolved{}
	config.Coordinator.MaxAttempts = 3
	if err := registerWorkerHandlers(registry, selected, config); err != nil {
		t.Fatalf("register worker handlers: %v", err)
	}
	catalog, err := registry.CompileWorkerCatalog(platform.BuildPlan{Components: []string{"core", "engine.website", "worker.prerender"}})
	if err != nil || catalog == nil {
		t.Fatalf("compile worker catalog: %#v, %v", catalog, err)
	}
	if err := registerWorkerHandlers(registry, selected, config); err == nil {
		t.Fatal("duplicate worker handler registration was accepted")
	}
}
