package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestAdminStaticCacheHeaders(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":                    {Data: []byte("<!doctype html><html></html>")},
		"assets/app-a1b2c3d4e5f6.js":    {Data: []byte("console.log(1)")},
		"assets/style-0123456789ab.css": {Data: []byte("body{}")},
		"assets/config.json":            {Data: []byte("{}")},
	}
	handler, err := NewHandler(assets)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path       string
		wantCache  string
		wantNoVary bool
	}{
		{path: "/admin/", wantCache: "no-store"},
		{path: "/admin/index.html", wantCache: "no-store"},
		{path: "/admin/assets/app-a1b2c3d4e5f6.js", wantCache: "public, max-age=31536000, immutable"},
		{path: "/admin/assets/style-0123456789ab.css", wantCache: "public, max-age=31536000, immutable"},
		{path: "/admin/assets/config.json", wantCache: "no-store"},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://admin.invalid"+testCase.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if got := response.Header().Get("Cache-Control"); got != testCase.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", got, testCase.wantCache)
			}
		})
	}
}
