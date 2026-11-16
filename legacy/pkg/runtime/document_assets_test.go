package runtime

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"
)

func TestDocumentHandlerServesEmbeddedRuntimeAssetsAndNeverFallsBackToViteHTML(t *testing.T) {
	scriptURL, stylesheetURL, err := publicRuntimeAssets()
	if err != nil {
		t.Fatal(err)
	}
	stylesheetCache := "public, max-age=3600"
	if strings.HasPrefix(stylesheetURL, "/assets/") {
		stylesheetCache = "public, max-age=31536000, immutable"
	}
	resolverCalls := 0
	handler := DocumentHandler("", func(context.Context, string, string) (Document, error) {
		resolverCalls++
		return SystemDocument(http.StatusServiceUnavailable), nil
	}, nil, time.Hour)

	for _, test := range []struct {
		name        string
		url         string
		contentType string
		cache       string
		resolverHit bool
	}{
		{name: "hashed runtime entry", url: scriptURL, contentType: "javascript", cache: "public, max-age=31536000, immutable"},
		{name: "runtime stylesheet", url: stylesheetURL, contentType: "text/css", cache: stylesheetCache},
		{name: "public route is a Go system document", url: "/", contentType: "text/html", cache: "no-store", resolverHit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.url, nil))
			if response.Code != http.StatusOK && test.resolverHit {
				if response.Code != http.StatusServiceUnavailable {
					t.Fatalf("system route status = %d, want 503", response.Code)
				}
			} else if response.Code != http.StatusOK {
				t.Fatalf("asset status = %d, body=%s", response.Code, response.Body.String())
			}
			if got := strings.ToLower(response.Header().Get("Content-Type")); !strings.Contains(got, test.contentType) {
				t.Fatalf("content type = %q, want %q", got, test.contentType)
			}
			if got := response.Header().Get("Cache-Control"); got != test.cache {
				t.Fatalf("cache policy = %q, want %q", got, test.cache)
			}
			if test.resolverHit && response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatalf("public route should be Go HTML, got %q", response.Header().Get("Content-Type"))
			}
		})
	}
	if resolverCalls != 1 {
		t.Fatalf("resolver calls = %d, public assets must not go through route fallback", resolverCalls)
	}
	if _, err := fs.Stat(embedded, "dist/index.html"); err == nil {
		t.Fatal("public handler must not have a Vite index.html fallback")
	}
	if strings.HasPrefix(path.Base(scriptURL), "index-") {
		t.Fatalf("unexpected legacy app entry filename %q", scriptURL)
	}
}
