package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	contractManifestID = "01938f7a-68af-7c10-a51d-78b6ca125301"
	contractDocumentID = "01938f7a-68af-7c10-a51d-78b6ca125302"
	contractAssetID    = "01938f7a-68af-7c10-a51d-78b6ca125304"
)

type contractObject struct {
	file contracts.File
	body string
}

type contractFiles struct {
	objects map[string]contractObject
}

func (files *contractFiles) StatFile(_ context.Context, id string) (contracts.File, error) {
	object, ok := files.objects[id]
	if !ok {
		return contracts.File{}, contracts.ErrFileNotFound
	}
	return object.file, nil
}

func (files *contractFiles) ReadFile(_ context.Context, id string) (io.ReadCloser, contracts.File, error) {
	object, ok := files.objects[id]
	if !ok {
		return nil, contracts.File{}, contracts.ErrFileNotFound
	}
	return io.NopCloser(strings.NewReader(object.body)), object.file, nil
}

func contractCoreFile(id, body, contentType string) contracts.File {
	sum := sha256.Sum256([]byte(body))
	return contracts.File{ID: id, ContentType: contentType, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Revision: 1}
}

// contractArtifact builds a manifest whose digest is the canonical SHA-256 of
// the payload with sorted object keys, exactly as the Website runtime does.
func contractArtifact(t *testing.T, cacheClass string, mutatePayload func(map[string]any), mutateManifest func(map[string]any)) ([]byte, *contractFiles) {
	t.Helper()
	documentBody := "<!doctype html><title>contract</title>"
	assetBody := "export const ready = true;"
	document := contractCoreFile(contractDocumentID, documentBody, "text/html; charset=utf-8")
	asset := contractCoreFile(contractAssetID, assetBody, "text/javascript; charset=utf-8")
	assetFields := map[string]any{
		"content_type": asset.ContentType,
		"file_id":      asset.ID,
		"sha256":       asset.SHA256,
		"size":         asset.Size,
	}
	if cacheClass != "" {
		assetFields["cache_class"] = cacheClass
	}
	payload := map[string]any{
		"assets": map[string]any{"assets/app.js": assetFields},
		"document": map[string]any{
			"content_type": document.ContentType,
			"file_id":      document.ID,
			"sha256":       document.SHA256,
			"size":         document.Size,
		},
		"format": "static-page-artifact/v1",
	}
	if mutatePayload != nil {
		mutatePayload(payload)
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	manifest := map[string]any{
		"digest":   "sha256:" + hex.EncodeToString(sum[:]),
		"format":   "static-page-artifact/v1",
		"assets":   payload["assets"],
		"document": payload["document"],
	}
	if mutateManifest != nil {
		mutateManifest(manifest)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestFile := contractCoreFile(contractManifestID, string(data), "application/json")
	files := &contractFiles{objects: map[string]contractObject{
		manifestFile.ID: {file: manifestFile, body: string(data)},
		document.ID:     {file: document, body: documentBody},
		asset.ID:        {file: asset, body: assetBody},
	}}
	return data, files
}

func TestRouterCanonicalArtifactPayloadIsRepresentationIndependent(t *testing.T) {
	data, _ := contractArtifact(t, "immutable", nil, nil)
	var envelope struct {
		Format   string          `json:"format"`
		Digest   string          `json:"digest"`
		Document json.RawMessage `json:"document"`
		Assets   json.RawMessage `json:"assets"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalArtifactPayload(data)
	if err != nil {
		t.Fatal(err)
	}
	reordered := []byte(`{"assets":` + string(envelope.Assets) + `,"document":` + string(envelope.Document) + `,"format":"static-page-artifact/v1"}`)
	reorderedCanonical, err := canonicalArtifactPayload(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, reorderedCanonical) {
		t.Fatalf("canonical payloads differ: %s vs %s", canonical, reorderedCanonical)
	}
	if bytes.Contains(canonical, []byte(`"digest"`)) {
		t.Fatalf("canonical payload contains digest: %s", canonical)
	}
	for _, invalid := range []string{
		`{"format":"static-page-artifact/v1","format":"static-page-artifact/v1"}`,
		`{"format":"static-page-artifact/v1"} {}`,
	} {
		if _, err := canonicalArtifactPayload([]byte(invalid)); err == nil {
			t.Errorf("invalid JSON accepted: %s", invalid)
		}
	}
}

func TestRouterAcceptsSharedSafeCacheClasses(t *testing.T) {
	for class, want := range map[string]string{
		"immutable": "public, max-age=31536000, immutable",
		"short":     "public, max-age=300",
		"none":      "no-store",
		"":          "no-store",
	} {
		t.Run(class, func(t *testing.T) {
			_, files := contractArtifact(t, class, nil, nil)
			manifest, _, err := readArtifactManifest(context.Background(), files, contractManifestID)
			if err != nil {
				t.Fatalf("safe cache class %q rejected: %v", class, err)
			}
			if got := artifactCacheControl(manifest.assets["assets/app.js"]); got != want {
				t.Fatalf("cache class %q = %q, want %q", class, got, want)
			}
		})
	}
}

func TestRouterRejectsDigestAndCacheClassSubstitution(t *testing.T) {
	t.Run("digest", func(t *testing.T) {
		_, files := contractArtifact(t, "immutable", nil, func(manifest map[string]any) {
			manifest["digest"] = "sha256:" + strings.Repeat("0", 64)
		})
		if _, _, err := readArtifactManifest(context.Background(), files, contractManifestID); !errors.Is(err, engine.ErrInvalidTarget) {
			t.Fatalf("tampered digest returned %v, want ErrInvalidTarget", err)
		}
	})
	for _, class := range []string{"document", "forever"} {
		t.Run("cache class "+class, func(t *testing.T) {
			_, files := contractArtifact(t, class, nil, nil)
			if _, _, err := readArtifactManifest(context.Background(), files, contractManifestID); !errors.Is(err, engine.ErrInvalidTarget) {
				t.Fatalf("cache class %q returned %v, want ErrInvalidTarget", class, err)
			}
		})
	}
	t.Run("document cache class", func(t *testing.T) {
		_, files := contractArtifact(t, "", func(payload map[string]any) {
			payload["document"].(map[string]any)["cache_class"] = ""
		}, nil)
		if _, _, err := readArtifactManifest(context.Background(), files, contractManifestID); !errors.Is(err, engine.ErrInvalidTarget) {
			t.Fatalf("document cache class returned %v, want ErrInvalidTarget", err)
		}
	})
}
