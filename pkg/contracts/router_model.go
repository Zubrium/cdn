package contracts

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Router route primitives are owned by the neutral contract package so both
// Router Presentation and the Router engine compile against one definition.
// Engine packages keep aliases for their internal Go API.
const (
	TargetCoreFile             = "core-file"
	TargetStaticSiteArtifact   = "static-site-artifact"
	TargetRegisteredOperation  = "registered-operation"
	DispositionInline          = "inline"
	DispositionAttachment      = "attachment"
	MatchExact                 = "exact"
	MatchPrefix                = "prefix"
	StateActive                = "active"
	MaxRoutePathBytes          = 2048
	MaxPathSegments            = 31
	MaxRoutePathSelectorValues = MaxPathSegments*2 + 1
	MaxOperationConfigBytes    = 64 << 10
)

var (
	ErrInvalidRoute  = errors.New("invalid Router route")
	ErrInvalidTarget = ErrInvalidRoute
)

var (
	routerHostLabel        = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	routerOperationID      = regexp.MustCompile(`^public\.[A-Za-z0-9._-]{1,121}$`)
	routerLocaleCharacters = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	routerEncodedSeparator = regexp.MustCompile(`(?i)%2f|%5c|%25`)
)

// LocaleArtifactTarget is one independently versioned locale slot of an
// artifact route. Its CAS revision is owned by Router and is never taken from
// a visitor request or from Website.
type LocaleArtifactTarget struct {
	Locale                 string `json:"locale"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
	Revision               int64  `json:"-"`
}

// LocaleSlot is the public-facing Router view of a configured artifact locale.
// Router CAS revisions are intentionally not exposed to callers or visitors.
type LocaleSlot struct {
	Locale                 string `json:"locale"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
}

type Target struct {
	Kind            string                 `json:"kind"`
	FileID          string                 `json:"file_id,omitempty"`
	Disposition     string                 `json:"disposition,omitempty"`
	DefaultLocale   string                 `json:"default_locale,omitempty"`
	LocaleTargets   []LocaleArtifactTarget `json:"locale_targets,omitempty"`
	OperationID     string                 `json:"operation_id,omitempty"`
	OperationConfig json.RawMessage        `json:"operation_config,omitempty"`
	FallbackEnabled bool                   `json:"fallback_enabled,omitempty"`
}

type Route struct {
	ID        string    `json:"id"`
	BranchID  string    `json:"branch_id"`
	Host      string    `json:"host"`
	Path      string    `json:"path"`
	Match     string    `json:"match"`
	Target    Target    `json:"target"`
	State     string    `json:"state"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ValidPublicOperationID(value string) bool { return routerOperationID.MatchString(value) }

// CanonicalLocale validates and canonicalizes a configured BCP 47-like tag.
// Router accepts only supported, server-configured locales; no visitor value
// reaches this function through a route target selection path.
func CanonicalLocale(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 64 || !routerLocaleCharacters.MatchString(value) ||
		strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || strings.Contains(value, "--") {
		return "", ErrInvalidRoute
	}
	parts := strings.Split(value, "-")
	if len(parts) > 4 || len(parts[0]) < 2 || len(parts[0]) > 3 {
		return "", ErrInvalidRoute
	}
	for index, part := range parts {
		if len(part) < 2 || len(part) > 8 {
			return "", ErrInvalidRoute
		}
		if index == 0 {
			parts[index] = strings.ToLower(part)
			continue
		}
		if index == 1 && len(part) == 2 {
			parts[index] = strings.ToUpper(part)
			continue
		}
		if index == 1 && len(part) == 4 {
			parts[index] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
			continue
		}
		parts[index] = strings.ToLower(part)
	}
	canonical := strings.Join(parts, "-")
	if canonical == "und" || len(canonical) > 64 {
		return "", ErrInvalidRoute
	}
	return canonical, nil
}

// LocaleTarget returns the immutable catalog entry for one configured locale.
func (target Target) LocaleTarget(locale string) (LocaleArtifactTarget, bool) {
	for _, item := range target.LocaleTargets {
		if item.Locale == locale {
			return item, true
		}
	}
	return LocaleArtifactTarget{}, false
}

// LocaleSlots exposes the target catalog without Router-internal CAS revisions.
func (target Target) LocaleSlots() []LocaleSlot {
	slots := make([]LocaleSlot, 0, len(target.LocaleTargets))
	for _, item := range target.LocaleTargets {
		slots = append(slots, LocaleSlot{Locale: item.Locale, ArtifactManifestFileID: item.ArtifactManifestFileID})
	}
	return slots
}

// DefaultArtifactFileID returns the artifact bound to the configured default
// locale. Delivery selects only from this server-owned catalog.
func (target Target) DefaultArtifactFileID() (string, error) {
	item, found := target.LocaleTarget(target.DefaultLocale)
	if !found {
		return "", ErrInvalidRoute
	}
	return item.ArtifactManifestFileID, nil
}

// NormalizeHost returns the canonical ASCII hostname used for route keys and
// matching. Ports, wildcard names, userinfo, and non-DNS syntax are rejected.
func NormalizeHost(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 253 || strings.ContainsAny(value, "/\\:@?#\r\n\t ") {
		return "", ErrInvalidRoute
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if value == "" || len(value) > 253 {
		return "", ErrInvalidRoute
	}
	for _, label := range strings.Split(value, ".") {
		if !routerHostLabel.MatchString(label) {
			return "", ErrInvalidRoute
		}
	}
	return value, nil
}

// NormalizeRequestHost strips a valid optional port from an HTTP Host value
// before applying the configured route hostname rules.
func NormalizeRequestHost(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", ErrInvalidRoute
	}
	if strings.HasPrefix(value, "[") {
		return "", ErrInvalidRoute
	}
	if strings.Count(value, ":") == 1 {
		host, port, found := strings.Cut(value, ":")
		if !found || host == "" || port == "" || len(port) > 5 {
			return "", ErrInvalidRoute
		}
		for _, digit := range port {
			if digit < '0' || digit > '9' {
				return "", ErrInvalidRoute
			}
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", ErrInvalidRoute
		}
		value = host
	} else if strings.Contains(value, ":") {
		return "", ErrInvalidRoute
	}
	return NormalizeHost(value)
}

// NormalizePath rejects ambiguous encodings and dot segments, then stores the
// canonical escaped path used by both configuration and incoming requests.
func NormalizePath(value string) (string, error) {
	if value == "" || len(value) > MaxRoutePathBytes || value[0] != '/' || value != strings.TrimSpace(value) ||
		strings.ContainsAny(value, "?#\\\x00\r\n") || routerEncodedSeparator.MatchString(value) {
		return "", ErrInvalidRoute
	}
	decoded, err := url.PathUnescape(value)
	if err != nil || !utf8.ValidString(decoded) || strings.ContainsRune(decoded, '\x00') || strings.ContainsRune(decoded, '\\') {
		return "", ErrInvalidRoute
	}
	segments := strings.Split(decoded, "/")
	for index, segment := range segments {
		if segment == "" && index != 0 && index != len(segments)-1 {
			return "", ErrInvalidRoute
		}
		if segment == "." || segment == ".." {
			return "", ErrInvalidRoute
		}
	}
	segmentCount := 0
	if decoded != "/" {
		segmentCount = strings.Count(decoded, "/")
		if strings.HasSuffix(decoded, "/") {
			segmentCount--
		}
	}
	if segmentCount > MaxPathSegments {
		return "", ErrInvalidRoute
	}
	canonical := (&url.URL{Path: decoded}).EscapedPath()
	if canonical == "" || len(canonical) > MaxRoutePathBytes {
		return "", ErrInvalidRoute
	}
	return canonical, nil
}

// PathPrefixes returns the request path and its ancestor paths at slash
// boundaries. Prefix routes therefore match complete path segments.
func PathPrefixes(value string) ([]string, error) {
	normalized, err := NormalizePath(value)
	if err != nil {
		return nil, err
	}
	decoded, err := url.PathUnescape(normalized)
	if err != nil {
		return nil, ErrInvalidRoute
	}
	return PathPrefixesDecoded(decoded), nil
}

func PathPrefixesDecoded(path string) []string {
	prefixes := []string{"/"}
	for index := 1; index < len(path); index++ {
		if path[index] == '/' {
			prefixes = append(prefixes, path[:index])
			if index < len(path)-1 {
				prefixes = append(prefixes, path[:index+1])
			}
		}
	}
	if path != "/" {
		prefixes = append(prefixes, path)
	}
	return prefixes
}

func PathMatches(routePath, requestPath, match string) bool {
	if match == MatchExact {
		return routePath == requestPath
	}
	if match != MatchPrefix || !strings.HasPrefix(requestPath, routePath) {
		return false
	}
	return routePath == "/" || routePath == requestPath || strings.HasSuffix(routePath, "/") || requestPath[len(routePath)] == '/'
}

func (target Target) Validate() error {
	switch target.Kind {
	case TargetCoreFile:
		id, err := uuid.Parse(target.FileID)
		if err != nil || id == uuid.Nil || id.String() != target.FileID || target.OperationID != "" || len(target.OperationConfig) != 0 ||
			target.FallbackEnabled || target.DefaultLocale != "" || len(target.LocaleTargets) != 0 ||
			(target.Disposition != "" && target.Disposition != DispositionInline && target.Disposition != DispositionAttachment) {
			return ErrInvalidRoute
		}
	case TargetStaticSiteArtifact:
		defaultLocale, err := CanonicalLocale(target.DefaultLocale)
		if err != nil || target.FileID != "" || target.Disposition != "" || target.OperationID != "" || len(target.OperationConfig) != 0 || target.FallbackEnabled ||
			len(target.LocaleTargets) < 1 || len(target.LocaleTargets) > 256 {
			return ErrInvalidRoute
		}
		seen := make(map[string]struct{}, len(target.LocaleTargets))
		defaultFound := false
		for _, item := range target.LocaleTargets {
			locale, err := CanonicalLocale(item.Locale)
			if err != nil || locale != item.Locale || item.Revision < 0 {
				return ErrInvalidRoute
			}
			if _, duplicate := seen[locale]; duplicate {
				return ErrInvalidRoute
			}
			id, err := uuid.Parse(item.ArtifactManifestFileID)
			if err != nil || id == uuid.Nil || id.String() != item.ArtifactManifestFileID {
				return ErrInvalidRoute
			}
			seen[locale] = struct{}{}
			if locale == defaultLocale {
				defaultFound = true
			}
		}
		if !defaultFound {
			return ErrInvalidRoute
		}
	case TargetRegisteredOperation:
		if !routerOperationID.MatchString(target.OperationID) || target.FileID != "" || target.Disposition != "" || len(target.OperationConfig) == 0 ||
			target.FallbackEnabled || len(target.OperationConfig) > MaxOperationConfigBytes || !json.Valid(target.OperationConfig) ||
			target.DefaultLocale != "" || len(target.LocaleTargets) != 0 {
			return ErrInvalidRoute
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(target.OperationConfig, &fields) != nil || fields == nil {
			return ErrInvalidRoute
		}
	default:
		return ErrInvalidRoute
	}
	return nil
}

// NewStaticArtifactTarget builds a strict locale catalog for an artifact
// route. Catalog order and the default locale are server-owned inputs.
func NewStaticArtifactTarget(defaultLocale string, items []LocaleArtifactTarget) (Target, error) {
	canonical, err := CanonicalLocale(defaultLocale)
	if err != nil {
		return Target{}, err
	}
	target := Target{Kind: TargetStaticSiteArtifact, DefaultLocale: canonical, LocaleTargets: append([]LocaleArtifactTarget(nil), items...)}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

func NewRoute(id, branchID, host, routePath, match string, target Target, now time.Time) (Route, error) {
	if target.Kind == TargetStaticSiteArtifact {
		target.LocaleTargets = append([]LocaleArtifactTarget(nil), target.LocaleTargets...)
		for index := range target.LocaleTargets {
			if target.LocaleTargets[index].Revision == 0 {
				target.LocaleTargets[index].Revision = 1
			}
		}
	}
	parsedID, err := uuid.Parse(id)
	if err != nil || parsedID == uuid.Nil || branchID == "" || len(branchID) > 128 || strings.TrimSpace(branchID) != branchID ||
		strings.ContainsAny(branchID, "\r\n\x00/\\") {
		return Route{}, ErrInvalidRoute
	}
	host, err = NormalizeHost(host)
	if err != nil {
		return Route{}, err
	}
	routePath, err = NormalizePath(routePath)
	if err != nil {
		return Route{}, err
	}
	if match != MatchExact && match != MatchPrefix {
		return Route{}, ErrInvalidRoute
	}
	if err := target.Validate(); err != nil || (target.Kind == TargetCoreFile && match != MatchExact) {
		return Route{}, ErrInvalidRoute
	}
	if now.IsZero() {
		return Route{}, ErrInvalidRoute
	}
	now = now.UTC()
	route := Route{ID: parsedID.String(), BranchID: branchID, Host: host, Path: routePath, Match: match,
		Target: target, State: StateActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := route.ValidateForStorage(); err != nil {
		return Route{}, err
	}
	return route, nil
}

func (route Route) ValidateForStorage() error {
	id, err := uuid.Parse(route.ID)
	if err != nil || id == uuid.Nil || id.String() != route.ID || route.BranchID == "" || len(route.BranchID) > 128 ||
		strings.TrimSpace(route.BranchID) != route.BranchID || strings.ContainsAny(route.BranchID, "\r\n\x00/\\") {
		return ErrInvalidRoute
	}
	for _, char := range route.BranchID {
		if char < 0x21 || char == 0x7f {
			return ErrInvalidRoute
		}
	}
	host, err := NormalizeHost(route.Host)
	if err != nil || host != route.Host {
		return ErrInvalidRoute
	}
	path, err := NormalizePath(route.Path)
	if err != nil || path != route.Path || (route.Match != MatchExact && route.Match != MatchPrefix) ||
		route.State != StateActive || route.Revision < 1 || route.CreatedAt.IsZero() || route.UpdatedAt.Before(route.CreatedAt) {
		return ErrInvalidRoute
	}
	if err := route.Target.Validate(); err != nil ||
		(route.Target.Kind == TargetCoreFile && route.Match != MatchExact) ||
		(route.Target.Kind == TargetRegisteredOperation && route.Match != MatchExact) {
		return ErrInvalidRoute
	}
	if route.Target.Kind == TargetStaticSiteArtifact {
		for _, item := range route.Target.LocaleTargets {
			if item.Revision < 1 {
				return ErrInvalidRoute
			}
		}
	}
	return nil
}
