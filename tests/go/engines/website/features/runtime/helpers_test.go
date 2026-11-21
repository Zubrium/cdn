package runtime_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const validFeatureManifest = `{
  "formatVersion": 2,
  "key": "hero",
  "name": "Hero",
  "version": "1.2.3",
  "engineApi": "v1",
  "slots": ["main"],
  "arguments": {"title": "text"},
  "dataInputs": [{"key": "items", "kind": "collection", "type": "text", "fields": [{"path": "title", "type": "text"}]}],
  "runtime": {
    "abi": "static-lifecycle",
    "entry": "assets/app.js",
    "styles": ["assets/app.css"],
    "origins": ["https://api.example.com"],
    "sdk": ["api"]
  }
}`

func featureFiles() map[string]struct{} {
	return map[string]struct{}{"assets/app.js": {}, "assets/app.css": {}}
}

func mustValidManifest(t *testing.T) []byte {
	t.Helper()
	return []byte(validFeatureManifest)
}

func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func zipWithHeader(t *testing.T, name string, mode os.FileMode, content string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func duplicateEntryZip(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for range 2 {
		entry, err := writer.Create("assets/app.js")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("export function render() {}")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

// artifactBytes builds a canonical artifact manifest with a matching digest.
func artifactBytes(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	documentID := "11111111-1111-4111-8111-111111111111"
	assetID := "22222222-2222-4222-8222-222222222222"
	payload := map[string]any{
		"format": "static-page-artifact/v1",
		"document": map[string]any{
			"file_id": documentID, "content_type": "text/html; charset=utf-8",
			"sha256": strings.Repeat("a", 64), "size": 120,
		},
		"assets": map[string]any{
			"assets/app.js": map[string]any{
				"file_id": assetID, "content_type": "text/javascript; charset=utf-8",
				"sha256": strings.Repeat("b", 64), "size": 64, "cache_class": "immutable",
			},
		},
	}
	if mutate != nil {
		mutate(payload)
	}
	withoutDigest, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(withoutDigest)
	payload["digest"] = "sha256:" + hex.EncodeToString(sum[:])
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
