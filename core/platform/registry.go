// Package platform contains in-process composition contracts.
package platform

import (
	"errors"
	"sync"

	"github.com/Zubrium/cdn/core/access"
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
type Module interface{ Operations() []Operation }
type Operation struct {
	Name         string
	Action       access.Action
	ResourceType string
}

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
