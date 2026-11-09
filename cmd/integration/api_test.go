package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"websiteengine/features/auth"
	"websiteengine/features/features"
	"websiteengine/features/forms"
	"websiteengine/features/media"
	"websiteengine/features/overview"
	"websiteengine/features/settings"
	"websiteengine/features/sitebuilder"
	"websiteengine/migrations"
	"websiteengine/pkg/config"
	"websiteengine/pkg/environment"
	"websiteengine/pkg/postgres"
	publicruntime "websiteengine/pkg/runtime"
	"websiteengine/pkg/storage"
	httptransport "websiteengine/presentation/http"
	hauth "websiteengine/presentation/http/handlers/auth"
	hfeatures "websiteengine/presentation/http/handlers/features"
	hforms "websiteengine/presentation/http/handlers/forms"
	hmedia "websiteengine/presentation/http/handlers/media"
	hoverview "websiteengine/presentation/http/handlers/overview"
	hsettings "websiteengine/presentation/http/handlers/settings"
	hsitebuilder "websiteengine/presentation/http/handlers/sitebuilder"
)

const testAdminToken = "test-admin-token-at-least-24-characters"

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

type fakeStore struct {
	objects       map[string][]byte
	types         map[string]string
	cacheControls map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string][]byte{}, types: map[string]string{}, cacheControls: map[string]string{}}
}

func (f *fakeStore) Put(_ context.Context, key string, body io.Reader, _ int64, opts storage.PutOptions) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.objects[key] = data
	f.types[key] = opts.ContentType
	f.cacheControls[key] = opts.CacheControl
	return nil
}

func (f *fakeStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeStore) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func (f *fakeStore) PublicURL(key string) (string, error) {
	return "https://cdn.example.test/" + key, nil
}

func (f *fakeStore) Ready(context.Context) error { return nil }

type testEnv struct {
	t           *testing.T
	pool        *pgxpool.Pool
	server      *httptest.Server
	devServer   *httptest.Server
	prodServer  *httptest.Server
	adminToken  string
	devBranch   environment.Environment
	prodBranch  environment.Environment
	store       *fakeStore
	objectStore storage.ObjectStore
	media       *media.Service
	processor   *integrationRenditionProcessor
	forms       *forms.Service
	snapshots   *sitebuilder.SnapshotService
	routerDeps  httptransport.Dependencies
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWithObjectStore(t, nil)
}

// newTestEnvWithObjectStore runs the ordinary disposable integration setup
// against an injected storage implementation. The fake remains available on
// testEnv.store for existing unit-style assertions; end-to-end callers should
// use objectStore so the same flow can be exercised against MinIO.
func newTestEnvWithObjectStore(t *testing.T, objectStore storage.ObjectStore) *testEnv {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	requireDisposableIntegrationDatabase(t, dbURL)
	ctx := context.Background()
	pool, err := postgres.Open(ctx, dbURL, integrationPoolSettings())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(ctx, pool, migrations.FS, time.Minute); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE snapshot_model_content_publications, site_snapshot_model_workspaces,
		site_snapshot_events, site_snapshots, feature_builds, media_renditions, media_objects,
		file_upload_parts, file_upload_sessions, file_object_deletions, form_submissions,
		feature_versions, features, access_tokens,
		site_environments, site_settings
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// The CASCADE truncate drops the seeded environment rows.
	if err := postgres.SeedDefaults(ctx, pool); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authRepo := auth.NewRepository(pool)
	authService := auth.NewService(authRepo)
	if err := authService.SetListLimit(50); err != nil {
		t.Fatalf("set token list limit: %v", err)
	}
	if created, err := authService.Bootstrap(ctx, testAdminToken); err != nil || !created {
		t.Fatalf("bootstrap admin: created=%v err=%v", created, err)
	}
	featureRepo := features.NewRepository(pool)
	featureService := features.NewService(featureRepo)
	formsRepo := forms.NewRepository(pool)
	formsService := forms.NewService(formsRepo, featureRepo)
	if err := formsService.SetLimits(50, 10000); err != nil {
		t.Fatalf("set submission limits: %v", err)
	}
	fake := newFakeStore()
	if objectStore == nil {
		objectStore = fake
	}
	buildService := configuredBuildService(t, pool, objectStore, 8<<20)
	snapshotService := sitebuilder.NewSnapshotService(pool, buildService)
	if err := snapshotService.SetDesignPreviewTTL(20 * time.Minute); err != nil {
		t.Fatalf("set design preview TTL: %v", err)
	}
	// Lighthouse opt-in uses a crawlable disposable branch with explicit
	// fixture metadata. Ordinary integration runs keep both branches private.
	indexable := os.Getenv("LIGHTHOUSE_BIN") != "" || os.Getenv("VITASIL_BROWSER_INDEXABLE") == "1"
	branches, err := snapshotService.RegisterBranches(ctx, []config.BranchConfig{
		{Name: "Integration Development", HTTPAddr: ":18081", CORSOrigins: []string{"https://site.example"}, Indexable: &indexable},
		{Name: "Integration Production", HTTPAddr: ":18082", CORSOrigins: []string{"https://site.example"}, Indexable: &indexable},
	})
	if err != nil || len(branches) != 2 {
		t.Fatalf("register integration branches: branches=%v err=%v", branches, err)
	}
	devBranch := environment.Environment(branches[0].ID.String())
	prodBranch := environment.Environment(branches[1].ID.String())
	formsService.SetSnapshotSchemaProvider(snapshotService)
	mediaRepo := media.NewRepository(pool)
	processor := &integrationRenditionProcessor{}
	mediaService := media.NewServiceWithProcessor(mediaRepo, objectStore, 20<<20, processor)
	configureMediaRenditionWorker(t, mediaService)
	snapshotService.SetMediaService(mediaService)
	mediaHandler := hmedia.NewHandler(mediaService, log, 20<<20)
	if err := mediaHandler.SetRedirectCacheTTL(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := mediaHandler.SetPagination(20, 50); err != nil {
		t.Fatalf("set media pagination: %v", err)
	}
	mediaHandler.SetProfileProvider(snapshotService)
	featureHandler := hfeatures.NewHandler(featureService, log)
	if err := featureHandler.SetPagination(20, 50); err != nil {
		t.Fatalf("set feature pagination: %v", err)
	}
	formsHandler := hforms.NewHandler(formsService, log, 1<<20)
	if err := formsHandler.SetLimits(20, 50); err != nil {
		t.Fatalf("set submission pagination: %v", err)
	}
	snapshotHandler := hsitebuilder.NewSnapshotsHandler(snapshotService, formsService, log, 1<<20)
	if err := snapshotHandler.SetPagination(20, 50); err != nil {
		t.Fatalf("set snapshot pagination: %v", err)
	}

	settingsService := settings.NewService(settings.Config{
		MaxJSONBytes:         1 << 20,
		MaxUploadBytes:       20 << 20,
		AdminPageSize:        50,
		PublicPageSize:       20,
		AcceptedContentTypes: media.AcceptedContentTypes(),
		ImageVariants:        media.ImageVariantNames(),
	})

	deps := httptransport.Dependencies{
		BranchIndexable: map[environment.Environment]bool{devBranch: indexable, prodBranch: indexable},
		PublicCORSByBranch: map[environment.Environment][]string{
			devBranch: {"https://site.example"}, prodBranch: {"https://site.example"},
		},
		Log: log, AuthService: authService,
		Auth:                  hauth.NewHandler(authService, log, 1<<20),
		Features:              featureHandler,
		Forms:                 formsHandler,
		Media:                 mediaHandler,
		Overview:              hoverview.NewHandler(overview.NewRepository(pool), log),
		Settings:              hsettings.NewHandler(settingsService),
		FeatureBuilds:         hsitebuilder.NewBuildsHandler(buildService, log, 8<<20),
		SiteSnapshots:         snapshotHandler,
		Sitepacks:             hsitebuilder.NewSitepackHandler(snapshotService, sitebuilder.SitepackOptions{TempDir: t.TempDir(), MaxBytes: 16 << 20, MaxExpandedBytes: 32 << 20, MaxEntries: 100, MaxIndexBytes: 1 << 20, MaxSVGBytes: 1 << 20, MaxBrowserBlobBytes: 1 << 20}, log),
		Health:                func(context.Context) error { return nil },
		MaxJSONBytes:          1 << 20,
		FormRateLimit:         10000,
		FormRateWindow:        time.Minute,
		FormRateMaxClients:    10000,
		FormRateSweepInterval: time.Minute,
		ReadinessTimeout:      3 * time.Second,
		RuntimeAssetCacheTTL:  time.Hour,
	}

	server := httptest.NewServer(httptransport.NewRouter(deps))
	t.Cleanup(server.Close)
	devServer := newPublicIntegrationServer(t, deps, devBranch, log)
	prodServer := newPublicIntegrationServer(t, deps, prodBranch, log)
	return &testEnv{t: t, pool: pool, server: server, devServer: devServer, prodServer: prodServer,
		adminToken: testAdminToken, devBranch: devBranch, prodBranch: prodBranch, store: fake, objectStore: objectStore,
		media: mediaService, processor: processor, forms: formsService, snapshots: snapshotService, routerDeps: deps}
}

func newPublicIntegrationServer(t *testing.T, deps httptransport.Dependencies, branch environment.Environment, log *slog.Logger) *httptest.Server {
	t.Helper()
	prerenderEnabled := strings.TrimSpace(os.Getenv("LIGHTHOUSE_BIN")) != "" && os.Getenv("LIGHTHOUSE_PRERENDER") != "0"
	if !prerenderEnabled {
		server := httptest.NewServer(httptransport.NewPublicRouter(deps, branch))
		t.Cleanup(server.Close)
		return server
	}

	nodePath := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	playwrightModule := strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browserPath := strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	if nodePath == "" || playwrightModule == "" || browserPath == "" {
		t.Fatal("LIGHTHOUSE_BIN requires PRERENDER_TEST_NODE, PRERENDER_TEST_MODULE, and PRERENDER_TEST_BROWSER for a prerendered public benchmark")
	}
	server := httptest.NewUnstartedServer(nil)
	baseURL := "http://" + server.Listener.Addr().String()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration package path for prerender worker")
	}
	workerPath, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), "../../scripts/prerender-worker.mjs"))
	if err != nil {
		t.Fatalf("resolve prerender worker path: %v", err)
	}
	prerenderer, err := publicruntime.NewPrerenderer(baseURL, publicruntime.PrerenderOptions{
		NodePath: nodePath, WorkerPath: workerPath, PlaywrightModule: playwrightModule, BrowserPath: browserPath,
		Timeout: 20 * time.Second, AttemptTimeout: 10 * time.Second, MaxAttempts: 2, QuietInterval: 200 * time.Millisecond,
		CacheTTL: 10 * time.Minute, Concurrency: 2, QueueSize: 8, CacheEntries: 64, MaxHTMLBytes: 4 << 20, HeapMB: 128,
		ViewportWidth: 1440, ViewportHeight: 1000, MaxRequests: 200, MaxNetworkBytes: 64 << 20,
	})
	if err != nil {
		t.Fatalf("create disposable Lighthouse prerenderer: %v", err)
	}
	prerenderer.SetLogger(log)
	if deps.Prerender == nil {
		deps.Prerender = make(map[environment.Environment]*publicruntime.Prerenderer)
	}
	deps.Prerender[branch] = prerenderer
	server.Config.Handler = httptransport.NewPublicRouter(deps, branch)
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func integrationPoolSettings() postgres.PoolSettings {
	return postgres.PoolSettings{
		MaxConns:          20,
		MinConns:          1,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   15 * time.Minute,
		HealthCheckPeriod: 30 * time.Second,
		ConnectTimeout:    10 * time.Second,
	}
}

// requireDisposableIntegrationDatabase is the final safety boundary for every
// test that calls newTestEnv: that helper runs migrations and truncates the
// selected database. Each integration run must use a uniquely named local QA
// database; a different local QA namespace means the test belongs to another
// isolated acceptance run and should be skipped.
func requireDisposableIntegrationDatabase(t *testing.T, rawURL string) {
	t.Helper()
	parsed, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("invalid TEST_DATABASE_URL: %v", err)
	}
	host := parsed.Host
	parsedIP := net.ParseIP(host)
	if host != "localhost" && (parsedIP == nil || !parsedIP.IsLoopback()) {
		t.Fatalf("integration tests require a loopback disposable database, got host %q", host)
	}
	if !strings.HasPrefix(parsed.Database, "website_engine_") || !strings.Contains(parsed.Database, "_qa_") || strings.HasSuffix(parsed.Database, "_qa_") {
		t.Skipf("newTestEnv requires a uniquely named local website_engine_*_qa_* disposable database; refusing to migrate or truncate %q", parsed.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, rawURL)
	if err != nil {
		t.Fatalf("connect to disposable integration database: %v", err)
	}
	defer conn.Close(context.Background())
	var currentDatabase string
	var otherSessions int
	if err := conn.QueryRow(ctx, `SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&currentDatabase, &otherSessions); err != nil {
		t.Fatalf("verify integration database identity and active sessions: %v", err)
	}
	if currentDatabase != parsed.Database {
		t.Fatalf("TEST_DATABASE_URL names database %q but server connected to %q", parsed.Database, currentDatabase)
	}
	if otherSessions != 0 {
		t.Fatalf("refusing integration database reset: disposable database %q already has %d other active sessions", currentDatabase, otherSessions)
	}
	t.Logf("verified unique local disposable integration database %s has no other sessions before migration/reset", currentDatabase)
}

func configureMediaRenditionWorker(t *testing.T, service *media.Service) {
	t.Helper()
	if err := service.SetRenditionWorkerConfig(20*time.Minute, time.Second); err != nil {
		t.Fatalf("set rendition worker configuration: %v", err)
	}
}

func configuredBuildService(t *testing.T, pool *pgxpool.Pool, store storage.ObjectStore, maxArchiveBytes int64) *sitebuilder.BuildService {
	t.Helper()
	service := sitebuilder.NewBuildService(pool, store, maxArchiveBytes)
	if err := service.SetRegistrationTimeout(30 * time.Second); err != nil {
		t.Fatalf("set feature build registration timeout: %v", err)
	}
	return service
}

func (e *testEnv) request(method, path, token string, body any) (int, map[string]any, http.Header) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, resp.Header
}

func (e *testEnv) rawRequest(method, path, token string, body io.Reader, contentType string) (int, map[string]any, http.Header) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, resp.Header
}

func TestTokenPolicyAndModeratorScope(t *testing.T) {
	env := newTestEnv(t)

	status, mod, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken,
		map[string]any{"name": "moderator", "role": "moderator"})
	if status != http.StatusCreated {
		t.Fatalf("create moderator: %d %v", status, mod)
	}
	modToken := mod["token"].(string)
	adminTokenID := ""

	status, list, _ := env.request(http.MethodGet, "/api/v1/admin/tokens", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("admin token list: %d %v", status, list)
	}
	for _, item := range list["items"].([]any) {
		token := item.(map[string]any)
		if token["role"] == "admin" {
			adminTokenID = token["id"].(string)
		}
	}

	status, body, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", modToken,
		map[string]any{"name": "escalation", "role": "admin"})
	if status != http.StatusForbidden {
		t.Fatalf("moderator escalation must be forbidden: %d %v", status, body)
	}
	status, body, _ = env.request(http.MethodPost, "/api/v1/admin/tokens", modToken,
		map[string]any{"name": "integration", "role": "user"})
	if status != http.StatusCreated {
		t.Fatalf("moderator user token: %d %v", status, body)
	}
	userToken := body["token"].(string)
	userTokenID := body["id"].(string)

	status, body, _ = env.request(http.MethodGet, "/api/v1/admin/tokens/"+adminTokenID, modToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("moderator must not see admin token: %d %v", status, body)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/me", userToken, nil)
	if status != http.StatusOK {
		t.Fatalf("user /me: %d", status)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/features", userToken, nil)
	if status != http.StatusOK {
		t.Fatalf("user feature list: %d", status)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/tokens", userToken, nil)
	if status != http.StatusForbidden {
		t.Fatalf("user token management: %d", status)
	}
	status, _, _ = env.request(http.MethodDelete, "/api/v1/admin/tokens/"+userTokenID, modToken, nil)
	if status != http.StatusNoContent {
		t.Fatalf("moderator revoke own child: %d", status)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/me", userToken, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: %d", status)
	}
}

func TestOverview(t *testing.T) {
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	status, overview, _ := env.request(http.MethodGet, "/api/v1/admin/overview", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("overview: %d %v", status, overview)
	}
	counts := overview["counts"].(map[string]any)
	if counts["draft_model_snapshots"].(float64) != 0 || counts["draft_model_records"].(float64) != 0 || counts["new_submissions"].(float64) != 0 {
		t.Fatalf("unexpected counts: %v", counts)
	}
	attention := overview["attention"].(map[string]any)
	if len(attention["draft_model_snapshots"].([]any)) != 0 || len(attention["new_submissions"].([]any)) != 0 {
		t.Fatalf("unexpected attention: %v", attention)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/overview", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous overview: %d", status)
	}
}

func TestPublicCORSIsOptIn(t *testing.T) {
	env := newTestEnv(t)
	req, _ := http.NewRequest(http.MethodOptions, env.devServer.URL+"/api/v2/runtime", nil)
	req.Header.Set("Origin", "https://site.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "https://site.example" {
		t.Fatalf("allowed preflight: %d %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}

	req, _ = http.NewRequest(http.MethodOptions, env.devServer.URL+"/api/v2/runtime", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied preflight: %d", resp.StatusCode)
	}

	// System fonts and the shared logo are served by the engine origin. Their
	// handler must preserve the same allow-list instead of adding wildcard CORS.
	assetURL := env.devServer.URL + "/api/v2/system-assets/" + uuid.NewString()
	for _, test := range []struct {
		origin string
		want   string
	}{
		{origin: "https://site.example", want: "https://site.example"},
		{origin: "https://evil.example", want: ""},
	} {
		req, _ := http.NewRequest(http.MethodGet, assetURL, nil)
		req.Header.Set("Origin", test.origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Access-Control-Allow-Origin") != test.want {
			t.Fatalf("system asset CORS for %s: status=%d allow-origin=%q", test.origin, resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestMediaUploadRedirectAndDelete(t *testing.T) {
	env := newTestEnv(t)
	imageData := minimalPNG(t)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "cover.png")
	_, _ = part.Write(imageData)
	_ = writer.Close()
	status, body, _ := env.rawRequest(http.MethodPost, "/api/v1/admin/media", env.adminToken, &buf, writer.FormDataContentType())
	if status != http.StatusCreated {
		t.Fatalf("upload media: %d %v", status, body)
	}
	mediaID := body["id"].(string)
	if body["status"] != "ready" {
		t.Fatalf("media not ready: %v", body)
	}
	variants := body["variants"].(map[string]any)
	if len(variants) != 1 || variants["original"] == nil {
		t.Fatalf("new upload should expose only the original before field-specific processing: %v", variants)
	}
	if len(env.store.objects) != 1 {
		t.Fatalf("new upload should store only the original, got %d objects", len(env.store.objects))
	}

	// Existing media records can retain the old shared variants until their
	// ABI 1 snapshots are retired. Simulate one such record and verify that its
	// legacy URL keeps resolving even though fresh uploads no longer create it.
	legacyVariantKey := "media/" + mediaID + "/legacy/thumbnail.webp"
	if err := env.store.Put(context.Background(), legacyVariantKey, bytes.NewReader([]byte("legacy-thumbnail")), 16, storage.PutOptions{ContentType: "image/webp"}); err != nil {
		t.Fatal(err)
	}
	legacyVariants, _ := json.Marshal(map[string]string{
		"original":  variants["original"].(string),
		"thumbnail": legacyVariantKey,
	})
	if _, err := env.pool.Exec(context.Background(), `UPDATE media_objects SET variants=$2::jsonb WHERE id=$1`, mediaID, legacyVariants); err != nil {
		t.Fatal(err)
	}
	status, listBody, _ := env.request(http.MethodGet, "/api/v1/admin/media?q=cover&type=image", env.adminToken, nil)
	if status != http.StatusOK || len(listBody["items"].([]any)) != 1 {
		t.Fatalf("media search: %d %v", status, listBody)
	}

	// Sorting and the optional date/size filters are applied on the server.
	status, sortedBody, _ := env.request(http.MethodGet, "/api/v1/admin/media?sort=size_desc&min_size=1", env.adminToken, nil)
	if status != http.StatusOK || len(sortedBody["items"].([]any)) != 1 {
		t.Fatalf("media size filter: %d %v", status, sortedBody)
	}
	status, hugeBody, _ := env.request(http.MethodGet, "/api/v1/admin/media?min_size=1000000", env.adminToken, nil)
	if status != http.StatusOK || len(hugeBody["items"].([]any)) != 0 {
		t.Fatalf("media min_size filter: %d %v", status, hugeBody)
	}
	status, emptyBody, _ := env.request(http.MethodGet, "/api/v1/admin/media?created_from=2999-01-01", env.adminToken, nil)
	if status != http.StatusOK || len(emptyBody["items"].([]any)) != 0 {
		t.Fatalf("media created_from filter: %d %v", status, emptyBody)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/media?created_from=not-a-date", env.adminToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid media filter must be rejected: %d", status)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/media?sort=unknown", env.adminToken, nil)
	if status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
		t.Fatalf("invalid media sort must be rejected: %d", status)
	}

	// Renaming changes only the display name.
	status, renameBody, _ := env.request(http.MethodPatch, "/api/v1/admin/media/"+mediaID, env.adminToken,
		map[string]any{"filename": "renamed-cover.png"})
	if status != http.StatusOK || renameBody["original_filename"] != "renamed-cover.png" {
		t.Fatalf("rename media: %d %v", status, renameBody)
	}
	status, renamedList, _ := env.request(http.MethodGet, "/api/v1/admin/media?q=renamed", env.adminToken, nil)
	if status != http.StatusOK || len(renamedList["items"].([]any)) != 1 {
		t.Fatalf("media search after rename: %d %v", status, renamedList)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/media/"+mediaID+"/usage", env.adminToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("retired per-media usage route should not be exposed; file references use the v2 files API: %d", status)
	}

	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/media/"+mediaID, nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.HasSuffix(resp.Header.Get("Location"), "/original.png") {
		t.Fatalf("original redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	req, _ = http.NewRequest(http.MethodGet, env.server.URL+"/media/"+mediaID+"?variant=thumbnail", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "https://cdn.example.test/") {
		t.Fatalf("redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/media", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous media list: %d", status)
	}
	status, _, _ = env.request(http.MethodPost, "/api/v1/admin/media/"+mediaID+"/retry", env.adminToken, nil)
	if status != http.StatusConflict {
		t.Fatalf("retry ready media: %d", status)
	}
	status, _, _ = env.request(http.MethodDelete, "/api/v1/admin/media/"+mediaID, env.adminToken, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete media: %d", status)
	}
	status, _, _ = env.request(http.MethodGet, "/media/"+mediaID, "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("deleted media redirect: %d", status)
	}
}

// uploadMedia sends one file through the admin media endpoint and returns the
// created object.
func (e *testEnv) uploadMedia(name string, data []byte) map[string]any {
	e.t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = writer.Close()
	status, body, _ := e.rawRequest(http.MethodPost, "/api/v1/admin/media", e.adminToken, &buf, writer.FormDataContentType())
	if status != http.StatusCreated {
		e.t.Fatalf("upload %s: %d %v", name, status, body)
	}
	return body
}

// TestMediaListSortingAndPagination covers the keyset cursor for sort orders
// that are not based on created_at.
func TestMediaListSortingAndPagination(t *testing.T) {
	env := newTestEnv(t)
	for _, name := range []string{"b.png", "a.png", "c.png"} {
		env.uploadMedia(name, minimalPNG(t))
	}

	status, first, _ := env.request(http.MethodGet, "/api/v1/admin/media?sort=name&limit=2", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("media first page: %d %v", status, first)
	}
	items := first["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items on the first page, got %d", len(items))
	}
	if items[0].(map[string]any)["original_filename"] != "a.png" ||
		items[1].(map[string]any)["original_filename"] != "b.png" {
		t.Fatalf("unexpected name order: %v", items)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("missing next cursor: %v", first)
	}

	status, second, _ := env.request(http.MethodGet, "/api/v1/admin/media?sort=name&limit=2&cursor="+cursor, env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("media second page: %d %v", status, second)
	}
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["original_filename"] != "c.png" {
		t.Fatalf("unexpected second page: %v", items)
	}
}

// minimalPNG returns a valid 2x2 PNG.
func minimalPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 1, color.RGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
