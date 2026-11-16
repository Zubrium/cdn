package files

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

// RenameConfiguredService is an operator action: identity and owned files
// remain unchanged. Update FILE_SERVICES before the next engine startup.
func RenameConfiguredService(ctx context.Context, pool *pgxpool.Pool, oldName, newName string) (Owner, error) {
	if oldName == "" || strings.TrimSpace(newName) != newName || newName == "" || len(newName) > 100 || strings.ContainsAny(newName, "\r\n\x00") {
		return Owner{}, ErrInvalid
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		return Owner{}, e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_file_owners'))`); e != nil {
		return Owner{}, e
	}
	var o Owner
	e = tx.QueryRow(ctx, `UPDATE file_owners SET name=$2 WHERE kind='service' AND name=$1 RETURNING id,name,kind`, oldName, newName).Scan(&o.ID, &o.Name, &o.Kind)
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			return Owner{}, fmt.Errorf("%w: service name already exists", ErrConflict)
		}
		return Owner{}, mapNotFound(e)
	}
	return o, tx.Commit(ctx)
}
