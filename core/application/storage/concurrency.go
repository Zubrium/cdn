package storage

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	appcache "github.com/Zubrium/cdn/core/application/cache"
)

// ErrOverloaded is returned before a storage operation reaches pgxpool. It
// wraps ErrUnavailable so existing HTTP mappings keep returning 503.
var ErrOverloaded = fmt.Errorf("%w: storage concurrency limit reached", ErrUnavailable)

type storageConcurrency struct {
	reads          chan struct{}
	authorizations chan struct{}
	timeout        time.Duration
	observer       appcache.Observer
	inFlight       atomic.Int64
}

// WithConcurrency installs explicit process-local limits. The caller must
// provide positive bounds and a timeout; no runtime default is substituted.
func (s Service) WithConcurrency(maxReads, maxAuthorizations int, timeout time.Duration, observer appcache.Observer) Service {
	if maxReads <= 0 || maxAuthorizations <= 0 || timeout <= 0 || observer == nil {
		return s
	}
	s.concurrency = &storageConcurrency{
		reads: make(chan struct{}, maxReads), authorizations: make(chan struct{}, maxAuthorizations),
		timeout: timeout, observer: observer,
	}
	return s
}

func (s Service) acquire(ctx context.Context, authorization bool) (func(), error) {
	state := s.concurrency
	if state == nil {
		return func() {}, nil
	}
	semaphore := state.reads
	if authorization {
		semaphore = state.authorizations
	}
	timer := time.NewTimer(state.timeout)
	defer timer.Stop()
	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, s.rejectOverload()
	case <-timer.C:
		return nil, s.rejectOverload()
	}
	state.inFlight.Add(1)
	state.observer.SetGauge(context.Background(), "zubrium_concurrency_in_flight",
		float64(state.inFlight.Load()), appcache.StringLabel("scope", "STORAGE"))
	var once atomic.Bool
	return func() {
		if !once.CompareAndSwap(false, true) {
			return
		}
		<-semaphore
		state.inFlight.Add(-1)
		state.observer.SetGauge(context.Background(), "zubrium_concurrency_in_flight",
			float64(state.inFlight.Load()), appcache.StringLabel("scope", "STORAGE"))
	}, nil
}

func (s Service) rejectOverload() error {
	if state := s.concurrency; state != nil {
		state.observer.IncCounter(context.Background(), "zubrium_concurrency_rejected_total", 1,
			appcache.StringLabel("scope", "STORAGE"), appcache.StringLabel("reason", "DB_RESERVE"))
	}
	return ErrOverloaded
}
