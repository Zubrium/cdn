package appconfig

import (
	"os"
	"strings"
	"testing"
)

func TestObservabilityExampleResolves(t *testing.T) {
	file, err := os.Open("../../config.observability.yaml.example")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cfg, err := Load(file)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Observability.Mode != "otel" || cfg.Observability.OTLP.Endpoint == "" {
		t.Fatalf("observability section was not loaded: %#v", cfg.Observability)
	}
	env := map[string]string{
		"ZUBRIUM_DATABASE_URL":                  "postgres://obs_admin:pwd@obs-postgres:5432/zubrium_obs?sslmode=disable",
		"ZUBRIUM_MIGRATION_DATABASE_URL":        "postgres://obs_migrator:pwd@obs-postgres:5432/zubrium_obs?sslmode=disable",
		"ZUBRIUM_BOOTSTRAP_SECRET":              "dummy-bootstrap",
		"ZUBRIUM_S3_ACCESS_KEY":                 "dummy-access",
		"ZUBRIUM_S3_SECRET_KEY":                 "dummy-secret",
		"ZUBRIUM_ROUTER_SERVICE_CREDENTIAL":     "dummy-router",
		"ZUBRIUM_FORMS_FILE_SERVICE_CREDENTIAL": "dummy-forms",
		"ZUBRIUM_CONTENT_SERVICE_CREDENTIAL":    "dummy-content",
		"ZUBRIUM_OBS_OTLP_HEADERS":              "X-OBS-STACK=observability",
	}
	resolved, err := cfg.Resolve(func(name string) string { return env[name] })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Observability.Mode != "otel" || resolved.Observability.Endpoint != "http://otel-collector:4318" {
		t.Fatalf("unexpected resolved observability config: %#v", resolved.Observability)
	}
	if len(resolved.Observability.Headers) != 1 || resolved.Observability.Headers["X-OBS-STACK"] != "observability" {
		t.Fatalf("OTLP headers were not resolved from the environment: %#v", resolved.Observability.Headers)
	}
}

func TestInsecureS3HostRequiresExactAllowlist(t *testing.T) {
	base := `core:
  database_url_env: ZUBRIUM_DATABASE_URL
  migration_database_url_env: ZUBRIUM_MIGRATION_DATABASE_URL
  s3_endpoint: http://obs-minio:9000
  s3_region: us-east-1
  s3_bucket: zubrium-private
  s3_access_key_env: ZUBRIUM_S3_ACCESS_KEY
  s3_secret_key_env: ZUBRIUM_S3_SECRET_KEY
  s3_allow_insecure_http: true
`
	if _, err := Load(strings.NewReader(base)); err == nil {
		t.Fatal("non-loopback insecure S3 endpoint was accepted without an allowlist")
	}
	withAllowlist := base + "  s3_insecure_allowed_hosts:\n    - obs-minio\n"
	if _, err := Load(strings.NewReader(withAllowlist)); err != nil {
		t.Fatalf("explicitly allowed host was rejected: %v", err)
	}
}
