package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRuntimeAPIContract(t *testing.T) {
	env := newTestEnv(t)
	status, settings, _ := env.request(http.MethodGet, "/api/v1/admin/settings", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("runtime settings endpoint: %d %v", status, settings)
	}
	publicAPI := settings["public_api"].(map[string]any)
	want := map[string]string{
		"runtime_path": "/api/v2/runtime?path={path}",
		"forms_path":   "/api/v2/forms/{key}/submissions",
		"admin_path":   "/api/v1/admin",
		"media_path":   "/media/{id}",
	}
	for key, expected := range want {
		if actual, ok := publicAPI[key].(string); !ok || actual != expected {
			t.Fatalf("public API %s = %v, want %q", key, publicAPI[key], expected)
		}
	}
	if _, exists := publicAPI["collection_path"]; exists {
		t.Fatal("settings advertise a removed standalone collection endpoint")
	}

	status, body, _ := env.public(env.devBranch, http.MethodGet, "/api/v2/runtime?path=%2F", nil)
	if status != http.StatusServiceUnavailable || body["error"] == nil {
		t.Fatalf("runtime without an active snapshot should return a structured 503: %d %v", status, body)
	}
}

func fetchSite(t *testing.T, baseURL, method, route string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, baseURL+route, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header
}

func TestPublicRouterServesEmbeddedRuntime(t *testing.T) {
	env := newTestEnv(t)
	for _, target := range []struct {
		name string
		url  string
	}{
		{name: "dev", url: env.devServer.URL},
		{name: "prod", url: env.prodServer.URL},
	} {
		status, html, headers := fetchSite(t, target.url, http.MethodGet, "/")
		if status != http.StatusServiceUnavailable || !strings.Contains(html, `Сайт ещё не настроен`) {
			t.Fatalf("%s should serve the embedded runtime shell: %d %q", target.name, status, html)
		}
		if headers.Get("Cache-Control") != "no-store" || !strings.Contains(headers.Get("Content-Security-Policy"), "script-src") {
			t.Fatalf("%s runtime headers are incomplete: %v", target.name, headers)
		}
		status, deepLink, _ := fetchSite(t, target.url, http.MethodGet, "/catalog/example")
		if status != http.StatusServiceUnavailable || !strings.Contains(deepLink, `Сайт ещё не настроен`) {
			t.Fatalf("%s deep links should return the runtime shell: %d %q", target.name, status, deepLink)
		}
		if status, _, _ := fetchSite(t, target.url, http.MethodGet, "/missing.js"); status != http.StatusNotFound {
			t.Fatalf("missing static assets should be 404, got %d", status)
		}
		if status, body, _ := fetchSite(t, target.url, http.MethodHead, "/"); status != http.StatusServiceUnavailable || body != "" {
			t.Fatalf("HEAD runtime shell should return headers without a body: %d %q", status, body)
		}
	}
}
