package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"websiteengine/pkg/config"
)

func TestBindHTTPAddressesReleasesPartialBindingsOnConflict(t *testing.T) {
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	firstAddress := first.Addr().String()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	occupiedAddress := occupied.Addr().String()

	listeners, err := bindHTTPAddresses([]string{firstAddress, occupiedAddress})
	if err == nil {
		closeHTTPListeners(listeners)
		t.Fatal("expected occupied listener address to fail")
	}
	if !strings.Contains(err.Error(), occupiedAddress) {
		t.Fatalf("bind error does not identify occupied address %q: %v", occupiedAddress, err)
	}
	if len(listeners) != 0 {
		closeHTTPListeners(listeners)
		t.Fatalf("partial listeners returned after bind failure: %v", listeners)
	}

	retry, err := net.Listen("tcp", firstAddress)
	if err != nil {
		t.Fatalf("partial bind was not released after failure: %v", err)
	}
	_ = retry.Close()
}

func setValidStartupConfiguration(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"DATABASE_URL":             "postgres://qa:qa@127.0.0.1:1/unreachable?sslmode=disable",
		"DATABASE_MAX_CONNECTIONS": "10", "DATABASE_MIN_CONNECTIONS": "0",
		"DATABASE_MAX_CONNECTION_LIFETIME": "1h", "DATABASE_MAX_CONNECTION_IDLE_TIME": "15m",
		"DATABASE_HEALTH_CHECK_INTERVAL": "30s", "DATABASE_CONNECT_TIMEOUT": "1s", "MIGRATION_LOCK_TIMEOUT": "1s",
		"HTTP_ADDR":             "127.0.0.1:45900",
		"HTTP_MAX_HEADER_BYTES": "1048576",
		"BRANCHES":              `[{"name":"qa","httpAddr":"127.0.0.1:45901","corsOrigins":[],"indexable":false}]`,
		"MIGRATE_ON_START":      "false", "LOG_LEVEL": "info", "TRUST_PROXY_HEADERS": "false",
		"MAX_JSON_BYTES": "1048576", "MAX_UPLOAD_BYTES": "5368709120",
		"FORM_RATE_LIMIT": "20", "FORM_RATE_WINDOW": "1m", "FORM_RATE_MAX_CLIENTS": "1000", "FORM_RATE_SWEEP_INTERVAL": "10m",
		"READ_HEADER_TIMEOUT": "10s", "SHUTDOWN_TIMEOUT": "20s", "HTTP_READ_TIMEOUT": "10m", "HTTP_WRITE_TIMEOUT": "10m", "HTTP_IDLE_TIMEOUT": "2m", "HEALTH_CHECK_TIMEOUT": "3s",
		"S3_STARTUP_TIMEOUT": "1s", "S3_RETRY_INTERVAL": "1s", "MEDIA_RENDITION_LEASE": "20m", "MEDIA_WORKER_POLL_INTERVAL": "1s",
		"MEDIA_REDIRECT_CACHE_TTL": "5m", "RUNTIME_ASSET_CACHE_TTL": "1h",
		"MAX_FEATURE_BUILD_BYTES": "268435456", "FEATURE_BUILD_REGISTRATION_TIMEOUT": "30s", "DESIGN_PREVIEW_TTL": "20m",
		"S3_ENDPOINT": "http://127.0.0.1:45902", "S3_PUBLIC_BASE_URL": "http://127.0.0.1:45900", "S3_REGION": "us-east-1", "S3_BUCKET": "engine-qa-files", "S3_ACCESS_KEY": "test-access", "S3_SECRET_KEY": "test-secret", "S3_FORCE_PATH_STYLE": "true", "S3_ENSURE_BUCKET": "false",
		"ADMIN_PAGE_SIZE": "200", "PUBLIC_PAGE_SIZE": "50", "SUBMISSION_EXPORT_MAX_ROWS": "10000",
		"FILES_MAX_BYTES": "5368709120", "FILES_PART_BYTES": "8388608", "FILES_UPLOAD_TTL": "24h", "FILES_CLEANUP_INTERVAL": "5m", "FILES_PROCESS_TIMEOUT": "15m", "FILES_OWNER_QUOTA_BYTES": "53687091200", "FILES_MAX_SESSIONS": "10", "FILES_LIST_LIMIT": "100", "FILES_RATE_LIMIT": "1200", "FILES_RATE_WINDOW": "1m", "FILES_RATE_MAX_CLIENTS": "1000", "FILES_LINK_MAX_TTL": "15m", "FILES_PUBLIC_BASE_URL": "http://127.0.0.1:45900", "FILES_TEMP_DIR": t.TempDir(), "FILES_SIGNING_KEY_ENV": "TEST_ENGINE_FILE_KEY", "FILE_SERVICES": "[]",
		"TEST_ENGINE_FILE_KEY": "test-only-file-signing-secret-at-least-32-characters",
		"SITEPACK_TEMP_DIR":    t.TempDir(), "SITEPACK_MAX_BYTES": "21474836480", "SITEPACK_MAX_EXPANDED_BYTES": "42949672960", "SITEPACK_MAX_ENTRIES": "10000", "SITEPACK_MAX_INDEX_BYTES": "16777216", "SITEPACK_MAX_SVG_BYTES": "8388608", "SITEPACK_BROWSER_FALLBACK_BYTES": "67108864",
		"PRERENDER_ENABLED": "false",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestRunValidatesAllConfigGroupsBeforeDatabaseConnections(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "FILES_MAX_BYTES", "PUBLIC_PAGE_SIZE", "SITEPACK_MAX_BYTES", "SITEPACK_MAX_SVG_BYTES", "HTTP_MAX_HEADER_BYTES", "MEDIA_REDIRECT_CACHE_TTL", "RUNTIME_ASSET_CACHE_TTL", "PRERENDER_ENABLED"} {
		t.Run("missing_"+key, func(t *testing.T) {
			setValidStartupConfiguration(t)
			t.Setenv(key, "")
			err := run(slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(strings.ToLower(err.Error()), "connect") {
				t.Fatalf("startup should report missing %s before trying the unreachable database: %v", key, err)
			}
		})
	}
}

func TestNewServerUsesConfiguredHeaderLimit(t *testing.T) {
	const want = 2 << 20
	server := newServer(":0", http.NotFoundHandler(), config.Config{MaxHeaderBytes: want})
	if server.MaxHeaderBytes != want {
		t.Fatalf("MaxHeaderBytes = %d, want configured value %d", server.MaxHeaderBytes, want)
	}
}

type blockedBucketEnsurer struct {
	started chan struct{}
}

func (s blockedBucketEnsurer) EnsureBucket(ctx context.Context) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestEnsureBucketTimeoutBoundsAnInFlightRequest(t *testing.T) {
	store := blockedBucketEnsurer{started: make(chan struct{})}
	startedAt := time.Now()
	err := ensureBucket(context.Background(), store, 75*time.Millisecond, time.Second)
	if err != context.DeadlineExceeded {
		t.Fatalf("ensureBucket error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("ensureBucket took %s after 75ms timeout", elapsed)
	}
	select {
	case <-store.started:
	default:
		t.Fatal("EnsureBucket was not called")
	}
}

func TestRenameCommandsValidateArgumentsBeforeDatabaseConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if err := renameBranch([]string{"old"}); err == nil || !strings.Contains(err.Error(), "usage: engine rename-branch") {
		t.Fatalf("rename-branch should report usage before missing database configuration: %v", err)
	}
	if err := renameService([]string{"old"}); err == nil || !strings.Contains(err.Error(), "usage: engine rename-service") {
		t.Fatalf("rename-service should report usage before missing database configuration: %v", err)
	}
}
