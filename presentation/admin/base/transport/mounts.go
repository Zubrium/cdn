package transport

import (
	"context"
	"net/http"
)

// HandlerMount is one independently owned Admin HTTP capability. Composition
// builds the handler; this transport only assigns route patterns and applies
// the shared server guard. Keeping ownership outside the server prevents the
// Admin base module from depending on every admin feature module.
type HandlerMount struct {
	Patterns  []string
	Handler   http.Handler
	Guarded   bool
	Synthetic bool
}

// HandlerContext carries only callbacks that depend on server instance state.
type HandlerContext struct {
	AuthorizeWorkflowSession func(context.Context, string, string) error
}
