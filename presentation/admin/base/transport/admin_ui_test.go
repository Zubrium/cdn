package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	website "github.com/Zubrium/cdn/engines/website/presentation"
	fileapi "github.com/Zubrium/cdn/presentation/fileapi"
)

// The mux builds handlers without invoking their APIs, so embedding the
// interfaces is enough to provide non-nil dependencies for route tests.
type uiTestLoader struct{ fileapi.Loader }
type uiTestWebsite struct{ website.API }

func uiTestServer(t *testing.T, uiHandler http.Handler) *AdminServer {
	t.Helper()
	server, err := NewAdminServer(AdminConfig{
		Server:         ServerConfig{Address: "127.0.0.1:0"},
		AllowedOrigins: []string{"http://localhost:5173"},
		FileAPI: fileapi.Config{
			MaxFileBytes: 1 << 20, MaxPartBytes: 1 << 20, MaxUploadParts: 1,
			MaxJSONBytes: 1 << 20, MaxAttributeBytes: 1 << 10, MaxGroupsPerFile: 1,
			DefaultPageSize: 1, MaxPageSize: 10,
		},
		MaxJSONBytes: 1 << 20, DefaultPageSize: 1, MaxPageSize: 10,
		UIHandler: uiHandler,
	}, AdminDependencies{
		Core: &core.Runtime{}, Files: &uiTestLoader{}, Website: &uiTestWebsite{},
	})
	if err != nil {
		t.Fatalf("create Admin server: %v", err)
	}
	return server
}

func serveUIRequest(t *testing.T, server *AdminServer, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux, err := server.adminMux()
	if err != nil {
		t.Fatalf("build Admin mux: %v", err)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func TestAdminUIServesSPAWithoutShadowingAdminAPI(t *testing.T) {
	server := uiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>admin-spa</html>"))
	}))
	for _, target := range []string{"/admin", "/admin/", "/admin/content"} {
		recorder := serveUIRequest(t, server, target)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "admin-spa") {
			t.Fatalf("GET %s status = %d, body = %q; want the Admin SPA", target, recorder.Code, recorder.Body.String())
		}
	}
	api := serveUIRequest(t, server, "/api/admin/model")
	if api.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/admin/model status = %d, want the guarded Admin API response", api.Code)
	}
	if strings.Contains(api.Body.String(), "admin-spa") {
		t.Fatal("Admin API response was served by the Admin UI handler")
	}
}

func TestAdminHealthzExposesConfiguredBuildIdentity(t *testing.T) {
	server := uiTestServer(t, nil)
	server.config.BuildIdentity = "qa-run-1795303103"
	recorder := serveUIRequest(t, server, "/healthz")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("GET /healthz status = %d, want 204", recorder.Code)
	}
	if got := recorder.Header().Get("X-Zubrium-Build-Identity"); got != "qa-run-1795303103" {
		t.Fatalf("X-Zubrium-Build-Identity = %q, want the configured run identity", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	server.config.BuildIdentity = ""
	recorder = serveUIRequest(t, server, "/healthz")
	if got := recorder.Header().Get("X-Zubrium-Build-Identity"); got != "" {
		t.Fatalf("healthz exposed stale build identity %q", got)
	}
}

func TestAdminUIRouteIsNotRegisteredWithoutHandler(t *testing.T) {
	server := uiTestServer(t, nil)
	for _, target := range []string{"/admin", "/admin/", "/admin/content"} {
		recorder := serveUIRequest(t, server, target)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404 when no Admin UI handler is configured", target, recorder.Code)
		}
	}
}
