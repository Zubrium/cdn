package appconfig

import (
	"strings"
	"testing"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
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
  session_idle_ttl: 8h
  session_absolute_ttl: 168h
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

func TestCoordinatorFingerprintAllowsPerBranchRouterProcesses(t *testing.T) {
	firstConfig := validRuntimeConfig()
	firstConfig.RouterAPI.BranchID = "preview"
	firstConfig.RouterAPI.PublicListenAddress = "127.0.0.1:8081"
	first, err := configFingerprint(firstConfig,
		"postgres://runtime:secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"postgres://migrator:secret@127.0.0.1:5432/zubrium_core_qa_unit")
	if err != nil {
		t.Fatal(err)
	}

	secondConfig := firstConfig
	secondConfig.RouterAPI.BranchID = "production"
	secondConfig.RouterAPI.PublicListenAddress = "127.0.0.1:8082"
	second, err := configFingerprint(secondConfig,
		"postgres://runtime:rotated@127.0.0.1:5432/zubrium_core_qa_unit",
		"postgres://migrator:rotated@127.0.0.1:5432/zubrium_core_qa_unit")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("Router processes serving different statically configured branches must share the system fingerprint")
	}

	secondConfig.RouterAPI.UploadMaxFileBytes++
	changedGlobalConfig, err := configFingerprint(secondConfig,
		"postgres://runtime:secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"postgres://migrator:secret@127.0.0.1:5432/zubrium_core_qa_unit")
	if err != nil {
		t.Fatal(err)
	}
	if changedGlobalConfig == first {
		t.Fatal("shared Router security limits must remain part of the system fingerprint")
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
	if resolved.AdminAPI.DefaultPageSize != 50 || resolved.AdminAPI.MaxPageSize != 200 {
		t.Fatalf("Admin API pagination settings = (%d, %d), want (50, 200)", resolved.AdminAPI.DefaultPageSize, resolved.AdminAPI.MaxPageSize)
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

func TestResolveRejectsInvalidAdminPageLimits(t *testing.T) {
	getenv := func(key string) string {
		return map[string]string{
			"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"ACCESS":       "access", "SECRET": "secret",
		}[key]
	}
	for _, limits := range [][2]int{{0, 200}, {50, 49}, {50, 501}, {501, 501}} {
		config := validRuntimeConfig()
		config.AdminAPI.DefaultPageSize, config.AdminAPI.MaxPageSize = limits[0], limits[1]
		if _, err := config.Resolve(getenv); err == nil {
			t.Fatalf("invalid Admin API page limits (%d, %d) were accepted", limits[0], limits[1])
		}
	}
}

func TestResolveRejectsInvalidRouterRequestAndPageLimits(t *testing.T) {
	getenv := func(key string) string {
		return map[string]string{
			"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"ACCESS":       "access", "SECRET": "secret",
		}[key]
	}
	for _, limits := range [][2]int{{0, 100}, {25, 24}, {25, 101}, {101, 101}} {
		config := validRuntimeConfig()
		config.RouterAPI.DefaultPageSize, config.RouterAPI.MaxPageSize = limits[0], limits[1]
		if _, err := config.Resolve(getenv); err == nil {
			t.Fatalf("invalid Router pagination limits accepted: %v", limits)
		}
	}
	config := validRuntimeConfig()
	config.RouterAPI.MaxJSONBytes = 0
	if _, err := config.Resolve(getenv); err == nil {
		t.Fatal("missing Router request-size limit accepted")
	}
}

func TestResolveRequiresExplicitOrderedSessionLifetimes(t *testing.T) {
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	getenv := func(key string) string { return values[key] }
	for _, tc := range []struct {
		name     string
		idle     string
		absolute string
	}{
		{name: "missing idle lifetime", idle: "", absolute: "168h"},
		{name: "idle exceeds absolute", idle: "8h", absolute: "7h"},
		{name: "absolute exceeds supported maximum", idle: "8h", absolute: "8761h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := validRuntimeConfig()
			config.Core.SessionIdleTTL = tc.idle
			config.Core.SessionAbsoluteTTL = tc.absolute
			if _, err := config.Resolve(getenv); err == nil {
				t.Fatal("invalid session lifetime configuration was accepted")
			}
		})
	}
	config := validRuntimeConfig()
	resolved, err := config.Resolve(getenv)
	if err != nil || resolved.Core.SessionIdleTTL != 8*time.Hour || resolved.Core.SessionAbsoluteTTL != 7*24*time.Hour {
		t.Fatalf("resolve session lifetimes: idle=%s absolute=%s err=%v", resolved.Core.SessionIdleTTL, resolved.Core.SessionAbsoluteTTL, err)
	}
}

func TestResolveRequiresExactUniqueAdminOrigins(t *testing.T) {
	getenv := func(key string) string {
		return map[string]string{
			"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"ACCESS":       "access", "SECRET": "secret",
		}[key]
	}
	for _, origins := range [][]string{
		nil,
		{"https://admin.example.com/path"},
		{"https://user@admin.example.com"},
		{"https://admin.example.com?mode=1"},
		{"https://admin.example.com:70000"},
		{"https://admin.example.com:443"},
		{"https://ADMIN.example.com"},
		{"https://admin.example.com:"},
		{"https://*.example.com"},
		{"https://admin.example.com", "https://admin.example.com"},
	} {
		config := validRuntimeConfig()
		config.AdminAPI.AllowedOrigins = origins
		if _, err := config.Resolve(getenv); err == nil {
			t.Fatalf("invalid Admin API origins were accepted: %#v", origins)
		}
	}
	config := validRuntimeConfig()
	config.AdminAPI.AllowedOrigins = []string{"https://admin.example.com", "http://127.0.0.1:8080"}
	if _, err := config.Resolve(getenv); err != nil {
		t.Fatalf("valid exact origins were rejected: %v", err)
	}
}

func TestBootstrapGroupLevelDefaultsToWriteUnlessExplicitlySet(t *testing.T) {
	withGroups := strings.Replace(validCoreYAML, "  access_groups: {}\n", "  access_groups:\n    defaulted: null\n    denied: 0\n", 1)
	loaded, err := Load(strings.NewReader(withGroups))
	if err != nil || loaded.Core.AccessGroups["defaulted"] != nil || loaded.Core.AccessGroups["denied"] == nil || *loaded.Core.AccessGroups["denied"] != platform.AccessLevelNone {
		t.Fatalf("YAML did not preserve omitted versus explicit bootstrap levels: groups=%+v err=%v", loaded.Core.AccessGroups, err)
	}
	read := platform.AccessLevelRead
	denied := platform.AccessLevelNone
	jobAccess := platform.AccessLevelWrite
	cfg := validRuntimeConfig()
	cfg.Core.AccessGroups = map[string]*platform.AccessLevel{"defaulted": nil, "readers": &read, "denied": &denied, "coordinator_jobs": &jobAccess}
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	resolved, err := cfg.Resolve(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]platform.AccessLevel{"defaulted": platform.AccessLevelWrite, "readers": platform.AccessLevelRead, "denied": platform.AccessLevelNone} {
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

func TestWebsiteBuildRequiresExplicitStaticSitesOutsideFixtures(t *testing.T) {
	production := platform.BuildPlan{Components: []string{"engine.website"}}
	if err := (Resolved{}).ValidateBuildPlan(production); err == nil {
		t.Fatal("Website production build accepted no configured sites")
	}
	configured := Resolved{Website: WebsiteSettings{Sites: []string{"main-site"}}}
	if err := configured.ValidateBuildPlan(production); err != nil {
		t.Fatalf("Website production build rejected configured sites: %v", err)
	}
	fixtures := platform.BuildPlan{Components: []string{"engine.website", "development.fixtures"}}
	if err := (Resolved{}).ValidateBuildPlan(fixtures); err != nil {
		t.Fatalf("development fixtures unexpectedly require production sites: %v", err)
	}
	for _, sites := range [][]string{{"bad/site"}, {"duplicate", "duplicate"}, make([]string, 257)} {
		if validWebsiteSiteIDs(sites) {
			t.Fatalf("invalid Website site IDs accepted: count=%d", len(sites))
		}
		if err := (Resolved{Website: WebsiteSettings{Sites: sites}}).ValidateBuildPlan(production); err == nil {
			t.Fatalf("production build accepted invalid Website site IDs: count=%d", len(sites))
		}
	}
}

func TestFormsBuildRequiresExplicitCSVExportBounds(t *testing.T) {
	plan := platform.BuildPlan{Components: []string{"engine.forms", "admin.forms", "development.fixtures"}}
	valid := Resolved{FormsAPI: FormsAPISettings{ExportMaxConcurrent: 2, ExportMaxRows: 100, ExportMaxBytes: 1 << 20}}
	if err := valid.ValidateBuildPlan(plan); err != nil {
		t.Fatalf("valid Forms export limits rejected: %v", err)
	}
	for _, forms := range []FormsAPISettings{
		{},
		{ExportMaxConcurrent: 2, ExportMaxRows: 10001, ExportMaxBytes: 1 << 20},
		{ExportMaxConcurrent: 2, ExportMaxRows: 100, ExportMaxBytes: 1023},
		{ExportMaxConcurrent: 2, ExportMaxRows: 100, ExportMaxBytes: 65 << 20},
		{ExportMaxConcurrent: 17, ExportMaxRows: 100, ExportMaxBytes: 1 << 20},
	} {
		if err := (Resolved{FormsAPI: forms}).ValidateBuildPlan(plan); err == nil {
			t.Fatalf("invalid Forms export limits were accepted: %+v", forms)
		}
	}
}

func validRuntimeConfig() Config {
	groupWrite := platform.AccessLevelWrite
	secureCookie := false
	return Config{
		Core: Core{DatabaseURLEnv: "DB", MigrationDatabaseURLEnv: "MIGRATION_DB", S3AccessKeyEnv: "ACCESS", S3SecretKeyEnv: "SECRET",
			S3Endpoint: "http://localhost:9000", S3Region: "local", S3Bucket: "test", S3AllowInsecureHTTP: true,
			AtlasBinary: "atlas", MigrationLockTimeout: "30s", AccessGroups: map[string]*platform.AccessLevel{"content": &groupWrite, "coordinator_jobs": &groupWrite},
			ModuleData:    ModuleData{MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20, MaxPageBytes: 16 << 20, MaxSelectors: 16, MaxSelectorValues: 64, MaxPageSize: 100, MaxBatchOperations: 64},
			CacheMaxBytes: 1 << 20, CacheTTL: "5m", MaxMetadataBytes: 1 << 16, MaxObjectBytes: 1 << 20, MaxCachedObjectBytes: 512,
			MaintenanceInterval: "1m", MaintenanceTimeout: "30s", PendingTTL: "1h", MaintenanceBatchSize: 100,
			SessionIdleTTL: "8h", SessionAbsoluteTTL: "168h",
			MinConnections: 0, MaxConnections: 7, ConnectTimeout: "2s", MaxLifetime: "1h", MaxIdleTime: "5m", HealthInterval: "30s"},
		Loader: Loader{PartSize: 64 << 10, MaxFileBytes: 1 << 20, MaxPartBytes: 64 << 10, MaxParts: 16, MaxConcurrentCommits: 1, SessionTTL: "1h", CommitTimeout: "1m", Mode: "auto",
			BufferThreshold: 64 << 10, MaxBufferedBytes: 128 << 10, SessionCacheEntries: 100, SessionCacheMaxBytes: 1 << 20,
			SessionCacheTTL: "1m", CleanupInterval: "1m", CleanupBatchSize: 100},
		FileAPI:   FileAPI{MaxJSONBytes: 1 << 20, MaxAttributeBytes: 1 << 15, MaxGroupsPerFile: 8, DefaultPageSize: 25, MaxPageSize: 100},
		AdminAPI:  AdminAPI{MaxJSONBytes: 1 << 20, DefaultPageSize: 50, MaxPageSize: 200, SessionCookieSecure: &secureCookie, AllowedOrigins: []string{"http://127.0.0.1:8080"}},
		RouterAPI: RouterAPI{MaxJSONBytes: 1 << 20, DefaultPageSize: 25, MaxPageSize: 100},
		HTTP:      HTTP{ListenAddress: "127.0.0.1:8080", ReadHeaderTimeout: "5s", ReadTimeout: "5m", WriteTimeout: "0s", IdleTimeout: "1m", ShutdownTimeout: "15s", MaxHeaderBytes: 1 << 16},
		Coordinator: CoordinatorConfig{JobAccessGroup: "coordinator_jobs", JobsPageDefault: 25, JobsPageMaximum: 100,
			InstanceLease: "30s", HeartbeatInterval: "5s", WorkerConcurrency: 2,
			JobLease: "20s", TaskTimeout: "15m", PollInterval: "1s", RetryBase: "2s", RetryMaximum: "1m",
			MaxAttempts: 3, MaxWorkflowTasks: 1000, MaxChildrenPerTask: 16, WorkflowRetention: "48h"},
	}
}

func TestLoadRejectsUnencryptedNonLoopbackS3(t *testing.T) {
	config := "core:\n  database_url_env: DB_URL\n  migration_database_url_env: MIGRATION_DB_URL\n  s3_endpoint: http://minio.internal:9000\n  s3_region: local\n  s3_bucket: test\n  s3_access_key_env: S3_KEY\n  s3_secret_key_env: S3_SECRET\n  s3_allow_insecure_http: true\n"
	if _, err := Load(strings.NewReader(config)); err == nil {
		t.Fatal("unencrypted remote S3 endpoint accepted")
	}
}
