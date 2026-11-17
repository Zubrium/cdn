package appconfig

import (
	"strings"
	"testing"

	"github.com/Zubrium/cdn/coordinator"
)

func TestResolvedCoordinatorBindingsMatchCompiledPublicationProfile(t *testing.T) {
	minimal := coordinator.BuildPlan{Components: []string{"core", "coordinator", "engine.loader", "admin.files"}}
	publication := coordinator.BuildPlan{Components: []string{
		"core", "coordinator", "engine.website", "engine.content", "engine.router",
		"worker.prerender", "automation.site-publication",
	}}

	t.Run("minimal profile accepts no publication bindings", func(t *testing.T) {
		resolved := Resolved{Coordinator: CoordinatorSettings{Bindings: []coordinator.PublicationBinding{}}}
		if err := resolved.ValidateBuildPlan(minimal); err != nil {
			t.Fatalf("empty publication configuration rejected: %v", err)
		}
	})

	t.Run("bindings require publication components", func(t *testing.T) {
		resolved := Resolved{Coordinator: CoordinatorSettings{Bindings: []coordinator.PublicationBinding{{}}}}
		if err := resolved.ValidateBuildPlan(minimal); err == nil {
			t.Fatal("profile without publication runtime accepted configured route bindings")
		}
	})

	t.Run("automation requires at least one binding", func(t *testing.T) {
		resolved := Resolved{Coordinator: CoordinatorSettings{Bindings: []coordinator.PublicationBinding{}}}
		if err := resolved.ValidateBuildPlan(publication); err == nil {
			t.Fatal("publication profile without a route binding was accepted")
		}
	})

	t.Run("full publication configuration is accepted", func(t *testing.T) {
		resolved := Resolved{Coordinator: CoordinatorSettings{Bindings: []coordinator.PublicationBinding{{}}}}
		if err := resolved.ValidateBuildPlan(publication); err != nil {
			t.Fatalf("compatible publication profile rejected: %v", err)
		}
	})
}

func TestBuildProfileParticipatesInCoordinatorFingerprint(t *testing.T) {
	resolved := Resolved{CoordinatorFingerprint: strings.Repeat("a", 64), Coordinator: CoordinatorSettings{Bindings: []coordinator.PublicationBinding{}}}
	firstPlan := coordinator.BuildPlan{Components: []string{"core", "coordinator", "engine.loader"}}
	reorderedPlan := coordinator.BuildPlan{Components: []string{"engine.loader", "core", "coordinator"}}
	secondPlan := coordinator.BuildPlan{Components: []string{"core", "coordinator", "engine.loader", "worker.archive"}}

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
		t.Fatalf("different compiled components shared one Coordinator fingerprint: same=%v err=%v", second.CoordinatorFingerprint == first.CoordinatorFingerprint, err)
	}
}
