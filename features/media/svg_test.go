package media

import (
	"strings"
	"testing"
)

func TestSanitizeSVGKeepsLocalIllustrationAndEmbeddedRaster(t *testing.T) {
	const embeddedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	input := []byte(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10" onload="alert(1)"><defs><linearGradient id="sky"><stop offset="0" stop-color="#fff"/></linearGradient></defs><rect width="10" height="10" fill="url(#sky)"/><image xlink:href="data:image/png;base64,` + embeddedPNG + `" width="2" height="2"/></svg>`)
	clean, err := sanitizeSVG(input)
	if err != nil {
		t.Fatal(err)
	}
	text := string(clean)
	if strings.Contains(text, "onload") || strings.Contains(text, "xlink:") || !strings.Contains(text, `href="data:image/png;base64,`+embeddedPNG+`"`) {
		t.Fatalf("unexpected sanitized SVG: %s", text)
	}
	if !looksLikeSVG(clean) {
		t.Fatalf("sanitized asset is not an SVG: %s", text)
	}
}

func TestSanitizeSVGRejectsExecutableOrRemoteContent(t *testing.T) {
	cases := [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil.test/x.svg"/></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(https://evil.test/#x)"/></svg>`),
		[]byte(`<!DOCTYPE svg [<!ENTITY x "bad">]><svg xmlns="http://www.w3.org/2000/svg"/>`),
	}
	for _, input := range cases {
		if _, err := sanitizeSVG(input); err == nil {
			t.Errorf("expected unsafe SVG to be rejected: %s", input)
		}
	}
}

func TestLooksLikeSVGChecksRootElement(t *testing.T) {
	if !looksLikeSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)) {
		t.Fatal("expected valid SVG root")
	}
	if looksLikeSVG([]byte(`<html><body/></html>`)) {
		t.Fatal("expected non-SVG root to be rejected")
	}
}
