package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
	publicruntime "websiteengine/pkg/runtime"
)

// TestVitasilLegalDocumentsBrowserAcceptance exercises the optional current
// legal-documents feature ZIP in an isolated model-backed snapshot. It is kept
// separate from the core all-placement test so a newly added optional feature
// cannot silently escape its own route, singleton, prerender, and browser checks.
func TestVitasilLegalDocumentsBrowserAcceptance(t *testing.T) {
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	node := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	module := strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browser := strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	if featureDir == "" || node == "" || module == "" || browser == "" {
		t.Skip("set TEST_FEATURE_DIR and PRERENDER_TEST_NODE/MODULE/BROWSER for legal-documents browser acceptance")
	}
	requireDisposableIntegrationDatabase(t, os.Getenv("TEST_DATABASE_URL"))

	env := newTestEnv(t)
	archive, version := readExternalFeatureArchive(t, featureDir, "legal-documents")
	verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
	if err != nil {
		t.Fatalf("verify current Vitasil legal-documents ZIP: %v", err)
	}
	manifest := verified.Manifest
	if manifest.Key != "legal-documents" || manifest.Version != version || manifest.FormatVersion != 2 || manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
		t.Fatalf("legal-documents ZIP does not satisfy format-2 static-module contract: key=%q version=%q format=%d ABI=%q", manifest.Key, manifest.Version, manifest.FormatVersion, manifest.RuntimeABI())
	}
	if len(manifest.DataInputs) != 1 || manifest.DataInputs[0].Key != "legalSettings" || manifest.DataInputs[0].Kind != "record" {
		t.Fatalf("legal-documents manifest must declare the legalSettings record input: %+v", manifest.DataInputs)
	}
	status, response, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, strings.NewReader(string(archive)), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register current legal-documents@%s ZIP: status=%d body=%v", version, status, response)
	}
	if response["key"] != "legal-documents" || response["version"] != version {
		t.Fatalf("registered feature identity differs from ZIP manifest: %v", response)
	}

	port := modelAcceptancePort(t, manifest, "legalSettings")
	model, entity, fieldIDs, fieldKeys := modelAcceptanceEntity("legal_settings", "LegalSettings", port, false)
	entity.Cardinality = sitebuilder.DataModelEntityCardinalitySingleton
	model.Entities[0] = entity
	placement := func(id, view string, slot string) sitebuilder.Placement {
		return sitebuilder.Placement{
			ID: id, Build: sitebuilder.BuildRef{FeatureKey: manifest.Key, Version: manifest.Version}, Slot: slot,
			Arguments: map[string]sitebuilder.ArgumentBinding{
				"view":        {Source: "literal", Value: json.RawMessage(fmt.Sprintf("%q", view))},
				"privacyPath": {Source: "literal", Value: json.RawMessage(`"/privacy"`)},
				"consentPath": {Source: "literal", Value: json.RawMessage(`"/personal-data-consent"`)},
				"cookiesPath": {Source: "literal", Value: json.RawMessage(`"/cookies"`)},
			},
			DataBindings: []sitebuilder.DataInputBinding{{InputKey: port.Key, EntityID: entity.ID, Fields: modelAcceptanceFieldBindings(port, fieldIDs)}},
		}
	}
	footer := func(id string) sitebuilder.Placement { return placement(id, "footer", "footer") }
	main := func(id, view string) sitebuilder.Placement { return placement(id, view, "main") }
	pages := []sitebuilder.Page{
		{ID: "home", Name: "Legal QA home", Kind: "page", Placements: []sitebuilder.Placement{footer("legal-footer-home")}},
		{ID: "privacy", Name: "Privacy", Kind: "page", Placements: []sitebuilder.Placement{main("legal-privacy", "privacy"), footer("legal-footer-privacy")}},
		{ID: "consent", Name: "Consent", Kind: "page", Placements: []sitebuilder.Placement{main("legal-consent", "consent"), footer("legal-footer-consent")}},
		{ID: "cookies", Name: "Cookies", Kind: "page", Placements: []sitebuilder.Placement{main("legal-cookies", "cookies"), footer("legal-footer-cookies")}},
	}
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Features:      []sitebuilder.BuildRef{{FeatureKey: manifest.Key, Version: manifest.Version}},
		DataModel:     &model,
		Pages:         pages,
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{
			{ID: "home-route", Type: "path", Path: "/", PageID: "home"},
			{ID: "privacy-route", Type: "path", Path: "/privacy", PageID: "privacy"},
			{ID: "consent-route", Type: "path", Path: "/personal-data-consent", PageID: "consent"},
			{ID: "cookies-route", Type: "path", Path: "/cookies", PageID: "cookies"},
		}},
	}
	configRaw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(configRaw, &configMap); err != nil {
		t.Fatal(err)
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Vitasil legal documents isolated acceptance")
	snapshotID, err := uuid.Parse(snapshot["id"].(string))
	if err != nil {
		t.Fatalf("parse isolated legal-documents snapshot ID: %v", err)
	}
	branchID := uuid.MustParse(string(env.devBranch))
	branchBefore, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil || branchBefore.SnapshotID == nil || branchBefore.SnapshotID.String() != snapshotID.String() {
		t.Fatalf("disposable test branch does not point to isolated legal-documents snapshot: branch=%+v err=%v", branchBefore, err)
	}

	modelContentPath := "/api/v2/admin/content/snapshots/" + snapshotID.String() + "/model"
	status, workspace, _ := env.request(http.MethodGet, modelContentPath, env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read new singleton LegalSettings workspace through admin API: status=%d body=%v", status, workspace)
	}
	var singletonID string
	workspaceRecords, ok := workspace["records"].([]any)
	if !ok {
		t.Fatalf("model-content API returned malformed records: %v", workspace)
	}
	for _, item := range workspaceRecords {
		record, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("model-content API returned malformed record: %T", item)
		}
		if record["entityId"] == entity.ID {
			if singletonID != "" {
				t.Fatalf("new singleton LegalSettings has duplicate draft records: %+v", workspaceRecords)
			}
			singletonID, _ = record["id"].(string)
		}
	}
	if singletonID == "" {
		t.Fatalf("snapshot creation did not auto-create the LegalSettings singleton draft record: %+v", workspace)
	}
	values := modelAcceptanceValues(fieldKeys, map[string]any{
		"/siteName":                 "Vitasil legal QA",
		"/operatorName":             "Vitasil QA Operator",
		"/operatorAddress":          "Москва, тестовый адрес",
		"/operatorInn":              "7700000000",
		"/operatorOgrn":             "1207700000000",
		"/privacyContact":           "privacy-qa@example.test",
		"/policyDate":               "2026-11-08",
		"/consentVersion":           "legal-qa-2026-11",
		"/cookiePreferencesVersion": "cookie-qa-2026-11",
		"/processors":               "Disposable QA only",
		"/retentionPeriod":          "Тестовый срок хранения",
		"/privacyPolicy":            "LEGAL PRIVACY DOCUMENT FROM SINGLETON QA 8F4C2",
		"/personalDataConsent":      "LEGAL CONSENT DOCUMENT FROM SINGLETON QA 1B9D7",
		"/cookieNotice":             "LEGAL COOKIE DOCUMENT FROM SINGLETON QA 6A3E1",
	})
	workspaceRevision, ok := workspace["revision"].(float64)
	if !ok {
		t.Fatalf("model-content API returned malformed revision: %v", workspace["revision"])
	}
	status, saved, _ := env.request(http.MethodPut, modelContentPath, env.adminToken, map[string]any{
		"revision":   workspaceRevision,
		"records":    []any{map[string]any{"id": singletonID, "entityId": entity.ID, "sortOrder": 0, "values": values}},
		"selections": []any{},
	})
	if status != http.StatusOK {
		t.Fatalf("save LegalSettings singleton record from manifest dataInputs: status=%d body=%v", status, saved)
	}
	savedRecords, ok := saved["records"].([]any)
	if !ok || len(savedRecords) != 1 {
		t.Fatalf("saving singleton replaced or duplicated its generated record ID: %+v", saved)
	}
	savedRecord, ok := savedRecords[0].(map[string]any)
	if !ok || savedRecord["id"] != singletonID {
		t.Fatalf("saving singleton replaced or duplicated its generated record ID: %+v", saved)
	}
	savedRevision, ok := saved["revision"].(float64)
	if !ok {
		t.Fatalf("model-content API returned malformed saved revision: %v", saved["revision"])
	}
	status, published, _ := env.request(http.MethodPost, modelContentPath+"/publish", env.adminToken, map[string]any{"revision": savedRevision})
	if status != http.StatusOK {
		t.Fatalf("publish LegalSettings singleton record: status=%d body=%v", status, published)
	}
	if published["publishedRevision"] != savedRevision {
		t.Fatalf("LegalSettings content was not published at its saved revision: saved=%v published=%v", savedRevision, published["publishedRevision"])
	}

	workerPath, err := filepath.Abs("../../scripts/prerender-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	options := publicruntime.PrerenderOptions{
		NodePath: node, WorkerPath: workerPath, PlaywrightModule: module, BrowserPath: browser,
		Timeout: 30 * time.Second, AttemptTimeout: 15 * time.Second, MaxAttempts: 1,
		QuietInterval: 100 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1,
		QueueSize: 4, CacheEntries: 8, MaxHTMLBytes: 1 << 20, HeapMB: 128,
		ViewportWidth: 1280, ViewportHeight: 900, MaxRequests: 100, MaxNetworkBytes: 16 << 20,
	}
	branch := branchBefore
	branch.Indexable = true
	server, worker := seoPublicServerWithWorker(t, env, branch, &options)
	if worker == nil {
		t.Fatal("legal-documents acceptance requires an active prerender worker")
	}

	routes := []struct {
		path, expectedText string
	}{
		{path: "/", expectedText: "Информация о cookie"},
		{path: "/privacy", expectedText: "LEGAL PRIVACY DOCUMENT FROM SINGLETON QA 8F4C2"},
		{path: "/personal-data-consent", expectedText: "LEGAL CONSENT DOCUMENT FROM SINGLETON QA 1B9D7"},
		{path: "/cookies", expectedText: "LEGAL COOKIE DOCUMENT FROM SINGLETON QA 6A3E1"},
	}
	for _, route := range routes {
		status, html, headers := seoGet(t, server, route.path)
		if status != http.StatusOK || headers.Get("X-Engine-Prerender") != "hit" || !strings.Contains(html, route.expectedText) {
			t.Fatalf("legal-documents route failed prerender/content acceptance: path=%s status=%d prerender=%q body=%s", route.path, status, headers.Get("X-Engine-Prerender"), html)
		}
		if route.path != "/" && !strings.Contains(html, "legalDocument") {
			t.Fatalf("legal document route omitted document view markup: path=%s html=%s", route.path, html)
		}
		t.Logf("prerendered legal-documents route %s with HTTP %d and expected model text", route.path, status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	browserScript := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
const base = process.env.LEGAL_PUBLIC_BASE_URL;
const errors = [];
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", error => errors.push("pageerror: " + error.message));
  page.on("console", message => {
    if (message.type() !== "error") return;
    const location = message.location();
    const path = location.url ? new URL(location.url).pathname : "";
    if (path === "/favicon.ico") return;
    errors.push("console: " + message.text() + " at " + JSON.stringify(location));
  });
  page.on("requestfailed", request => errors.push("requestfailed: " + request.method() + " " + request.url() + " " + (request.failure()?.errorText || "")));
  const expected = [
    ["/", "Информация о cookie"],
    ["/privacy", "LEGAL PRIVACY DOCUMENT FROM SINGLETON QA 8F4C2"],
    ["/personal-data-consent", "LEGAL CONSENT DOCUMENT FROM SINGLETON QA 1B9D7"],
    ["/cookies", "LEGAL COOKIE DOCUMENT FROM SINGLETON QA 6A3E1"],
  ];
  const hydrateVisiblePlacements = async () => {
    const ids = await page.locator("[data-engine-placement]").evaluateAll(roots => roots.map(root => root.getAttribute("data-engine-placement")).filter(Boolean));
    for (const id of ids) {
      await page.evaluate(placementId => [...document.querySelectorAll("[data-engine-placement]")]
        .find(root => root.getAttribute("data-engine-placement") === placementId)?.scrollIntoView({ block: "center" }), id);
      await page.waitForFunction(placementId => [...document.querySelectorAll("[data-engine-placement]")]
        .some(root => root.getAttribute("data-engine-placement") === placementId
          && (root.hasAttribute("data-runtime-feature-ready") || root.hasAttribute("data-runtime-feature-error"))), id, { timeout: 30000 });
    }
    await page.waitForFunction(() => document.documentElement.dataset.engineReady === "true", { timeout: 30000 });
    await page.evaluate(() => window.scrollTo(0, 0));
  };
  for (const [route, text] of expected) {
    const response = await page.goto(base + route, { waitUntil: "domcontentloaded" });
    if (!response || response.status() !== 200) throw new Error(route + ": expected HTTP 200, got " + (response?.status() ?? "no response"));
    await hydrateVisiblePlacements();
    if (!(await page.locator("body").innerText()).includes(text)) throw new Error(route + ": rendered body omitted expected model text " + text);
    const structure = await page.evaluate(() => {
      const placements = [...document.querySelectorAll("[data-engine-feature][data-engine-placement]")];
      const main = document.querySelector("main.runtime-slot--main");
      return {
        roots: placements.length,
        mainCount: document.querySelectorAll("main").length,
        mainHosts: document.querySelectorAll("main.runtime-slot--main").length,
        directChildren: placements.every(root => root.parentElement === (root.getAttribute("data-engine-slot") === "main" ? main : document.getElementById("root"))),
        nestedPlacements: placements.some(parent => placements.some(child => parent !== child && parent.contains(child))),
        extraSlotHosts: [...document.querySelectorAll(".runtime-slot")].filter(host => host !== main).length,
        placementSlots: placements.map(root => root.getAttribute("data-engine-slot")),
      };
    });
    if (structure.roots !== (route === "/" ? 1 : 2) || structure.mainCount !== 1 || structure.mainHosts !== 1 || !structure.directChildren || structure.nestedPlacements || structure.extraSlotHosts !== 0) {
      throw new Error(route + ": placements are not direct children of the page root or its one main landmark: " + JSON.stringify(structure));
    }
    if (route !== "/") {
      const main = page.locator('[data-engine-feature="legal-documents"] article.legalDocument');
      if (await main.count() !== 1) throw new Error(route + ": expected one rendered legal document view");
    }
  }

  await page.goto(base + "/", { waitUntil: "domcontentloaded" });
  await hydrateVisiblePlacements();
  const footer = page.locator('[data-engine-feature="legal-documents"] section[aria-label="Правовая информация"]');
  if (await footer.count() !== 1) throw new Error("legal footer region is missing or ambiguous");
  for (const [label, href] of [
    ["Политика обработки персональных данных", "/privacy"],
    ["Согласие на обработку персональных данных", "/personal-data-consent"],
    ["Информация о cookie", "/cookies"],
  ]) {
    const link = footer.getByRole("link", { name: label, exact: true });
    if (await link.getAttribute("href") !== href) throw new Error("footer link " + label + " has wrong target");
  }
  const rejectButton = footer.getByRole("button", { name: "Отклонить", exact: true });
  const acceptButton = footer.getByRole("button", { name: "Принять", exact: true });
  if (await rejectButton.count() !== 1 || await acceptButton.count() !== 1) throw new Error("cookie footer must expose only the binary accept/reject choices");
  const cookiesBefore = await context.cookies();
  const localBefore = await page.evaluate(() => Object.fromEntries(Array.from({ length: localStorage.length }, (_, index) => { const key = localStorage.key(index); return [key, key === null ? null : localStorage.getItem(key)]; })));
  const preferenceRequests = [];
  page.on("request", request => preferenceRequests.push(request.method() + " " + request.url()));
  await rejectButton.focus();
  await page.keyboard.press("Enter");
  const preference = await page.evaluate(() => {
    const raw = localStorage.getItem("vitasil.cookie-preferences");
    return raw === null ? null : JSON.parse(raw);
  });
  if (!preference || preference.version !== "cookie-qa-2026-11" || preference.choice !== "rejected" || typeof preference.savedAt !== "string") throw new Error("cookie selection was not persisted with the expected localStorage-only value: " + JSON.stringify(preference));
  const localAfter = await page.evaluate(() => Object.fromEntries(Array.from({ length: localStorage.length }, (_, index) => { const key = localStorage.key(index); return [key, key === null ? null : localStorage.getItem(key)]; })));
  const changedKeys = new Set([...Object.keys(localBefore), ...Object.keys(localAfter)].filter(key => localBefore[key] !== localAfter[key]));
  if (changedKeys.size !== 1 || !changedKeys.has("vitasil.cookie-preferences")) throw new Error("cookie choice changed localStorage outside the expected preference key: " + JSON.stringify({ localBefore, localAfter }));
  if (JSON.stringify(await context.cookies()) !== JSON.stringify(cookiesBefore)) throw new Error("cookie preference interaction created or changed browser cookies");
  if (preferenceRequests.length !== 0) throw new Error("cookie preference interaction caused network side effects: " + preferenceRequests.join(" | "));
  const preferenceSideEffects = preferenceRequests.length;
  await page.goto(base + "/cookies", { waitUntil: "domcontentloaded" });
  await hydrateVisiblePlacements();
  const cookieDocument = page.locator('[data-engine-feature="legal-documents"] article.legalDocument');
  await cookieDocument.getByRole("button", { name: "Принять", exact: true }).focus();
  await page.keyboard.press("Enter");
  const accepted = await page.evaluate(() => JSON.parse(localStorage.getItem("vitasil.cookie-preferences") || "null"));
  if (!accepted || accepted.version !== "cookie-qa-2026-11" || accepted.choice !== "accepted") throw new Error("cookie document did not allow a later accepted choice: " + JSON.stringify(accepted));
  if (errors.length) throw new Error("browser runtime errors: " + errors.join(" | "));
  console.log(JSON.stringify({ routes: expected.map(item => item[0]), footerLinks: 3, binaryCookieChoices: true, keyboardSave: true, localStorageKey: "vitasil.cookie-preferences", cookieDelta: 0, preferenceRequests: preferenceSideEffects, errors }));
  await context.close();
} finally { await browser.close(); }`
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", browserScript)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browser,
		"LEGAL_PUBLIC_BASE_URL="+strings.TrimRight(server.URL, "/"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Vitasil legal-documents Chromium acceptance failed: %v\n%s", err, output)
	}
	t.Logf("Vitasil legal-documents Chromium acceptance metrics:\n%s", output)

	branchAfter, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil || branchAfter.SnapshotID == nil || branchAfter.SnapshotID.String() != snapshotID.String() || branchAfter.Revision != branchBefore.Revision {
		t.Fatalf("legal-documents API/prerender/browser flow changed the snapshot-to-branch assignment: before=%+v after=%+v err=%v", branchBefore, branchAfter, err)
	}
}
