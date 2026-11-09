package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocumentHandlerLogsOnlyBoundedResolverPhaseTiming(t *testing.T) {
	var logOutput bytes.Buffer
	worker := &Prerenderer{}
	worker.SetLogger(slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug})))

	handler := DocumentHandler("", func(_ context.Context, path, query string) (Document, error) {
		if path != "/private-route" || query != "token=secret-value" {
			t.Fatalf("resolver inputs changed: path=%q query=%q", path, query)
		}
		return Document{HTML: []byte("<html><head><title>Page</title></head><body>ok</body></html>"), Status: http.StatusOK}, nil
	}, worker, time.Hour)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/private-route?token=secret-value", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("document status = %d", response.Code)
	}
	logged := logOutput.String()
	if !strings.Contains(logged, "msg=\"prerender phase timing\"") || !strings.Contains(logged, "phase=document_resolve") || !strings.Contains(logged, "duration_ms=") {
		t.Fatalf("resolver phase timing was not logged: %q", logged)
	}
	if strings.Contains(logged, "/private-route") || strings.Contains(logged, "secret-value") {
		t.Fatalf("resolver log contains request data: %q", logged)
	}
}

func TestDocumentHandlerRevalidatesIndexableDocuments(t *testing.T) {
	document := Document{HTML: []byte("<!DOCTYPE html><html><body>published</body></html>"), Status: http.StatusOK, CacheKey: "resolved-runtime-identity", SnapshotID: "snapshot", Cacheable: true}
	handler := DocumentHandler("", func(context.Context, string, string) (Document, error) {
		return document, nil
	}, nil, time.Hour)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if first.Code != http.StatusOK || first.Body.String() != string(document.HTML) {
		t.Fatalf("initial document: status=%d body=%q", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" || strings.ContainsAny(etag, "\r\n,") {
		t.Fatalf("invalid ETag %q", etag)
	}
	if got := first.Header().Get("Cache-Control"); got != "public, max-age=0, must-revalidate" {
		t.Fatalf("cache policy = %q", got)
	}
	previousETag := etag
	document.HTML = []byte("<!DOCTYPE html><html><body>prerendered published</body></html>")
	rendered := httptest.NewRecorder()
	handler.ServeHTTP(rendered, httptest.NewRequest(http.MethodGet, "/", nil))
	etag = rendered.Header().Get("ETag")
	if rendered.Code != http.StatusOK || etag == "" || etag == previousETag {
		t.Fatalf("prerender representation did not receive a new validator: status=%d headers=%v", rendered.Code, rendered.Header())
	}

	for _, validator := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("If-None-Match", validator)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotModified || response.Body.Len() != 0 || response.Header().Get("ETag") != etag {
			t.Errorf("validator %q: status=%d body=%q headers=%v", validator, response.Code, response.Body.String(), response.Header())
		}
	}

	head := httptest.NewRequest(http.MethodHead, "/", nil)
	head.Header.Set("If-None-Match", etag)
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusNotModified || headResponse.Body.Len() != 0 {
		t.Fatalf("conditional HEAD: status=%d body=%q", headResponse.Code, headResponse.Body.String())
	}
}

func TestDocumentHandlerDoesNotCacheNoindexOrErrorPages(t *testing.T) {
	for _, document := range []Document{
		{HTML: []byte("<!DOCTYPE html><html></html>"), Status: http.StatusOK, CacheKey: "noindex", SnapshotID: "snapshot", Cacheable: false},
		{HTML: []byte("<!DOCTYPE html><html><body>not found</body></html>"), Status: http.StatusNotFound, CacheKey: "error", SnapshotID: "snapshot", Cacheable: true},
		SystemDocument(http.StatusServiceUnavailable),
	} {
		handler := DocumentHandler("", func(context.Context, string, string) (Document, error) { return document, nil }, nil, time.Hour)
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("If-None-Match", "*")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != document.Status && !(document.Status == 0 && response.Code == http.StatusOK) {
			t.Errorf("document status = %d, want %d", response.Code, document.Status)
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("ETag") != "" || response.Code == http.StatusNotModified {
			t.Errorf("non-indexable/error document got cache validators: status=%d headers=%v", response.Code, response.Header())
		}
	}
}

func TestDocumentHandlerDoesNotCacheWorkerFailureFallback(t *testing.T) {
	dir := t.TempDir()
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", PrerenderOptions{
		NodePath: filepath.Join(dir, "missing-node"), WorkerPath: "unused-worker", PlaywrightModule: "unused-module", BrowserPath: "unused-browser",
		Timeout: 2 * time.Second, AttemptTimeout: 2 * time.Second, MaxAttempts: 1, QuietInterval: 10 * time.Millisecond,
		CacheTTL: time.Minute, Concurrency: 1, QueueSize: 1, CacheEntries: 2, MaxHTMLBytes: 4096,
		HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 4, MaxNetworkBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := DocumentHandler("", func(_ context.Context, requestPath, _ string) (Document, error) {
		status := http.StatusOK
		if requestPath == "/missing" {
			status = http.StatusNotFound
		}
		return Document{
			HTML: []byte("<!DOCTYPE html><html><body>server shell</body></html>"), Status: status,
			CacheKey: "cache-key-" + requestPath, SnapshotID: "snapshot", Cacheable: true,
		}, nil
	}, renderer, time.Hour)

	for _, check := range []struct {
		path   string
		status int
	}{{path: "/", status: http.StatusOK}, {path: "/missing", status: http.StatusNotFound}} {
		request := httptest.NewRequest(http.MethodGet, check.path, nil)
		request.Header.Set("If-None-Match", "*")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != check.status || response.Header().Get("X-Engine-Prerender") != "unavailable" {
			t.Errorf("worker failure path %s: status=%d headers=%v", check.path, response.Code, response.Header())
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("ETag") != "" || response.Code == http.StatusNotModified {
			t.Errorf("worker failure path %s produced cache validators: status=%d headers=%v", check.path, response.Code, response.Header())
		}
		if response.Body.String() != "<!DOCTYPE html><html><body>server shell</body></html>" {
			t.Errorf("worker failure path %s changed server fallback: %q", check.path, response.Body.String())
		}
	}
}

func TestDocumentHandlerSkipsExternalBackendPrerenderAndLimitsCSPOrigins(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "worker-called")
	fakeNode := filepath.Join(dir, "fake-node")
	script := "#!/bin/sh\nprintf called > \"$PRERENDER_MARKER\"\nprintf '%s' '<!DOCTYPE html><html><body>worker output</body></html>'\n"
	if err := os.WriteFile(fakeNode, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRERENDER_MARKER", marker)
	renderer, err := NewPrerenderer("http://127.0.0.1:12345", PrerenderOptions{
		NodePath: fakeNode, WorkerPath: "unused-worker", PlaywrightModule: "unused-module", BrowserPath: "unused-browser",
		Timeout: 2 * time.Second, AttemptTimeout: 2 * time.Second, MaxAttempts: 1, QuietInterval: 10 * time.Millisecond,
		CacheTTL: time.Minute, Concurrency: 1, QueueSize: 1, CacheEntries: 2, MaxHTMLBytes: 4096,
		HeapMB: 32, ViewportWidth: 320, ViewportHeight: 240, MaxRequests: 4, MaxNetworkBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}

	document := Document{
		HTML: []byte("<!DOCTYPE html><html><body>server shell</body></html>"), Status: http.StatusOK,
		CacheKey: "external-page", SnapshotID: "snapshot", Cacheable: false, SkipPrerender: true,
		ConnectOrigins: []string{
			"https://api.example.com", "https://api.example.com", "https://bad.example; connect-src *",
			"https://path.example.com/v1", "https://user:password@credentials.example.com", "https://trailing.example.com:",
		},
	}
	handler := DocumentHandler("", func(context.Context, string, string) (Document, error) { return document, nil }, renderer, time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("If-None-Match", "*")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(document.HTML) {
		t.Fatalf("external page was not served from the current shell: status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Engine-Prerender") != "" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("ETag") != "" {
		t.Fatalf("external page ran worker or retained cache validators: headers=%v", response.Header())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("prerender worker was executed for external page: stat err=%v", err)
	}
	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self' https://api.example.com;") || strings.Contains(csp, "connect-src 'self' *") || strings.Contains(csp, "bad.example") || strings.Contains(csp, "path.example.com") || strings.Contains(csp, "credentials.example.com") || strings.Contains(csp, "trailing.example.com") {
		t.Fatalf("CSP contains an unsafe or missing external origin: %q", csp)
	}

	document.SkipPrerender = false
	document.Cacheable = true
	document.CacheKey = "same-origin-page"
	document.ConnectOrigins = nil
	sameOrigin := httptest.NewRecorder()
	handler.ServeHTTP(sameOrigin, httptest.NewRequest(http.MethodGet, "/", nil))
	if sameOrigin.Code != http.StatusOK || sameOrigin.Body.String() != "<!DOCTYPE html><html><body>worker output</body></html>" || sameOrigin.Header().Get("X-Engine-Prerender") != "hit" {
		t.Fatalf("same-origin document no longer prerenders: status=%d body=%q headers=%v", sameOrigin.Code, sameOrigin.Body.String(), sameOrigin.Header())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("same-origin prerender did not invoke the worker: %v", err)
	}
	if got := sameOrigin.Header().Get("Content-Security-Policy"); !strings.Contains(got, "connect-src 'self';") || strings.Contains(got, "api.example.com") {
		t.Fatalf("same-origin CSP unexpectedly adds an external origin: %q", got)
	}
}

func TestSafeConnectOriginAcceptsOnlyBareHTTPOrigins(t *testing.T) {
	for _, origin := range []string{"https://api.example.com", "http://api.example.net:8443", "https://[2001:4860:4860::8888]:8443"} {
		if !safeConnectOrigin(origin) {
			t.Errorf("safe origin %q was rejected", origin)
		}
	}
	for _, origin := range []string{
		"data:text/plain,hi", "//api.example.com", "https://user:password@api.example.com",
		"https://api.example.com/path", "https://api.example.com?x=1", "https://api.example.com#fragment",
		"https://api.example.com; default-src *", "https://api.example.com:", "https://api.example.com:bad",
	} {
		if safeConnectOrigin(origin) {
			t.Errorf("unsafe or non-origin value %q was accepted", origin)
		}
	}
}
