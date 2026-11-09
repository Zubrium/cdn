package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var validLoadConfig = map[string]string{
	"HTTP_ADDR":                          ":8080",
	"HTTP_MAX_HEADER_BYTES":              "1048576",
	"DATABASE_URL":                       "postgres://engine:test@localhost:5432/engine?sslmode=disable",
	"DATABASE_MAX_CONNECTIONS":           "20",
	"DATABASE_MIN_CONNECTIONS":           "1",
	"DATABASE_MAX_CONNECTION_LIFETIME":   "1h",
	"DATABASE_MAX_CONNECTION_IDLE_TIME":  "15m",
	"DATABASE_HEALTH_CHECK_INTERVAL":     "30s",
	"DATABASE_CONNECT_TIMEOUT":           "10s",
	"MIGRATION_LOCK_TIMEOUT":             "60s",
	"BRANCHES":                           `[{"name":"preview","httpAddr":":8081","corsOrigins":[],"indexable":false}]`,
	"MAX_FEATURE_BUILD_BYTES":            "268435456",
	"FEATURE_BUILD_REGISTRATION_TIMEOUT": "30s",
	"DESIGN_PREVIEW_TTL":                 "20m",
	"MIGRATE_ON_START":                   "false",
	"LOG_LEVEL":                          "info",
	"TRUST_PROXY_HEADERS":                "false",
	"MAX_JSON_BYTES":                     "1048576",
	"MAX_UPLOAD_BYTES":                   "5368709120",
	"FORM_RATE_LIMIT":                    "20",
	"FORM_RATE_WINDOW":                   "1m",
	"FORM_RATE_MAX_CLIENTS":              "10000",
	"FORM_RATE_SWEEP_INTERVAL":           "10m",
	"READ_HEADER_TIMEOUT":                "10s",
	"SHUTDOWN_TIMEOUT":                   "20s",
	"HTTP_READ_TIMEOUT":                  "10m",
	"HTTP_WRITE_TIMEOUT":                 "10m",
	"HTTP_IDLE_TIMEOUT":                  "2m",
	"HEALTH_CHECK_TIMEOUT":               "3s",
	"S3_STARTUP_TIMEOUT":                 "60s",
	"S3_RETRY_INTERVAL":                  "2s",
	"MEDIA_RENDITION_LEASE":              "20m",
	"MEDIA_WORKER_POLL_INTERVAL":         "1s",
	"MEDIA_REDIRECT_CACHE_TTL":           "5m",
	"RUNTIME_ASSET_CACHE_TTL":            "1h",
	"S3_ENDPOINT":                        "http://localhost:9000",
	"S3_REGION":                          "us-east-1",
	"S3_BUCKET":                          "engine-files",
	"S3_ACCESS_KEY":                      "test-access-key",
	"S3_SECRET_KEY":                      "test-secret-key",
	"S3_PUBLIC_BASE_URL":                 "http://localhost:9000/",
	"S3_FORCE_PATH_STYLE":                "true",
	"S3_ENSURE_BUCKET":                   "true",
}

func setLoadConfig(t *testing.T) {
	t.Helper()
	for key, value := range validLoadConfig {
		t.Setenv(key, value)
	}
}

func TestLoadRequiresAndUsesExplicitRuntimeValues(t *testing.T) {
	setLoadConfig(t)
	t.Setenv("BOOTSTRAP_ADMIN_TOKEN", "test-bootstrap-token-long-enough")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxHeaderBytes != 1<<20 || cfg.ReadHeaderTimeout != 10*time.Second || cfg.ShutdownTimeout != 20*time.Second || cfg.ReadTimeout != 10*time.Minute || cfg.WriteTimeout != 10*time.Minute || cfg.IdleTimeout != 2*time.Minute {
		t.Fatalf("HTTP timeouts were not loaded from the environment: %+v", cfg)
	}
	if cfg.MaxFeatureBuildBytes != 268435456 || cfg.FormRateWindow != time.Minute || cfg.FormRateMaxClients != 10000 || cfg.FormRateSweepInterval != 10*time.Minute || len(cfg.Branches) != 1 || cfg.Branches[0].Name != "preview" {
		t.Fatalf("operational values were not loaded explicitly: %+v", cfg)
	}
	if cfg.Database.MaxConns != 20 || cfg.Database.MinConns != 1 || cfg.Database.ConnectTimeout != 10*time.Second || cfg.MigrationLockTimeout != time.Minute || cfg.ReadinessTimeout != 3*time.Second || cfg.S3StartupTimeout != time.Minute || cfg.S3RetryInterval != 2*time.Second || cfg.MediaRenditionLease != 20*time.Minute || cfg.MediaWorkerPollInterval != time.Second || cfg.MediaRedirectCacheTTL != 5*time.Minute || cfg.RuntimeAssetCacheTTL != time.Hour || cfg.FeatureBuildRegistrationTimeout != 30*time.Second || cfg.DesignPreviewTTL != 20*time.Minute {
		t.Fatalf("infrastructure settings were not loaded explicitly: %+v", cfg)
	}
	if cfg.S3.PublicBaseURL != "http://localhost:9000" || !cfg.S3.ForcePathStyle || !cfg.S3.EnsureBucket {
		t.Fatalf("S3 values were not normalized or loaded: %+v", cfg.S3)
	}

	for key := range validLoadConfig {
		t.Run("missing_"+key, func(t *testing.T) {
			setLoadConfig(t)
			t.Setenv(key, "")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("Load error = %v, want a required-value error for %s", err, key)
			}
		})
	}
}

func TestLoadRejectsMalformedScalarsAndBranchSettings(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"integer", "MAX_JSON_BYTES", "one-megabyte"},
		{"header_bytes_below_range", "HTTP_MAX_HEADER_BYTES", "1023"},
		{"header_bytes_above_range", "HTTP_MAX_HEADER_BYTES", "16777217"},
		{"boolean", "MIGRATE_ON_START", "sometimes"},
		{"duration", "HTTP_READ_TIMEOUT", "later"},
		{"media_redirect_cache_ttl_invalid", "MEDIA_REDIRECT_CACHE_TTL", "later"},
		{"media_redirect_cache_ttl_zero", "MEDIA_REDIRECT_CACHE_TTL", "0s"},
		{"media_redirect_cache_ttl_subsecond", "MEDIA_REDIRECT_CACHE_TTL", "999ms"},
		{"media_redirect_cache_ttl_fractional_second", "MEDIA_REDIRECT_CACHE_TTL", "1500ms"},
		{"runtime_asset_cache_ttl_invalid", "RUNTIME_ASSET_CACHE_TTL", "later"},
		{"runtime_asset_cache_ttl_zero", "RUNTIME_ASSET_CACHE_TTL", "0s"},
		{"runtime_asset_cache_ttl_subsecond", "RUNTIME_ASSET_CACHE_TTL", "999ms"},
		{"runtime_asset_cache_ttl_fractional_second", "RUNTIME_ASSET_CACHE_TTL", "1500ms"},
		{"zero_duration", "FORM_RATE_WINDOW", "0s"},
		{"zero_rate_sweep_interval", "FORM_RATE_SWEEP_INTERVAL", "0s"},
		{"invalid_rate_capacity", "FORM_RATE_MAX_CLIENTS", "many"},
		{"zero_rate_capacity", "FORM_RATE_MAX_CLIENTS", "0"},
		{"db_max_connections_zero", "DATABASE_MAX_CONNECTIONS", "0"},
		{"db_min_connections_negative", "DATABASE_MIN_CONNECTIONS", "-1"},
		{"db_connect_timeout_invalid", "DATABASE_CONNECT_TIMEOUT", "soon"},
		{"db_min_exceeds_max", "DATABASE_MIN_CONNECTIONS", "21"},
		{"branches_not_array", "BRANCHES", `{"name":"preview"}`},
		{"branch_index_policy_missing", "BRANCHES", `[{"name":"preview","httpAddr":":8081","corsOrigins":[]}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setLoadConfig(t)
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted %s=%q", test.key, test.value)
			}
		})
	}
}

func TestLoadAcceptsOneSecondCacheTTLs(t *testing.T) {
	setLoadConfig(t)
	t.Setenv("MEDIA_REDIRECT_CACHE_TTL", "1s")
	t.Setenv("RUNTIME_ASSET_CACHE_TTL", "1s")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MediaRedirectCacheTTL != time.Second || cfg.RuntimeAssetCacheTTL != time.Second {
		t.Fatalf("one-second cache TTLs were not preserved: media=%s runtime=%s", cfg.MediaRedirectCacheTTL, cfg.RuntimeAssetCacheTTL)
	}
}

func TestLoadDatabaseRejectsMalformedURLWithoutLeakingCredentials(t *testing.T) {
	setLoadConfig(t)
	t.Setenv("DATABASE_URL", "postgres://qa:secret-value@localhost:not-a-port/engine")
	_, err := LoadDatabase()
	if err == nil {
		t.Fatal("LoadDatabase accepted malformed PostgreSQL URL")
	}
	if strings.Contains(err.Error(), "secret-value") || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("malformed URL error should identify DATABASE_URL without revealing its credentials: %v", err)
	}
}

func TestLoadPaginationRequiresPositiveConfiguredBounds(t *testing.T) {
	setPaginationConfig := func(t *testing.T) {
		t.Helper()
		t.Setenv("ADMIN_PAGE_SIZE", "200")
		t.Setenv("PUBLIC_PAGE_SIZE", "50")
		t.Setenv("SUBMISSION_EXPORT_MAX_ROWS", "10000")
	}
	setPaginationConfig(t)
	cfg, err := LoadPagination()
	if err != nil || cfg.AdminPageSize != 200 || cfg.PublicPageSize != 50 || cfg.SubmissionExportMaxRows != 10000 {
		t.Fatalf("LoadPagination() = %+v, %v", cfg, err)
	}
	for key := range map[string]bool{"ADMIN_PAGE_SIZE": true, "PUBLIC_PAGE_SIZE": true, "SUBMISSION_EXPORT_MAX_ROWS": true} {
		t.Run("missing_"+key, func(t *testing.T) {
			setPaginationConfig(t)
			t.Setenv(key, "")
			if _, err := LoadPagination(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("LoadPagination error = %v, want required %s", err, key)
			}
		})
	}
	t.Setenv("PUBLIC_PAGE_SIZE", "201")
	if _, err := LoadPagination(); err == nil {
		t.Fatal("PUBLIC_PAGE_SIZE above ADMIN_PAGE_SIZE was accepted")
	}
}

func setFilesConfig(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"FILES_MAX_BYTES": "5368709120", "FILES_PART_BYTES": "8388608", "FILES_OWNER_QUOTA_BYTES": "53687091200",
		"FILES_MAX_SESSIONS": "10", "FILES_LIST_LIMIT": "100", "FILES_RATE_LIMIT": "1200", "FILES_RATE_MAX_CLIENTS": "10000",
		"FILES_UPLOAD_TTL": "24h", "FILES_CLEANUP_INTERVAL": "5m", "FILES_LINK_MAX_TTL": "15m", "FILES_PROCESS_TIMEOUT": "15m", "FILES_RATE_WINDOW": "1m",
		"FILES_TEMP_DIR": t.TempDir(), "FILES_PUBLIC_BASE_URL": "http://localhost:8080", "FILES_SIGNING_KEY_ENV": "TEST_FILES_SIGNING_KEY", "FILE_SERVICES": "[]",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	t.Setenv("TEST_FILES_SIGNING_KEY", "test-only-signing-key-that-is-at-least-32-bytes")
}

func TestLoadFilesRequiresExplicitLimitsAndAllowsEmptyServiceList(t *testing.T) {
	setFilesConfig(t)
	cfg, err := LoadFiles()
	if err != nil || cfg.MaxBytes != 5<<30 || len(cfg.Services) != 0 {
		t.Fatalf("LoadFiles() = %+v, %v", cfg, err)
	}
	t.Setenv("FILE_SERVICES", "")
	if _, err := LoadFiles(); err == nil {
		t.Fatal("empty FILE_SERVICES was accepted instead of explicit []")
	}
}

func TestLoadFilesNamesMissingOperationalSettings(t *testing.T) {
	keys := []string{"FILES_MAX_BYTES", "FILES_PART_BYTES", "FILES_OWNER_QUOTA_BYTES", "FILES_MAX_SESSIONS", "FILES_LIST_LIMIT", "FILES_RATE_LIMIT", "FILES_RATE_MAX_CLIENTS", "FILES_UPLOAD_TTL", "FILES_CLEANUP_INTERVAL", "FILES_LINK_MAX_TTL", "FILES_PROCESS_TIMEOUT", "FILES_RATE_WINDOW", "FILES_TEMP_DIR", "FILES_PUBLIC_BASE_URL", "FILES_SIGNING_KEY_ENV", "FILE_SERVICES"}
	for _, key := range keys {
		t.Run("missing_"+key, func(t *testing.T) {
			setFilesConfig(t)
			t.Setenv(key, "")
			if _, err := LoadFiles(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("LoadFiles error = %v, want required setting %s", err, key)
			}
		})
	}
}

func TestLoadPrerenderRequiresExplicitSwitchAndResourcesWhenEnabled(t *testing.T) {
	t.Setenv("PRERENDER_ENABLED", "")
	if _, err := LoadPrerender(); err == nil || !strings.Contains(err.Error(), "PRERENDER_ENABLED") {
		t.Fatalf("missing switch error = %v", err)
	}
	t.Setenv("PRERENDER_ENABLED", "false")
	if options, err := LoadPrerender(); err != nil || options != nil {
		t.Fatalf("disabled prerender = %v, %v", options, err)
	}

	dir := t.TempDir()
	paths := make([]string, 4)
	for index, name := range []string{"node", "worker.mjs", "playwright.mjs", "chromium"} {
		paths[index] = filepath.Join(dir, name)
		mode := os.FileMode(0o600)
		if index == 0 || index == 3 {
			mode = 0o700
		}
		if err := os.WriteFile(paths[index], []byte("test executable placeholder"), mode); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{
		"PRERENDER_ENABLED": "true", "PRERENDER_NODE_PATH": paths[0], "PRERENDER_WORKER_PATH": paths[1], "PRERENDER_PLAYWRIGHT_MODULE": paths[2], "PRERENDER_BROWSER_PATH": paths[3],
		"PRERENDER_CONCURRENCY": "2", "PRERENDER_QUEUE_SIZE": "8", "PRERENDER_CACHE_ENTRIES": "200", "PRERENDER_MAX_HTML_BYTES": "4194304", "PRERENDER_HEAP_MB": "128", "PRERENDER_VIEWPORT_WIDTH": "1440", "PRERENDER_VIEWPORT_HEIGHT": "1000", "PRERENDER_MAX_ATTEMPTS": "2", "PRERENDER_MAX_REQUESTS": "200", "PRERENDER_MAX_NETWORK_BYTES": "67108864",
		"PRERENDER_TIMEOUT": "20s", "PRERENDER_ATTEMPT_TIMEOUT": "10s", "PRERENDER_QUIET_INTERVAL": "200ms", "PRERENDER_CACHE_TTL": "10m",
	} {
		t.Setenv(key, value)
	}
	options, err := LoadPrerender()
	if err != nil || options == nil || options.Concurrency != 2 || options.Timeout != 20*time.Second {
		t.Fatalf("enabled prerender = %+v, %v", options, err)
	}
	t.Setenv("PRERENDER_CONCURRENCY", "")
	if _, err := LoadPrerender(); err == nil || !strings.Contains(err.Error(), "PRERENDER_CONCURRENCY") {
		t.Fatalf("missing enabled prerender limit error = %v", err)
	}
}

func TestLoadSitepackRequiresExplicitLimits(t *testing.T) {
	t.Setenv("SITEPACK_TEMP_DIR", t.TempDir())
	values := map[string]string{"SITEPACK_MAX_BYTES": "21474836480", "SITEPACK_MAX_EXPANDED_BYTES": "42949672960", "SITEPACK_MAX_ENTRIES": "10000", "SITEPACK_MAX_INDEX_BYTES": "16777216", "SITEPACK_MAX_SVG_BYTES": "8388608", "SITEPACK_BROWSER_FALLBACK_BYTES": "67108864"}
	for key, value := range values {
		t.Setenv(key, value)
	}
	if _, err := LoadSitepack(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SITEPACK_MAX_BYTES", "")
	if _, err := LoadSitepack(); err == nil || !strings.Contains(err.Error(), "SITEPACK_MAX_BYTES") {
		t.Fatalf("missing sitepack limit error = %v", err)
	}
	t.Setenv("SITEPACK_MAX_BYTES", values["SITEPACK_MAX_BYTES"])
	t.Setenv("SITEPACK_MAX_SVG_BYTES", "1023")
	if _, err := LoadSitepack(); err == nil || !strings.Contains(err.Error(), "SITEPACK_MAX_SVG_BYTES") {
		t.Fatalf("undersized SVG limit error = %v", err)
	}
	t.Setenv("SITEPACK_MAX_SVG_BYTES", "8388608")
	t.Setenv("SITEPACK_MAX_BYTES", "1048576")
	if _, err := LoadSitepack(); err == nil || !strings.Contains(err.Error(), "SITEPACK_MAX_SVG_BYTES") {
		t.Fatalf("SVG limit larger than archive error = %v", err)
	}
	t.Setenv("SITEPACK_MAX_BYTES", values["SITEPACK_MAX_BYTES"])
	for key := range values {
		t.Run("missing_"+key, func(t *testing.T) {
			for field, value := range values {
				t.Setenv(field, value)
			}
			t.Setenv("SITEPACK_TEMP_DIR", t.TempDir())
			t.Setenv(key, "")
			if _, err := LoadSitepack(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("LoadSitepack error = %v, want required setting %s", err, key)
			}
		})
	}
}
