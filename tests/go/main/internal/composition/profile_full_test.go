//go:build full && !devfixtures && !router

package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	"testing"
)

func TestFullProfileRegistersEngineContractsAndOperations(t *testing.T) {
	r := platform.NewRegistry()
	if err := RegisterProfile(r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"router", "website", "content", "forms"} {
		module, err := r.Lookup(name)
		if err != nil || len(module.Operations()) == 0 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	groups := map[string]bool{}
	for _, descriptor := range r.ComponentDescriptors() {
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
