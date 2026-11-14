package sitebuilder

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestRouteCapturesNamedArgumentsAndRespectsPriority(t *testing.T) {
	routes, err := CompileRouteTree(RouteTreeNode{ID: "root", Type: "index", PageID: "home", Children: []RouteTreeNode{
		{ID: "special", Type: "static", Value: "products", Children: []RouteTreeNode{
			{ID: "sku", Type: "regex", Pattern: `(?<slug>[a-z0-9-]+)`, PageID: "product"},
			{ID: "fallback", Type: "param", Name: "fallback", PageID: "not-found"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	route, args, matched, err := MatchRoute(routes, "/products/green-feed")
	if err != nil || !matched {
		t.Fatalf("MatchRoute() = (%#v, %#v, %v, %v)", route, args, matched, err)
	}
	if route.ID != "sku" || args["slug"] != "green-feed" {
		t.Fatalf("unexpected match: route=%#v args=%#v", route, args)
	}
}

func TestResolveArgumentsAcceptsLiteralPathAndQueryBindings(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"page":{"type":"integer","default":1},"enabled":{"type":"boolean"},"tags":{"type":"array","items":{"type":"string"}}},"required":["slug","page","enabled"],"additionalProperties":false}`)
	resolved, err := ResolveArguments(schema, map[string]ArgumentBinding{
		"slug":    {Source: "path", PathArg: "slug"},
		"enabled": {Source: "literal", Value: json.RawMessage(`true`)},
		"tags":    {Source: "query", QueryKey: "tag"},
	}, map[string]string{"slug": "product-7"}, url.Values{"tag": {"feed", "mineral"}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(resolved, &got); err != nil {
		t.Fatal(err)
	}
	if got["slug"] != "product-7" || got["page"].(float64) != 1 || got["enabled"] != true || len(got["tags"].([]any)) != 2 {
		t.Fatalf("unexpected resolved arguments: %s", resolved)
	}
}

func TestResolveArgumentsRejectsMissingAndInvalidBindings(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}`)
	cases := []map[string]ArgumentBinding{
		{},
		{"unknown": {Source: "literal", Value: json.RawMessage(`1`)}},
		{"id": {Source: "path", PathArg: "missing"}},
		{"id": {Source: "literal", Value: json.RawMessage(`"not an integer"`)}},
	}
	for i, bindings := range cases {
		if _, err := ResolveArguments(schema, bindings, map[string]string{}, nil); err == nil {
			t.Fatalf("case %d: expected binding error", i)
		}
	}
}

func TestResolveArgumentsRejectsRepeatedScalarQueryAndCoercesTypedArray(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer"},"ids":{"type":"array","items":{"type":"integer"}}},"required":["page","ids"]}`)
	bindings := map[string]ArgumentBinding{
		"page": {Source: "query", QueryKey: "page"},
		"ids":  {Source: "query", QueryKey: "id"},
	}
	if _, err := ResolveArguments(schema, bindings, nil, url.Values{"page": {"1", "2"}, "id": {"3"}}); err == nil {
		t.Fatal("expected repeated scalar query to fail")
	}
	resolved, err := ResolveArguments(schema, bindings, nil, url.Values{"page": {"2"}, "id": {"3", "4"}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(resolved, &got); err != nil {
		t.Fatal(err)
	}
	if got["page"].(float64) != 2 || len(got["ids"].([]any)) != 2 {
		t.Fatalf("unexpected resolved query arguments: %s", resolved)
	}
}

func TestCompileRouteTreeSupportsIntermediateRegexCapturesAndCatchAll(t *testing.T) {
	routes, err := CompileRouteTree(RouteTreeNode{ID: "root", Type: "index", Children: []RouteTreeNode{
		{ID: "category", Type: "regex", Pattern: `(?<category>[a-z]+)`, Children: []RouteTreeNode{
			{ID: "asset", Type: "catchAll", Name: "path", PageID: "asset"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	route, args, matched, err := MatchRoute(routes, "/images/feed/icons/logo.svg")
	if err != nil || !matched || route.ID != "asset" || args["category"] != "images" || args["path"] != "feed/icons/logo.svg" {
		t.Fatalf("unexpected catch-all match: route=%#v args=%#v matched=%v err=%v", route, args, matched, err)
	}
}

func TestCompileRouteTreeSupportsPagesAtIntermediateNodes(t *testing.T) {
	valid := RouteTreeNode{ID: "root", Type: "index", Children: []RouteTreeNode{
		{ID: "catalog", Type: "static", Value: "catalog", Children: []RouteTreeNode{
			{ID: "item", Type: "param", Name: "slug", PageID: "detail"},
		}},
	}}
	if _, err := CompileRouteTree(valid); err != nil {
		t.Fatalf("valid routed tree was rejected: %v", err)
	}

	branchPage := valid
	branchPage.Children = []RouteTreeNode{{ID: "catalog", Type: "static", Value: "catalog", PageID: "list", Children: []RouteTreeNode{
		{ID: "item", Type: "param", Name: "slug", PageID: "detail"},
	}}}
	routes, err := CompileRouteTree(branchPage)
	if err != nil {
		t.Fatalf("page on an intermediate route was rejected: %v", err)
	}
	if len(routes) != 2 || routes[0].PageID != "list" || routes[1].PageID != "detail" {
		t.Fatalf("expected both the parent and child pages, got %#v", routes)
	}

	missingLeafPage := RouteTreeNode{ID: "root", Type: "index", Children: []RouteTreeNode{{ID: "catalog", Type: "static", Value: "catalog"}}}
	if _, err := CompileRouteTree(missingLeafPage); err == nil {
		t.Fatal("expected a route leaf without a page to be rejected")
	}
}

func TestCompileSiteRouteForestSupportsIndependentRootsAndAliases(t *testing.T) {
	routes, err := CompileRouteTree(RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "home", Type: "path", Path: "/", PageID: "home"},
		{ID: "about", Type: "path", Path: "/about", PageID: "about"},
		{ID: "catalog", Type: "path", Path: "/catalog", PageID: "catalog", Children: []RouteTreeNode{
			{ID: "catalog-detail", Type: "param", Name: "slug", PageID: "detail"},
		}},
		{ID: "products", Type: "path", Path: "/products", PageID: "catalog", Children: []RouteTreeNode{
			{ID: "product-detail", Type: "param", Name: "slug", PageID: "detail"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"^/$":                        "home",
		"^/about$":                   "about",
		"^/catalog$":                 "catalog",
		"^/catalog/(?<slug>[^/]+)$":  "detail",
		"^/products$":                "catalog",
		"^/products/(?<slug>[^/]+)$": "detail",
	}
	if len(routes) != len(want) {
		t.Fatalf("compiled %d routes, want %d: %#v", len(routes), len(want), routes)
	}
	for _, route := range routes {
		if pageID, ok := want[route.Pattern]; !ok || pageID != route.PageID {
			t.Fatalf("unexpected compiled route %#v", route.Route)
		}
	}
	for _, path := range []string{"/", "/about", "/catalog", "/catalog/one", "/products", "/products/one"} {
		if _, _, matched, err := MatchRoute(routes, path); err != nil || !matched {
			t.Fatalf("path %q did not match: matched=%v err=%v", path, matched, err)
		}
	}
}

func TestCompileRouteTreeSupportsStaticPublicFileTargets(t *testing.T) {
	fileID := "11111111-1111-4111-8111-111111111111"
	routes, err := CompileRouteTree(RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "download", Type: "path", Path: "/files/price-list.pdf", FileID: fileID, FileDisposition: "attachment"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	route, _, matched, err := MatchRoute(routes, "/files/price-list.pdf")
	if err != nil || !matched {
		t.Fatalf("file route did not match: matched=%v err=%v", matched, err)
	}
	if route.FileID != fileID || route.FileDisposition != "attachment" || route.PageID != "" {
		t.Fatalf("unexpected file route: %#v", route)
	}

	for name, node := range map[string]RouteTreeNode{
		"dynamic file route": {ID: "dynamic", Type: "path", Path: "/files/:name", FileID: fileID},
		"file route with child": {
			ID: "file", Type: "path", Path: "/download", FileID: fileID,
			Children: []RouteTreeNode{{ID: "child", Type: "static", Value: "child", PageID: "page"}},
		},
		"page and file": {ID: "both", Type: "path", Path: "/both", PageID: "page", FileID: fileID},
	} {
		if _, err := CompileRouteTree(RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{node}}); err == nil {
			t.Fatalf("%s should have been rejected", name)
		}
	}
}

func TestCompileSiteRootPathSupportsTypedSegmentsAndRejectsInvalidPaths(t *testing.T) {
	routes, err := CompileRouteTree(RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "root", Type: "path", Path: "/catalog/~(?<category>[a-z]+)/:slug", PageID: "product"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, args, matched, err := MatchRoute(routes, "/catalog/feed/green-feed")
	if err != nil || !matched || args["category"] != "feed" || args["slug"] != "green-feed" {
		t.Fatalf("unexpected root path match: args=%#v matched=%v err=%v", args, matched, err)
	}
	for _, path := range []string{"catalog", "/catalog/", "/catalog//item", "/catalog/*rest/final", "/catalog/:bad-name", "/catalog/~[", "/../admin"} {
		if _, err := CompileRouteTree(RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{{ID: "root", Type: "path", Path: path, PageID: "page"}}}); err == nil {
			t.Errorf("path %q should have been rejected", path)
		}
	}

	queryRegex, err := CompileRouteTree(RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "optional-segment", Type: "path", Path: "/catalog/~(?<version>v[0-9]+)", PageID: "catalog"},
	}})
	if err != nil {
		t.Fatalf("regex syntax containing ? was rejected: %v", err)
	}
	if _, args, matched, err := MatchRoute(queryRegex, "/catalog/v2"); err != nil || !matched || args["version"] != "v2" {
		t.Fatalf("unexpected regex path match: args=%#v matched=%v err=%v", args, matched, err)
	}
	for _, pattern := range []string{"a*", "(?:)"} {
		if _, err := CompileRouteTree(RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
			{ID: "empty", Type: "path", Path: "/~" + pattern, PageID: "page"},
		}}); err == nil {
			t.Errorf("root regex %q should not match an empty URL segment", pattern)
		}
	}
}
