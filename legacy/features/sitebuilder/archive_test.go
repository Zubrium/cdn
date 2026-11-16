package sitebuilder

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
)

func makeFeatureArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, value := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestVerifyFeatureArchive(t *testing.T) {
	manifest := strings.Replace(string(validManifest()), `"assets/entry.js"`, `"entry.js"`, 1)
	manifest = strings.Replace(manifest, `"assets/entry.css"`, `"entry.css"`, 1)
	archive, err := VerifyArchive(makeFeatureArchive(t, map[string]string{"manifest.json": manifest, "entry.js": "export default function Feature() {}", "entry.css": ".feature { color: red }"}), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Manifest.Key != "product-detail" || len(archive.Assets) != 2 || len(archive.Digest) != 64 {
		t.Fatalf("unexpected verified archive: %#v", archive)
	}
}

func TestVerifyArchiveRejectsTraversalMissingAssetsAndOversize(t *testing.T) {
	manifest := strings.Replace(string(validManifest()), `"assets/entry.js"`, `"../entry.js"`, 1)
	archive := makeFeatureArchive(t, map[string]string{"manifest.json": manifest, "../entry.js": "bad"})
	if _, err := VerifyArchive(archive, 8<<20); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}

	manifest = strings.Replace(string(validManifest()), `"assets/entry.js"`, `"entry.js"`, 1)
	manifest = strings.Replace(manifest, `"assets/entry.css"`, `"missing.css"`, 1)
	archive = makeFeatureArchive(t, map[string]string{"manifest.json": manifest, "entry.js": "export default 1"})
	if _, err := VerifyArchive(archive, 8<<20); err == nil {
		t.Fatal("expected missing stylesheet to be rejected")
	}
	if _, err := VerifyArchive(archive, 1); err == nil {
		t.Fatal("expected compressed archive size limit to be enforced")
	}
}

func runtimeManifest(t *testing.T, abi, entry string, styles ...string) string {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(validManifest(), &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest, "entry")
	delete(manifest, "styles")
	manifest["runtime"] = map[string]any{"abi": abi, "entry": entry, "styles": styles}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestVerifyStaticModuleArchiveAndLocalDependencies(t *testing.T) {
	manifest := runtimeManifest(t, RuntimeABIStaticModule, "dist/entry.js", "dist/feature.css")
	archive := makeFeatureArchive(t, map[string]string{
		"manifest.json":    manifest,
		"dist/entry.js":    `import { render } from "./view.js"; export function mount(root, context) { root.textContent = render(context); return { ready: Promise.resolve(), update(next) { root.textContent = render(next); }, unmount() { root.replaceChildren(); } }; }`,
		"dist/view.js":     `export function render(context) { return context.featureKey; }`,
		"dist/feature.css": `[data-engine-feature="product-detail"] .title { color: red; }`,
	})
	verified, err := VerifyArchive(archive, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Manifest.RuntimeABI() != RuntimeABIStaticModule || verified.Manifest.RuntimeEntry() != "dist/entry.js" || len(verified.Manifest.RuntimeStyles()) != 1 {
		t.Fatalf("unexpected module contract: %#v", verified.Manifest)
	}
}

func TestVerifyStaticModuleParsesRegexLiteralsAndDivision(t *testing.T) {
	manifest := runtimeManifest(t, RuntimeABIStaticModule, "dist/entry.js")
	archive := makeFeatureArchive(t, map[string]string{
		"manifest.json": manifest,
		"dist/entry.js": `const slashRuns = /\/+/g;
const dimensions = /\d+(%|px|rem)/g;
const importLikeText = /import\("https:\/\/example\.test"\)/;
const quotient = 12 / 3;
import { value } from "./view.js";
export { value as renamed } from "./view.js";
export const result = importLikeText.test("import()") ? slashRuns.test("//") : dimensions.test(String(quotient));
`,
		"dist/view.js": `export const value = "ok";`,
	})
	if _, err := VerifyArchive(archive, 8<<20); err != nil {
		t.Fatalf("valid regex literals, division, and local ESM imports should be accepted: %v", err)
	}
}

func TestVerifyStaticArchiveIncludesExplicitSharedStyles(t *testing.T) {
	manifest := runtimeManifest(t, RuntimeABIStaticModule, "dist/entry.js", "dist/feature.css")
	manifest = strings.Replace(manifest, `"styles":["dist/feature.css"]`, `"styles":["dist/feature.css"],"sharedStyles":["dist/core.css"]`, 1)
	archive := makeFeatureArchive(t, map[string]string{
		"manifest.json":    manifest,
		"dist/entry.js":    `export function mount(root) { root.textContent = "Standalone"; return { ready: Promise.resolve(), update() {}, unmount() {} }; }`,
		"dist/feature.css": `[data-engine-feature="product-detail"] .title { color: red; }`,
		"dist/core.css":    `:root { --color-primary: blue; }`,
	})
	verified, err := VerifyArchive(archive, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := verified.Manifest.RuntimeSharedStyles(); len(got) != 1 || got[0] != "dist/core.css" {
		t.Fatalf("shared stylesheet was not retained: %v", got)
	}
}

func TestVerifyStaticModuleAcceptsCSSMinifierUnquotedFeatureScope(t *testing.T) {
	manifest := runtimeManifest(t, RuntimeABIStaticModule, "dist/entry.js", "dist/feature.css")
	for name, selector := range map[string]string{
		"unquoted":          `[data-engine-feature=product-detail] .title { color: red; }`,
		"unquoted in where": `:where([data-engine-feature=product-detail]) .title { color: red; }`,
		"quoted":            `[data-engine-feature="product-detail"] .title { color: red; }`,
	} {
		t.Run(name, func(t *testing.T) {
			archive := makeFeatureArchive(t, map[string]string{
				"manifest.json":    manifest,
				"dist/entry.js":    `export function mount() { return { ready: Promise.resolve(), update() {}, unmount() {} }; }`,
				"dist/feature.css": selector,
			})
			if _, err := VerifyArchive(archive, 8<<20); err != nil {
				t.Fatalf("valid build output scope should be accepted: %v", err)
			}
		})
	}
}

func TestVerifyStaticModuleRejectsExternalBareDynamicAndMissingImports(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		files map[string]string
	}{
		{name: "bare import", entry: `import React from "react"; export function mount() {}`, files: map[string]string{}},
		{name: "external import", entry: `import "https://cdn.example.test/library.js"; export function mount() {}`, files: map[string]string{}},
		{name: "dynamic import", entry: `export function mount() { return import(window.featurePath); }`, files: map[string]string{}},
		{name: "missing local import", entry: `import "./missing.js"; export function mount() {}`, files: map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := runtimeManifest(t, RuntimeABIStaticModule, "dist/entry.js")
			assets := map[string]string{"manifest.json": manifest, "dist/entry.js": tc.entry}
			for name, value := range tc.files {
				assets[name] = value
			}
			if _, err := VerifyArchive(makeFeatureArchive(t, assets), 8<<20); err == nil {
				t.Fatal("expected invalid module imports to be rejected")
			}
		})
	}
}

func TestVerifyStaticHTMLFragmentAndLocalAssets(t *testing.T) {
	manifest := runtimeManifest(t, RuntimeABIStaticHTML, "dist/card.html", "dist/card.css")
	archive := makeFeatureArchive(t, map[string]string{
		"manifest.json":  manifest,
		"dist/card.html": `<section class="tile"><img src="./tile.png" alt="Tile" loading="lazy"><h2>Static content</h2><a href="/catalog">Open</a></section>`,
		"dist/card.css":  `[data-engine-feature="product-detail"] .tile { display: block; }`,
		"dist/tile.png":  "png fixture",
	})
	if _, err := VerifyArchive(archive, 8<<20); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyStaticHTMLRejectsTemplatesActiveContentUnsafeURLsAndGlobalCSS(t *testing.T) {
	cases := []struct {
		name  string
		html  string
		style string
	}{
		{name: "template expression", html: `<p>{{ title }}</p>`, style: `[data-engine-feature="product-detail"] p{color:red}`},
		{name: "script", html: `<script>window.x=1</script>`, style: `[data-engine-feature="product-detail"] p{color:red}`},
		{name: "event handler", html: `<button onclick="run()">Run</button>`, style: `[data-engine-feature="product-detail"] button{color:red}`},
		{name: "form", html: `<form><button>Send</button></form>`, style: `[data-engine-feature="product-detail"] button{color:red}`},
		{name: "javascript URL", html: `<a href="javascript:alert(1)">bad</a>`, style: `[data-engine-feature="product-detail"] a{color:red}`},
		{name: "remote resource", html: `<img src="https://cdn.example.test/photo.jpg">`, style: `[data-engine-feature="product-detail"] img{width:100%}`},
		{name: "nested page landmark", html: `<main><section>Content</section></main>`, style: `[data-engine-feature="product-detail"] section{color:red}`},
		{name: "global selector", html: `<p>Text</p>`, style: `body{color:red}`},
		{name: "remote CSS", html: `<p>Text</p>`, style: `[data-engine-feature="product-detail"] p{background:url(https://cdn.example.test/a.png)}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := runtimeManifest(t, RuntimeABIStaticHTML, "dist/card.html", "dist/card.css")
			archive := makeFeatureArchive(t, map[string]string{"manifest.json": manifest, "dist/card.html": tc.html, "dist/card.css": tc.style})
			if _, err := VerifyArchive(archive, 8<<20); err == nil {
				t.Fatal("expected unsafe static HTML archive to be rejected")
			}
		})
	}
}

func TestVerifyStaticHTMLEntryHasRuntimeSizeLimit(t *testing.T) {
	manifest := runtimeManifest(t, RuntimeABIStaticHTML, "entry.html")
	content := make([]byte, maxStaticHTMLBytes+1)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	archive := makeFeatureArchive(t, map[string]string{
		"manifest.json": manifest,
		"entry.html":    string(content),
	})
	if _, err := VerifyArchive(archive, 8<<20); err == nil || !strings.Contains(err.Error(), "static HTML entry exceeds") {
		t.Fatalf("expected static HTML size limit error, got %v", err)
	}
}

func TestVerifyStaticModuleUsesBrowserMIMETypes(t *testing.T) {
	manifest := runtimeManifest(t, RuntimeABIStaticModule, "dist/entry.mjs", "dist/style.css")
	archive := makeFeatureArchive(t, map[string]string{
		"manifest.json":  manifest,
		"dist/entry.mjs": `export function mount() { return { ready: Promise.resolve(), update() {}, unmount() {} }; }`,
		"dist/style.css": `[data-engine-feature="product-detail"] { color: red; }`,
	})
	verified, err := VerifyArchive(archive, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	contentTypes := map[string]string{}
	for _, asset := range verified.Assets {
		contentTypes[asset.Path] = asset.ContentType
	}
	if contentTypes["dist/entry.mjs"] != "text/javascript; charset=utf-8" || contentTypes["dist/style.css"] != "text/css; charset=utf-8" {
		t.Fatalf("unexpected browser asset MIME types: %#v", contentTypes)
	}
}
