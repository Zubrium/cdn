//go:build router && !full && !devfixtures

package main

import "testing"

func TestRouterProfileHasRunnableProductionComposition(t *testing.T) {
	plan := compiledBuildPlan()
	selected := make(map[string]struct{}, len(plan.Components))
	for _, id := range plan.Components {
		selected[id] = struct{}{}
	}
	if err := validateRunnableComposition(selected); err != nil {
		t.Fatalf("Router profile is not runnable: %v", err)
	}
	for _, id := range []string{"engine.router", "admin.router", "public.router", "public.form-upload"} {
		if _, ok := selected[id]; !ok {
			t.Errorf("Router profile omitted %s: %v", id, plan.Components)
		}
	}
	for _, id := range []string{"development.fixtures", "engine.website", "engine.content", "engine.forms"} {
		if _, ok := selected[id]; ok {
			t.Errorf("production Router profile selected %s: %v", id, plan.Components)
		}
	}
}
