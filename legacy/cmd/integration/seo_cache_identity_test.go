package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	"websiteengine/pkg/environment"
)

func TestBranchSEOEffectiveRenderCacheIdentity(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	yes := true
	branches, err := e.snapshots.RegisterBranches(ctx, []config.BranchConfig{{Name: "cache-index", HTTPAddr: ":18381", CORSOrigins: []string{}, Indexable: &yes}})
	if err != nil {
		t.Fatal(err)
	}
	branch := branches[0]

	manifest := testFeatureManifestFor("cache-query", "1.0.0")
	manifest["arguments"] = objectSchema(map[string]any{
		"slug":   map[string]any{"type": "string"},
		"filter": map[string]any{"type": "string"},
		"labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, "slug")
	uploadFeatureBuild(t, e, manifest, "export default function Feature() {}")
	cfg := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Pages: []sitebuilder.Page{{
			ID: "product", Name: "Product", Kind: "page",
			Placements: []sitebuilder.Placement{{
				ID: "query-feature", Slot: "main", Build: sitebuilder.BuildRef{FeatureKey: "cache-query", Version: "1.0.0"},
				Arguments: map[string]sitebuilder.ArgumentBinding{
					"slug":   {Source: "path", PathArg: "slug"},
					"filter": {Source: "query", QueryKey: "filter"},
					"labels": {Source: "query", QueryKey: "label"},
				},
			}},
		}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "product", Type: "path", Path: "/product/:slug", PageID: "product"}}},
	}
	metadata := sitebuilder.DefaultSiteMetadata()
	metadata.CanonicalDomain = "https://cache.example"
	metadata.Language = "en"
	root, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Query cache identity", Config: &cfg, Metadata: &metadata})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.snapshots.AssignBranch(ctx, actor, branch.ID, root.ID, branch.Revision); err != nil {
		t.Fatal(err)
	}
	env := environment.Environment(branch.ID.String())

	withoutQuery, err := e.snapshots.PageDocument(ctx, env, "/product/alpha", nil, true)
	if err != nil || !strings.Contains(string(withoutQuery.HTML), `content="index,follow"`) {
		t.Fatalf("empty query changed the indexable robots policy: err=%v html=%s", err, withoutQuery.HTML)
	}
	queryA := url.Values{"filter": {"sale"}, "label": {"one", "two"}, "utm_source": {"one"}}
	queryB := url.Values{"utm_source": {"two"}, "filter": {"sale"}, "label": {"one", "two"}}
	first, err := e.snapshots.PageDocument(ctx, env, "/product/alpha", queryA, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.snapshots.PageDocument(ctx, env, "/product/alpha", queryB, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first.HTML), `content="noindex,follow"`) {
		t.Fatalf("query variant must retain noindex,follow: %s", first.HTML)
	}
	if first.CacheKey != second.CacheKey {
		t.Fatal("an unrelated tracking value changed the effective render identity")
	}
	arrayOrderChanged, err := e.snapshots.PageDocument(ctx, env, "/product/alpha", url.Values{"filter": {"sale"}, "label": {"two", "one"}, "utm_source": {"two"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheKey == arrayOrderChanged.CacheKey {
		t.Fatal("reordering repeated query values bound to an array did not change the render identity")
	}

	changedArgument, err := e.snapshots.PageDocument(ctx, env, "/product/alpha", url.Values{"filter": {"new"}, "utm_source": {"two"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheKey == changedArgument.CacheKey {
		t.Fatal("a query value bound to the manifest argument did not change the render identity")
	}
	changedPath, err := e.snapshots.PageDocument(ctx, env, "/product/beta", queryB, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheKey == changedPath.CacheKey {
		t.Fatal("a distinct dynamic route path did not change the render identity")
	}
	if first.CacheKey == withoutQuery.CacheKey {
		t.Fatal("query changed robots metadata but failed to change the render identity")
	}

	previewData, err := json.Marshal(sitebuilder.DesignSystem{
		FormatVersion: 1,
		Tokens:        map[string]string{"--cache-preview": "#123456"},
		MobileTokens:  map[string]string{},
		Fonts:         []sitebuilder.DesignFont{},
		FontRoles:     map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	previewA, err := e.snapshots.CreateDesignPreview(ctx, previewData)
	if err != nil {
		t.Fatal(err)
	}
	previewB, err := e.snapshots.CreateDesignPreview(ctx, previewData)
	if err != nil {
		t.Fatal(err)
	}
	previewDocA, err := e.snapshots.PageDocument(ctx, env, "/product/alpha", url.Values{"filter": {"sale"}, "__designPreview": {previewA}}, true)
	if err != nil {
		t.Fatal(err)
	}
	previewDocB, err := e.snapshots.PageDocument(ctx, env, "/product/alpha", url.Values{"filter": {"sale"}, "__designPreview": {previewB}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if previewDocA.CacheKey == previewDocB.CacheKey {
		t.Fatal("different temporary theme URLs collided in the render cache")
	}
}
