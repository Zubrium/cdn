package files

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnerRateLimiterIsolationAndCapacity(t *testing.T) {
	now := time.Unix(1, 0)
	limiter := newOwnerLimiter(2, 2, time.Minute)
	for i := 0; i < 2; i++ {
		if ok, _ := limiter.allow("owner:a", now); !ok {
			t.Fatal("owner a denied too soon")
		}
	}
	if ok, retry := limiter.allow("owner:a", now); ok || retry != time.Minute {
		t.Fatalf("owner a not limited: %v %v", ok, retry)
	}
	if ok, _ := limiter.allow("owner:b", now); !ok {
		t.Fatal("one owner exhausted another owner's allowance")
	}
	if ok, _ := limiter.allow("ip:c", now); ok {
		t.Fatal("client capacity not enforced")
	}
	if ok, _ := limiter.allow("ip:c", now.Add(time.Minute)); !ok {
		t.Fatal("expired clients were not reclaimed")
	}
}

func TestRateLimitReturnsRetryAfter(t *testing.T) {
	h := &Handler{limiter: newOwnerLimiter(1, 4, time.Minute)}
	r := httptest.NewRequest(http.MethodGet, "/api/v2/files", nil)
	first := httptest.NewRecorder()
	if !h.rateAllowed(first, r, "owner:first") {
		t.Fatal("first request denied")
	}
	limited := httptest.NewRecorder()
	if h.rateAllowed(limited, r, "owner:first") || limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatal("limit response missing status or Retry-After")
	}
	other := httptest.NewRecorder()
	if !h.rateAllowed(other, r, "owner:second") {
		t.Fatal("independent owner was limited")
	}
}
