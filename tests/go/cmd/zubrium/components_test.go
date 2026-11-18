package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appconfig "github.com/Zubrium/cdn/cmd/zubrium/config"
	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/google/uuid"
)

type testApplicationReadiness bool

func (r testApplicationReadiness) Ready() bool { return bool(r) }

func TestFileAPIStaysClosedUntilCoreCoordinatorIsReady(t *testing.T) {
	called := false
	component := &fileAPIComponent{ready: true, applicationReadiness: testApplicationReadiness(true)}
	handler := component.requireCoreRuntime(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/files", nil))
	if response.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("File API served without Core's Coordinator: status=%d handlerCalled=%v", response.Code, called)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("Core unavailable response is cacheable or lacks retry guidance: %v", response.Header())
	}
}

func TestSyntheticDataModeMarksResponsesAndDisablesCaching(t *testing.T) {
	handler := syntheticDataMode(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "synthetic write unavailable", http.StatusServiceUnavailable)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v2/admin/content/records", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("synthetic handler status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Header().Get("X-Zubrium-Data-Mode") != "synthetic" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("synthetic response is not identified or was cacheable: %v", response.Header())
	}
}

func TestFileAPIStaysClosedWhenComposedApplicationLosesReadiness(t *testing.T) {
	called := false
	component := &fileAPIComponent{ready: true, applicationReadiness: testApplicationReadiness(false)}
	response := httptest.NewRecorder()
	component.requireCoreRuntime(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(
		response, httptest.NewRequest(http.MethodGet, "/api/v2/files", nil),
	)
	if response.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("File API served after composed application lost readiness: status=%d handlerCalled=%v", response.Code, called)
	}
}

func TestComponentLifecycleAdaptersFailClosedBeforeInitialization(t *testing.T) {
	ctx := context.Background()
	var core *coreComponent
	if err := core.Ready(ctx); err == nil {
		t.Fatal("Core component reported ready before runtime startup")
	}
	if err := core.ActivateAfterComponents(ctx); !errors.Is(err, platform.ErrCoordinatorNotReady) {
		t.Fatalf("uninitialized Core activated Coordinator: %v", err)
	}
	if err := core.QuiesceBeforeComponents(ctx); err != nil {
		t.Fatalf("quiescing an uninitialized Core failed: %v", err)
	}
	if err := core.Stop(ctx); err != nil || core.IsReady() {
		t.Fatalf("stopping an uninitialized Core: err=%v ready=%t", err, core.IsReady())
	}
	if _, open := <-core.Errors(); open {
		t.Fatal("uninitialized Core exposed a live failure stream")
	}

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
	if fileAPI.applicationReady() {
		t.Fatal("File API accepted an application that has lost readiness")
	}
	if err := fileAPI.Stop(ctx); err != nil {
		t.Fatalf("stopping an uninitialized File API failed: %v", err)
	}
	if _, open := <-fileAPI.Errors(); open {
		t.Fatal("uninitialized File API exposed a live failure stream")
	}
	var nilFileAPI *fileAPIComponent
	if nilFileAPI.applicationReady() || nilFileAPI.IsReady() {
		t.Fatal("nil File API reported ready")
	}
	nilFileAPI.setApplicationReadiness(testApplicationReadiness(true))
}

func TestCoordinatorAdminComponentServesOnlyAfterStart(t *testing.T) {
	var nilComponent *coordinatorAdminComponent
	if err := nilComponent.Start(context.Background()); !errors.Is(err, platform.ErrCoordinatorInputInvalid) {
		t.Fatalf("nil Coordinator Admin component start: %v", err)
	}
	if err := nilComponent.Ready(context.Background()); err == nil || nilComponent.IsReady() {
		t.Fatalf("nil Coordinator Admin component state: err=%v ready=%t", err, nilComponent.IsReady())
	}
	if err := nilComponent.Stop(context.Background()); err != nil {
		t.Fatalf("stop nil Coordinator Admin component: %v", err)
	}
	response := httptest.NewRecorder()
	nilComponent.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/coordinator/jobs", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("uninitialized Coordinator Admin returned %d, want 503", response.Code)
	}

	called := false
	component := &coordinatorAdminComponent{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})}
	if err := component.Ready(context.Background()); err == nil {
		t.Fatal("Coordinator Admin passed readiness before start")
	}
	if err := component.Start(context.Background()); err != nil {
		t.Fatalf("start Coordinator Admin component: %v", err)
	}
	if err := component.Ready(context.Background()); err != nil || !component.IsReady() {
		t.Fatalf("started Coordinator Admin readiness: ready=%t err=%v", component.IsReady(), err)
	}
	response = httptest.NewRecorder()
	component.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/coordinator/jobs", nil))
	if response.Code != http.StatusAccepted || !called {
		t.Fatalf("started Coordinator Admin did not forward the request: status=%d called=%t", response.Code, called)
	}
	if err := component.Stop(context.Background()); err != nil || component.IsReady() {
		t.Fatalf("stop Coordinator Admin: ready=%t err=%v", component.IsReady(), err)
	}
	called = false
	response = httptest.NewRecorder()
	component.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/coordinator/jobs", nil))
	if response.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("stopped Coordinator Admin served a request: status=%d called=%t", response.Code, called)
	}
}

func TestCoordinatorAdaptersAndComponentRegistrationStayScoped(t *testing.T) {
	ctx := context.Background()
	authorization := coreCoordinatorGroupAuthorization{}
	if _, err := authorization.Authenticate(ctx, "credential"); !errors.Is(err, platform.ErrUnavailable) {
		t.Fatalf("group authorization without Core runtime did not fail closed: %v", err)
	}
	if err := authorization.RequireGroupLevel(ctx, platform.Session{}, "content", platform.AccessLevelRead); !errors.Is(err, platform.ErrUnavailable) {
		t.Fatalf("group-level check without Core runtime did not fail closed: %v", err)
	}
	jobControl := liveCoordinatorJobControl{}
	if _, err := jobControl.List(ctx, platform.JobListFilter{Limit: 10}); !errors.Is(err, platform.ErrCoordinatorNotReady) {
		t.Fatalf("job listing without Core runtime did not fail closed: %v", err)
	}
	if err := jobControl.Retry(ctx, uuid.New()); !errors.Is(err, platform.ErrCoordinatorNotReady) {
		t.Fatalf("job retry without Core runtime did not fail closed: %v", err)
	}

	plan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files", "admin.coordinator"}}
	resolved := appconfig.Resolved{Coordinator: appconfig.CoordinatorSettings{
		JobAccessGroup: "coordinator_jobs", JobsPageDefault: 20, JobsPageMaximum: 100,
	}}
	resolved.AdminAPI.AllowedOrigins = []string{"http://127.0.0.1:8080"}
	resolved.Core.AccessGroups = map[string]platform.AccessLevel{"coordinator_jobs": platform.AccessLevelWrite}
	registry, core, loader, fileAPI, err := registerComponents(resolved, plan)
	if err != nil {
		t.Fatalf("register compiled Coordinator composition: %v", err)
	}
	if registry == nil || core == nil || loader == nil || fileAPI == nil || fileAPI.coordinatorAdmin == nil {
		t.Fatalf("registered composition omitted a selected component: registry=%t core=%t loader=%t fileAPI=%t coordinatorAdmin=%t",
			registry != nil, core != nil, loader != nil, fileAPI != nil, fileAPI != nil && fileAPI.coordinatorAdmin != nil)
	}
	if _, _, _, _, err := registerComponents(resolved, platform.BuildPlan{}); !errors.Is(err, platform.ErrCoordinatorInputInvalid) {
		t.Fatalf("empty build plan was accepted by component registration: %v", err)
	}
	if _, _, _, _, err := registerComponents(appconfig.Resolved{}, plan); err == nil {
		t.Fatal("Coordinator Admin registered without an authorized group in startup config")
	}
}

func TestIdentityAdminIsRegisteredOnlyWhenSelectedAndSharesTheAdminListener(t *testing.T) {
	basePlan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files"}}
	withIdentity := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files", "admin.identity"}}

	baseRegistry, _, _, baseAPI, err := registerComponents(appconfig.Resolved{}, basePlan)
	if err != nil {
		t.Fatalf("register File API without identity API: %v", err)
	}
	if baseRegistry == nil || baseAPI == nil || baseAPI.identityAdmin != nil {
		t.Fatal("unselected identity API was registered")
	}

	registry, _, _, api, err := registerComponents(appconfig.Resolved{}, withIdentity)
	if err != nil {
		t.Fatalf("register identity API: %v", err)
	}
	if registry == nil || api == nil || api.identityAdmin == nil {
		t.Fatal("selected identity API was not connected to the shared Admin listener")
	}
}

func TestComponentRegistrationFailsClosedForUnimplementedSelectedAPIs(t *testing.T) {
	plan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files", "engine.content", "admin.content"}}
	_, _, _, _, err := registerComponents(appconfig.Resolved{}, plan)
	if err == nil || !strings.Contains(err.Error(), "has no runtime implementation") {
		t.Fatalf("selected Content API was silently omitted instead of failing startup: %v", err)
	}
}
