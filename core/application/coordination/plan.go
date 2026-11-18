package coordinator

// BuildPlan is supplied by the application composition root. Core treats
// component identifiers as opaque strings and validates only registered
// capabilities and dependency ordering.
type BuildPlan struct{ Components []string }
