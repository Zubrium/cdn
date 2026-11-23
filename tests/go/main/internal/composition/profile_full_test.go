//go:build full && !devfixtures && !router

package composition

import (
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestFullProfileRegistersEngineContractsAndOperations(t *testing.T) {
	r := NewProfileRegistry(platform.NewRegistry())
	if err := RegisterProfile(r); err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for _, descriptor := range r.ComponentDescriptors() {
		if descriptor.Kind == "engine" && len(descriptor.AccessGroups) == 0 && descriptor.ID != "engine.loader" {
			t.Errorf("%s has no static profile metadata", descriptor.ID)
		}
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
	selected := map[string]struct{}{
		"core": {}, "engine.website": {}, "worker.prerender": {},
	}
	config := appconfig.Resolved{}
	config.Coordinator.MaxAttempts = 3
	operations, err := registerWorkerHandlers(selected, config, &coreComponent{}, nil)
	if err != nil {
		t.Fatalf("compile task operations: %v", err)
	}
	if _, exists := operations[contracts.Key{ID: "build-page", Version: 1}]; !exists {
		t.Fatalf("Website operation missing from static DataBus bindings: %v", operations)
	}
	if _, exists := operations[contracts.Key{ID: "render-page", Version: 1}]; !exists {
		t.Fatalf("prerender operation missing from static DataBus bindings: %v", operations)
	}
}
