package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	engine "github.com/Zubrium/cdn/engines/router/presentation"
	routeradmin "github.com/Zubrium/cdn/presentation/admin/router"
)

type fakeEngine struct{ deliveryCalls int }

func (fakeEngine) ComponentDescriptor() core.ComponentDescriptor {
	return core.ComponentDescriptor{ID: "engine.router", Kind: "engine", Version: 1}
}
func (fakeEngine) Operations() []core.Operation { return nil }
func (fakeEngine) Authenticate(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}
func (fakeEngine) AuthenticateSession(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}
func (fakeEngine) ListRoutes(context.Context, engine.ListRoutesRequest) (engine.RoutePage, error) {
	return engine.RoutePage{Items: []engine.Route{}}, nil
}
func (fakeEngine) GetRoute(context.Context, string) (engine.Route, error) {
	return engine.Route{}, engine.ErrNotFound
}
func (fakeEngine) CreateRoute(context.Context, engine.CreateRouteRequest) (engine.Route, error) {
	return engine.Route{}, engine.ErrUnavailable
}
func (fakeEngine) ReplaceRouteTarget(context.Context, engine.ReplaceRouteTargetRequest) (engine.Route, error) {
	return engine.Route{}, engine.ErrUnavailable
}
func (fakeEngine) ReplaceLocaleTarget(context.Context, engine.ReplaceLocaleTargetRequest) (engine.Route, error) {
	return engine.Route{}, engine.ErrUnavailable
}
func (fakeEngine) DeleteRoute(context.Context, engine.DeleteRouteRequest) error {
	return engine.ErrUnavailable
}
func (api *fakeEngine) Deliver(context.Context, engine.DeliveryRequest) (engine.DeliveryResponse, error) {
	api.deliveryCalls++
	return engine.DeliveryResponse{}, engine.ErrUnavailable
}

func TestAdminRouterAdapterDoesNotExposePublicDeliveryRoutes(t *testing.T) {
	api := &fakeEngine{}
	handler, err := routeradmin.NewHandler(api, routeradmin.Config{BranchID: "main", MaxJSONBytes: 4096,
		DefaultPageSize: 20, MaxPageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/assets/app.js", "/api/admin/router/routes"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if path == "/api/admin/router/routes" {
			if response.Code != http.StatusUnauthorized {
				t.Errorf("Admin route without auth status=%d body=%s", response.Code, response.Body.String())
			}
		} else if response.Code != http.StatusNotFound {
			t.Errorf("Admin adapter exposed public path %q with status %d", path, response.Code)
		}
	}
	if api.deliveryCalls != 0 {
		t.Fatalf("Admin adapter invoked public delivery %d times", api.deliveryCalls)
	}
}
