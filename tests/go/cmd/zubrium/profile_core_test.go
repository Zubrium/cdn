//go:build !full && !devfixtures && !router

package main

import (
	"errors"
	platform "github.com/Zubrium/cdn/core/presentation"
	"testing"
)

func TestCoreProfileDoesNotEnableEngines(t *testing.T) {
	plan := compiledBuildPlan()
	selected := make(map[string]bool, len(plan.Components))
	for _, component := range plan.Components {
		selected[component] = true
	}
	if !selected["admin.identity"] || !selected["admin.files"] {
		t.Fatalf("Admin identity API must share the File API listener in the minimal profile: %v", plan.Components)
	}
	r := platform.NewRegistry()
	if err := registerProfile(r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"router", "website", "content", "forms"} {
		if _, err := r.Lookup(name); !errors.Is(err, platform.ErrUnavailable) {
			t.Fatalf("%s unexpectedly available: %v", name, err)
		}
	}
}
