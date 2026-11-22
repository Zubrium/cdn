package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	engine "github.com/Zubrium/cdn/engines/router/presentation"
	websiteruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
)

const (
	crossManifestID = "01938f7a-68af-7c10-a51d-78b6ca125301"
	crossDocumentID = "01938f7a-68af-7c10-a51d-78b6ca125302"
	crossAssetID    = "01938f7a-68af-7c10-a51d-78b6ca125304"
)

type crossObject struct {
	file core.File
	body string
}

type crossFiles struct {
	objects map[string]crossObject
}

func (files *crossFiles) StatFile(_ context.Context, _ core.Session, id string) (core.File, error) {
	object, ok := files.objects[id]
	if !ok {
		return core.File{}, core.ErrFileNotFound
	}
	return object.file, nil
}

func (files *crossFiles) ReadFile(_ context.Context, _ core.Session, id string) (io.ReadCloser, core.File, error) {
	object, ok := files.objects[id]
	if !ok {
		return nil, core.File{}, core.ErrFileNotFound
	}
	return io.NopCloser(strings.NewReader(object.body)), object.file, nil
}

type crossIdentity struct{}

func (crossIdentity) Authenticate(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{}, nil
}

type crossRoutes struct{ route model.Route }

func (routes crossRoutes) ResolveRoute(context.Context, string, string) (model.Route, error) {
	if routes.route.ID == "" {
		return model.Route{}, engine.ErrNotFound
	}
	return routes.route, nil
}

func crossTime(t *testing.T) time.Time {
	t.Helper()
	result, err := time.Parse(time.RFC3339, "2026-11-18T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func crossCoreFile(id, body, contentType string) core.File {
	sum := sha256.Sum256([]byte(body))
	return core.File{ID: id, ContentType: contentType, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Revision: 1}
}

func crossArtifact(t *testing.T, cacheClass string, mutatePayload func(map[string]any), mutateManifest func(map[string]any)) ([]byte, *crossFiles, map[string]websiteruntime.CoreFileMetadata) {
	t.Helper()
	documentBody := "<!doctype html><title>cross-module contract</title>"
	assetBody := "export const ready = true;"
	document := crossCoreFile(crossDocumentID, documentBody, "text/html; charset=utf-8")
	asset := crossCoreFile(crossAssetID, assetBody, "text/javascript; charset=utf-8")
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
	manifestFile := crossCoreFile(crossManifestID, string(data), "application/json")
	files := &crossFiles{objects: map[string]crossObject{
		manifestFile.ID: {file: manifestFile, body: string(data)},
		document.ID:     {file: document, body: documentBody},
		asset.ID:        {file: asset, body: assetBody},
	}}
	observed := map[string]websiteruntime.CoreFileMetadata{
		document.ID: {ContentType: document.ContentType, SHA256: document.SHA256, Size: document.Size},
		asset.ID:    {ContentType: asset.ContentType, SHA256: asset.SHA256, Size: asset.Size},
	}
	return data, files, observed
}

func crossRouterTargetValid(t *testing.T, files *crossFiles) error {
	t.Helper()
	validator, err := NewCoreTargetValidator(crossIdentity{}, files, "router-service-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	return validator.ValidateRouteTarget(context.Background(),
		model.Target{Kind: model.TargetStaticSiteArtifact, FileID: crossManifestID}, model.MatchPrefix)
}

func TestWebsiteAndRouterShareArtifactCanonicalPayload(t *testing.T) {
	data, _, _ := crossArtifact(t, "immutable", nil, nil)
	websiteCanonical, err := websiteruntime.CanonicalArtifactPayload(data)
	if err != nil {
		t.Fatal(err)
	}
	routerCanonical, err := canonicalArtifactPayload(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(websiteCanonical, routerCanonical) {
		t.Fatalf("canonical payloads differ:\nwebsite: %s\nrouter:  %s", websiteCanonical, routerCanonical)
	}
	websiteDigest, err := websiteruntime.ArtifactDigest(data)
	if err != nil {
		t.Fatal(err)
	}
	routerDigest, err := artifactPayloadDigest(data)
	if err != nil || websiteDigest != routerDigest {
		t.Fatalf("digests differ: website=%q router=%q err=%v", websiteDigest, routerDigest, err)
	}
}

func TestArtifactManifestPassesBothValidators(t *testing.T) {
	for _, class := range []string{"immutable", "short", "none", ""} {
		t.Run(class, func(t *testing.T) {
			data, files, observed := crossArtifact(t, class, nil, nil)
			manifest, err := websiteruntime.ValidateArtifact(data, 8, 1<<20)
			if err != nil {
				t.Fatalf("Website validator rejected safe class %q: %v", class, err)
			}
			if err := websiteruntime.VerifyArtifactFiles(manifest, observed); err != nil {
				t.Fatalf("Website file verification failed: %v", err)
			}
			if _, _, err := readArtifactManifest(context.Background(), files, core.Session{}, crossManifestID); err != nil {
				t.Fatalf("Router validator rejected safe class %q: %v", class, err)
			}
			if err := crossRouterTargetValid(t, files); err != nil {
				t.Fatalf("Router target activation rejected safe class %q: %v", class, err)
			}
		})
	}
}

func TestBothValidatorsRejectDigestAndCacheClassSubstitution(t *testing.T) {
	t.Run("digest", func(t *testing.T) {
		data, files, _ := crossArtifact(t, "immutable", nil, func(manifest map[string]any) {
			manifest["digest"] = "sha256:" + strings.Repeat("0", 64)
		})
		if _, err := websiteruntime.ValidateArtifact(data, 8, 1<<20); !errors.Is(err, websiteruntime.ErrArtifact) {
			t.Fatalf("Website digest check returned %v, want ErrArtifact", err)
		}
		if _, _, err := readArtifactManifest(context.Background(), files, core.Session{}, crossManifestID); !errors.Is(err, engine.ErrInvalidTarget) {
			t.Fatalf("Router digest check returned %v, want ErrInvalidTarget", err)
		}
	})
	for _, class := range []string{"document", "forever"} {
		t.Run("cache class "+class, func(t *testing.T) {
			data, files, _ := crossArtifact(t, class, nil, nil)
			if _, err := websiteruntime.ValidateArtifact(data, 8, 1<<20); !errors.Is(err, websiteruntime.ErrArtifact) {
				t.Fatalf("Website cache class %q returned %v, want ErrArtifact", class, err)
			}
			if _, _, err := readArtifactManifest(context.Background(), files, core.Session{}, crossManifestID); !errors.Is(err, engine.ErrInvalidTarget) {
				t.Fatalf("Router cache class %q returned %v, want ErrInvalidTarget", class, err)
			}
		})
	}
	t.Run("document cache class", func(t *testing.T) {
		data, files, _ := crossArtifact(t, "", func(payload map[string]any) {
			payload["document"].(map[string]any)["cache_class"] = ""
		}, nil)
		if _, err := websiteruntime.ValidateArtifact(data, 8, 1<<20); !errors.Is(err, websiteruntime.ErrArtifact) {
			t.Fatalf("Website document cache class returned %v, want ErrArtifact", err)
		}
		if _, _, err := readArtifactManifest(context.Background(), files, core.Session{}, crossManifestID); !errors.Is(err, engine.ErrInvalidTarget) {
			t.Fatalf("Router document cache class returned %v, want ErrInvalidTarget", err)
		}
	})
}

func TestBothValidatorsRejectMismatchedCoreFileMetadata(t *testing.T) {
	data, files, observed := crossArtifact(t, "immutable", nil, nil)
	manifest, err := websiteruntime.ValidateArtifact(data, 8, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	observed[crossAssetID] = websiteruntime.CoreFileMetadata{
		ContentType: "text/javascript; charset=utf-8", SHA256: strings.Repeat("0", 64), Size: 21,
	}
	if err := websiteruntime.VerifyArtifactFiles(manifest, observed); !errors.Is(err, websiteruntime.ErrFileMeta) {
		t.Fatalf("Website metadata mismatch returned %v, want ErrFileMeta", err)
	}
	asset := files.objects[crossAssetID]
	asset.file.SHA256 = strings.Repeat("0", 64)
	files.objects[crossAssetID] = asset
	if err := crossRouterTargetValid(t, files); !errors.Is(err, engine.ErrInvalidTarget) {
		t.Fatalf("Router metadata mismatch returned %v, want ErrInvalidTarget", err)
	}
}

func TestRouterDocumentDeliveryUsesNoStoreWhileAssetsUseSafeCachePolicy(t *testing.T) {
	_, files, _ := crossArtifact(t, "immutable", nil, nil)
	route, err := model.NewRoute("01938f7a-68af-7c10-a51d-78b6ca125303", "main", "example.com", "/site", model.MatchPrefix,
		model.Target{Kind: model.TargetStaticSiteArtifact, FileID: crossManifestID}, crossTime(t))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := NewCoreDelivery("main", "router-service-key", crossIdentity{}, files, crossRoutes{route: route}, nil)
	if err != nil {
		t.Fatal(err)
	}
	document, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{
		BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/site",
	})
	if err != nil || document.CacheControl != "no-store" {
		t.Fatalf("document delivery = %+v, %v", document, err)
	}
	asset, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{
		BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/site/assets/app.js",
	})
	if err != nil || asset.CacheControl != "public, max-age=31536000, immutable" {
		t.Fatalf("asset delivery = %+v, %v", asset, err)
	}
}
