package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"

	"github.com/jackc/pgx/v5/pgxpool"
)

// revisionTable stores the applied migrations. The layout follows the Atlas
// convention so the directory can also be inspected with the Atlas CLI.
const revisionTable = "atlas_schema_revisions"

// Migrate applies the embedded migrations with the Atlas migrate executor.
//
// The executor verifies the integrity sum (atlas.sum) of the migration
// directory before touching the database and records every applied file in
// the revision table. All files are applied inside a single transaction, and
// a transaction-scoped advisory lock makes sure that only one instance
// migrates at a time.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, lockTimeout time.Duration) error {
	if pool == nil {
		return errors.New("database: nil pool")
	}
	if lockTimeout <= 0 {
		return errors.New("database: migration lock timeout must be explicitly configured")
	}

	dir, err := dirFromFS(fsys)
	if err != nil {
		return err
	}
	if err := migrate.Validate(dir); err != nil {
		return fmt.Errorf("database: validate migrations: %w", err)
	}

	db, err := sql.Open("pgx", pool.Config().ConnString())
	if err != nil {
		return fmt.Errorf("database: open sql connection: %w", err)
	}
	defer db.Close()

	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()

	tx, err := db.BeginTx(lockCtx, nil)
	if err != nil {
		return fmt.Errorf("database: begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(lockCtx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_migrate'))`); err != nil {
		return fmt.Errorf("database: acquire migration lock: %w", err)
	}

	rrw := &revisionStore{tx: tx}
	if err := rrw.init(ctx); err != nil {
		return fmt.Errorf("database: create revision table: %w", err)
	}

	drv, err := postgres.Open(tx)
	if err != nil {
		return fmt.Errorf("database: open atlas driver: %w", err)
	}

	exec, err := migrate.NewExecutor(drv, dir, rrw, migrate.WithAllowDirty(true))
	if err != nil {
		return fmt.Errorf("database: create migration executor: %w", err)
	}

	switch err := exec.ExecuteN(ctx, 0); {
	case err == nil:
	case errors.Is(err, migrate.ErrNoPendingFiles):
	default:
		return fmt.Errorf("database: apply migrations: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("database: commit migrations: %w", err)
	}
	return nil
}

// SeedDefaults adds only non-site operational settings. Site state is configured
// by immutable snapshots and statically configured branches.
func SeedDefaults(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `INSERT INTO site_settings (id) VALUES (TRUE) ON CONFLICT (id) DO NOTHING`); err != nil {
		return fmt.Errorf("seed site settings: %w", err)
	}
	return nil
}

// dirFromFS copies the embedded migration directory into memory. The sum file
// is copied as well, so the executor can verify the integrity of the files.
func dirFromFS(fsys fs.FS) (migrate.Dir, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("database: read migrations: %w", err)
	}
	dir := &migrate.MemDir{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("database: read migration %s: %w", name, err)
		}
		if err := dir.WriteFile(name, b); err != nil {
			return nil, fmt.Errorf("database: load migration %s: %w", name, err)
		}
	}
	return dir, nil
}

// revisionStore persists the applied migrations in the revision table.
type revisionStore struct {
	tx *sql.Tx
}

// Ident implements migrate.RevisionReadWriter.
func (s *revisionStore) Ident() *migrate.TableIdent {
	return &migrate.TableIdent{Name: revisionTable}
}

// init creates the revision table if it does not exist yet.
func (s *revisionStore) init(ctx context.Context) error {
	_, err := s.tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS atlas_schema_revisions (
    version          VARCHAR(255) PRIMARY KEY,
    description      VARCHAR(255) NOT NULL,
    type             VARCHAR(20)  NOT NULL,
    applied          INT          NOT NULL,
    total            INT          NOT NULL,
    executed_at      TIMESTAMPTZ  NOT NULL,
    execution_time   BIGINT       NOT NULL,
    error            TEXT         NULL,
    error_stmt       TEXT         NULL,
    hash             VARCHAR(255) NOT NULL,
    partial_hashes   TEXT[]       NULL,
    operator_version VARCHAR(255) NOT NULL
)`)
	return err
}

// ReadRevisions implements migrate.RevisionReadWriter.
func (s *revisionStore) ReadRevisions(ctx context.Context) ([]*migrate.Revision, error) {
	rows, err := s.tx.QueryContext(ctx, `
SELECT version, description, type, applied, total, executed_at, execution_time,
       error, error_stmt, hash, partial_hashes, operator_version
  FROM atlas_schema_revisions
 ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var revs []*migrate.Revision
	for rows.Next() {
		var (
			rev      migrate.Revision
			typ      string
			execAt   time.Time
			errText  sql.NullString
			errStmt  sql.NullString
			partials []string
		)
		if err := rows.Scan(
			&rev.Version, &rev.Description, &typ, &rev.Applied, &rev.Total,
			&execAt, &rev.ExecutionTime, &errText, &errStmt, &rev.Hash,
			pqArray(&partials), &rev.OperatorVersion,
		); err != nil {
			return nil, err
		}
		rev.Type = revisionType(typ)
		rev.ExecutedAt = execAt
		rev.Error = errText.String
		rev.ErrorStmt = errStmt.String
		rev.PartialHashes = partials
		revs = append(revs, &rev)
	}
	return revs, rows.Err()
}

// ReadRevision implements migrate.RevisionReadWriter.
func (s *revisionStore) ReadRevision(ctx context.Context, version string) (*migrate.Revision, error) {
	var (
		rev      migrate.Revision
		typ      string
		execAt   time.Time
		errText  sql.NullString
		errStmt  sql.NullString
		partials []string
	)
	err := s.tx.QueryRowContext(ctx, `
SELECT version, description, type, applied, total, executed_at, execution_time,
       error, error_stmt, hash, partial_hashes, operator_version
  FROM atlas_schema_revisions
 WHERE version = $1`, version).Scan(
		&rev.Version, &rev.Description, &typ, &rev.Applied, &rev.Total,
		&execAt, &rev.ExecutionTime, &errText, &errStmt, &rev.Hash,
		pqArray(&partials), &rev.OperatorVersion,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, migrate.ErrRevisionNotExist
	case err != nil:
		return nil, err
	}
	rev.Type = revisionType(typ)
	rev.ExecutedAt = execAt
	rev.Error = errText.String
	rev.ErrorStmt = errStmt.String
	rev.PartialHashes = partials
	return &rev, nil
}

// WriteRevision implements migrate.RevisionReadWriter.
func (s *revisionStore) WriteRevision(ctx context.Context, rev *migrate.Revision) error {
	var partials any
	if len(rev.PartialHashes) == 0 {
		partials = nil
	} else {
		partials = pqStringArray(rev.PartialHashes)
	}
	_, err := s.tx.ExecContext(ctx, `
INSERT INTO atlas_schema_revisions (
    version, description, type, applied, total, executed_at, execution_time,
    error, error_stmt, hash, partial_hashes, operator_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11, $12)
ON CONFLICT (version) DO UPDATE SET
    description     = EXCLUDED.description,
    type            = EXCLUDED.type,
    applied         = EXCLUDED.applied,
    total           = EXCLUDED.total,
    executed_at     = EXCLUDED.executed_at,
    execution_time  = EXCLUDED.execution_time,
    error           = EXCLUDED.error,
    error_stmt      = EXCLUDED.error_stmt,
    hash            = EXCLUDED.hash,
    partial_hashes  = EXCLUDED.partial_hashes,
    operator_version = EXCLUDED.operator_version`,
		rev.Version, rev.Description, revisionTypeName(rev.Type), rev.Applied, rev.Total,
		rev.ExecutedAt, rev.ExecutionTime, rev.Error, rev.ErrorStmt, rev.Hash,
		partials, rev.OperatorVersion,
	)
	return err
}

// DeleteRevision implements migrate.RevisionReadWriter.
func (s *revisionStore) DeleteRevision(ctx context.Context, version string) error {
	_, err := s.tx.ExecContext(ctx, `DELETE FROM atlas_schema_revisions WHERE version = $1`, version)
	return err
}

// revisionType maps the stored type name to the SDK constant.
func revisionType(name string) migrate.RevisionType {
	switch name {
	case "baseline":
		return migrate.RevisionTypeBaseline
	case "execute":
		return migrate.RevisionTypeExecute
	default:
		return migrate.RevisionTypeUnknown
	}
}

// revisionTypeName maps the SDK constant to the stored type name.
func revisionTypeName(t migrate.RevisionType) string {
	switch t {
	case migrate.RevisionTypeBaseline:
		return "baseline"
	case migrate.RevisionTypeExecute:
		return "execute"
	default:
		return "unknown"
	}
}

func pqArray(dst *[]string) interface{} {
	return pq.Array(dst)
}

func pqStringArray(src []string) interface{} {
	return pq.Array(src)
}
