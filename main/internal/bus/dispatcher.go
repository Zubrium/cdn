package bus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
)

var (
	ErrClosed              = errors.New("dispatcher is closed")
	ErrNotSealed           = errors.New("dispatcher is not sealed")
	ErrAlreadySealed       = errors.New("dispatcher is already sealed")
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
	streams  bool
	active   sync.WaitGroup
	drained  chan struct{}
	drain    sync.Once
	observer *observability.Observer
}

// WithObserver attaches the process observer to the bus dispatcher. It must
// be called before Seal and is safe to call with nil.
func (dispatcher *Dispatcher) WithObserver(observer *observability.Observer) *Dispatcher {
	if dispatcher != nil {
		dispatcher.observer = observer
	}
	return dispatcher
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

func (dispatcher *Dispatcher) EnableScopedStreams() error {
	if dispatcher == nil {
		return contracts.ErrDispatcherUnavailable
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.closed {
		return ErrClosed
	}
	if dispatcher.sealed {
		return ErrAlreadySealed
	}
	dispatcher.streams = true
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

func (dispatcher *Dispatcher) Dispatch(ctx context.Context, request contracts.Envelope) (result contracts.Envelope, err error) {
	if dispatcher == nil {
		return contracts.Envelope{}, contracts.ErrDispatcherUnavailable
	}
	key := request.Key()
	if err := key.Validate(); err != nil {
		return contracts.Envelope{}, err
	}
	started := time.Now()
	dispatchCtx := ctx
	if observer := dispatcher.observer; observer != nil {
		source := observer.CurrentIdentity(ctx)
		identity := observability.Identity{
			ModuleKind: observability.ModuleKindBus, ModuleID: "bus", ComponentID: "dispatcher", OperationID: key.ID,
		}
		var span observability.Span
		dispatchCtx, span = observer.Start(observer.WithIdentity(ctx, identity), "bus.dispatch",
			observability.String("bus.target.module", targetModule(key.ID)),
			observability.String("bus.source.module", source.ModuleID),
			observability.String("bus.source.component", source.ComponentID))
		defer func() {
			span.End(err)
			labels := []observability.Attribute{
				observability.String("operation.id", key.ID),
				observability.String("bus.target.module", targetModule(key.ID)),
				observability.String("outcome", outcome(err)),
			}
			observer.IncCounter(dispatchCtx, "zubrium_bus_dispatch_total", 1, labels...)
			observer.ObserveHistogram(dispatchCtx, "zubrium_bus_dispatch_duration_seconds", time.Since(started).Seconds(), labels...)
		}()
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
	withStreams := dispatcher.streams
	dispatcher.active.Add(1)
	dispatcher.mu.RUnlock()
	defer dispatcher.active.Done()
	if err := ctx.Err(); err != nil {
		return contracts.Envelope{}, err
	}
	if withStreams {
		if _, provided := contracts.StreamCapabilityFromContext(ctx); !provided {
			streams := NewScopedStreams()
			defer func() {
				if closeErr := streams.Close(); err == nil {
					err = closeErr
				}
			}()
			ctx = contracts.WithStreamCapability(ctx, streams)
		}
	}
	payload, err := handler(dispatchCtx, request.Payload)
	if err != nil {
		return contracts.Envelope{}, err
	}
	return contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: payload}, nil
}

func targetModule(operationID string) string {
	switch {
	case strings.HasPrefix(operationID, "core."):
		return "core"
	case strings.HasPrefix(operationID, "content."):
		return "engine.content"
	case strings.HasPrefix(operationID, "main.publication."):
		return "engine.publication"
	case strings.HasPrefix(operationID, "main.retirement."):
		return "composition.retirement"
	default:
		return "composition"
	}
}

func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
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
