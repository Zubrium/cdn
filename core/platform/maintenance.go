package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/Zubrium/cdn/core/internal/postgres"
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
			err := postgres.RecoverPending(operation, r.database, r.objects, now.Add(-cfg.PendingTTL), cfg.MaintenanceBatchSize)
			cancel()
			if err != nil && ctx.Err() == nil {
				r.report(fmt.Errorf("core maintenance: %w", err))
			}
		}
	}
}

func (r *Runtime) listenInvalidations(ctx context.Context) {
	defer close(r.notifyDone)
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		conn, err := r.database.Acquire(ctx)
		if err != nil {
			if ctx.Err() == nil {
				r.report(fmt.Errorf("core cache invalidation connection: %w", err))
			}
			if !waitContext(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		_, err = conn.Exec(ctx, `LISTEN core_cache_invalidation`)
		if err != nil {
			conn.Release()
			if ctx.Err() == nil {
				r.report(fmt.Errorf("listen for Core cache invalidations: %w", err))
			}
			if !waitContext(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		backoff = 100 * time.Millisecond
		for ctx.Err() == nil {
			notification, waitErr := conn.Conn().WaitForNotification(ctx)
			if waitErr != nil {
				conn.Release()
				if r.byteCache != nil {
					r.byteCache.Clear()
				}
				if ctx.Err() != nil {
					return
				}
				r.report(fmt.Errorf("core cache invalidation listener disconnected: %w", waitErr))
				break
			}
			if r.byteCache != nil {
				if notification.Payload == "*" {
					r.byteCache.Clear()
				} else {
					r.files.InvalidateCache(notification.Payload)
				}
			}
		}
		if !waitContext(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
