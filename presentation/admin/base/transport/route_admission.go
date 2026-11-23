package transport

import (
	"errors"
	"net/http"
	"strings"
)

// ErrInvalidRouteAdmission rejects a per-route table that cannot be applied
// before the HTTP listener starts. Route keys come only from startup
// configuration; visitors never supply a route ID.
var ErrInvalidRouteAdmission = errors.New("invalid per-route HTTP admission configuration")

// RouteAdmissionOverride binds a static route ID from startup configuration to
// an exact request path. The path is not a route ID supplied by the visitor.
type RouteAdmissionOverride struct {
	ID          string
	Path        string
	MaxInFlight int
}

// PerRouteAdmission is the explicit transport API used by composition to pass
// router.concurrency.http.per_route. Defaults are never inferred at runtime.
type PerRouteAdmission struct {
	Enabled            bool
	DefaultMaxInFlight int
	Overrides          []RouteAdmissionOverride
}

type routeAdmissionTable struct {
	defaultSlots chan struct{}
	byPath       map[string]chan struct{}
}

func buildRouteAdmission(setting PerRouteAdmission, globalMaxInFlight int) (*routeAdmissionTable, error) {
	if globalMaxInFlight <= 0 {
		return nil, ErrInvalidRouteAdmission
	}
	if setting.DefaultMaxInFlight < 0 || setting.DefaultMaxInFlight > globalMaxInFlight {
		return nil, ErrInvalidRouteAdmission
	}
	seenIDs := make(map[string]struct{}, len(setting.Overrides))
	seenPaths := make(map[string]struct{}, len(setting.Overrides))
	for _, override := range setting.Overrides {
		if !validRouteKey(override.ID) || !validRoutePath(override.Path) ||
			override.MaxInFlight <= 0 || override.MaxInFlight > globalMaxInFlight {
			return nil, ErrInvalidRouteAdmission
		}
		if _, duplicate := seenIDs[override.ID]; duplicate {
			return nil, ErrInvalidRouteAdmission
		}
		if _, duplicate := seenPaths[override.Path]; duplicate {
			return nil, ErrInvalidRouteAdmission
		}
		seenIDs[override.ID] = struct{}{}
		seenPaths[override.Path] = struct{}{}
	}
	if !setting.Enabled {
		return nil, nil
	}
	if setting.DefaultMaxInFlight <= 0 {
		return nil, ErrInvalidRouteAdmission
	}
	table := &routeAdmissionTable{
		defaultSlots: make(chan struct{}, setting.DefaultMaxInFlight),
		byPath:       make(map[string]chan struct{}, len(setting.Overrides)),
	}
	for _, override := range setting.Overrides {
		table.byPath[override.Path] = make(chan struct{}, override.MaxInFlight)
	}
	return table, nil
}

func validRouteKey(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}

func validRoutePath(path string) bool {
	if len(path) < 1 || len(path) > 512 || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// slotsFor maps the request to a limit using only the exact path and the
// startup table. Query parameters, headers and any route ID from the visitor
// are never consulted, and the route ID never becomes a metric label.
func (table *routeAdmissionTable) slotsFor(request *http.Request) chan struct{} {
	if table == nil || request == nil {
		return nil
	}
	if slots, configured := table.byPath[request.URL.Path]; configured {
		return slots
	}
	return table.defaultSlots
}
