package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"websiteengine/pkg/response"
)

type rateEntry struct {
	count   int
	resetAt time.Time
}

// RateLimiter is a small per-process fixed-window limiter suitable for public
// form submission. For multi-instance deployments use a shared limiter.
type RateLimiter struct {
	mu            sync.Mutex
	clients       map[string]*rateEntry
	limit         int
	window        time.Duration
	maxClients    int
	sweepInterval time.Duration
	trustProxy    bool
	lastSweep     time.Time
}

func NewRateLimiter(limit int, window time.Duration, maxClients int, sweepInterval time.Duration, trustProxy bool) *RateLimiter {
	return &RateLimiter{
		clients:       make(map[string]*rateEntry),
		limit:         limit,
		window:        window,
		maxClients:    maxClients,
		sweepInterval: sweepInterval,
		trustProxy:    trustProxy,
	}
}

func (l *RateLimiter) sweepExpired(now time.Time) {
	for key, entry := range l.clients {
		if !now.Before(entry.resetAt) {
			delete(l.clients, key)
		}
	}
	l.lastSweep = now
}

func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r, l.trustProxy)
		now := time.Now()

		l.mu.Lock()
		if l.lastSweep.IsZero() || now.Sub(l.lastSweep) >= l.sweepInterval {
			l.sweepExpired(now)
		}
		entry, ok := l.clients[ip]
		capacityExceeded := false
		if !ok && len(l.clients) >= l.maxClients {
			l.sweepExpired(now)
			capacityExceeded = len(l.clients) >= l.maxClients
		}
		if capacityExceeded {
			nextReset := time.Time{}
			for _, client := range l.clients {
				if nextReset.IsZero() || client.resetAt.Before(nextReset) {
					nextReset = client.resetAt
				}
			}
			retryAfter := max(int(time.Until(nextReset).Seconds())+1, 1)
			l.mu.Unlock()
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.limit))
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			response.Error(w, r, http.StatusTooManyRequests, "rate_limited", "Слишком много запросов. Повторите позже")
			return
		}
		if !ok || now.After(entry.resetAt) {
			entry = &rateEntry{count: 0, resetAt: now.Add(l.window)}
			l.clients[ip] = entry
		}
		entry.count++
		remaining := max(l.limit-entry.count, 0)
		exceeded := entry.count > l.limit
		retryAfter := int(time.Until(entry.resetAt).Seconds()) + 1
		l.mu.Unlock()

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if exceeded {
			w.Header().Set("Retry-After", strconv.Itoa(max(retryAfter, 1)))
			response.Error(w, r, http.StatusTooManyRequests, "rate_limited", "Слишком много запросов. Повторите позже")
			return
		}
		next.ServeHTTP(w, r)
	})
}
