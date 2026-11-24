package command

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/main/internal/composition"
)

func TestRequiresConfiguration(t *testing.T) {
	var out, err bytes.Buffer
	if code := Run(nil, &out, &err); code == 0 || !strings.Contains(err.String(), "provide --config") {
		t.Fatalf("unexpected startup: %d %q", code, err.String())
	}
	out.Reset()
	err.Reset()
	if code := Run([]string{"--version"}, &out, &err); code != 0 || !strings.Contains(out.String(), "dev_v2") {
		t.Fatalf("version: %d %q", code, out.String())
	}
}

func TestParseRunConfigArgumentsCanSuppressBootstrapSecret(t *testing.T) {
	path, suppressSecret, ok := parseRunConfigArguments([]string{"--suppress-bootstrap-secret", "--config", "config.yaml"})
	if !ok || path != "config.yaml" || !suppressSecret {
		t.Fatalf("parsed run arguments = (%q, %t, %t)", path, suppressSecret, ok)
	}
}

func TestCheckConfig(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run([]string{"--check-config", filepath.Join(t.TempDir(), "missing")}, &out, &stderr); code == 0 {
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
  max_metadata_bytes: 65536
  min_connections: 0
  max_connections: 12
  connect_timeout: 2s
  max_lifetime: 1h
  max_idle_time: 5m
  health_interval: 30s
  max_object_bytes: 1048576
  cache:
    max_bytes: 1048576
    ttl: 5m
    max_object_bytes: 512
    access_cache:
      max_entries: 100
      max_bytes: 65536
      ttl: 1m
    warmup:
      enabled: false
      mode: off
      timeout: 30s
      per_key_timeout: 5s
      concurrency: 1
      max_keys: 10
      retry:
        attempts: 1
        backoff: 10ms
      refresh_interval: 0s
      sources:
        inline: false
        published_artifacts: false
      targets:
        authorization: false
        metadata: true
        bytes: true
        router_manifests: false
        router_routes: false
      keys: []
  concurrency:
    storage:
      max_concurrent_reads: 2
      max_concurrent_authorizations: 2
      operation_timeout: 5s
      read_reserve: 1
    cache:
      shards: 2
      lru_promotion_interval: 1
      coalesce:
        enabled: true
        max_waiters: 4
        timeout: 1s
    invalidation:
      batch_size: 16
      batch_window: 1ms
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
  concurrency:
    max_concurrent_commits: 1
    max_concurrent_session_lookups: 2
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
  form_session_cookie_secure: false
  public_listen_address: 127.0.0.1:8081
  upload_staging_directory: /tmp/zubrium-router-upload-qa
  upload_max_file_bytes: 524288
  upload_max_concurrent: 1
  upload_max_staged_bytes: 524288
  upload_allowed_content_types: [image/png]
forms_api:
  file_service_credential_env: TEST_FORMS_SERVICE
  visitor_session_ttl: 30m
  visitor_session_cleanup_interval: 1m
  export_max_concurrent: 2
  export_max_rows: 10000
  export_max_bytes: 33554432
content_api:
  service_credential_env: TEST_CONTENT_SERVICE
  locale: en-US
admin:
  concurrency:
    http:
      max_in_flight: 16
      request_timeout: 30s
router:
  cache:
    manifests:
      enabled: true
      max_entries: 16
      max_bytes: 65536
      ttl: 1m
    routes:
      enabled: true
      max_entries: 16
      max_bytes: 65536
      ttl: 1m
    http:
      compression:
        enabled: false
        algorithms: [gzip]
        min_bytes: 64
        max_uncompressed_bytes: 4096
        max_compressed_bytes: 2048
        media_types: [text/html]
      vary:
        accept_encoding: true
        locale_from_cookie: no_store
  concurrency:
    http:
      max_in_flight: 16
      request_timeout: 30s
      per_route:
        enabled: false
        default_max_in_flight: 8
        overrides: []
    routes:
      max_concurrent_resolutions: 8
bus:
  concurrency:
    max_in_flight: 16
    per_operation:
      enabled: false
      default_max_in_flight: 8
      overrides: []
worker:
  archive:
    concurrency: {enabled: true, limit: 1, poll_interval: 1s, task_timeout: 1m, lease_duration: 10s}
  media:
    concurrency: {enabled: true, limit: 1, poll_interval: 1s, task_timeout: 1m, lease_duration: 10s}
  prerender:
    concurrency: {enabled: true, limit: 1, poll_interval: 1s, task_timeout: 1m, lease_duration: 10s}
  publication:
    concurrency: {enabled: true, limit: 1, poll_interval: 1s, task_timeout: 1m, lease_duration: 10s}
http:
  listen_address: 127.0.0.1:8080
  read_header_timeout: 5s
  read_timeout: 5m
  write_timeout: 0s
  idle_timeout: 1m
  shutdown_timeout: 30s
  max_header_bytes: 65536
coordinator:
  workflow_access_group: coordinator_jobs
  workflows_page_default: 25
  workflows_page_maximum: 100
  instance_lease: 30s
  heartbeat_interval: 5s
  retry_base: 2s
  retry_maximum: 1m
  max_attempts: 3
  max_workflow_tasks: 1000
  max_children_per_task: 16
  workflow_retention: 48h
`
	if slices.Contains(composition.BuildPlan().Components, "public.locale-preference") {
		data = strings.Replace(data, "forms_api:\n", "  locale_preference:\n    enabled: true\n    secure: false\n    max_age: 86400s\nforms_api:\n", 1)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DB", "postgres://runtime:test-only@127.0.0.1:5432/zubrium_core_qa_unit")
	t.Setenv("TEST_MIGRATION_DB", "postgres://migrator:test-only@127.0.0.1:5432/zubrium_core_qa_unit")
	t.Setenv("TEST_ACCESS", "test")
	t.Setenv("TEST_SECRET", "test")
	t.Setenv("TEST_ROUTER_SERVICE", "test")
	t.Setenv("TEST_FORMS_SERVICE", "test")
	t.Setenv("TEST_CONTENT_SERVICE", "test")
	out.Reset()
	stderr.Reset()
	if code := Run([]string{"--check-config", path}, &out, &stderr); code != 0 {
		t.Fatalf("check failed: %q", stderr.String())
	}
}
