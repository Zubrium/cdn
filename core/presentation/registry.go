// Package presentation exposes the Core capabilities and engine-neutral
// contracts used by the composition root and registered components.
package presentation

import (
	"errors"
	"sync"
)

var ErrDuplicate = errors.New("module already registered")
var ErrUnavailable = errors.New("module unavailable in this build")
var ErrInvalid = errors.New("invalid module registration")

type Registry struct {
	mu  sync.RWMutex
	ids map[string]struct{}
}

func NewRegistry() *Registry { return &Registry{ids: make(map[string]struct{})} }
func (r *Registry) Register(name string) error {
	if name == "" || r == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.ids[name]; ok {
		return ErrDuplicate
	}
	r.ids[name] = struct{}{}
	return nil
}
func (r *Registry) Lookup(name string) error {
	if r == nil || name == "" {
		return ErrUnavailable
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.ids[name]
	if !ok {
		return ErrUnavailable
	}
	return nil
}
