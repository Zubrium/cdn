package integration

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/sitebuilder"
)

// TestExternalBackendActualCORSAndRetryBrowserAcceptance exercises the browser's
// real CORS and CSP enforcement against controlled loopback listeners. Chromium
// maps the public-looking test origins to those listeners, so the test never
// contacts an external service.
func TestExternalBackendActualCORSAndRetryBrowserAcceptance(t *testing.T) {
	requireUniqueExternalBackendBrowserDatabase(t)
	node, module, browserPath := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browserPath == "" {
		t.Skip("set PRERENDER_TEST_NODE/MODULE/BROWSER to run the controlled CORS browser acceptance")
	}
	e := newTestEnv(t)

	var publicOrigin atomic.Value
	publicOrigin.Store("")
	var successHits, missingCORSHits, foreignCORSHits, serverErrorHits, timeoutHits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/profiles/alice" {
			http.NotFound(w, r)
			return
		}
		mode := r.URL.Query().Get("case")
		switch mode {
		case "success":
			successHits.Add(1)
		case "cors-missing":
			missingCORSHits.Add(1)
		case "cors-foreign":
			foreignCORSHits.Add(1)
		case "server-error":
			serverErrorHits.Add(1)
		case "timeout":
			timeoutHits.Add(1)
		}
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if mode != "cors-missing" {
			allowOrigin := "https://other-origin.example.net"
			if mode != "cors-foreign" && r.Header.Get("Origin") == publicOrigin.Load().(string) {
				allowOrigin = publicOrigin.Load().(string)
			}
			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		}
		if mode == "timeout" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(1200 * time.Millisecond):
			}
		}
		if mode == "server-error" {
			http.Error(w, "controlled backend error", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("controlled backend response"))
	}))
	t.Cleanup(backend.Close)
	backendPort := listenerPort(t, backend)
	backendOrigin := fmt.Sprintf("http://api.cors-probe.example.net:%d", backendPort)

	var unapprovedHits atomic.Int32
	unapproved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unapprovedHits.Add(1)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write([]byte("must never be reached"))
	}))
	t.Cleanup(unapproved.Close)
	unapprovedOrigin := fmt.Sprintf("http://unapproved.cors-probe.example.net:%d", listenerPort(t, unapproved))

	backendID := uuid.NewString()
	manifest := staticRuntimeManifest("actual-cors-probe", sitebuilder.RuntimeABIStaticModule, "dist/entry.mjs")
	manifest["arguments"] = objectSchema(map[string]any{
		"profileRequest": map[string]any{"type": "object", "x-editor-managed-by-site": true},
	})
	manifest["requests"] = []any{map[string]any{
		"key": "profile", "label": "Profile request", "argument": "profileRequest", "defaultPath": "/profiles/{username}",
		"destinations": []any{
			map[string]any{"kind": "path", "key": "username", "label": "Имя пользователя", "type": "string", "sources": []string{"route"}, "required": true},
			map[string]any{"kind": "query", "key": "case", "label": "Test case", "type": "string", "sources": []string{"query"}},
		},
	}}
	entry := `export function mount(root, context) {
  const request = context.arguments.profileRequest;
  const status = document.createElement("output");
  status.setAttribute("aria-live", "polite");
  const retry = document.createElement("button");
  retry.type = "button";
  retry.textContent = "Retry request";
  root.append(status, retry);
  async function load(caseOverride) {
    root.dataset.backendStatus = "loading";
    status.textContent = "Loading";
    if (request.error) {
      root.dataset.backendStatus = "config-error";
      status.textContent = "Configuration unavailable";
      return;
    }
    const target = new URL(request.path, request.baseUrl || window.location.origin);
    for (const [key, values] of Object.entries(request.query || {})) {
      for (const value of values) target.searchParams.append(key, String(value));
    }
    if (caseOverride) target.searchParams.set("case", caseOverride);
    const controller = new AbortController();
    const timeoutId = target.searchParams.get("case") === "timeout"
      ? setTimeout(() => controller.abort(), 500)
      : null;
    try {
      const response = await fetch(target, { signal: controller.signal });
      if (!response.ok) {
        root.dataset.backendStatus = "http-" + response.status;
        status.textContent = "HTTP " + response.status;
        return;
      }
      await response.text();
      root.dataset.backendStatus = "success";
      status.textContent = "Request succeeded";
    } catch (error) {
      const state = error && error.name === "AbortError" ? "timeout" : "network-error";
      root.dataset.backendStatus = state;
      status.textContent = state === "timeout" ? "Request timed out" : "Network request failed";
    } finally {
      if (timeoutId !== null) clearTimeout(timeoutId);
    }
  }
  retry.addEventListener("click", () => { void load("success"); });
  const initialCase = new URLSearchParams(context.route.search).get("case");
  const ready = load(initialCase);
  return { ready, update() {}, unmount() { root.replaceChildren(); } };
}

`
	archive := staticFeatureArtifact(t, manifest, map[string][]byte{"dist/entry.mjs": []byte(entry)})
	status, body, _ := e.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", e.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register controlled CORS feature: HTTP %d body=%v", status, body)
	}
	config := map[string]any{
		"formatVersion": 3,
		"backends":      []any{map[string]any{"id": backendID, "name": "Controlled API", "baseUrl": backendOrigin}},
		"pages": []any{map[string]any{
			"id": "profile", "name": "Profile", "kind": "page",
			"placements": []any{map[string]any{
				"id": "cors-probe", "slot": "main",
				"build": map[string]any{"featureKey": "actual-cors-probe", "version": "1.0.0"},
				"backendRequests": map[string]any{"profile": map[string]any{
					"backendId": backendID, "path": "/profiles/{username}",
					"bindings": []any{
						map[string]any{"destination": map[string]any{"kind": "path", "key": "username"}, "source": map[string]any{"kind": "route", "key": "slug"}},
						map[string]any{"destination": map[string]any{"kind": "query", "key": "case"}, "source": map[string]any{"kind": "query", "key": "case"}},
					},
				}},
			}},
		}},
		"routeTree": map[string]any{"id": "site", "type": "site", "children": []any{
			map[string]any{"id": "profile-route", "type": "path", "path": "/people/:slug", "pageId": "profile"},
		}},
	}
	root := saveAndDeploySnapshot(t, e, config, "Actual browser CORS acceptance")
	branchUUID, err := uuid.Parse(string(e.devBranch))
	if err != nil {
		t.Fatal(err)
	}
	branch, err := e.snapshots.Branch(context.Background(), branchUUID)
	if err != nil {
		t.Fatalf("load branch for public CORS runtime: %v", err)
	}
	publicServer, _ := externalBindingsPublicServer(t, e, branch)
	engineURL, err := url.Parse(publicServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	engineOrigin := engineURL.Scheme + "://" + engineURL.Host
	publicOrigin.Store(engineOrigin)
	statusCode, _, headers := seoGet(t, publicServer, "/people/alice")
	if statusCode != http.StatusOK {
		t.Fatalf("load public CORS page: HTTP %d", statusCode)
	}
	assertExactConnectSource(t, headers.Get("Content-Security-Policy"), backendOrigin, unapprovedOrigin)
	if headers.Get("Cache-Control") != "no-store" || headers.Get("ETag") != "" || headers.Get("X-Engine-Prerender") != "" {
		t.Fatalf("external-backend document must remain uncached and unprerendered: headers=%v", headers)
	}

	script := strings.Join([]string{
		`import { pathToFileURL } from "node:url";`,
		`const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);`,
		`const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox", "--host-resolver-rules=" + process.env.HOST_RESOLVER_RULES] });`,
		`try {`,
		`  const page = await browser.newPage();`,
		`  const errors = [];`,
		`  page.on("pageerror", error => errors.push(error.message));`,
		`  await page.goto(process.env.PUBLIC_URL, { waitUntil: "domcontentloaded" });`,
		`  const root = page.locator("[data-engine-placement=cors-probe]");`,
		`  const waitState = state => page.waitForFunction(expected => document.querySelector("[data-engine-placement=cors-probe]")?.dataset.backendStatus === expected, state, { timeout: 12000 });`,
		`  const goToCase = async mode => { const target = new URL(process.env.PUBLIC_URL); target.searchParams.set("case", mode); await page.goto(target.href, { waitUntil: "domcontentloaded" }); await waitState(mode === "success" ? "success" : mode === "server-error" ? "http-503" : mode === "timeout" ? "timeout" : "network-error"); };`,
		`  await waitState("success");`,
		`  if (await root.getAttribute("data-backend-status") !== "success") throw new Error("allowed-origin request did not succeed");`,
		`  await page.evaluate(() => { window.__cspViolations = []; addEventListener("securitypolicyviolation", event => window.__cspViolations.push({ directive: event.effectiveDirective, blocked: event.blockedURI })); });`,
		`  const unapprovedResult = await page.evaluate(async target => { try { await fetch(target, { mode: "no-cors" }); return "resolved"; } catch { return "rejected"; } }, process.env.UNAPPROVED_URL);`,
		`  if (unapprovedResult !== "rejected") throw new Error("CSP did not block an unregistered connect-src origin");`,
		`  await page.waitForFunction(() => window.__cspViolations.some(item => item.directive === "connect-src"), null, { timeout: 5000 });`,
		`  const violation = await page.evaluate(() => window.__cspViolations.find(item => item.directive === "connect-src"));`,
		`  if (!violation.blocked.startsWith(new URL(process.env.UNAPPROVED_URL).origin)) throw new Error("CSP blocked an unexpected origin: " + JSON.stringify(violation));`,
		`  await goToCase("cors-missing");`,
		`  await root.getByRole("button", { name: "Retry request" }).click(); await waitState("success");`,
		`  await goToCase("cors-foreign");`,
		`  await root.getByRole("button", { name: "Retry request" }).click(); await waitState("success");`,
		`  await goToCase("server-error");`,
		`  await goToCase("timeout");`,
		`  await root.getByRole("button", { name: "Retry request" }).click(); await waitState("success");`,
		`  if (errors.length) throw new Error("uncaught browser errors: " + errors.join("; "));`,
		`  console.log("RESULT " + JSON.stringify({ allowed: true, missingCors: "network-error", foreignCors: "network-error", httpError: "http-503", timeout: "timeout", retryAfterCors: true, retryAfterTimeout: true, cspDirective: violation.directive, cspBlockedOrigin: new URL(process.env.UNAPPROVED_URL).origin }));`,
		`} finally { await browser.close(); }`,
	}, "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", script)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browserPath,
		"PUBLIC_URL="+publicServer.URL+"/people/alice?case=success",
		"UNAPPROVED_URL="+unapprovedOrigin+"/probe",
		"HOST_RESOLVER_RULES=MAP api.cors-probe.example.net 127.0.0.1,MAP unapproved.cors-probe.example.net 127.0.0.1",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual CORS browser acceptance failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"allowed":true`) || !strings.Contains(string(output), `"missingCors":"network-error"`) || !strings.Contains(string(output), `"foreignCors":"network-error"`) || !strings.Contains(string(output), `"httpError":"http-503"`) || !strings.Contains(string(output), `"timeout":"timeout"`) {
		t.Fatalf("browser did not report the required CORS/error/retry states:\n%s", output)
	}
	if successHits.Load() != 4 || missingCORSHits.Load() != 1 || foreignCORSHits.Load() != 1 || serverErrorHits.Load() != 1 || timeoutHits.Load() != 1 {
		t.Fatalf("controlled backend request counts do not match browser scenarios: success=%d missing-cors=%d foreign-cors=%d server-error=%d timeout=%d", successHits.Load(), missingCORSHits.Load(), foreignCORSHits.Load(), serverErrorHits.Load(), timeoutHits.Load())
	}
	if unapprovedHits.Load() != 0 {
		t.Fatalf("CSP-blocked origin reached its controlled backend %d times", unapprovedHits.Load())
	}
	if root["id"] == "" {
		t.Fatal("acceptance snapshot ID was not returned")
	}
	t.Log("controlled browser CORS acceptance passed: exact allowed origin, missing/foreign CORS rejection, 503, AbortController timeout, retry recovery, and CSP-blocked unregistered origin")
}

func listenerPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	address, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok || address.Port < 1 {
		t.Fatalf("test server listener address is not a TCP port: %v", server.Listener.Addr())
	}
	return address.Port
}

func assertExactConnectSource(t *testing.T, policy, allowedOrigin, unapprovedOrigin string) {
	t.Helper()
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(strings.TrimSpace(directive))
		if len(fields) == 0 || fields[0] != "connect-src" {
			continue
		}
		if len(fields) != 3 || fields[1] != "'self'" || fields[2] != allowedOrigin {
			t.Fatalf("connect-src must contain only self and the exact configured origin: %q", directive)
		}
		if strings.Contains(policy, unapprovedOrigin) {
			t.Fatalf("unregistered origin leaked into CSP connect-src: %q", policy)
		}
		return
	}
	t.Fatalf("CSP has no explicit connect-src directive: %q", policy)
}

func requireUniqueExternalBackendBrowserDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	if raw == "" || err != nil {
		t.Skip("set TEST_DATABASE_URL to a unique local website_engine_external_backend_browser_qa_* disposable database")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(databaseName, "website_engine_external_backend_browser_qa_") ||
		(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Skip("controlled CORS acceptance requires a local website_engine_external_backend_browser_qa_* disposable database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to controlled CORS disposable database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var sessions int
	if err := connection.QueryRow(ctx, "SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())").Scan(&currentDatabase, &sessions); err != nil {
		t.Fatalf("verify controlled CORS disposable database and active sessions: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("TEST_DATABASE_URL database %q does not match URL name %q", currentDatabase, databaseName)
	}
	if sessions != 0 {
		t.Fatalf("refusing newTestEnv TRUNCATE: disposable database %q has %d other active sessions", currentDatabase, sessions)
	}
	t.Logf("verified controlled CORS disposable database %s has no other sessions before integration setup", currentDatabase)
}
