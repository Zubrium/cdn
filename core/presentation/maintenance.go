package presentation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zubrium/cdn/core/infrastructure/postgres"
)

func (r *Runtime) Errors() <-chan error { return r.errors }

func (r *Runtime) maintain(ctx context.Context, cfg RuntimeConfig) {
	defer close(r.done)
	ticker := time.NewTicker(cfg.MaintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			operation, cancel := context.WithTimeout(ctx, cfg.MaintenanceTimeout)
			err := r.maintenance.Run(operation, now)
			cancel()
			if err != nil && ctx.Err() == nil {
				r.report(fmt.Errorf("core maintenance: %w", err))
			}
		}
	}
}

// listenInvalidations batches PostgreSQL notifications inside the configured
// hard window. Overflow, reconnect and shutdown clear all local caches so a
// missed notification can never leave a stale authorization usable.
func (r *Runtime) listenInvalidations(ctx context.Context, batchSize int, batchWindow time.Duration) {
	defer close(r.notifyDone)
	if batchSize <= 0 || batchWindow <= 0 {
		r.report(errors.New("invalid core cache invalidation batch bounds"))
		return
	}
	payloads := make(chan string, batchSize*2)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		postgres.WatchCacheInvalidations(ctx, r.database, func() {
			r.files.InvalidateAll()
		}, func(payload string) {
			select {
			case payloads <- payload:
			default:
				r.files.InvalidateAll()
			}
		}, r.report)
	}()

	timer := time.NewTimer(batchWindow)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	batch := make([]string, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if r.cacheStore != nil {
			r.cacheStore.NotifyBatch(batch)
		} else {
			for _, id := range batch {
				r.files.InvalidateCache(id)
			}
		}
		batch = batch[:0]
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(batchWindow)
	}

	for {
		select {
		case payload := <-payloads:
			batch = append(batch, payload)
			if len(batch) >= batchSize {
				flush()
			}
		case <-timer.C:
			flush()
		case <-watchDone:
			flush()
			return
		case <-ctx.Done():
			flush()
			return
		}
	}
}

func (r *Runtime) report(err error) {
	if r == nil || r.errors == nil || err == nil {
		return
	}
	select {
	case r.errors <- err:
	default:
	}
}
