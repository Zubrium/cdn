package runtime

import (
	"strings"
	"testing"
)

func TestRenderStaticHTMLFallback(t *testing.T) {
	shell := []byte(`<!doctype html><html><head><meta charset="utf-8"></head><body><div id="root"></div><script type="module" src="/assets/runtime.js"></script></body></html>`)
	rendered, err := RenderStaticHTMLFallback(shell, "snapshot-1", 7, "page", []string{"/theme.css"}, []StaticHTMLFeature{{
		FeatureKey: "catalog-card", PlacementID: "card-1", Slot: "main", Digest: strings.Repeat("a", 64), Entry: "dist/card.html",
		HTML:      `<article><img src="./images/card.png" srcset="./images/card.png 1x, ./images/card@2x.png 2x" alt="Card"><h2>Static card</h2></article>`,
		StyleURLs: []string{"/feature.css", "/theme.css"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := string(rendered)
	for _, expected := range []string{
		`id="root" class="runtime-page runtime-page--page" data-snapshot="snapshot-1" data-content-revision="7"`,
		`<main class="runtime-slot runtime-slot--main">`,
		`class="runtime-placement runtime-placement--main" data-engine-feature="catalog-card" data-engine-placement="card-1" data-engine-slot="main"`,
		`Static card`, `/feature-assets/` + strings.Repeat("a", 64) + `/dist/images/card.png`,
		`/feature-assets/` + strings.Repeat("a", 64) + `/dist/images/card@2x.png 2x`,
		`href="/theme.css"`, `href="/feature.css"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("rendered static fallback does not contain %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "<noscript") {
		t.Fatalf("static fallback must reuse the server-rendered placement roots, not add a parallel subtree:\n%s", body)
	}
	if strings.Count(body, `href="/theme.css"`) != 1 {
		t.Fatal("duplicate stylesheet link was emitted")
	}
	if strings.Count(body, "<main ") != 1 {
		t.Fatalf("static fallback must contain exactly one main landmark:\n%s", body)
	}
}

func TestRenderStaticHTMLFallbackGroupsSiblingPlacementsBySlot(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rendered, err := RenderStaticHTMLFallback(
		[]byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`),
		"snapshot", 1, "page", nil,
		[]StaticHTMLFeature{
			{FeatureKey: "first", PlacementID: "first", Slot: "main", Digest: digest, Entry: "first.html", HTML: `<article>First</article>`},
			{FeatureKey: "second", PlacementID: "second", Slot: "main", Digest: digest, Entry: "second.html", HTML: `<section>Second</section>`},
			{FeatureKey: "site-header", PlacementID: "header", Slot: "header", Digest: digest, Entry: "header.html", HTML: `<header>Header</header>`},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := string(rendered)
	mainSiblings := `<main class="runtime-slot runtime-slot--main"><div class="runtime-placement runtime-placement--main" data-engine-feature="first" data-engine-placement="first" data-engine-slot="main"><article>First</article></div><div class="runtime-placement runtime-placement--main" data-engine-feature="second" data-engine-placement="second" data-engine-slot="main"><section>Second</section></div></main>`
	headerSiblings := `<div class="runtime-placement runtime-placement--header" data-engine-feature="site-header" data-engine-placement="header" data-engine-slot="header"><header>Header</header></div>`
	if !strings.Contains(body, mainSiblings) {
		t.Fatalf("main placements must be direct siblings in one main host:\n%s", body)
	}
	if !strings.Contains(body, headerSiblings) {
		t.Fatalf("header placement must use one shared slot host:\n%s", body)
	}
	if strings.Index(body, headerSiblings) > strings.Index(body, mainSiblings) {
		t.Fatalf("non-main placements must be direct #root children before the main host:\n%s", body)
	}
	if strings.Count(body, `id="root"`) != 1 || strings.Contains(body, `<noscript`) {
		t.Fatalf("static fragment must use the single active DOM tree:\n%s", body)
	}
	if strings.Count(body, "<main ") != 1 {
		t.Fatalf("fallback must contain exactly one main landmark:\n%s", body)
	}
}

func TestRenderStaticHTMLFallbackKeepsMainLandmarkOnChromeOnlyPage(t *testing.T) {
	rendered, err := RenderStaticHTMLFallback(
		[]byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`),
		"snapshot", 1, "page", nil,
		[]StaticHTMLFeature{{FeatureKey: "legal-documents", PlacementID: "legal-footer", Slot: "footer", Digest: strings.Repeat("a", 64), Entry: "footer.html", HTML: `<footer>Legal links</footer>`}},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := string(rendered)
	if !strings.Contains(body, `<main class="runtime-slot runtime-slot--main"></main>`) {
		t.Fatalf("chrome-only fallback must keep an empty page-level main landmark:\n%s", body)
	}
	if strings.Contains(body, `runtime-slot--footer`) {
		t.Fatalf("chrome-only fallback must not add an empty footer host:\n%s", body)
	}
	if strings.Count(body, "<main ") != 1 {
		t.Fatalf("chrome-only fallback must contain exactly one main landmark:\n%s", body)
	}
}

func TestRenderStaticHTMLFallbackRejectsResourceEscapes(t *testing.T) {
	_, err := RenderStaticHTMLFallback([]byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`), "snapshot", 1, "page", nil, []StaticHTMLFeature{{
		FeatureKey: "catalog-card", PlacementID: "card-1", Slot: "main", Digest: strings.Repeat("a", 64), Entry: "dist/card.html", HTML: `<img src="../../outside.png">`,
	}})
	if err == nil {
		t.Fatal("expected a path escaping the feature entry directory to be rejected")
	}
}
