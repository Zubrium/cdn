package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
)

type fakeRouter struct {
	authCalls     int
	listInput     engine.ListRoutesRequest
	createInput   engine.CreateRouteRequest
	deliveryInput engine.DeliveryRequest
	createRoute   engine.Route
	delivery      engine.DeliveryResponse
	authErr       error
	deliverErr    error
}

func (f *fakeRouter) Authenticate(ctx context.Context, _ string) (context.Context, error) {
	f.authCalls++
	if f.authErr != nil {
		return nil, f.authErr
	}
	return context.WithValue(ctx, struct{}{}, "actor"), nil
}
func (f *fakeRouter) ListRoutes(_ context.Context, input engine.ListRoutesRequest) (engine.RoutePage, error) {
	f.listInput = input
	return engine.RoutePage{Items: []engine.Route{}}, nil
}
func (*fakeRouter) GetRoute(context.Context, string) (engine.Route, error) {
	return engine.Route{}, nil
}
func (f *fakeRouter) CreateRoute(_ context.Context, input engine.CreateRouteRequest) (engine.Route, error) {
	f.createInput = input
	return f.createRoute, nil
}
func (*fakeRouter) ReplaceRouteTarget(context.Context, engine.ReplaceRouteTargetRequest) (engine.Route, error) {
	return engine.Route{}, nil
}
func (*fakeRouter) DeleteRoute(context.Context, engine.DeleteRouteRequest) error { return nil }
func (f *fakeRouter) Deliver(_ context.Context, input engine.DeliveryRequest) (engine.DeliveryResponse, error) {
	f.deliveryInput = input
	return f.delivery, f.deliverErr
}

func TestSettingsAPIAuthenticatesAndPinsConfiguredBranch(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, Config{BranchID: "storefront", MaxJSONBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/router/routes?branch_id=attacker&host=site.example", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.authCalls != 1 {
		t.Fatalf("status=%d auth=%d body=%s", response.Code, api.authCalls, response.Body.String())
	}
	if api.listInput.BranchID != "storefront" || api.listInput.Host != "site.example" {
		t.Fatalf("request escaped configured branch: %+v", api.listInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", response.Header().Get("Cache-Control"))
	}
}

func TestCreateRouteAcceptsOnlyExplicitCoreTargets(t *testing.T) {
	api := &fakeRouter{createRoute: engine.Route{ID: "route-id", State: "active"}}
	handler, err := NewHandler(api, Config{BranchID: "storefront", MaxJSONBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/router/routes", strings.NewReader(`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-0001")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if api.createInput.BranchID != "storefront" || api.createInput.Target.Kind != engine.TargetCoreFile || api.createInput.IdempotencyKey != "router-route-create-0001" {
		t.Fatalf("unexpected route request: %+v", api.createInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", response.Header().Get("Cache-Control"))
	}
}

func TestCreateRouteRejectsHandlersAndUnknownFields(t *testing.T) {
	for _, body := range []string{
		`{"host":"site.example","path":"/","match":"exact","target":{"kind":"handler","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`,
		`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"},"handler":"evil"}`,
	} {
		api := &fakeRouter{}
		handler, err := NewHandler(api, Config{BranchID: "storefront", MaxJSONBytes: 4096})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/router/routes", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer admin-secret")
		request.Header.Set("Idempotency-Key", "router-route-create-0001")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
			t.Fatalf("status=%d create=%+v body=%s", response.Code, api.createInput, response.Body.String())
		}
	}
}

func TestDeliveryUsesConfiguredBranchAndIgnoresForwardedHost(t *testing.T) {
	api := &fakeRouter{delivery: engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: "text/html; charset=utf-8", ContentLength: 5, Body: io.NopCloser(strings.NewReader("hello"))}}
	handler, err := NewHandler(api, Config{BranchID: "storefront", MaxJSONBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://public.example/catalog?sort=asc", nil)
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("Range", "bytes=0-4")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "hello" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if api.deliveryInput.BranchID != "storefront" || api.deliveryInput.Host != "public.example" || api.deliveryInput.EscapedPath != "/catalog" || api.deliveryInput.Query != "sort=asc" || api.deliveryInput.Range != "bytes=0-4" {
		t.Fatalf("unexpected delivery input: %+v", api.deliveryInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe delivery headers: %v", response.Header())
	}
}

func TestDeliveryDoesNotCacheAndDoesNotLeakEngineErrors(t *testing.T) {
	api := &fakeRouter{deliverErr: engine.ErrUnavailable}
	handler, err := NewHandler(api, Config{BranchID: "storefront", MaxJSONBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://public.example/missing", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}
