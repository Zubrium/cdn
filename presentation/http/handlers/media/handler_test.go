package media

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestMediaListFailsClosedWithoutConfiguredPagination(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(nil, nil, 1).List(response, httptest.NewRequest("GET", "/?limit=1", nil))
	if response.Code != 500 {
		t.Fatalf("unconfigured media list status = %d, want 500", response.Code)
	}
}

func TestMediaPaginationRejectsInvalidConfiguration(t *testing.T) {
	if err := NewHandler(nil, nil, 1).SetPagination(0, 200); err == nil {
		t.Fatal("zero default page size accepted")
	}
	if err := NewHandler(nil, nil, 1).SetPagination(50, 49); err == nil {
		t.Fatal("maximum below default page size accepted")
	}
}

func TestMediaRedirectCacheTTLBoundaries(t *testing.T) {
	handler := NewHandler(nil, nil, 1)
	for _, invalid := range []time.Duration{0, 999 * time.Millisecond, 1500 * time.Millisecond} {
		if err := handler.SetRedirectCacheTTL(invalid); err == nil {
			t.Fatalf("accepted redirect cache TTL %s", invalid)
		}
	}
	for _, test := range []struct {
		ttl  time.Duration
		want string
	}{{time.Second, "public, max-age=1"}, {5 * time.Minute, "public, max-age=300"}} {
		if err := handler.SetRedirectCacheTTL(test.ttl); err != nil {
			t.Fatalf("SetRedirectCacheTTL(%s): %v", test.ttl, err)
		}
		if got := redirectCacheControl(handler.redirectCacheTTL); got != test.want {
			t.Fatalf("redirect cache header for %s = %q, want %q", test.ttl, got, test.want)
		}
	}
}

func TestLegacyMediaRedirectFailsClosedWithoutCacheTTL(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(nil, nil, 1).Redirect(response, httptest.NewRequest("GET", "/api/v1/media/00000000-0000-0000-0000-000000000001", nil))
	if response.Code != 500 {
		t.Fatalf("unconfigured redirect status = %d, want 500", response.Code)
	}
}
