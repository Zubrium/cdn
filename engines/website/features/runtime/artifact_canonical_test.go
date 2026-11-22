package runtime_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func TestCanonicalArtifactPayloadIgnoresInputOrderAndDigestField(t *testing.T) {
	const (
		documentID = "11111111-1111-4111-8111-111111111111"
		assetID    = "22222222-2222-4222-8222-222222222222"
	)
	payload := map[string]any{
		"format": "static-page-artifact/v1",
		"document": map[string]any{
			"content_type": "text/html; charset=utf-8",
			"file_id":      documentID,
			"sha256":       strings.Repeat("a", 64),
			"size":         120,
		},
		"assets": map[string]any{
			"assets/app.js": map[string]any{
				"cache_class":  "immutable",
				"content_type": "text/javascript; charset=utf-8",
				"file_id":      assetID,
				"sha256":       strings.Repeat("b", 64),
				"size":         64,
			},
		},
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	manifest := map[string]any{"format": payload["format"], "digest": "sha256:" + hex.EncodeToString(digest[:]),
		"document": payload["document"], "assets": payload["assets"]}
	ordered, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// Reorder every object and add insignificant whitespace. The canonical
	// payload must be unchanged because it is derived from the JSON value.
	reordered := []byte(`{
		"assets": { "assets/app.js": { "size": 64, "sha256": "` + strings.Repeat("b", 64) + `",
			"file_id": "` + assetID + `", "content_type": "text/javascript; charset=utf-8", "cache_class": "immutable" } },
		"digest": "` + "sha256:" + hex.EncodeToString(digest[:]) + `",
		"document": { "size": 120, "sha256": "` + strings.Repeat("a", 64) + `",
			"file_id": "` + documentID + `", "content_type": "text/html; charset=utf-8" },
		"format": "static-page-artifact/v1"
	}`)
	for name, data := range map[string][]byte{"ordered": ordered, "reordered": reordered} {
		got, err := runtime.CanonicalArtifactPayload(data)
		if err != nil {
			t.Fatalf("%s: canonical payload: %v", name, err)
		}
		if !bytes.Equal(got, canonical) {
			t.Fatalf("%s: canonical payload = %s, want %s", name, got, canonical)
		}
		gotDigest, err := runtime.ArtifactDigest(data)
		if err != nil || gotDigest != "sha256:"+hex.EncodeToString(digest[:]) {
			t.Fatalf("%s: digest = %q, %v", name, gotDigest, err)
		}
	}
}

func TestCanonicalArtifactPayloadRejectsDuplicateKeysAndTrailingData(t *testing.T) {
	for _, data := range []string{
		`{"format":"static-page-artifact/v1","format":"static-page-artifact/v1"}`,
		`{"format":"static-page-artifact/v1"} {}`,
		`{"format":"static-page-artifact/v1","assets":{}} trailing`,
	} {
		if _, err := runtime.CanonicalArtifactPayload([]byte(data)); err == nil {
			t.Errorf("canonical payload accepted invalid JSON: %s", data)
		}
	}
}

func TestArtifactValidationRejectsDocumentCacheClassField(t *testing.T) {
	payload := map[string]any{
		"format":   "static-page-artifact/v1",
		"document": map[string]any{"file_id": "11111111-1111-4111-8111-111111111111", "content_type": "text/html; charset=utf-8", "sha256": strings.Repeat("a", 64), "size": 120},
		"assets":   map[string]any{},
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	manifest := map[string]any{"format": payload["format"], "digest": "sha256:" + hex.EncodeToString(sum[:]),
		"document": payload["document"], "assets": payload["assets"]}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ValidateArtifact(data, 8, 1<<20); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}
	document := payload["document"].(map[string]any)
	document["cache_class"] = ""
	canonical, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(canonical)
	manifest["digest"] = "sha256:" + hex.EncodeToString(sum[:])
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ValidateArtifact(data, 8, 1<<20); !errors.Is(err, runtime.ErrArtifact) {
		t.Fatalf("document with explicit empty cache class returned %v, want ErrArtifact", err)
	}
}
