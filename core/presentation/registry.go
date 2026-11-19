// Package presentation exposes the Core capabilities and engine-neutral
// contracts used by the composition root and registered components.
package presentation

import (
	"errors"
	"sort"
	"sync"

	"github.com/Zubrium/cdn/core/domain/coordination"
)

var ErrDuplicate = errors.New("module already registered")
var ErrUnavailable = errors.New("module unavailable in this build")
var ErrInvalid = errors.New("invalid module registration")

type Registry struct {
	mu      sync.RWMutex
	modules map[string]Module
}

// Module exposes descriptive operations; engine-specific Go interfaces carry
// their typed requests and responses.
type Module = coordination.Module
type Operation = coordination.Operation

func NewRegistry() *Registry { return &Registry{modules: make(map[string]Module)} }
func (r *Registry) Register(name string, module Module) error {
	if name == "" || module == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.modules[name]; ok {
		return ErrDuplicate
	}
	r.modules[name] = module
	return nil
}
func (r *Registry) Lookup(name string) (Module, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	module, ok := r.modules[name]
	if !ok {
		return nil, ErrUnavailable
	}
	return module, nil
}

// ComponentDescriptors returns only generic workflow metadata declared by
// registered modules. Core does not infer component IDs or operations from
// module names.
func (r *Registry) ComponentDescriptors() []coordination.ComponentDescriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]coordination.ComponentDescriptor, 0)
	for _, module := range r.modules {
		provider, ok := module.(coordination.ComponentDescriptorProvider)
		if !ok {
			continue
		}
		descriptor := provider.ComponentDescriptor()
		descriptor.Dependencies = append([]string(nil), descriptor.Dependencies...)
		descriptor.Operations = append([]coordination.ComponentOperation(nil), descriptor.Operations...)
		descriptor.AccessGroups = append([]string(nil), descriptor.AccessGroups...)
		items = append(items, descriptor)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}
