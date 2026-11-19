package bus

import (
	"context"
	"errors"
	"sync"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrClosed          = errors.New("dispatcher is closed")
	ErrNotSealed       = errors.New("dispatcher is not sealed")
	ErrDuplicateKey    = errors.New("operation is already registered")
	ErrHandlerNotFound = errors.New("operation handler is not registered")
)

type Handler func(context.Context, any) (any, error)

type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[contracts.Key]Handler
	sealed   bool
	closed   bool
	active   sync.WaitGroup
	drained  chan struct{}
	drain    sync.Once
}

func New() *Dispatcher {
	return &Dispatcher{handlers: make(map[contracts.Key]Handler), drained: make(chan struct{})}
}

func Register[Request, Response any](dispatcher *Dispatcher, operation contracts.Operation[Request, Response], handler contracts.Handler[Request, Response]) error {
	if dispatcher == nil {
		return contracts.ErrDispatcherUnavailable
	}
	bound, err := contracts.Bind(operation, handler)
	if err != nil {
		return err
	}
	return dispatcher.register(operation.Key, bound)
}

func (dispatcher *Dispatcher) register(key contracts.Key, handler Handler) error {
	if dispatcher == nil || key.Validate() != nil || handler == nil {
		return contracts.ErrInvalidOperation
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.closed {
		return ErrClosed
	}
	if dispatcher.sealed {
		return ErrNotSealed
	}
	if _, exists := dispatcher.handlers[key]; exists {
		return ErrDuplicateKey
	}
	dispatcher.handlers[key] = handler
	return nil
}

func (dispatcher *Dispatcher) Seal() error {
	if dispatcher == nil {
		return contracts.ErrDispatcherUnavailable
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.closed {
		return ErrClosed
	}
	dispatcher.sealed = true
	return nil
}

func (dispatcher *Dispatcher) Dispatch(ctx context.Context, key contracts.Key, request any) (any, error) {
	if dispatcher == nil {
		return nil, contracts.ErrDispatcherUnavailable
	}
	dispatcher.mu.RLock()
	if dispatcher.closed {
		dispatcher.mu.RUnlock()
		return nil, ErrClosed
	}
	if !dispatcher.sealed {
		dispatcher.mu.RUnlock()
		return nil, ErrNotSealed
	}
	handler, exists := dispatcher.handlers[key]
	if !exists {
		dispatcher.mu.RUnlock()
		return nil, ErrHandlerNotFound
	}
	dispatcher.active.Add(1)
	dispatcher.mu.RUnlock()
	defer dispatcher.active.Done()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return handler(ctx, request)
}

func (dispatcher *Dispatcher) Close(ctx context.Context) error {
	if dispatcher == nil {
		return nil
	}
	dispatcher.mu.Lock()
	dispatcher.closed = true
	dispatcher.mu.Unlock()
	dispatcher.drain.Do(func() {
		go func() {
			dispatcher.active.Wait()
			close(dispatcher.drained)
		}()
	})
	select {
	case <-dispatcher.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
