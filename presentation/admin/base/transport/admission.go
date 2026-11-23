package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/Zubrium/cdn/pkg/observability"
)

// ErrInvalidIngressConcurrency reports an incomplete or contradictory explicit
// HTTP admission configuration. There are no implicit limits or timeouts.
var ErrInvalidIngressConcurrency = errors.New("invalid HTTP ingress concurrency configuration")

type ingressLimiter struct {
	config   ServerConfig
	observer *observability.Observer
	pool     string
	slots    chan struct{}

	mu      sync.RWMutex
	closed  bool
	active  sync.WaitGroup
	drained chan struct{}
	drain   sync.Once

	inFlight atomic.Int64
}

func newIngressLimiter(config ServerConfig, observer *observability.Observer, pool string) (*ingressLimiter, error) {
	if config.MaxInFlight <= 0 || config.RequestTimeout <= 0 || config.ShutdownTimeout <= 0 || config.ShutdownTimeout < config.RequestTimeout {
		return nil, ErrInvalidIngressConcurrency
	}
	return &ingressLimiter{
		config: config, observer: observer, pool: pool,
		slots: make(chan struct{}, config.MaxInFlight), drained: make(chan struct{}),
	}, nil
}

// Wrap applies request admission and the explicit request deadline. Health and
// readiness probes are intentionally outside the in-flight pool so overload
// cannot hide the liveness signal.
func (limiter *ingressLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		if limiter == nil || next == nil {
			writeIngressOverload(w)
			return
		}
		if err := r.Context().Err(); err != nil {
			writeIngressOverload(w)
			return
		}

		limiter.mu.RLock()
		if limiter.closed {
			limiter.mu.RUnlock()
			limiter.reject("SHUTDOWN")
			writeIngressOverload(w)
			return
		}
		select {
		case limiter.slots <- struct{}{}:
		default:
			limiter.mu.RUnlock()
			limiter.reject("GLOBAL")
			writeIngressOverload(w)
			return
		}
		limiter.active.Add(1)
		limiter.mu.RUnlock()

		limiter.setInFlight(1)
		var released sync.Once
		release := func() {
			released.Do(func() {
				limiter.setInFlight(-1)
				<-limiter.slots
				limiter.active.Done()
			})
		}
		defer release()
		if err := r.Context().Err(); err != nil {
			writeIngressOverload(w)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), limiter.config.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Close stops admission and waits for the already admitted requests to drain.
// It is idempotent; repeated calls do not emit duplicate metric updates.
func (limiter *ingressLimiter) Close(ctx context.Context) error {
	if limiter == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limiter.mu.Lock()
	limiter.closed = true
	limiter.mu.Unlock()
	limiter.drain.Do(func() {
		go func() {
			limiter.active.Wait()
			close(limiter.drained)
		}()
	})
	select {
	case <-limiter.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// concurrencyContext drops caller identity and trace attribution so admission
// metrics stay low-cardinality: scope and pool only, never route or request IDs.
func (limiter *ingressLimiter) concurrencyContext() context.Context {
	return limiter.observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindPresentation, ModuleID: limiter.pool, ComponentID: "http-server",
	})
}

func (limiter *ingressLimiter) setInFlight(delta int64) {
	if limiter == nil || limiter.observer == nil {
		return
	}
	current := limiter.inFlight.Add(delta)
	limiter.observer.SetGauge(limiter.concurrencyContext(), "zubrium_concurrency_in_flight", float64(current),
		observability.String("scope", "HTTP"), observability.String("pool", limiter.pool))
}

func (limiter *ingressLimiter) reject(reason string) {
	if limiter == nil || limiter.observer == nil {
		return
	}
	limiter.observer.IncCounter(limiter.concurrencyContext(), "zubrium_concurrency_rejected_total", 1,
		observability.String("scope", "HTTP"), observability.String("pool", limiter.pool), observability.String("reason", reason))
}

func writeIngressOverload(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"code": "overloaded", "message": "The server is at request capacity",
	}})
}
