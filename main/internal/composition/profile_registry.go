package composition

import (
	"sort"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// ProfileRegistry keeps component metadata in the composition root and gives
// Core only opaque registrations needed for owner-scoped capabilities.
type ProfileRegistry struct {
	core        *platform.Registry
	descriptors []contracts.ComponentDescriptor
}

func NewProfileRegistry(coreRegistry *platform.Registry) *ProfileRegistry {
	return &ProfileRegistry{core: coreRegistry}
}

func (registry *ProfileRegistry) Register(id string, provider contracts.ComponentDescriptorProvider) error {
	if registry == nil || registry.core == nil || provider == nil {
		return platform.ErrInvalid
	}
	descriptor := provider.ComponentDescriptor()
	if descriptor.ID == "" || descriptor.Version < 1 {
		return platform.ErrInvalid
	}
	if err := registry.core.Register(id); err != nil {
		return err
	}
	descriptor.Dependencies = append([]string(nil), descriptor.Dependencies...)
	descriptor.Operations = append([]contracts.ComponentOperation(nil), descriptor.Operations...)
	descriptor.AccessGroups = append([]string(nil), descriptor.AccessGroups...)
	registry.descriptors = append(registry.descriptors, descriptor)
	return nil
}

func (registry *ProfileRegistry) ComponentDescriptors() []contracts.ComponentDescriptor {
	if registry == nil {
		return nil
	}
	result := append([]contracts.ComponentDescriptor(nil), registry.descriptors...)
	for index := range result {
		result[index].Dependencies = append([]string(nil), result[index].Dependencies...)
		result[index].Operations = append([]contracts.ComponentOperation(nil), result[index].Operations...)
		result[index].AccessGroups = append([]string(nil), result[index].AccessGroups...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
