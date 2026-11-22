package composition

import (
	"context"

	platform "github.com/Zubrium/cdn/core/presentation"
)

// workerComponent keeps a compiled worker catalog entry available while the
// coordinator is active. Concrete worker execution remains fail-closed until
// server-side configuration is provided for that worker.
type workerComponent struct {
	*readyComponent
	failures chan error
}

func (c *workerComponent) Errors() <-chan error {
	// Fail-closed workers have no failures to report. The channel must remain
	// open: a closed channel would be read as a fatal component failure, and a
	// nil channel is rejected by the Coordinator's failure-source contract.
	return c.failures
}

var _ platform.ManagedComponent = (*workerComponent)(nil)

func newWorkerComponent(ctx context.Context, id string) (platform.ManagedComponent, error) {
	_ = ctx
	return &workerComponent{readyComponent: &readyComponent{id: id, ready: true}, failures: make(chan error)}, nil
}
