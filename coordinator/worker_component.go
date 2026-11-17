package coordinator

import (
	"context"
	"errors"
	"sync"
)

var ErrWorkerComponentNotReady = errors.New("worker component is not ready")

// WorkerComponent adapts a leased task pool to the same lifecycle contract as
// engines and APIs. Its factory should be registered only when the selected
// build contains at least one real handler for that worker module.
type WorkerComponent struct {
	pool    *WorkerPool
	mu      sync.Mutex
	started bool
}

func NewWorkerComponent(store TaskStore, config WorkerConfig, handlers map[string]TaskHandler) (*WorkerComponent, error) {
	pool, err := NewWorkerPool(store, config, handlers)
	if err != nil {
		return nil, err
	}
	return &WorkerComponent{pool: pool}, nil
}

func (c *WorkerComponent) Start(ctx context.Context) error {
	if c == nil || c.pool == nil || ctx == nil {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return ErrWorkerAlreadyStarted
	}
	if err := c.pool.Start(ctx); err != nil {
		return err
	}
	c.started = true
	return nil
}

func (c *WorkerComponent) Ready(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.pool == nil || !c.pool.Healthy() {
		return ErrWorkerComponentNotReady
	}
	return nil
}

func (c *WorkerComponent) Stop(ctx context.Context) error {
	if c == nil || c.pool == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalid
	}
	c.mu.Lock()
	started := c.started
	c.mu.Unlock()
	if !started {
		return nil
	}
	return c.pool.Close(ctx)
}

func (c *WorkerComponent) IsReady() bool {
	return c != nil && c.pool != nil && c.pool.Healthy()
}

func (c *WorkerComponent) Errors() <-chan error {
	if c == nil || c.pool == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return c.pool.Errors()
}
