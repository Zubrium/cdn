// Package coordinator contains the validated build profile, runtime lifecycle,
// durable worker loop, and publication orchestration contracts.
package coordinator

import "fmt"

type Component struct {
	ID       string
	Requires []string
}

// Registry describes available compile-time components, not runtime plugins.
type Registry map[string]Component

func (r Registry) Validate(selected []string) error {
	active := make(map[string]bool, len(selected))
	for _, id := range selected {
		component, ok := r[id]
		if !ok || id == "" || component.ID != id {
			return fmt.Errorf("unknown component %q", id)
		}
		if active[id] {
			return fmt.Errorf("duplicate component %q", id)
		}
		active[id] = true
	}
	if !active["core"] {
		return fmt.Errorf("core is required")
	}
	if !active["coordinator"] {
		return fmt.Errorf("coordinator is required")
	}
	states := make(map[string]uint8, len(active))
	var visit func(string) error
	visit = func(id string) error {
		if states[id] == 1 {
			return fmt.Errorf("cyclic dependency at %q", id)
		}
		if states[id] == 2 {
			return nil
		}
		states[id] = 1
		for _, dep := range r[id].Requires {
			if !active[dep] {
				return fmt.Errorf("component %q requires %q", id, dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		states[id] = 2
		return nil
	}
	for _, id := range selected {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
