package files

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"websiteengine/pkg/principal"
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

func TestAuthenticatedAdminRateLimitIsolatedByPrincipal(t *testing.T) {
	limiter := newOwnerLimiter(1, 4, time.Minute)
	h := &Handler{limiter: limiter}
	a := principal.Principal{TokenID: uuid.New(), Role: principal.RoleUser}
	b := principal.Principal{TokenID: uuid.New(), Role: principal.RoleUser}
	requestFor := func(p principal.Principal) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/v2/admin/files/limits", nil).
			WithContext(principal.WithPrincipal(context.Background(), p))
	}
	keyA, ok := h.adminPrincipalRateKey(requestFor(a))
	if !ok {
		t.Fatal("authenticated principal must produce a rate-limit key")
	}
	keyB, ok := h.adminPrincipalRateKey(requestFor(b))
	if !ok || keyA == keyB {
		t.Fatal("distinct authenticated principals must have distinct rate-limit keys")
	}
	if ok, _ := limiter.allow(keyA, time.Now()); !ok {
		t.Fatal("first principal request denied")
	}
	if ok, _ := limiter.allow(keyA, time.Now()); ok {
		t.Fatal("first principal should exhaust only their own allowance")
	}
	if ok, _ := limiter.allow(keyB, time.Now()); !ok {
		t.Fatal("one principal exhausted another principal's allowance")
	}
}

func TestAdminPrincipalRateKeyRejectsMissingPrincipalIdentity(t *testing.T) {
	h := &Handler{limiter: newOwnerLimiter(1, 4, time.Minute)}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/files/limits", nil).
		WithContext(principal.WithPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}))
	if _, ok := h.adminPrincipalRateKey(request); ok {
		t.Fatal("principal without a token ID must not share the nil-UUID rate-limit bucket")
	}
}
