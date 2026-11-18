package presentation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Zubrium/cdn/core/domain/access"
	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/jackc/pgx/v5/pgxpool"
)

type coordinatorTestModule struct{}

func (coordinatorTestModule) Operations() []Operation { return nil }

type componentDescriptorTestModule struct {
	descriptor corecoord.ComponentDescriptor
}

func (m componentDescriptorTestModule) Operations() []Operation { return nil }
func (m componentDescriptorTestModule) ComponentDescriptor() corecoord.ComponentDescriptor {
	return m.descriptor
}

func TestRegistryExposesOnlyExplicitGenericComponentDescriptors(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("unannotated", coordinatorTestModule{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("module-b", componentDescriptorTestModule{descriptor: corecoord.ComponentDescriptor{
		ID: "component.b", Kind: "worker", Version: 2,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("module-a", componentDescriptorTestModule{descriptor: corecoord.ComponentDescriptor{
		ID: "component.a", Kind: "engine", Version: 1,
		Operations: []corecoord.ComponentOperation{{ID: "prepare", ContractVersion: 1}},
	}}); err != nil {
		t.Fatal(err)
	}
	got := registry.ComponentDescriptors()
	want := []corecoord.ComponentDescriptor{
		{ID: "component.a", Kind: "engine", Version: 1, Operations: []corecoord.ComponentOperation{{ID: "prepare", ContractVersion: 1}}},
		{ID: "component.b", Kind: "worker", Version: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generic component descriptors = %#v, want %#v", got, want)
	}
	got[0].Operations[0].ID = "caller-mutation"
	if registry.ComponentDescriptors()[0].Operations[0].ID != "prepare" {
		t.Fatal("caller mutated registered operation descriptors")
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
