package bus_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type fakeCoreAccess struct{ calls int }

func (access *fakeCoreAccess) Authenticate(_ context.Context, credential string) (contracts.Principal, error) {
	if credential == "" {
		return contracts.Principal{}, contracts.ErrAccessUnauthorized
	}
	access.calls++
	return contracts.Principal{ID: "principal-1", Active: true}, nil
}

func (access *fakeCoreAccess) AuthenticateSession(_ context.Context, token string) (contracts.Principal, error) {
	if token == "" {
		return contracts.Principal{}, contracts.ErrAccessUnauthorized
	}
	access.calls++
	return contracts.Principal{ID: "principal-1", Active: true}, nil
}

func (access *fakeCoreAccess) RequireGroupLevel(_ context.Context, _ contracts.AuthorizationSubject, _ []string, _ contracts.AccessLevel) error {
	access.calls++
	return nil
}

func TestCoreAccessBusBindsModuleAndDeclaredGroups(t *testing.T) {
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 1},
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
	)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeCoreAccess{}
	if err := bus.RegisterCoreAccess(dispatcher, backend, map[string][]string{"engine.router": {"router_routes"}}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	client, err := contracts.NewCoreAccessClient(bus.NewModuleDispatcher(dispatcher, "engine.router"))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := client.AuthenticateSession(context.Background(), "token")
	if err != nil || principal.ID != "principal-1" {
		t.Fatalf("AuthenticateSession() = %#v, %v", principal, err)
	}
	if err := client.RequireGroupLevel(context.Background(), contracts.AuthorizationSubject{SessionToken: "token"}, []string{"router_routes"}, contracts.AccessLevelRead); err != nil {
		t.Fatalf("declared group authorization failed: %v", err)
	}
	if err := client.RequireGroupLevel(context.Background(), contracts.AuthorizationSubject{SessionToken: "token"}, []string{"content"}, contracts.AccessLevelRead); !errors.Is(err, contracts.ErrAccessForbidden) {
		t.Fatalf("undeclared group authorization error = %v, want forbidden", err)
	}
	if backend.calls != 2 {
		t.Fatalf("Core access backend calls = %d, want 2; forbidden group must be rejected in bus", backend.calls)
	}
}

func TestCoreAccessBusRejectsUnboundModule(t *testing.T) {
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 1},
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterCoreAccess(dispatcher, &fakeCoreAccess{}, map[string][]string{"engine.router": {"router_routes"}}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	client, err := contracts.NewCoreAccessClient(dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Authenticate(context.Background(), "credential"); !errors.Is(err, bus.ErrInvalidModuleIdentity) {
		t.Fatalf("unbound Authenticate() error = %v, want invalid module identity", err)
	}
}
