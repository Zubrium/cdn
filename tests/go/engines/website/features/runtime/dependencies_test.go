package runtime_test

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func TestDependencyResolverPinsVersionsDigestsAndAPIs(t *testing.T) {
	catalog := []runtime.Candidate{
		{FeatureID: "api", Version: "1.2.0", Digest: "sha256:api-120", APIVersion: "v1"},
		{FeatureID: "api", Version: "1.3.0", Digest: "sha256:api-130", APIVersion: "v1"},
		{FeatureID: "api", Version: "2.0.0", Digest: "sha256:api-200", APIVersion: "v2"},
	}
	resolver, err := runtime.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	requirements := []runtime.Dependency{{FeatureID: "api", Range: "^1.0.0"}}
	lock, err := resolver.Resolve(requirements, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock) != 1 || lock[0].Version != "1.3.0" || lock[0].Digest != "sha256:api-130" || lock[0].APIVersion != "v1" {
		t.Fatalf("unexpected lock: %+v", lock)
	}

	previous := []runtime.LockEntry{{FeatureID: "api", Version: "1.2.0", Digest: "sha256:api-120", APIVersion: "v1"}}
	retained, err := resolver.Resolve(requirements, previous)
	if err != nil || retained[0].Version != "1.2.0" || retained[0].Digest != "sha256:api-120" {
		t.Fatalf("compatible lock was not retained: lock=%+v err=%v", retained, err)
	}
}

func TestDependencyResolverRejectsUnsafeClosures(t *testing.T) {
	resolver, err := runtime.NewResolver([]runtime.Candidate{
		{FeatureID: "api", Version: "1.0.0", Digest: "sha256:a", APIVersion: "v1", Depends: []runtime.Dependency{{FeatureID: "ui", Range: "^1.0.0"}}},
		{FeatureID: "ui", Version: "1.0.0", Digest: "sha256:b", APIVersion: "v1", Depends: []runtime.Dependency{{FeatureID: "api", Range: "^1.0.0"}}},
		{FeatureID: "solo", Version: "1.0.0", Digest: "sha256:c", APIVersion: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve([]runtime.Dependency{{FeatureID: "api", Range: "^1.0.0"}}, nil); !errors.Is(err, runtime.ErrCycle) {
		t.Fatalf("cycle: err=%v", err)
	}
	if _, err := resolver.Resolve([]runtime.Dependency{{FeatureID: "solo", Range: "^2.0.0"}}, nil); !errors.Is(err, runtime.ErrDependency) {
		t.Fatalf("unresolved range: err=%v", err)
	}
	conflict := []runtime.Dependency{
		{FeatureID: "solo", Range: "^1.0.0"},
		{FeatureID: "solo", Range: "^2.0.0"},
	}
	if _, err := resolver.Resolve(conflict, nil); !errors.Is(err, runtime.ErrDependency) {
		t.Fatalf("conflicting ranges: err=%v", err)
	}
	changed := []runtime.LockEntry{{FeatureID: "solo", Version: "1.0.0", Digest: "sha256:changed", APIVersion: "v1"}}
	if _, err := resolver.Resolve([]runtime.Dependency{{FeatureID: "solo", Range: "^1.0.0"}}, changed); !errors.Is(err, runtime.ErrLock) {
		t.Fatalf("changed imported digest: err=%v", err)
	}
}

func web013DependencyLockIsDeterministicAndFailsClosed(t *testing.T) {
	TestDependencyResolverPinsVersionsDigestsAndAPIs(t)
	TestDependencyResolverRejectsUnsafeClosures(t)
}

func web025DependencyLockIsDeterministicAndFailsClosed(t *testing.T) {
	TestDependencyResolverPinsVersionsDigestsAndAPIs(t)
	TestDependencyResolverRejectsUnsafeClosures(t)
}
