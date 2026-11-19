package postgres

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAtlasMigrationDirectoryHasValidChecksum(t *testing.T) {
	dir, err := loadMigrationDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Validate(dir); err != nil {
		t.Fatalf("Atlas migration directory is invalid: %v", err)
	}
}

func TestAtlasVersionMustMatchPinnedVersion(t *testing.T) {
	for _, output := range []string{"atlas version v1.3.0", "atlas community version v1.3.0\nhttps://example.invalid"} {
		if !isPinnedAtlasVersion(output) {
			t.Errorf("rejected pinned Atlas version output %q", output)
		}
	}
	for _, output := range []string{"atlas version v1.3.0-beta", "atlas version v1.30.0", "other version v1.3.0", "atlas community version v1.3.0.1"} {
		if isPinnedAtlasVersion(output) {
			t.Errorf("accepted unpinned Atlas output %q", output)
		}
	}
}

func TestAtlasCommandOutputIsSafeForMigrationErrors(t *testing.T) {
	output := `atlas: failed postgres://core_app:db-secret@127.0.0.1:5432/qa?sslmode=disable password=second-secret`
	detail := atlasErrorDetails(output)
	for _, secret := range []string{"db-secret", "second-secret", "core_app:"} {
		if strings.Contains(detail, secret) {
			t.Fatalf("Atlas diagnostic leaked %q: %q", secret, detail)
		}
	}
	if !strings.Contains(detail, "postgres://[redacted]") || !strings.Contains(detail, "password=[redacted]") {
		t.Fatalf("Atlas diagnostic did not retain a useful, redacted cause: %q", detail)
	}

	long := strings.Repeat("x", 4096)
	if got := atlasErrorDetails(long); len(got) > 2048 {
		t.Fatalf("Atlas diagnostic was not bounded: %d bytes", len(got))
	}
	if got := atlasErrorDetails(" \n\t "); got != "" {
		t.Fatalf("empty Atlas output produced a diagnostic: %q", got)
	}
}

func TestAtlasEnvironmentKeepsDatabaseAndMigrationDirectorySeparate(t *testing.T) {
	config := atlasEnvironmentConfig{
		CoreDatabaseURL:      "postgres://runtime:secret@127.0.0.1/core",
		MigrationDatabaseURL: "postgres://migrator:secret@127.0.0.1/core",
		MigrationDir:         "file:///tmp/core-migrations",
	}
	values := make(map[string]string)
	for _, variable := range atlasEnvironment(config) {
		key, value, ok := strings.Cut(variable, "=")
		if ok {
			values[key] = value
		}
	}
	if values["ZUBRIUM_CORE_DATABASE_URL"] != config.CoreDatabaseURL {
		t.Fatalf("Core URL environment value = %q", values["ZUBRIUM_CORE_DATABASE_URL"])
	}
	if values["ZUBRIUM_CORE_MIGRATION_DATABASE_URL"] != config.MigrationDatabaseURL {
		t.Fatalf("migration URL environment value = %q", values["ZUBRIUM_CORE_MIGRATION_DATABASE_URL"])
	}
	if values["ZUBRIUM_CORE_MIGRATION_DIR"] != config.MigrationDir {
		t.Fatalf("migration directory environment value = %q", values["ZUBRIUM_CORE_MIGRATION_DIR"])
	}
}

func TestCoreSchemaRelationInventoryIncludesEngineRevisionSequence(t *testing.T) {
	if !containsString(coreSchemaRelations, "engine_data.revision_seq") {
		t.Fatal("Core schema relation allowlist omits the engine-record revision sequence")
	}
	if containsString(coreSchemaTables, "engine_data.revision_seq") {
		t.Fatal("sequence was counted as an application table in the clean schema check")
	}
	for _, schema := range coreSchemas {
		if schema == "core" {
			t.Fatal("retired core schema remains in the target schema inventory")
		}
	}
}

func TestCoreMigrationURLIsScopedToPublicHistorySchema(t *testing.T) {
	input := "postgres://runtime:secret@db.example:5432/zubrium?sslmode=verify-full&application_name=core"
	got, err := atlasURLForSchema(input, "public")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("search_path") != "public" {
		t.Fatalf("Atlas migration URL is not restricted to the history schema: %q", parsed.Query().Get("search_path"))
	}
	if parsed.Query().Get("sslmode") != "verify-full" || parsed.Query().Get("application_name") != "core" {
		t.Fatalf("scoping the Atlas URL discarded existing query options: %q", parsed.RawQuery)
	}
	if parsed.User.String() != "runtime:secret" || parsed.Host != "db.example:5432" || parsed.Path != "/zubrium" {
		t.Fatalf("scoping the Atlas URL changed connection identity: %s", parsed.Redacted())
	}
}

func TestFreshMigrationDefinesBoundedContextSchemas(t *testing.T) {
	schema := initialSchema(t)
	if strings.Contains(strings.ToLower(schema), "alter table") {
		t.Fatal("fresh initial migration contains ALTER TABLE")
	}
	lower := strings.ToLower(schema)
	for _, schemaName := range []string{"access", "files", "uploads", "file_index", "engine_data", "coordination"} {
		if !strings.Contains(lower, "create schema "+schemaName) {
			t.Errorf("fresh Atlas migration does not create schema %s", schemaName)
		}
	}
	for _, required := range []string{
		"create sequence engine_data.revision_seq",
		"create index records_indexes_gin_idx on engine_data.records using gin (index_data jsonb_path_ops)",
		"create index records_expiry_idx on engine_data.records",
		"namespace text not null check (length(namespace) <= 255",
		"grant usage, select on sequence engine_data.revision_seq to zubrium_core_runtime",
	} {
		if !strings.Contains(lower, required) {
			t.Errorf("fresh Core migration is missing %q", required)
		}
	}
	for _, table := range coreSchemaTables {
		if !hasTable(schema, table) {
			t.Errorf("fresh schema is missing table %s", table)
		}
	}
	for _, retired := range []string{
		"core.core_principals", "core.core_credentials", "core.core_bootstrap", "core.core_access_groups",
		"core.core_group_access", "core.core_objects", "core.core_file_groups", "core.core_file_sets",
		"core.core_file_set_members", "core.core_upload_requests", "core.core_staged_objects", "core.module_data",
		"coordination.publications", "coordination.route_targets",
	} {
		if hasTable(schema, retired) {
			t.Errorf("fresh schema contains retired table %s", retired)
		}
	}
	for table, fields := range map[string][]string{
		"access.users":              {"id", "name", "active", "can_manage_administrators"},
		"access.credentials":        {"id", "user_id", "secret_hash", "expires_at", "revoked_at"},
		"access.groups":             {"group_key", "active"},
		"access.grants":             {"user_id", "group_key", "access_level"},
		"files.objects":             {"id", "object_key", "metadata", "content_type", "size_bytes", "sha256", "revision", "updated_at"},
		"files.memberships":         {"object_id", "group_key"},
		"file_index.sets":           {"id", "namespace", "name", "management_group", "revision", "updated_at"},
		"file_index.members":        {"set_id", "object_id"},
		"uploads.requests":          {"user_id", "request_key", "request_hash", "state", "updated_at"},
		"uploads.staged":            {"id", "object_key", "user_id", "group_keys", "size_bytes", "sha256", "state", "updated_at"},
		"engine_data.records":       {"engine_id", "namespace", "scope_type", "scope_id", "record_key", "payload", "payload_version", "index_data", "revision", "expires_at"},
		"coordination.runtime":      {"singleton", "fingerprint", "epoch"},
		"coordination.instances":    {"instance_id", "config_epoch", "lease_until"},
		"coordination.workflows":    {"workflow_id", "root_task_id", "owner_engine_id", "idempotency_key", "input_hash", "state", "result"},
		"coordination.tasks":        {"task_id", "workflow_id", "component_id", "operation_id", "payload", "result", "state", "fencing_token"},
		"coordination.dependencies": {"workflow_id", "task_id", "depends_on"},
	} {
		definition := tableDefinition(t, schema, table)
		for _, field := range fields {
			if !hasColumn(definition, field) {
				t.Errorf("%s is missing column %s", table, field)
			}
		}
	}
	for table, fields := range map[string][]string{
		"access.users":           {"kind", "type", "role", "roles"},
		"access.credentials":     {"kind", "type"},
		"access.groups":          {"owner_id", "name"},
		"files.objects":          {"owner_id", "group_id", "public", "deleted_at", "version_id", "name"},
		"files.memberships":      {"owner_id", "public"},
		"file_index.sets":        {"created_by", "owner_id", "group_id", "public"},
		"uploads.requests":       {"owner_id"},
		"uploads.staged":         {"owner_id", "session_id", "part_number"},
		"engine_data.records":    {"module_id"},
		"coordination.runtime":   {"system_id"},
		"coordination.instances": {"system_id"},
		"coordination.tasks":     {"system_id", "job_type"},
	} {
		definition := tableDefinition(t, schema, table)
		for _, field := range fields {
			if hasColumn(definition, field) {
				t.Errorf("%s contains retired column %s", table, field)
			}
		}
	}
}

func TestFreshBaselineOmitsUnusedOperationalTimestamps(t *testing.T) {
	schema := initialSchema(t)
	for table, fields := range map[string][]string{
		"access.groups":          {"created_at"},
		"files.objects":          {"created_at"},
		"uploads.requests":       {"created_at"},
		"uploads.staged":         {"created_at"},
		"file_index.members":     {"added_at"},
		"coordination.runtime":   {"updated_at"},
		"coordination.instances": {"heartbeat_at"},
	} {
		definition := tableDefinition(t, schema, table)
		for _, field := range fields {
			if hasColumn(definition, field) {
				t.Errorf("%s contains unconsumed operational timestamp %s", table, field)
			}
		}
	}
	lower := strings.ToLower(schema)
	if !strings.Contains(lower, "create index objects_pending_idx on files.objects (updated_at, id)") {
		t.Fatal("pending object recovery index must use the updated_at cursor used by recovery queries")
	}
}

func TestCoreGoCodeDoesNotExecuteSchemaDDL(t *testing.T) {
	root := repositoryRoot(t)
	ddl := regexp.MustCompile(`(?is)\b(create|alter|drop|truncate)\s+(?:or\s+replace\s+)?(?:(?:temporary|temp|unlogged)\s+)?(table|index|schema|database|role|type|domain|extension|sequence|view|materialized\s+view|trigger|function|policy|tablespace|publication|subscription)\b`)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if ddl.Match(source) {
			t.Errorf("Go source %s contains schema DDL; keep it in Atlas migrations", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.work")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("cannot locate repository root")
		}
		current = parent
	}
}

func TestDisposableMigratedSchemaHasNoRetiredModels(t *testing.T) {
	pool := openDisposablePool(t, "TEST_CORE_SCHEMA_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatalf("second startup rejected the already migrated Core schema: %v", err)
	}
	for _, schema := range coreSchemas {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, schema).Scan(&exists); err != nil || !exists {
			t.Errorf("target schema %s missing after Atlas migration: %v", schema, err)
		}
	}
	for _, relation := range coreSchemaTables {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, relation).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("target table %s is missing after Atlas migration", relation)
		}
	}
	if !containsString(coreSchemaRelations, "engine_data.revision_seq") {
		t.Fatal("engine revision sequence missing from allowed relation inventory")
	}
	var oldSchemaExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='core')`).Scan(&oldSchemaExists); err != nil {
		t.Fatal(err)
	}
	if oldSchemaExists {
		t.Fatal("retired core schema was created by the target baseline")
	}
	for _, relation := range []string{"coordination.publications", "coordination.route_targets", "core.module_data", "engine_data.module_data"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, relation).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("retired relation %s still exists", relation)
		}
	}
	for _, field := range []struct{ schema, table, column string }{
		{"access", "users", "kind"},
		{"files", "objects", "owner_id"}, {"files", "objects", "public"}, {"files", "objects", "deleted_at"},
		{"access", "groups", "owner_id"}, {"access", "grants", "role_id"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3)`, field.schema, field.table, field.column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("retired column %s.%s.%s still exists", field.schema, field.table, field.column)
		}
	}
}

func TestRuntimeRejectsUnexpectedDatabaseRoleMembership(t *testing.T) {
	const runtimeEnv = "TEST_CORE_RUNTIME_ROLE_DATABASE_URL"
	const migrationEnv = "TEST_CORE_RUNTIME_ROLE_MIGRATION_DATABASE_URL"
	runtimeDSN := os.Getenv(runtimeEnv)
	migrationDSN := os.Getenv(migrationEnv)
	if runtimeDSN == "" && migrationDSN == "" {
		t.Skip("disposable elevated-role Core database is not configured")
	}
	if runtimeDSN == "" || migrationDSN == "" {
		t.Fatalf("configure both %s and %s", runtimeEnv, migrationEnv)
	}
	runtimePool := openDisposablePool(t, runtimeEnv)
	runtimeConfig, err := pgxpool.ParseConfig(runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	migrationConfig, err := pgxpool.ParseConfig(migrationDSN)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeConfig.ConnConfig.Database != migrationConfig.ConnConfig.Database || runtimeConfig.ConnConfig.User == migrationConfig.ConnConfig.User {
		t.Fatal("runtime-role and migration DSNs must use separate users for the same disposable database")
	}
	migrationConfig.MaxConns = 2
	migrationPool, err := pgxpool.NewWithConfig(context.Background(), migrationConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(migrationPool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := Migrate(ctx, migrationPool, testAtlasBinary(), 30*time.Second); err != nil {
		t.Fatalf("apply clean Core Atlas baseline for role-membership test: %v", err)
	}
	if err := ValidateRuntimeAccess(ctx, runtimePool); err == nil {
		t.Fatal("runtime login with an additional settable database role passed privilege validation")
	}
}

func migrateForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) error {
	t.Helper()
	// A configured disposable database makes Atlas a required test dependency;
	// do not silently pass migration acceptance when its executable is absent.
	return Migrate(ctx, pool, testAtlasBinary(), 30*time.Second)
}

func testAtlasBinary() string {
	binary := os.Getenv("TEST_ATLAS_BINARY")
	if binary == "" {
		binary = "atlas"
	}
	return binary
}

func initialSchema(t *testing.T) string {
	t.Helper()
	contents, err := migrationFiles.ReadFile("migrations/20261117000000_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func loadMigrationDir(directory string) (migrate.Dir, error) {
	entries, err := fs.ReadDir(migrationFiles, directory)
	if err != nil {
		return nil, err
	}
	dir := &migrate.MemDir{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		contents, err := migrationFiles.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, err
		}
		if err := dir.WriteFile(entry.Name(), contents); err != nil {
			return nil, err
		}
	}
	return dir, nil
}

func hasTable(schema, table string) bool {
	pattern := `(?i)\bcreate\s+table\s+(?:if\s+not\s+exists\s+)?` + regexp.QuoteMeta(table) + `\s*\(`
	return regexp.MustCompile(pattern).MatchString(schema)
}

func tableDefinition(t *testing.T, schema, table string) string {
	t.Helper()
	pattern := `(?is)\bcreate\s+table\s+(?:if\s+not\s+exists\s+)?` + regexp.QuoteMeta(table) + `\s*\((.*?)\);`
	match := regexp.MustCompile(pattern).FindStringSubmatch(schema)
	if len(match) != 2 {
		t.Fatalf("could not find CREATE TABLE definition for %s", table)
	}
	return match[1]
}

func hasColumn(definition, column string) bool {
	pattern := `(?im)^\s*` + regexp.QuoteMeta(column) + `\s+`
	return regexp.MustCompile(pattern).MatchString(definition)
}

func openDisposablePool(t *testing.T, envName string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(envName)
	if dsn == "" {
		t.Skipf("%s is not configured", envName)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "zubrium_core_qa_") {
		t.Fatalf("refusing non-disposable database from %s", envName)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var other int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()`).Scan(&other); err != nil || other != 0 {
		pool.Close()
		t.Fatalf("database from %s has other sessions: %d: %v", envName, other, err)
	}
	var initialized bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.atlas_schema_revisions') IS NOT NULL OR EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=ANY($1::text[]))`, coreSchemas).Scan(&initialized); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if initialized {
		pool.Close()
		t.Fatalf("refusing database from %s because it already contains a Core schema; use a fresh disposable database", envName)
	}
	t.Cleanup(pool.Close)
	return pool
}
