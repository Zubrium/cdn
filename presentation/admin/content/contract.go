// Package contentapi defines the Content Admin HTTP adapter contract.
package contentapi

import (
	"context"

	engine "github.com/Zubrium/cdn/engines/content/presentation"
)

type Engine = engine.API

// SchemaSnapshots is supplied by the trusted outer Presentation composition.
// Implementations must obtain Website-owned projections from a trusted facade,
// never from request data. Missing projections fail closed.
type SchemaSnapshots interface {
	GetContentProjections(context.Context, string) (engine.Projections, error)
}
