package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSystemDocumentStatesWorkWithoutJavaScript(t *testing.T) {
	for code, title := range map[int]string{404: "Страница не найдена", 403: "Доступ ограничен", 500: "Сайт временно недоступен", 503: "Сайт ещё не настроен"} {
		document := SystemDocument(code)
		if document.Status != code || !strings.Contains(string(document.HTML), title) || !strings.Contains(string(document.HTML), "noindex,nofollow") {
			t.Fatalf("bad system document %d: %s", code, document.HTML)
		}
	}
}

func TestParseWorkerDiagnosticsAcceptsOnlyBoundedTimingPhases(t *testing.T) {
	diagnostics := parseWorkerDiagnostics(strings.Join([]string{
		"timing phase=browser_launch ms=14",
		"timing phase=context_creation ms=0",
		"timing phase=navigation ms=120",
		"timing phase=runtime_data ms=74",
		"timing phase=readiness ms=38",
		"timing phase=serialization ms=2",
		"timing phase=database ms=1",
		"timing phase=navigation ms=-1",
		"timing phase=runtime_data ms=300001",
		"timing phase=readiness ms=300001",
		"timing phase=serialization ms=2 path=/private/secret",
		"timing phase=runtime_data ms=3 url=/api/v2/content/private-record/records?token=secret",
	}, "\n"))
	want := map[string]int{
		"browser_launch":   14,
		"context_creation": 0,
		"navigation":       120,
		"runtime_data":     74,
		"readiness":        38,
		"serialization":    2,
	}
	if len(diagnostics.timings) != len(want) {
		t.Fatalf("accepted timing phases = %#v, want %#v", diagnostics.timings, want)
	}
	for phase, duration := range want {
		if diagnostics.timings[phase] != duration {
			t.Fatalf("timing %s = %d ms, want %d ms", phase, diagnostics.timings[phase], duration)
		}
	}
}

func TestDocumentHandlerRetainsStatusAndHEADSemantics(t *testing.T) {
	handler := DocumentHandler("", func(context.Context, string, string) (Document, error) {
		return SystemDocument(http.StatusNotFound), nil
	}, nil, time.Hour)
	for _, method := range []string{"GET", "HEAD"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, "/missing", nil))
		if recorder.Code != 404 {
			t.Fatalf("%s returned %d", method, recorder.Code)
		}
		if method == "HEAD" && recorder.Body.Len() != 0 {
			t.Fatal("HEAD returned body")
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("page error cache policy missing")
		}
	}
}

func TestDocumentHandlerServesEmbeddedFaviconWithoutSiteRoute(t *testing.T) {
	resolved := false
	handler := DocumentHandler("", func(context.Context, string, string) (Document, error) {
		resolved = true
		return SystemDocument(http.StatusServiceUnavailable), nil
	}, nil, 3*time.Hour)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, "/favicon.svg", nil))
		if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "image/svg+xml") {
			t.Fatalf("%s favicon response: status=%d headers=%v body=%q", method, recorder.Code, recorder.Header(), recorder.Body.String())
		}
		if recorder.Header().Get("Cache-Control") != "public, max-age=10800" {
			t.Fatalf("favicon cache policy = %q", recorder.Header().Get("Cache-Control"))
		}
		if method == http.MethodGet && !strings.Contains(recorder.Body.String(), "<svg") {
			t.Fatalf("favicon response did not contain SVG data: %q", recorder.Body.String())
		}
		if method == http.MethodHead && recorder.Body.Len() != 0 {
			t.Fatal("HEAD favicon response had a body")
		}
	}
	if resolved {
		t.Fatal("embedded favicon request was treated as a page route")
	}
}

func TestDocumentHandlerMissingScriptDoesNotResolveAsSitePage(t *testing.T) {
	resolved := false
	handler := DocumentHandler("", func(context.Context, string, string) (Document, error) {
		resolved = true
		return SystemDocument(http.StatusServiceUnavailable), nil
	}, nil, time.Hour)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing.js", nil))
	if recorder.Code != http.StatusNotFound || !resolved {
		t.Fatalf("missing script must resolve routing then return 404, got status %d (resolved %t)", recorder.Code, resolved)
	}
	handler = DocumentHandler("", func(context.Context, string, string) (Document, error) {
		return Document{HTML: []byte("<main>valid page</main>"), Status: http.StatusOK}, nil
	}, nil, time.Hour)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/real.page.js", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "valid page") {
		t.Fatalf("configured page must take precedence over suffix: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPrerenderWorkerRendersAndRejectsSideEffects(t *testing.T) {
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		t.Skip("set PRERENDER_TEST_NODE/MODULE/BROWSER to exercise the installed browser")
	}
	worker, err := filepath.Abs("../../scripts/prerender-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	options := PrerenderOptions{NodePath: node, PlaywrightModule: module, BrowserPath: browser, WorkerPath: worker, Timeout: 15 * time.Second, AttemptTimeout: 15 * time.Second, MaxAttempts: 1, QuietInterval: 100 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1, QueueSize: 2, CacheEntries: 2, MaxHTMLBytes: 1 << 20, HeapMB: 128, ViewportWidth: 1280, ViewportHeight: 720, MaxRequests: 100, MaxNetworkBytes: 16 << 20}
	var forbiddenRequests atomic.Int32
	var faviconRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/runtime":
			time.Sleep(75 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"private":"private-runtime-data","snapshot":"private-snapshot-id"}`))
			return
		case "/api/v2/content/private-record-id/records":
			time.Sleep(75 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"records":[{"title":"private-content-record"}]}`))
			return
		case "/favicon.svg":
			faviconRequests.Add(1)
			time.Sleep(350 * time.Millisecond) // Must not be counted as runtime/data loading.
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><circle r="1"/></svg>`))
			return
		}
		if r.URL.Path == "/api/v2/admin/private" || r.Method == "POST" {
			forbiddenRequests.Add(1)
		}
		if r.URL.Path == "/assets/fixture.js" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`Promise.all([fetch('/api/v2/runtime?token=private-query-secret').then(response=>response.json()),fetch('/api/v2/content/private-record-id/records?token=private-record-query-secret').then(response=>response.json())]).then(()=>{document.getElementById('root').innerHTML='<main data-snapshot="fixture" data-content-revision="7"><h1>Published content</h1><form><button>Send</button></form></main>';document.documentElement.dataset.engineReady='true';});fetch('/favicon.svg').catch(()=>{});if(location.search.includes('unsafe')){fetch('/api/v2/forms/test/submissions',{method:'POST'}).catch(()=>{});fetch('/api/v2/admin/private').catch(()=>{});}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><link rel="icon" href="/favicon.svg"><script type="module" src="/assets/fixture.js"></script></head><body><div id="root"></div></body></html>`))
	}))
	defer server.Close()
	renderer, err := NewPrerenderer(server.URL, options)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	renderer.SetLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	document := Document{Status: 200, CacheKey: "fixture7", SnapshotID: "fixture", ContentRevision: 7}
	output, err := renderer.Render(context.Background(), document, "/", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Published content", `id="root"`, `id="engine-live-root" hidden=""`, `inert=""`} {
		if !strings.Contains(string(output), text) {
			t.Fatalf("missing %q: %s", text, output)
		}
	}
	if strings.Contains(string(output), `id="prerender-root"`) {
		t.Fatalf("the HTTP document must keep prerendered content under #root until JavaScript starts: %s", output)
	}
	// A current snapshot/content identity is required; an old render cannot be
	// cached as though it described the next publication.
	wrong := document
	wrong.ContentRevision = 8
	wrong.CacheKey = "fixture8"
	if _, err := renderer.Render(context.Background(), wrong, "/", ""); err == nil {
		t.Fatal("rendered the wrong content revision")
	}
	// Query-dependent server output must have its own render identity. Reusing
	// the cached safe document would skip the worker and never exercise its
	// request policy.
	unsafeDocument := document
	unsafeDocument.CacheKey = "fixture-unsafe"
	if _, err := renderer.Render(context.Background(), unsafeDocument, "/", "unsafe=1"); err == nil {
		t.Fatal("side effect attempt should fail generation")
	}
	if forbiddenRequests.Load() != 0 {
		t.Fatalf("worker allowed %d side effect/admin requests", forbiddenRequests.Load())
	}
	if faviconRequests.Load() == 0 {
		t.Fatal("worker did not allow a same-origin favicon request")
	}
	var firstRender map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("parse safe render metrics %q: %v", line, err)
		}
		if record["msg"] == "prerender render" && record["cache_result"] == "miss" {
			firstRender = record
			break
		}
	}
	if firstRender == nil {
		t.Fatalf("successful render metrics were not logged: %s", logs.String())
	}
	for _, phase := range []string{"browser_launch_ms", "context_creation_ms", "navigation_ms", "runtime_data_ms", "readiness_ms", "serialization_ms"} {
		if _, ok := firstRender[phase]; !ok {
			t.Fatalf("successful render metrics are missing %s: %#v", phase, firstRender)
		}
	}
	if duration, ok := firstRender["runtime_data_ms"].(float64); !ok || duration <= 0 || duration >= 300 {
		t.Fatalf("public runtime/data response lifecycle was not measured: %#v", firstRender)
	}
	for _, privateValue := range []string{
		"private-query-secret", "private-record-query-secret", "private-record-id", "private-runtime-data",
		"private-snapshot-id", "private-content-record", "/api/v2/runtime?token=", "/api/v2/content/private-record-id/records",
	} {
		if strings.Contains(logs.String(), privateValue) {
			t.Fatalf("worker stage diagnostics leaked request/page data %q: %s", privateValue, logs.String())
		}
	}
}

func TestPrerenderWorkerReadinessTimeoutHasSafeDiagnostics(t *testing.T) {
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		t.Skip("set PRERENDER_TEST_NODE/MODULE/BROWSER to exercise the installed browser")
	}
	worker, err := filepath.Abs("../../scripts/prerender-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	options := prerenderTestOptions(node)
	options.WorkerPath = worker
	options.PlaywrightModule = module
	options.BrowserPath = browser
	options.Timeout = 10 * time.Second
	options.AttemptTimeout = 8 * time.Second
	options.HeapMB = 128
	options.MaxRequests = 10
	options.MaxNetworkBytes = 1 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/runtime":
			http.Error(w, "private-response-body", http.StatusServiceUnavailable)
			return
		case "/assets/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`document.getElementById("root").innerHTML = '<main data-snapshot="fixture" data-content-revision="7"><div data-engine-placement="private-feature-key"><div class="runtime-feature-error">private-page-content</div></div></main>'; fetch("/api/v2/runtime").catch(() => {}); fetch("/api/v2/admin/private?token=private-secret").catch(() => {}); setTimeout(() => { throw new Error("private-js-error-message"); }, 0);`))
			return
		}
		time.Sleep(600 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><head><title>private-page-title</title><script type=\"module\" src=\"/assets/fixture.js\"></script></head><body><div id=\"root\"></div></body></html>"))
	}))
	defer server.Close()
	renderer, err := NewPrerenderer(server.URL, options)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	renderer.SetLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	_, err = renderer.Render(context.Background(), Document{Status: http.StatusOK, CacheKey: "readiness-fixture", SnapshotID: "fixture", ContentRevision: 7}, "/", "")
	if err == nil {
		t.Fatal("worker must reject a page that never becomes ready")
	}
	for _, expected := range []string{
		"\"worker_stage\":\"readiness_timeout\"",
		"\"readiness_snapshot_match\":true",
		"\"readiness_revision_match\":true",
		"\"readiness_engine_ready\":false",
		"\"readiness_placements\":1",
		"\"readiness_ready\":0",
		"\"readiness_errors\":1",
		"\"readiness_request_failures\":1",
		"\"readiness_failed_responses\":1",
		"\"readiness_page_errors\":1",
		"\"readiness_policy_blocked_requests\":1",
		"\"readiness_origin_blocks\":0",
		"\"readiness_method_blocks\":0",
		"\"readiness_path_blocks\":1",
		"\"readiness_request_limit_blocks\":0",
		"\"readiness_request_failure_aborted\":0",
		"\"readiness_request_failure_failed\":0",
		"\"readiness_request_failure_blocked_by_client\":0",
		"\"readiness_request_failure_connection\":0",
		"\"readiness_request_failure_other\":0",
		"\"readiness_request_failure_policy_abort\":1",
		"\"readiness_aborted_script\":0",
		"\"readiness_aborted_stylesheet\":0",
		"\"readiness_aborted_fetch_xhr\":0",
		"\"readiness_aborted_image\":0",
		"\"readiness_aborted_other\":0",
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("diagnostics missing %q: %s", expected, logs.String())
		}
	}
	for _, privateValue := range []string{"private-page-title", "private-page-content", "private-feature-key", "private-response-body", "private-js-error-message", "private-secret", "/api/v2/admin/private", server.URL} {
		if strings.Contains(logs.String(), privateValue) || strings.Contains(err.Error(), privateValue) {
			t.Fatalf("private page value leaked into diagnostics: %q", privateValue)
		}
	}
}

func TestPrerenderWorkerHandlesFallbackStylesheetReplacement(t *testing.T) {
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		t.Skip("set PRERENDER_TEST_NODE/MODULE/BROWSER to exercise the installed browser")
	}
	worker, err := filepath.Abs("../../scripts/prerender-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	options := prerenderTestOptions(node)
	options.WorkerPath = worker
	options.PlaywrightModule = module
	options.BrowserPath = browser
	options.Timeout = 10 * time.Second
	options.AttemptTimeout = 8 * time.Second
	options.QuietInterval = 100 * time.Millisecond
	options.HeapMB = 128
	options.MaxHTMLBytes = 1 << 20
	options.MaxRequests = 16
	options.MaxNetworkBytes = 1 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets/feature-a.css" || r.URL.Path == "/assets/feature-b.css" {
			time.Sleep(800 * time.Millisecond)
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			if r.URL.Path == "/assets/feature-b.css" {
				_, _ = w.Write([]byte("@font-face { font-family: fixture; src: url('/fixture.woff2'); } .fixture { font-family: fixture; }"))
				return
			}
			_, _ = w.Write([]byte(".fixture { color: black; }"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>fallback fixture</title><script>const replaceFallback=()=>{const fallback=[...document.querySelectorAll('link[data-feature-style]')];if(fallback.length!==2)return;clearInterval(timer);let left=fallback.length;for(const oldLink of fallback){const link=document.createElement('link');link.rel='stylesheet';link.href=oldLink.href;link.dataset.featureStyle=oldLink.dataset.featureStyle;link.onload=()=>{link.dataset.loaded='true';if(--left===0)document.documentElement.dataset.engineReady='true'};oldLink.replaceWith(link)}};const timer=setInterval(replaceFallback,1)</script><link rel="stylesheet" href="/assets/feature-a.css" data-feature-style="/assets/feature-a.css"><link rel="stylesheet" href="/assets/feature-b.css" data-feature-style="/assets/feature-b.css"></head><body><div id="root"><main data-snapshot="fixture" data-content-revision="7"><div data-engine-placement="a" data-runtime-feature-ready></div><div data-engine-placement="b" data-runtime-feature-ready></div></main></div></body></html>`))
	}))
	defer server.Close()
	renderer, err := NewPrerenderer(server.URL, options)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	renderer.SetLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	output, err := renderer.Render(context.Background(), Document{Status: http.StatusOK, CacheKey: "fallback-style-fixture", SnapshotID: "fixture", ContentRevision: 7}, "/", "")
	if err != nil {
		t.Fatalf("replacing loading no-JS styles must not fail prerender: %v; logs=%s", err, logs.String())
	}
	if !bytes.HasPrefix(output, []byte("<!DOCTYPE html>")) {
		t.Fatalf("worker returned invalid HTML: %q", output)
	}
	if got := string(output); !strings.Contains(got, `<style data-feature-style="/assets/feature-a.css" data-loaded="true">.fixture { color: black; }</style>`) {
		t.Fatalf("self-contained feature stylesheet was not inlined: %s", got)
	} else if !strings.Contains(got, `href="`+server.URL+`/assets/feature-b.css"`) || !strings.Contains(got, `data-feature-style="/assets/feature-b.css" data-loaded="true"`) {
		t.Fatalf("stylesheet with external font dependency was not preserved as a link: %s", got)
	}
	for _, expected := range []string{
		`"outcome":"success"`,
		`"worker_stage":"serialized"`,
		`"readiness_snapshot_match":true`,
		`"readiness_revision_match":true`,
		`"readiness_engine_ready":true`,
		`"readiness_placements":2`,
		`"readiness_ready":2`,
		`"readiness_errors":0`,
		`"readiness_request_failure_aborted":0`,
		`"readiness_aborted_stylesheet":0`,
		`"readiness_aborted_script":0`,
		`"readiness_aborted_fetch_xhr":0`,
		`"readiness_aborted_image":0`,
		`"readiness_aborted_other":0`,
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("safe readiness metrics missing %q: %s", expected, logs.String())
		}
	}
	for _, privateValue := range []string{"fallback fixture", server.URL, "/assets/feature-a.css", "/assets/feature-b.css"} {
		if strings.Contains(logs.String(), privateValue) {
			t.Fatalf("fixture diagnostic leaked page or URL data %q", privateValue)
		}
	}
}

func TestWorkerStageDoesNotExposeDiagnosticContent(t *testing.T) {
	if got := workerStage("stage=module\nstage=context\nsecret-token-and-url\nstage=unknown\n"); got != "context" {
		t.Fatalf("unexpected stage: %s", got)
	}
	if got := workerStage("secret-token"); got != "startup" {
		t.Fatalf("unexpected fallback stage: %s", got)
	}
}

func TestParseWorkerDiagnosticsAcceptsOnlyBoundedReadinessValues(t *testing.T) {
	got := parseWorkerDiagnostics("stage=navigation\nstage=readiness_timeout\nreadiness snapshot_match=true revision_match=false engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18\nsecret-token-and-url\n")
	if got.stage != "readiness_timeout" || got.readiness == nil {
		t.Fatalf("valid readiness diagnostic was not parsed: %+v", got)
	}
	if !got.readiness.snapshotMatch || got.readiness.revisionMatch || got.readiness.engineReady || got.readiness.placements != 3 || got.readiness.ready != 2 || got.readiness.errors != 1 || got.readiness.requestFailures != 4 || got.readiness.failedResponses != 5 || got.readiness.pageErrors != 6 || got.readiness.policyBlockedRequests != 7 || got.readiness.originBlocks != 1 || got.readiness.methodBlocks != 2 || got.readiness.pathBlocks != 3 || got.readiness.requestLimitBlocks != 1 || got.readiness.requestFailureAborted != 8 || got.readiness.requestFailureFailed != 9 || got.readiness.requestFailureBlocked != 10 || got.readiness.requestFailureConnection != 11 || got.readiness.requestFailureOther != 12 || got.readiness.requestFailurePolicyAbort != 13 || got.readiness.abortedScripts != 14 || got.readiness.abortedStylesheets != 15 || got.readiness.abortedFetchXHR != 16 || got.readiness.abortedImages != 17 || got.readiness.abortedOther != 18 {
		t.Fatalf("unexpected normalized readiness diagnostic: %+v", got.readiness)
	}
	for _, malformed := range []string{
		"readiness snapshot_match=yes revision_match=false engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18",
		"readiness snapshot_match=true revision_match=false engine_ready=false placements=10001 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18",
		"readiness snapshot_match=true revision_match=false engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=10001 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18",
		"readiness snapshot_match=true revision_match=false engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=10001 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18",
		"readiness snapshot_match=true revision_match=false engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=10001 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18",
		"readiness snapshot_match=true revision_match=false engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18 route=/private?token=secret",
		"readiness snapshot_match=true revision_match=false engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=10001",
	} {
		if parsed := parseWorkerDiagnostics("stage=navigation\n" + malformed); parsed.readiness != nil {
			t.Fatalf("malformed diagnostic was accepted: %q -> %+v", malformed, parsed.readiness)
		}
	}
}

func TestPrerenderCacheEvictsLeastRecentlyUsedPage(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	script := "#!/bin/sh\ncount_file=\"$(dirname \"$0\")/calls\"\nprintf x >> \"$count_file\"\nprintf '<!DOCTYPE html><html></html>'\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	options := PrerenderOptions{NodePath: program, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused", Timeout: 5 * time.Second, AttemptTimeout: 5 * time.Second, MaxAttempts: 1, QuietInterval: 10 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1, QueueSize: 2, CacheEntries: 2, MaxHTMLBytes: 1024, HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024}
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	renderer.SetLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	render := func(key string) {
		t.Helper()
		if _, err := renderer.Render(context.Background(), Document{CacheKey: key, SnapshotID: "snapshot", Cacheable: true}, "/", ""); err != nil {
			t.Fatalf("render %s: %v", key, err)
		}
	}
	count := func() int {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "calls"))
		if err != nil {
			t.Fatal(err)
		}
		return len(data)
	}

	render("A")
	render("B")
	render("A") // Refresh A's recency without extending its TTL.
	render("C") // B is least recently used and should be evicted.
	render("A")
	if got := count(); got != 3 {
		t.Fatalf("recent A was evicted: worker calls=%d, want 3", got)
	}
	render("B")
	if got := count(); got != 4 {
		t.Fatalf("old B should have been evicted: worker calls=%d, want 4", got)
	}
	if len(renderer.cache) > options.CacheEntries {
		t.Fatalf("cache exceeded configured cap: %d > %d", len(renderer.cache), options.CacheEntries)
	}
	if !strings.Contains(logs.String(), `"reason":"lru"`) || !strings.Contains(logs.String(), `"entries":1`) {
		t.Fatalf("LRU eviction was not observable: %s", logs.String())
	}
}

func TestPrerenderCacheExpiresAfterConfiguredTTL(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	script := "#!/bin/sh\ncount_file=\"$(dirname \"$0\")/calls\"\nprintf x >> \"$count_file\"\nprintf '<!DOCTYPE html><html></html>'\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	options := PrerenderOptions{NodePath: program, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused", Timeout: 5 * time.Second, AttemptTimeout: 5 * time.Second, MaxAttempts: 1, QuietInterval: 10 * time.Millisecond, CacheTTL: 20 * time.Millisecond, Concurrency: 1, QueueSize: 2, CacheEntries: 2, MaxHTMLBytes: 1024, HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024}
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
	if err != nil {
		t.Fatal(err)
	}
	document := Document{CacheKey: "expiring", SnapshotID: "snapshot", Cacheable: true}
	if _, err := renderer.Render(context.Background(), document, "/", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := renderer.Render(context.Background(), document, "/", ""); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || len(calls) != 2 {
		t.Fatalf("expired HTML should be regenerated, worker calls=%d err=%v", len(calls), err)
	}
}

func TestPrerenderCacheUsesResolvedIdentityInsteadOfRawQuery(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	script := "#!/bin/sh\ncount_file=\"$(dirname \"$0\")/calls\"\nprintf x >> \"$count_file\"\nprintf '<!DOCTYPE html><html></html>'\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	options := PrerenderOptions{NodePath: program, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused", Timeout: 5 * time.Second, AttemptTimeout: 5 * time.Second, MaxAttempts: 1, QuietInterval: 10 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1, QueueSize: 2, CacheEntries: 4, MaxHTMLBytes: 1024, HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024}
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
	if err != nil {
		t.Fatal(err)
	}
	render := func(document Document, path, query string) {
		t.Helper()
		if _, err := renderer.Render(context.Background(), document, path, query); err != nil {
			t.Fatalf("render %s?%s: %v", path, query, err)
		}
	}
	alpha := Document{CacheKey: "resolved-filter-alpha", SnapshotID: "snapshot", Cacheable: true}
	render(alpha, "/search", "filter=alpha&utm_source=one")
	// PageDocument resolves filter=alpha to the same feature argument; raw
	// query ordering and an unrelated tracking value do not change its key.
	render(alpha, "/search", "utm_source=two&filter=alpha")
	render(Document{CacheKey: "resolved-filter-beta", SnapshotID: "snapshot", Cacheable: true}, "/search", "filter=beta")
	// Route path remains part of the cache identity even for equal document IDs.
	render(alpha, "/product/alpha", "filter=alpha")
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected one cache hit for equivalent query and distinct renders for bound value/path, got %d workers", len(calls))
	}
}

func TestPrerenderBypassesCacheForQueryDependentDocuments(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	countFile := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\npayload=\"$(cat)\"\nprintf x >> %q\ncase \"$payload\" in\n  *'filter=alpha'*) value=alpha ;;\n  *'filter=beta'*) value=beta ;;\n  *) echo 'missing query value' >&2; exit 1 ;;\nesac\nprintf '<!DOCTYPE html><html><body>%%s</body></html>' \"$value\"\n", countFile)
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", prerenderTestOptions(program))
	if err != nil {
		t.Fatal(err)
	}
	document := Document{CacheKey: "same-resolved-document", SnapshotID: "snapshot", Cacheable: false}
	for _, check := range []struct{ query, want string }{{"filter=alpha", "alpha"}, {"filter=beta", "beta"}, {"filter=alpha", "alpha"}} {
		data, renderErr := renderer.Render(context.Background(), document, "/search", check.query)
		if renderErr != nil {
			t.Fatalf("render %s: %v", check.query, renderErr)
		}
		if !strings.Contains(string(data), "<body>"+check.want+"</body>") {
			t.Fatalf("query %s returned mismatched HTML: %s", check.query, data)
		}
	}
	calls, err := os.ReadFile(countFile)
	if err != nil || len(calls) != 3 {
		t.Fatalf("uncacheable query documents should render independently, worker calls=%d err=%v", len(calls), err)
	}
	if len(renderer.cache) != 0 || len(renderer.pending) != 0 {
		t.Fatalf("uncacheable documents were retained: cache=%d pending=%d", len(renderer.cache), len(renderer.pending))
	}
}

func TestPrerenderConcurrentRequestsShareOneGeneration(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	script := "#!/bin/sh\ncount_file=\"$(dirname \"$0\")/calls\"\nprintf x >> \"$count_file\"\nsleep 0.15\nprintf '<!DOCTYPE html><html></html>'\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	options := PrerenderOptions{NodePath: program, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused", Timeout: 5 * time.Second, AttemptTimeout: 5 * time.Second, MaxAttempts: 1, QuietInterval: 10 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 2, QueueSize: 2, CacheEntries: 2, MaxHTMLBytes: 1024, HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024}
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
	if err != nil {
		t.Fatal(err)
	}
	const requests = 8
	results := make(chan error, requests)
	var wait sync.WaitGroup
	for range requests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			data, err := renderer.Render(context.Background(), Document{CacheKey: "same", SnapshotID: "snapshot", Cacheable: true}, "/", "")
			if err == nil && !strings.HasPrefix(string(data), "<!DOCTYPE html>") {
				err = errors.New("worker returned invalid HTML")
			}
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || len(data) != 1 {
		t.Fatalf("concurrent identical requests should share one generation, calls=%d err=%v", len(data), err)
	}
}

func TestPrerenderTimeoutLogsOnlySafeStage(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'stage=readiness_timeout\\nreadiness snapshot_match=true revision_match=true engine_ready=false placements=3 ready=2 errors=1 request_failures=4 failed_responses=5 page_errors=6 policy_blocked_requests=7 origin_blocks=1 method_blocks=2 path_blocks=3 request_limit_blocks=1 request_failure_aborted=8 request_failure_failed=9 request_failure_blocked_by_client=10 request_failure_connection=11 request_failure_other=12 request_failure_policy_abort=13 aborted_script=14 aborted_stylesheet=15 aborted_fetch_xhr=16 aborted_image=17 aborted_other=18\\nprivate-secret\\n' >&2\nsleep 5\n"), 0700); err != nil {
		t.Fatal(err)
	}
	options := PrerenderOptions{NodePath: program, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused", Timeout: 2 * time.Second, AttemptTimeout: 2 * time.Second, MaxAttempts: 1, QuietInterval: 10 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1, QueueSize: 1, CacheEntries: 1, MaxHTMLBytes: 1024, HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024}
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	renderer.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	_, err = renderer.Render(context.Background(), Document{CacheKey: "key", SnapshotID: "id"}, "/", "")
	if err == nil {
		t.Fatal("worker must time out")
	}
	if !strings.Contains(logs.String(), "stage=readiness_timeout") || !strings.Contains(logs.String(), "reason=timeout") || !strings.Contains(logs.String(), "readiness_snapshot_match=true") || !strings.Contains(logs.String(), "readiness_revision_match=true") || !strings.Contains(logs.String(), "readiness_engine_ready=false") || !strings.Contains(logs.String(), "readiness_placements=3") || !strings.Contains(logs.String(), "readiness_ready=2") || !strings.Contains(logs.String(), "readiness_errors=1") || !strings.Contains(logs.String(), "readiness_request_failures=4") || !strings.Contains(logs.String(), "readiness_failed_responses=5") || !strings.Contains(logs.String(), "readiness_page_errors=6") || !strings.Contains(logs.String(), "readiness_policy_blocked_requests=7") || !strings.Contains(logs.String(), "readiness_path_blocks=3") || !strings.Contains(logs.String(), "readiness_request_failure_aborted=8") || !strings.Contains(logs.String(), "readiness_request_failure_failed=9") || !strings.Contains(logs.String(), "readiness_request_failure_blocked_by_client=10") || !strings.Contains(logs.String(), "readiness_request_failure_connection=11") || !strings.Contains(logs.String(), "readiness_request_failure_other=12") || !strings.Contains(logs.String(), "readiness_request_failure_policy_abort=13") || !strings.Contains(logs.String(), "readiness_aborted_script=14") || !strings.Contains(logs.String(), "readiness_aborted_stylesheet=15") || !strings.Contains(logs.String(), "readiness_aborted_fetch_xhr=16") || !strings.Contains(logs.String(), "readiness_aborted_image=17") || !strings.Contains(logs.String(), "readiness_aborted_other=18") || strings.Contains(logs.String(), "private-secret") || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("unsafe or incomplete diagnostics: %s", logs.String())
	}
}

func TestPrerenderRetriesOnlyHungBrowserStartup(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	script := "#!/bin/sh\nstate=\"$(dirname \"$0\")/attempted\"\nif [ ! -f \"$state\" ]; then touch \"$state\"; printf 'stage=browser\\n' >&2; sleep 5; else printf 'stage=context\\n' >&2; printf '<!DOCTYPE html><html></html>'; fi\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	opts := PrerenderOptions{NodePath: program, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused", Timeout: 6 * time.Second, AttemptTimeout: 2 * time.Second, MaxAttempts: 2, QuietInterval: 10 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1, QueueSize: 1, CacheEntries: 1, MaxHTMLBytes: 1024, HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024}
	p, err := NewPrerenderer("http://127.0.0.1:12345", opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := p.Render(context.Background(), Document{CacheKey: "retry", SnapshotID: "id"}, "/", "")
	if err != nil || !strings.Contains(string(data), "<!DOCTYPE html>") {
		t.Fatalf("startup retry failed: %s %v", data, err)
	}
}

func TestPrerenderDoesNotRetryAfterContextStarted(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	script := "#!/bin/sh\nprintf x >> \"$(dirname \"$0\")/calls\"\nprintf 'stage=context\\n' >&2\nsleep 5\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	opts := PrerenderOptions{NodePath: program, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused", Timeout: 6 * time.Second, AttemptTimeout: 2 * time.Second, MaxAttempts: 2, QuietInterval: 10 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1, QueueSize: 1, CacheEntries: 1, MaxHTMLBytes: 1024, HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024}
	p, err := NewPrerenderer("http://127.0.0.1:12345", opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Render(context.Background(), Document{CacheKey: "no-retry", SnapshotID: "id"}, "/", ""); err == nil {
		t.Fatal("expected timeout")
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || string(calls) != "x" {
		t.Fatalf("context failure retried: %q %v", calls, err)
	}
}

func TestPrerenderLogsCacheAndSafeWorkerMeasurements(t *testing.T) {
	dir := t.TempDir()
	successWorker := filepath.Join(dir, "success-worker")
	successScript := "#!/bin/sh\nprintf 'stage=serialized\\n' >&2\nprintf '<!DOCTYPE html><html><body>private-page-content</body></html>'\n"
	if err := os.WriteFile(successWorker, []byte(successScript), 0700); err != nil {
		t.Fatal(err)
	}
	options := prerenderTestOptions(successWorker)
	options.CacheEntries = 4
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	renderer.SetLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	secretPath := "/private-route?access_token=route-secret"
	document := Document{CacheKey: "identity-secret", SnapshotID: "snapshot-secret", Cacheable: true}
	for i := 0; i < 2; i++ {
		if _, err := renderer.Render(context.Background(), document, secretPath, "access_token=query-secret"); err != nil {
			t.Fatal(err)
		}
	}

	failureWorker := filepath.Join(dir, "failure-worker")
	failureScript := "#!/bin/sh\nprintf 'stage=navigation\\nprivate-worker-stderr\\n' >&2\nexit 9\n"
	if err := os.WriteFile(failureWorker, []byte(failureScript), 0700); err != nil {
		t.Fatal(err)
	}
	failureOptions := prerenderTestOptions(failureWorker)
	failureRenderer, err := NewPrerenderer("http://127.0.0.1:12345", failureOptions)
	if err != nil {
		t.Fatal(err)
	}
	failureRenderer.SetLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if _, err := failureRenderer.Render(context.Background(), Document{CacheKey: "failure", SnapshotID: "snapshot", Cacheable: true}, secretPath, "access_token=query-secret"); err == nil {
		t.Fatal("expected worker failure")
	}

	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("parse structured log %q: %v", line, err)
		}
		if record["msg"] == "prerender render" {
			records = append(records, record)
		}
	}
	if len(records) != 3 {
		t.Fatalf("got %d render events, want miss, hit, and failure: %s", len(records), logs.String())
	}
	if records[0]["cache_result"] != "miss" || records[0]["outcome"] != "success" || records[0]["cache_miss_reason"] != "absent" {
		t.Fatalf("first request should be a generated cache miss: %#v", records[0])
	}
	if records[1]["cache_result"] != "hit" || records[1]["outcome"] != "success" {
		t.Fatalf("second request should be a cache hit: %#v", records[1])
	}
	if records[2]["cache_result"] != "miss" || records[2]["outcome"] != "error" || records[2]["error_code"] != "worker_error" {
		t.Fatalf("failed worker request should be an uncached error: %#v", records[2])
	}
	for _, index := range []int{0, 2} {
		for _, field := range []string{"queue_wait_ms", "worker_duration_ms", "worker_attempts", "worker_stage", "render_duration_ms"} {
			if _, ok := records[index][field]; !ok {
				t.Fatalf("render event missing %s: %#v", field, records[index])
			}
		}
	}
	if _, ok := records[1]["worker_duration_ms"]; ok {
		t.Fatalf("cache hit must not claim a worker run: %#v", records[1])
	}
	for _, secret := range []string{"route-secret", "query-secret", "identity-secret", "snapshot-secret", "private-page-content", "private-worker-stderr", secretPath} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("structured diagnostics leaked sensitive value %q: %s", secret, logs.String())
		}
	}
}

func TestPrerenderQueueMeasurementsIncludeWait(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "worker")
	callsPath := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\nsleep 0.2\nprintf '<!DOCTYPE html><html></html>'\n", callsPath)
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	options := prerenderTestOptions(program)
	options.Concurrency = 1
	options.QueueSize = 2
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	renderer.SetLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	results := make(chan error, 2)
	go func() {
		_, err := renderer.Render(context.Background(), Document{CacheKey: "first", SnapshotID: "snapshot", Cacheable: true}, "/", "")
		results <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(callsPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first worker did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	go func() {
		_, err := renderer.Render(context.Background(), Document{CacheKey: "second", SnapshotID: "snapshot", Cacheable: true}, "/", "")
		results <- err
	}()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	var longestWait float64
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] == "prerender render" && record["cache_result"] == "miss" {
			if wait, ok := record["queue_wait_ms"].(float64); ok && wait > longestWait {
				longestWait = wait
			}
		}
	}
	if longestWait < 75 {
		t.Fatalf("queue wait was not captured for the serialized worker: %fms logs=%s", longestWait, logs.String())
	}
}

func prerenderTestOptions(nodePath string) PrerenderOptions {
	return PrerenderOptions{
		NodePath: nodePath, WorkerPath: "unused", PlaywrightModule: "unused", BrowserPath: "unused",
		Timeout: 5 * time.Second, AttemptTimeout: 5 * time.Second, MaxAttempts: 1,
		QuietInterval: 10 * time.Millisecond, CacheTTL: time.Minute,
		Concurrency: 2, QueueSize: 4, CacheEntries: 2, MaxHTMLBytes: 1024, HeapMB: 32,
		ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 1, MaxNetworkBytes: 1024,
	}
}

func BenchmarkPrerenderCache(b *testing.B) {
	for _, test := range []struct {
		name string
		cold bool
	}{{name: "cold", cold: true}, {name: "warm"}} {
		b.Run(test.name, func(b *testing.B) {
			dir := b.TempDir()
			program := filepath.Join(dir, "worker")
			if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '<!DOCTYPE html><html></html>'\n"), 0700); err != nil {
				b.Fatal(err)
			}
			options := prerenderTestOptions(program)
			options.CacheEntries = 1
			renderer, err := NewPrerenderer("http://127.0.0.1:12345", options)
			if err != nil {
				b.Fatal(err)
			}
			document := Document{CacheKey: "benchmark-warm", SnapshotID: "snapshot", Cacheable: true}
			if !test.cold {
				if _, err := renderer.Render(context.Background(), document, "/", ""); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if test.cold {
					document.CacheKey = fmt.Sprintf("benchmark-cold-%d", i)
				}
				if _, err := renderer.Render(context.Background(), document, "/", ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
