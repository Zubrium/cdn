// Package contentapi defines the Content Admin HTTP adapter contract.
package contentapi

import (
	"context"

	engine "github.com/Zubrium/cdn/pkg/contracts"
)

type Engine = engine.AdminContentAPI

// SchemaSnapshots is supplied by the trusted outer Presentation composition.
// Implementations must obtain Website-owned projections from a trusted facade,
// never from request data. Missing projections fail closed.
type SchemaSnapshots interface {
	GetContentProjections(context.Context, string) (engine.AdminContentProjections, error)
}
