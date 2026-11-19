package bus

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrClosed              = errors.New("dispatcher is closed")
	ErrNotSealed           = errors.New("dispatcher is not sealed")
	ErrDuplicateKey        = errors.New("operation is already registered")
	ErrHandlerNotFound     = errors.New("operation handler is not registered")
	ErrOperationNotAllowed = errors.New("operation is not in the static allowlist")
	ErrInvalidAllowlist    = errors.New("invalid operation allowlist")
)

type Handler func(context.Context, json.RawMessage) (json.RawMessage, error)

type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[contracts.Key]Handler
	allowed  map[contracts.Key]struct{}
	sealed   bool
	closed   bool
	active   sync.WaitGroup
	drained  chan struct{}
	drain    sync.Once
}

func NewAllowlisted(keys ...contracts.Key) (*Dispatcher, error) {
	allowed := make(map[contracts.Key]struct{}, len(keys))
	for _, key := range keys {
		if key.Validate() != nil {
			return nil, ErrInvalidAllowlist
		}
		if _, exists := allowed[key]; exists {
			return nil, ErrDuplicateKey
		}
		allowed[key] = struct{}{}
	}
	return &Dispatcher{handlers: make(map[contracts.Key]Handler), allowed: allowed, drained: make(chan struct{})}, nil
}

func Register[Request, Response any](dispatcher *Dispatcher, operation contracts.Operation[Request, Response], handler contracts.Handler[Request, Response]) error {
	if dispatcher == nil {
		return contracts.ErrDispatcherUnavailable
	}
	bound, err := contracts.Bind(operation, handler)
	if err != nil {
		return err
	}
	return dispatcher.register(operation.Key, func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
		request, err := contracts.DecodePayload[Request](payload)
		if err != nil {
			return nil, err
		}
		response, err := bound(ctx, request)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return encoded, nil
	})
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
	if _, allowed := dispatcher.allowed[key]; !allowed {
		return ErrOperationNotAllowed
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

func (dispatcher *Dispatcher) Dispatch(ctx context.Context, request contracts.Envelope) (contracts.Envelope, error) {
	if dispatcher == nil {
		return contracts.Envelope{}, contracts.ErrDispatcherUnavailable
	}
	key := request.Key()
	if err := key.Validate(); err != nil {
		return contracts.Envelope{}, err
	}
	dispatcher.mu.RLock()
	if dispatcher.closed {
		dispatcher.mu.RUnlock()
		return contracts.Envelope{}, ErrClosed
	}
	if !dispatcher.sealed {
		dispatcher.mu.RUnlock()
		return contracts.Envelope{}, ErrNotSealed
	}
	if _, allowed := dispatcher.allowed[key]; !allowed {
		dispatcher.mu.RUnlock()
		return contracts.Envelope{}, ErrOperationNotAllowed
	}
	handler, exists := dispatcher.handlers[key]
	if !exists {
		dispatcher.mu.RUnlock()
		return contracts.Envelope{}, ErrHandlerNotFound
	}
	dispatcher.active.Add(1)
	dispatcher.mu.RUnlock()
	defer dispatcher.active.Done()
	if err := ctx.Err(); err != nil {
		return contracts.Envelope{}, err
	}
	response, err := handler(ctx, request.Payload)
	if err != nil {
		return contracts.Envelope{}, err
	}
	return contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: response}, nil
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
