package appconfig

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/coordinator"
	"github.com/Zubrium/cdn/core/access"
	loaderapp "github.com/Zubrium/cdn/engines/loader/features/files/application"
)

const validCoreYAML = `core:
  database_url_env: DB_URL
  migration_database_url_env: MIGRATION_DB_URL
  access_groups: {}
  s3_endpoint: http://localhost:9000
  s3_region: local
  s3_bucket: test
  s3_access_key_env: S3_KEY
  s3_secret_key_env: S3_SECRET
  s3_allow_insecure_http: true
  module_data:
    max_payload_bytes: 1048576
    max_index_bytes: 65536
    max_batch_bytes: 8388608
    max_page_bytes: 16777216
    max_selectors: 16
    max_selector_values: 64
    max_page_size: 100
    max_batch_operations: 64
`

func TestLoadRequiresExplicitCoreConfiguration(t *testing.T) {
	if _, err := Load(strings.NewReader(validCoreYAML)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(strings.NewReader("core:\n  unexpected: true\n")); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := Load(strings.NewReader("core:\n  database_url_env: DB_URL\n")); err == nil {
		t.Fatal("incomplete config accepted")
	}
	if _, err := Load(strings.NewReader(validCoreYAML + "---\ncore: {}\n")); err == nil {
		t.Fatal("multiple documents accepted")
	}
	if _, err := Load(strings.NewReader(validCoreYAML + "\ncore: {}\n")); err == nil {
		t.Fatal("duplicate configuration key accepted")
	}
	if _, err := Load(strings.NewReader("defaults: &defaults {database_url_env: DB_URL}\ncore: *defaults\n")); err == nil {
		t.Fatal("configuration alias accepted")
	}
}

func TestCoordinatorFingerprintIncludesConnectionPolicyAndOmitsCredentials(t *testing.T) {
	cfg := validRuntimeConfig()
	first, err := configFingerprint(cfg,
		"postgres://runtime:first-secret@127.0.0.1:5432/zubrium_core_qa_unit?sslmode=require",
		"postgres://migrator:migration-secret@127.0.0.1:5432/zubrium_core_qa_unit?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := configFingerprint(cfg,
		"postgres://runtime:rotated-secret@127.0.0.1:5432/zubrium_core_qa_unit?sslmode=require",
		"postgres://migrator:rotated-migration-secret@127.0.0.1:5432/zubrium_core_qa_unit?sslmode=require")
	if err != nil || rotated != first {
		t.Fatalf("credential rotation changed the non-secret configuration fingerprint: same=%v err=%v", rotated == first, err)
	}
	weaker, err := configFingerprint(cfg,
		"postgres://runtime:rotated-secret@127.0.0.1:5432/zubrium_core_qa_unit?sslmode=disable",
		"postgres://migrator:rotated-migration-secret@127.0.0.1:5432/zubrium_core_qa_unit?sslmode=require")
	if err != nil || weaker == first {
		t.Fatalf("connection security policy was omitted from configuration fingerprint: same=%v err=%v", weaker == first, err)
	}
}

func TestCoordinatorFingerprintNormalizesConfiguredUUIDBindings(t *testing.T) {
	cfg := validRuntimeConfig()
	autoPublish := true
	first := PublicationBinding{SiteID: "a0b1c2d3-e4f5-4678-9abc-def012345678", ContentID: "b0b1c2d3-e4f5-4678-9abc-def012345678",
		RouteID: "c0b1c2d3-e4f5-4678-9abc-def012345678", AutoPublish: &autoPublish}
	second := PublicationBinding{SiteID: "d0b1c2d3-e4f5-4678-9abc-def012345678", ContentID: "e0b1c2d3-e4f5-4678-9abc-def012345678",
		RouteID: "f0b1c2d3-e4f5-4678-9abc-def012345678", AutoPublish: &autoPublish}
	cfg.Coordinator.Bindings = []PublicationBinding{first, second}
	want, err := configFingerprint(cfg, "postgres://runtime@127.0.0.1:5432/core", "postgres://migrator@127.0.0.1:5432/core")
	if err != nil {
		t.Fatal(err)
	}
	first.SiteID, first.ContentID, first.RouteID = strings.ToUpper(first.SiteID), strings.ToUpper(first.ContentID), strings.ToUpper(first.RouteID)
	second.SiteID, second.ContentID, second.RouteID = strings.ToUpper(second.SiteID), strings.ToUpper(second.ContentID), strings.ToUpper(second.RouteID)
	cfg.Coordinator.Bindings = []PublicationBinding{second, first}
	got, err := configFingerprint(cfg, "postgres://runtime@127.0.0.1:5432/core", "postgres://migrator@127.0.0.1:5432/core")
	if err != nil || got != want {
		t.Fatalf("equivalent route bindings changed the instance fingerprint: equal=%v err=%v", got == want, err)
	}
}

func TestResolveUsesExplicitEnvironmentAndPoolSettings(t *testing.T) {
	config := validRuntimeConfig()
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	resolved, err := config.Resolve(func(key string) string { return values[key] })
	if err != nil || resolved.Core.Database.URL != values["DB"] || resolved.Core.S3.SecretKey != values["SECRET"] {
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

func TestBootstrapGroupLevelDefaultsToWriteUnlessExplicitlySet(t *testing.T) {
	withGroups := strings.Replace(validCoreYAML, "  access_groups: {}\n", "  access_groups:\n    defaulted: null\n    denied: 0\n", 1)
	loaded, err := Load(strings.NewReader(withGroups))
	if err != nil || loaded.Core.AccessGroups["defaulted"] != nil || loaded.Core.AccessGroups["denied"] == nil || *loaded.Core.AccessGroups["denied"] != access.LevelNone {
		t.Fatalf("YAML did not preserve omitted versus explicit bootstrap levels: groups=%+v err=%v", loaded.Core.AccessGroups, err)
	}
	read := access.LevelRead
	denied := access.LevelNone
	jobAccess := access.LevelWrite
	cfg := validRuntimeConfig()
	cfg.Core.AccessGroups = map[string]*access.Level{"defaulted": nil, "readers": &read, "denied": &denied, "coordinator_jobs": &jobAccess}
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	resolved, err := cfg.Resolve(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]access.Level{"defaulted": access.LevelWrite, "readers": access.LevelRead, "denied": access.LevelNone} {
		if got := resolved.Core.AccessGroups[key]; got != want {
			t.Errorf("bootstrap initial access for %q = %d, want %d", key, got, want)
		}
	}
}

func TestResolveRequiresPoolCapacityForCacheListenerAndStagedStreaming(t *testing.T) {
	cfg := validRuntimeConfig()
	cfg.Core.MaxConnections = 2
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	if _, err := cfg.Resolve(func(key string) string { return values[key] }); err == nil {
		t.Fatal("runtime pool too small for the notification listener and simultaneous staging/final upload was accepted")
	}
}

func TestResolveRequiresPoolCapacityForCoordinatorWorkersAndLoaderCommits(t *testing.T) {
	cfg := validRuntimeConfig()
	cfg.Loader.MaxConcurrentCommits = 2
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	resolve := func() error {
		_, err := cfg.Resolve(func(key string) string { return values[key] })
		return err
	}
	if resolve() == nil {
		t.Fatal("pool without capacity for Core and two connections per Loader commit was accepted")
	}
	cfg.Core.MaxConnections = 8
	if resolve() == nil {
		t.Fatal("pool without capacity for worker handlers and lease renewals was accepted")
	}
	cfg.Core.MaxConnections = 9
	if err := resolve(); err != nil {
		t.Fatalf("pool with capacity for Core, two Loader commits, and two workers plus renewals was rejected: %v", err)
	}
}

func TestResolveRequiresBoundedModuleDataBatchBytes(t *testing.T) {
	cfg := validRuntimeConfig()
	cfg.Core.ModuleData.MaxBatchBytes = cfg.Core.ModuleData.MaxPayloadBytes + cfg.Core.ModuleData.MaxIndexBytes - 1
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	if _, err := cfg.Resolve(func(key string) string { return values[key] }); err == nil {
		t.Fatal("module data batch budget smaller than a maximum record was accepted")
	}
}

func TestResolveRequiresCapacityForLoaderModuleData(t *testing.T) {
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	resolve := func(cfg Config) error {
		_, err := cfg.Resolve(func(key string) string { return values[key] })
		return err
	}

	cfg := validRuntimeConfig()
	cfg.Core.ModuleData.MaxIndexBytes = loaderapp.MinimumModuleDataIndexBytes - 1
	if resolve(cfg) == nil {
		t.Fatal("Loader index limit below its registered projection size was accepted")
	}

	cfg = validRuntimeConfig()
	cfg.Core.ModuleData.MaxIndexBytes = loaderapp.MinimumModuleDataIndexBytes
	cfg.Core.ModuleData.MaxBatchBytes = cfg.Core.ModuleData.MaxPayloadBytes + cfg.Core.ModuleData.MaxIndexBytes
	if resolve(cfg) == nil {
		t.Fatal("Loader batch budget without room for its auxiliary records was accepted")
	}
}

func TestResolveRejectsSharedRuntimeAndMigrationDatabaseUser(t *testing.T) {
	config := validRuntimeConfig()
	values := map[string]string{
		"DB":           "postgres://same:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://same:other-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	if _, err := config.Resolve(func(key string) string { return values[key] }); err == nil {
		t.Fatal("shared runtime/migration user accepted")
	}
}

func validRuntimeConfig() Config {
	groupWrite := access.LevelWrite
	return Config{
		Core: Core{DatabaseURLEnv: "DB", MigrationDatabaseURLEnv: "MIGRATION_DB", S3AccessKeyEnv: "ACCESS", S3SecretKeyEnv: "SECRET",
			S3Endpoint: "http://localhost:9000", S3Region: "local", S3Bucket: "test", S3AllowInsecureHTTP: true,
			AtlasBinary: "atlas", MigrationLockTimeout: "30s", AccessGroups: map[string]*access.Level{"content": &groupWrite, "coordinator_jobs": &groupWrite},
			ModuleData:    ModuleData{MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20, MaxPageBytes: 16 << 20, MaxSelectors: 16, MaxSelectorValues: 64, MaxPageSize: 100, MaxBatchOperations: 64},
			CacheMaxBytes: 1 << 20, CacheTTL: "5m", MaxMetadataBytes: 1 << 16, MaxObjectBytes: 1 << 20, MaxCachedObjectBytes: 512,
			MaintenanceInterval: "1m", MaintenanceTimeout: "30s", PendingTTL: "1h", MaintenanceBatchSize: 100,
			MinConnections: 0, MaxConnections: 7, ConnectTimeout: "2s", MaxLifetime: "1h", MaxIdleTime: "5m", HealthInterval: "30s"},
		Loader: Loader{PartSize: 64 << 10, MaxFileBytes: 1 << 20, MaxPartBytes: 64 << 10, MaxParts: 16, MaxConcurrentCommits: 1, SessionTTL: "1h", CommitTimeout: "1m", Mode: "auto",
			BufferThreshold: 64 << 10, MaxBufferedBytes: 128 << 10, SessionCacheEntries: 100, SessionCacheMaxBytes: 1 << 20,
			SessionCacheTTL: "1m", CleanupInterval: "1m", CleanupBatchSize: 100},
		FileAPI: FileAPI{MaxJSONBytes: 1 << 20, MaxAttributeBytes: 1 << 15, MaxGroupsPerFile: 8, DefaultPageSize: 25, MaxPageSize: 100},
		HTTP:    HTTP{ListenAddress: "127.0.0.1:8080", ReadHeaderTimeout: "5s", ReadTimeout: "5m", WriteTimeout: "0s", IdleTimeout: "1m", ShutdownTimeout: "15s", MaxHeaderBytes: 1 << 16},
		Coordinator: CoordinatorConfig{SystemID: "test-system", JobAccessGroup: "coordinator_jobs", JobsPageDefault: 25, JobsPageMaximum: 100,
			InstanceLease: "30s", HeartbeatInterval: "5s", WorkerConcurrency: 2,
			JobLease: "20s", TaskTimeout: "15m", PollInterval: "1s", RetryBase: "2s", RetryMaximum: "1m", MaxAttempts: 3, Bindings: []PublicationBinding{}},
	}
}

func TestCoordinatorAdminAPIRequiresConfiguredJobAccessGroup(t *testing.T) {
	plan := coordinator.BuildPlan{Components: []string{"core", "coordinator", "engine.loader", "admin.files", "admin.coordinator"}}
	resolved := Resolved{CoordinatorFingerprint: strings.Repeat("a", 64), Coordinator: CoordinatorSettings{
		Bindings: []coordinator.PublicationBinding{}, JobsPageDefault: 25, JobsPageMaximum: 100,
	}}
	if err := resolved.ValidateBuildPlan(plan); !errors.Is(err, ErrCoordinatorProfileMismatch) {
		t.Fatalf("Coordinator API without an explicit group was accepted: %v", err)
	}
	resolved.Coordinator.JobAccessGroup = "coordinator_jobs"
	resolved.Core.AccessGroups = map[string]access.Level{"coordinator_jobs": access.LevelNone}
	if err := resolved.ValidateBuildPlan(plan); err != nil {
		t.Fatalf("explicitly configured zero-access group should be valid for runtime authorization: %v", err)
	}
	delete(resolved.Core.AccessGroups, "coordinator_jobs")
	if err := resolved.ValidateBuildPlan(plan); !errors.Is(err, ErrCoordinatorProfileMismatch) {
		t.Fatalf("Coordinator API group missing from Core config was accepted: %v", err)
	}
}

func TestLoadRejectsUnencryptedNonLoopbackS3(t *testing.T) {
	config := "core:\n  database_url_env: DB_URL\n  migration_database_url_env: MIGRATION_DB_URL\n  s3_endpoint: http://minio.internal:9000\n  s3_region: local\n  s3_bucket: test\n  s3_access_key_env: S3_KEY\n  s3_secret_key_env: S3_SECRET\n  s3_allow_insecure_http: true\n"
	if _, err := Load(strings.NewReader(config)); err == nil {
		t.Fatal("unencrypted remote S3 endpoint accepted")
	}
}
