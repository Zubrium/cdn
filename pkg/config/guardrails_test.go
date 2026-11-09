package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setPrerenderGuardrailConfig(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	paths := map[string]string{
		"PRERENDER_NODE_PATH":         filepath.Join(dir, "node"),
		"PRERENDER_WORKER_PATH":       filepath.Join(dir, "worker.mjs"),
		"PRERENDER_PLAYWRIGHT_MODULE": filepath.Join(dir, "playwright.mjs"),
		"PRERENDER_BROWSER_PATH":      filepath.Join(dir, "chromium"),
	}
	for key, path := range paths {
		mode := os.FileMode(0o600)
		if key == "PRERENDER_NODE_PATH" || key == "PRERENDER_BROWSER_PATH" {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte("test fixture"), mode); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	for key, value := range map[string]string{
		"PRERENDER_ENABLED":           "true",
		"PRERENDER_CONCURRENCY":       "2",
		"PRERENDER_QUEUE_SIZE":        "8",
		"PRERENDER_CACHE_ENTRIES":     "200",
		"PRERENDER_MAX_HTML_BYTES":    "4194304",
		"PRERENDER_HEAP_MB":           "128",
		"PRERENDER_VIEWPORT_WIDTH":    "1440",
		"PRERENDER_VIEWPORT_HEIGHT":   "1000",
		"PRERENDER_MAX_REQUESTS":      "200",
		"PRERENDER_MAX_NETWORK_BYTES": "67108864",
		"PRERENDER_MAX_ATTEMPTS":      "2",
		"PRERENDER_TIMEOUT":           "20s",
		"PRERENDER_ATTEMPT_TIMEOUT":   "10s",
		"PRERENDER_QUIET_INTERVAL":    "200ms",
		"PRERENDER_CACHE_TTL":         "10m",
	} {
		t.Setenv(key, value)
	}
	return paths
}

func setSitepackGuardrailConfig(t *testing.T) map[string]string {
	t.Helper()
	t.Setenv("SITEPACK_TEMP_DIR", t.TempDir())
	values := map[string]string{
		"SITEPACK_MAX_BYTES":              "21474836480",
		"SITEPACK_MAX_EXPANDED_BYTES":     "42949672960",
		"SITEPACK_MAX_ENTRIES":            "10000",
		"SITEPACK_MAX_INDEX_BYTES":        "16777216",
		"SITEPACK_MAX_SVG_BYTES":          "8388608",
		"SITEPACK_BROWSER_FALLBACK_BYTES": "67108864",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	return values
}

func requireConfigErrorNames(t *testing.T, err error, names ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected configuration error mentioning %s", strings.Join(names, ", "))
	}
	for _, name := range names {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("configuration error %q does not identify %s", err, name)
		}
	}
}

func TestLoadDatabaseRejectsEveryInvalidPoolSettingByName(t *testing.T) {
	setDatabase := func(t *testing.T) {
		t.Helper()
		for key, value := range map[string]string{
			"DATABASE_URL":                      "postgres://engine:secret@localhost:5432/engine?sslmode=disable",
			"DATABASE_MAX_CONNECTIONS":          "20",
			"DATABASE_MIN_CONNECTIONS":          "1",
			"DATABASE_MAX_CONNECTION_LIFETIME":  "1h",
			"DATABASE_MAX_CONNECTION_IDLE_TIME": "15m",
			"DATABASE_HEALTH_CHECK_INTERVAL":    "30s",
			"DATABASE_CONNECT_TIMEOUT":          "10s",
			"MIGRATION_LOCK_TIMEOUT":            "60s",
		} {
			t.Setenv(key, value)
		}
	}
	tests := []struct{ name, key, value string }{
		{"missing_database_url", "DATABASE_URL", ""},
		{"invalid_max_connections", "DATABASE_MAX_CONNECTIONS", "many"},
		{"zero_max_connections", "DATABASE_MAX_CONNECTIONS", "0"},
		{"negative_min_connections", "DATABASE_MIN_CONNECTIONS", "-1"},
		{"min_exceeds_max", "DATABASE_MIN_CONNECTIONS", "21"},
		{"invalid_max_lifetime", "DATABASE_MAX_CONNECTION_LIFETIME", "forever"},
		{"zero_max_lifetime", "DATABASE_MAX_CONNECTION_LIFETIME", "0s"},
		{"invalid_idle_lifetime", "DATABASE_MAX_CONNECTION_IDLE_TIME", "later"},
		{"zero_idle_lifetime", "DATABASE_MAX_CONNECTION_IDLE_TIME", "0s"},
		{"invalid_health_period", "DATABASE_HEALTH_CHECK_INTERVAL", "often"},
		{"zero_health_period", "DATABASE_HEALTH_CHECK_INTERVAL", "0s"},
		{"invalid_connect_timeout", "DATABASE_CONNECT_TIMEOUT", "soon"},
		{"zero_connect_timeout", "DATABASE_CONNECT_TIMEOUT", "0s"},
		{"invalid_migration_timeout", "MIGRATION_LOCK_TIMEOUT", "never"},
		{"zero_migration_timeout", "MIGRATION_LOCK_TIMEOUT", "0s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setDatabase(t)
			t.Setenv(test.key, test.value)
			_, err := LoadDatabase()
			requireConfigErrorNames(t, err, test.key)
		})
	}
}

func TestLoadDatabaseAllowsZeroMinimumConnections(t *testing.T) {
	for key, value := range map[string]string{
		"DATABASE_URL":                      "postgres://engine:secret@localhost:5432/engine?sslmode=disable",
		"DATABASE_MAX_CONNECTIONS":          "20",
		"DATABASE_MIN_CONNECTIONS":          "0",
		"DATABASE_MAX_CONNECTION_LIFETIME":  "1h",
		"DATABASE_MAX_CONNECTION_IDLE_TIME": "15m",
		"DATABASE_HEALTH_CHECK_INTERVAL":    "30s",
		"DATABASE_CONNECT_TIMEOUT":          "10s",
		"MIGRATION_LOCK_TIMEOUT":            "60s",
	} {
		t.Setenv(key, value)
	}
	if _, err := LoadDatabase(); err != nil {
		t.Fatalf("zero DATABASE_MIN_CONNECTIONS is valid: %v", err)
	}
}

func TestLoadersRejectUnwritableTempDirectoriesBeforeRuntimeStartup(t *testing.T) {
	makeInvalidPath := func(t *testing.T) string {
		t.Helper()
		file := filepath.Join(t.TempDir(), "regular-file")
		if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(file, "child")
	}

	t.Run("files", func(t *testing.T) {
		setFilesConfig(t)
		path := makeInvalidPath(t)
		t.Setenv("FILES_TEMP_DIR", path)
		_, err := LoadFiles()
		requireConfigErrorNames(t, err, "FILES_TEMP_DIR")
		if strings.Contains(err.Error(), path) {
			t.Fatalf("FILES_TEMP_DIR error leaked configured path: %v", err)
		}
	})

	t.Run("sitepack", func(t *testing.T) {
		setSitepackGuardrailConfig(t)
		path := makeInvalidPath(t)
		t.Setenv("SITEPACK_TEMP_DIR", path)
		_, err := LoadSitepack()
		requireConfigErrorNames(t, err, "SITEPACK_TEMP_DIR")
		if strings.Contains(err.Error(), path) {
			t.Fatalf("SITEPACK_TEMP_DIR error leaked configured path: %v", err)
		}
	})
}

func TestLoadPrerenderRejectsEveryInvalidResourceBoundByName(t *testing.T) {
	tests := []struct{ name, key, value string }{
		{"bad_concurrency", "PRERENDER_CONCURRENCY", "many"},
		{"zero_concurrency", "PRERENDER_CONCURRENCY", "0"},
		{"negative_queue", "PRERENDER_QUEUE_SIZE", "-1"},
		{"zero_cache_entries", "PRERENDER_CACHE_ENTRIES", "0"},
		{"html_below_minimum", "PRERENDER_MAX_HTML_BYTES", "1023"},
		{"heap_below_minimum", "PRERENDER_HEAP_MB", "31"},
		{"viewport_width_below_minimum", "PRERENDER_VIEWPORT_WIDTH", "319"},
		{"viewport_height_below_minimum", "PRERENDER_VIEWPORT_HEIGHT", "239"},
		{"zero_attempts", "PRERENDER_MAX_ATTEMPTS", "0"},
		{"zero_requests", "PRERENDER_MAX_REQUESTS", "0"},
		{"network_below_minimum", "PRERENDER_MAX_NETWORK_BYTES", "1023"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setPrerenderGuardrailConfig(t)
			t.Setenv(test.key, test.value)
			_, err := LoadPrerender()
			requireConfigErrorNames(t, err, test.key)
		})
	}
}

func TestLoadPrerenderRejectsEveryInvalidDurationByName(t *testing.T) {
	tests := []struct{ key, value string }{
		{"PRERENDER_TIMEOUT", "later"},
		{"PRERENDER_TIMEOUT", "0s"},
		{"PRERENDER_ATTEMPT_TIMEOUT", "soon"},
		{"PRERENDER_ATTEMPT_TIMEOUT", "0s"},
		{"PRERENDER_QUIET_INTERVAL", "often"},
		{"PRERENDER_QUIET_INTERVAL", "0s"},
		{"PRERENDER_CACHE_TTL", "eventually"},
		{"PRERENDER_CACHE_TTL", "0s"},
	}
	for _, test := range tests {
		t.Run(test.key+"_"+test.value, func(t *testing.T) {
			setPrerenderGuardrailConfig(t)
			t.Setenv(test.key, test.value)
			_, err := LoadPrerender()
			requireConfigErrorNames(t, err, test.key)
		})
	}
}

func TestLoadPrerenderNamesRelatedTimeoutErrors(t *testing.T) {
	t.Run("attempt_timeout_exceeds_total", func(t *testing.T) {
		setPrerenderGuardrailConfig(t)
		t.Setenv("PRERENDER_TIMEOUT", "5s")
		t.Setenv("PRERENDER_ATTEMPT_TIMEOUT", "6s")
		_, err := LoadPrerender()
		requireConfigErrorNames(t, err, "PRERENDER_ATTEMPT_TIMEOUT", "PRERENDER_TIMEOUT")
	})
	t.Run("quiet_interval_exceeds_attempt", func(t *testing.T) {
		setPrerenderGuardrailConfig(t)
		t.Setenv("PRERENDER_QUIET_INTERVAL", "10s")
		t.Setenv("PRERENDER_ATTEMPT_TIMEOUT", "10s")
		_, err := LoadPrerender()
		requireConfigErrorNames(t, err, "PRERENDER_QUIET_INTERVAL", "PRERENDER_ATTEMPT_TIMEOUT")
	})
}

func TestLoadPrerenderRejectsInvalidPathsByName(t *testing.T) {
	t.Run("empty_node_path", func(t *testing.T) {
		setPrerenderGuardrailConfig(t)
		t.Setenv("PRERENDER_NODE_PATH", "")
		_, err := LoadPrerender()
		requireConfigErrorNames(t, err, "PRERENDER_NODE_PATH")
	})
	t.Run("directory_worker", func(t *testing.T) {
		setPrerenderGuardrailConfig(t)
		t.Setenv("PRERENDER_WORKER_PATH", t.TempDir())
		_, err := LoadPrerender()
		requireConfigErrorNames(t, err, "PRERENDER_WORKER_PATH")
	})
	t.Run("missing_module", func(t *testing.T) {
		setPrerenderGuardrailConfig(t)
		t.Setenv("PRERENDER_PLAYWRIGHT_MODULE", filepath.Join(t.TempDir(), "missing.mjs"))
		_, err := LoadPrerender()
		requireConfigErrorNames(t, err, "PRERENDER_PLAYWRIGHT_MODULE")
	})
	t.Run("non_executable_browser", func(t *testing.T) {
		setPrerenderGuardrailConfig(t)
		path := filepath.Join(t.TempDir(), "chromium")
		if err := os.WriteFile(path, []byte("browser"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PRERENDER_BROWSER_PATH", path)
		_, err := LoadPrerender()
		requireConfigErrorNames(t, err, "PRERENDER_BROWSER_PATH")
	})
}

func TestLoadPrerenderAcceptsDocumentedMinimumResources(t *testing.T) {
	setPrerenderGuardrailConfig(t)
	for key, value := range map[string]string{
		"PRERENDER_CONCURRENCY":       "1",
		"PRERENDER_QUEUE_SIZE":        "1",
		"PRERENDER_CACHE_ENTRIES":     "1",
		"PRERENDER_MAX_HTML_BYTES":    "1024",
		"PRERENDER_HEAP_MB":           "32",
		"PRERENDER_VIEWPORT_WIDTH":    "320",
		"PRERENDER_VIEWPORT_HEIGHT":   "240",
		"PRERENDER_MAX_REQUESTS":      "1",
		"PRERENDER_MAX_NETWORK_BYTES": "1024",
		"PRERENDER_MAX_ATTEMPTS":      "1",
		"PRERENDER_TIMEOUT":           "1s",
		"PRERENDER_ATTEMPT_TIMEOUT":   "1s",
		"PRERENDER_QUIET_INTERVAL":    "1ms",
		"PRERENDER_CACHE_TTL":         "1ns",
	} {
		t.Setenv(key, value)
	}
	options, err := LoadPrerender()
	if err != nil || options == nil {
		t.Fatalf("documented minimum prerender configuration rejected: options=%+v err=%v", options, err)
	}
}

func TestLoadFilesNamesCrossLimitAndSecretErrors(t *testing.T) {
	tests := []struct {
		name, key, value string
		names            []string
	}{
		{"maximum_file_exceeds_five_gib", "FILES_MAX_BYTES", "5368709121", []string{"FILES_MAX_BYTES"}},
		{"part_exceeds_file", "FILES_PART_BYTES", "5368709121", []string{"FILES_PART_BYTES", "FILES_MAX_BYTES"}},
		{"owner_quota_below_file", "FILES_OWNER_QUOTA_BYTES", "5368709119", []string{"FILES_OWNER_QUOTA_BYTES", "FILES_MAX_BYTES"}},
		{"zero_sessions", "FILES_MAX_SESSIONS", "0", []string{"FILES_MAX_SESSIONS"}},
		{"list_limit_above_hard_cap", "FILES_LIST_LIMIT", "1001", []string{"FILES_LIST_LIMIT", "1000"}},
		{"zero_rate_limit", "FILES_RATE_LIMIT", "0", []string{"FILES_RATE_LIMIT"}},
		{"zero_rate_clients", "FILES_RATE_MAX_CLIENTS", "0", []string{"FILES_RATE_MAX_CLIENTS"}},
		{"zero_upload_ttl", "FILES_UPLOAD_TTL", "0s", []string{"FILES_UPLOAD_TTL"}},
		{"zero_cleanup_interval", "FILES_CLEANUP_INTERVAL", "0s", []string{"FILES_CLEANUP_INTERVAL"}},
		{"zero_processing_timeout", "FILES_PROCESS_TIMEOUT", "0s", []string{"FILES_PROCESS_TIMEOUT"}},
		{"zero_rate_window", "FILES_RATE_WINDOW", "0s", []string{"FILES_RATE_WINDOW"}},
		{"zero_link_ttl", "FILES_LINK_MAX_TTL", "0s", []string{"FILES_LINK_MAX_TTL"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setFilesConfig(t)
			t.Setenv("FILES_SIGNING_KEY_FILE", "")
			t.Setenv(test.key, test.value)
			_, err := LoadFiles()
			requireConfigErrorNames(t, err, test.names...)
		})
	}
	t.Run("short_signing_secret_is_not_reported", func(t *testing.T) {
		setFilesConfig(t)
		t.Setenv("FILES_SIGNING_KEY_FILE", "")
		t.Setenv("TEST_FILES_SIGNING_KEY", "sensitive-short-value")
		_, err := LoadFiles()
		requireConfigErrorNames(t, err, "FILES_SIGNING_KEY_ENV", "FILES_SIGNING_KEY_FILE")
		if strings.Contains(err.Error(), "sensitive-short-value") {
			t.Fatalf("error leaked configured secret: %v", err)
		}
	})
	t.Run("short_service_secret_is_not_reported", func(t *testing.T) {
		setFilesConfig(t)
		t.Setenv("FILES_SIGNING_KEY_FILE", "")
		t.Setenv("TEST_DOMAIN_SERVICE_KEY", "sensitive-short-value")
		t.Setenv("FILE_SERVICES", `[{"name":"domain","permissions":["read"],"keys":[{"env":"TEST_DOMAIN_SERVICE_KEY"}]}]`)
		_, err := LoadFiles()
		requireConfigErrorNames(t, err, "FILE_SERVICES", "domain")
		if strings.Contains(err.Error(), "sensitive-short-value") {
			t.Fatalf("error leaked configured secret: %v", err)
		}
	})
}

func TestLoadSitepackNamesCrossLimitErrors(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		names  []string
	}{
		{"expanded_below_archive", map[string]string{"SITEPACK_MAX_EXPANDED_BYTES": "21474836479"}, []string{"SITEPACK_MAX_EXPANDED_BYTES", "SITEPACK_MAX_BYTES"}},
		{"index_above_expanded", map[string]string{"SITEPACK_MAX_INDEX_BYTES": "42949672961"}, []string{"SITEPACK_MAX_INDEX_BYTES", "SITEPACK_MAX_EXPANDED_BYTES"}},
		{"svg_above_archive", map[string]string{"SITEPACK_MAX_BYTES": "1048576", "SITEPACK_MAX_EXPANDED_BYTES": "1048576", "SITEPACK_MAX_INDEX_BYTES": "1024", "SITEPACK_MAX_SVG_BYTES": "2097152", "SITEPACK_BROWSER_FALLBACK_BYTES": "1024"}, []string{"SITEPACK_MAX_SVG_BYTES", "SITEPACK_MAX_BYTES"}},
		{"svg_above_hard_cap", map[string]string{"SITEPACK_MAX_BYTES": "67108865", "SITEPACK_MAX_EXPANDED_BYTES": "67108865", "SITEPACK_MAX_SVG_BYTES": "67108865"}, []string{"SITEPACK_MAX_SVG_BYTES", "64 MiB"}},
		{"fallback_above_archive", map[string]string{"SITEPACK_BROWSER_FALLBACK_BYTES": "21474836481"}, []string{"SITEPACK_BROWSER_FALLBACK_BYTES", "SITEPACK_MAX_BYTES"}},
		{"zero_entries", map[string]string{"SITEPACK_MAX_ENTRIES": "0"}, []string{"SITEPACK_MAX_ENTRIES"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setSitepackGuardrailConfig(t)
			for key, value := range test.values {
				t.Setenv(key, value)
			}
			_, err := LoadSitepack()
			requireConfigErrorNames(t, err, test.names...)
		})
	}
}

func TestLoadRejectsS3URLsContainingCredentialsWithoutLeakingThem(t *testing.T) {
	for _, key := range []string{"S3_ENDPOINT", "S3_PUBLIC_BASE_URL"} {
		t.Run(key, func(t *testing.T) {
			setLoadConfig(t)
			t.Setenv(key, "https://s3-user:private-secret@example.test")
			_, err := Load()
			requireConfigErrorNames(t, err, key)
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "s3-user") {
				t.Fatalf("error leaked URL credentials: %v", err)
			}
		})
	}
}
