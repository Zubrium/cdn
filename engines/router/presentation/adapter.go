package presentation

import (
	"context"
	"errors"
	"reflect"

	platform "github.com/Zubrium/cdn/core/presentation"
	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	"github.com/Zubrium/cdn/engines/router/features/routes/model"
)

type RouteUseCase interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	ListRoutes(context.Context, string, string, string, int) ([]model.Route, string, error)
	GetRoute(context.Context, string, string) (model.Route, error)
	CreateRoute(context.Context, routesapp.CreateRouteInput) (model.Route, error)
	ReplaceTarget(context.Context, string, routesapp.ReplaceTargetInput) (model.Route, error)
	DeleteRoute(context.Context, string, routesapp.DeleteRouteInput) error
}

// APIAdapter maps the engine's route use cases onto its stable public facade.
// Public delivery can be attached independently; a missing delivery adapter
// fails closed with 503 through ErrUnavailable.
type APIAdapter struct {
	Descriptor
	branchID string
	routes   RouteUseCase
	delivery DeliveryAPI
}

func NewAPIAdapter(routes RouteUseCase, delivery DeliveryAPI, branchID string) (*APIAdapter, error) {
	if isNilAdapterDependency(routes) || branchID == "" || len(branchID) > 128 {
		return nil, ErrInvalid
	}
	return &APIAdapter{branchID: branchID, routes: routes, delivery: delivery}, nil
}

func (adapter *APIAdapter) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if adapter == nil || adapter.routes == nil {
		return nil, ErrUnavailable
	}
	return adapter.routes.Authenticate(ctx, credential)
}

func (adapter *APIAdapter) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if adapter == nil || adapter.routes == nil {
		return nil, ErrUnavailable
	}
	return adapter.routes.AuthenticateSession(ctx, token)
}

func (adapter *APIAdapter) ListRoutes(ctx context.Context, input ListRoutesRequest) (RoutePage, error) {
	if adapter == nil || adapter.routes == nil {
		return RoutePage{}, ErrUnavailable
	}
	items, next, err := adapter.routes.ListRoutes(ctx, input.BranchID, input.Host, input.Cursor, input.Limit)
	if err != nil {
		return RoutePage{}, mapApplicationError(err)
	}
	if items == nil {
		items = []Route{}
	}
	return RoutePage{Items: items, NextCursor: next}, nil
}

func (adapter *APIAdapter) GetRoute(ctx context.Context, id string) (Route, error) {
	if adapter == nil || adapter.routes == nil {
		return Route{}, ErrUnavailable
	}
	route, err := adapter.routes.GetRoute(ctx, adapter.branchID, id)
	if err != nil {
		return Route{}, mapApplicationError(err)
	}
	return route, nil
}

func (adapter *APIAdapter) CreateRoute(ctx context.Context, input CreateRouteRequest) (Route, error) {
	if adapter == nil || adapter.routes == nil {
		return Route{}, ErrUnavailable
	}
	route, err := adapter.routes.CreateRoute(ctx, routesapp.CreateRouteInput{
		BranchID: input.BranchID, Host: input.Host, Path: input.Path, Match: input.Match,
		Target: input.Target, IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		return Route{}, mapApplicationError(err)
	}
	return route, nil
}

func (adapter *APIAdapter) ReplaceRouteTarget(ctx context.Context, input ReplaceRouteTargetRequest) (Route, error) {
	if adapter == nil || adapter.routes == nil {
		return Route{}, ErrUnavailable
	}
	route, err := adapter.routes.ReplaceTarget(ctx, adapter.branchID, routesapp.ReplaceTargetInput{
		RouteID: input.RouteID, Target: input.Target, ExpectedRevision: input.ExpectedRevision,
	})
	if err != nil {
		return Route{}, mapApplicationError(err)
	}
	return route, nil
}

func (adapter *APIAdapter) DeleteRoute(ctx context.Context, input DeleteRouteRequest) error {
	if adapter == nil || adapter.routes == nil {
		return ErrUnavailable
	}
	return mapApplicationError(adapter.routes.DeleteRoute(ctx, adapter.branchID, routesapp.DeleteRouteInput{
		RouteID: input.RouteID, ExpectedRevision: input.ExpectedRevision,
	}))
}

func (adapter *APIAdapter) Deliver(ctx context.Context, request DeliveryRequest) (DeliveryResponse, error) {
	if adapter == nil || adapter.delivery == nil {
		return DeliveryResponse{}, ErrUnavailable
	}
	return adapter.delivery.Deliver(ctx, request)
}

func mapApplicationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, routesapp.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, routesapp.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, routesapp.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, routesapp.ErrConflict):
		return ErrConflict
	case errors.Is(err, routesapp.ErrInvalid), errors.Is(err, model.ErrInvalidRoute):
		return ErrInvalid
	default:
		return ErrUnavailable
	}
}

func isNilAdapterDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ Contract = (*APIAdapter)(nil)
var _ platform.Module = (*APIAdapter)(nil)
