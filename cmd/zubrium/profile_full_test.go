//go:build full

package main

import (
	"github.com/Zubrium/cdn/core/platform"
	"testing"
)

func TestFullProfileRegistersBoundariesWithoutPretendingOperationsExist(t *testing.T) {
	r := platform.NewRegistry()
	if err := registerProfile(r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"website", "content", "forms"} {
		module, err := r.Lookup(name)
		if err != nil || len(module.Operations()) != 0 {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
