package coordinator

// Descriptor registers Core's Coordinator subsystem with the registry. It is
// metadata, not an API permission
// check; operator-facing handlers must authorize their own requests.
type Descriptor struct{}
