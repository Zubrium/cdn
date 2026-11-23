package postgres

import (
	"context"
	"errors"

	appcache "github.com/Zubrium/cdn/core/application/cache"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CacheEpochSource exposes the internal monotonic authorization epoch and the
// authoritative file revision to Core's bounded caches. It does not change the
// public credential-bound session contract.
type CacheEpochSource struct{ Pool *pgxpool.Pool }

var _ appcache.EpochSource = CacheEpochSource{}

func (s CacheEpochSource) AccessState(ctx context.Context, principalID, fileID string) (int64, int64, error) {
	if s.Pool == nil {
		return 0, 0, errors.New("nil database pool")
	}
	principal, err := uuid.Parse(principalID)
	if err != nil || principal == uuid.Nil {
		return 0, 0, errors.New("invalid principal ID")
	}
	file, err := uuid.Parse(fileID)
	if err != nil || file == uuid.Nil {
		return 0, 0, errors.New("invalid file ID")
	}
	var epoch, revision int64
	err = s.Pool.QueryRow(ctx, `SELECT users.access_epoch, objects.revision
		FROM access.users AS users CROSS JOIN files.objects AS objects
		WHERE users.id=$1 AND objects.id=$2`, principal, file).Scan(&epoch, &revision)
	return epoch, revision, err
}

// Revision reads the authoritative file revision without a principal scope.
func (s CacheEpochSource) Revision(ctx context.Context, fileID string) (int64, error) {
	if s.Pool == nil {
		return 0, errors.New("nil database pool")
	}
	file, err := uuid.Parse(fileID)
	if err != nil || file == uuid.Nil {
		return 0, errors.New("invalid file ID")
	}
	var revision int64
	err = s.Pool.QueryRow(ctx, `SELECT revision FROM files.objects WHERE id=$1`, file).Scan(&revision)
	return revision, err
}

// bumpAccessEpoch increments the internal epoch for the affected principals in
// the caller's transaction. With no principals it invalidates every cached
// authorization because a group-wide change can affect any grant holder.
func bumpAccessEpoch(ctx context.Context, tx pgx.Tx, principalIDs ...uuid.UUID) error {
	if len(principalIDs) == 0 {
		_, err := tx.Exec(ctx, `UPDATE access.users SET access_epoch=access_epoch+1`)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE access.users SET access_epoch=access_epoch+1 WHERE id = ANY($1::uuid[])`, principalIDs)
	return err
}
