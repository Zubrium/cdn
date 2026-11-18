package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WatchCacheInvalidations listens to the shared PostgreSQL channel and
// reconnects with bounded backoff. A reconnect callback lets each process
// discard cached state that may have missed notifications while disconnected.
func WatchCacheInvalidations(
	ctx context.Context,
	pool *pgxpool.Pool,
	onReconnect func(),
	onPayload func(string),
	onError func(error),
) {
	if ctx == nil || pool == nil {
		return
	}
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			reportListenerError(ctx, onError, "core cache invalidation connection", err)
			if !waitListener(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		_, err = conn.Exec(ctx, `LISTEN core_cache_invalidation`)
		if err != nil {
			conn.Release()
			reportListenerError(ctx, onError, "listen for Core cache invalidations", err)
			if !waitListener(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		backoff = 100 * time.Millisecond
		if onReconnect != nil {
			onReconnect()
		}
		for ctx.Err() == nil {
			notification, waitErr := conn.Conn().WaitForNotification(ctx)
			if waitErr != nil {
				conn.Release()
				reportListenerError(ctx, onError, "Core cache invalidation listener disconnected", waitErr)
				break
			}
			if onPayload != nil {
				onPayload(notification.Payload)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if !waitListener(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func reportListenerError(ctx context.Context, report func(error), message string, err error) {
	if report != nil && err != nil && ctx.Err() == nil {
		report(fmt.Errorf("%s: %w", message, err))
	}
}

func waitListener(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
