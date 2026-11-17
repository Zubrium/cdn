//go:build !full

package main

import (
	"errors"
	"github.com/Zubrium/cdn/core/platform"
	"testing"
)

func TestCoreProfileDoesNotEnableEngines(t *testing.T) {
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
