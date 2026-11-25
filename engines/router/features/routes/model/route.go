// Package model defines Router's persisted route records. Concrete primitives
// live in pkg/contracts; this package preserves the engine-internal names as
// aliases and thin wrappers.
package model

import (
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	TargetCoreFile             = contracts.TargetCoreFile
	TargetStaticSiteArtifact   = contracts.TargetStaticSiteArtifact
	TargetRegisteredOperation  = contracts.TargetRegisteredOperation
	DispositionInline          = contracts.DispositionInline
	DispositionAttachment      = contracts.DispositionAttachment
	MatchExact                 = contracts.MatchExact
	MatchPrefix                = contracts.MatchPrefix
	StateActive                = contracts.StateActive
	MaxRoutePathBytes          = contracts.MaxRoutePathBytes
	MaxPathSegments            = contracts.MaxPathSegments
	MaxRoutePathSelectorValues = contracts.MaxRoutePathSelectorValues
	MaxOperationConfigBytes    = contracts.MaxOperationConfigBytes
)

var ErrInvalidRoute = contracts.ErrInvalidRoute

type LocaleArtifactTarget = contracts.LocaleArtifactTarget
type LocaleSlot = contracts.LocaleSlot
type Target = contracts.Target
type Route = contracts.Route

func ValidPublicOperationID(value string) bool { return contracts.ValidPublicOperationID(value) }

func CanonicalLocale(value string) (string, error) { return contracts.CanonicalLocale(value) }

func NormalizeHost(value string) (string, error) { return contracts.NormalizeHost(value) }

func NormalizeRequestHost(value string) (string, error) { return contracts.NormalizeRequestHost(value) }

func NormalizePath(value string) (string, error) { return contracts.NormalizePath(value) }

func PathPrefixes(value string) ([]string, error) { return contracts.PathPrefixes(value) }

func PathPrefixesDecoded(path string) []string { return contracts.PathPrefixesDecoded(path) }

func PathMatches(routePath, requestPath, match string) bool {
	return contracts.PathMatches(routePath, requestPath, match)
}

func NewStaticArtifactTarget(defaultLocale string, items []LocaleArtifactTarget) (Target, error) {
	return contracts.NewStaticArtifactTarget(defaultLocale, items)
}

func NewRoute(id, branchID, host, routePath, match string, target Target, now time.Time) (Route, error) {
	return contracts.NewRoute(id, branchID, host, routePath, match, target, now)
}
