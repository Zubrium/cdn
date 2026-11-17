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
	for _, name := range []string{"migrations", "schema-migrations"} {
		dir, err := loadMigrationDir(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrate.Validate(dir); err != nil {
			t.Fatalf("Atlas migration directory %s is invalid: %v", name, err)
		}
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

func TestCoreSchemaRelationAllowlistIncludesRevisionSequence(t *testing.T) {
	if !containsString(coreSchemaRelations, "module_data_revision_seq") {
		t.Fatal("fresh Core schema relation allowlist omits the module-data revision sequence")
	}
	if containsString(coreSchemaTables, "module_data_revision_seq") {
		t.Fatal("sequence was counted as an application table in the clean schema check")
	}
}

func TestCoreMigrationURLIsScopedToCoreSchema(t *testing.T) {
	input := "postgres://runtime:secret@db.example:5432/zubrium?sslmode=verify-full&application_name=core"
	got, err := atlasURLForSchema(input, "core")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("search_path") != "core" {
		t.Fatalf("Atlas migration URL is not restricted to core: %q", parsed.Query().Get("search_path"))
	}
	if parsed.Query().Get("sslmode") != "verify-full" || parsed.Query().Get("application_name") != "core" {
		t.Fatalf("scoping the Atlas URL discarded existing query options: %q", parsed.RawQuery)
	}
	if parsed.User.String() != "runtime:secret" || parsed.Host != "db.example:5432" || parsed.Path != "/zubrium" {
		t.Fatalf("scoping the Atlas URL changed connection identity: %s", parsed.Redacted())
	}
}

func TestFreshMigrationDefinesTheCoreV2Schema(t *testing.T) {
	schema := initialSchema(t)
	bootstrap, err := migrationFiles.ReadFile("schema-migrations/20261116000000_create_core_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(bootstrap)), "create schema if not exists core") {
		t.Fatal("Atlas bootstrap migration does not create the isolated core schema")
	}
	if strings.Contains(strings.ToLower(schema), "alter table") {
		t.Fatal("fresh initial migration contains ALTER TABLE")
	}
	for _, required := range []string{
		"create sequence core.module_data_revision_seq",
		"create index module_data_indexes_gin_idx on core.module_data using gin (index_data jsonb_path_ops)",
		"create index module_data_expiry_idx on core.module_data",
		"namespace text not null check (length(namespace) <= 255",
		"grant usage, select on sequence core.module_data_revision_seq to zubrium_core_runtime",
	} {
		if !strings.Contains(strings.ToLower(schema), required) {
			t.Errorf("fresh Core migration is missing %q", required)
		}
	}
	if !containsString(coreSchemaRelations, "module_data_revision_seq") || containsString(coreSchemaTables, "module_data_revision_seq") {
		t.Fatal("Core revision sequence must be allowed as a relation without being counted as an application table")
	}
	for _, table := range []string{
		"core_principals", "core_credentials", "core_bootstrap", "core_access_groups",
		"core_group_access", "core_objects", "core_file_groups", "core_file_sets",
		"core_file_set_members", "core_upload_requests", "core_staged_objects", "module_data",
	} {
		if !hasTable(schema, table) {
			t.Errorf("fresh schema is missing table %s", table)
		}
	}
	for _, table := range []string{
		"core_roles", "core_role_memberships", "core_grants", "core_admins", "core_services",
		"core_service_accounts", "core_groups", "core_object_versions", "core_file_versions",
		"core_object_references",
	} {
		if hasTable(schema, table) {
			t.Errorf("fresh schema contains retired table %s", table)
		}
	}
	for table, fields := range map[string][]string{
		"core_principals":       {"id", "name", "active", "can_manage_administrators"},
		"core_credentials":      {"id", "principal_id", "secret_hash", "expires_at", "revoked_at"},
		"core_access_groups":    {"group_key", "active"},
		"core_group_access":     {"principal_id", "group_key", "access_level"},
		"core_objects":          {"id", "object_key", "metadata", "content_type", "size_bytes", "sha256", "revision"},
		"core_file_groups":      {"object_id", "group_key"},
		"core_file_sets":        {"id", "created_by", "namespace", "name", "management_group", "revision", "updated_at"},
		"core_file_set_members": {"file_set_id", "object_id"},
		"core_upload_requests":  {"principal_id", "request_key", "request_hash", "state"},
		"core_staged_objects":   {"id", "object_key", "principal_id", "group_keys", "size_bytes", "sha256", "state"},
		"module_data":           {"module_id", "namespace", "scope_type", "scope_id", "record_key", "payload", "payload_version", "index_data", "revision", "expires_at"},
	} {
		definition := tableDefinition(t, schema, table)
		for _, field := range fields {
			if !hasColumn(definition, field) {
				t.Errorf("%s is missing column %s", table, field)
			}
		}
	}
	for table, fields := range map[string][]string{
		"core_principals":      {"kind", "type", "role", "roles"},
		"core_credentials":     {"kind", "type"},
		"core_access_groups":   {"owner_id", "name"},
		"core_objects":         {"owner_id", "group_id", "public", "deleted_at", "version_id", "name"},
		"core_file_groups":     {"owner_id", "public"},
		"core_file_sets":       {"owner_id", "group_id", "public"},
		"core_upload_requests": {"owner_id"}, "core_staged_objects": {"owner_id", "session_id", "part_number"},
	} {
		definition := tableDefinition(t, schema, table)
		for _, field := range fields {
			if hasColumn(definition, field) {
				t.Errorf("%s contains retired column %s", table, field)
			}
		}
	}
}

func TestCoreGoCodeDoesNotExecuteSchemaDDL(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	ddl := regexp.MustCompile(`(?is)\b(create|alter|drop|truncate)\s+(?:or\s+replace\s+)?(?:(?:temporary|temp|unlogged)\s+)?(table|index|schema|database|role|type|domain|extension|sequence|view|materialized\s+view|trigger|function|policy|tablespace|publication|subscription)\b`)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == "legacy" || entry.Name() == ".git") {
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
	for _, name := range []string{
		"core_roles", "core_role_memberships", "core_grants", "core_admins", "core_services",
		"core_service_accounts", "core_groups", "core_object_versions", "core_file_versions",
		"core_object_references",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('core.' || $1) IS NOT NULL`, name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("retired table %s still exists", name)
		}
	}
	for _, field := range []struct{ table, column string }{
		{"core_principals", "kind"},
		{"core_objects", "owner_id"}, {"core_objects", "public"}, {"core_objects", "group_id"}, {"core_objects", "deleted_at"},
		{"core_access_groups", "owner_id"}, {"core_group_access", "role_id"}, {"core_upload_requests", "owner_id"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`, field.table, field.column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("retired column %s.%s still exists", field.table, field.column)
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
	contents, err := migrationFiles.ReadFile("migrations/20261116000000_initial.sql")
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
	pattern := `(?i)\bcreate\s+table\s+(?:core\.)?` + regexp.QuoteMeta(table) + `\s*\(`
	return regexp.MustCompile(pattern).MatchString(schema)
}

func tableDefinition(t *testing.T, schema, table string) string {
	t.Helper()
	pattern := `(?is)\bcreate\s+table\s+(?:core\.)?` + regexp.QuoteMeta(table) + `\s*\((.*?)\);`
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
	cfg.ConnConfig.RuntimeParams["search_path"] = "core"
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
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.atlas_schema_revisions') IS NOT NULL OR to_regclass('core.atlas_schema_revisions') IS NOT NULL OR to_regclass('core.core_principals') IS NOT NULL OR EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='core')`).Scan(&initialized); err != nil {
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
