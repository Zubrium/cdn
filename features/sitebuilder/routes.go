package sitebuilder

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/google/uuid"

	"websiteengine/pkg/platform/validate"
)

const routeMatchTimeout = 25 * time.Millisecond

type Route struct {
	ID              string `json:"id"`
	Pattern         string `json:"pattern"`
	PageID          string `json:"pageId,omitempty"`
	FileID          string `json:"fileId,omitempty"`
	FileDisposition string `json:"fileDisposition,omitempty"`
}

type CompiledRoute struct {
	Route
	compiled *regexp2.Regexp
	captures []string
}

// CompileRoute validates a PCRE/.NET-compatible route expression. Matching is
// anchored to the complete URL path, while named capture groups become args.
func CompileRoute(route Route) (CompiledRoute, error) {
	if strings.TrimSpace(route.ID) == "" {
		return CompiledRoute{}, errors.New("route id is required")
	}
	if (route.PageID == "") == (route.FileID == "") {
		return CompiledRoute{}, errors.New("route must reference exactly one page or file")
	}
	if route.FileDisposition != "" && route.FileDisposition != "inline" && route.FileDisposition != "attachment" {
		return CompiledRoute{}, errors.New("file disposition must be inline or attachment")
	}
	if route.FileDisposition != "" && route.FileID == "" {
		return CompiledRoute{}, errors.New("file disposition requires a file route")
	}
	if route.FileID != "" {
		if _, err := uuid.Parse(route.FileID); err != nil {
			return CompiledRoute{}, errors.New("file route id must be a UUID")
		}
	}
	if len(route.Pattern) == 0 || len(route.Pattern) > 500 {
		return CompiledRoute{}, errors.New("route pattern must contain between 1 and 500 characters")
	}
	if route.Pattern[0] != '^' {
		return CompiledRoute{}, errors.New("route pattern must start with ^")
	}
	re, err := regexp2.Compile(`\A(?:`+route.Pattern+`)\z`, regexp2.None)
	if err != nil {
		return CompiledRoute{}, fmt.Errorf("invalid route pattern: %w", err)
	}
	re.MatchTimeout = routeMatchTimeout
	var captures []string
	for _, name := range re.GetGroupNames() {
		if _, err := strconv.Atoi(name); err == nil {
			continue
		}
		if !argumentPattern.MatchString(name) {
			return CompiledRoute{}, fmt.Errorf("route capture name %q is invalid", name)
		}
		captures = append(captures, name)
	}
	if route.FileID != "" && len(captures) > 0 {
		return CompiledRoute{}, errors.New("file route must be static and cannot expose path captures")
	}
	return CompiledRoute{Route: route, compiled: re, captures: captures}, nil
}

func (r CompiledRoute) CaptureNames() []string { return append([]string(nil), r.captures...) }

func CompileRoutes(routes []Route) ([]CompiledRoute, error) {
	if len(routes) == 0 {
		return nil, errors.New("at least one route is required")
	}
	seenIDs := make(map[string]bool, len(routes))
	compiled := make([]CompiledRoute, 0, len(routes))
	for _, route := range routes {
		if seenIDs[route.ID] {
			return nil, fmt.Errorf("duplicate route id %q", route.ID)
		}
		seenIDs[route.ID] = true
		item, err := CompileRoute(route)
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", route.ID, err)
		}
		compiled = append(compiled, item)
	}
	return compiled, nil
}

func MatchRoute(routes []CompiledRoute, requestPath string) (Route, map[string]string, bool, error) {
	if len(requestPath) > 4096 {
		return Route{}, nil, false, errors.New("request path exceeds 4096 characters")
	}
	for _, route := range routes {
		match, err := route.compiled.FindStringMatch(requestPath)
		if err != nil {
			return Route{}, nil, false, fmt.Errorf("route %q match failed: %w", route.ID, err)
		}
		if match == nil {
			continue
		}
		args := make(map[string]string)
		for _, group := range match.Groups() {
			name := group.Name
			if name == "0" || name == "" || !argumentPattern.MatchString(name) || len(group.Captures) == 0 {
				continue
			}
			args[name] = group.Captures[len(group.Captures)-1].String()
		}
		return route.Route, args, true, nil
	}
	return Route{}, nil, false, nil
}

type ArgumentBinding struct {
	Source   string          `json:"source"` // literal, path or query
	Value    json.RawMessage `json:"value,omitempty"`
	PathArg  string          `json:"pathArg,omitempty"`
	QueryKey string          `json:"queryKey,omitempty"`
}

// ResolveArguments applies per-placement literal/route bindings and validates
// the resulting props against the feature's manifest schema.
func ResolveArguments(schema json.RawMessage, bindings map[string]ArgumentBinding, pathArgs map[string]string, query url.Values) (json.RawMessage, error) {
	var spec struct {
		Properties map[string]struct {
			Type    string          `json:"type"`
			Default json.RawMessage `json:"default"`
			Items   struct {
				Type string `json:"type"`
			} `json:"items"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &spec); err != nil {
		return nil, fmt.Errorf("decode arguments schema: %w", err)
	}
	resolved := make(map[string]json.RawMessage, len(spec.Properties))
	for name, binding := range bindings {
		property, ok := spec.Properties[name]
		if !ok {
			return nil, fmt.Errorf("unknown feature argument %q", name)
		}
		var raw json.RawMessage
		switch binding.Source {
		case "literal":
			if len(binding.Value) == 0 || binding.PathArg != "" || binding.QueryKey != "" {
				return nil, fmt.Errorf("argument %q must have exactly one literal value", name)
			}
			raw = binding.Value
		case "path":
			if binding.PathArg == "" || len(binding.Value) != 0 || binding.QueryKey != "" {
				return nil, fmt.Errorf("argument %q must have exactly one path parameter", name)
			}
			value, ok := pathArgs[binding.PathArg]
			if !ok {
				return nil, fmt.Errorf("path parameter %q is unavailable", binding.PathArg)
			}
			var err error
			raw, err = coerceRouteValue(property.Type, value)
			if err != nil {
				return nil, fmt.Errorf("path parameter %q cannot populate %s: %w", binding.PathArg, name, err)
			}
		case "query":
			if strings.TrimSpace(binding.QueryKey) == "" || len(binding.Value) != 0 || binding.PathArg != "" {
				return nil, fmt.Errorf("argument %q must have exactly one query key", name)
			}
			values, ok := query[binding.QueryKey]
			if !ok || len(values) == 0 {
				continue
			}
			if property.Type != "array" && len(values) > 1 {
				return nil, fmt.Errorf("query key %q was repeated for non-array argument %q", binding.QueryKey, name)
			}
			if property.Type == "array" {
				items := make([]json.RawMessage, 0, len(values))
				for _, value := range values {
					item, err := coerceRouteValue(property.Items.Type, value)
					if err != nil {
						return nil, fmt.Errorf("query key %q cannot populate %s: %w", binding.QueryKey, name, err)
					}
					items = append(items, item)
				}
				encoded, err := json.Marshal(items)
				if err != nil {
					return nil, err
				}
				raw = encoded
			} else {
				var err error
				raw, err = coerceRouteValue(property.Type, values[0])
				if err != nil {
					return nil, fmt.Errorf("query key %q cannot populate %s: %w", binding.QueryKey, name, err)
				}
			}
		default:
			return nil, fmt.Errorf("argument %q source must be literal, path or query", name)
		}
		resolved[name] = raw
	}
	for name, property := range spec.Properties {
		if _, ok := resolved[name]; !ok && len(property.Default) > 0 {
			resolved[name] = property.Default
		}
	}
	for _, name := range spec.Required {
		if _, ok := resolved[name]; !ok {
			return nil, fmt.Errorf("required feature argument %q is not configured", name)
		}
	}
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	fieldErrors, err := validate.Validate(schema, out)
	if err != nil {
		return nil, err
	}
	if len(fieldErrors) > 0 {
		return nil, fmt.Errorf("feature arguments are invalid: %s", fieldErrors[0].Message)
	}
	return out, nil
}

// CompileRouteTree turns a route tree (or a v3 forest under a virtual site
// root) into anchored segment-regex routes. Depth-first order preserves route
// priority while the virtual root keeps independent top-level paths separate.
func CompileRouteTree(root RouteTreeNode) ([]CompiledRoute, error) {
	if root.Type != "index" && root.Type != "site" {
		return nil, errors.New("route tree root must be an index or site node")
	}
	seen := map[string]bool{}
	var routes []Route
	var walk func(RouteTreeNode, string, map[string]bool, bool) error
	walk = func(node RouteTreeNode, prefix string, inherited map[string]bool, routeRoot bool) error {
		if strings.TrimSpace(node.ID) == "" || seen[node.ID] {
			return fmt.Errorf("route node id %q is missing or duplicated", node.ID)
		}
		if node.PageID != "" && node.FileID != "" {
			return fmt.Errorf("route node %q cannot reference both a page and a file", node.ID)
		}
		if node.FileID != "" && len(node.Children) > 0 {
			return fmt.Errorf("file route node %q must be terminal", node.ID)
		}
		seen[node.ID] = true
		pattern := prefix
		captures := cloneSet(inherited)
		switch node.Type {
		case "path":
			if !routeRoot || node.Value != "" || node.Name != "" || node.Pattern != "" {
				return fmt.Errorf("path node %q is only allowed as a route root", node.ID)
			}
			pathPattern, pathCaptures, err := compilePathRoot(node.Path)
			if err != nil {
				return fmt.Errorf("path node %q: %w", node.ID, err)
			}
			for name := range pathCaptures {
				if captures[name] {
					return fmt.Errorf("path node %q has duplicate capture %q", node.ID, name)
				}
				captures[name] = true
			}
			pattern = pathPattern
		case "index":
			if !routeRoot || prefix != "" || node.Path != "" || node.Value != "" || node.Name != "" || node.Pattern != "" {
				return fmt.Errorf("index node %q is only allowed as a route root", node.ID)
			}
			pattern = ""
		case "static":
			if node.Path != "" || node.Value == "" || node.Value == "." || node.Value == ".." || strings.ContainsAny(node.Value, "/?#") || node.Name != "" || node.Pattern != "" {
				return fmt.Errorf("static node %q must contain one URL segment", node.ID)
			}
			pattern += "/" + regexp.QuoteMeta(node.Value)
		case "param":
			if node.Path != "" || !argumentPattern.MatchString(node.Name) || node.Value != "" || node.Pattern != "" || captures[node.Name] {
				return fmt.Errorf("parameter node %q has an invalid or duplicate name", node.ID)
			}
			captures[node.Name] = true
			pattern += "/(?<" + node.Name + ">[^/]+)"
		case "regex":
			if node.Path != "" || node.Pattern == "" || strings.Contains(node.Pattern, "/") || node.Value != "" || node.Name != "" {
				return fmt.Errorf("regex node %q must match one URL segment", node.ID)
			}
			segment, err := regexp2.Compile("\\A(?:"+node.Pattern+")\\z", regexp2.None)
			if err != nil {
				return fmt.Errorf("regex node %q: %w", node.ID, err)
			}
			segment.MatchTimeout = routeMatchTimeout
			emptyMatch, err := segment.FindStringMatch("")
			if err != nil {
				return fmt.Errorf("regex node %q: %w", node.ID, err)
			}
			if emptyMatch != nil {
				return fmt.Errorf("regex node %q must not match an empty URL segment", node.ID)
			}
			for _, name := range segment.GetGroupNames() {
				if _, err := strconv.Atoi(name); err == nil {
					continue
				}
				if !argumentPattern.MatchString(name) || captures[name] {
					return fmt.Errorf("regex node %q has invalid or duplicate capture %q", node.ID, name)
				}
				captures[name] = true
			}
			pattern += "/(?:" + node.Pattern + ")"
		case "catchAll":
			if node.Path != "" || !argumentPattern.MatchString(node.Name) || node.Value != "" || node.Pattern != "" || captures[node.Name] || len(node.Children) != 0 {
				return fmt.Errorf("catch-all node %q must be terminal and have a unique name", node.ID)
			}
			captures[node.Name] = true
			pattern += "/(?<" + node.Name + ">.+)"
		default:
			return fmt.Errorf("route node %q has unknown type %q", node.ID, node.Type)
		}
		if node.PageID != "" || node.FileID != "" {
			matchPattern := pattern
			if matchPattern == "" {
				matchPattern = "/"
			}
			routes = append(routes, Route{
				ID:              node.ID,
				Pattern:         "^" + matchPattern + "$",
				PageID:          node.PageID,
				FileID:          node.FileID,
				FileDisposition: node.FileDisposition,
			})
		} else if len(node.Children) == 0 {
			return fmt.Errorf("route leaf %q must reference a page or file", node.ID)
		}
		for _, child := range node.Children {
			if err := walk(child, pattern, captures, false); err != nil {
				return err
			}
		}
		return nil
	}
	if root.Type == "site" {
		if strings.TrimSpace(root.ID) == "" || root.PageID != "" || root.FileID != "" || root.FileDisposition != "" || root.Path != "" || root.Value != "" || root.Name != "" || root.Pattern != "" {
			return nil, errors.New("site route tree root must be an unrendered virtual node")
		}
		seen[root.ID] = true
		for _, child := range root.Children {
			if err := walk(child, "", map[string]bool{}, true); err != nil {
				return nil, err
			}
		}
	} else if err := walk(root, "", map[string]bool{}, true); err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, errors.New("route tree must contain at least one page")
	}
	return CompileRoutes(routes)
}

// compilePathRoot parses the user-facing absolute-path syntax used by a root
// route: static segments, :named parameters, ~segment-regex, and *catch-all.
// Regex applies to exactly one segment and may expose named captures.
func compilePathRoot(path string) (string, map[string]bool, error) {
	if path == "/" {
		return "", map[string]bool{}, nil
	}
	if len(path) < 2 || len(path) > 500 || path[0] != '/' || strings.HasSuffix(path, "/") || strings.Contains(path, "#") {
		return "", nil, errors.New("root path must be an absolute URL path without a trailing slash, query, or fragment")
	}
	segments := strings.Split(path[1:], "/")
	captures := make(map[string]bool)
	var pattern strings.Builder
	for index, segment := range segments {
		if segment == "" {
			return "", nil, errors.New("root path cannot contain empty segments")
		}
		pattern.WriteByte('/')
		switch {
		case strings.HasPrefix(segment, ":"):
			name := strings.TrimPrefix(segment, ":")
			if !argumentPattern.MatchString(name) || captures[name] {
				return "", nil, fmt.Errorf("root path has an invalid or duplicate parameter %q", name)
			}
			captures[name] = true
			pattern.WriteString("(?<" + name + ">[^/]+)")
		case strings.HasPrefix(segment, "*"):
			name := strings.TrimPrefix(segment, "*")
			if index != len(segments)-1 || !argumentPattern.MatchString(name) || captures[name] {
				return "", nil, fmt.Errorf("root path catch-all %q must be a unique final segment", name)
			}
			captures[name] = true
			pattern.WriteString("(?<" + name + ">.+)")
		case strings.HasPrefix(segment, "~"):
			segmentRegex := strings.TrimPrefix(segment, "~")
			if segmentRegex == "" || strings.Contains(segmentRegex, "/") {
				return "", nil, errors.New("root path regex segment cannot be empty")
			}
			compiled, err := regexp2.Compile(`\A(?:`+segmentRegex+`)\z`, regexp2.None)
			if err != nil {
				return "", nil, fmt.Errorf("invalid root path regex: %w", err)
			}
			compiled.MatchTimeout = routeMatchTimeout
			emptyMatch, err := compiled.FindStringMatch("")
			if err != nil {
				return "", nil, fmt.Errorf("invalid root path regex: %w", err)
			}
			if emptyMatch != nil {
				return "", nil, errors.New("root path regex must not match an empty URL segment")
			}
			for _, name := range compiled.GetGroupNames() {
				if _, err := strconv.Atoi(name); err == nil {
					continue
				}
				if !argumentPattern.MatchString(name) || captures[name] {
					return "", nil, fmt.Errorf("root path regex has an invalid or duplicate capture %q", name)
				}
				captures[name] = true
			}
			pattern.WriteString("(?:" + segmentRegex + ")")
		default:
			if segment == "." || segment == ".." || strings.ContainsAny(segment, "/?#") {
				return "", nil, errors.New("root path contains an invalid static segment")
			}
			pattern.WriteString(regexp.QuoteMeta(segment))
		}
	}
	return pattern.String(), captures, nil
}

func cloneSet(input map[string]bool) map[string]bool {
	output := make(map[string]bool, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func coerceRouteValue(targetType, value string) (json.RawMessage, error) {
	switch targetType {
	case "integer":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(strconv.FormatInt(n, 10)), nil
	case "number":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(strconv.FormatFloat(n, 'f', -1, 64)), nil
	case "boolean":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(strconv.FormatBool(b)), nil
	case "string", "":
		return json.Marshal(value)
	default:
		return nil, fmt.Errorf("unsupported route argument type %q", targetType)
	}
}
