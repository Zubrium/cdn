package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	contentengine "github.com/Zubrium/cdn/engines/content/presentation"
	formsengine "github.com/Zubrium/cdn/engines/forms/presentation"
	adminapi "github.com/Zubrium/cdn/presentation/admin/base/admin/main"
	contentadmin "github.com/Zubrium/cdn/presentation/admin/content"
	fileapi "github.com/Zubrium/cdn/presentation/fileapi"
)

// The mux builds handlers without invoking their APIs, so embedded interfaces
// are enough to exercise route ownership without a live Core or database.
type isolationLoader struct{ fileapi.Loader }
type isolationIdentity struct{ adminapi.IdentityAdminAPI }
type isolationSessions struct{ adminapi.SessionAdminAPI }
type isolationGroupAccess struct {
	adminapi.SessionGroupAccessResolver
}
type isolationContent struct{ contentengine.API }
type isolationContentSchemas struct{ contentadmin.SchemaSnapshots }
type isolationForms struct{ formsengine.API }

func apiIsolationMux(t *testing.T, notifications bool) http.Handler {
	t.Helper()
	config := AdminConfig{
		Server: ServerConfig{
			Address: "127.0.0.1:0", MaxInFlight: 1,
			RequestTimeout: time.Second, ShutdownTimeout: 2 * time.Second,
		},
		AllowedOrigins: []string{"http://localhost:5173"},
		FileAPI: fileapi.Config{
			MaxFileBytes: 1 << 20, MaxPartBytes: 1 << 20, MaxUploadParts: 1,
			MaxJSONBytes: 1 << 20, MaxAttributeBytes: 1 << 10, MaxGroupsPerFile: 1,
			DefaultPageSize: 1, MaxPageSize: 10,
		},
		MaxJSONBytes: 1 << 20, DefaultPageSize: 1, MaxPageSize: 10,
		IdentityEnabled:          true,
		FormsExportMaxConcurrent: 1,
		FormsExportMaxRows:       100,
		FormsExportMaxBytes:      1 << 20,
	}
	deps := AdminDependencies{
		CoreReady:      func() bool { return false },
		Files:          &isolationLoader{},
		Identity:       &isolationIdentity{},
		Sessions:       &isolationSessions{},
		GroupAccess:    &isolationGroupAccess{},
		Content:        &isolationContent{},
		ContentSchemas: &isolationContentSchemas{},
		Forms:          &isolationForms{},
	}
	if notifications {
		deps.Notifications = NewNotificationHub()
	}
	server, err := NewAdminServer(config, deps)
	if err != nil {
		t.Fatalf("NewAdminServer: %v", err)
	}
	mux, err := server.adminMux()
	if err != nil {
		t.Fatalf("adminMux: %v", err)
	}
	return mux
}

func apiIsolationResponse(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestAdminMuxKeepsModuleNamespacesIsolated(t *testing.T) {
	// Core is intentionally not ready: every path owned by an enabled adapter
	// reaches its guard (503), while unowned paths fall through to the terminal
	// 404. This distinguishes mounted ownership from an implicit catch-all.
	withNotifications := apiIsolationMux(t, true)
	withoutNotifications := apiIsolationMux(t, false)

	t.Run("identity_owns_only_declared_paths", func(t *testing.T) {
		for _, path := range []string{
			"/api/admin/session",
			"/api/admin/principals",
			"/api/admin/principals/00000000-0000-0000-0000-000000000001",
			"/api/admin/credentials/00000000-0000-0000-0000-000000000001",
			"/api/admin/access-groups",
		} {
			if got := apiIsolationResponse(t, withNotifications, path).Code; got != http.StatusServiceUnavailable {
				t.Errorf("GET %s = %d, want 503 from the identity adapter", path, got)
			}
		}
		for _, path := range []string{
			"/api/admin/unknown-module-route",
			"/api/admin/router/operations",
			"/api/admin/model",
		} {
			if got := apiIsolationResponse(t, withNotifications, path).Code; got != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404 outside every enabled adapter", path, got)
			}
		}
	})

	t.Run("other_modules_keep_their_own_subtrees", func(t *testing.T) {
		for _, path := range []string{
			"/api/admin/content/locales",
			"/api/admin/forms/schemas",
			"/api/admin/notifications",
		} {
			if got := apiIsolationResponse(t, withNotifications, path).Code; got != http.StatusServiceUnavailable {
				t.Errorf("GET %s = %d, want 503 from its own adapter", path, got)
			}
		}
		if got := apiIsolationResponse(t, withoutNotifications, "/api/admin/notifications").Code; got != http.StatusNotFound {
			t.Errorf("disabled notifications = %d, want 404", got)
		}
	})

	t.Run("subtree_roots_do_not_redirect", func(t *testing.T) {
		for _, path := range []string{"/api/admin/content", "/api/admin/forms"} {
			response := apiIsolationResponse(t, withNotifications, path)
			if response.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", path, response.Code)
			}
			if location := response.Header().Get("Location"); location != "" {
				t.Errorf("GET %s redirected to %q; subtree roots are not routes", path, location)
			}
		}
	})
}
