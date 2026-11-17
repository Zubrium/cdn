package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PutStagedObject stores one opaque Loader chunk. Core keeps only the byte
// handle and its authorization scope; upload sessions and part ordering belong
// to Loader. The Loader-generated UUID makes retries safe after a lost reply.
func PutStagedObject(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, actor access.Principal, idValue string, groups []string, maxBytes, size int64, checksum string, body io.Reader) error {
	if pool == nil || store == nil || !actor.Active || body == nil || size < 0 || maxBytes <= 0 || size > maxBytes ||
		!storage.ValidUUID(idValue) || !storage.ValidGroups(groups) {
		return errors.New("invalid staged object")
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil || actorID == uuid.Nil {
		return ErrUnauthorized
	}
	id, _ := uuid.Parse(idValue)
	expectedHash, err := parseSHA256(checksum)
	if err != nil {
		return errors.New("invalid staged object checksum")
	}
	orderedGroups := append([]string(nil), groups...)
	sort.Strings(orderedGroups)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	lockKey := stagedLockKey(id)
	if err := lockAdvisory(ctx, conn, lockKey); err != nil {
		conn.Release()
		return err
	}
	defer releaseAdvisoryLocks(conn, []string{lockKey})

	var ownerID uuid.UUID
	var objectKey uuid.UUID
	var storedGroups []string
	var storedSize int64
	var storedHash []byte
	var state string
	err = conn.QueryRow(ctx, `SELECT principal_id,object_key,group_keys,size_bytes,sha256,state
		FROM core_staged_objects WHERE id=$1`, id).
		Scan(&ownerID, &objectKey, &storedGroups, &storedSize, &storedHash, &state)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if exists {
		if ownerID != actorID {
			return storage.ErrNotFound
		}
		if storedSize != size || !sameStrings(storedGroups, orderedGroups) || !bytes.Equal(storedHash, expectedHash) {
			return storage.ErrRequestConflict
		}
		switch state {
		case "ready":
			return nil
		case "pending":
			return storage.ErrUploadInProgress
		case "deleting":
			return storage.ErrRequestDeleted
		case "failed":
			if err := store.Delete(ctx, objectKey.String()); err != nil {
				return fmt.Errorf("clean previous staged object attempt: %w", err)
			}
		default:
			return errors.New("invalid staged object state")
		}
	} else {
		objectKey, err = uuid.NewRandom()
		if err != nil {
			return err
		}
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireUploadAccess(ctx, tx, actorID, orderedGroups); err != nil {
		return err
	}
	if exists {
		_, err = tx.Exec(ctx, `UPDATE core_staged_objects SET state='pending',updated_at=now()
			WHERE id=$1 AND principal_id=$2 AND state='failed'`, id, actorID)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO core_staged_objects(id,object_key,principal_id,group_keys,size_bytes,sha256,state)
			VALUES($1,$2,$3,$4,$5,$6,'pending')`, id, objectKey, actorID, orderedGroups, size, expectedHash)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	actualHash := sha256.New()
	if err := store.Put(ctx, objectKey.String(), io.TeeReader(body, actualHash), size, "application/octet-stream"); err != nil {
		markStagedFailure(conn, store, id, objectKey)
		return fmt.Errorf("store staged object: %w", err)
	}
	if !bytes.Equal(actualHash.Sum(nil), expectedHash) {
		markStagedFailure(conn, store, id, objectKey)
		return errors.New("staged object checksum mismatch")
	}

	finalTx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = finalTx.Rollback(context.Background()) }()
	if err := requireUploadAccess(ctx, finalTx, actorID, orderedGroups); err != nil {
		_ = finalTx.Rollback(context.Background())
		markStagedFailure(conn, store, id, objectKey)
		return fmt.Errorf("staged object authorization at commit: %w", err)
	}
	tag, err := finalTx.Exec(ctx, `UPDATE core_staged_objects SET state='ready',updated_at=now()
		WHERE id=$1 AND principal_id=$2 AND state='pending'`, id, actorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("staged object reservation lost")
	}
	return finalTx.Commit(ctx)
}

// ReadStagedObject holds an advisory lock until the caller closes the returned
// stream, so expiry cleanup cannot delete bytes during Loader's commit stream.
func ReadStagedObject(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, actor access.Principal, idValue string) (io.ReadCloser, int64, string, error) {
	if pool == nil || store == nil || !actor.Active {
		return nil, 0, "", ErrUnauthorized
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil || actorID == uuid.Nil || !storage.ValidUUID(idValue) {
		return nil, 0, "", storage.ErrNotFound
	}
	id, _ := uuid.Parse(idValue)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, 0, "", err
	}
	lockKey := stagedLockKey(id)
	if err := lockAdvisory(ctx, conn, lockKey); err != nil {
		conn.Release()
		return nil, 0, "", err
	}
	var objectKey uuid.UUID
	var size int64
	var checksum []byte
	err = conn.QueryRow(ctx, `SELECT s.object_key,s.size_bytes,s.sha256
		FROM core_staged_objects s JOIN core_principals p ON p.id=s.principal_id AND p.active
		WHERE s.id=$1 AND s.principal_id=$2 AND s.state='ready'
		AND NOT EXISTS (
			SELECT 1 FROM unnest(s.group_keys) AS target(group_key)
			WHERE NOT EXISTS (
				SELECT 1 FROM core_access_groups g JOIN core_group_access ga ON ga.group_key=g.group_key
				WHERE g.group_key=target.group_key AND g.active AND ga.principal_id=p.id AND ga.access_level=2))
		FOR UPDATE OF s`, id, actorID).Scan(&objectKey, &size, &checksum)
	if errors.Is(err, pgx.ErrNoRows) {
		releaseAdvisoryLocks(conn, []string{lockKey})
		return nil, 0, "", storage.ErrNotFound
	}
	if err != nil {
		releaseAdvisoryLocks(conn, []string{lockKey})
		return nil, 0, "", err
	}
	reader, err := store.Read(ctx, objectKey.String())
	if err != nil {
		releaseAdvisoryLocks(conn, []string{lockKey})
		return nil, 0, "", err
	}
	return &lockedStagedReader{ReadCloser: reader, conn: conn, lockKey: lockKey}, size, hex.EncodeToString(checksum), nil
}

// DeleteStagedObject is owner-scoped and idempotent. It only removes temporary
// bytes; current group access is not required for cleanup after revocation.
func DeleteStagedObject(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, actor access.Principal, idValue string) error {
	if pool == nil || store == nil || !actor.Active || !storage.ValidUUID(idValue) {
		return ErrUnauthorized
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil || actorID == uuid.Nil {
		return ErrUnauthorized
	}
	id, _ := uuid.Parse(idValue)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	lockKey := stagedLockKey(id)
	if err := lockAdvisory(ctx, conn, lockKey); err != nil {
		conn.Release()
		return err
	}
	defer releaseAdvisoryLocks(conn, []string{lockKey})
	var active bool
	if err := conn.QueryRow(ctx, `SELECT active FROM core_principals WHERE id=$1`, actorID).Scan(&active); errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return ErrUnauthorized
	} else if err != nil {
		return err
	}
	var ownerID, objectKey uuid.UUID
	err = conn.QueryRow(ctx, `SELECT principal_id,object_key FROM core_staged_objects WHERE id=$1`, id).Scan(&ownerID, &objectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ownerID != actorID {
		return storage.ErrNotFound
	}
	return purgeStagedLocked(ctx, conn, store, id, objectKey)
}

// PurgeStagedObject is only exposed through the trusted Loader file
// capability. The UUID is the complete authority: Core never accepts an
// object key or arbitrary S3 path from the engine.
func PurgeStagedObject(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, idValue string) error {
	if pool == nil || store == nil || !storage.ValidUUID(idValue) {
		return errors.New("invalid staged object cleanup request")
	}
	id, _ := uuid.Parse(idValue)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	lockKey := stagedLockKey(id)
	if err := lockAdvisory(ctx, conn, lockKey); err != nil {
		conn.Release()
		return err
	}
	defer releaseAdvisoryLocks(conn, []string{lockKey})
	var objectKey uuid.UUID
	err = conn.QueryRow(ctx, `SELECT object_key FROM core_staged_objects WHERE id=$1`, id).Scan(&objectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return purgeStagedLocked(ctx, conn, store, id, objectKey)
}

func purgeStagedLocked(ctx context.Context, conn *pgxpool.Conn, store ObjectWriter, id, objectKey uuid.UUID) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `UPDATE core_staged_objects SET state='deleting',updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := store.Delete(ctx, objectKey.String()); err != nil {
		return fmt.Errorf("delete staged object bytes: %w", err)
	}
	_, err = conn.Exec(ctx, `DELETE FROM core_staged_objects WHERE id=$1 AND state='deleting'`, id)
	return err
}

func recoverStaged(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, before time.Time, limit int) error {
	if limit <= 0 {
		return nil
	}
	rows, err := pool.Query(ctx, `SELECT id,object_key::text FROM core_staged_objects
		WHERE state IN ('pending','deleting','failed') AND updated_at<$1
		ORDER BY updated_at,id LIMIT $2`, before, limit)
	if err != nil {
		return err
	}
	type candidate struct {
		id  uuid.UUID
		key string
	}
	var items []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.key); err != nil {
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
		if err := recoverOneStaged(ctx, pool, store, item.id, item.key, before); err != nil {
			return err
		}
	}
	return nil
}

func recoverOneStaged(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, id uuid.UUID, objectKey string, before time.Time) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	lockKey := stagedLockKey(id)
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, lockKey).Scan(&locked); err != nil {
		conn.Release()
		return err
	}
	if !locked {
		conn.Release()
		return nil
	}
	defer releaseAdvisoryLocks(conn, []string{lockKey})
	var state string
	err = conn.QueryRow(ctx, `SELECT state FROM core_staged_objects WHERE id=$1 AND updated_at<$2 FOR UPDATE`, id, before).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "pending" && state != "deleting" && state != "failed" {
		return nil
	}
	if err := store.Delete(ctx, objectKey); err != nil {
		return fmt.Errorf("remove expired staged object bytes: %w", err)
	}
	_, err = conn.Exec(ctx, `DELETE FROM core_staged_objects WHERE id=$1 AND updated_at<$2`, id, before)
	return err
}

func markStagedFailure(conn *pgxpool.Conn, store ObjectWriter, id, objectKey uuid.UUID) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = store.Delete(cleanupCtx, objectKey.String())
	_, _ = conn.Exec(cleanupCtx, `UPDATE core_staged_objects SET state='failed',updated_at=now() WHERE id=$1 AND state='pending'`, id)
}

func stagedLockKey(id uuid.UUID) string { return "core-stage:" + id.String() }

func parseSHA256(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value {
		return nil, errors.New("invalid SHA-256")
	}
	return decoded, nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type lockedStagedReader struct {
	io.ReadCloser
	conn     *pgxpool.Conn
	lockKey  string
	once     sync.Once
	closeErr error
}

func (r *lockedStagedReader) Close() error {
	r.once.Do(func() {
		r.closeErr = r.ReadCloser.Close()
		releaseAdvisoryLocks(r.conn, []string{r.lockKey})
	})
	return r.closeErr
}
