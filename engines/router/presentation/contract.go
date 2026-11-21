// Package presentation defines Router's aggregate typed facade. Settings and
// public delivery are separate capabilities; neither depends on Website APIs.
package presentation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
	routemodel "github.com/Zubrium/cdn/engines/router/features/routes/model"
)

const (
	TargetCoreFile                = routemodel.TargetCoreFile
	TargetStaticSiteArtifact      = routemodel.TargetStaticSiteArtifact
	TargetRegisteredOperation     = routemodel.TargetRegisteredOperation
	maxPublicOperationConfigBytes = 64 << 10
)

var ErrInvalidTarget = routemodel.ErrInvalidRoute
var (
	ErrUnauthorized                 = errors.New("unauthorized")
	ErrForbidden                    = errors.New("forbidden")
	ErrNotFound                     = errors.New("not found")
	ErrConflict                     = errors.New("revision conflict")
	ErrInvalid                      = errors.New("invalid route request")
	ErrUnavailable                  = errors.New("Router dependency unavailable")
	ErrTooLarge                     = errors.New("public operation request exceeds its size limit")
	ErrInvalidPublicOperation       = errors.New("invalid public operation")
	ErrPublicOperationNotRegistered = errors.New("public operation is not registered")
)

type Contract interface {
	platform.Module
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
	DeleteRoute(context.Context, DeleteRouteRequest) error
}

type DeliveryAPI interface {
	Deliver(context.Context, DeliveryRequest) (DeliveryResponse, error)
}

type RouteTarget = routemodel.Target

func ValidPublicOperationID(value string) bool {
	return routemodel.ValidPublicOperationID(value)
}

type Route = routemodel.Route

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
	FormSessionTokens map[string]string
}

// PublicOperationDescriptor is immutable metadata from one compiled engine
// operation. Only operations with a public.* ID may be put on a Router route.
type PublicOperationDescriptor struct {
	ID                  string   `json:"id"`
	ContractVersion     int      `json:"contract_version"`
	Methods             []string `json:"methods"`
	MaxRequestBytes     int64    `json:"max_request_bytes"`
	AllowedContentTypes []string `json:"allowed_content_types"`
}

// PublicOperationRequest contains a normalized request and the server-side
// route configuration. Cookies and arbitrary HTTP headers are never forwarded.
type PublicOperationRequest struct {
	Method            string
	Host              string
	EscapedPath       string
	Query             string
	ContentType       string
	ContentLength     int64
	IdempotencyKey    string
	IfNoneMatch       string
	Body              io.Reader
	FormSessionTokens map[string]string
}

type PublicOperation interface {
	Descriptor() PublicOperationDescriptor
	ValidateRouteConfig(json.RawMessage) error
	Execute(context.Context, json.RawMessage, PublicOperationRequest) (DeliveryResponse, error)
}

// PublicOperationCatalog is built from static imports selected by the profile.
// It cannot load callbacks, modules, or network destinations from route data.
type PublicOperationCatalog interface {
	ListPublicOperations(context.Context) ([]PublicOperationDescriptor, error)
	ResolvePublicOperation(context.Context, string) (PublicOperation, error)
	ValidatePublicOperationTarget(context.Context, RouteTarget) error
}

type DeliveryResponse struct {
	StatusCode         int
	ContentType        string
	ContentLength      int64
	ETag               string
	LastModified       string
	CacheControl       string
	ContentDisposition string
	AcceptRanges       string
	Body               io.ReadCloser
	FormSessionCookie  *FormSessionCookieChange
}

// FormSessionCookieChange is a typed session transport instruction. HTTP
// cookie parsing and serialization remain in the Router Presentation adapter.
type FormSessionCookieChange struct {
	FormID    string
	Token     string
	ExpiresAt time.Time
	Clear     bool
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.router", Kind: "engine", Version: 1, AccessGroups: []string{"router_routes"}}
}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "route.list", Action: platform.AccessActionRead, ResourceType: "router-route"},
		{Name: "route.read", Action: platform.AccessActionRead, ResourceType: "router-route"},
		{Name: "route.create", Action: platform.AccessActionWrite, ResourceType: "router-route"},
		{Name: "route.target.replace", Action: platform.AccessActionWrite, ResourceType: "router-route"},
		{Name: "route.delete", Action: platform.AccessActionDelete, ResourceType: "router-route"},
		{Name: "route.deliver", Action: platform.AccessActionRead, ResourceType: "configured-route"},
	}
}

var _ platform.Module = Descriptor{}
