package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

func mustReadyHTML(t *testing.T, value string) ReadyHTML {
	t.Helper()
	html, err := NewReadyHTML([]byte(value))
	if err != nil {
		t.Fatalf("NewReadyHTML(%q): %v", value, err)
	}
	return html
}

func validView(t *testing.T) PageView {
	t.Helper()
	return PageView{
		Title:        "Example page",
		Description:  "A public page",
		CanonicalURL: "https://example.com/page?view=full",
		Robots:       "index, follow",
		OGMetadata: map[string]string{
			"title": "Example",
		},
		StatusCode: 200,
		Placements: []PlacementView{
			{
				ID:        "hero",
				Slot:      "content",
				HTML:      mustReadyHTML(t, `<section class="hero"><h1>Hello</h1></section>`),
				Lifecycle: LifecycleAttrs{Operation: LifecycleHydrate, FeatureKey: "hero", SignalID: "signal-1"},
			},
		},
		Bootstrap:     map[string]any{"route": "/page"},
		RuntimeAssets: nil,
		RenderedAt:    time.Date(2026, time.November, 22, 10, 30, 0, 0, time.UTC),
		Identity:      "global/snapshot-2/revision-3",
	}
}

func TestRenderPageFullDocument(t *testing.T) {
	view := validView(t)
	view.Title = `Title <b>"quoted" 'single'</b>`
	view.OGMetadata = map[string]string{
		"title":       `<script>alert("x")</script>`,
		"description": `He said "hi" and it's <b>bold</b>`,
	}
	view.Bootstrap = map[string]any{
		"message": `<script>alert("x")</script>`,
		"quote":   `"double" and 'single'`,
		"count":   2,
	}
	view.Placements = []PlacementView{
		{
			ID:        "hero",
			Slot:      "content",
			Region:    PlacementRegionMain,
			HTML:      mustReadyHTML(t, `<section id="hero-output">hero</section>`),
			Lifecycle: LifecycleAttrs{Operation: LifecycleHydrate, FeatureKey: "hero", SignalID: "signal-1"},
		},
		{
			ID:     "secondary",
			Slot:   "content",
			Region: PlacementRegionMain,
			HTML:   mustReadyHTML(t, `<section id="secondary-output">secondary</section>`),
		},
		{
			ID:     "footer",
			Slot:   "footer",
			Region: PlacementRegionOutside,
			HTML:   mustReadyHTML(t, `<aside id="footer-output">footer</aside>`),
		},
	}
	view.RuntimeAssets = []Asset{
		{Kind: AssetStyle, Path: "assets/site.abcdef0123456789.css", Integrity: "sha384-abcdefghijklmnopqrstuvwxyz0123456789"},
		{Kind: AssetScript, Path: "assets/runtime.abcdef0123456789.js", Integrity: "sha384-0123456789abcdefghijklmnopqrstuvwxyz"},
	}

	output, err := RenderPage(view)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	document := string(output)

	if !strings.HasPrefix(document, "<!doctype html>") {
		t.Fatalf("document must start with doctype, got %q", document[:32])
	}
	for _, fragment := range []string{"<html", "<head>", "</head>", "<body>", "</body>", "</html>"} {
		if !strings.Contains(document, fragment) {
			t.Errorf("missing %q", fragment)
		}
	}
	mainCount := regexp.MustCompile(`(?i)<main\b`).FindAllStringIndex(document, -1)
	if len(mainCount) != 1 {
		t.Fatalf("expected exactly one main landmark, got %d", len(mainCount))
	}
	if !strings.Contains(document, `<main id="`+MainElementID+`">`) {
		t.Errorf("expected the single main landmark to use the configured id")
	}

	hero := strings.Index(document, `data-placement-id="hero"`)
	secondary := strings.Index(document, `data-placement-id="secondary"`)
	footer := strings.Index(document, `data-placement-id="footer"`)
	if hero < 0 || secondary < 0 || footer < 0 || !(hero < secondary && secondary < footer) {
		t.Fatalf("placement order broken: hero=%d secondary=%d footer=%d", hero, secondary, footer)
	}
	mainClose := strings.Index(document, "</main>")
	if !(hero < mainClose && secondary < mainClose && footer > mainClose) {
		t.Errorf("main and outside placements are grouped incorrectly")
	}
	if !strings.Contains(document, `data-lifecycle="hydrate"`) {
		t.Errorf("lifecycle attributes are missing")
	}

	for _, escaped := range []string{"&lt;script&gt;", "&#34;", "&#39;"} {
		if !strings.Contains(document, escaped) {
			t.Errorf("expected contextual escaping to emit %q", escaped)
		}
	}
	if strings.Contains(document, `<script>alert("x")</script>`) {
		t.Errorf("unescaped script payload found in metadata")
	}

	scriptOpen := `<script type="application/json" id="` + BootstrapElementID + `">`
	start := strings.Index(document, scriptOpen)
	if start < 0 {
		t.Fatalf("inert bootstrap script is missing")
	}
	bodyStart := start + len(scriptOpen)
	end := strings.Index(document[bodyStart:], "</script>")
	if end < 0 {
		t.Fatalf("bootstrap script is not closed")
	}
	rawJSON := document[bodyStart : bodyStart+end]
	if !strings.Contains(rawJSON, `\u003cscript\u003e`) {
		t.Errorf("bootstrap JSON must keep HTML escaping, got %q", rawJSON)
	}
	var bootstrap map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &bootstrap); err != nil {
		t.Fatalf("bootstrap is not valid JSON: %v; %q", err, rawJSON)
	}
	if bootstrap["message"] != `<script>alert("x")</script>` {
		t.Errorf("unexpected bootstrap message: %#v", bootstrap["message"])
	}
	if bootstrap["count"] != float64(2) {
		t.Errorf("unexpected bootstrap count: %#v", bootstrap["count"])
	}
	if strings.Contains(document, "application/javascript") {
		t.Errorf("executable script MIME type must not be emitted")
	}

	if !strings.Contains(document, `rel="stylesheet"`) ||
		!strings.Contains(document, `href="assets/site.abcdef0123456789.css"`) {
		t.Errorf("stylesheet asset is missing")
	}
	if !strings.Contains(document, `<script type="module" src="assets/runtime.abcdef0123456789.js"`) {
		t.Errorf("module runtime asset is missing")
	}
	if !strings.Contains(document, `integrity="sha384-abcdefghijklmnopqrstuvwxyz0123456789"`) {
		t.Errorf("integrity metadata is missing")
	}
}

func TestRenderPageToMatchesRenderPage(t *testing.T) {
	view := validView(t)
	want, err := RenderPage(view)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	var buf bytes.Buffer
	if err := RenderPageTo(&buf, view); err != nil {
		t.Fatalf("RenderPageTo: %v", err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		t.Errorf("RenderPageTo output differs from RenderPage")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestRenderPageToReportsWriterFailure(t *testing.T) {
	if err := RenderPageTo(failingWriter{}, validView(t)); err == nil {
		t.Fatal("expected writer error")
	}
}

func TestNewReadyHTMLRejectsStructuralTags(t *testing.T) {
	cases := []string{
		`<main>nested</main>`,
		` <MAIn>nested</MAIn>`,
		`</html>`,
		`<script>alert(1)</script>`,
		`</script>`,
		`<style>body{}</style>`,
		`<link rel="stylesheet" href="/x.css">`,
		`<meta name="robots" content="noindex">`,
		`<base href="/">`,
		`<title>nested</title>`,
		`<!doctype html>`,
		`<template><main></main></template>`,
		`<iframe src="/"></iframe>`,
		`<div data-placement-id="nested"></div>`,
		`<div id="placement-nested"></div>`,
		"   ",
	}
	for _, value := range cases {
		if _, err := NewReadyHTML([]byte(value)); err == nil {
			t.Errorf("NewReadyHTML accepted %q", value)
		}
	}
}

func TestNewReadyHTMLCopiesInput(t *testing.T) {
	input := []byte(`<section>original</section>`)
	html, err := NewReadyHTML(input)
	if err != nil {
		t.Fatalf("NewReadyHTML: %v", err)
	}
	copy(input, `<section>mutated!</section>`)
	if !bytes.Contains(html.bytes, []byte("original")) {
		t.Errorf("ReadyHTML did not copy its input")
	}
}

func TestRenderPageRejectsUnreadyHTML(t *testing.T) {
	view := validView(t)
	view.Placements[0].HTML = ReadyHTML{}
	if _, err := RenderPage(view); !errors.Is(err, ErrInvalidView) {
		t.Fatalf("zero ReadyHTML must be rejected, got %v", err)
	}
}

func TestRenderPageRejectsDuplicatePlacementIDs(t *testing.T) {
	view := validView(t)
	view.Placements = append(view.Placements, view.Placements[0])
	if _, err := RenderPage(view); !errors.Is(err, ErrInvalidView) {
		t.Fatalf("duplicate IDs must be rejected, got %v", err)
	}
}

func TestRenderPageFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PageView)
	}{
		{"status 201", func(v *PageView) { v.StatusCode = 201 }},
		{"empty title", func(v *PageView) { v.Title = "  " }},
		{"relative canonical", func(v *PageView) { v.CanonicalURL = "/relative" }},
		{"canonical with userinfo", func(v *PageView) { v.CanonicalURL = "https://user@example.com/page" }},
		{"canonical with fragment", func(v *PageView) { v.CanonicalURL = "https://example.com/page#frag" }},
		{"inline CSS", func(v *PageView) { v.CSS = "body { color: red }" }},
		{"inline JS", func(v *PageView) { v.JS = "alert(1)" }},
		{"bootstrap with function", func(v *PageView) { v.Bootstrap = map[string]any{"fn": func() {}} }},
		{"bootstrap array", func(v *PageView) { v.Bootstrap = []string{"not", "an", "object"} }},
		{"empty placement ID", func(v *PageView) { v.Placements[0].ID = "" }},
		{"unsafe placement ID", func(v *PageView) { v.Placements[0].ID = "bad id" }},
		{"empty slot", func(v *PageView) { v.Placements[0].Slot = "" }},
		{"unknown region", func(v *PageView) { v.Placements[0].Region = "navigation" }},
		{"unknown lifecycle operation", func(v *PageView) { v.Placements[0].Lifecycle.Operation = "dispose" }},
		{"unsafe feature key", func(v *PageView) { v.Placements[0].Lifecycle.FeatureKey = `" onload="` }},
		{"error status without noindex", func(v *PageView) { v.StatusCode = 503; v.Robots = "index, follow" }},
		{"200 without identity", func(v *PageView) { v.Identity = "" }},
		{"200 without render time", func(v *PageView) { v.RenderedAt = time.Time{} }},
		{"asset traversal path", func(v *PageView) {
			v.RuntimeAssets = []Asset{{Kind: AssetScript, Path: "../runtime.js", Integrity: "sha384-abcdefghijklmnopqrstuvwxyz0123456789"}}
		}},
		{"asset with scheme", func(v *PageView) {
			v.RuntimeAssets = []Asset{{Kind: AssetStyle, Path: "https://example.com/x.css", Integrity: "sha384-abcdefghijklmnopqrstuvwxyz0123456789"}}
		}},
		{"asset without integrity", func(v *PageView) {
			v.RuntimeAssets = []Asset{{Kind: AssetScript, Path: "assets/runtime.js", Integrity: ""}}
		}},
		{"unknown asset kind", func(v *PageView) {
			v.RuntimeAssets = []Asset{{Kind: "font", Path: "assets/font.woff2", Integrity: "sha384-abcdefghijklmnopqrstuvwxyz0123456789"}}
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			view := validView(t)
			testCase.mutate(&view)
			output, err := RenderPage(view)
			if !errors.Is(err, ErrInvalidView) {
				t.Fatalf("expected ErrInvalidView, got %v", err)
			}
			if output != nil {
				t.Errorf("rejected view must not produce bytes")
			}
		})
	}
}

func TestRenderPageErrorStatusDocument(t *testing.T) {
	view := validView(t)
	view.StatusCode = 404
	view.CanonicalURL = ""
	view.Robots = "noindex, nofollow"
	view.Identity = ""
	view.RenderedAt = time.Time{}

	output, err := RenderPage(view)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !strings.Contains(string(output), `content="noindex, nofollow"`) {
		t.Errorf("error document must keep noindex robots")
	}
	if strings.Contains(string(output), `rel="canonical"`) {
		t.Errorf("error document without canonical must not emit one")
	}
}
