package composition

import (
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type profileDescriptorFixture struct{ descriptor contracts.ComponentDescriptor }

func (fixture profileDescriptorFixture) ComponentDescriptor() contracts.ComponentDescriptor {
	return fixture.descriptor
}

func TestProfileMetadataLivesOutsideCoreAndIsReturnedByCopy(t *testing.T) {
	coreRegistry := platform.NewRegistry()
	profile := NewProfileRegistry(coreRegistry)
	descriptor := contracts.ComponentDescriptor{
		ID: "engine.example", Kind: "engine", Version: 1,
		Operations:   []contracts.ComponentOperation{{ID: "example.read", ContractVersion: 1}},
		AccessGroups: []string{"example"},
	}
	if err := profile.Register("example", profileDescriptorFixture{descriptor: descriptor}); err != nil {
		t.Fatalf("register profile descriptor: %v", err)
	}
	if err := coreRegistry.Lookup("example"); err != nil {
		t.Fatalf("Core did not receive opaque owner identity: %v", err)
	}
	metadata := profile.ComponentDescriptors()
	if len(metadata) != 1 || metadata[0].ID != "engine.example" || metadata[0].Operations[0].ID != "example.read" {
		t.Fatalf("profile metadata = %#v", metadata)
	}
	metadata[0].Operations[0].ID = "mutated"
	metadata[0].AccessGroups[0] = "mutated"
	again := profile.ComponentDescriptors()[0]
	if again.Operations[0].ID != "example.read" || again.AccessGroups[0] != "example" {
		t.Fatal("caller mutated registered profile metadata")
	}
}

func TestProfileRegistryRejectsMismatchedAndDuplicateOwners(t *testing.T) {
	profile := NewProfileRegistry(platform.NewRegistry())
	if err := profile.Register("other", profileDescriptorFixture{descriptor: contracts.ComponentDescriptor{
		ID: "component", Version: 0,
	}}); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("invalid owner metadata = %v", err)
	}
	fixture := profileDescriptorFixture{descriptor: contracts.ComponentDescriptor{ID: "component", Version: 1}}
	if err := profile.Register("component", fixture); err != nil {
		t.Fatal(err)
	}
	if err := profile.Register("component", fixture); !errors.Is(err, platform.ErrDuplicate) {
		t.Fatalf("duplicate owner registration = %v", err)
	}
}
