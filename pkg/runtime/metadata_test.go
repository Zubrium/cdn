package runtime

import (
	"io/fs"
	"strings"
	"testing"
)

func TestRenderShellUsesServerDocumentTemplateAndEscapesMetadata(t *testing.T) {
	got, err := RenderShell(HeadMetadata{
		Title:         `Vita <Sil>`,
		Description:   `Quality & care`,
		FaviconURL:    `/media/icon?id=1&v=2`,
		OGTitle:       `Share "this"`,
		OGDescription: `A <description>`,
		OGImageURL:    `/media/image`,
		CanonicalURL:  `https://site.test/path?q=a&x=b`,
		Robots:        "noindex,nofollow",
		Language:      "ru",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := string(got)
	scriptURL, _, err := publicRuntimeAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`<!doctype html>`,
		`<html lang="ru">`,
		`<title>Vita &lt;Sil&gt;</title>`,
		`<meta name="description" content="Quality &amp; care" />`,
		`<link rel="icon" href="/media/icon?id=1&amp;v=2" />`,
		`<meta property="og:title" content="Share &#34;this&#34;" />`,
		`<meta property="og:description" content="A &lt;description&gt;" />`,
		`<meta property="og:image" content="/media/image" />`,
		`<link rel="canonical" href="https://site.test/path?q=a&amp;x=b" />`,
		`<meta name="robots" content="noindex,nofollow" />`,
		`<div id="root"></div>`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("server-rendered document does not contain %q:\n%s", expected, html)
		}
	}
	if !strings.Contains(html, `<script type="module" src="`+scriptURL+`"></script>`) {
		t.Fatalf("Go document must reference the embedded runtime asset %q: %s", scriptURL, html)
	}
	if strings.Count(html, "<html") != 1 || strings.Count(html, "<head>") != 1 || strings.Count(html, "<body>") != 1 {
		t.Fatalf("expected one complete HTML document, got:\n%s", html)
	}
}

func TestRenderShellDoesNotDependOnViteIndexAndUsesBuiltRuntimeAssets(t *testing.T) {
	if _, err := fs.Stat(embedded, "dist/index.html"); err == nil {
		t.Fatal("public runtime build must not embed a Vite HTML app shell")
	}
	for _, name := range []string{"dist/favicon.svg"} {
		if _, err := fs.Stat(embedded, name); err != nil {
			t.Fatalf("expected embedded public asset %s: %v", name, err)
		}
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
	got, err := RenderShell(HeadMetadata{Title: "Catalog"})
	if err != nil {
		t.Fatal(err)
	}
	html := string(got)
	if strings.Contains(html, `rel="stylesheet"`) || !strings.Contains(html, "<style>") {
		t.Fatalf("expected small runtime CSS inline in Go-rendered document: %s", html)
	}
	if strings.Contains(html, "Zubrium") || !strings.Contains(html, `<title>Catalog</title>`) {
		t.Fatalf("public shell must not inject a product-specific brand: %s", html)
	}
}

func TestRenderShellForUnconfiguredMetadataUsesSafeFaviconAndEmptyTitle(t *testing.T) {
	html, err := RenderShell(HeadMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	value := string(html)
	if strings.Contains(value, "Website") || !strings.Contains(value, "<title></title>") {
		t.Fatalf("an unconfigured title must stay empty: %s", value)
	}
	if !strings.Contains(value, `<link rel="icon" href="/favicon.svg" />`) {
		t.Fatalf("unconfigured sites must emit the served default favicon: %s", value)
	}
}
