package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrBusy       = errors.New("placement has an active lifecycle operation")
	ErrDisposed   = errors.New("placement is disposed")
	ErrSuperseded = errors.New("lifecycle operation was superseded")
	ErrTimeout    = errors.New("lifecycle operation timed out")
)

// Operation is one phase of the static-lifecycle ABI.
type Operation string

const (
	OpRender  Operation = "render"
	OpHydrate Operation = "hydrate"
	OpUpdate  Operation = "update"
	OpDispose Operation = "dispose"
)

// Executor executes one lifecycle operation against a feature module. The host
// supplies it; this package never embeds or guesses a JavaScript engine.
type Executor interface {
	Execute(context.Context, Operation, FeatureContext) error
}

// ExecutorFunc adapts a function to Executor.
type ExecutorFunc func(context.Context, Operation, FeatureContext) error

func (fn ExecutorFunc) Execute(ctx context.Context, op Operation, featureContext FeatureContext) error {
	return fn(ctx, op, featureContext)
}

type lifecycleCall struct {
	op      Operation
	context FeatureContext
	done    chan struct{}
	err     error

	mu     sync.Mutex
	cancel context.CancelFunc
}

func (call *lifecycleCall) setCancel(cancel context.CancelFunc) {
	call.mu.Lock()
	call.cancel = cancel
	call.mu.Unlock()
}

func (call *lifecycleCall) requestCancel() {
	call.mu.Lock()
	cancel := call.cancel
	call.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (call *lifecycleCall) finish(err error) {
	call.err = err
	close(call.done)
}

// Placement enforces the lifecycle invariants for exactly one feature instance:
// one operation at a time, latest-wins update, cooperative cancellation,
// bounded wait before dispose, and isolation from sibling placements.
type Placement struct {
	mu       sync.Mutex
	executor Executor
	timeout  time.Duration
	active   *lifecycleCall
	pending  *lifecycleCall
	disposed bool
}

func NewPlacement(executor Executor, timeout time.Duration) (*Placement, error) {
	if executor == nil || timeout <= 0 {
		return nil, fmt.Errorf("%w: executor and positive timeout are required", ErrContext)
	}
	return &Placement{executor: executor, timeout: timeout}, nil
}

// Render runs on an empty lifecycle root. It cannot overlap another operation.
func (p *Placement) Render(ctx context.Context, featureContext FeatureContext) error {
	return p.begin(ctx, OpRender, featureContext)
}

// Hydrate attaches interactive state to an existing server-rendered root.
func (p *Placement) Hydrate(ctx context.Context, featureContext FeatureContext) error {
	return p.begin(ctx, OpHydrate, featureContext)
}

func (p *Placement) begin(parent context.Context, op Operation, featureContext FeatureContext) error {
	call := &lifecycleCall{op: op, context: featureContext, done: make(chan struct{})}
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return ErrDisposed
	}
	if p.active != nil {
		p.mu.Unlock()
		return ErrBusy
	}
	p.active = call
	p.mu.Unlock()
	return p.run(call, parent)
}

// Update cancels the current operation and applies only the newest pending
// context. Earlier pending updates are superseded and never mutate the root.
func (p *Placement) Update(parent context.Context, featureContext FeatureContext) error {
	call := &lifecycleCall{op: OpUpdate, context: featureContext, done: make(chan struct{})}
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return ErrDisposed
	}
	if p.active == nil {
		p.active = call
		p.mu.Unlock()
		return p.run(call, parent)
	}
	active := p.active
	active.requestCancel()
	p.pending = call
	p.mu.Unlock()

	for {
		<-active.done
		p.mu.Lock()
		if p.disposed {
			p.mu.Unlock()
			return ErrDisposed
		}
		if p.active != nil {
			next := p.active
			p.mu.Unlock()
			active = next
			continue
		}
		if p.pending != call {
			// A newer update owns the pending slot and will run it. Do not
			// clear the slot here: the newest context must still be applied.
			p.mu.Unlock()
			return ErrSuperseded
		}
		p.pending = nil
		p.active = call
		p.mu.Unlock()
		return p.run(call, parent)
	}
}

// Dispose waits for an active operation within the configured timeout, cancels
// it, then calls the dispose phase once. Repeated calls are harmless.
func (p *Placement) Dispose(parent context.Context) error {
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return nil
	}
	p.disposed = true
	active := p.active
	if active != nil {
		active.requestCancel()
	}
	p.pending = nil
	p.mu.Unlock()

	if active != nil {
		waitCtx, cancel := context.WithTimeout(parent, p.timeout)
		defer cancel()
		select {
		case <-active.done:
		case <-waitCtx.Done():
			return fmt.Errorf("%w: dispose waited for active operation", ErrTimeout)
		}
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	return p.executor.Execute(ctx, OpDispose, FeatureContext{})
}

// State reports a snapshot for diagnostics; it does not block on execution.
func (p *Placement) State() (active Operation, busy bool, disposed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil {
		return p.active.op, true, p.disposed
	}
	return "", false, p.disposed
}

func (p *Placement) run(call *lifecycleCall, parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	call.setCancel(cancel)
	err := p.executor.Execute(ctx, call.op, call.context)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) &&
		(err == nil || errors.Is(err, context.DeadlineExceeded)) {
		err = ErrTimeout
	}
	p.mu.Lock()
	if p.active == call {
		p.active = nil
	}
	p.mu.Unlock()
	call.finish(err)
	return err
}

// Supervisor owns independent placements and guarantees that one failing
// placement never blocks or mutates its siblings.
type Supervisor struct {
	mu         sync.Mutex
	executor   Executor
	timeout    time.Duration
	placements map[string]*Placement
}

func NewSupervisor(executor Executor, timeout time.Duration) *Supervisor {
	return &Supervisor{executor: executor, timeout: timeout, placements: make(map[string]*Placement)}
}

func (s *Supervisor) Placement(placementID string) (*Placement, error) {
	if placementID == "" {
		return nil, fmt.Errorf("%w: placement id", ErrContext)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if placement, ok := s.placements[placementID]; ok {
		return placement, nil
	}
	placement, err := NewPlacement(s.executor, s.timeout)
	if err != nil {
		return nil, err
	}
	s.placements[placementID] = placement
	return placement, nil
}

func (s *Supervisor) Remove(placementID string) {
	s.mu.Lock()
	delete(s.placements, placementID)
	s.mu.Unlock()
}
