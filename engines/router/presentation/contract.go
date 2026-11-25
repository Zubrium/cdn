// Package presentation defines Router's aggregate typed facade. Settings and
// public delivery are separate capabilities; neither depends on Website APIs.
// The concrete contracts live in pkg/contracts; this package keeps the
// engine-local facade names as aliases and thin wrappers.
package presentation

import "github.com/Zubrium/cdn/pkg/contracts"

const (
	TargetCoreFile                = contracts.TargetCoreFile
	TargetStaticSiteArtifact      = contracts.TargetStaticSiteArtifact
	TargetRegisteredOperation     = contracts.TargetRegisteredOperation
	RouteStateActive              = contracts.RouteStateActive
	maxPublicOperationConfigBytes = 64 << 10
)

var ErrInvalidTarget = contracts.ErrInvalidTarget
var (
	ErrUnauthorized                 = contracts.ErrPublicUnauthorized
	ErrForbidden                    = contracts.ErrPublicForbidden
	ErrNotFound                     = contracts.ErrPublicNotFound
	ErrConflict                     = contracts.ErrPublicConflict
	ErrInvalid                      = contracts.ErrPublicInvalid
	ErrUnavailable                  = contracts.ErrPublicUnavailable
	ErrTooLarge                     = contracts.ErrPublicTooLarge
	ErrInvalidPublicOperation       = contracts.ErrPublicInvalidOperation
	ErrPublicOperationNotRegistered = contracts.ErrPublicOperationNotRegistered
)

type Contract = contracts.Contract
type SettingsAPI = contracts.SettingsAPI
type DeliveryAPI = contracts.DeliveryAPI

type RouteTarget = contracts.RouteTarget

func ValidPublicOperationID(value string) bool {
	return contracts.ValidPublicOperationID(value)
}

// CanonicalLocale is the facade form of the route locale validator. Outer
// Presentation uses it for preference cookies without importing engine
// internals.
func CanonicalLocale(value string) (string, error) {
	return contracts.CanonicalLocale(value)
}

// NormalizePath is the facade form of the route path validator. It keeps the
// preference return path bound to the same canonical form used by matching.
func NormalizePath(value string) (string, error) {
	return contracts.NormalizePath(value)
}

// PathMatches is the facade form of route/request path matching. Outer
// Presentation validates server-configured warm-up paths without importing
// engine internals.
func PathMatches(routePath, requestPath, match string) bool {
	return contracts.PathMatches(routePath, requestPath, match)
}

// ValidateRoute is the facade form of persisted-route validation. Warm-up
// rechecks the authoritative route before priming a cache entry.
func ValidateRoute(route Route) error {
	return contracts.ValidateRoute(route)
}

type Route = contracts.Route
type ListRoutesRequest = contracts.ListRoutesRequest
type RoutePage = contracts.RoutePage
type CreateRouteRequest = contracts.CreateRouteRequest
type ReplaceRouteTargetRequest = contracts.ReplaceRouteTargetRequest
type ReplaceLocaleTargetRequest = contracts.ReplaceLocaleTargetRequest
type DeleteRouteRequest = contracts.DeleteRouteRequest
type DeliveryRequest = contracts.DeliveryRequest

// Public Router catalog contracts are owned by pkg/contracts. These aliases
// preserve the facade names while keeping one neutral definition for both
// sides of the boundary.
type PublicOperationDescriptor = contracts.PublicOperationDescriptor
type PublicOperationRequest = contracts.PublicOperationRequest
type PublicOperation = contracts.PublicOperation
type DeliveryResponse = contracts.DeliveryResponse
type FormSessionCookieChange = contracts.FormSessionCookieChange

type PublicOperationCatalog = contracts.PublicOperationCatalog

type Descriptor = contracts.RouterDescriptor
