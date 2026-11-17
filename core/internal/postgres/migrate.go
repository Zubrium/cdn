// Package postgres implements Core metadata persistence. Runtime DDL lives in
// the Atlas migration directory; this package only inspects database state
// and delegates schema changes to the pinned Atlas executable.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ariga.io/atlas/sql/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/* schema-migrations/* atlas.hcl
var migrationFiles embed.FS

var ErrDirtyDatabase = errors.New("refusing a database that is not a clean Core schema")

const AtlasVersion = "v1.3.0"

var coreSchemaTables = []string{
	"core_access_groups",
	"core_bootstrap",
	"core_credentials",
	"core_file_groups",
	"core_file_set_members",
	"core_file_sets",
	"module_data",
	"coordinator_config",
	"coordinator_instances",
	"coordinator_jobs",
	"coordinator_publications",
	"coordinator_route_targets",
	"core_group_access",
	"core_objects",
	"core_principals",
	"core_staged_objects",
	"core_upload_requests",
}

var coreSchemaRelations = append(append([]string(nil), coreSchemaTables...), "module_data_revision_seq")

// Migrate validates the embedded versioned directory, refuses to initialize an
// unmanaged or legacy database, and delegates all DDL and migration tracking
// to Atlas. The executable and lock timeout are explicit deployment settings.
func Migrate(ctx context.Context, pool *pgxpool.Pool, atlasBinary string, lockTimeout time.Duration) error {
	if pool == nil {
		return errors.New("postgres: nil pool")
	}
	if pool.Config().MaxConns < 2 {
		return errors.New("core Atlas migrations require a database pool with at least two connections")
	}
	if strings.TrimSpace(atlasBinary) == "" || lockTimeout <= 0 {
		return errors.New("postgres: Atlas binary and lock timeout must be configured")
	}
	if err := validateMigrations(); err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire core migration lock connection: %w", err)
	}
	const migrationLock = "core-atlas-schema-migration-v2"
	lockCtx, cancelLock := context.WithTimeout(ctx, lockTimeout)
	defer cancelLock()
	if err := lockAdvisory(lockCtx, conn, migrationLock); err != nil {
		conn.Release()
		return fmt.Errorf("acquire core migration lock: %w", err)
	}
	defer releaseAdvisoryLocks(conn, []string{migrationLock})
	if err := ensureEmptyOrAtlasManaged(ctx, pool); err != nil {
		return err
	}
	var runtimeRoleExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname='zubrium_core_runtime')`).Scan(&runtimeRoleExists); err != nil {
		return fmt.Errorf("inspect core runtime database role: %w", err)
	}
	if !runtimeRoleExists {
		return errors.New("PostgreSQL role zubrium_core_runtime must be provisioned before applying core Atlas migrations")
	}
	binary, err := exec.LookPath(atlasBinary)
	if err != nil {
		return fmt.Errorf("locate Atlas executable: %w", err)
	}
	versionCommand := exec.CommandContext(ctx, binary, "version")
	versionCommand.Env = atlasEnvironment("", "", "", "")
	versionOutput, versionErr := versionCommand.CombinedOutput()
	if versionErr != nil || !isPinnedAtlasVersion(string(versionOutput)) {
		return fmt.Errorf("atlas CLI must be version %s", AtlasVersion)
	}
	databaseURL := pool.Config().ConnString()
	coreMigrationDatabaseURL, err := atlasURLForSchema(databaseURL, "core")
	if err != nil {
		return fmt.Errorf("scope core Atlas migration URL: %w", err)
	}

	stage, cleanup, err := stageAtlasFiles()
	if err != nil {
		return err
	}
	defer cleanup()
	configPath := filepath.Join(stage, "atlas.hcl")
	configURL := (&url.URL{Scheme: "file", Path: configPath}).String()
	migrationPath := filepath.Join(stage, "migrations")
	migrationURL := (&url.URL{Scheme: "file", Path: migrationPath}).String()

	schemaMigrationURL := (&url.URL{Scheme: "file", Path: filepath.Join(stage, "schema-migrations")}).String()
	for _, step := range []struct {
		environment, revisionSchema string
		migrationURL                string
	}{
		{environment: "core_schema", revisionSchema: "public", migrationURL: schemaMigrationURL},
		{environment: "core", revisionSchema: "core", migrationURL: migrationURL},
	} {
		cmd := exec.CommandContext(ctx, binary, "migrate", "apply", "--config", configURL,
			"--env", step.environment, "--lock-timeout", lockTimeout.String(), "--revisions-schema", step.revisionSchema)
		cmd.Env = atlasEnvironment(databaseURL, coreMigrationDatabaseURL, migrationURL, schemaMigrationURL)
		if _, err := cmd.CombinedOutput(); err != nil {
			// Atlas output may include connection diagnostics. Do not return it through
			// application errors, where it could disclose database connection details.
			return fmt.Errorf("atlas migration %s failed (exit %d): %w", step.environment, exitCode(err), err)
		}
	}
	return nil
}

func isPinnedAtlasVersion(output string) bool {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	fields := strings.Fields(firstLine)
	return (len(fields) == 3 || len(fields) == 4) && fields[0] == "atlas" && fields[len(fields)-2] == "version" && fields[len(fields)-1] == AtlasVersion && (len(fields) == 3 || fields[1] == "community")
}

func validateMigrations() error {
	for _, directory := range []string{"migrations", "schema-migrations"} {
		dir, err := loadAtlasMigrationDir(directory)
		if err != nil {
			return err
		}
		if err := migrate.Validate(dir); err != nil {
			return fmt.Errorf("validate Atlas migration directory %q: %w", directory, err)
		}
	}
	return nil
}

func loadAtlasMigrationDir(directory string) (migrate.Dir, error) {
	entries, err := fs.ReadDir(migrationFiles, directory)
	if err != nil {
		return nil, fmt.Errorf("read embedded Atlas migration directory %q: %w", directory, err)
	}
	dir := &migrate.MemDir{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		contents, err := migrationFiles.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read Atlas migration %s: %w", entry.Name(), err)
		}
		if err := dir.WriteFile(entry.Name(), contents); err != nil {
			return nil, fmt.Errorf("load Atlas migration %s: %w", entry.Name(), err)
		}
	}
	return dir, nil
}

func ensureEmptyOrAtlasManaged(ctx context.Context, pool *pgxpool.Pool) error {
	var hasPublicHistory, hasCoreHistory, hasCoreSchema bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.atlas_schema_revisions') IS NOT NULL,
		to_regclass('core.atlas_schema_revisions') IS NOT NULL,
		EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='core')`).Scan(&hasPublicHistory, &hasCoreHistory, &hasCoreSchema); err != nil {
		return fmt.Errorf("inspect Atlas migration histories: %w", err)
	}
	allowedPublic := []string{"atlas_schema_revisions"}
	allowedCore := append(append([]string(nil), coreSchemaRelations...), "atlas_schema_revisions")
	rows, err := pool.Query(ctx, `SELECT n.nspname,c.relname FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname IN ('public','core') AND c.relkind IN ('r','p','v','m','f','S')`)
	if err != nil {
		return fmt.Errorf("inspect database relations before Atlas migration: %w", err)
	}
	var publicRelations, coreRelations []string
	for rows.Next() {
		var schema, relation string
		if err := rows.Scan(&schema, &relation); err != nil {
			rows.Close()
			return err
		}
		if schema == "public" {
			publicRelations = append(publicRelations, relation)
		} else {
			coreRelations = append(coreRelations, relation)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, relation := range publicRelations {
		if !containsString(allowedPublic, relation) {
			return ErrDirtyDatabase
		}
	}
	for _, relation := range coreRelations {
		if !containsString(allowedCore, relation) {
			return ErrDirtyDatabase
		}
	}
	var hasCustomObjects bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace
		WHERE n.nspname IN ('public','core') AND t.typrelid=0 AND t.typtype IN ('d','e','r','m')
		UNION ALL
		SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
		WHERE n.nspname IN ('public','core')
	)`).Scan(&hasCustomObjects); err != nil {
		return fmt.Errorf("inspect custom database objects before Atlas migration: %w", err)
	}
	if hasCustomObjects {
		return ErrDirtyDatabase
	}
	if (hasCoreSchema && !hasPublicHistory && !hasCoreHistory) || (hasPublicHistory && !hasCoreSchema) {
		return ErrDirtyDatabase
	}
	if hasPublicHistory {
		var incompatibleBootstrapHistory bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.atlas_schema_revisions WHERE version <> $1)`, "20261116000000").Scan(&incompatibleBootstrapHistory); err != nil {
			return fmt.Errorf("inspect Core schema bootstrap history: %w", err)
		}
		if incompatibleBootstrapHistory {
			return ErrDirtyDatabase
		}
	}
	if hasCoreHistory {
		var hasBaseline, hasAppliedCoreMigrations bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.atlas_schema_revisions WHERE version = $1),
			EXISTS(SELECT 1 FROM core.atlas_schema_revisions)`, "20261116000000").Scan(&hasBaseline, &hasAppliedCoreMigrations); err != nil {
			return fmt.Errorf("inspect Core migration baseline: %w", err)
		}
		var existingCoreTables int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_class c
			JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname='core' AND c.relkind IN ('r','p') AND c.relname=ANY($1::text[])`, coreSchemaTables).Scan(&existingCoreTables); err != nil {
			return fmt.Errorf("count Core schema tables: %w", err)
		}
		if (hasAppliedCoreMigrations && !hasBaseline) ||
			(hasBaseline && existingCoreTables != len(coreSchemaTables)) ||
			(!hasBaseline && existingCoreTables != 0) {
			return ErrDirtyDatabase
		}
	} else if len(coreRelations) > 0 {
		// The only valid schema-only state is a newly created empty Core schema
		// whose bootstrap migration is recorded in public.
		return ErrDirtyDatabase
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func stageAtlasFiles() (string, func(), error) {
	stage, err := os.MkdirTemp("", "zubrium-core-atlas-")
	if err != nil {
		return "", nil, fmt.Errorf("create private Atlas staging directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	if err := os.Chmod(stage, 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("secure Atlas staging directory: %w", err)
	}
	for _, directory := range []string{"migrations", "schema-migrations"} {
		stagedDirectory := filepath.Join(stage, directory)
		if err := os.Mkdir(stagedDirectory, 0o700); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("create staged Atlas migration directory %q: %w", directory, err)
		}
		entries, err := fs.ReadDir(migrationFiles, directory)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("read embedded Atlas migrations %q: %w", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if name != "atlas.sum" && !strings.HasSuffix(name, ".sql") {
				continue
			}
			contents, err := migrationFiles.ReadFile(directory + "/" + name)
			if err != nil {
				cleanup()
				return "", nil, fmt.Errorf("read embedded Atlas file %s/%s: %w", directory, name, err)
			}
			if err := os.WriteFile(filepath.Join(stagedDirectory, name), contents, 0o600); err != nil {
				cleanup()
				return "", nil, fmt.Errorf("stage Atlas file %s/%s: %w", directory, name, err)
			}
		}
	}
	config, err := migrationFiles.ReadFile("atlas.hcl")
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("read embedded Atlas config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "atlas.hcl"), config, 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("stage Atlas config: %w", err)
	}
	return stage, cleanup, nil
}

func atlasURLForSchema(databaseURL, schemaName string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.Path == "" {
		return "", errors.New("core Atlas migration requires a PostgreSQL URL")
	}
	if schemaName != "core" {
		return "", errors.New("core Atlas migration schema must be core")
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func atlasEnvironment(databaseURL, coreMigrationDatabaseURL, migrationURL, schemaMigrationURL string) []string {
	var env []string
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return append(env,
		"ATLAS_NO_UPDATE_NOTIFIER=true",
		"ZUBRIUM_CORE_DATABASE_URL="+databaseURL,
		"ZUBRIUM_CORE_MIGRATION_DATABASE_URL="+coreMigrationDatabaseURL,
		"ZUBRIUM_CORE_MIGRATION_DIR="+migrationURL,
		"ZUBRIUM_CORE_SCHEMA_MIGRATION_DIR="+schemaMigrationURL,
	)
}

func exitCode(err error) int {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}
