package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestExternalBackendBindingsDisposableAcceptance(t *testing.T) {
	requireUniqueExternalBackendDatabase(t)
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)

	backendID := uuid.NewString()
	manifest := staticRuntimeManifest("external-backend-probe", sitebuilder.RuntimeABIStaticModule, "dist/entry.mjs")
	manifest["arguments"] = objectSchema(map[string]any{
		"profileRequest": map[string]any{"type": "object", "x-editor-managed-by-site": true},
	})
	manifest["requests"] = []any{map[string]any{
		"key": "profile", "label": "Profile request", "argument": "profileRequest", "defaultPath": "/profiles/{username}",
		"destinations": []any{
			map[string]any{"kind": "path", "key": "username", "label": "Имя пользователя", "type": "string", "sources": []string{"route"}, "required": true},
			map[string]any{"kind": "query", "key": "tags", "label": "Теги", "type": "array", "itemsType": "string", "sources": []string{"query"}},
		},
	}}
	entry := "export function mount(root, context) {\n" +
		"  const request = context.arguments.profileRequest;\n" +
		"  const sameOrigin = fetch(\"/api/v2/runtime?path=\" + encodeURIComponent(context.route.path) + \"&search=\" + encodeURIComponent(context.route.search)).then(response => response.status).catch(() => 0);\n" +
		"  const remote = async () => {\n" +
		"    if (request.error) return \"config-\" + request.error;\n" +
		"    try {\n" +
		"      const target = new URL(request.path, request.baseUrl || window.location.origin);\n" +
		"      for (const [key, values] of Object.entries(request.query || {})) for (const value of values) target.searchParams.append(key, String(value));\n" +
		"      const testCase = new URLSearchParams(context.route.search).get(\"case\");\n" +
		"      if (testCase) target.searchParams.set(\"case\", testCase);\n" +
		"      const response = await fetch(target);\n" +
		"      return response.ok ? \"success-\" + await response.text() : \"http-\" + response.status;\n" +
		"    } catch { return \"network-error\"; }\n" +
		"  };\n" +
		"  const ready = Promise.all([sameOrigin, remote()]).then(([status, result]) => {\n" +
		"    root.dataset.sameOriginStatus = String(status);\n" +
		"    root.dataset.backendState = result.startsWith(\"success-\") ? \"success\" : result;\n" +
		"    root.textContent = result;\n" +
		"  });\n" +
		"  return { ready, update() {}, unmount() { root.replaceChildren(); } };\n" +
		"}\n"
	archive := staticFeatureArtifact(t, manifest, map[string][]byte{"dist/entry.mjs": []byte(entry)})
	status, body, _ := e.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", e.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register synthetic public-request feature: HTTP %d body=%v", status, body)
	}

	config := map[string]any{
		"formatVersion": 3,
		"backends":      []any{map[string]any{"id": backendID, "name": "Profiles API", "baseUrl": "https://api.example.com"}},
		"systemPages":   map[string]string{"404": "not-found"},
		"pages": []any{map[string]any{
			"id": "profile", "name": "Profile", "kind": "page",
			"placements": []any{map[string]any{
				"id":    "backend-probe",
				"build": map[string]any{"featureKey": "external-backend-probe", "version": "1.0.0"},
				"slot":  "main",
				"backendRequests": map[string]any{"profile": map[string]any{
					"backendId": backendID,
					"path":      "/profiles/{username}",
					"bindings": []any{
						map[string]any{"destination": map[string]any{"kind": "path", "key": "username"}, "source": map[string]any{"kind": "route", "key": "slug"}},
						map[string]any{"destination": map[string]any{"kind": "query", "key": "tags"}, "source": map[string]any{"kind": "query", "key": "tag"}},
					},
				}},
			}},
		}, map[string]any{
			"id": "not-found", "name": "Not found", "kind": "page", "placements": []any{},
		}},
		"routeTree": map[string]any{"id": "site", "type": "site", "children": []any{
			map[string]any{"id": "profile-route", "type": "path", "path": "/people/:slug", "pageId": "profile"},
			map[string]any{"id": "catch-all-route", "type": "path", "path": "/legacy/*rest", "pageId": "not-found"},
		}},
	}
	root := saveAndDeploySnapshot(t, e, config, "External backend bindings")
	rootID, err := uuid.Parse(root["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE site_environments SET indexable=true WHERE key=$1", e.devBranch.String()); err != nil {
		t.Fatal(err)
	}
	branchID, err := uuid.Parse(e.devBranch.String())
	if err != nil {
		t.Fatal(err)
	}
	branchBefore, err := e.snapshots.Branch(ctx, branchID)
	if err != nil || branchBefore.SnapshotID == nil || *branchBefore.SnapshotID != rootID {
		t.Fatalf("initial development assignment does not reference the root snapshot: branch=%+v err=%v", branchBefore, err)
	}

	resolved, err := e.snapshots.Runtime(ctx, e.devBranch, "/people/alice", url.Values{"tag": {"one", "two"}})
	if err != nil {
		t.Fatal(err)
	}
	var runtimeArguments map[string]json.RawMessage
	if err := json.Unmarshal(resolved.Placements[0].Arguments, &runtimeArguments); err != nil {
		t.Fatal(err)
	}
	var request sitebuilder.ResolvedBackendRequest
	if err := json.Unmarshal(runtimeArguments["profileRequest"], &request); err != nil {
		t.Fatal(err)
	}
	if request.BaseURL != "https://api.example.com" || request.Path != "/profiles/alice" || len(request.Query["tags"]) != 2 || request.Error != "" {
		t.Fatalf("resolved external request did not preserve origin/path/repeated query: %+v", request)
	}
	missing, err := e.snapshots.Runtime(ctx, e.devBranch, "/people/alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(missing.Placements[0].Arguments, &runtimeArguments); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(runtimeArguments["profileRequest"], &request); err != nil {
		t.Fatal(err)
	}
	if request.Error != "missing_source" {
		t.Fatalf("missing incoming query source error = %q, want missing_source", request.Error)
	}

	detail, err := e.snapshots.Snapshot(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	removedBackend := detail.Payload.Config
	removedBackend.Backends = nil
	if _, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Invalid removal", ParentID: &rootID, Config: &removedBackend}); err == nil || !errors.Is(err, sitebuilder.ErrInvalid) {
		t.Fatalf("snapshot accepted removal of a backend with placements: %v", err)
	}
	childConfig := detail.Payload.Config
	childConfig.Backends[0].Name = "Profiles API v2"
	child, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Backend child", ParentID: &rootID, Config: &childConfig})
	if err != nil {
		t.Fatal(err)
	}
	branchAfterChild, err := e.snapshots.Branch(ctx, branchID)
	if err != nil || branchAfterChild.SnapshotID == nil || *branchAfterChild.SnapshotID != rootID || branchAfterChild.Revision != branchBefore.Revision {
		t.Fatalf("creating a child snapshot changed branch assignment: before=%+v after=%+v err=%v", branchBefore, branchAfterChild, err)
	}
	if _, err := e.snapshots.AssignBranch(ctx, actor, branchID, child.ID, branchBefore.Revision-1, &child.ContentRevision); !errors.Is(err, sitebuilder.ErrConflict) {
		t.Fatalf("stale branch revision should conflict: %v", err)
	}
	branchAfterConflict, err := e.snapshots.Branch(ctx, branchID)
	if err != nil || branchAfterConflict.SnapshotID == nil || *branchAfterConflict.SnapshotID != rootID || branchAfterConflict.Revision != branchBefore.Revision {
		t.Fatalf("stale assignment changed branch state: %+v err=%v", branchAfterConflict, err)
	}

	branch, err := e.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	publicServer, _ := externalBindingsPublicServer(t, e, branch)
	pageStatus, _, pageHeaders := seoGet(t, publicServer, "/people/alice")
	if pageStatus != http.StatusOK || pageHeaders.Get("Cache-Control") != "no-store" || pageHeaders.Get("ETag") != "" || pageHeaders.Get("X-Engine-Prerender") != "" {
		t.Fatalf("external backend page was cached or prerendered: status=%d headers=%v", pageStatus, pageHeaders)
	}
	csp := pageHeaders.Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self' https://api.example.com;") || strings.Contains(csp, "connect-src 'self' *") {
		t.Fatalf("page CSP did not allow only the exact used public origin: %q", csp)
	}
	notFoundStatus, notFoundHTML, notFoundHeaders := seoGet(t, publicServer, "/legacy/unmatched/path")
	if notFoundStatus != http.StatusNotFound || !strings.Contains(notFoundHTML, "noindex,nofollow") || notFoundHeaders.Get("Cache-Control") != "no-store" || notFoundHeaders.Get("ETag") != "" {
		t.Fatalf("configured catch-all 404 lost status/noindex/no-store: status=%d headers=%v body=%s", notFoundStatus, notFoundHeaders, notFoundHTML)
	}

	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		t.Log("browser acceptance not run: set PRERENDER_TEST_NODE/MODULE/BROWSER")
		return
	}
	runExternalBackendBrowserAcceptance(t, e, publicServer.URL+"/people/alice?tag=one&tag=two", child.ID.String(), rootID.String(), branchID.String(), branchBefore.Revision, node, module, browser)
}

func externalBindingsPublicServer(t *testing.T, e *testEnv, branch sitebuilder.BranchRecord) (*httptest.Server, *publicruntime.Prerenderer) {
	t.Helper()
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		server, worker := seoPublicServerWithWorker(t, e, branch, nil)
		return server, worker
	}
	workerPath, err := filepath.Abs("../../scripts/prerender-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	options := publicruntime.PrerenderOptions{
		NodePath: node, WorkerPath: workerPath, PlaywrightModule: module, BrowserPath: browser,
		Timeout: 20 * time.Second, AttemptTimeout: 15 * time.Second, MaxAttempts: 1, QuietInterval: 100 * time.Millisecond,
		CacheTTL: time.Minute, Concurrency: 1, QueueSize: 2, CacheEntries: 4, MaxHTMLBytes: 1 << 20,
		HeapMB: 128, ViewportWidth: 1280, ViewportHeight: 720, MaxRequests: 100, MaxNetworkBytes: 16 << 20,
	}
	server, worker := seoPublicServerWithWorker(t, e, branch, &options)
	return server, worker
}

func runExternalBackendBrowserAcceptance(t *testing.T, e *testEnv, publicURL, childSnapshotID, rootSnapshotID, branchID string, branchRevision int64, node, module, browser string) {
	t.Helper()
	script := strings.Join([]string{
		"import { pathToFileURL } from \"node:url\";",
		"const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);",
		"const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: [\"--no-sandbox\"] });",
		"try {",
		"  const page = await browser.newPage();",
		"  const externalRequests = [];",
		"  const pageErrors = [];",
		"  page.on(\"pageerror\", error => pageErrors.push(error.message));",
		"  await page.route(\"https://api.example.com/**\", async route => {",
		"    const url = new URL(route.request().url());",
		"    externalRequests.push(url.href);",
		"    const caseName = url.searchParams.get(\"case\");",
		"    const status = caseName === \"404\" ? 404 : caseName === \"500\" ? 500 : 200;",
		"    await route.fulfill({ status, headers: { \"access-control-allow-origin\": process.env.PUBLIC_ORIGIN, \"content-type\": \"application/json\" }, body: status === 200 ? \"profile-ok\" : \"backend-error\" });",
		"  });",
		"  const check = async (url, expected, expectedExternalCount) => {",
		"    await page.goto(process.env.PUBLIC_URL.replace(\"/people/alice?tag=one&tag=two\", url), { waitUntil: \"domcontentloaded\" });",
		"    await page.waitForFunction(state => document.querySelector(\"[data-engine-placement=backend-probe]\")?.dataset.backendState === state, expected);",
		"    const root = page.locator(\"[data-engine-placement=backend-probe]\");",
		"    if (await root.getAttribute(\"data-same-origin-status\") !== \"200\") throw new Error(\"same-origin runtime GET did not succeed\");",
		"    if (externalRequests.length !== expectedExternalCount) throw new Error(\"unexpected external GET count: \" + externalRequests.length);",
		"  };",
		"  await check(\"/people/alice?tag=one&tag=two\", \"success\", 1);",
		"  const firstExternal = new URL(externalRequests[0]);",
		"  if (firstExternal.origin !== \"https://api.example.com\" || firstExternal.pathname !== \"/profiles/alice\" || JSON.stringify(firstExternal.searchParams.getAll(\"tags\")) !== JSON.stringify([\"one\", \"two\"])) throw new Error(\"bound path or repeated query values were not preserved: \" + firstExternal.href);",
		"  await check(\"/people/alice?tag=one&case=404\", \"http-404\", 2);",
		"  await check(\"/people/alice?tag=one&case=500\", \"http-500\", 3);",
		"  await check(\"/people/alice\", \"config-missing_source\", 3);",
		"  if (pageErrors.length) throw new Error(\"runtime browser errors: \" + pageErrors.join(\"; \"));",
		"  const admin = await browser.newPage();",
		"  const adminErrors = [];",
		"  admin.on(\"pageerror\", error => adminErrors.push(error.message));",
		"  await admin.addInitScript(token => localStorage.setItem(\"we_admin_token\", token), process.env.TEST_ADMIN_TOKEN);",
		"  await admin.goto(process.env.ADMIN_URL + \"/admin/site/settings?snapshot=\" + process.env.ROOT_SNAPSHOT, { waitUntil: \"domcontentloaded\" });",
		"  await admin.getByRole(\"heading\", { name: \"Настройки сайта\", exact: true }).waitFor({ timeout: 20000 });",
		"  await admin.getByRole(\"heading\", { name: \"Публичные API сайта\", exact: true }).waitFor();",
		"  const apiStatus = await admin.evaluate(async token => (await fetch(\"/api/v1/admin/me\", { headers: { Authorization: \"Bearer \" + token } })).status, process.env.TEST_ADMIN_TOKEN);",
		"  if (apiStatus !== 200) throw new Error(\"admin UI is not connected to the disposable Engine API: HTTP \" + apiStatus);",
		"  await admin.getByRole(\"button\", { name: \"Добавить backend\", exact: true }).click();",
		"  const backendGroups = admin.getByRole(\"group\", { name: \"Публичный backend\" });",
		"  const addedBackend = backendGroups.last();",
		"  await addedBackend.locator(\"input\").nth(0).fill(\"Profiles API v2\");",
		"  await addedBackend.locator(\"input\").nth(1).fill(\"https://api-v2.example.com\");",
		"  await admin.getByRole(\"button\", { name: \"Сохранить\", exact: true }).click();",
		"  await admin.waitForFunction(previous => new URL(location.href).searchParams.get(\"snapshot\") !== previous, process.env.ROOT_SNAPSHOT, { timeout: 20000 });",
		"  const firstChild = new URL(admin.url()).searchParams.get(\"snapshot\");",
		"  if (!firstChild || firstChild === process.env.ROOT_SNAPSHOT) throw new Error(\"editing backend registry did not create a child snapshot\");",
		"  await admin.goto(process.env.ADMIN_URL + \"/admin/site/pages?snapshot=\" + firstChild, { waitUntil: \"domcontentloaded\" });",
		"  await admin.locator(\"[data-route-id=profile-route] button\").first().focus();",
		"  await admin.locator(\"[data-route-id=profile-route] button\").first().press(\"Enter\");",
		"  await admin.getByRole(\"heading\", { name: \"Profile\", exact: true }).waitFor({ timeout: 15000 });",
		"  if (await admin.getByLabel(\"Источник Имя пользователя\").inputValue() !== \"route\") throw new Error(\"route-to-path binding was not restored in the page inspector\");",
		"  if (await admin.getByLabel(\"Параметр пути\").inputValue() !== \"slug\") throw new Error(\"route capture mapping was not restored in the page inspector\");",
		"  if (await admin.getByLabel(\"Источник Теги\").inputValue() !== \"query\") throw new Error(\"incoming-query binding was not restored in the page inspector\");",
		"  if (await admin.getByLabel(\"Ключ входного query\").inputValue() !== \"tag\") throw new Error(\"incoming query key was not restored in the page inspector\");",
		"  await admin.getByLabel(\"Публичный backend\").selectOption({ label: \"Profiles API v2\" });",
		"  await admin.getByLabel(\"Путь запроса\").fill(\"/directory/{username}\");",
		"  await admin.getByRole(\"button\", { name: \"Сохранить\", exact: true }).click();",
		"  await admin.waitForFunction(previous => new URL(location.href).searchParams.get(\"snapshot\") !== previous, firstChild, { timeout: 20000 });",
		"  const finalSnapshot = new URL(admin.url()).searchParams.get(\"snapshot\");",
		"  if (!finalSnapshot || finalSnapshot === firstChild) throw new Error(\"editing request placement did not create a child snapshot\");",
		"  if (adminErrors.length) throw new Error(\"admin browser errors: \" + adminErrors.join(\"; \"));",
		"  console.log(\"RESULT \" + JSON.stringify({ externalRequests, rootSnapshot: process.env.ROOT_SNAPSHOT, firstChild, finalSnapshot, adminApiStatus: apiStatus }));",
		"} finally { await browser.close(); }",
	}, "\n")
	parsedURL, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	publicOrigin := parsedURL.Scheme + "://" + parsedURL.Host
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", script)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module, "PLAYWRIGHT_TEST_BROWSER="+browser,
		"PUBLIC_URL="+publicURL, "PUBLIC_ORIGIN="+publicOrigin, "ADMIN_URL="+e.server.URL,
		"TEST_ADMIN_TOKEN="+e.adminToken, "ROOT_SNAPSHOT="+rootSnapshotID,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("external backend browser acceptance failed: %v\n%s", err, output)
	}
	resultLine := ""
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "RESULT ") {
			resultLine = strings.TrimPrefix(line, "RESULT ")
		}
	}
	if resultLine == "" {
		t.Fatalf("browser acceptance returned no result summary:\n%s", output)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultLine), &result); err != nil {
		t.Fatalf("decode browser acceptance summary: %v\n%s", err, output)
	}
	externalRequests, _ := result["externalRequests"].([]any)
	firstChild, _ := result["firstChild"].(string)
	finalSnapshot, _ := result["finalSnapshot"].(string)
	rootSnapshot, _ := result["rootSnapshot"].(string)
	adminAPIStatus, _ := result["adminApiStatus"].(float64)
	if len(externalRequests) != 3 || rootSnapshot != rootSnapshotID || firstChild == "" || finalSnapshot == "" || adminAPIStatus != http.StatusOK {
		t.Fatalf("browser acceptance summary is incomplete: %+v", result)
	}
	branchUUID, _ := uuid.Parse(branchID)
	branch, err := e.snapshots.Branch(context.Background(), branchUUID)
	if err != nil || branch.SnapshotID == nil || branch.SnapshotID.String() != rootSnapshotID || branch.Revision != branchRevision {
		t.Fatalf("site editor changed branch assignment: branch=%+v err=%v", branch, err)
	}
	rootUUID, _ := uuid.Parse(rootSnapshotID)
	root, err := e.snapshots.Snapshot(context.Background(), rootUUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Payload.Config.Backends) != 1 || root.Payload.Config.Backends[0].Name != "Profiles API" {
		t.Fatalf("editing child snapshot changed its parent registry: %+v", root.Payload.Config.Backends)
	}
	finalUUID, err := uuid.Parse(finalSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	final, err := e.snapshots.Snapshot(context.Background(), finalUUID)
	if err != nil {
		t.Fatal(err)
	}
	finalPlacement := final.Payload.Config.Pages[0].Placements[0]
	if len(final.Payload.Config.Backends) != 2 || finalPlacement.BackendRequests["profile"].Path != "/directory/{username}" {
		t.Fatalf("final editor child did not preserve registry and edited placement: backends=%+v request=%+v", final.Payload.Config.Backends, finalPlacement.BackendRequests["profile"])
	}
}

func TestExternalBackendBindingsVitasilArchiveCompatibility(t *testing.T) {
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	if featureDir == "" {
		t.Skip("set TEST_FEATURE_DIR to the current Vitasil frontend/dist/feature-builds directory")
	}
	keys := []string{"background", "catalog", "catalog-detail", "footer", "form", "header", "hero", "main-data", "not-found"}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			archive, version := readExternalFeatureArchive(t, featureDir, key)
			verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
			if err != nil {
				t.Fatalf("read-only archive verification failed for %s@%s: %v", key, version, err)
			}
			if verified.Manifest.Key != key || verified.Manifest.Version != version {
				t.Fatalf("archive identity changed during verification: %s@%s manifest=%s@%s", key, version, verified.Manifest.Key, verified.Manifest.Version)
			}
			t.Logf("verified %s@%s using existing manifest/runtime contract", key, version)
		})
	}
}

func requireUniqueExternalBackendDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	if raw == "" || err != nil {
		t.Skip("set TEST_DATABASE_URL to a unique local website_engine_external_backend_qa_* database; integration setup truncates it")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(databaseName, "website_engine_external_backend_qa_") ||
		(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Skip("external backend acceptance requires a local website_engine_external_backend_qa_* disposable database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to disposable external backend database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var sessions int
	if err := connection.QueryRow(ctx, "SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())").Scan(&currentDatabase, &sessions); err != nil {
		t.Fatalf("verify disposable database and active sessions: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("TEST_DATABASE_URL database %q does not match URL name %q", currentDatabase, databaseName)
	}
	if sessions != 0 {
		t.Fatalf("refusing newTestEnv TRUNCATE: disposable database %q already has %d other sessions", currentDatabase, sessions)
	}
	t.Logf("verified disposable local database %s has no other sessions before integration setup", currentDatabase)
}
