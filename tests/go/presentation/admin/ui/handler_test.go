package adminui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAdminUIServesAssetsAndFallsBackOnlyForHTMLNavigation(t *testing.T) {
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>admin</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('ok')")},
	}
	handler, err := NewHandler(assets)
	if err != nil { t.Fatal(err) }
	navigation := httptest.NewRequest(http.MethodGet, "/admin/users/123", nil)
	navigation.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, navigation)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "admin") || response.Header().Get("Cache-Control") != "no-cache" { t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String()) }
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "console.log") { t.Fatalf("status=%d body=%s", asset.Code, asset.Body.String()) }
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/admin/assets/missing.js", nil))
	if missing.Code != http.StatusNotFound { t.Fatalf("missing asset fell back: status=%d body=%s", missing.Code, missing.Body.String()) }
}

func TestAdminUIRejectsPathsOutsideMountAndRequiresAssets(t *testing.T) {
	if _, err := NewHandler(nil); err == nil { t.Fatal("nil filesystem accepted") }
	handler, err := NewHandler(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("index")}})
	if err != nil { t.Fatal(err) }
	for _, path := range []string{"/api/admin/principals", "/admin/%2e%2e/secret"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code == http.StatusOK { t.Errorf("unexpectedly served %s", path) }
	}
	var _ fs.FS = fstest.MapFS{}
}
