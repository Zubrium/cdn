package composition

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/google/uuid"
)

type testApplicationReadiness bool

func (ready testApplicationReadiness) Ready() bool { return bool(ready) }

func assertSilentFailureStream(t *testing.T, source <-chan error) {
	t.Helper()
	if source == nil {
		t.Fatal("component exposed a nil failure stream")
	}
	select {
	case err, ok := <-source:
		t.Fatalf("component emitted an unexpected failure: ok=%t err=%v", ok, err)
	default:
	}
}

func TestMergeProfileAccessGroupsUnionsSourcesAndPreservesYAMLLevel(t *testing.T) {
	groups, err := mergeProfileAccessGroups(map[string]platform.AccessLevel{
		"router_routes": platform.AccessLevelNone,
		"integration":   platform.AccessLevelRead,
	}, []contracts.ComponentDescriptor{
		{ID: "engine.router", AccessGroups: []string{"router_routes"}},
		{ID: "engine.forms", AccessGroups: []string{"forms_schemas", "forms_submissions"}},
		{ID: "engine.content", AccessGroups: []string{"content"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]platform.AccessLevel{
		"router_routes":     platform.AccessLevelNone,
		"integration":       platform.AccessLevelRead,
		"forms_schemas":     platform.AccessLevelWrite,
		"forms_submissions": platform.AccessLevelWrite,
		"content":           platform.AccessLevelWrite,
	}
	if len(groups) != len(want) {
		t.Fatalf("merged groups = %#v, want %#v", groups, want)
	}
	for key, level := range want {
		if groups[key] != level {
			t.Errorf("merged level for %q = %d, want %d", key, groups[key], level)
		}
	}
}

func TestMergeProfileAccessGroupsRejectsInvalidEngineDeclaration(t *testing.T) {
	if _, err := mergeProfileAccessGroups(nil, []contracts.ComponentDescriptor{{ID: "engine.example", AccessGroups: []string{"invalid group"}}}); err == nil {
		t.Fatal("invalid engine group declaration was accepted")
	}
}

func TestWebsiteCanRequestOnlyFeatureAndConfiguredWorkflowGrants(t *testing.T) {
	descriptors := []contracts.ComponentDescriptor{{ID: "engine.website", AccessGroups: []string{"features"}}}
	selected := map[string]struct{}{"engine.website": {}, "admin.coordinator": {}}
	configured := map[string]platform.AccessLevel{
		"features": platform.AccessLevelWrite, "site_jobs": platform.AccessLevelRead, "unrelated": platform.AccessLevelWrite,
	}
	groups := moduleCoreAccessGroups(descriptors, selected, configured, "site_jobs")
	if got := groups["engine.website"]; !slices.Equal(got, []string{"features", "site_jobs"}) {
		t.Fatalf("Website access groups = %v, want features and site_jobs", got)
	}
}

func TestComponentLifecycleAdaptersFailClosedBeforeInitialization(t *testing.T) {
	ctx := context.Background()
	var core *coreComponent
	if err := core.Ready(ctx); err == nil {
		t.Fatal("Core component reported ready before runtime startup")
	}
	if err := core.ActivateAfterComponents(ctx); !errors.Is(err, contracts.ErrWorkflowUnavailable) {
		t.Fatalf("uninitialized Core activated Coordinator: %v", err)
	}
	if err := core.QuiesceBeforeComponents(ctx); err != nil {
		t.Fatalf("quiescing an uninitialized Core failed: %v", err)
	}
	if err := core.Stop(ctx); err != nil || core.IsReady() {
		t.Fatalf("stopping an uninitialized Core: err=%v ready=%t", err, core.IsReady())
	}
	assertSilentFailureStream(t, core.Errors())

	loader := &loaderComponent{}
	if err := loader.Start(ctx); err == nil {
		t.Fatal("Loader started without a Core capability")
	}
	if err := loader.Ready(ctx); err == nil || loader.IsReady() {
		t.Fatalf("uninitialized Loader state: err=%v ready=%t", err, loader.IsReady())
	}
	if err := loader.Stop(ctx); err != nil {
		t.Fatalf("stopping an uninitialized Loader failed: %v", err)
	}

	fileAPI := &fileAPIComponent{}
	if err := fileAPI.Start(ctx); err == nil {
		t.Fatal("File API started without the Loader service")
	}
	if err := fileAPI.Ready(ctx); err == nil || fileAPI.IsReady() {
		t.Fatalf("uninitialized File API state: err=%v ready=%t", err, fileAPI.IsReady())
	}
	fileAPI.setApplicationReadiness(testApplicationReadiness(false))
	if err := fileAPI.Stop(ctx); err != nil {
		t.Fatalf("stopping an uninitialized File API failed: %v", err)
	}
	assertSilentFailureStream(t, fileAPI.Errors())
	var nilFileAPI *fileAPIComponent
	if nilFileAPI.IsReady() {
		t.Fatal("nil File API reported ready")
	}
}

func TestWorkflowControlFailsClosedWithoutCoreRuntimeAndRegistrationStaysScoped(t *testing.T) {
	ctx := context.Background()
	workflowControl := adminWorkflowControl{}
	if _, err := workflowControl.List(ctx, contracts.AdminWorkflowListFilter{Limit: 10}); !errors.Is(err, contracts.ErrWorkflowUnavailable) {
		t.Fatalf("workflow listing without Core runtime did not fail closed: %v", err)
	}
	if _, err := workflowControl.Get(ctx, uuid.New(), contracts.AdminWorkflowDetailFilter{Limit: 10}); !errors.Is(err, contracts.ErrWorkflowUnavailable) {
		t.Fatalf("workflow detail without Core runtime did not fail closed: %v", err)
	}
	if err := workflowControl.RetryTask(ctx, uuid.New(), uuid.New()); !errors.Is(err, contracts.ErrWorkflowUnavailable) {
		t.Fatalf("task retry without Core runtime did not fail closed: %v", err)
	}

	plan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files", "admin.coordinator"}}
	resolved := appconfig.Resolved{Coordinator: appconfig.CoordinatorSettings{
		WorkflowAccessGroup: "coordinator_jobs", WorkflowsPageDefault: 20, WorkflowsPageMaximum: 100,
	}}
	resolved.AdminAPI.AllowedOrigins = []string{"http://127.0.0.1:8080"}
	resolved.Scales.Bus.MaxInFlight = 8
	resolved.Core.AccessGroups = map[string]platform.AccessLevel{"coordinator_jobs": platform.AccessLevelWrite}
	if err := resolved.ValidateBuildPlan(plan); err != nil {
		t.Fatalf("configured Coordinator profile is invalid: %v", err)
	}
	registry, core, loader, fileAPI, err := registerComponents(resolved, plan, nil)
	if err != nil {
		t.Fatalf("register compiled Coordinator composition: %v", err)
	}
	if registry == nil || core == nil || loader == nil || fileAPI == nil || fileAPI.workflowControl == nil {
		t.Fatalf("registered composition omitted a selected component: registry=%t core=%t loader=%t fileAPI=%t workflowControl=%t",
			registry != nil, core != nil, loader != nil, fileAPI != nil, fileAPI != nil && fileAPI.workflowControl != nil)
	}
	if _, _, _, _, err := registerComponents(resolved, platform.BuildPlan{}, nil); !errors.Is(err, platform.ErrCoordinatorInputInvalid) {
		t.Fatalf("empty build plan was accepted by component registration: %v", err)
	}
	if err := (appconfig.Resolved{}).ValidateBuildPlan(plan); err == nil {
		t.Fatal("Coordinator Admin profile accepted without an authorized group in startup config")
	}
}

func TestIdentityAdminIsRegisteredOnlyWhenSelectedAndSharesTheAdminListener(t *testing.T) {
	basePlan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files"}}
	withIdentity := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files", "admin.identity"}}

	resolved := appconfig.Resolved{}
	resolved.Scales.Bus.MaxInFlight = 8
	baseRegistry, _, _, baseAPI, err := registerComponents(resolved, basePlan, nil)
	if err != nil {
		t.Fatalf("register File API without identity API: %v", err)
	}
	if baseRegistry == nil || baseAPI == nil || baseAPI.identityAdmin {
		t.Fatal("unselected identity API was registered")
	}

	registry, _, _, api, err := registerComponents(resolved, withIdentity, nil)
	if err != nil {
		t.Fatalf("register identity API: %v", err)
	}
	if registry == nil || api == nil || !api.identityAdmin {
		t.Fatal("selected identity API was not connected to the shared Admin listener")
	}
}

// TestComponentRegistrationWiresObservers guards the production metric path:
// Core cache and Loader admission observers must come from the same deployed
// observer, and registration must not invent one when composition has none.
func TestComponentRegistrationWiresObserversIntoCoreAndLoader(t *testing.T) {
	observer, err := observability.New(observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	plan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files"}}
	resolved := appconfig.Resolved{}
	resolved.Scales.Bus.MaxInFlight = 8

	_, core, loader, _, err := registerComponents(resolved, plan, observer)
	if err != nil {
		t.Fatalf("register composition with observer: %v", err)
	}
	if core.observer == nil || loader.observer == nil {
		t.Fatalf("observer was not wired into the metrics paths: core=%t loader=%t", core.observer != nil, loader.observer != nil)
	}

	_, coreWithout, loaderWithout, _, err := registerComponents(resolved, plan, nil)
	if err != nil {
		t.Fatalf("register composition without observer: %v", err)
	}
	if coreWithout.observer != nil || loaderWithout.observer != nil {
		t.Fatal("registration silently invented an observer instead of failing closed at Start")
	}
}

func TestComponentRegistrationFailsClosedForUnimplementedSelectedAPIs(t *testing.T) {
	plan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files", "engine.unknown"}}
	resolved := appconfig.Resolved{}
	resolved.Scales.Bus.MaxInFlight = 8
	_, _, _, _, err := registerComponents(resolved, plan, nil)
	if err == nil || !strings.Contains(err.Error(), "has no runtime implementation") {
		t.Fatalf("unimplemented component was silently omitted instead of failing startup: %v", err)
	}
}
