package router

import (
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
	"github.com/Zubrium/cdn/presentation/httpjson"
)

const (
	deliveryManifestID = "01938f7a-68af-7c10-a51d-78b6ca125301"
	deliveryPageID     = "01938f7a-68af-7c10-a51d-78b6ca125302"
	deliveryRouteID    = "01938f7a-68af-7c10-a51d-78b6ca125303"
)

type deliveryIdentity struct{ err error }

func (identity deliveryIdentity) Authenticate(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{}, identity.err
}

type deliveryFiles struct {
	objects   map[string]deliveryObject
	forbidden map[string]bool
	statCalls int
	readCalls int
}

type deliveryObject struct {
	file core.File
	body string
}

func (files *deliveryFiles) StatFile(_ context.Context, _ core.Session, id string) (core.File, error) {
	files.statCalls++
	if files.forbidden[id] {
		return core.File{}, core.ErrForbidden
	}
	object, ok := files.objects[id]
	if !ok {
		return core.File{}, core.ErrFileNotFound
	}
	return object.file, nil
}

func (files *deliveryFiles) ReadFile(_ context.Context, _ core.Session, id string) (io.ReadCloser, core.File, error) {
	files.readCalls++
	if files.forbidden[id] {
		return nil, core.File{}, core.ErrForbidden
	}
	object, ok := files.objects[id]
	if !ok {
		return nil, core.File{}, core.ErrFileNotFound
	}
	return io.NopCloser(strings.NewReader(object.body)), object.file, nil
}

type deliveryRoutes struct{ route model.Route }

func (routes deliveryRoutes) ResolveRoute(context.Context, string, string) (model.Route, error) {
	if routes.route.ID == "" {
		return model.Route{}, engine.ErrNotFound
	}
	return routes.route, nil
}

func newDeliveryRoute(t *testing.T, target model.Target, match, path string) model.Route {
	t.Helper()
	route, err := model.NewRoute(deliveryRouteID, "main", "example.com", path, match, target,
		parseDeliveryTime(t, "2026-11-18T12:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func TestCoreFileDeliveryUsesOnlyConfiguredIDAndRechecksCoreAccess(t *testing.T) {
	body := "private bytes now intentionally published"
	file := deliveryFile(deliveryPageID, body, "application/pdf")
	files := &deliveryFiles{objects: map[string]deliveryObject{file.ID: {file: file, body: body}}, forbidden: map[string]bool{}}
	target := model.Target{Kind: model.TargetCoreFile, FileID: file.ID}
	route := newDeliveryRoute(t, target, model.MatchExact, "/download")
	delivery, err := NewCoreDelivery("main", "router-service-key", deliveryIdentity{}, files, deliveryRoutes{route: route}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{
		BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/download",
	})
	if err != nil || response.StatusCode != http.StatusOK || response.CacheControl != "no-store" || response.ContentDisposition != "attachment" ||
		response.ETag != `"sha256-`+file.SHA256+`"` || response.ContentLength != int64(len(body)) {
		t.Fatalf("Core file response = %+v, %v", response, err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(data) != body || files.readCalls != 1 {
		t.Fatalf("Core file bytes = %q, reads=%d, err=%v", data, files.readCalls, err)
	}
	files.forbidden[file.ID] = true
	if _, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{
		BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/download",
	}); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("revoked Core access returned %v, want hidden not-found", err)
	}
	if _, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{
		BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/different",
	}); err == nil {
		t.Fatal("visitor-selected path bypassed the configured route")
	}
}

func TestCoreFileHeadAndConditionalGetAuthorizeBeforeReturningHeaders(t *testing.T) {
	body := "file"
	file := deliveryFile(deliveryPageID, body, "application/pdf")
	files := &deliveryFiles{objects: map[string]deliveryObject{file.ID: {file: file, body: body}}, forbidden: map[string]bool{}}
	route := newDeliveryRoute(t, model.Target{Kind: model.TargetCoreFile, FileID: file.ID}, model.MatchExact, "/download")
	delivery, err := NewCoreDelivery("main", "router-service-key", deliveryIdentity{}, files, deliveryRoutes{route: route}, nil)
	if err != nil {
		t.Fatal(err)
	}
	head, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{BranchID: "main", Host: "example.com", Method: http.MethodHead, EscapedPath: "/download"})
	if err != nil || head.StatusCode != http.StatusOK || head.Body != nil || head.ContentLength != int64(len(body)) || files.statCalls != 1 {
		t.Fatalf("HEAD response=%+v stat=%d err=%v", head, files.statCalls, err)
	}
	conditional, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{BranchID: "main", Host: "example.com", Method: http.MethodGet,
		EscapedPath: "/download", IfNoneMatch: head.ETag})
	if err != nil || conditional.StatusCode != http.StatusNotModified || conditional.Body != nil || files.statCalls != 2 {
		t.Fatalf("conditional response=%+v stat=%d err=%v", conditional, files.statCalls, err)
	}
	files.forbidden[file.ID] = true
	if _, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{BranchID: "main", Host: "example.com", Method: http.MethodHead,
		EscapedPath: "/download"}); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("unauthorized HEAD returned %v, want not-found", err)
	}
}

func TestStaticArtifactValidatesManifestAndUsesOnlyListedPaths(t *testing.T) {
	pageBody := "<!doctype html><title>ok</title>"
	page := deliveryFile(deliveryPageID, pageBody, "text/html; charset=utf-8")
	fallback := "/index.html"
	payload := artifactPayload{Format: "static-site-artifact/v1", Entrypoints: map[string]string{"/": "/index.html"},
		Files:    map[string]artifactFile{"/index.html": {FileID: page.ID, ContentType: page.ContentType, SHA256: page.SHA256, Size: page.Size, CacheClass: "document"}},
		Fallback: &fallback}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	envelope := artifactEnvelope{Format: payload.Format, Digest: "sha256:" + hex.EncodeToString(digest[:]),
		Entrypoints: payload.Entrypoints, Files: payload.Files, Fallback: payload.Fallback}
	manifestBody, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	manifestFile := deliveryFile(deliveryManifestID, string(manifestBody), "application/json")
	files := &deliveryFiles{objects: map[string]deliveryObject{
		page.ID:         {file: page, body: pageBody},
		manifestFile.ID: {file: manifestFile, body: string(manifestBody)},
	}, forbidden: map[string]bool{}}
	identity := deliveryIdentity{}
	validator, err := NewCoreTargetValidator(identity, files, "router-service-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{Kind: model.TargetStaticSiteArtifact, FileID: manifestFile.ID, FallbackEnabled: true}
	if err := validator.ValidateRouteTarget(context.Background(), target); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}
	route := newDeliveryRoute(t, target, model.MatchPrefix, "/site")
	delivery, err := NewCoreDelivery("main", "router-service-key", identity, files, deliveryRoutes{route: route}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/site/missing"})
	if err != nil || response.StatusCode != http.StatusOK || response.ContentType != page.ContentType || response.CacheControl != "no-store" {
		t.Fatalf("fallback delivery = %+v, %v", response, err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(data) != pageBody {
		t.Fatalf("fallback bytes = %q, %v", data, err)
	}
	route.Target.FallbackEnabled = false
	delivery.routes = deliveryRoutes{route: route}
	if _, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/site/missing"}); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("disabled fallback returned %v, want not-found", err)
	}
	if _, err := delivery.Deliver(context.Background(), engine.DeliveryRequest{BranchID: "main", Host: "example.com", Method: http.MethodGet, EscapedPath: "/site/../secret"}); err == nil {
		t.Fatal("artifact path traversal was served")
	}
}

func TestArtifactManifestRejectsDuplicateKeysAndBadDigest(t *testing.T) {
	for _, raw := range []string{
		`{"format":"static-site-artifact/v1","format":"static-site-artifact/v1"}`,
		`{"format":"static-site-artifact/v1","files":{"/a":{"file_id":"x","file_id":"y"}}}`,
		`{"format":"static-site-artifact/v1"} {}`,
	} {
		if httpjson.ValidJSON([]byte(raw)) {
			t.Errorf("invalid or duplicate-key JSON accepted: %s", raw)
		}
	}
}

func deliveryFile(id, body, contentType string) core.File {
	digest := sha256.Sum256([]byte(body))
	return core.File{ID: id, ContentType: contentType, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(body)), Revision: 1}
}

func parseDeliveryTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
