package runtime_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func TestABIOnlyAcceptsTheUnversionedName(t *testing.T) {
	if manifest, err := runtime.ValidateManifest(mustValidManifest(t), featureFiles()); err != nil || manifest.Runtime.ABI != runtime.ABI {
		t.Fatalf("valid manifest rejected: manifest=%+v err=%v", manifest, err)
	}
	for _, rejected := range []string{"static-lifecycle-v2", "static-lifecycle-v1", "static-module-v1", "v2", ""} {
		manifest := strings.Replace(string(mustValidManifest(t)), `"abi": "static-lifecycle"`, `"abi": "`+rejected+`"`, 1)
		_, err := runtime.ValidateManifest([]byte(manifest), featureFiles())
		if !errors.Is(err, runtime.ErrABI) {
			t.Fatalf("ABI %q: err=%v, want ErrABI", rejected, err)
		}
	}
}

func TestManifestRejectsDuplicateUnknownAndSecretFields(t *testing.T) {
	cases := map[string]string{
		"duplicate key":      `{"formatVersion":2,"formatVersion":2}`,
		"unknown field":      strings.Replace(string(mustValidManifest(t)), `"engineApi": "v1",`, `"engineApi": "v1", "unexpected": true,`, 1),
		"secret argument":    strings.Replace(string(mustValidManifest(t)), `"title": "text"`, `"api_key": "text"`, 1),
		"invalid version":    strings.Replace(string(mustValidManifest(t)), `"version": "1.2.3"`, `"version": "v2"`, 1),
		"invalid data kind":  strings.Replace(string(mustValidManifest(t)), `"kind": "collection"`, `"kind": "relation"`, 1),
		"invalid field type": strings.Replace(string(mustValidManifest(t)), `"path": "title", "type": "text"`, `"path": "title", "type": "script"`, 1),
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := runtime.ValidateManifest([]byte(manifest), featureFiles()); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestManifestValidatesAssetsOriginsAndSecretFreePublicContract(t *testing.T) {
	files := featureFiles()
	if _, err := runtime.ValidateManifest(mustValidManifest(t), files); err != nil {
		t.Fatal(err)
	}
	missing := strings.Replace(string(mustValidManifest(t)), `"assets/app.css"`, `"assets/missing.css"`, 1)
	if _, err := runtime.ValidateManifest([]byte(missing), files); !errors.Is(err, runtime.ErrAsset) {
		t.Fatalf("missing asset: err=%v", err)
	}
	traversal := strings.Replace(string(mustValidManifest(t)), `"entry": "assets/app.js"`, `"entry": "../outside.js"`, 1)
	if _, err := runtime.ValidateManifest([]byte(traversal), files); !errors.Is(err, runtime.ErrAsset) {
		t.Fatalf("traversal entry: err=%v", err)
	}
	for _, origin := range []string{"http://api.example.com", "https://*", "https://127.0.0.1", "https://localhost", "https://user@api.example.com"} {
		manifest := strings.Replace(string(mustValidManifest(t)), `"https://api.example.com"`, `"`+origin+`"`, 1)
		if _, err := runtime.ValidateManifest([]byte(manifest), files); !errors.Is(err, runtime.ErrOrigin) {
			t.Fatalf("origin %q: err=%v, want ErrOrigin", origin, err)
		}
	}
}

func web023ABIAndManifestValidationAreStrict(t *testing.T) {
	TestABIOnlyAcceptsTheUnversionedName(t)
	TestManifestRejectsDuplicateUnknownAndSecretFields(t)
	TestManifestValidatesAssetsOriginsAndSecretFreePublicContract(t)
	TestManifestDigestBindsImmutableBytes(t)
}

func TestManifestDigestBindsImmutableBytes(t *testing.T) {
	first := runtime.ManifestDigest(mustValidManifest(t))
	second := runtime.ManifestDigest(mustValidManifest(t))
	changed := runtime.ManifestDigest([]byte(strings.Replace(string(mustValidManifest(t)), `"name": "Hero"`, `"name": "Other"`, 1)))
	if first != second || first == changed || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("digest is not deterministic and content-bound: %q %q %q", first, second, changed)
	}
}
