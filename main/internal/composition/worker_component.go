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
}

func (c *workerComponent) Errors() <-chan error {
	closed := make(chan error)
	close(closed)
	return closed
}

var _ platform.ManagedComponent = (*workerComponent)(nil)

func newWorkerComponent(ctx context.Context, id string) (platform.ManagedComponent, error) {
	_ = ctx
	return &workerComponent{readyComponent: &readyComponent{id: id, ready: true}}, nil
}
