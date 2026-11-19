// Package postgres implements Core metadata persistence. Atlas SQL is the only
// source of schema DDL; this package validates database state and invokes Atlas.
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
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"ariga.io/atlas/sql/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/* atlas.hcl
var migrationFiles embed.FS

var ErrDirtyDatabase = errors.New("refusing a database that is not a clean Core database")

const AtlasVersion = "v1.3.0"
const baselineVersion = "20261117000000"

var coreSchemas = []string{"access", "files", "uploads", "file_index", "engine_data", "coordination"}

var coreSchemaTables = []string{
	"access.users", "access.bootstrap", "access.credentials", "access.groups", "access.grants",
	"files.objects", "files.memberships",
	"uploads.requests", "uploads.staged",
	"file_index.sets", "file_index.members",
	"engine_data.records",
	"coordination.runtime", "coordination.instances", "coordination.workflows", "coordination.tasks", "coordination.dependencies",
}

var coreSchemaRelations = append(append([]string(nil), coreSchemaTables...), "engine_data.revision_seq")

var atlasURLPattern = regexp.MustCompile(`(?i)\bpostgres(?:ql)?://[^\s"'<>]+`)
var atlasPasswordPattern = regexp.MustCompile(`(?i)\b(password\s*[=:]\s*)(?:'[^']*'|"[^"]*"|[^\s,;]+)`)

// Migrate validates the embedded clean baseline, refuses unknown or unmanaged
// application objects before Atlas can execute DDL, and applies one migration
// history to a fresh database. Existing Core databases are never converted.
func Migrate(ctx context.Context, pool *pgxpool.Pool, atlasBinary string, lockTimeout time.Duration) error {
	if pool == nil {
		return errors.New("postgres: nil pool")
	}
	if pool.Config().MaxConns < 2 {
		return errors.New("Core Atlas migrations require at least two database connections")
	}
	if strings.TrimSpace(atlasBinary) == "" || lockTimeout <= 0 {
		return errors.New("postgres: Atlas binary and lock timeout must be configured")
	}
	if err := validateMigrations(); err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire Core migration lock connection: %w", err)
	}
	const migrationLock = "zubrium-core-atlas-baseline-v3"
	lockCtx, cancelLock := context.WithTimeout(ctx, lockTimeout)
	defer cancelLock()
	if err := lockAdvisory(lockCtx, conn, migrationLock); err != nil {
		conn.Release()
		return fmt.Errorf("acquire Core migration lock: %w", err)
	}
	defer releaseAdvisoryLocks(conn, []string{migrationLock})
	if err := ensureEmptyOrAtlasManaged(ctx, pool); err != nil {
		return err
	}
	var runtimeRoleExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname='zubrium_core_runtime')`).Scan(&runtimeRoleExists); err != nil {
		return fmt.Errorf("inspect Core runtime database role: %w", err)
	}
	if !runtimeRoleExists {
		return errors.New("PostgreSQL role zubrium_core_runtime must be provisioned before applying Core Atlas migrations")
	}
	binary, err := exec.LookPath(atlasBinary)
	if err != nil {
		return fmt.Errorf("locate Atlas executable: %w", err)
	}
	versionCommand := exec.CommandContext(ctx, binary, "version")
	versionCommand.Env = atlasEnvironment(atlasEnvironmentConfig{})
	versionOutput, versionErr := versionCommand.CombinedOutput()
	if versionErr != nil || !isPinnedAtlasVersion(string(versionOutput)) {
		return fmt.Errorf("atlas CLI must be version %s", AtlasVersion)
	}
	databaseURL := pool.Config().ConnString()
	migrationURL, err := atlasURLForSchema(databaseURL, "public")
	if err != nil {
		return fmt.Errorf("scope Core Atlas migration URL: %w", err)
	}
	stage, cleanup, err := stageAtlasFiles()
	if err != nil {
		return err
	}
	defer cleanup()
	configURL := (&url.URL{Scheme: "file", Path: filepath.Join(stage, "atlas.hcl")}).String()
	dirURL := (&url.URL{Scheme: "file", Path: filepath.Join(stage, "migrations")}).String()
	cmd := exec.CommandContext(ctx, binary, "migrate", "apply", "--config", configURL,
		"--env", "core", "--lock-timeout", lockTimeout.String(), "--revisions-schema", "public")
	cmd.Env = atlasEnvironment(atlasEnvironmentConfig{
		CoreDatabaseURL:      databaseURL,
		MigrationDatabaseURL: migrationURL,
		MigrationDir:         dirURL,
	})
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := atlasErrorDetails(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("atlas Core migration failed (exit %d): %s: %w", exitCode(err), detail, err)
	}
	return nil
}

func isPinnedAtlasVersion(output string) bool {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	fields := strings.Fields(firstLine)
	return (len(fields) == 3 || len(fields) == 4) && fields[0] == "atlas" && fields[len(fields)-2] == "version" && fields[len(fields)-1] == AtlasVersion && (len(fields) == 3 || fields[1] == "community")
}

func validateMigrations() error {
	dir, err := loadAtlasMigrationDir("migrations")
	if err != nil {
		return err
	}
	if err := migrate.Validate(dir); err != nil {
		return fmt.Errorf("validate Core Atlas migration directory: %w", err)
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
	var hasHistory bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.atlas_schema_revisions') IS NOT NULL`).Scan(&hasHistory); err != nil {
		return fmt.Errorf("inspect Atlas migration history: %w", err)
	}
	allowedSchemas := append([]string{"public"}, coreSchemas...)
	rows, err := pool.Query(ctx, `SELECT n.nspname,c.relname FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname !~ '^pg_(toast|temp)'
		AND c.relkind IN ('r','p','v','m','f','S')`)
	if err != nil {
		return fmt.Errorf("inspect database relations before Atlas migration: %w", err)
	}
	var relations []string
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			rows.Close()
			return err
		}
		if schema == "public" {
			if name != "atlas_schema_revisions" {
				rows.Close()
				return ErrDirtyDatabase
			}
		} else {
			relations = append(relations, schema+"."+name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	var userSchemas []string
	rows, err = pool.Query(ctx, `SELECT nspname FROM pg_catalog.pg_namespace
		WHERE nspname NOT IN ('public','pg_catalog','information_schema') AND nspname !~ '^pg_(toast|temp)'`)
	if err != nil {
		return fmt.Errorf("inspect database schemas before Atlas migration: %w", err)
	}
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			rows.Close()
			return err
		}
		if !containsString(allowedSchemas, schema) {
			rows.Close()
			return ErrDirtyDatabase
		}
		userSchemas = append(userSchemas, schema)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	var hasCustomObjects bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace
		WHERE n.nspname=ANY($1::text[]) AND t.typrelid=0 AND t.typtype IN ('d','e','r','m')
		UNION ALL
		SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
		WHERE n.nspname=ANY($1::text[]))`, allowedSchemas).Scan(&hasCustomObjects); err != nil {
		return fmt.Errorf("inspect custom database objects before Atlas migration: %w", err)
	}
	if hasCustomObjects {
		return ErrDirtyDatabase
	}
	if hasHistory {
		var hasBaseline bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.atlas_schema_revisions WHERE version=$1)`, baselineVersion).Scan(&hasBaseline); err != nil {
			return fmt.Errorf("inspect Core Atlas baseline history: %w", err)
		}
		if !hasBaseline || len(relations) != len(coreSchemaRelations) || len(userSchemas) != len(coreSchemas) {
			return ErrDirtyDatabase
		}
		for _, relation := range coreSchemaRelations {
			if !containsString(relations, relation) {
				return ErrDirtyDatabase
			}
		}
		for _, schema := range coreSchemas {
			if !containsString(userSchemas, schema) {
				return ErrDirtyDatabase
			}
		}
	} else if len(relations) > 0 || len(userSchemas) > 0 {
		// Do not mutate partial schemas or a database that has been prepared for
		// the previous single-schema baseline.
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
	directory := filepath.Join(stage, "migrations")
	if err := os.Mkdir(directory, 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create staged Atlas migration directory: %w", err)
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("read embedded Atlas migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name != "atlas.sum" && !strings.HasSuffix(name, ".sql") {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("read embedded Atlas file %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("stage Atlas file %s: %w", name, err)
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
		return "", errors.New("Core Atlas migration requires a PostgreSQL URL")
	}
	if schemaName != "public" {
		return "", errors.New("Core Atlas revision history must be scoped to public")
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type atlasEnvironmentConfig struct {
	CoreDatabaseURL      string
	MigrationDatabaseURL string
	MigrationDir         string
}

func atlasEnvironment(config atlasEnvironmentConfig) []string {
	var env []string
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return append(env,
		"ATLAS_NO_UPDATE_NOTIFIER=true",
		"ZUBRIUM_CORE_DATABASE_URL="+config.CoreDatabaseURL,
		"ZUBRIUM_CORE_MIGRATION_DATABASE_URL="+config.MigrationDatabaseURL,
		"ZUBRIUM_CORE_MIGRATION_DIR="+config.MigrationDir,
	)
}

func exitCode(err error) int {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

func atlasErrorDetails(output string) string {
	detail := strings.TrimSpace(string(atlasURLPattern.ReplaceAll([]byte(output), []byte("postgres://[redacted]"))))
	detail = atlasPasswordPattern.ReplaceAllString(detail, "$1[redacted]")
	if len(detail) > 2048 {
		detail = detail[:2045]
		for !utf8.ValidString(detail) {
			detail = detail[:len(detail)-1]
		}
		detail += "..."
	}
	return detail
}
