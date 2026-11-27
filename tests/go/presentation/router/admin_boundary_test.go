package router

import (
	"context"
	"encoding/json"
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
	}}, Config{MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10})
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

type adminRouteContractAPI struct {
	cacheScenarioHandlerAPI
	route contracts.Route
}

func (api adminRouteContractAPI) Authenticate(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

func (api adminRouteContractAPI) ListRoutes(context.Context, contracts.ListRoutesRequest) (contracts.RoutePage, error) {
	return contracts.RoutePage{Items: []contracts.Route{api.route}, NextCursor: "next"}, nil
}

func (api adminRouteContractAPI) GetRoute(context.Context, string) (contracts.Route, error) {
	return api.route, nil
}

func (api adminRouteContractAPI) CreateRoute(context.Context, contracts.CreateRouteRequest) (contracts.Route, error) {
	return api.route, nil
}

func (api adminRouteContractAPI) ReplaceRouteTarget(context.Context, contracts.ReplaceRouteTargetRequest) (contracts.Route, error) {
	return api.route, nil
}

func TestRouterAdminRouteResponsesUseSingleListenerContract(t *testing.T) {
	const routeID = "01938f7a-68af-7c10-a51d-78b6ca125301"
	const target = `"target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125302"}`
	api := adminRouteContractAPI{route: contracts.Route{ID: routeID, Match: "exact", Revision: 1}}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		method     string
		suffix     string
		body       string
		wantStatus int
	}{
		{http.MethodGet, "", "", http.StatusOK},
		{http.MethodGet, "/" + routeID, "", http.StatusOK},
		{http.MethodPost, "", `{"host":"example.test","path":"/","match":"exact",` + target + `}`, http.StatusCreated},
		{http.MethodPut, "/" + routeID + "/target", `{` + target + `}`, http.StatusOK},
		{http.MethodPost, "", `{"unexpected_field":"value","host":"example.test","path":"/","match":"exact",` + target + `}`, http.StatusBadRequest},
	} {
		t.Run(scenario.method+scenario.suffix, func(t *testing.T) {
			request := httptest.NewRequest(scenario.method, routesPath+scenario.suffix, strings.NewReader(scenario.body))
			request.Header.Set("Authorization", "Bearer route-contract-test")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "single-listener-contract-test")
			request.Header.Set("If-Match", `"1"`)
			response := httptest.NewRecorder()
			handler.AdminHTTPHandler().ServeHTTP(response, request)
			if response.Code != scenario.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, scenario.wantStatus, response.Body)
			}
			if response.Code == http.StatusBadRequest {
				return
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if scenario.method == http.MethodGet && scenario.suffix == "" {
				var items []map[string]json.RawMessage
				if err := json.Unmarshal(body["items"], &items); err != nil || len(items) != 1 || string(body["next_cursor"]) != `"next"` {
					t.Fatalf("unexpected page: %s", response.Body)
				}
				body = items[0]
			}
			if _, exists := body["unexpected_field"]; exists || string(body["id"]) != `"`+routeID+`"` {
				t.Fatalf("unexpected route: %s", response.Body)
			}
		})
	}
}
