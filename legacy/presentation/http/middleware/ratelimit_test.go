package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBoundsClientStateAndExpiresCounters(t *testing.T) {
	limiter := NewRateLimiter(5, time.Hour, 1, time.Hour, false)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	wrapped := limiter.Middleware(next)
	request := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/forms", nil)
		req.RemoteAddr = ip + ":1234"
		response := httptest.NewRecorder()
		wrapped.ServeHTTP(response, req)
		return response
	}

	if got := request("192.0.2.1").Code; got != http.StatusNoContent {
		t.Fatalf("first client status = %d, want 204", got)
	}
	blocked := request("192.0.2.2")
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("new client at capacity status/Retry-After = %d/%q, want 429 and retry hint", blocked.Code, blocked.Header().Get("Retry-After"))
	}
	if len(limiter.clients) != 1 {
		t.Fatalf("client state grew beyond configured capacity: %d", len(limiter.clients))
	}

	limiter.mu.Lock()
	for _, entry := range limiter.clients {
		entry.resetAt = time.Now().Add(-time.Second)
	}
	limiter.lastSweep = time.Now()
	limiter.mu.Unlock()
	if got := request("192.0.2.2").Code; got != http.StatusNoContent {
		t.Fatalf("new client after stale counter cleanup status = %d, want 204", got)
	}
	if len(limiter.clients) != 1 {
		t.Fatalf("expired counter was not replaced within configured capacity: %d", len(limiter.clients))
	}
}

func TestRateLimiterUsesConfiguredSweepInterval(t *testing.T) {
	limiter := NewRateLimiter(5, time.Hour, 2, time.Minute, false)
	now := time.Now()
	limiter.clients["192.0.2.1"] = &rateEntry{count: 1, resetAt: now.Add(-time.Second)}
	limiter.lastSweep = now.Add(-2 * time.Minute)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "/forms", nil)
	req.RemoteAddr = "192.0.2.2:1234"
	response := httptest.NewRecorder()
	limiter.Middleware(next).ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("request status = %d, want 204", response.Code)
	}
	if len(limiter.clients) != 1 {
		t.Fatalf("periodic sweep retained expired counter or failed to add new client: %#v", limiter.clients)
	}
}
