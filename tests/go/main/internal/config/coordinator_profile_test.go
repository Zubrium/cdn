package appconfig

import (
	"strings"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	routermodel "github.com/Zubrium/cdn/engines/router/features/routes/model"
)

func TestCoordinatorAdminAPIRequiresConfiguredAccessGroup(t *testing.T) {
	plan := platform.BuildPlan{Components: []string{"core", "engine.loader", "admin.files", "admin.coordinator"}}
	resolved := Resolved{Coordinator: CoordinatorSettings{JobsPageDefault: 25, JobsPageMaximum: 100}}
	if err := resolved.ValidateBuildPlan(plan); err == nil {
		t.Fatal("Coordinator Admin API without a configured group was accepted")
	}
	resolved.Coordinator.JobAccessGroup = "coordinator_jobs"
	resolved.Core.AccessGroups = map[string]platform.AccessLevel{"coordinator_jobs": platform.AccessLevelNone}
	if err := resolved.ValidateBuildPlan(plan); err != nil {
		t.Fatalf("explicitly configured zero-access group should remain valid: %v", err)
	}
	delete(resolved.Core.AccessGroups, "coordinator_jobs")
	if err := resolved.ValidateBuildPlan(plan); err == nil {
		t.Fatal("Coordinator API group missing from Core config was accepted")
	}
}

func TestPublicRouterLeavesListenerExposureToDeployment(t *testing.T) {
	resolved := Resolved{
		Core: platform.RuntimeConfig{
			AccessGroups: map[string]platform.AccessLevel{"router_routes": platform.AccessLevelWrite},
			ModuleData:   platform.ModuleDataLimits{MaxSelectorValues: routermodel.MaxRoutePathSelectorValues},
		},
		RouterAPI: RouterAPISettings{
			BranchID:            "main",
			ServiceCredential:   "router-service-credential",
			PublicListenAddress: "0.0.0.0:8081",
		},
	}
	plan := platform.BuildPlan{Components: []string{"engine.router", "public.router"}}
	if err := resolved.ValidateBuildPlan(plan); err != nil {
		t.Fatalf("Router must leave listener exposure and proxy topology to deployment configuration: %v", err)
	}
}

func TestBuildProfileParticipatesInCoordinatorFingerprint(t *testing.T) {
	resolved := Resolved{CoordinatorFingerprint: strings.Repeat("a", 64)}
	firstPlan := platform.BuildPlan{Components: []string{"core", "engine.loader"}}
	reorderedPlan := platform.BuildPlan{Components: []string{"engine.loader", "core"}}
	secondPlan := platform.BuildPlan{Components: []string{"core", "engine.loader", "worker.archive"}}

	first, err := resolved.WithBuildPlan(firstPlan)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := resolved.WithBuildPlan(reorderedPlan)
	if err != nil || reordered.CoordinatorFingerprint != first.CoordinatorFingerprint {
		t.Fatalf("component order changed the build fingerprint: same=%v err=%v", reordered.CoordinatorFingerprint == first.CoordinatorFingerprint, err)
	}
	second, err := resolved.WithBuildPlan(secondPlan)
	if err != nil || second.CoordinatorFingerprint == first.CoordinatorFingerprint {
		t.Fatalf("different compiled components shared a fingerprint: same=%v err=%v", second.CoordinatorFingerprint == first.CoordinatorFingerprint, err)
	}
}
