package presentation

import (
	"context"
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

func (r *Runtime) listenInvalidations(ctx context.Context) {
	defer close(r.notifyDone)
	postgres.WatchCacheInvalidations(ctx, r.database, func() {
		// PostgreSQL is authoritative. Clearing on every reconnect accounts for
		// notifications missed while this instance was disconnected.
		if r.byteCache != nil {
			r.byteCache.Clear()
		}
	}, func(payload string) {
		if r.byteCache == nil {
			return
		}
		if payload == "*" {
			r.byteCache.Clear()
		} else {
			r.files.InvalidateCache(payload)
		}
	}, r.report)
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
