package features

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFeatureListsFailClosedWithoutConfiguredPagination(t *testing.T) {
	handler := NewHandler(nil, nil)
	for _, route := range []func(http.ResponseWriter, *http.Request){handler.List, handler.Versions} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/?limit=1", nil)
		route(response, request)
		if response.Code != 500 {
			t.Fatalf("unconfigured feature list status = %d, want 500", response.Code)
		}
	}
}

func TestFeaturePaginationRejectsInvalidConfiguration(t *testing.T) {
	if err := NewHandler(nil, nil).SetPagination(0, 200); err == nil {
		t.Fatal("zero default page size accepted")
	}
	if err := NewHandler(nil, nil).SetPagination(50, 49); err == nil {
		t.Fatal("maximum below default page size accepted")
	}
}
