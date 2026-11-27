//go:build website_acceptance
// +build website_acceptance

package render_test

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/website/render"
)

func scenarioReadyHTML(t *testing.T, value string) render.ReadyHTML {
	t.Helper()
	html, err := render.NewReadyHTML([]byte(value))
	if err != nil {
		t.Fatalf("NewReadyHTML(%q): %v", value, err)
	}
	return html
}

func scenarioPublishedView(t *testing.T) render.PageView {
	t.Helper()
	return render.PageView{
		Title:        `Public <b>title</b>`,
		Description:  `A "quoted" description with 'single'`,
		CanonicalURL: "https://example.com/pages/home?view=full",
		Robots:       "index, follow",
		OGMetadata:   map[string]string{"title": `<script>alert(1)</script>`},
		StatusCode:   200,
		Placements: []render.PlacementView{
			{
				ID:        "hero",
				Slot:      "content",
				Region:    render.PlacementRegionMain,
				HTML:      scenarioReadyHTML(t, `<section id="hero-output"><h1>Preserved</h1></section>`),
				Lifecycle: render.LifecycleAttrs{Operation: render.LifecycleHydrate, FeatureKey: "hero", SignalID: "signal-1"},
			},
			{
				ID:     "footer",
				Slot:   "footer",
				Region: render.PlacementRegionOutside,
				HTML:   scenarioReadyHTML(t, `<aside id="footer-output">Outside</aside>`),
			},
		},
		Bootstrap: map[string]any{
			"route":   "/pages/home",
			"payload": `<script>alert(1)</script>`,
		},
		RuntimeAssets: []render.Asset{
			{Kind: render.AssetStyle, Path: "assets/site.abcdef0123456789.css", Integrity: "sha384-abcdefghijklmnopqrstuvwxyz0123456789"},
			{Kind: render.AssetScript, Path: "assets/runtime.abcdef0123456789.js", Integrity: "sha384-0123456789abcdefghijklmnopqrstuvwxyz"},
		},
		RenderedAt: time.Date(2026, time.November, 22, 10, 30, 0, 0, time.UTC),
		Identity:   "global/snapshot-7/revision-9",
	}
}

func TestWebsiteScenarioRenderAndSEO(t *testing.T) {
	t.Run("WEB-015", func(t *testing.T) {
		view := scenarioPublishedView(t)
		documentBytes, err := render.RenderPage(view)
		if err != nil {
			t.Fatalf("RenderPage: %v", err)
		}
		document := string(documentBytes)

		mainRoots := regexp.MustCompile(`(?i)<main\b`).FindAllStringIndex(document, -1)
		if len(mainRoots) != 1 {
			t.Fatalf("expected exactly one main landmark, got %d", len(mainRoots))
		}
		if !strings.Contains(document, `<section id="hero-output"><h1>Preserved</h1></section>`) ||
			!strings.Contains(document, `<aside id="footer-output">Outside</aside>`) {
			t.Fatal("server-rendered placement markup was not preserved")
		}
		hero := strings.Index(document, `data-placement-id="hero"`)
		footer := strings.Index(document, `data-placement-id="footer"`)
		mainClose := strings.Index(document, "</main>")
		if !(hero >= 0 && mainClose > hero && footer > mainClose) {
			t.Fatalf("main and outside placement roots are not separated: hero=%d mainClose=%d footer=%d", hero, mainClose, footer)
		}
		placementRoots := regexp.MustCompile(`data-placement-id="`).FindAllStringIndex(document, -1)
		if len(placementRoots) != 2 {
			t.Fatalf("expected exactly two placement roots, got %d", len(placementRoots))
		}
		if !strings.Contains(document, `data-lifecycle="hydrate" data-feature-key="hero" data-signal-id="signal-1"`) {
			t.Error("hydrate runtime contract attributes are missing")
		}
		if !strings.Contains(document, `rel="canonical" href="https://example.com/pages/home?view=full"`) ||
			!strings.Contains(document, `<meta name="robots" content="index, follow">`) ||
			!strings.Contains(document, `<meta property="og:title"`) {
			t.Error("public SEO metadata is missing")
		}

		for _, escaped := range []string{"&lt;script&gt;", "&#34;", "&#39;"} {
			if !strings.Contains(document, escaped) {
				t.Errorf("expected contextual escaping %q in public document", escaped)
			}
		}
		if strings.Contains(document, `<script>alert(1)</script>`) ||
			strings.Contains(document, `<style>`) ||
			!strings.Contains(document, `<script type="application/json" id="`+render.BootstrapElementID+`">`) ||
			!strings.Contains(document, `<script type="module" src="assets/runtime.abcdef0123456789.js"`) {
			t.Fatal("public document violates the inert bootstrap / module-only script contract")
		}

		bootstrapOpen := `<script type="application/json" id="` + render.BootstrapElementID + `">`
		start := strings.Index(document, bootstrapOpen)
		bodyStart := start + len(bootstrapOpen)
		end := strings.Index(document[bodyStart:], "</script>")
		if start < 0 || end < 0 {
			t.Fatal("inert bootstrap payload is missing or unterminated")
		}
		var bootstrap map[string]any
		raw := document[bodyStart : bodyStart+end]
		if err := json.Unmarshal([]byte(raw), &bootstrap); err != nil {
			t.Fatalf("bootstrap is not valid JSON: %v; %q", err, raw)
		}
		if bootstrap["route"] != "/pages/home" || bootstrap["payload"] != `<script>alert(1)</script>` {
			t.Fatalf("unexpected public bootstrap: %#v", bootstrap)
		}
		for _, private := range []string{"password", "credential", "session", "secret"} {
			if strings.Contains(strings.ToLower(raw), private) {
				t.Errorf("private context leaked into public bootstrap: %q", private)
			}
		}

		missing := view
		missing.StatusCode = 404
		missing.Robots = "noindex"
		missing.CanonicalURL = ""
		missing.OGMetadata = nil
		missing.Description = ""
		missing.Placements = nil
		missing.Identity = ""
		missing.RenderedAt = time.Time{}
		notFoundBytes, err := render.RenderPage(missing)
		if err != nil {
			t.Fatalf("RenderPage(404): %v", err)
		}
		notFound := string(notFoundBytes)
		if !strings.Contains(notFound, `<meta name="robots" content="noindex">`) ||
			strings.Contains(notFound, `rel="canonical"`) ||
			strings.Contains(notFound, `data-placement-id=`) {
			t.Fatalf("error page must be noindex and carry no placements or canonical URL: %q", notFound)
		}

		partial := view
		partial.CSS = "body { color: red }"
		partialBytes, err := render.RenderPage(partial)
		if err == nil || !errors.Is(err, render.ErrInvalidView) || partialBytes != nil {
			t.Fatalf("invalid placement or inline CSS must fail closed without a partial document: bytes=%q err=%v", partialBytes, err)
		}
		partial.Placements = append(partial.Placements, render.PlacementView{ID: "broken", Slot: "content"})
		partial.CSS = ""
		partialBytes, err = render.RenderPage(partial)
		if err == nil || !errors.Is(err, render.ErrInvalidView) || partialBytes != nil || strings.Contains(string(partialBytes), "hero-output") {
			t.Fatalf("sibling markup must not be emitted when one placement is invalid: bytes=%q err=%v", partialBytes, err)
		}
	})
}
