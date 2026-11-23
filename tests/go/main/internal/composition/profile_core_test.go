//go:build !full && !devfixtures && !router

package composition

import (
	"errors"
	platform "github.com/Zubrium/cdn/core/presentation"
	"testing"
)

func TestCoreProfileDoesNotEnableEngines(t *testing.T) {
	plan := BuildPlan()
	selected := make(map[string]bool, len(plan.Components))
	for _, component := range plan.Components {
		selected[component] = true
	}
	if !selected["admin.identity"] || !selected["admin.files"] {
		t.Fatalf("Admin identity API must share the File API listener in the minimal profile: %v", plan.Components)
	}
	r := NewProfileRegistry(platform.NewRegistry())
	if err := RegisterProfile(r); err != nil {
		t.Fatal(err)
	}
	// The minimal profile registers Loader for the File API, but it must not
	// declare any static access groups of its own.
	for _, descriptor := range r.ComponentDescriptors() {
		if descriptor.ID != "engine.loader" {
			t.Errorf("minimal profile unexpectedly declares component %q", descriptor.ID)
		}
		if len(descriptor.AccessGroups) != 0 {
			t.Errorf("component %q unexpectedly declares access groups: %v", descriptor.ID, descriptor.AccessGroups)
		}
	}
	for _, name := range []string{"router", "website", "content", "forms"} {
		if err := r.core.Lookup(name); !errors.Is(err, platform.ErrUnavailable) {
			t.Fatalf("%s unexpectedly available: %v", name, err)
		}
	}
}
