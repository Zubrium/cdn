package runtime

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	htmlparser "golang.org/x/net/html"
)

func parseTestDocument(t *testing.T, source []byte) *htmlparser.Node {
	t.Helper()
	document, err := htmlparser.Parse(strings.NewReader(string(source)))
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestRenderPageDocumentEscapesMetadataPlacementAndBootstrap(t *testing.T) {
	got, err := RenderPageDocument(HeadMetadata{
		Title:         `Vita <Sil>`,
		Description:   `Quality & care`,
		FaviconURL:    `/media/icon?id=1&v=2`,
		OGTitle:       `Share "this"`,
		OGDescription: `A <description>`,
		OGImageURL:    `/media/image`,
		CanonicalURL:  `https://site.test/path?q=a&x=b`,
		Robots:        "noindex,nofollow",
		Language:      "ru",
	}, "page", `snap&1`, 7, []PlacementRoot{{ID: `x" onmouseover="bad`, Slot: "main", FeatureKey: `f<&`}}, []byte(`{"title":"</script><script>attack()</script>","value":"<&>"}`))
	if err != nil {
		t.Fatal(err)
	}
	html := string(got)
	scriptURL, _, err := publicRuntimeAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`<!doctype html>`, `<html lang="ru">`, `<title>Vita &lt;Sil&gt;</title>`,
		`<meta name="description" content="Quality &amp; care" />`,
		`<link rel="icon" href="/media/icon?id=1&amp;v=2" />`,
		`<meta property="og:title" content="Share &#34;this&#34;" />`,
		`<meta property="og:description" content="A &lt;description&gt;" />`,
		`<meta property="og:image" content="/media/image" />`,
		`<link rel="canonical" href="https://site.test/path?q=a&amp;x=b" />`,
		`<meta name="robots" content="noindex,nofollow" />`,
		`data-snapshot="snap&amp;1"`, `data-engine-feature="f&lt;&amp;"`,
		`type="application/json" id="engine-runtime-bootstrap"`,
		`<script type="module" src="` + scriptURL + `"></script>`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("server-rendered document does not contain %q:\n%s", expected, html)
		}
	}
	if strings.Contains(html, `onmouseover="bad`) {
		t.Fatalf("placement values must be contextually escaped: %s", html)
	}
	document := parseTestDocument(t, got)
	if countTestElements(document, "script") != 2 {
		t.Fatalf("bootstrap JSON must not create an extra script element: %s", html)
	}
	bootstrap := findTestElementByID(document, "engine-runtime-bootstrap")
	var decoded map[string]string
	if bootstrap == nil || json.Unmarshal([]byte(childText(bootstrap)), &decoded) != nil {
		t.Fatalf("bootstrap must remain valid inert JSON: %s", html)
	}
	if decoded["title"] != `</script><script>attack()</script>` || decoded["value"] != "<&>" {
		t.Fatalf("bootstrap data changed during escaping: %#v", decoded)
	}
	if strings.Count(html, "</script>") != 2 {
		t.Fatalf("only the two trusted script closing tags may remain: %s", html)
	}
}

func TestRenderPageDocumentUsesEmbeddedRuntimeAssetsAndNoViteHTML(t *testing.T) {
	if _, err := fs.Stat(embedded, "dist/index.html"); err == nil {
		t.Fatal("public runtime build must not embed a Vite HTML app shell")
	}
	if _, err := fs.Stat(embedded, "dist/favicon.svg"); err != nil {
		t.Fatalf("expected embedded public favicon: %v", err)
	}
	scriptURL, stylesheetURL, err := publicRuntimeAssets()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(scriptURL, "/assets/public-runtime-") || !strings.HasSuffix(scriptURL, ".js") {
		t.Fatalf("runtime entry must be a content-addressed JS asset, got %q", scriptURL)
	}
	if !strings.HasPrefix(stylesheetURL, "/public-runtime.css") && !strings.HasPrefix(stylesheetURL, "/assets/public-runtime") {
		t.Fatalf("unexpected public runtime stylesheet URL %q", stylesheetURL)
	}
	got, err := RenderPageDocument(HeadMetadata{Title: "Catalog"}, "page", "snapshot", 1, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	html := string(got)
	if strings.Contains(html, `rel="stylesheet"`) || !strings.Contains(html, "<style>") {
		t.Fatalf("expected small runtime CSS inline in Go-rendered document: %s", html)
	}
	if strings.Contains(html, "Zubrium") || !strings.Contains(html, `<title>Catalog</title>`) {
		t.Fatalf("public document must not inject a product-specific brand: %s", html)
	}
	if strings.Count(html, "<main ") != 1 || strings.Count(html, `id="root"`) != 1 {
		t.Fatalf("expected one root and one main landmark: %s", html)
	}
}

func TestRenderPageDocumentUsesSafeUnconfiguredMetadataAndRejectsInvalidInputs(t *testing.T) {
	got, err := RenderPageDocument(HeadMetadata{}, "page", "snapshot", 0, nil, []byte(`null`))
	if err != nil {
		t.Fatal(err)
	}
	value := string(got)
	if strings.Contains(value, "Website") || !strings.Contains(value, "<title></title>") || !strings.Contains(value, `<link rel="icon" href="/favicon.svg" />`) {
		t.Fatalf("unconfigured page metadata did not use safe empty values: %s", value)
	}
	if _, err := RenderPageDocument(HeadMetadata{}, "page", "snapshot", 1, nil, []byte(`{"ok":true} trailing`)); err == nil {
		t.Fatal("expected trailing bootstrap data to be rejected")
	}
	if _, err := RenderPageDocument(HeadMetadata{}, "page", "snapshot", 1, []PlacementRoot{{Slot: "aside"}}, []byte(`{}`)); err == nil {
		t.Fatal("expected unsupported placement slot to be rejected")
	}
}
