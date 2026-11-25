package contracts

import (
	"context"
	"io"
)

// Router's settings and delivery facade is owned by the neutral contract
// package. The Router engine keeps aliases so its internal callers do not
// change, while outer Presentation imports only this package.
type Contract interface {
	SettingsAPI
	DeliveryAPI
}

type SettingsAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	ListRoutes(context.Context, ListRoutesRequest) (RoutePage, error)
	GetRoute(context.Context, string) (Route, error)
	CreateRoute(context.Context, CreateRouteRequest) (Route, error)
	ReplaceRouteTarget(context.Context, ReplaceRouteTargetRequest) (Route, error)
	ReplaceLocaleTarget(context.Context, ReplaceLocaleTargetRequest) (Route, error)
	DeleteRoute(context.Context, DeleteRouteRequest) error
}

type DeliveryAPI interface {
	Deliver(context.Context, DeliveryRequest) (DeliveryResponse, error)
}

// RouteTarget is the neutral facade name for the persisted route target.
type RouteTarget = Target

// RouteStateActive is the facade form of the active route state constant.
const RouteStateActive = StateActive

// ValidateRoute is the facade form of persisted-route validation used by
// warm-up before a route cache entry is primed.
func ValidateRoute(route Route) error { return route.ValidateForStorage() }

type ListRoutesRequest struct {
	BranchID string
	Host     string
	Cursor   string
	Limit    int
}

type RoutePage struct {
	Items      []Route `json:"items"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type CreateRouteRequest struct {
	BranchID       string      `json:"-"`
	Host           string      `json:"host"`
	Path           string      `json:"path"`
	Match          string      `json:"match"`
	Target         RouteTarget `json:"target"`
	IdempotencyKey string      `json:"-"`
}

type ReplaceRouteTargetRequest struct {
	RouteID          string      `json:"route_id"`
	Target           RouteTarget `json:"target"`
	ExpectedRevision int64       `json:"expected_revision"`
}

// ReplaceLocaleTargetRequest is the trusted facade input used by outer
// orchestration. Locale is a server-configured catalog slot, never a visitor
// selection.
type ReplaceLocaleTargetRequest struct {
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
	ExpectedRevision       int64  `json:"expected_revision"`
	IdempotencyKey         string `json:"-"`
}

type DeleteRouteRequest struct {
	RouteID          string `json:"route_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type DeliveryRequest struct {
	BranchID          string
	Host              string
	Method            string
	EscapedPath       string
	Query             string
	ContentType       string
	ContentLength     int64
	IdempotencyKey    string
	Body              io.Reader
	IfNoneMatch       string
	IfModifiedSince   string
	Range             string
	IfRange           string
	LocalePreference  string
	AcceptLanguage    string
	FormSessionTokens map[string]string
	// Streams is supplied by the HTTP boundary for scoped upload capability.
	// It is never carried in a visitor payload or route configuration.
	Streams PublicUploadStreams
}

// PublicOperationCatalog is built from static imports selected by the profile.
// It cannot load callbacks, modules, or network destinations from route data.
type PublicOperationCatalog interface {
	ListPublicOperations(context.Context) ([]PublicOperationDescriptor, error)
	ResolvePublicOperation(context.Context, string) (PublicOperation, error)
	ValidatePublicOperationTarget(context.Context, RouteTarget) error
}

// RouterDescriptor is the neutral component descriptor helper. The engine
// facade aliases it under its historical Descriptor name.
type RouterDescriptor struct{}

func (RouterDescriptor) ComponentDescriptor() ComponentDescriptor {
	return ComponentDescriptor{ID: "engine.router", Kind: "engine", Version: 1, AccessGroups: []string{"router_routes"}}
}
