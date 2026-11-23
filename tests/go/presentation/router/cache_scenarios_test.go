package router

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	engine "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	cacheScenarioBranch = "main"
	cacheScenarioHost   = "www.example.invalid"
	cacheScenarioPath   = "/site/"
)

type cacheScenarioFile struct {
	file contracts.File
	body string
}

type cacheScenarioFiles struct {
	mu        sync.Mutex
	objects   map[string]cacheScenarioFile
	statErr   error
	statFails map[string]bool
	reads     map[string]int
}

func newCacheScenarioFiles() *cacheScenarioFiles {
	return &cacheScenarioFiles{
		objects:   make(map[string]cacheScenarioFile),
		statFails: make(map[string]bool),
		reads:     make(map[string]int),
	}
}

func (files *cacheScenarioFiles) StatFile(_ context.Context, id string) (contracts.File, error) {
	files.mu.Lock()
	defer files.mu.Unlock()
	if files.statErr != nil || files.statFails[id] {
		if files.statErr != nil {
			return contracts.File{}, files.statErr
		}
		return contracts.File{}, contracts.ErrFileNotFound
	}
	object, ok := files.objects[id]
	if !ok {
		return contracts.File{}, contracts.ErrFileNotFound
	}
	return object.file, nil
}

func (files *cacheScenarioFiles) ReadFile(_ context.Context, id string) (io.ReadCloser, contracts.File, error) {
	files.mu.Lock()
	defer files.mu.Unlock()
	object, ok := files.objects[id]
	if !ok {
		return nil, contracts.File{}, contracts.ErrFileNotFound
	}
	files.reads[id]++
	return io.NopCloser(strings.NewReader(object.body)), object.file, nil
}

func (files *cacheScenarioFiles) readCount(id string) int {
	files.mu.Lock()
	defer files.mu.Unlock()
	return files.reads[id]
}

func cacheScenarioCoreFile(id, body, contentType string, revision int64) contracts.File {
	sum := sha256.Sum256([]byte(body))
	return contracts.File{ID: id, ContentType: contentType, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Revision: revision}
}

// cacheScenarioArtifact writes a self-consistent static-page-artifact/v1 pair
// with a unique asset marker so a test can prove which revision was delivered.
func cacheScenarioArtifact(t *testing.T, files *cacheScenarioFiles, manifestID, documentID, assetID, marker string, revision int64) {
	t.Helper()
	documentBody := "<!doctype html><title>" + marker + "</title>"
	assetBody := "export const marker = " + strconvQuote(marker) + ";"
	document := cacheScenarioCoreFile(documentID, documentBody, "text/html; charset=utf-8", revision)
	asset := cacheScenarioCoreFile(assetID, assetBody, "text/javascript; charset=utf-8", revision)
	payload := map[string]any{
		"assets": map[string]any{"assets/app.js": map[string]any{
			"content_type": asset.ContentType,
			"file_id":      asset.ID,
			"sha256":       asset.SHA256,
			"size":         asset.Size,
			"cache_class":  "immutable",
		}},
		"document": map[string]any{
			"content_type": document.ContentType,
			"file_id":      document.ID,
			"sha256":       document.SHA256,
			"size":         document.Size,
		},
		"format": "static-page-artifact/v1",
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
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestFile := cacheScenarioCoreFile(manifestID, string(data), "application/json", revision)
	files.mu.Lock()
	defer files.mu.Unlock()
	files.objects[manifestID] = cacheScenarioFile{file: manifestFile, body: string(data)}
	files.objects[documentID] = cacheScenarioFile{file: document, body: documentBody}
	files.objects[assetID] = cacheScenarioFile{file: asset, body: assetBody}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func cacheScenarioTarget(manifestID string) model.Target {
	return model.Target{
		Kind:          model.TargetStaticSiteArtifact,
		DefaultLocale: "en-US",
		LocaleTargets: []model.LocaleArtifactTarget{{Locale: "en-US", ArtifactManifestFileID: manifestID, Revision: 1}},
	}
}

func cacheScenarioRoute(target model.Target, revision int64) model.Route {
	return model.Route{
		ID:        "01938f7a-68af-7c20-a51d-78b6ca125399",
		BranchID:  cacheScenarioBranch,
		Host:      cacheScenarioHost,
		Path:      cacheScenarioPath,
		Match:     model.MatchPrefix,
		Target:    target,
		State:     model.StateActive,
		Revision:  revision,
		CreatedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(1, 0).UTC(),
	}
}

type cacheScenarioRoutes struct {
	mu             sync.Mutex
	route          model.Route
	fingerprint    string
	resolveErr     error
	fingerprintErr error
	resolves       int
	validations    int
}

func (routes *cacheScenarioRoutes) ResolveRoute(_ context.Context, _, _ string) (model.Route, error) {
	routes.mu.Lock()
	defer routes.mu.Unlock()
	routes.resolves++
	if routes.resolveErr != nil {
		return model.Route{}, routes.resolveErr
	}
	return routes.route, nil
}

func (routes *cacheScenarioRoutes) ResolveRouteFingerprint(_ context.Context, _, _ string) (string, error) {
	routes.mu.Lock()
	defer routes.mu.Unlock()
	routes.validations++
	if routes.fingerprintErr != nil {
		return "", routes.fingerprintErr
	}
	return routes.fingerprint, nil
}

func (routes *cacheScenarioRoutes) counters() (int, int) {
	routes.mu.Lock()
	defer routes.mu.Unlock()
	return routes.resolves, routes.validations
}

func cacheScenarioDeliveryRequest(path string) engine.DeliveryRequest {
	return engine.DeliveryRequest{
		BranchID: cacheScenarioBranch, Host: cacheScenarioHost, Method: http.MethodGet,
		EscapedPath: path,
	}
}

const cacheScenarioFingerprint = "cache-scenario-route-v1"

func cacheScenarioCaches() RouterCacheConfig {
	return RouterCacheConfig{
		Manifests: CacheSettings{Enabled: true, MaxEntries: 8, MaxBytes: 1 << 20, TTL: time.Minute},
		Routes:    CacheSettings{Enabled: true, MaxEntries: 8, MaxBytes: 1 << 20, TTL: time.Minute},
	}
}

func cacheScenarioBody(t *testing.T, response engine.DeliveryResponse) string {
	t.Helper()
	if response.Body == nil {
		return ""
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type cacheScenarioHandlerAPI struct {
	deliver func(context.Context, engine.DeliveryRequest) (engine.DeliveryResponse, error)
}

func (api cacheScenarioHandlerAPI) Authenticate(context.Context, string) (context.Context, error) {
	return context.Background(), nil
}
func (api cacheScenarioHandlerAPI) AuthenticateSession(context.Context, string) (context.Context, error) {
	return context.Background(), nil
}
func (api cacheScenarioHandlerAPI) ListRoutes(context.Context, engine.ListRoutesRequest) (engine.RoutePage, error) {
	return engine.RoutePage{}, engine.ErrUnavailable
}
func (api cacheScenarioHandlerAPI) GetRoute(context.Context, string) (engine.Route, error) {
	return engine.Route{}, engine.ErrUnavailable
}
func (api cacheScenarioHandlerAPI) CreateRoute(context.Context, engine.CreateRouteRequest) (engine.Route, error) {
	return engine.Route{}, engine.ErrUnavailable
}
func (api cacheScenarioHandlerAPI) ReplaceRouteTarget(context.Context, engine.ReplaceRouteTargetRequest) (engine.Route, error) {
	return engine.Route{}, engine.ErrUnavailable
}
func (api cacheScenarioHandlerAPI) ReplaceLocaleTarget(context.Context, engine.ReplaceLocaleTargetRequest) (engine.Route, error) {
	return engine.Route{}, engine.ErrUnavailable
}
func (api cacheScenarioHandlerAPI) DeleteRoute(context.Context, engine.DeleteRouteRequest) error {
	return engine.ErrUnavailable
}
func (api cacheScenarioHandlerAPI) Deliver(ctx context.Context, request engine.DeliveryRequest) (engine.DeliveryResponse, error) {
	return api.deliver(ctx, request)
}

func TestCacheRouterScenario(t *testing.T) {
	t.Run("CACHE-006", func(t *testing.T) {
		files := newCacheScenarioFiles()
		cacheScenarioArtifact(t, files,
			"01938f7a-68af-7c10-a51d-78b6ca125301", "01938f7a-68af-7c10-a51d-78b6ca125302", "01938f7a-68af-7c10-a51d-78b6ca125304", "v1", 1)
		routes := &cacheScenarioRoutes{route: cacheScenarioRoute(cacheScenarioTarget("01938f7a-68af-7c10-a51d-78b6ca125301"), 1), fingerprint: cacheScenarioFingerprint}
		delivery, err := NewCoreDeliveryWithCache(cacheScenarioBranch, files, routes, nil, cacheScenarioCaches())
		if err != nil {
			t.Fatal(err)
		}
		path := cacheScenarioPath + "assets/app.js"
		first, err := delivery.Deliver(context.Background(), cacheScenarioDeliveryRequest(path))
		if err != nil {
			t.Fatal(err)
		}
		if body := cacheScenarioBody(t, first); !strings.Contains(body, "v1") {
			t.Fatalf("first delivery body = %q", body)
		}
		manifestID := "01938f7a-68af-7c10-a51d-78b6ca125301"
		if reads := files.readCount(manifestID); reads != 1 {
			t.Fatalf("cold manifest reads = %d, want 1", reads)
		}
		second, err := delivery.Deliver(context.Background(), cacheScenarioDeliveryRequest(path))
		if err != nil {
			t.Fatal(err)
		}
		if body := cacheScenarioBody(t, second); !strings.Contains(body, "v1") {
			t.Fatalf("cached delivery body = %q", body)
		}
		if reads := files.readCount(manifestID); reads != 1 {
			t.Fatalf("cached manifest reads = %d, want 1", reads)
		}
		resolveCount, validationCount := routes.counters()
		if resolveCount != 1 || validationCount < 2 {
			t.Fatalf("route resolve/validation counts = %d/%d, want 1/at least 2", resolveCount, validationCount)
		}

		cacheScenarioArtifact(t, files,
			"01938f7a-68af-7c10-a51d-78b6ca125311", "01938f7a-68af-7c10-a51d-78b6ca125312", "01938f7a-68af-7c10-a51d-78b6ca125314", "v2", 2)
		routes.mu.Lock()
		routes.route = cacheScenarioRoute(cacheScenarioTarget("01938f7a-68af-7c10-a51d-78b6ca125311"), 2)
		routes.fingerprint = "cache-scenario-route-v2"
		routes.mu.Unlock()
		third, err := delivery.Deliver(context.Background(), cacheScenarioDeliveryRequest(path))
		if err != nil {
			t.Fatal(err)
		}
		if body := cacheScenarioBody(t, third); !strings.Contains(body, "v2") {
			t.Fatalf("delivery after CAS change = %q, want v2", body)
		}

		routes.mu.Lock()
		routes.fingerprintErr = errors.New("revision source unavailable")
		routes.mu.Unlock()
		if _, err := delivery.Deliver(context.Background(), cacheScenarioDeliveryRequest(path)); err == nil {
			t.Fatal("stale route match was served when revision validation failed")
		}
		routes.mu.Lock()
		routes.fingerprintErr = nil
		routes.mu.Unlock()
		files.mu.Lock()
		files.statErr = contracts.ErrFileForbidden
		files.mu.Unlock()
		if _, err := delivery.Deliver(context.Background(), cacheScenarioDeliveryRequest(path)); err == nil {
			t.Fatal("cached manifest was served when Core metadata check failed")
		}
	})

	t.Run("CACHE-007", func(t *testing.T) {
		files := newCacheScenarioFiles()
		cacheScenarioArtifact(t, files,
			"01938f7a-68af-7c10-a51d-78b6ca125301", "01938f7a-68af-7c10-a51d-78b6ca125302", "01938f7a-68af-7c10-a51d-78b6ca125304", "en", 1)
		cacheScenarioArtifact(t, files,
			"01938f7a-68af-7c10-a51d-78b6ca125305", "01938f7a-68af-7c10-a51d-78b6ca125306", "01938f7a-68af-7c10-a51d-78b6ca125307", "de", 1)
		target := model.Target{
			Kind: model.TargetStaticSiteArtifact, DefaultLocale: "en-US",
			LocaleTargets: []model.LocaleArtifactTarget{
				{Locale: "en-US", ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125301", Revision: 1},
				{Locale: "de-DE", ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125305", Revision: 1},
			},
		}
		routes := &cacheScenarioRoutes{route: cacheScenarioRoute(target, 1), fingerprint: cacheScenarioFingerprint}
		delivery, err := NewCoreDelivery(cacheScenarioBranch, files, routes, nil)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := NewHandler(cacheScenarioHandlerAPI{deliver: func(ctx context.Context, request engine.DeliveryRequest) (engine.DeliveryResponse, error) {
			return delivery.Deliver(ctx, request)
		}}, Config{BranchID: cacheScenarioBranch, MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		path := cacheScenarioPath + "assets/app.js"
		cookieRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+path, nil)
		cookieRequest.AddCookie(&http.Cookie{Name: "zubrium_locale", Value: "de-DE"})
		cookieResponse := httptest.NewRecorder()
		handler.PublicHTTPHandler().ServeHTTP(cookieResponse, cookieRequest)
		if cookieResponse.Code != http.StatusOK {
			t.Fatalf("cookie locale status = %d", cookieResponse.Code)
		}
		if got := cookieResponse.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("cookie locale Cache-Control = %q, want no-store", got)
		}
		if got := cookieResponse.Header().Values("Vary"); len(got) != 0 || strings.Contains(strings.Join(got, ","), "Cookie") {
			t.Fatalf("cookie locale Vary = %q, want none", got)
		}
		if !strings.Contains(cookieResponse.Body.String(), "de") {
			t.Fatalf("cookie locale body = %q", cookieResponse.Body.String())
		}

		languageRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+path, nil)
		languageRequest.Header.Set("Accept-Language", "de-DE")
		languageResponse := httptest.NewRecorder()
		handler.PublicHTTPHandler().ServeHTTP(languageResponse, languageRequest)
		if languageResponse.Code != http.StatusOK {
			t.Fatalf("Accept-Language status = %d", languageResponse.Code)
		}
		if got := languageResponse.Header().Get("Vary"); got != "Accept-Language" {
			t.Fatalf("Accept-Language Vary = %q", got)
		}
		if got := languageResponse.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("Accept-Language Cache-Control = %q", got)
		}
		if !strings.Contains(languageResponse.Body.String(), "de") {
			t.Fatalf("Accept-Language body = %q", languageResponse.Body.String())
		}

		arbitraryRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+path, nil)
		arbitraryRequest.AddCookie(&http.Cookie{Name: "tracking", Value: "arbitrary"})
		arbitraryResponse := httptest.NewRecorder()
		handler.PublicHTTPHandler().ServeHTTP(arbitraryResponse, arbitraryRequest)
		if got := strings.Join(arbitraryResponse.Header().Values("Vary"), ","); strings.Contains(got, "Cookie") {
			t.Fatalf("arbitrary cookie produced Vary: %q", got)
		}
		if !strings.Contains(arbitraryResponse.Body.String(), "en") {
			t.Fatalf("arbitrary cookie body = %q", arbitraryResponse.Body.String())
		}
	})

	t.Run("CACHE-008", func(t *testing.T) {
		body := strings.Repeat("public representation ", 24)
		etag := `"sha256-` + strings.Repeat("a", 64) + `"`
		forbiddenControl := false
		newAPI := func() cacheScenarioHandlerAPI {
			return cacheScenarioHandlerAPI{deliver: func(_ context.Context, request engine.DeliveryRequest) (engine.DeliveryResponse, error) {
				if request.IfNoneMatch == etag {
					return engine.DeliveryResponse{StatusCode: http.StatusNotModified, ContentLength: -1, ETag: etag, CacheControl: "no-store"}, nil
				}
				if forbiddenControl {
					return engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: "text/html; charset=utf-8", ContentLength: int64(len(body)), ETag: etag, CacheControl: "private", Body: io.NopCloser(strings.NewReader(body))}, nil
				}
				return engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: "text/html; charset=utf-8", ContentLength: int64(len(body)), ETag: etag, CacheControl: "no-store", Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
		}
		handler, err := NewHandler(newAPI(), Config{
			BranchID: cacheScenarioBranch, MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10,
			Compression: CompressionConfig{Enabled: true, Algorithms: []string{"gzip"}, MinBytes: 16, MaxUncompressedBytes: 1 << 20, MaxCompressedBytes: 1 << 20, MediaTypes: []string{"text/html"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		gzipRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+"/page", nil)
		gzipRequest.Header.Set("Accept-Encoding", "gzip")
		gzipResponse := httptest.NewRecorder()
		handler.PublicHTTPHandler().ServeHTTP(gzipResponse, gzipRequest)
		if gzipResponse.Code != http.StatusOK || gzipResponse.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("gzip response status/encoding = %d/%q", gzipResponse.Code, gzipResponse.Header().Get("Content-Encoding"))
		}
		if got := gzipResponse.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Fatalf("gzip Vary = %q", got)
		}
		if gzipResponse.Header().Get("ETag") != etag || gzipResponse.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("gzip validators changed: %#v", gzipResponse.Header())
		}
		if got := gzipResponse.Header().Get("Content-Length"); got != strconv.Itoa(gzipResponse.Body.Len()) {
			t.Fatalf("encoded Content-Length = %q, want %d", got, gzipResponse.Body.Len())
		}
		reader, err := gzip.NewReader(bytes.NewReader(gzipResponse.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded) != body {
			t.Fatalf("gzip round trip mismatch")
		}

		rangeRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+"/page", nil)
		rangeRequest.Header.Set("Accept-Encoding", "gzip")
		rangeRequest.Header.Set("Range", "bytes=0-15")
		rangeResponse := httptest.NewRecorder()
		handler.PublicHTTPHandler().ServeHTTP(rangeResponse, rangeRequest)
		if got := rangeResponse.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("range response was compressed: %q", got)
		}
		if got := rangeResponse.Header().Get("Vary"); got != "" {
			t.Fatalf("range response Vary = %q", got)
		}
		if rangeResponse.Body.String() != body {
			t.Fatalf("range response body was transformed")
		}

		conditionalRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+"/page", nil)
		conditionalRequest.Header.Set("If-None-Match", etag)
		conditionalRequest.Header.Set("Accept-Encoding", "gzip")
		conditionalResponse := httptest.NewRecorder()
		handler.PublicHTTPHandler().ServeHTTP(conditionalResponse, conditionalRequest)
		if conditionalResponse.Code != http.StatusNotModified || conditionalResponse.Body.Len() != 0 {
			t.Fatalf("conditional response status/body = %d/%d", conditionalResponse.Code, conditionalResponse.Body.Len())
		}
		if conditionalResponse.Header().Get("ETag") != etag || conditionalResponse.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("conditional response lost validators: %#v", conditionalResponse.Header())
		}

		for _, forbidden := range []string{"no-cache", "must-revalidate", "max-age=0", "private", "public, max-age=0", "public, max-age=30, stale-while-revalidate=60"} {
			if validCacheControl(forbidden) {
				t.Errorf("Cache-Control allowlist accepted %q", forbidden)
			}
		}
		if !validCacheControl("public, max-age=300") || !validCacheControl("public, max-age=31536000, immutable") {
			t.Fatal("Cache-Control allowlist rejected a permitted value")
		}

		forbiddenControl = true
		probeHandler, err := NewHandler(newAPI(), Config{BranchID: cacheScenarioBranch, MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		probeRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+"/page", nil)
		probeResponse := httptest.NewRecorder()
		probeHandler.PublicHTTPHandler().ServeHTTP(probeResponse, probeRequest)
		forbiddenControl = false
		if probeResponse.Code != http.StatusBadGateway {
			t.Fatalf("forbidden Cache-Control status = %d, want 502", probeResponse.Code)
		}

		if _, err := NewHandler(newAPI(), Config{
			BranchID: cacheScenarioBranch, MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10,
			Compression: CompressionConfig{Enabled: true, Algorithms: []string{"gzip"}, MaxUncompressedBytes: 1 << 20, MaxCompressedBytes: 1 << 20, MediaTypes: []string{"text/html"}},
		}); err == nil {
			t.Fatal("compression configuration without MinBytes was accepted")
		}
		if _, err := NewHandler(newAPI(), Config{
			BranchID: cacheScenarioBranch, MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10,
			Compression: CompressionConfig{Enabled: true, Algorithms: []string{"gzip"}, MinBytes: 16, MaxCompressedBytes: 1 << 20, MediaTypes: []string{"text/html"}},
		}); err == nil {
			t.Fatal("compression configuration without MaxUncompressedBytes was accepted")
		}

		// When the compressed representation exceeds the explicit cap the full
		// uncompressed body must still be delivered, not a consumed/truncated one.
		fallbackHandler, err := NewHandler(newAPI(), Config{
			BranchID: cacheScenarioBranch, MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10,
			Compression: CompressionConfig{Enabled: true, Algorithms: []string{"gzip"}, MinBytes: 16, MaxUncompressedBytes: 1 << 20, MaxCompressedBytes: 1, MediaTypes: []string{"text/html"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		fallbackRequest := httptest.NewRequest(http.MethodGet, "http://"+cacheScenarioHost+"/page", nil)
		fallbackRequest.Header.Set("Accept-Encoding", "gzip")
		fallbackResponse := httptest.NewRecorder()
		fallbackHandler.PublicHTTPHandler().ServeHTTP(fallbackResponse, fallbackRequest)
		if fallbackResponse.Code != http.StatusOK || fallbackResponse.Header().Get("Content-Encoding") != "" {
			t.Fatalf("fallback status/encoding = %d/%q", fallbackResponse.Code, fallbackResponse.Header().Get("Content-Encoding"))
		}
		if got := fallbackResponse.Header().Get("Vary"); got != "" {
			t.Fatalf("fallback Vary = %q", got)
		}
		if fallbackResponse.Body.String() != body {
			t.Fatal("fallback body was truncated after compression attempt")
		}
	})

	t.Run("CACHE-009", func(t *testing.T) {
		if os.Getenv("CACHE_ROUTER_TEST_DISPOSABLE") != "1" || os.Getenv("CACHE_ROUTER_TEST_PROXY_BASE_URL") == "" ||
			os.Getenv("CACHE_ROUTER_TEST_NO_STORE_PATH") == "" || os.Getenv("CACHE_ROUTER_TEST_SSE_PATH") == "" ||
			os.Getenv("CACHE_ROUTER_TEST_FORM_PATH") == "" || os.Getenv("CACHE_ROUTER_TEST_ADMIN_PATH") == "" ||
			os.Getenv("CACHE_ROUTER_TEST_TIMEOUT") == "" {
			t.Skip("disposable caching proxy is not configured; CACHE-009 remains blocked")
		}
		timeout, err := time.ParseDuration(os.Getenv("CACHE_ROUTER_TEST_TIMEOUT"))
		if err != nil || timeout <= 0 {
			t.Fatalf("invalid CACHE_ROUTER_TEST_TIMEOUT: %q", os.Getenv("CACHE_ROUTER_TEST_TIMEOUT"))
		}
		base := strings.TrimSuffix(os.Getenv("CACHE_ROUTER_TEST_PROXY_BASE_URL"), "/")
		client := &http.Client{Timeout: timeout}
		for _, path := range []string{
			os.Getenv("CACHE_ROUTER_TEST_NO_STORE_PATH"), os.Getenv("CACHE_ROUTER_TEST_SSE_PATH"),
			os.Getenv("CACHE_ROUTER_TEST_FORM_PATH"), os.Getenv("CACHE_ROUTER_TEST_ADMIN_PATH"),
		} {
			request, err := http.NewRequest(http.MethodGet, base+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("proxy request %s failed: %v", path, err)
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if got := response.Header.Get("Cache-Control"); got != "no-store" {
				t.Errorf("proxy response %s Cache-Control = %q, want no-store", path, got)
			}
		}
	})

	t.Run("CACHE-012", func(t *testing.T) {
		root := filepath.Join("..", "..")
		for _, module := range []string{"presentation/router", "engines/router"} {
			err := filepath.WalkDir(filepath.Join(root, module), func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if strings.Contains(string(data), "ROUTE_DIAG") {
					t.Errorf("%s still contains ROUTE_DIAG diagnostics", relative)
				}
				for _, line := range strings.Split(string(data), "\n") {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "println(") || strings.HasPrefix(trimmed, "print(") {
						t.Errorf("%s still contains ad-hoc diagnostics: %s", relative, trimmed)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	})
}

var _ engine.DeliveryAPI = cacheScenarioHandlerAPI{}
