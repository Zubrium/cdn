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
	if code := run([]string{"--version"}, &out, &err); code != 0 || !strings.Contains(out.String(), "dev_v2") {
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
    coordinator_jobs: 2
    router_routes: 2
  cache_max_bytes: 1048576
  cache_ttl: 5m
  max_metadata_bytes: 65536
  min_connections: 0
  max_connections: 7
  connect_timeout: 2s
  max_lifetime: 1h
  max_idle_time: 5m
  health_interval: 30s
  max_object_bytes: 1048576
  max_cached_object_bytes: 512
  maintenance_interval: 1m
  maintenance_timeout: 30s
  pending_ttl: 1h
  session_idle_ttl: 8h
  session_absolute_ttl: 168h
  maintenance_batch_size: 100
  module_data:
    max_payload_bytes: 1048576
    max_index_bytes: 65536
    max_batch_bytes: 8388608
    max_page_bytes: 16777216
    max_selectors: 16
    max_selector_values: 256
    max_page_size: 100
    max_batch_operations: 64
loader:
  part_size_bytes: 65536
  max_file_bytes: 1048576
  max_part_bytes: 65536
  max_parts: 16
  max_concurrent_commits: 1
  session_ttl: 1h
  commit_timeout: 1m
  mode: auto
  buffer_threshold_bytes: 65536
  max_buffered_bytes: 131072
  session_cache_entries: 100
  session_cache_max_bytes: 1048576
  session_cache_ttl: 1m
  cleanup_interval: 1m
  cleanup_batch_size: 100
file_api:
  max_json_bytes: 1048576
  max_attribute_bytes: 32768
  max_groups_per_file: 8
  default_page_size: 25
  max_page_size: 100
admin_api:
  max_json_bytes: 1048576
  default_page_size: 25
  max_page_size: 100
  session_cookie_secure: false
  allowed_origins: [http://127.0.0.1:8080]
router_api:
  max_json_bytes: 1048576
  default_page_size: 25
  max_page_size: 100
  branch_id: branch-a
  service_credential_env: TEST_ROUTER_SERVICE
  public_listen_address: 127.0.0.1:8081
  upload_staging_directory: /tmp/zubrium-router-upload-qa
  upload_max_file_bytes: 524288
  upload_max_concurrent: 1
  upload_max_staged_bytes: 524288
  upload_allowed_content_types: [image/png]
http:
  listen_address: 127.0.0.1:8080
  read_header_timeout: 5s
  read_timeout: 5m
  write_timeout: 0s
  idle_timeout: 1m
  shutdown_timeout: 15s
  max_header_bytes: 65536
coordinator:
  job_access_group: coordinator_jobs
  jobs_page_default: 25
  jobs_page_maximum: 100
  instance_lease: 30s
  heartbeat_interval: 5s
  worker_concurrency: 2
  job_lease: 20s
  task_timeout: 15m
  poll_interval: 1s
  retry_base: 2s
  retry_maximum: 1m
  max_attempts: 3
  max_workflow_tasks: 1000
  max_children_per_task: 16
  workflow_retention: 48h
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DB", "postgres://runtime:test-only@127.0.0.1:5432/zubrium_core_qa_unit")
	t.Setenv("TEST_MIGRATION_DB", "postgres://migrator:test-only@127.0.0.1:5432/zubrium_core_qa_unit")
	t.Setenv("TEST_ACCESS", "test")
	t.Setenv("TEST_SECRET", "test")
	t.Setenv("TEST_ROUTER_SERVICE", "test")
	out.Reset()
	stderr.Reset()
	if code := run([]string{"--check-config", path}, &out, &stderr); code != 0 {
		t.Fatalf("check failed: %q", stderr.String())
	}
}
