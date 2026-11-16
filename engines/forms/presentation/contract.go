// Package presentation defines the engine composition contract.
package presentation

import "github.com/Zubrium/cdn/core/platform"

// Contract is implemented by an engine's unified presentation facade.
// Concrete typed operations will be added as the engine is migrated.
type Contract interface{ platform.Module }

// Descriptor registers the engine boundary; operations are added during migration.
type Descriptor struct{}

func (Descriptor) Operations() []platform.Operation { return nil }
