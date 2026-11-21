package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrContext          = errors.New("invalid feature context")
	ErrUnsupportedToken = errors.New("unsupported design token")
)

// TrustedProjection is the host-provided, page-scoped input for a placement.
// It contains only published, public data; callers must never pass credentials,
// private Core file data or visitor-controlled module identifiers.
type TrustedProjection struct {
	PlacementID string
	FeatureKey  string
	Version     string
	Arguments   map[string]any
	Content     map[string]any
	RoutePath   string
	RouteParams map[string]string
	RouteSearch string
	SystemLogo  string
	Collection  string
	DetailLink  *DetailLink
	SDK         []string
	Locale      string
	TokenValues map[string]string
}

// DetailLink is a typed projection of a detail-route binding. The feature never
// constructs arbitrary routes or receives an engine/module namespace.
type DetailLink struct {
	RouteID      string `json:"routeId"`
	Field        string `json:"field"`
	PathTemplate string `json:"pathTemplate"`
}

// FeatureContext is the complete public context passed to every lifecycle call.
// It intentionally has no free-form extension field: unknown data cannot leak
// through it.
type FeatureContext struct {
	PlacementID  string            `json:"placementId"`
	FeatureKey   string            `json:"featureKey"`
	Version      string            `json:"version"`
	Arguments    map[string]any    `json:"arguments"`
	Content      map[string]any    `json:"content"`
	Route        Route             `json:"route"`
	SystemAssets SystemAssets      `json:"systemAssets"`
	Collection   string            `json:"collectionEndpoint,omitempty"`
	DetailLink   *DetailLink       `json:"detailLink,omitempty"`
	SDK          []string          `json:"sdk"`
	Signal       Cancellation      `json:"signal"`
	Locale       string            `json:"locale"`
	TokenValues  map[string]string `json:"tokens"`
}

// Route is the request projection supplied by Router, never by the feature.
type Route struct {
	Path   string            `json:"path"`
	Params map[string]string `json:"params"`
	Search string            `json:"search"`
}

type SystemAssets struct {
	LogoURL string `json:"logoUrl,omitempty"`
}

// Cancellation is a serializable handle for the host-side AbortController.
// A feature never receives a Go context or a raw callback.
type Cancellation struct {
	ID      string `json:"id"`
	Aborted bool   `json:"aborted"`
}

// BuildContext maps a trusted projection into a serializable public context.
// It fails closed on missing identity, secret-like keys and unsupported tokens.
func BuildContext(projection TrustedProjection, signalID string) (FeatureContext, error) {
	if strings.TrimSpace(projection.PlacementID) == "" || strings.TrimSpace(projection.FeatureKey) == "" ||
		strings.TrimSpace(signalID) == "" {
		return FeatureContext{}, fmt.Errorf("%w: placement, feature and signal identity are required", ErrContext)
	}
	if projection.Version != "" && !semverPattern.MatchString(projection.Version) {
		return FeatureContext{}, fmt.Errorf("%w: version", ErrContext)
	}
	if err := rejectSecretKeys(projection.Arguments); err != nil {
		return FeatureContext{}, err
	}
	if err := rejectSecretKeys(projection.Content); err != nil {
		return FeatureContext{}, err
	}
	for key := range projection.RouteParams {
		if secretKeyPart.MatchString(key) {
			return FeatureContext{}, fmt.Errorf("%w: route parameter %q", ErrSecret, key)
		}
	}
	for _, token := range projection.TokenValues {
		if !safeTokenValue(token) {
			return FeatureContext{}, fmt.Errorf("%w: token value", ErrUnsupportedToken)
		}
	}
	context := FeatureContext{
		PlacementID:  projection.PlacementID,
		FeatureKey:   projection.FeatureKey,
		Version:      projection.Version,
		Arguments:    cloneMap(projection.Arguments),
		Content:      cloneMap(projection.Content),
		Route:        Route{Path: projection.RoutePath, Params: cloneStringMap(projection.RouteParams), Search: projection.RouteSearch},
		SystemAssets: SystemAssets{LogoURL: projection.SystemLogo},
		Collection:   projection.Collection,
		DetailLink:   projection.DetailLink,
		SDK:          append([]string(nil), projection.SDK...),
		Signal:       Cancellation{ID: signalID},
		Locale:       projection.Locale,
		TokenValues:  cloneStringMap(projection.TokenValues),
	}
	return context, nil
}

// MarshalContext serializes the public context with deterministic key order.
// json.Marshal sorts map keys; no private field is reachable from this value.
func MarshalContext(context FeatureContext) ([]byte, error) {
	data, err := json.Marshal(context)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrContext, err)
	}
	return data, nil
}

// safeTokenValue rejects CSS injection and executable payloads. Feature-owned
// token values are graphical settings, never raw stylesheet expressions.
func safeTokenValue(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	lowered := strings.ToLower(value)
	for _, forbidden := range []string{"url(", "javascript:", "expression(", "@import", "<", ">", `"`, "'", ";", "{", "}", "\\", "\n", "\r"} {
		if strings.Contains(lowered, forbidden) {
			return false
		}
	}
	return true
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
