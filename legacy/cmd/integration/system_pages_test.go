package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"websiteengine/features/sitebuilder"
	httptransport "websiteengine/presentation/http"
)

func createAssignedSystemPagesSnapshot(t *testing.T, env *testEnv, cfg sitebuilder.SiteConfig) sitebuilder.SnapshotRecord {
	t.Helper()
	ctx := context.Background()
	actor := testActor(t, env)
	snapshot, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "System page fallback QA", Config: &cfg})
	if err != nil {
		t.Fatalf("create system pages snapshot: %v", err)
	}
	branchID, err := uuid.Parse(env.devBranch.String())
	if err != nil {
		t.Fatal(err)
	}
	branch, err := env.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatalf("load QA branch: %v", err)
	}
	if _, err := env.snapshots.AssignBranch(ctx, actor, branchID, snapshot.ID, branch.Revision); err != nil {
		t.Fatalf("assign QA snapshot: %v", err)
	}
	return snapshot
}

func uploadSystemPageBuild(t *testing.T, env *testEnv, key string) {
	t.Helper()
	manifest := testFeatureManifestFor(key, "1.0.0")
	uploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")
}

func uploadRequiredQuerySystemPageBuild(t *testing.T, env *testEnv, key string) sitebuilder.BuildRef {
	t.Helper()
	manifest := testFeatureManifestFor(key, "1.0.0")
	manifest["arguments"] = map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"requiredValue": map[string]any{"type": "string"}},
		"required":             []string{"requiredValue"},
		"additionalProperties": false,
	}
	uploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")
	return sitebuilder.BuildRef{FeatureKey: key, Version: "1.0.0"}
}

func requiredQuerySystemPagePlacement(id string, build sitebuilder.BuildRef) sitebuilder.Placement {
	return sitebuilder.Placement{
		ID: id, Build: build, Slot: "main",
		Arguments: map[string]sitebuilder.ArgumentBinding{
			"requiredValue": {Source: "query", QueryKey: "requiredValue"},
		},
	}
}

func simpleSystemPagesConfig(homeBuild sitebuilder.BuildRef) sitebuilder.SiteConfig {
	return sitebuilder.SiteConfig{
		FormatVersion: 3,
		Features:      []sitebuilder.BuildRef{homeBuild},
		Pages: []sitebuilder.Page{{
			ID: "home", Name: "Home", Kind: "page",
			Placements: []sitebuilder.Placement{{ID: "home-placement", Build: homeBuild, Slot: "main"}},
		}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "root", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home"}}},
	}
}

func TestSystemPage404FallsBackWhenOverrideIsMissingOrBroken(t *testing.T) {
	t.Run("missing override", func(t *testing.T) {
		env := newTestEnv(t)
		uploadSystemPageBuild(t, env, "fallback-home")
		home := sitebuilder.BuildRef{FeatureKey: "fallback-home", Version: "1.0.0"}
		snapshot := createAssignedSystemPagesSnapshot(t, env, simpleSystemPagesConfig(home))

		document, err := env.snapshots.PageDocument(context.Background(), env.devBranch, "/missing", nil, false)
		if err != nil || document.Status != http.StatusNotFound {
			t.Fatalf("missing route should return a core 404: status=%d err=%v", document.Status, err)
		}
		if !strings.Contains(string(document.HTML), "Страница не найдена") || !strings.Contains(string(document.HTML), "noindex,nofollow") {
			t.Fatalf("missing override did not produce the safe core 404: %s", document.HTML)
		}
		if document.SnapshotID != "" || document.CacheKey != "" {
			t.Fatalf("core fallback should not claim a snapshot render: %+v (snapshot %s)", document, snapshot.ID)
		}
	})

	t.Run("broken override", func(t *testing.T) {
		env := newTestEnv(t)
		uploadSystemPageBuild(t, env, "fallback-home")
		home := sitebuilder.BuildRef{FeatureKey: "fallback-home", Version: "1.0.0"}
		errorBuild := uploadRequiredQuerySystemPageBuild(t, env, "broken-404")
		cfg := simpleSystemPagesConfig(home)
		cfg.Features = append(cfg.Features, errorBuild)
		cfg.Pages = append(cfg.Pages, sitebuilder.Page{ID: "not-found", Name: "Not found", Kind: "page", Placements: []sitebuilder.Placement{requiredQuerySystemPagePlacement("not-found-placement", errorBuild)}})
		cfg.RouteTree.Children = append(cfg.RouteTree.Children, sitebuilder.RouteTreeNode{ID: "not-found-route", Type: "path", Path: "/not-found", PageID: "not-found"})
		cfg.SystemPages = map[string]string{"404": "not-found"}
		createAssignedSystemPagesSnapshot(t, env, cfg)

		document, err := env.snapshots.PageDocument(context.Background(), env.devBranch, "/missing", nil, false)
		if err != nil || document.Status != http.StatusNotFound {
			t.Fatalf("broken 404 override should preserve the original 404 status: status=%d err=%v", document.Status, err)
		}
		if !strings.Contains(string(document.HTML), "Страница не найдена") || !strings.Contains(string(document.HTML), "noindex,nofollow") {
			t.Fatalf("broken override did not produce the safe core 404: %s", document.HTML)
		}
		if document.SnapshotID != "" || document.CacheKey != "" {
			t.Fatalf("core fallback should not claim a snapshot render: %+v", document)
		}
	})
}

func TestUnassignedBranchUsesBuiltInNotConfigured503(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	branchID := uuid.MustParse(env.prodBranch.String())
	branch, err := env.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.SnapshotID != nil {
		t.Fatalf("test branch must begin without a snapshot, got %s", *branch.SnapshotID)
	}

	document, err := env.snapshots.PageDocument(ctx, env.prodBranch, "/", nil, true)
	if err != nil || document.Status != http.StatusServiceUnavailable {
		t.Fatalf("unassigned branch should return the built-in 503: status=%d err=%v", document.Status, err)
	}
	if !strings.Contains(string(document.HTML), "Сайт ещё не настроен") || !strings.Contains(string(document.HTML), "noindex,nofollow") {
		t.Fatalf("unassigned branch did not return the safe built-in page: %s", document.HTML)
	}
	if document.SnapshotID != "" || document.CacheKey != "" || document.Cacheable {
		t.Fatalf("built-in 503 must not claim snapshot identity or be cached: %+v", document)
	}

	response, err := http.Get(env.prodServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "Сайт ещё не настроен") {
		t.Fatalf("public HTTP branch response must serve built-in 503: status=%d body=%s", response.StatusCode, body)
	}
}

func TestSystemPage500OverrideAndFallbackWhenOverrideIsBroken(t *testing.T) {
	env := newTestEnv(t)
	home := uploadRequiredQuerySystemPageBuild(t, env, "broken-home")
	uploadSystemPageBuild(t, env, "server-error")
	errorBuild := sitebuilder.BuildRef{FeatureKey: "server-error", Version: "1.0.0"}
	cfg := simpleSystemPagesConfig(home)
	cfg.Pages[0].Placements = []sitebuilder.Placement{requiredQuerySystemPagePlacement("home-placement", home)}
	cfg.Features = append(cfg.Features, errorBuild)
	cfg.Pages = append(cfg.Pages, sitebuilder.Page{ID: "server-error", Name: "Server error", Kind: "page", Placements: []sitebuilder.Placement{{ID: "server-error-placement", Build: errorBuild, Slot: "main"}}})
	cfg.RouteTree.Children = append(cfg.RouteTree.Children, sitebuilder.RouteTreeNode{ID: "server-error-route", Type: "path", Path: "/server-error", PageID: "server-error"})
	cfg.SystemPages = map[string]string{"500": "server-error"}
	snapshot := createAssignedSystemPagesSnapshot(t, env, cfg)

	composition, status, err := env.snapshots.PublicRuntime(context.Background(), env.devBranch, "/", nil)
	if err != nil || status != http.StatusInternalServerError || composition.Page.ID != "server-error" {
		t.Fatalf("valid 500 override was not selected: status=%d page=%q err=%v", status, composition.Page.ID, err)
	}
	document, err := env.snapshots.PageDocument(context.Background(), env.devBranch, "/", nil, false)
	if err != nil || document.Status != http.StatusInternalServerError || document.SnapshotID != snapshot.ID.String() || document.CacheKey == "" {
		t.Fatalf("valid 500 override should retain snapshot identity and status: document=%+v err=%v", document, err)
	}

	brokenErrorBuild := uploadRequiredQuerySystemPageBuild(t, env, "server-error-broken")
	brokenConfig := simpleSystemPagesConfig(home)
	brokenConfig.Pages[0].Placements = []sitebuilder.Placement{requiredQuerySystemPagePlacement("home-placement", home)}
	brokenConfig.Features = append(brokenConfig.Features, brokenErrorBuild)
	brokenConfig.Pages = append(brokenConfig.Pages, sitebuilder.Page{ID: "server-error", Name: "Server error", Kind: "page", Placements: []sitebuilder.Placement{requiredQuerySystemPagePlacement("server-error-broken-placement", brokenErrorBuild)}})
	brokenConfig.RouteTree.Children = append(brokenConfig.RouteTree.Children, sitebuilder.RouteTreeNode{ID: "server-error-route", Type: "path", Path: "/server-error", PageID: "server-error"})
	brokenConfig.SystemPages = map[string]string{"500": "server-error"}
	createAssignedSystemPagesSnapshot(t, env, brokenConfig)
	composition, status, err = env.snapshots.PublicRuntime(context.Background(), env.devBranch, "/", nil)
	if err == nil || status != http.StatusInternalServerError || composition.Page.ID != "" {
		t.Fatalf("broken 500 override should return the original error for core fallback: composition=%+v status=%d err=%v", composition, status, err)
	}
	document, err = env.snapshots.PageDocument(context.Background(), env.devBranch, "/", nil, false)
	if err != nil || document.Status != http.StatusInternalServerError {
		t.Fatalf("broken override should preserve the 500 status: status=%d err=%v", document.Status, err)
	}
	if !strings.Contains(string(document.HTML), "Сайт временно недоступен") || !strings.Contains(string(document.HTML), "noindex,nofollow") {
		t.Fatalf("broken override did not produce the safe core 500: %s", document.HTML)
	}
	if document.SnapshotID != "" || document.CacheKey != "" {
		t.Fatalf("core fallback should not claim a snapshot render: %+v", document)
	}
}

func TestConfiguredSystemPagesAreExcludedFromSitemapWithoutNoindexOverride(t *testing.T) {
	env := newTestEnv(t)
	uploadSystemPageBuild(t, env, "sitemap-home")
	home := sitebuilder.BuildRef{FeatureKey: "sitemap-home", Version: "1.0.0"}
	notFound := sitebuilder.BuildRef{FeatureKey: "sitemap-not-found", Version: "1.0.0"}
	uploadSystemPageBuild(t, env, notFound.FeatureKey)

	cfg := simpleSystemPagesConfig(home)
	cfg.Features = append(cfg.Features, notFound)
	cfg.Pages = append(cfg.Pages, sitebuilder.Page{
		ID: "not-found", Name: "Not found", Kind: "page",
		Placements: []sitebuilder.Placement{{ID: "not-found-placement", Build: notFound, Slot: "main"}},
	})
	cfg.RouteTree.Children = append(cfg.RouteTree.Children, sitebuilder.RouteTreeNode{
		ID: "not-found-route", Type: "path", Path: "/not-found", PageID: "not-found",
	})
	cfg.SystemPages = map[string]string{"404": "not-found"}
	metadata := sitebuilder.DefaultSiteMetadata()
	metadata.CanonicalDomain = "https://system-pages.example"
	actor := testActor(t, env)
	snapshot, err := env.snapshots.CreateSnapshot(context.Background(), actor, sitebuilder.CreateSnapshotInput{
		Name: "System pages do not appear in sitemap", Config: &cfg, Metadata: &metadata,
	})
	if err != nil {
		t.Fatalf("create system page sitemap snapshot: %v", err)
	}
	branch, err := env.snapshots.Branch(context.Background(), uuid.MustParse(env.devBranch.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.snapshots.AssignBranch(context.Background(), actor, branch.ID, snapshot.ID, branch.Revision); err != nil {
		t.Fatalf("assign system page sitemap snapshot: %v", err)
	}

	index, err := env.snapshots.PageIndex(context.Background(), env.devBranch, true)
	if err != nil {
		t.Fatalf("build page index: %v", err)
	}
	if len(index.URLs) != 1 || index.URLs[0] != "https://system-pages.example/" {
		t.Fatalf("page index should include home but exclude the configured 404 page even without noindex metadata: %+v", index)
	}

	// The default integration branch is non-indexable. Enable indexing only in
	// this in-memory router dependency so the HTTP sitemap exercises the index.
	env.routerDeps.BranchIndexable[env.devBranch] = true
	sitemapServer := httptest.NewServer(httptransport.NewPublicRouter(env.routerDeps, env.devBranch))
	defer sitemapServer.Close()
	response, err := http.Get(sitemapServer.URL + "/sitemap.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "public, max-age=0, must-revalidate" || response.Header.Get("ETag") == "" {
		t.Fatalf("indexable sitemap should be publicly revalidatable with an ETag: status=%d headers=%v", response.StatusCode, response.Header)
	}
	if !strings.Contains(string(body), "https://system-pages.example/") || strings.Contains(string(body), "https://system-pages.example/not-found") {
		t.Fatalf("sitemap must retain the homepage and omit configured system pages: %s", body)
	}
	conditional, err := http.NewRequest(http.MethodGet, sitemapServer.URL+"/sitemap.xml", nil)
	if err != nil {
		t.Fatal(err)
	}
	conditional.Header.Set("If-None-Match", response.Header.Get("ETag"))
	notModified, err := http.DefaultClient.Do(conditional)
	if err != nil {
		t.Fatal(err)
	}
	defer notModified.Body.Close()
	if notModified.StatusCode != http.StatusNotModified || notModified.Header.Get("ETag") != response.Header.Get("ETag") {
		t.Fatalf("unchanged sitemap should return 304 with the same ETag: status=%d headers=%v", notModified.StatusCode, notModified.Header)
	}
}
