package appconfig

import (
	"strings"
	"testing"

	"github.com/Zubrium/cdn/core/access"
)

func TestLoadRequiresExplicitCoreConfiguration(t *testing.T) {
	valid := "core:\n  database_url_env: DB_URL\n  migration_database_url_env: MIGRATION_DB_URL\n  access_groups: {}\n  s3_endpoint: http://localhost:9000\n  s3_region: local\n  s3_bucket: test\n  s3_access_key_env: S3_KEY\n  s3_secret_key_env: S3_SECRET\n  s3_allow_insecure_http: true\n"
	if _, err := Load(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(strings.NewReader("core:\n  unexpected: true\n")); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := Load(strings.NewReader("core:\n  database_url_env: DB_URL\n")); err == nil {
		t.Fatal("incomplete config accepted")
	}
	if _, err := Load(strings.NewReader(valid + "---\ncore: {}\n")); err == nil {
		t.Fatal("multiple documents accepted")
	}
}

func TestResolveUsesExplicitEnvironmentAndPoolSettings(t *testing.T) {
	config := Config{Core: Core{DatabaseURLEnv: "DB", MigrationDatabaseURLEnv: "MIGRATION_DB", S3AccessKeyEnv: "ACCESS", S3SecretKeyEnv: "SECRET", S3Endpoint: "http://localhost:9000", S3Region: "local", S3Bucket: "test", AtlasBinary: "atlas", MigrationLockTimeout: "30s", AccessGroups: map[string]access.Level{"content": access.LevelWrite}, CacheMaxBytes: 1 << 20, CacheTTL: "5m", MaxMetadataBytes: 1 << 16, MaxObjectBytes: 1024, MaxCachedObjectBytes: 512, MaintenanceInterval: "1m", MaintenanceTimeout: "30s", PendingTTL: "1h", MaintenanceBatchSize: 100, MinConnections: 0, MaxConnections: 4, ConnectTimeout: "2s", MaxLifetime: "1h", MaxIdleTime: "5m", HealthInterval: "30s"}}
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	resolved, err := config.Resolve(func(key string) string { return values[key] })
	if err != nil || resolved.Database.URL != values["DB"] || resolved.S3.SecretKey != values["SECRET"] {
		t.Fatalf("resolve: %v", err)
	}
	delete(values, "SECRET")
	if _, err := config.Resolve(func(key string) string { return values[key] }); err == nil {
		t.Fatal("missing secret accepted")
	}
	config.Core.ConnectTimeout = "0s"
	if _, err := config.Resolve(func(key string) string { return values[key] }); err == nil {
		t.Fatal("zero timeout accepted")
	}
	config.Core.ConnectTimeout = "2s"
	config.Core.MaxObjectBytes = config.Core.CacheMaxBytes + 2
	config.Core.MaxCachedObjectBytes = config.Core.CacheMaxBytes + 1
	values["SECRET"] = "secret"
	if _, err := config.Resolve(func(key string) string { return values[key] }); err == nil {
		t.Fatal("cache entry larger than the total cache memory budget accepted")
	}
}

func TestResolveRejectsSharedRuntimeAndMigrationDatabaseUser(t *testing.T) {
	config := Config{Core: Core{
		DatabaseURLEnv: "DB", MigrationDatabaseURLEnv: "MIGRATION_DB", S3AccessKeyEnv: "ACCESS", S3SecretKeyEnv: "SECRET",
		S3Endpoint: "https://s3.example.test", S3Region: "local", S3Bucket: "test", AtlasBinary: "atlas",
		MigrationLockTimeout: "30s", AccessGroups: map[string]access.Level{}, CacheMaxBytes: 1 << 20, CacheTTL: "5m",
		MaxMetadataBytes: 1 << 16, MaxObjectBytes: 1024, MaxCachedObjectBytes: 512, MaintenanceInterval: "1m",
		MaintenanceTimeout: "30s", PendingTTL: "1h", MaintenanceBatchSize: 100, MinConnections: 0, MaxConnections: 4,
		ConnectTimeout: "2s", MaxLifetime: "1h", MaxIdleTime: "5m", HealthInterval: "30s",
	}}
	values := map[string]string{
		"DB":           "postgres://same:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://same:other-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	if _, err := config.Resolve(func(key string) string { return values[key] }); err == nil {
		t.Fatal("shared runtime/migration user accepted")
	}
}

func TestLoadRejectsUnencryptedNonLoopbackS3(t *testing.T) {
	config := "core:\n  database_url_env: DB_URL\n  migration_database_url_env: MIGRATION_DB_URL\n  s3_endpoint: http://minio.internal:9000\n  s3_region: local\n  s3_bucket: test\n  s3_access_key_env: S3_KEY\n  s3_secret_key_env: S3_SECRET\n  s3_allow_insecure_http: true\n"
	if _, err := Load(strings.NewReader(config)); err == nil {
		t.Fatal("unencrypted remote S3 endpoint accepted")
	}
}
