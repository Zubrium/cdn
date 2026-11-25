package router

import (
	"context"
	"github.com/Zubrium/cdn/pkg/contracts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterPublicListenerDoesNotServeAdminOperations(t *testing.T) {
	handler, err := NewHandler(cacheScenarioHandlerAPI{deliver: func(context.Context, contracts.DeliveryRequest) (contracts.DeliveryResponse, error) {
		t.Fatal("public delivery must not be called for an Admin path")
		return contracts.DeliveryResponse{}, nil
	}}, Config{BranchID: "main", MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/admin/router/operations",
		"/api/admin/router/routes",
		"/api/admin/model",
		"/api/admin/unknown-module-route",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("public GET %s = %d, want 404", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "not_found") {
			t.Errorf("public GET %s body = %q, want not_found", path, response.Body.String())
		}
	}

	// The Router Admin surface is still mounted, but only on the dedicated
	// handler that composition attaches to the authenticated Admin listener.
	adminResponse := httptest.NewRecorder()
	handler.AdminHTTPHandler().ServeHTTP(adminResponse, httptest.NewRequest(http.MethodGet, "/api/admin/router/operations", nil))
	if adminResponse.Code != http.StatusUnauthorized {
		t.Errorf("admin GET /api/admin/router/operations = %d, want 401", adminResponse.Code)
	}
}
