package presentation

import (
	"context"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRegistryStoresOpaqueOwnerIdentities(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("owner.example"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Lookup("owner.example"); err != nil {
		t.Fatalf("registered owner is not available: %v", err)
	}
	if err := registry.Lookup("other.example"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unregistered owner lookup = %v, want unavailable", err)
	}
}

func TestCoordinatorAuthorizationCapabilityIsOptInAndValidatesPolicyInputs(t *testing.T) {
	runtime := &Runtime{database: &pgxpool.Pool{}, Registry: NewRegistry()}
	capability := runtime.CoordinatorAuthorization()
	if _, err := capability.Authenticate(context.Background(), "invalid"); !errors.Is(err, ErrCoordinatorInvalid) {
		t.Fatalf("Coordinator identity capability was available without module registration: %v", err)
	}
	if err := runtime.Registry.Register("coordinator"); err != nil {
		t.Fatal(err)
	}
	if _, err := capability.Authenticate(context.Background(), "invalid"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing Core identity adapter did not fail closed: %v", err)
	}
	for _, input := range []struct {
		group string
		level access.Level
	}{
		{group: "invalid group", level: access.LevelRead},
		{group: "coordinator_jobs", level: access.LevelNone},
		{group: "coordinator_jobs", level: access.Level(3)},
	} {
		if err := capability.RequireGroupLevel(context.Background(), Session{}, input.group, input.level); !errors.Is(err, ErrCoordinatorInvalid) {
			t.Errorf("invalid Coordinator authorization request was accepted: group=%q level=%d err=%v", input.group, input.level, err)
		}
	}
}
