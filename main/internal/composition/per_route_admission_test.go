package composition

import (
	"context"
	"errors"
	"strings"
	"testing"

	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	routemodel "github.com/Zubrium/cdn/engines/router/features/routes/model"
	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

type staticRouteUseCase struct {
	routes map[string]routemodel.Route
}

func (useCase staticRouteUseCase) Authenticate(context.Context, string) (context.Context, error) {
	return context.Background(), nil
}

func (useCase staticRouteUseCase) AuthenticateSession(context.Context, string) (context.Context, error) {
	return context.Background(), nil
}

func (useCase staticRouteUseCase) ListRoutes(context.Context, string, string, string, int) ([]routemodel.Route, string, error) {
	items := make([]routemodel.Route, 0, len(useCase.routes))
	for _, route := range useCase.routes {
		items = append(items, route)
	}
	return items, "", nil
}

func (useCase staticRouteUseCase) GetRoute(_ context.Context, _, id string) (routemodel.Route, error) {
	route, ok := useCase.routes[id]
	if !ok {
		return routemodel.Route{}, errors.New("route not found")
	}
	return route, nil
}

func (useCase staticRouteUseCase) CreateRoute(context.Context, routesapp.CreateRouteInput) (routemodel.Route, error) {
	return routemodel.Route{}, errors.New("not supported")
}

func (useCase staticRouteUseCase) ReplaceTarget(context.Context, string, routesapp.ReplaceTargetInput) (routemodel.Route, error) {
	return routemodel.Route{}, errors.New("not supported")
}

func (useCase staticRouteUseCase) ReplaceLocaleTarget(context.Context, string, routesapp.ReplaceLocaleTargetInput) (routemodel.Route, error) {
	return routemodel.Route{}, errors.New("not supported")
}

func (useCase staticRouteUseCase) DeleteRoute(context.Context, string, routesapp.DeleteRouteInput) error {
	return errors.New("not supported")
}

func TestResolvePerRouteAdmissionUsesStartupRouteIDs(t *testing.T) {
	adapter, err := routerapi.NewAPIAdapter(staticRouteUseCase{routes: map[string]routemodel.Route{
		"route-1": {ID: "route-1", Path: "/qa", BranchID: "main"},
	}}, nil, "main")
	if err != nil {
		t.Fatal(err)
	}
	component := &publicRouterComponent{engine: &routerEngineComponent{api: adapter}}
	resolution := appconfig.NamedAdmission{
		Enabled:            true,
		DefaultMaxInFlight: 8,
		Overrides:          []appconfig.NamedLimit{{ID: "route-1", MaxInFlight: 2}},
	}
	component.config.Scales.RouterHTTP.PerRoute = resolution

	admission, err := resolvePerRouteAdmission(context.Background(), component)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Enabled || admission.DefaultMaxInFlight != 8 || len(admission.Overrides) != 1 {
		t.Fatalf("unexpected admission: %+v", admission)
	}
	if got := admission.Overrides[0]; got.ID != "route-1" || got.Path != "/qa" || got.MaxInFlight != 2 {
		t.Fatalf("override was not resolved from the static route ID: %+v", got)
	}

	resolution.Overrides[0].ID = "missing-route"
	component.config.Scales.RouterHTTP.PerRoute = resolution
	if _, err := resolvePerRouteAdmission(context.Background(), component); err == nil ||
		!strings.Contains(err.Error(), "missing-route") {
		t.Fatalf("unresolvable per-route key did not fail closed: %v", err)
	}

	component.config.Scales.RouterHTTP.PerRoute = appconfig.NamedAdmission{}
	disabled, err := resolvePerRouteAdmission(context.Background(), component)
	if err != nil || disabled.Enabled || len(disabled.Overrides) != 0 {
		t.Fatalf("disabled table was not empty: %+v err=%v", disabled, err)
	}
}
