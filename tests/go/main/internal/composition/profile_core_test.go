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
	r := platform.NewRegistry()
	if err := RegisterProfile(r); err != nil {
		t.Fatal(err)
	}
	if descriptors := r.ComponentDescriptors(); len(descriptors) != 0 {
		t.Fatalf("minimal profile unexpectedly declares engine access groups: %#v", descriptors)
	}
	for _, name := range []string{"router", "website", "content", "forms"} {
		if _, err := r.Lookup(name); !errors.Is(err, platform.ErrUnavailable) {
			t.Fatalf("%s unexpectedly available: %v", name, err)
		}
	}
}
