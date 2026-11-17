package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/core/access"
	"github.com/jackc/pgx/v5/pgxpool"
)

type coordinatorTestModule struct{}

func (coordinatorTestModule) Operations() []Operation { return nil }

func TestRouterTargetCapabilityIsOptInAndIssuedOnce(t *testing.T) {
	runtime := &Runtime{database: &pgxpool.Pool{}, Registry: NewRegistry(), routeTargetChanges: make(chan string, 1)}
	if _, err := runtime.RouterTargetStore(); err == nil {
		t.Fatal("Router target capability was issued without a registered Router")
	}
	if err := runtime.Registry.Register("router", coordinatorTestModule{}); err != nil {
		t.Fatal(err)
	}
	store, err := runtime.RouterTargetStore()
	if err != nil || store == nil {
		t.Fatalf("registered Router did not receive its capability: store=%v err=%v", store, err)
	}
	if store.Changes() != (<-chan string)(runtime.routeTargetChanges) {
		t.Fatal("Router capability is not connected to Core's invalidation stream")
	}
	if _, err := runtime.RouterTargetStore(); err == nil {
		t.Fatal("Router target capability was issued more than once")
	}
}

func TestCoordinatorAuthorizationCapabilityIsOptInAndValidatesPolicyInputs(t *testing.T) {
	runtime := &Runtime{database: &pgxpool.Pool{}, Registry: NewRegistry()}
	capability := runtime.CoordinatorAuthorization()
	if _, err := capability.Authenticate(context.Background(), "invalid"); !errors.Is(err, ErrCoordinatorInvalid) {
		t.Fatalf("Coordinator identity capability was available without module registration: %v", err)
	}
	if err := runtime.Registry.Register("coordinator", coordinatorTestModule{}); err != nil {
		t.Fatal(err)
	}
	if _, err := capability.Authenticate(context.Background(), "invalid"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid credential was not rejected before database access: %v", err)
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
