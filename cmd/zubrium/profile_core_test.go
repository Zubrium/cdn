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
	if _, err := r.Lookup("website"); !errors.Is(err, platform.ErrUnavailable) {
		t.Fatalf("website unexpectedly available: %v", err)
	}
}
