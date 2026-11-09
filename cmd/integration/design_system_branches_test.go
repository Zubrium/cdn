package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
)

// Structural design changes must be isolated from active snapshots while
// preserving feature-scoped, typed tokens and per-width overrides.
func TestBranchDesignTokensRemainSnapshotScoped(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, env)
	yes := true
	branches, err := env.snapshots.RegisterBranches(ctx, []config.BranchConfig{
		{Name: "Site", HTTPAddr: ":39971", CORSOrigins: []string{}, Indexable: &yes},
		{Name: "Preview", HTTPAddr: ":39972", CORSOrigins: []string{}, Indexable: &yes},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := testFeatureManifestFor("design-hero", "1.0.0")
	manifest["designTokens"] = []any{map[string]any{"key": "text-color", "label": "Text color", "group": "Colors", "kind": "color", "default": map[string]any{"color": map[string]any{"hex": "#000000", "opacity": 100}}}}
	uploadFeatureBuild(t, env, manifest, "export default function Feature() {}")
	other := testFeatureManifestFor("design-other", "1.0.0")
	other["designTokens"] = []any{map[string]any{"key": "spacing", "label": "Spacing", "group": "Size", "kind": "length", "default": map[string]any{"number": 8, "unit": "px"}}}
	uploadFeatureBuild(t, env, other, "export default function Feature() {}")
	raw, _ := json.Marshal(singlePageConfig("design-hero", "1.0.0"))
	cfg, err := sitebuilder.ParseSiteConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Parent", Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	for i, branch := range branches {
		branches[i], err = env.snapshots.AssignBranch(ctx, actor, branch.ID, parent.ID, branch.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	fontID, logoID := uuid.New(), uuid.New()
	for _, asset := range []struct {
		id                     uuid.UUID
		name, mime, visibility string
	}{{fontID, "face.woff2", "font/woff2", "public"}, {logoID, "logo.svg", "image/svg+xml", "public"}} {
		if _, err := env.pool.Exec(ctx, `INSERT INTO media_objects(id,object_key,original_filename,content_type,size_bytes,status,variants,visibility) VALUES($1,$2,$3,$4,1,'ready','{}',$5)`, asset.id, "qa/"+asset.id.String(), asset.name, asset.mime, asset.visibility); err != nil {
			t.Fatal(err)
		}
	}
	groupID, tokenID := uuid.NewString(), uuid.NewString()
	theme := sitebuilder.NewDesignSystem()
	theme.Groups = []sitebuilder.TokenGroup{{ID: groupID, Name: "Brand"}}
	theme.BaseTokens = []sitebuilder.BaseToken{{ID: tokenID, Name: "Color", GroupID: groupID, Kind: "color", Value: sitebuilder.TokenValue{Color: &sitebuilder.TokenColor{Hex: "#123456", Opacity: 80}}}}
	theme.FeatureValues = map[string]sitebuilder.TokenValue{"design-hero/text-color": {BaseTokenID: tokenID}}
	theme.Breakpoints = []sitebuilder.TokenBreakpoint{
		{ID: uuid.NewString(), Name: "Tablet", MinWidthPx: 600, BaseValues: map[string]sitebuilder.TokenValue{tokenID: {Color: &sitebuilder.TokenColor{Hex: "#445566", Opacity: 100}}}, FeatureValues: map[string]sitebuilder.TokenValue{}},
		{ID: uuid.NewString(), Name: "Desktop", MinWidthPx: 1024, BaseValues: map[string]sitebuilder.TokenValue{tokenID: {Color: &sitebuilder.TokenColor{Hex: "#abcdef", Opacity: 100}}}, FeatureValues: map[string]sitebuilder.TokenValue{}},
		{ID: uuid.NewString(), Name: "Wide", MinWidthPx: 1440, BaseValues: map[string]sitebuilder.TokenValue{tokenID: {Color: &sitebuilder.TokenColor{Hex: "#0a0b0c", Opacity: 100}}}, FeatureValues: map[string]sitebuilder.TokenValue{}},
	}
	theme.BrandLogoID = logoID.String()
	theme.Fonts = []sitebuilder.DesignFont{{Family: "Editorial", MediaID: fontID.String(), Weight: "400", Style: "normal"}}
	child, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Child", ParentID: &parent.ID, DesignSystem: &theme})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := env.snapshots.SnapshotDesignWorkspace(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.FeatureTokens) != 1 || workspace.FeatureTokens[0].FeatureKey != "design-hero" {
		t.Fatalf("unselected feature visible: %+v", workspace.FeatureTokens)
	}
	css, err := env.snapshots.SnapshotThemeStylesheet(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--feature-design-hero-text-color:var(--base-" + tokenID + ")", "min-width:600px", "min-width:1024px", "min-width:1440px", "171 205 239", fontID.String()} {
		if !strings.Contains(css, want) {
			t.Fatalf("missing %q in %s", want, css)
		}
	}
	parentCSS, err := env.snapshots.SnapshotThemeStylesheet(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(parentCSS, tokenID) || strings.Contains(parentCSS, "min-width:1024px") {
		t.Fatal("child changed parent design")
	}
	for _, branch := range branches {
		current, err := env.snapshots.Branch(ctx, branch.ID)
		if err != nil || current.SnapshotID == nil || *current.SnapshotID != parent.ID {
			t.Fatalf("branch assignment changed: %+v %v", current, err)
		}
	}
	t.Run("public browser computes responsive base-linked tokens", func(t *testing.T) {
		preview, err := env.snapshots.Branch(ctx, branches[1].ID)
		if err != nil {
			t.Fatal(err)
		}
		preview, err = env.snapshots.AssignBranch(ctx, actor, preview.ID, child.ID, preview.Revision)
		if err != nil {
			t.Fatalf("explicitly assign the preview branch to the child snapshot: %v", err)
		}
		assertResponsiveDesignTokensInBrowser(t, env, preview, tokenID, "--feature-design-hero-text-color", []designTokenBrowserCase{
			{Width: 500, Color: "rgba(18, 52, 86, 0.8)"},
			{Width: 800, Color: "rgb(68, 85, 102)"},
			{Width: 1280, Color: "rgb(171, 205, 239)"},
			{Width: 1600, Color: "rgb(10, 11, 12)"},
		})
	})

	// Feature catalog is driven by the snapshot, even for an unplaced feature.
	cfg.Features = []sitebuilder.BuildRef{{FeatureKey: "design-hero", Version: "1.0.0"}, {FeatureKey: "design-other", Version: "1.0.0"}}
	extended, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "With additional feature", ParentID: &child.ID, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = env.snapshots.SnapshotDesignWorkspace(ctx, extended.ID)
	if err != nil || len(workspace.FeatureTokens) != 2 {
		t.Fatalf("added but unplaced feature missing: %+v %v", workspace, err)
	}

	// Removing a selected feature hides its tokens, but a later compatible
	// version can restore its previous values without touching the parent.
	withoutHero := cfg
	withoutHero.Features = []sitebuilder.BuildRef{{FeatureKey: "design-other", Version: "1.0.0"}}
	withoutHero.Pages = append([]sitebuilder.Page(nil), cfg.Pages...)
	withoutHero.Pages[0].Placements = nil
	removed, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Removed feature", ParentID: &extended.ID, Config: &withoutHero})
	if err != nil || len(removed.Warnings) == 0 {
		t.Fatalf("feature removal should preserve values with a warning: %+v %v", removed, err)
	}
	workspace, err = env.snapshots.SnapshotDesignWorkspace(ctx, removed.ID)
	if err != nil || len(workspace.FeatureTokens) != 1 || workspace.FeatureTokens[0].FeatureKey != "design-other" || len(workspace.DesignSystem.LegacyFeatureValues) != 1 {
		t.Fatalf("removed feature should be hidden with its value retained: %+v %v", workspace, err)
	}
	withHero := withoutHero
	withHero.Features = append(append([]sitebuilder.BuildRef(nil), withoutHero.Features...), sitebuilder.BuildRef{FeatureKey: "design-hero", Version: "1.0.0"})
	restored, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Restored feature", ParentID: &removed.ID, Config: &withHero})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = env.snapshots.SnapshotDesignWorkspace(ctx, restored.ID)
	if err != nil || len(workspace.DesignSystem.LegacyFeatureValues) != 0 || workspace.DesignSystem.FeatureValues["design-hero/text-color"].BaseTokenID != tokenID {
		t.Fatalf("compatible feature value was not restored: %+v %v", workspace, err)
	}

	if _, err := env.pool.Exec(ctx, `UPDATE media_objects SET visibility='private' WHERE id=$1`, logoID); err != nil {
		t.Fatal(err)
	}
	status, body, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots/"+child.ID.String()+"/design-system", env.adminToken, map[string]any{"name": "private logo", "designSystem": theme})
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(map[string]any)["message"].(string), "must be public") {
		t.Fatalf("private logo accepted: %d %+v", status, body)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE media_objects SET visibility='public' WHERE id=$1`, logoID); err != nil {
		t.Fatal(err)
	}
	theme.Fonts[0].MediaID = logoID.String()
	status, body, _ = env.request(http.MethodPost, "/api/v2/admin/site/snapshots/"+child.ID.String()+"/design-system", env.adminToken, map[string]any{"name": "image as font", "designSystem": theme})
	if status != http.StatusBadRequest {
		t.Fatalf("image accepted as font because logo shared ID: %d %+v", status, body)
	}
}

type designTokenBrowserCase struct {
	Width int    `json:"width"`
	Color string `json:"color"`
}

func assertResponsiveDesignTokensInBrowser(t *testing.T, env *testEnv, branch sitebuilder.BranchRecord, baseTokenID, featureTokenCSSName string, expected []designTokenBrowserCase) {
	t.Helper()
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		t.Skip("set PRERENDER_TEST_NODE/MODULE/BROWSER for browser-computed design token acceptance")
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	server := seoPublicServer(t, env, branch, nil)
	script := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({ viewport: { width: 500, height: 900 } });
  await page.goto(process.argv[1], { waitUntil: "domcontentloaded" });
  const base = "--base-" + process.env.BASE_TOKEN_ID;
  const feature = process.env.FEATURE_TOKEN_CSS_NAME;
  await page.waitForFunction(({ base, feature }) => {
    const style = getComputedStyle(document.documentElement);
    return Boolean(style.getPropertyValue(base).trim() && style.getPropertyValue(feature).trim());
  }, { base, feature }, { timeout: 10000 });
  const expected = JSON.parse(process.env.EXPECTED_COLORS);
  for (const { width, color } of expected) {
    await page.setViewportSize({ width, height: 900 });
    const result = await page.evaluate(({ base, feature }) => {
      const root = getComputedStyle(document.documentElement);
      const probe = document.createElement("span");
      probe.style.color = "var(" + feature + ")";
      document.body.append(probe);
      return { baseValue: root.getPropertyValue(base).trim(), featureValue: root.getPropertyValue(feature).trim(), color: getComputedStyle(probe).color };
    }, { base, feature });
    if (!result.baseValue || result.featureValue !== result.baseValue || result.color !== color) {
      throw new Error("width " + width + "px: expected " + color + " through a base link, got " + JSON.stringify(result));
    }
  }
  console.log("verified responsive base-linked color cascade at " + expected.length + " viewport widths");
} finally { await browser.close(); }`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, server.URL+"/")
	command.Env = append(os.Environ(), "PLAYWRIGHT_TEST_MODULE="+module, "PLAYWRIGHT_TEST_BROWSER="+browser, "BASE_TOKEN_ID="+baseTokenID, "FEATURE_TOKEN_CSS_NAME="+featureTokenCSSName, "EXPECTED_COLORS="+string(expectedJSON))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("responsive design token browser check failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
