package bus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
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
	ErrOverloaded          = errors.New("dispatcher is overloaded")
	ErrInvalidConcurrency  = errors.New("invalid dispatcher concurrency configuration")
)

type Handler func(context.Context, json.RawMessage) (json.RawMessage, error)

// OperationLimit is an explicit per-operation ceiling. Its key must belong to
// the static allowlist; callers cannot choose it through an HTTP request.
type OperationLimit struct {
	Key         contracts.Key
	MaxInFlight int
}

// PerOperationLimits narrows concurrency for operations compiled into the
// dispatcher. Disabled limits are not represented in the runtime path at all.
type PerOperationLimits struct {
	Enabled            bool
	DefaultMaxInFlight int
	Overrides          []OperationLimit
}

// ConcurrencyLimits is supplied explicitly by the composition root. There are
// no implicit limits and no compatibility aliases for older constructors.
type ConcurrencyLimits struct {
	MaxInFlight  int
	PerOperation PerOperationLimits
}

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

	globalSlots   chan struct{}
	operationSlot map[contracts.Key]chan struct{}
	inFlight      atomic.Int64
}

// WithObserver attaches the process observer to the bus dispatcher. It must
// be called before Seal and is safe to call with nil.
func (dispatcher *Dispatcher) WithObserver(observer *observability.Observer) *Dispatcher {
	if dispatcher == nil {
		return nil
	}
	dispatcher.observer = observer
	if observer != nil {
		dispatcher.publishConcurrencyContract(observer)
	}
	return dispatcher
}

// publishConcurrencyContract exposes the canonical BUS series before the first
// dispatch, so disabled or not-yet-exercised admission paths are visible as
// zero-valued series with low-cardinality labels only.
func (dispatcher *Dispatcher) publishConcurrencyContract(observer *observability.Observer) {
	ctx := dispatcher.concurrencyContext()
	observer.SetGauge(ctx, "zubrium_concurrency_in_flight", 0,
		observability.String("scope", "BUS"))
	observer.SetGauge(ctx, "zubrium_bus_in_flight", 0,
		observability.String("scope", "BUS"))
	for _, reason := range []string{"GLOBAL", "PER_ROUTE", "PER_OPERATION", "DB_RESERVE"} {
		observer.IncCounter(ctx, "zubrium_concurrency_rejected_total", 0,
			observability.String("scope", "BUS"), observability.String("reason", reason))
	}
}

func NewAllowlisted(limits ConcurrencyLimits, keys ...contracts.Key) (*Dispatcher, error) {
	if limits.MaxInFlight <= 0 {
		return nil, ErrInvalidConcurrency
	}
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
	operationSlot := make(map[contracts.Key]chan struct{})
	if limits.PerOperation.Enabled {
		if limits.PerOperation.DefaultMaxInFlight <= 0 || limits.PerOperation.DefaultMaxInFlight > limits.MaxInFlight {
			return nil, ErrInvalidConcurrency
		}
		for _, key := range keys {
			operationSlot[key] = make(chan struct{}, limits.PerOperation.DefaultMaxInFlight)
		}
		seen := make(map[contracts.Key]struct{}, len(limits.PerOperation.Overrides))
		for _, override := range limits.PerOperation.Overrides {
			if override.MaxInFlight <= 0 || override.MaxInFlight > limits.MaxInFlight || override.Key.Validate() != nil {
				return nil, ErrInvalidConcurrency
			}
			if _, exists := seen[override.Key]; exists {
				return nil, ErrInvalidConcurrency
			}
			if _, allowedKey := allowed[override.Key]; !allowedKey {
				return nil, ErrOperationNotAllowed
			}
			seen[override.Key] = struct{}{}
			operationSlot[override.Key] = make(chan struct{}, override.MaxInFlight)
		}
	} else if limits.PerOperation.DefaultMaxInFlight != 0 || len(limits.PerOperation.Overrides) != 0 {
		return nil, ErrInvalidConcurrency
	}
	return &Dispatcher{
		handlers: make(map[contracts.Key]Handler), allowed: allowed, drained: make(chan struct{}),
		globalSlots: make(chan struct{}, limits.MaxInFlight), operationSlot: operationSlot,
	}, nil
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
	if ctx == nil {
		ctx = context.Background()
	}
	key := request.Key()
	if err := key.Validate(); err != nil {
		return contracts.Envelope{}, err
	}
	started := time.Now()
	observer := dispatcher.observer
	dispatchCtx := ctx
	if observer != nil {
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

	// Hold the read lock across admission and active.Add so Close cannot start
	// waiting before an admitted dispatch is registered in the drain set.
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
	if err := ctx.Err(); err != nil {
		dispatcher.mu.RUnlock()
		return contracts.Envelope{}, err
	}
	select {
	case dispatcher.globalSlots <- struct{}{}:
	default:
		dispatcher.mu.RUnlock()
		dispatcher.recordRejected("GLOBAL")
		return contracts.Envelope{}, ErrOverloaded
	}
	var operationSlot chan struct{}
	if slot, configured := dispatcher.operationSlot[key]; configured {
		select {
		case slot <- struct{}{}:
			operationSlot = slot
		default:
			<-dispatcher.globalSlots
			dispatcher.mu.RUnlock()
			dispatcher.recordRejected("PER_OPERATION")
			return contracts.Envelope{}, ErrOverloaded
		}
	}
	dispatcher.active.Add(1)
	withStreams := dispatcher.streams
	if withStreams {
		if capability, provided := contracts.StreamCapabilityFromContext(ctx); provided {
			if scoped, ok := capability.(*ScopedStreams); ok {
				dispatcher.active.Add(1)
				scoped.onClose(dispatcher.active.Done)
			}
		}
	}
	dispatcher.mu.RUnlock()

	dispatcher.setInFlight(1)
	var released sync.Once
	release := func() {
		released.Do(func() {
			dispatcher.setInFlight(-1)
			if operationSlot != nil {
				<-operationSlot
			}
			<-dispatcher.globalSlots
			dispatcher.active.Done()
		})
	}
	defer release()
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
			dispatchCtx = contracts.WithStreamCapability(dispatchCtx, streams)
		}
	}
	payload, err := handler(dispatchCtx, request.Payload)
	if err != nil {
		return contracts.Envelope{}, err
	}
	return contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: payload}, nil
}

// concurrencyContext drops operation and trace attribution inherited from the
// dispatch span so admission metrics stay low-cardinality.
func (dispatcher *Dispatcher) concurrencyContext() context.Context {
	return dispatcher.observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindBus, ModuleID: "bus", ComponentID: "dispatcher",
	})
}

func (dispatcher *Dispatcher) setInFlight(delta int64) {
	observer := dispatcher.observer
	if observer == nil {
		return
	}
	current := dispatcher.inFlight.Add(delta)
	// SPEC-concurrency.md §5.6 names the bus-specific gauge, while §12 requires
	// the canonical scope-based gauge. Both describe the same admission load and
	// must stay in sync; neither carries operation or caller identity.
	observer.SetGauge(dispatcher.concurrencyContext(), "zubrium_bus_in_flight", float64(current), observability.String("scope", "BUS"))
	observer.SetGauge(dispatcher.concurrencyContext(), "zubrium_concurrency_in_flight", float64(current), observability.String("scope", "BUS"))
}

func (dispatcher *Dispatcher) recordRejected(reason string) {
	observer := dispatcher.observer
	if observer == nil {
		return
	}
	observer.IncCounter(dispatcher.concurrencyContext(), "zubrium_concurrency_rejected_total", 1,
		observability.String("scope", "BUS"), observability.String("reason", reason))
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
	if ctx == nil {
		ctx = context.Background()
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
