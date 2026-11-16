package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RecoverPending removes stale incomplete file and Loader staging objects and
// completes permanent file deletions. Advisory locks keep cleanup from racing
// active uploads and staging streams.
func RecoverPending(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, before time.Time, limit int) error {
	if pool == nil || store == nil || before.IsZero() || limit <= 0 || limit > 10000 {
		return errors.New("invalid recovery configuration")
	}
	rows, err := pool.Query(ctx, `SELECT id,object_key::text,state FROM core_objects
		WHERE state IN ('pending','deleting','failed') AND updated_at<$1 ORDER BY updated_at,id LIMIT $2`, before, limit)
	if err != nil {
		return err
	}
	type candidate struct {
		id    uuid.UUID
		key   string
		state string
	}
	var items []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.key, &item.state); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := recoverOne(ctx, pool, store, item.id, item.key, before); err != nil {
			return err
		}
	}
	return recoverStaged(ctx, pool, store, before, limit-len(items))
}

func recoverOne(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, id uuid.UUID, key string, before time.Time) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked); err != nil {
		conn.Release()
		return err
	}
	if !locked {
		conn.Release()
		return nil
	}
	defer releaseAdvisoryLocks(conn, []string{key})
	var state string
	err = conn.QueryRow(ctx, `SELECT state FROM core_objects WHERE id=$1 AND updated_at<$2 FOR UPDATE`, id, before).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "pending" && state != "deleting" && state != "failed" {
		return nil
	}
	if err := store.Delete(ctx, key); err != nil {
		return fmt.Errorf("remove abandoned file bytes: %w", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if state == "deleting" {
		if _, err := tx.Exec(ctx, `UPDATE core_upload_requests SET state='deleted',object_id=NULL,updated_at=now() WHERE object_id=$1`, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE core_upload_requests SET state='failed',object_id=NULL,updated_at=now() WHERE object_id=$1 AND state IN ('pending','failed')`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core_objects WHERE id=$1 AND state=$2`, id, state); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation',$1)`, id.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
