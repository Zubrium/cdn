package files

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"websiteengine/pkg/response"
)

type rateWindow struct {
	count   int
	expires time.Time
}
type ownerLimiter struct {
	mu                sync.Mutex
	entries           map[string]rateWindow
	limit, maxClients int
	window            time.Duration
}

func newOwnerLimiter(limit, maxClients int, window time.Duration) *ownerLimiter {
	return &ownerLimiter{entries: make(map[string]rateWindow), limit: limit, maxClients: maxClients, window: window}
}

func (l *ownerLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || !now.Before(entry.expires) {
		if len(l.entries) >= l.maxClients {
			for k, value := range l.entries {
				if !now.Before(value.expires) {
					delete(l.entries, k)
				}
			}
			if len(l.entries) >= l.maxClients {
				return false, l.window
			}
		}
		entry = rateWindow{expires: now.Add(l.window)}
	}
	if entry.count >= l.limit {
		return false, entry.expires.Sub(now)
	}
	entry.count++
	l.entries[key] = entry
	return true, 0
}

func (h *Handler) rateAllowed(w http.ResponseWriter, r *http.Request, key string) bool {
	ok, retry := h.limiter.allow(key, time.Now())
	if !ok {
		seconds := int(retry / time.Second)
		if retry%time.Second != 0 {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		response.Error(w, r, http.StatusTooManyRequests, "rate_limited", "Слишком много запросов. Повторите позже")
	}
	return ok
}
