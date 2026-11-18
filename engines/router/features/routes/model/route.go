// Package model defines Router's persisted route records.
package model

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

const (
	TargetCoreFile             = "core-file"
	TargetStaticSiteArtifact   = "static-site-artifact"
	TargetRegisteredOperation  = "registered-operation"
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
	hostLabel        = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	operationID      = regexp.MustCompile(`^public\.[A-Za-z0-9._-]{1,121}$`)
	encodedSeparator = regexp.MustCompile(`(?i)%2f|%5c|%25`)
)

type Target struct {
	Kind            string          `json:"kind"`
	FileID          string          `json:"file_id,omitempty"`
	OperationID     string          `json:"operation_id,omitempty"`
	OperationConfig json.RawMessage `json:"operation_config,omitempty"`
	FallbackEnabled bool            `json:"fallback_enabled,omitempty"`
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

func ValidPublicOperationID(value string) bool { return operationID.MatchString(value) }

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
		if !hostLabel.MatchString(label) {
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
		strings.ContainsAny(value, "?#\\\x00\r\n") || encodedSeparator.MatchString(value) {
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
		if err != nil || id == uuid.Nil || id.String() != target.FileID || target.OperationID != "" || len(target.OperationConfig) != 0 || target.FallbackEnabled {
			return ErrInvalidRoute
		}
	case TargetStaticSiteArtifact:
		id, err := uuid.Parse(target.FileID)
		if err != nil || id == uuid.Nil || id.String() != target.FileID || target.OperationID != "" || len(target.OperationConfig) != 0 {
			return ErrInvalidRoute
		}
	case TargetRegisteredOperation:
		if !operationID.MatchString(target.OperationID) || target.FileID != "" || len(target.OperationConfig) == 0 ||
			target.FallbackEnabled || len(target.OperationConfig) > MaxOperationConfigBytes || !json.Valid(target.OperationConfig) {
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

func NewRoute(id, branchID, host, routePath, match string, target Target, now time.Time) (Route, error) {
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
	if err := route.Target.Validate(); err != nil || (route.Target.Kind == TargetCoreFile && route.Match != MatchExact) {
		return ErrInvalidRoute
	}
	return nil
}
