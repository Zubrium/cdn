//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"testing"
	"time"

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
	config.Scales.WorkerPools.Prerender.Enabled = true
	config.Scales.WorkerPools.Publication.Enabled = true
	operations, err := registerWorkerHandlers(selected, config, &coreComponent{}, nil)
	if err != nil {
		t.Fatalf("compile task operations: %v", err)
	}
	if _, exists := operations[contracts.Key{ID: "build-page", Version: 1}]; !exists {
		t.Fatalf("Website operation missing from static DataBus bindings: %v", operations)
	}
	child, exists := operations[contracts.Key{ID: "render-page", Version: 1}]
	if !exists || child.TaskClass != "worker.prerender" {
		t.Fatalf("prerender child binding missing or misclassified: %+v", operations)
	}
	if classes := compiledWorkerClasses(selected); len(classes) != 2 {
		t.Fatalf("compiled worker classes must include disabled classes too: %v", classes)
	}
}

func TestFullProfileDisabledClassKeepsResolvableFailClosedBinding(t *testing.T) {
	selected := map[string]struct{}{
		"core": {}, "engine.website": {}, "worker.prerender": {},
	}
	config := appconfig.Resolved{}
	config.Coordinator.MaxAttempts = 3
	config.Scales.WorkerPools.Publication.Enabled = true
	operations, err := registerWorkerHandlers(selected, config, &coreComponent{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	child, exists := operations[contracts.Key{ID: "render-page", Version: 1}]
	if !exists || child.TaskClass != "worker.prerender" {
		t.Fatalf("disabled prerender child binding is not resolvable: %+v", operations)
	}
	if _, err := child.Handler(context.Background(), contracts.OperationTask{}); err == nil {
		t.Fatal("disabled prerender handler executed work")
	}
}

func TestFullProfileBuildsOnlyEnabledIsolatedPools(t *testing.T) {
	selected := map[string]struct{}{
		"core": {}, "worker.media": {}, "worker.prerender": {},
	}
	config := appconfig.Resolved{}
	config.Coordinator.MaxAttempts = 3
	config.Scales.WorkerPools.Media = appconfig.ResolvedWorkerPool{Enabled: true, Limit: 2,
		PollInterval: time.Millisecond, TaskTimeout: time.Minute, LeaseDuration: 4 * time.Second}
	if _, err := workerTaskPools(config, nil, nil); err == nil {
		t.Fatal("workerTaskPools accepted a nil dispatcher for an enabled pool")
	}
	config.Scales.WorkerPools.Prerender = config.Scales.WorkerPools.Media
	operations, err := registerWorkerHandlers(selected, config, &coreComponent{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 3 {
		t.Fatalf("expected media (v1/v2) and prerender registry bindings, got %v", operations)
	}
	pools, err := workerTaskPools(config, nil, operations)
	if err == nil || len(pools) != 0 {
		t.Fatalf("workerTaskPools built pools without a dispatcher: %+v %v", pools, err)
	}
	if classes := compiledWorkerClasses(selected); len(classes) != 2 {
		t.Fatalf("compiled worker classes must include disabled classes too: %v", classes)
	}
}
