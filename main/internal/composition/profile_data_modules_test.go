//go:build full && !devfixtures && !router

package composition

import (
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
)

// The full product starts archive/media workers and the trusted publication
// fan-out. Every RegisterDataModule call in that composition must have its
// module identity registered before Core issues the scoped data capability.
func TestFullProfileRegistersDataModuleIdentities(t *testing.T) {
	registry := NewProfileRegistry(platform.NewRegistry())
	if err := RegisterProfile(registry); err != nil {
		t.Fatalf("register full profile: %v", err)
	}
	for _, id := range []string{"worker.archive", "worker.media", "worker.prerender", "publication", "retirement"} {
		if err := registry.core.Lookup(id); err != nil {
			t.Errorf("full profile must register %s before RegisterDataModule: %v", id, err)
		}
	}
}
