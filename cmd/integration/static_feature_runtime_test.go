package integration

import (
	"archive/zip"
	"bytes"

	"encoding/json"
	"fmt"
	"io"

	"net/http"
	"net/http/httptest"

	"strings"
	"testing"
	"time"
)

func staticFeatureArtifact(t *testing.T, manifest map[string]any, assets map[string][]byte) []byte {
	t.Helper()
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	assets["manifest.json"] = manifestRaw
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, data := range assets {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func measuredRuntimeGet(t *testing.T, server *httptest.Server, path string) (int, string, http.Header, time.Duration) {
	t.Helper()
	started := time.Now()
	response, err := http.Get(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data), response.Header, time.Since(started)
}

func staticRuntimeManifest(key, abi, entry string, styles ...string) map[string]any {
	manifest := testFeatureManifestFor(key, "1.0.0")
	delete(manifest, "entry")
	manifest["runtime"] = map[string]any{"abi": abi, "entry": entry, "styles": styles}
	return manifest
}

func TestStaticFeatureBuildCompositionAndHTMLFallback(t *testing.T) {
	e := newTestEnv(t)
	moduleManifest := staticRuntimeManifest("vanilla-widget", "static-module-v1", "dist/entry.mjs", "dist/widget.css")
	moduleArchive := staticFeatureArtifact(t, moduleManifest, map[string][]byte{
		"dist/entry.mjs":  []byte(`export function mount(root, context) { root.textContent = "Module " + context.featureKey; return { ready: Promise.resolve(), update(next) { root.textContent = "Module " + next.featureKey; }, unmount() { root.replaceChildren(); } }; }`),
		"dist/widget.css": []byte(`[data-engine-feature="vanilla-widget"] .widget { color: #234; }`),
	})
	status, moduleBuild, _ := e.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", e.adminToken, bytes.NewReader(moduleArchive), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("upload static module: status=%d body=%v", status, moduleBuild)
	}

	htmlManifest := staticRuntimeManifest("static-card", "static-html-v1", "dist/card.html", "dist/card.css")
	htmlArchive := staticFeatureArtifact(t, htmlManifest, map[string][]byte{
		"dist/card.html":         []byte(`<article class="tile"><img src="./images/tile.svg" alt="Tile"><h2>Static HTML card</h2><a href="/catalog">Browse</a></article>`),
		"dist/card.css":          []byte(`[data-engine-feature="static-card"] .tile { display: block; background-image: url("./textures/card.svg"); }`),
		"dist/images/tile.svg":   []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0h1v1H0z"/></svg>`),
		"dist/textures/card.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0h1v1H0z"/></svg>`),
	})
	status, htmlBuild, _ := e.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", e.adminToken, bytes.NewReader(htmlArchive), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("upload static HTML: status=%d body=%v", status, htmlBuild)
	}

	config := map[string]any{
		"formatVersion": 2,
		"pages": []any{map[string]any{
			"id": "home", "name": "Home", "kind": "page",
			"placements": []any{
				map[string]any{"id": "module", "build": map[string]any{"featureKey": "vanilla-widget", "version": "1.0.0"}, "slot": "main"},
				map[string]any{"id": "card", "build": map[string]any{"featureKey": "static-card", "version": "1.0.0"}, "slot": "main"},
			},
		}},
		"routeTree": map[string]any{"id": "root", "type": "index", "pageId": "home"},
	}
	snapshot := saveAndDeploySnapshot(t, e, config, "Static runtime fixture")

	publicStatus, compositionBody, _ := rawHTTPGet(t, e.devServer.URL+"/api/v2/runtime?path=%2F")
	if publicStatus != http.StatusOK {
		t.Fatalf("get runtime composition: status=%d body=%s", publicStatus, compositionBody)
	}
	var composition struct {
		Snapshot struct {
			ID string `json:"id"`
		} `json:"snapshot"`
		Placements []struct {
			ID         string   `json:"id"`
			RuntimeABI string   `json:"runtimeAbi"`
			EntryURL   string   `json:"entryUrl"`
			StyleURLs  []string `json:"styleUrls"`
		} `json:"placements"`
	}
	if err := json.Unmarshal(compositionBody, &composition); err != nil {
		t.Fatal(err)
	}
	if composition.Snapshot.ID != snapshot["id"] || len(composition.Placements) != 2 {
		t.Fatalf("unexpected runtime composition: %+v", composition)
	}
	if composition.Placements[0].RuntimeABI != "static-module-v1" || !strings.HasSuffix(composition.Placements[0].EntryURL, "/dist/entry.mjs") || len(composition.Placements[0].StyleURLs) != 1 {
		t.Fatalf("module ABI or immutable URLs missing: %+v", composition.Placements[0])
	}
	if composition.Placements[1].RuntimeABI != "static-html-v1" || !strings.HasSuffix(composition.Placements[1].EntryURL, "/dist/card.html") || len(composition.Placements[1].StyleURLs) != 1 {
		t.Fatalf("HTML ABI or immutable URLs missing: %+v", composition.Placements[1])
	}

	pageStatus, page, headers := seoGet(t, e.devServer, "/")
	if pageStatus != http.StatusOK || !strings.Contains(headers.Get("Content-Type"), "text/html") {
		t.Fatalf("get public page document: status=%d type=%q", pageStatus, headers.Get("Content-Type"))
	}
	for _, expected := range []string{
		"<main class=\"runtime-slot runtime-slot--main\">", "<article class=\"tile\"><img", "Static HTML card", "data-engine-feature=\"static-card\"", "data-engine-placement=\"card\"",
		fmt.Sprintf("/feature-assets/%s/dist/images/tile.svg", digestFromEntryURL(t, composition.Placements[1].EntryURL)),
		composition.Placements[1].StyleURLs[0],
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("public HTML fallback does not contain %q:\n%s", expected, page)
		}
	}
	if strings.Contains(page, "<noscript") || strings.Count(page, "<main ") != 1 {
		t.Fatalf("public document must keep one active placement tree and one main landmark:\n%s", page)
	}
	assetStatus, assetBody, assetHeaders := seoGet(t, e.devServer, composition.Placements[1].EntryURL)
	if assetStatus != http.StatusOK || !strings.Contains(assetBody, "Static HTML card") || !strings.Contains(assetHeaders.Get("Content-Type"), "text/html") {
		t.Fatalf("feature HTML asset was not served immutably: status=%d type=%q body=%q", assetStatus, assetHeaders.Get("Content-Type"), assetBody)
	}
	styleStatus, styleBody, styleHeaders := seoGet(t, e.devServer, composition.Placements[1].StyleURLs[0])
	if styleStatus != http.StatusOK || !strings.Contains(styleBody, "./textures/card.svg") || !strings.Contains(styleHeaders.Get("Content-Type"), "text/css") {
		t.Fatalf("feature stylesheet or local resource was not served immutably: status=%d type=%q body=%q", styleStatus, styleHeaders.Get("Content-Type"), styleBody)
	}
	textureURL := "/feature-assets/" + digestFromEntryURL(t, composition.Placements[1].EntryURL) + "/dist/textures/card.svg"
	textureStatus, textureBody, textureHeaders := seoGet(t, e.devServer, textureURL)
	if textureStatus != http.StatusOK || !strings.Contains(textureBody, "<svg") || !strings.Contains(textureHeaders.Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("local static resource was not served immutably: status=%d type=%q", textureStatus, textureHeaders.Get("Content-Type"))
	}
}

func rawHTTPGet(t *testing.T, target string) (int, []byte, http.Header) {
	t.Helper()
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body, response.Header
}

func digestFromEntryURL(t *testing.T, entryURL string) string {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(entryURL, "/feature-assets/"), "/")
	if len(parts) < 2 || len(parts[0]) != 64 {
		t.Fatalf("invalid feature entry URL %q", entryURL)
	}
	return parts[0]
}
