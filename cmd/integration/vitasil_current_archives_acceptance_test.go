package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"websiteengine/features/sitebuilder"
	publicruntime "websiteengine/pkg/runtime"
)

// This browser acceptance covers the current Vitasil header and hero ZIPs. The
// older disposable Vitasil fixture is intentionally not used: the test loads
// both immutable archives directly from TEST_FEATURE_DIR.
func TestVitasilCurrentHeaderAndHeroResponsiveRuntime(t *testing.T) {
	featureDir := os.Getenv("TEST_FEATURE_DIR")
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if featureDir == "" || node == "" || module == "" || browser == "" {
		t.Skip("set TEST_FEATURE_DIR and PRERENDER_TEST_NODE/MODULE/BROWSER for current Vitasil responsive acceptance")
	}
	requireUniqueVitasilArchivesDatabase(t)

	env := newTestEnv(t)
	manifests := map[string]sitebuilder.Manifest{}
	versions := map[string]string{}
	for _, key := range []string{"header", "hero"} {
		archive, version := readExternalFeatureArchive(t, featureDir, key)
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify current Vitasil %s archive: %v", key, err)
		}
		if verified.Manifest.Key != key || verified.Manifest.Version != version || verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
			t.Fatalf("current Vitasil %s archive has unexpected identity or ABI: key=%q version=%q ABI=%q", key, verified.Manifest.Key, verified.Manifest.Version, verified.Manifest.RuntimeABI())
		}
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register current Vitasil %s archive: status=%d body=%v", key, status, body)
		}
		if body["key"] != key || body["version"] != version {
			t.Fatalf("Engine registered a different Vitasil build: key=%s version=%s response=%v", key, version, body)
		}
		manifests[key] = verified.Manifest
		versions[key] = version
	}
	t.Logf("responsive acceptance uses latest available Vitasil ZIPs header@%s and hero@%s", versions["header"], versions["hero"])
	headerPort := modelAcceptancePort(t, manifests["header"], "header")
	navigationPort := modelAcceptancePort(t, manifests["header"], "navigation")
	heroPort := modelAcceptancePort(t, manifests["hero"], "hero")
	model, headerEntity, headerFieldIDs, headerFieldKeys := modelAcceptanceEntity("site_header", "Site header", headerPort, false)
	_, navigationEntity, navigationFieldIDs, navigationFieldKeys := modelAcceptanceEntity("navigation_link", "Navigation link", navigationPort, false)
	_, heroEntity, heroFieldIDs, heroFieldKeys := modelAcceptanceEntity("hero_content", "Hero content", heroPort, false)
	model.Entities = append(model.Entities, navigationEntity, heroEntity)
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		DataModel:     &model,
		Pages: []sitebuilder.Page{{ID: "home", Name: "Home", Kind: "page", Placements: []sitebuilder.Placement{
			{ID: "vitasil-header", Build: sitebuilder.BuildRef{FeatureKey: "header", Version: versions["header"]}, Slot: "header", DataBindings: []sitebuilder.DataInputBinding{
				{InputKey: headerPort.Key, EntityID: headerEntity.ID, Fields: modelAcceptanceFieldBindings(headerPort, headerFieldIDs)},
				{InputKey: navigationPort.Key, EntityID: navigationEntity.ID, Fields: modelAcceptanceFieldBindings(navigationPort, navigationFieldIDs)},
			}},
			{ID: "vitasil-hero", Build: sitebuilder.BuildRef{FeatureKey: "hero", Version: versions["hero"]}, Slot: "main", DataBindings: []sitebuilder.DataInputBinding{
				{InputKey: heroPort.Key, EntityID: heroEntity.ID, Fields: modelAcceptanceFieldBindings(heroPort, heroFieldIDs)},
			}},
		}}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "root", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home"}}},
	}
	configRaw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(configRaw, &configMap); err != nil {
		t.Fatal(err)
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Vitasil current responsive runtime")
	snapshotID, err := uuid.Parse(snapshot["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	// The current Vitasil header and hero are both populated from snapshot
	// model records; only their public route and responsive runtime are under test.
	content, err := env.snapshots.SaveModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, sitebuilder.ModelContentDraftInput{
		Revision: 0,
		Records: []sitebuilder.ModelContentRecord{
			{EntityID: headerEntity.ID, Values: modelAcceptanceValues(headerFieldKeys, map[string]any{
				"/brand": "Vitasil", "/brandTo": "/", "/navigationLabel": "Основная навигация",
			})},
			{EntityID: navigationEntity.ID, SortOrder: 0, Values: modelAcceptanceValues(navigationFieldKeys, map[string]any{"/label": "Каталог", "/to": "/catalog"})},
			{EntityID: heroEntity.ID, Values: modelAcceptanceValues(heroFieldKeys, map[string]any{
				"/title": "ИССИТ", "/subtitle": "Проверенные продукты из натуральных ингредиентов",
				"/actionLabel": "Открыть каталог", "/actionTo": "/catalog",
			})},
		},
	})
	if err != nil {
		t.Fatalf("save current Vitasil model data: %v", err)
	}
	if len(content.Records) != 3 {
		t.Fatalf("initial model draft should return three generated record IDs: %+v", content.Records)
	}
	recordIDs := make(map[string]string, len(content.Records))
	for _, record := range content.Records {
		recordIDs[record.EntityID] = record.ID
	}
	for _, entityID := range []string{headerEntity.ID, navigationEntity.ID, heroEntity.ID} {
		if recordIDs[entityID] == "" {
			t.Fatalf("initial model draft did not generate an ID for entity %s: %+v", entityID, content.Records)
		}
	}
	selected, err := env.snapshots.SaveModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, sitebuilder.ModelContentDraftInput{
		Revision: content.Revision,
		Records:  content.Records,
		Selections: []sitebuilder.ModelContentSelection{
			{PlacementID: "vitasil-header", InputKey: headerPort.Key, Mode: "ids", RecordIDs: []string{recordIDs[headerEntity.ID]}},
			{PlacementID: "vitasil-header", InputKey: navigationPort.Key, Mode: "ids", RecordIDs: []string{recordIDs[navigationEntity.ID]}},
			{PlacementID: "vitasil-hero", InputKey: heroPort.Key, Mode: "ids", RecordIDs: []string{recordIDs[heroEntity.ID]}},
		},
	})
	if err != nil {
		t.Fatalf("select current Vitasil header, navigation and hero records for their placements: %v", err)
	}
	if _, err := env.snapshots.PublishModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, selected.Revision); err != nil {
		t.Fatalf("publish current Vitasil model data: %v", err)
	}

	branchID, err := uuid.Parse(string(env.devBranch))
	if err != nil {
		t.Fatal(err)
	}
	workerPath, err := filepath.Abs("../../scripts/prerender-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	options := publicruntime.PrerenderOptions{
		NodePath: node, WorkerPath: workerPath, PlaywrightModule: module, BrowserPath: browser,
		Timeout: 30 * time.Second, AttemptTimeout: 15 * time.Second, MaxAttempts: 1,
		QuietInterval: 100 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1,
		QueueSize: 2, CacheEntries: 4, MaxHTMLBytes: 1 << 20, HeapMB: 128,
		ViewportWidth: 1280, ViewportHeight: 900, MaxRequests: 100, MaxNetworkBytes: 16 << 20,
	}
	server, worker := seoPublicServerWithWorker(t, env, sitebuilder.BranchRecord{ID: branchID, Indexable: true}, &options)
	worker.SetLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	status, html, headers := seoGet(t, server, "/")
	if status != http.StatusOK || headers.Get("X-Engine-Prerender") != "hit" || !strings.Contains(html, "ИССИТ") || !strings.Contains(html, "Vitasil") {
		t.Fatalf("current Vitasil header/hero did not prerender: status=%d headers=%v html=%s", status, headers, html)
	}
	if !strings.Contains(html, `id="engine-runtime-bootstrap"`) {
		t.Fatal("prerendered document omitted the inert runtime bootstrap required by the interactive browser mount")
	}
	if strings.Count(html, "<main") != 1 || strings.Count(html, "<h1") != 1 {
		t.Fatalf("current Vitasil homepage must prerender exactly one page-level main and H1: %s", html)
	}

	browserScript := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
const url = process.argv[1];
try {
  for (const [width, height] of [[360, 800], [390, 844], [1280, 900]]) {
    const page = await browser.newPage({ viewport: { width, height } });
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
    await page.goto(url, { waitUntil: "domcontentloaded" });
    try {
      await page.waitForFunction(() => document.documentElement.dataset.engineReady === "true");
    } catch (error) {
      const state = await page.evaluate(() => ({
        engineReady: document.documentElement.dataset.engineReady,
        rootHidden: document.getElementById("root")?.hidden,
        prerenderRoot: Boolean(document.getElementById("prerender-root")),
        placements: [...document.querySelectorAll("[data-engine-placement]")].map((item) => ({
          slot: item.getAttribute("data-engine-slot"),
          ready: item.hasAttribute("data-runtime-feature-ready"),
          failed: item.hasAttribute("data-runtime-feature-error"),
        })),
      }));
      console.log(JSON.stringify({ width, state, errors }));
      throw error;
    }
    const metrics = await page.evaluate(() => {
      const header = document.querySelector('[data-engine-feature="header"] header');
      const hero = document.querySelector('[data-engine-feature="hero"] section');
      const heading = hero?.querySelector("h1");
      const action = hero?.querySelector("a");
      if (!header || !hero || !heading || !action) return { error: "missing header/hero elements" };
      const box = element => {
        const rect = element.getBoundingClientRect();
        return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom };
      };
      return {
        viewport: innerWidth,
        viewportHeight: innerHeight,
        documentWidth: document.documentElement.scrollWidth,
        headerPosition: getComputedStyle(header).position,
        header: box(header), hero: box(hero), heading: box(heading), action: box(action),
        headingClientWidth: heading.clientWidth,
        headingScrollWidth: heading.scrollWidth,
        headingText: heading.textContent?.trim(),
        mainCount: document.querySelectorAll("main").length,
        h1Count: document.querySelectorAll("h1").length,
        navigationLabel: document.querySelector("nav")?.getAttribute("aria-label"),
        actionLabel: action.textContent?.trim(),
      };
    });
    if (metrics.error) throw new Error(width + "px: " + metrics.error);
    if (metrics.documentWidth > width + 1) throw new Error(width + "px: horizontal overflow " + JSON.stringify(metrics));
    if (metrics.headerPosition !== "fixed") throw new Error(width + "px: header is not fixed over the hero as designed " + JSON.stringify(metrics));
    if (metrics.hero.top !== 0) throw new Error(width + "px: hero does not begin behind the fixed header " + JSON.stringify(metrics));
    if (metrics.heading.top < metrics.header.bottom - 1) throw new Error(width + "px: hero heading is obscured by the fixed header " + JSON.stringify(metrics));
    if (metrics.heading.left < -1 || metrics.heading.right > width + 1 || metrics.headingScrollWidth > metrics.headingClientWidth + 1) throw new Error(width + "px: heading is clipped or overflows " + JSON.stringify(metrics));
    if (metrics.headingText !== "ИССИТ") throw new Error(width + "px: the current Vitasil hero title was not rendered " + JSON.stringify(metrics));
    if (metrics.action.top < metrics.header.bottom - 1) throw new Error(width + "px: CTA overlaps the header " + JSON.stringify(metrics));
    if (metrics.action.top < -1 || metrics.action.bottom > height + 1) throw new Error(width + "px: CTA is not fully visible in the initial viewport " + JSON.stringify(metrics));
    if (metrics.mainCount !== 1 || metrics.h1Count !== 1 || metrics.navigationLabel !== "Основная навигация" || metrics.actionLabel !== "Открыть каталог") throw new Error(width + "px: current runtime lost accessible landmarks/labels " + JSON.stringify(metrics));
    await page.keyboard.press("Tab");
    const focusVisible = await page.evaluate(() => document.activeElement?.matches(":focus-visible") === true);
    if (!focusVisible) throw new Error(width + "px: first keyboard focus is not visibly indicated");
    if (errors.length) throw new Error(width + "px: runtime errors: " + errors.join(" | "));
    console.log(JSON.stringify(metrics));
    await page.close();
  }
  const noJs = await browser.newPage({ viewport: { width: 390, height: 844 }, javaScriptEnabled: false });
  await noJs.goto(url, { waitUntil: "domcontentloaded" });
  if (await noJs.getByRole("heading", { level: 1, name: "ИССИТ" }).count() !== 1) throw new Error("no-JS page omitted the current Vitasil hero heading");
  const noJsAction = noJs.getByRole("link", { name: "Открыть каталог" });
  if (await noJsAction.count() !== 1) {
    const state = await noJs.evaluate(() => ({
      hero: document.querySelector('[data-engine-feature="hero"]')?.innerHTML || "",
      root: document.getElementById("root")?.innerHTML.slice(0, 12000) || "",
    }));
    throw new Error("no-JS page omitted the CTA link: " + JSON.stringify(state));
  }
  await noJsAction.waitFor({ state: "visible" });
  const noJsActionBox = await noJsAction.boundingBox();
  if (!noJsActionBox || noJsActionBox.y < 0 || noJsActionBox.y + noJsActionBox.height > 845 || await noJsAction.getAttribute("href") !== "/catalog") throw new Error("no-JS CTA is not fully visible or usable: " + JSON.stringify(noJsActionBox));
  if (await noJs.getByRole("navigation", { name: "Основная навигация" }).count() !== 1) throw new Error("no-JS page omitted the labeled navigation");
  const noJsStructure = await noJs.evaluate(() => {
    const root = document.getElementById("root");
    const main = root?.querySelector(":scope > main");
    const placements = [...document.querySelectorAll("[data-engine-placement]")];
    return {
      rootCount: document.querySelectorAll("#root").length,
      rootHasSnapshot: Boolean(root?.hasAttribute("data-snapshot")),
      duplicateMountRootCount: document.querySelectorAll("#engine-live-root, #prerender-root").length,
      mainCount: document.querySelectorAll("main").length,
      placementsUnderRoot: placements.every((placement) => placement.parentElement === (placement.getAttribute("data-engine-slot") === "main" ? main : root)),
    };
  });
  if (noJsStructure.rootCount !== 1 || !noJsStructure.rootHasSnapshot || noJsStructure.duplicateMountRootCount !== 0 || noJsStructure.mainCount !== 1 || !noJsStructure.placementsUnderRoot) {
    throw new Error("no-JS response does not preserve the populated canonical #root: " + JSON.stringify(noJsStructure));
  }
  await noJs.close();
} finally { await browser.close(); }`
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", browserScript, server.URL+"/")
	command.Env = append(os.Environ(), "PLAYWRIGHT_TEST_MODULE="+module, "PLAYWRIGHT_TEST_BROWSER="+browser)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("current Vitasil responsive/no-JS acceptance failed: %v\n%s", err, output)
	}
	t.Logf("current Vitasil responsive/no-JS metrics:\n%s", output)
}

func requireUniqueVitasilArchivesDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	if raw == "" || err != nil {
		t.Skip("set TEST_DATABASE_URL to a uniquely named disposable website_engine_vitasil_archive_qa_* database; integration setup truncates it")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(databaseName, "website_engine_vitasil_archive_qa_") ||
		(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Skip("Vitasil current-archive acceptance requires a local website_engine_vitasil_archive_qa_* disposable database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to disposable Vitasil current-archive database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var sessions int
	if err := connection.QueryRow(ctx, `SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&currentDatabase, &sessions); err != nil {
		t.Fatalf("verify disposable Vitasil database ownership and sessions: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("TEST_DATABASE_URL database %q does not match its URL name %q", currentDatabase, databaseName)
	}
	if sessions != 0 {
		t.Fatalf("refusing newTestEnv TRUNCATE: disposable Vitasil database %q already has %d other sessions", currentDatabase, sessions)
	}
	t.Logf("verified disposable local Vitasil database %s has no other sessions before integration setup", currentDatabase)
}
