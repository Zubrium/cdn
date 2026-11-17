// Package presentation defines Router's aggregate typed facade. Settings and
// public delivery are separate capabilities; neither depends on Website APIs.
package presentation

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

const (
	TargetCoreFile          = "core-file"
	TargetStaticSiteArtifact = "static-site-artifact"
)

var ErrInvalidTarget = errors.New("invalid route target")
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("revision conflict")
	ErrInvalid      = errors.New("invalid route request")
	ErrUnavailable  = errors.New("Router dependency unavailable")
)

type Contract interface {
	platform.Module
	SettingsAPI
	DeliveryAPI
}

type SettingsAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	ListRoutes(context.Context, ListRoutesRequest) (RoutePage, error)
	GetRoute(context.Context, string) (Route, error)
	CreateRoute(context.Context, CreateRouteRequest) (Route, error)
	ReplaceRouteTarget(context.Context, ReplaceRouteTargetRequest) (Route, error)
	DeleteRoute(context.Context, DeleteRouteRequest) error
}

type DeliveryAPI interface {
	Deliver(context.Context, DeliveryRequest) (DeliveryResponse, error)
}

type RouteTarget struct {
	Kind   string `json:"kind"`
	FileID string `json:"file_id"`
}

func (target RouteTarget) Validate() error {
	if target.Kind != TargetCoreFile && target.Kind != TargetStaticSiteArtifact {
		return ErrInvalidTarget
	}
	id, err := uuid.Parse(target.FileID)
	if err != nil || id == uuid.Nil || strings.ContainsAny(target.FileID, "/\\?#") {
		return ErrInvalidTarget
	}
	return nil
}

type Route struct {
	ID        string      `json:"id"`
	BranchID  string      `json:"branch_id"`
	Host      string      `json:"host"`
	Path      string      `json:"path"`
	Match     string      `json:"match"`
	Target    RouteTarget `json:"target"`
	State     string      `json:"state"`
	Revision  int64       `json:"revision"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

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
	BranchID      string      `json:"branch_id"`
	Host          string      `json:"host"`
	Path          string      `json:"path"`
	Match         string      `json:"match"`
	Target        RouteTarget `json:"target"`
	IdempotencyKey string     `json:"-"`
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
	BranchID   string
	Host       string
	Method     string
	EscapedPath string
	Query      string
	IfNoneMatch string
	IfModifiedSince string
	Range      string
	IfRange    string
}

type DeliveryResponse struct {
	StatusCode        int
	ContentType       string
	ContentLength     int64
	ETag              string
	LastModified      string
	CacheControl      string
	ContentDisposition string
	AcceptRanges      string
	Body              io.ReadCloser
}

type Descriptor struct{}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "route.list", Action: access.Read, ResourceType: "router-route"},
		{Name: "route.read", Action: access.Read, ResourceType: "router-route"},
		{Name: "route.create", Action: access.Write, ResourceType: "router-route"},
		{Name: "route.target.replace", Action: access.Write, ResourceType: "router-route"},
		{Name: "route.delete", Action: access.Delete, ResourceType: "router-route"},
		{Name: "route.deliver", Action: access.Read, ResourceType: "configured-route"},
	}
}

var _ platform.Module = Descriptor{}
