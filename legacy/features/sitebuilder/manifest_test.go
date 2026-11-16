package sitebuilder

import (
	"encoding/json"
	"strings"
	"testing"
)

func validManifest() []byte {
	return []byte(`{
		"formatVersion":2,
		"key":"product-detail",
		"name":"Product detail",
		"version":"2.0.0",
		"engineApi":"1",
		"entry":"assets/entry.js",
		"styles":["assets/entry.css"],
		"slots":["main"],
		"arguments":{"type":"object","properties":{"slug":{"type":"string","title":"Slug"}},"required":["slug"],"additionalProperties":false},
		"dataInputs":[{"key":"product","kind":"record","fields":[{"path":"/title","type":"text"},{"path":"/artwork/main","type":"media","profile":{"accept":"raster-image","renditions":[{"name":"card","width":600,"height":400,"fit":"cover"}]}}]}]
	}`)
}

func TestParseManifestValidatesV2TypedInputContract(t *testing.T) {
	m, err := ParseManifest(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	if m.Key != "product-detail" || m.RuntimeInput.Mode != "single" || m.Entry != "assets/entry.js" {
		t.Fatalf("unexpected manifest: %#v", m)
	}
	var schema struct {
		Properties map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(m.RuntimeInput.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	product := schema.Properties["product"]
	if len(product.Properties) != 2 || product.Properties["artwork"] == nil {
		t.Fatalf("typed inputs did not produce the expected runtime input shape: %s", m.RuntimeInput.Schema)
	}
}

func TestParseManifestRejectsLegacyContentAndRequiresDataInputs(t *testing.T) {
	legacy := strings.Replace(string(validManifest()), `"formatVersion":2`, `"formatVersion":1`, 1)
	if _, err := ParseManifest([]byte(legacy)); err == nil || !strings.Contains(err.Error(), "formatVersion must be 2") {
		t.Fatalf("format 1 manifest was not rejected with a migration error: %v", err)
	}

	var withoutInputs map[string]any
	if err := json.Unmarshal(validManifest(), &withoutInputs); err != nil {
		t.Fatal(err)
	}
	delete(withoutInputs, "dataInputs")
	withoutInputsRaw, err := json.Marshal(withoutInputs)
	if err != nil {
		t.Fatal(err)
	}
	missingInputs := string(withoutInputsRaw)
	if _, err := ParseManifest([]byte(missingInputs)); err == nil {
		t.Fatal("missing dataInputs was accepted")
	}
	withContent := strings.TrimSuffix(strings.TrimSpace(string(validManifest())), "}") + `,"content":{"mode":"single","schema":{"type":"object"}}}`
	if _, err := ParseManifest([]byte(withContent)); err == nil || !strings.Contains(err.Error(), "unknown field \"content\"") {
		t.Fatalf("legacy feature CMS schema was not rejected: %v", err)
	}
	if _, err := ParseManifest([]byte(strings.Replace(string(validManifest()), `"dataInputs":[`, `"dataInputs":null,"discarded":[`, 1))); err == nil {
		t.Fatal("null or malformed dataInputs was accepted")
	}
}

func TestUnmarshalRejectsLegacyManifestEverywhere(t *testing.T) {
	raw := []byte(`{"formatVersion":1,"key":"legacy-form","name":"Legacy form","version":"1.2.3","engineApi":"1","entry":"entry.js","slots":["main"],"arguments":{"type":"object","properties":{}},"content":{"mode":"form","schema":{"type":"object","properties":{"title":{"type":"string"}}},"form":{"submissionSchema":{"type":"object","properties":{"email":{"type":"string","format":"email"}},"required":["email"],"additionalProperties":false}}}}`)
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err == nil || !strings.Contains(err.Error(), "legacy feature content schemas are unsupported") {
		t.Fatalf("stored legacy manifest must be rejected: %v", err)
	}
	if _, err := ParseManifest(raw); err == nil || !strings.Contains(err.Error(), "formatVersion must be 2") {
		t.Fatalf("legacy manifest must be rejected: %v", err)
	}
}

func TestParseManifestRequiresValidSemVer(t *testing.T) {
	for _, version := range []string{"1.2.3-01", "1.2.3-alpha..1", "1.2.3+meta..1", "01.2.3"} {
		t.Run("rejects "+version, func(t *testing.T) {
			raw := strings.Replace(string(validManifest()), `"version":"2.0.0"`, `"version":"`+version+`"`, 1)
			if _, err := ParseManifest([]byte(raw)); err == nil {
				t.Fatalf("invalid SemVer %q was accepted", version)
			}
		})
	}
	for _, version := range []string{"2.0.0-alpha.1", "2.0.0-01alpha", "2.0.0+build.01"} {
		t.Run("accepts "+version, func(t *testing.T) {
			raw := strings.Replace(string(validManifest()), `"version":"2.0.0"`, `"version":"`+version+`"`, 1)
			if _, err := ParseManifest([]byte(raw)); err != nil {
				t.Fatalf("valid SemVer %q was rejected: %v", version, err)
			}
		})
	}
}

func TestParseManifestSupportsOnlyExplicitStaticRuntimeContracts(t *testing.T) {
	for _, tc := range []struct {
		abi   string
		entry string
		want  string
	}{
		{abi: RuntimeABIStaticModule, entry: "dist/entry.mjs", want: RuntimeABIStaticModule},
		{abi: RuntimeABIStaticHTML, entry: "dist/fragment.html", want: RuntimeABIStaticHTML},
	} {
		raw := runtimeManifest(t, tc.abi, tc.entry)
		manifest, err := ParseManifest([]byte(raw))
		if err != nil || manifest.RuntimeABI() != tc.want || manifest.RuntimeEntry() != tc.entry {
			t.Fatalf("runtime %q was rejected or decoded incorrectly: manifest=%#v err=%v", tc.abi, manifest, err)
		}
	}

	invalid := []struct {
		name string
		raw  string
	}{
		{name: "unknown ABI", raw: strings.Replace(runtimeManifest(t, RuntimeABIStaticModule, "entry.js"), RuntimeABIStaticModule, "static-wasm-v1", 1)},
		{name: "module with HTML entry", raw: runtimeManifest(t, RuntimeABIStaticModule, "entry.html")},
		{name: "HTML with module entry", raw: runtimeManifest(t, RuntimeABIStaticHTML, "entry.js")},
		{name: "mixed root and runtime declarations", raw: strings.Replace(string(validManifest()), `"engineApi":"1",`, `"engineApi":"1","runtime":{"abi":"static-module-v1","entry":"entry.js"},`, 1)},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.raw)); err == nil {
				t.Fatal("expected invalid runtime contract to be rejected")
			}
		})
	}
}

func TestParseManifestSupportsSharedStylesInsideStaticRuntime(t *testing.T) {
	raw := runtimeManifest(t, RuntimeABIStaticModule, "dist/entry.js", "dist/feature.css")
	raw = strings.Replace(raw, `"styles":["dist/feature.css"]`, `"styles":["dist/feature.css"],"sharedStyles":["dist/core.css"]`, 1)
	manifest, err := ParseManifest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.RuntimeSharedStyles(); len(got) != 1 || got[0] != "dist/core.css" {
		t.Fatalf("runtime shared styles = %v", got)
	}

	legacyRootField := strings.Replace(raw, `"runtime":`, `"sharedStyles":["dist/core.css"],"runtime":`, 1)
	if _, err := ParseManifest([]byte(legacyRootField)); err == nil {
		t.Fatal("runtime builds must not mix root and nested shared style declarations")
	}

	duplicate := strings.Replace(raw, `"sharedStyles":["dist/core.css"]`, `"sharedStyles":["dist/core.css","dist/core.css"]`, 1)
	if _, err := ParseManifest([]byte(duplicate)); err == nil {
		t.Fatal("duplicate runtime shared styles must be rejected")
	}
}

func TestParseManifestDerivesCollectionAndFormRuntimeModes(t *testing.T) {
	collection := strings.Replace(string(validManifest()), `"kind":"record"`, `"kind":"collection"`, 1)
	manifest, err := ParseManifest([]byte(collection))
	if err != nil || manifest.RuntimeInput.Mode != "collection" {
		t.Fatalf("single collection input should derive collection runtime mode: mode=%q err=%v", manifest.RuntimeInput.Mode, err)
	}
	form := strings.Replace(string(validManifest()), `"dataInputs":[`, `"form":{"submissionSchema":{"type":"object","properties":{"email":{"type":"string","format":"email"}},"required":["email"],"additionalProperties":false}},"dataInputs":[`, 1)
	manifest, err = ParseManifest([]byte(form))
	if err != nil || manifest.RuntimeInput.Mode != "form" || len(manifest.SubmissionSchema()) == 0 {
		t.Fatalf("form submission schema should be accepted independently: mode=%q err=%v", manifest.RuntimeInput.Mode, err)
	}
}

func TestParseManifestRejectsInvalidTypedInputContract(t *testing.T) {
	cases := []struct {
		name string
		edit func(string) string
	}{
		{"invalid input kind", func(s string) string { return strings.Replace(s, `"kind":"record"`, `"kind":"page"`, 1) }},
		{"duplicate input path", func(s string) string {
			return strings.Replace(s, `{"path":"/artwork/main","type":"media","profile":{"accept":"raster-image","renditions":[{"name":"card","width":600,"height":400,"fit":"cover"}]}}`, `{"path":"/title","type":"text"}`, 1)
		}},
		{"invalid media profile", func(s string) string { return strings.Replace(s, `"accept":"raster-image"`, `"accept":"unknown"`, 1) }},
		{"unsupported field type", func(s string) string { return strings.Replace(s, `"type":"text"`, `"type":"object"`, 1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.edit(string(validManifest())))); err == nil {
				t.Fatal("expected invalid typed input to be rejected")
			}
		})
	}
}

func TestParseManifestRejectsUnsafeAssetsAndUnsupportedEngineAPI(t *testing.T) {
	cases := []struct {
		name string
		edit func(string) string
	}{
		{"unsafe entry", func(s string) string { return strings.Replace(s, `assets/entry.js`, `../entry.js`, 1) }},
		{"unsupported engine api", func(s string) string { return strings.Replace(s, `"engineApi":"1"`, `"engineApi":"9"`, 1) }},
		{"remote argument schema ref", func(s string) string {
			return strings.Replace(s, `"title":"Slug"`, `"$ref":"https://example.test/schema.json"`, 1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.edit(string(validManifest())))); err == nil {
				t.Fatal("expected invalid manifest to be rejected")
			}
		})
	}
}
