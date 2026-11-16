package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/pkg/environment"
	publicruntime "websiteengine/pkg/runtime"
)

func TestCanonicalAndBranchIndexingPolicy(t *testing.T) {
	metadata := DefaultSiteMetadata()
	metadata.CanonicalDomain = "https://example.test"
	metadata.Language = "ru"
	noIndex := true
	metadata.Pages["private"] = SiteMetadataOverride{NoIndex: &noIndex}
	for _, input := range []struct {
		page      string
		indexable bool
		robots    string
	}{{"home", true, "index,follow"}, {"home", false, "noindex,nofollow"}, {"private", true, "noindex,nofollow"}} {
		head := metadata.ForPath(input.page, "/catalog/товар", input.indexable)
		if head.Robots != input.robots || !strings.HasPrefix(head.CanonicalURL, "https://example.test/catalog/") {
			t.Fatalf("unexpected policy: %+v", head)
		}
	}
	for _, raw := range []string{"https://user:pw@example.test", "https://example.test/path", "https://example.test?query=yes", "javascript:alert(1)", "//example.test"} {
		metadata.CanonicalDomain = raw
		if metadata.Validate() == nil {
			t.Fatalf("accepted invalid origin %q", raw)
		}
	}
}

func TestStaticPagePathsKeepDynamicRoutesOutOfSitemap(t *testing.T) {
	root := RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "home", Type: "path", Path: "/", PageID: "home"},
		{ID: "catalog", Type: "path", Path: "/catalog", PageID: "catalog", Children: []RouteTreeNode{{ID: "detail", Type: "param", Name: "slug", PageID: "detail"}}},
	}}
	paths, warnings := staticPagePaths(root)
	if len(paths) != 2 || paths[0] != "/" || paths[1] != "/catalog" || len(warnings) != 1 {
		t.Fatalf("paths=%v warnings=%v", paths, warnings)
	}
}

func TestPageIndexRouteHintPreservesFirstMatchAndCaptures(t *testing.T) {
	root := RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "dynamic-first", Type: "path", Path: "/catalog/:slug", PageID: "dynamic"},
		{ID: "static-later", Type: "path", Path: "/catalog/sale", PageID: "static"},
	}}
	routes, err := CompileRouteTree(root)
	if err != nil {
		t.Fatal(err)
	}
	indexes := map[string]int{"dynamic-first": 0, "static-later": 1}
	got, args, matched, err := matchPageIndexRoute(routes, indexes, "static-later", "/catalog/sale")
	if err != nil || !matched {
		t.Fatalf("matchPageIndexRoute() = (%+v, %v, %v), want a match: %v", got, args, matched, err)
	}
	if got.ID != "dynamic-first" || args["slug"] != "sale" {
		t.Fatalf("hint changed first-match route semantics: route=%+v args=%v", got, args)
	}
}

func TestPageIndexRouteHintFallsThroughWhenHintDoesNotMatch(t *testing.T) {
	root := RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "first", Type: "path", Path: "/about", PageID: "about"},
		{ID: "hint", Type: "path", Path: "/catalog/special", PageID: "special"},
		{ID: "fallback", Type: "path", Path: "/catalog/:slug", PageID: "catalog"},
	}}
	routes, err := CompileRouteTree(root)
	if err != nil {
		t.Fatal(err)
	}
	indexes := map[string]int{"first": 0, "hint": 1, "fallback": 2}
	got, args, matched, err := matchPageIndexRoute(routes, indexes, "hint", "/catalog/sale")
	if err != nil || !matched {
		t.Fatalf("matchPageIndexRoute() = (%+v, %v, %v), want a match: %v", got, args, matched, err)
	}
	if got.ID != "fallback" || args["slug"] != "sale" {
		t.Fatalf("route after a non-matching hint should retain first-match behavior: route=%+v args=%v", got, args)
	}
	got, args, matched, err = matchPageIndexRoute(routes, indexes, "hint", "/unknown")
	if err != nil || matched {
		t.Fatalf("unmatched hint unexpectedly matched: route=%+v args=%v matched=%v err=%v", got, args, matched, err)
	}
}

func BenchmarkPageIndexRouteResolution(b *testing.B) {
	const routeCount = 2000
	routes := make([]Route, 0, routeCount)
	indexes := make(map[string]int, routeCount)
	hintedRouteID := ""
	for i := 0; i < routeCount; i++ {
		id := fmt.Sprintf("route-%04d", i)
		pattern := fmt.Sprintf("^/unrelated/%d$", i)
		if i == routeCount/4 {
			pattern = "^/catalog/sale$"
			hintedRouteID = id
		}
		indexes[id] = i
		routes = append(routes, Route{ID: id, Pattern: pattern, PageID: "page"})
	}
	compiled, err := CompileRoutes(routes)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("full-scan", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, _, err := MatchRoute(compiled, "/catalog/sale"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("route-hint", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, _, err := matchPageIndexRoute(compiled, indexes, hintedRouteID, "/catalog/sale"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestRuntimeForSnapshotPathReusesPreparedReadState(t *testing.T) {
	root := RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "home", Type: "path", Path: "/", PageID: "home"},
		{ID: "about", Type: "path", Path: "/about", PageID: "about"},
	}}
	routes, err := CompileRouteTree(root)
	if err != nil {
		t.Fatalf("compile test routes: %v", err)
	}
	metadata := DefaultSiteMetadata()
	metadata.CanonicalDomain = "https://site.example"
	payload := SnapshotPayload{
		Metadata: &metadata,
		Config: SiteConfig{
			Pages:     []Page{{ID: "home", Name: "Home"}, {ID: "about", Name: "About"}},
			RouteTree: root,
		},
	}
	service := &SnapshotService{}
	state := &runtimeReadState{branchLoaded: true, branchIndexable: true, modelLoaded: true}
	record := SnapshotRecord{ID: uuid.New(), ContentRevision: 4}
	branch := environment.Environment(uuid.NewString())
	for _, path := range []string{"/", "/about"} {
		composition, err := service.runtimeForSnapshotPath(context.Background(), branch, record, payload, routes, path, nil, state)
		if err != nil {
			t.Fatalf("compose %s from prepared state: %v", path, err)
		}
		if composition.Metadata.Robots != "index,follow" || composition.Metadata.CanonicalURL != "https://site.example"+path {
			t.Errorf("unexpected metadata for %s: %+v", path, composition.Metadata)
		}
	}
}

func TestRouteTreeNodeIsCatchAll(t *testing.T) {
	root := RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{
		{ID: "wild-root", Type: "path", Path: "/legacy/*rest", PageID: "not-found"},
		{ID: "static", Type: "path", Path: "/not-found", PageID: "not-found"},
		{ID: "nested", Type: "path", Path: "/docs", Children: []RouteTreeNode{
			{ID: "tail", Type: "catchAll", Name: "path", PageID: "not-found"},
			{ID: "param", Type: "param", Name: "slug", PageID: "not-found"},
		}},
	}}
	for _, input := range []struct {
		id   string
		want bool
	}{{"wild-root", true}, {"tail", true}, {"static", false}, {"param", false}, {"missing", false}} {
		if got := routeTreeNodeIsCatchAll(root, input.id); got != input.want {
			t.Errorf("route %q catch-all = %v, want %v", input.id, got, input.want)
		}
	}
}

func TestApplyDetailSEOMetadataUsesPublishedProjectionAndKeepsPageFallback(t *testing.T) {
	metadata := DefaultSiteMetadata()
	metadata.CanonicalDomain = "https://shop.example"
	metadata.SiteName = "Каталог"
	metadata.Description = "Описание сайта"
	pageSEO := "Статический og:title"
	metadata.Pages["detail"] = SiteMetadataOverride{OGTitle: &pageSEO}
	head := metadata.ForPath("detail", "/catalog/pear", true)

	updated, err := applyDetailSEOMetadata(head, metadata, "detail", json.RawMessage(`{"title":"Груша","description":"Сочный фрукт","ogDescription":"Свежая груша","ogImage":"00000000-0000-4000-8000-00000000000a"}`))
	if err != nil {
		t.Fatalf("apply published detail metadata: %v", err)
	}
	if updated.Title != "Груша" || updated.Description != "Сочный фрукт" || updated.OGTitle != pageSEO || updated.OGDescription != "Свежая груша" || updated.OGImageURL != "https://shop.example/media/00000000-0000-4000-8000-00000000000a" {
		t.Fatalf("unexpected detail head metadata: %+v", updated)
	}

	metadata.Pages = nil
	head = metadata.ForPath("detail", "/catalog/pear", true)
	updated, err = applyDetailSEOMetadata(head, metadata, "detail", json.RawMessage(`{"title":"Яблоко","description":"Сочный фрукт"}`))
	if err != nil {
		t.Fatalf("apply inherited detail metadata: %v", err)
	}
	if updated.Title != "Яблоко" || updated.Description != "Сочный фрукт" || updated.OGTitle != "Яблоко" || updated.OGDescription != "Сочный фрукт" {
		t.Fatalf("derived Open Graph text should follow dynamic title/description: %+v", updated)
	}
}

func TestApplyDetailSEOMetadataRejectsInvalidPublishedMediaID(t *testing.T) {
	if _, err := applyDetailSEOMetadata(publicruntime.HeadMetadata{}, DefaultSiteMetadata(), "detail", json.RawMessage(`{"ogImage":"private-or-invalid"}`)); err == nil {
		t.Fatal("invalid model media ID must not be inserted into public head metadata")
	}
}

type mediaReadinessRow struct {
	ready bool
	err   error
}

func (r mediaReadinessRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("expected one scan destination")
	}
	value, ok := dest[0].(*bool)
	if !ok {
		return errors.New("expected bool scan destination")
	}
	*value = r.ready
	return nil
}

type mediaReadinessVerifier struct {
	ready    map[uuid.UUID]bool
	calls    map[uuid.UUID]int
	failOnce map[uuid.UUID]bool
}

func (v *mediaReadinessVerifier) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	id := args[0].(uuid.UUID)
	v.calls[id]++
	if v.failOnce[id] {
		delete(v.failOnce, id)
		return mediaReadinessRow{err: errors.New("temporary verifier failure")}
	}
	return mediaReadinessRow{ready: v.ready[id]}
}

func TestValidatePublicMediaArgumentsReusesPerBuildReadiness(t *testing.T) {
	readyID := uuid.New()
	privateID := uuid.New()
	verifier := &mediaReadinessVerifier{
		ready:    map[uuid.UUID]bool{readyID: true, privateID: false},
		calls:    map[uuid.UUID]int{},
		failOnce: map[uuid.UUID]bool{},
	}
	cache := make(map[uuid.UUID]bool)
	schema := json.RawMessage(`{"type":"string","format":"media"}`)
	validate := func(id uuid.UUID) error {
		data, err := json.Marshal(id.String())
		if err != nil {
			t.Fatal(err)
		}
		return validatePublicMediaArguments(context.Background(), verifier, schema, data, cache)
	}

	if err := validate(readyID); err != nil {
		t.Fatalf("first ready public media validation: %v", err)
	}
	if err := validate(readyID); err != nil {
		t.Fatalf("cached ready public media validation: %v", err)
	}
	if verifier.calls[readyID] != 1 {
		t.Fatalf("ready media verifier calls = %d, want 1", verifier.calls[readyID])
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := validate(privateID); err == nil {
			t.Fatal("private or unavailable media must fail closed")
		}
	}
	if verifier.calls[privateID] != 1 {
		t.Fatalf("unavailable media verifier calls = %d, want 1", verifier.calls[privateID])
	}

	transientID := uuid.New()
	verifier.ready[transientID] = true
	verifier.failOnce[transientID] = true
	if err := validate(transientID); err == nil {
		t.Fatal("transient verifier error must propagate")
	}
	if err := validate(transientID); err != nil {
		t.Fatalf("retry after verifier error: %v", err)
	}
	if verifier.calls[transientID] != 2 {
		t.Fatalf("transient media verifier calls = %d, want 2", verifier.calls[transientID])
	}

	// A nil cache retains request-local behavior and never stores readiness.
	data, _ := json.Marshal(readyID.String())
	for attempt := 0; attempt < 2; attempt++ {
		if err := validatePublicMediaArguments(context.Background(), verifier, schema, data, nil); err != nil {
			t.Fatalf("validation with nil cache: %v", err)
		}
	}
	if verifier.calls[readyID] != 3 {
		t.Fatalf("nil-cache media verifier calls = %d, want 3 total", verifier.calls[readyID])
	}
}

func TestDetailRouteFieldValueSkipsMissingAndEmptyPublishedValues(t *testing.T) {
	for _, record := range []ModelContentRecord{
		{Values: map[string]any{}},
		{Values: map[string]any{"slug": nil}},
		{Values: map[string]any{"slug": "  "}},
	} {
		if value, ok := detailRouteFieldValue(record, "slug"); ok || value != "" {
			t.Errorf("missing/empty published route value should be skipped: value=%q present=%v", value, ok)
		}
	}
	if value, ok := detailRouteFieldValue(ModelContentRecord{Values: map[string]any{"slug": "pear"}}, "slug"); !ok || value != "pear" {
		t.Fatalf("published slug was not resolved: value=%q present=%v", value, ok)
	}
	if value, ok := detailRouteFieldValue(ModelContentRecord{Values: map[string]any{"slug": " pear "}}, "slug"); !ok || value != " pear " {
		t.Fatalf("published route value must not be normalized away from lookup identity: value=%q present=%v", value, ok)
	}
}
