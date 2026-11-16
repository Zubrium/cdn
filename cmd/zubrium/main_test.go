package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiresConfiguration(t *testing.T) {
	var out, err bytes.Buffer
	if code := run(nil, &out, &err); code == 0 || !strings.Contains(err.String(), "provide --config") {
		t.Fatalf("unexpected startup: %d %q", code, err.String())
	}
	out.Reset()
	err.Reset()
	if code := run([]string{"--version"}, &out, &err); code != 0 || !strings.Contains(out.String(), "incomplete") {
		t.Fatalf("version: %d %q", code, out.String())
	}
}

func TestCheckConfig(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := run([]string{"--check-config", filepath.Join(t.TempDir(), "missing")}, &out, &stderr); code == 0 {
		t.Fatal("missing configuration accepted")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `core:
  database_url_env: TEST_DB
  migration_database_url_env: TEST_MIGRATION_DB
  s3_endpoint: http://localhost:9000
  s3_region: local
  s3_bucket: test
  s3_access_key_env: TEST_ACCESS
  s3_secret_key_env: TEST_SECRET
  s3_allow_insecure_http: true
  atlas_binary: atlas
  migration_lock_timeout: 30s
  access_groups:
    content: 2
  cache_max_bytes: 1048576
  cache_ttl: 5m
  max_metadata_bytes: 65536
  min_connections: 0
  max_connections: 2
  connect_timeout: 2s
  max_lifetime: 1h
  max_idle_time: 5m
  health_interval: 30s
  max_object_bytes: 1024
  max_cached_object_bytes: 512
  maintenance_interval: 1m
  maintenance_timeout: 30s
  pending_ttl: 1h
  maintenance_batch_size: 100
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DB", "postgres://runtime:test-only@127.0.0.1:5432/zubrium_core_qa_unit")
	t.Setenv("TEST_MIGRATION_DB", "postgres://migrator:test-only@127.0.0.1:5432/zubrium_core_qa_unit")
	t.Setenv("TEST_ACCESS", "test")
	t.Setenv("TEST_SECRET", "test")
	out.Reset()
	stderr.Reset()
	if code := run([]string{"--check-config", path}, &out, &stderr); code != 0 {
		t.Fatalf("check failed: %q", stderr.String())
	}
}
