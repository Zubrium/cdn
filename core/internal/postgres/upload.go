package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UploadNew reserves a new immutable object row, streams its bytes to private
// object storage, then publishes the object and idempotency result atomically.
func UploadNew(ctx context.Context, pool *pgxpool.Pool, store ObjectWriter, actor access.Principal, requestKey string, groups []string, metadata map[string]string, maxMetadataBytes int64, contentType string, size int64, expectedSHA256 string, body io.Reader) (uuid.UUID, error) {
	if pool == nil || store == nil || !actor.Active || body == nil || size < 0 || !storage.ValidUUID(requestKey) || !storage.ValidGroups(groups) || !storage.ValidContentType(contentType) {
		return uuid.Nil, errors.New("invalid upload")
	}
	if !storage.ValidMetadata(metadata, maxMetadataBytes) {
		return uuid.Nil, errors.New("invalid upload metadata")
	}
	var expectedDigest []byte
	if expectedSHA256 != "" {
		var checksumErr error
		expectedDigest, checksumErr = parseSHA256(expectedSHA256)
		if checksumErr != nil {
			return uuid.Nil, errors.New("invalid expected upload checksum")
		}
	}
	principalID, err := uuid.Parse(actor.ID)
	if err != nil || principalID == uuid.Nil {
		return uuid.Nil, ErrUnauthorized
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return uuid.Nil, err
	}
	requestID, _ := uuid.Parse(requestKey)
	groupCopy := append([]string(nil), groups...)
	sort.Strings(groupCopy)
	descriptor, err := json.Marshal(struct {
		Groups         []string          `json:"groups"`
		Metadata       map[string]string `json:"metadata"`
		ContentType    string            `json:"contentType"`
		Size           int64             `json:"size"`
		ExpectedSHA256 string            `json:"expectedSha256,omitempty"`
	}{groupCopy, metadata, contentType, size, expectedSHA256})
	if err != nil {
		return uuid.Nil, err
	}
	descriptorHash := sha256.Sum256(descriptor)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	requestLock := "core-upload:" + principalID.String() + ":" + requestID.String()
	if err := lockAdvisory(ctx, conn, requestLock); err != nil {
		conn.Release()
		return uuid.Nil, err
	}
	locks := []string{requestLock}
	defer func() { releaseAdvisoryLocks(conn, locks) }()
	accessTx, err := conn.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if err := requireUploadAccess(ctx, accessTx, principalID, groups); err != nil {
		_ = accessTx.Rollback(context.Background())
		return uuid.Nil, err
	}
	if err := accessTx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}

	var existingHash []byte
	var existingID, existingState string
	err = conn.QueryRow(ctx, `SELECT request_hash,COALESCE(object_id::text,''),state FROM core_upload_requests WHERE principal_id=$1 AND request_key=$2`, principalID, requestID).
		Scan(&existingHash, &existingID, &existingState)
	requestExists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	if requestExists {
		switch existingState {
		case "pending":
			return uuid.Nil, storage.ErrUploadInProgress
		case "deleted":
			return uuid.Nil, storage.ErrRequestDeleted
		case "ready":
			return compareIdempotentBody(body, size, descriptorHash, existingHash, existingID)
		case "failed":
			if !bytes.Equal(existingHash, descriptorHash[:]) {
				return uuid.Nil, storage.ErrRequestConflict
			}
		default:
			return uuid.Nil, errors.New("invalid upload request state")
		}
	}

	objectID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, err
	}
	objectKey, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, err
	}
	if err := lockAdvisory(ctx, conn, objectKey.String()); err != nil {
		return uuid.Nil, err
	}
	locks = append(locks, objectKey.String())

	tx, err := conn.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireUploadAccess(ctx, tx, principalID, groups); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core_objects(id,object_key,metadata,content_type,size_bytes,revision,state)
		VALUES($1,$2,$3::jsonb,$4,$5,1,'pending')`, objectID, objectKey, string(metadataJSON), contentType, size); err != nil {
		return uuid.Nil, err
	}
	for _, group := range groups {
		if _, err := tx.Exec(ctx, `INSERT INTO core_file_groups(object_id,group_key) VALUES($1,$2)`, objectID, group); err != nil {
			return uuid.Nil, err
		}
	}
	if !requestExists {
		if _, err := tx.Exec(ctx, `INSERT INTO core_upload_requests(principal_id,request_key,request_hash,object_id,state)
			VALUES($1,$2,$3,$4,'pending')`, principalID, requestID, descriptorHash[:], objectID); err != nil {
			return uuid.Nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE core_upload_requests SET object_id=$3,state='pending',request_hash=$4,updated_at=now()
			WHERE principal_id=$1 AND request_key=$2 AND state='failed'`, principalID, requestID, objectID, descriptorHash[:]); err != nil {
			return uuid.Nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}

	bodyHash := sha256.New()
	if err := store.Put(ctx, objectKey.String(), io.TeeReader(body, bodyHash), size, contentType); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = store.Delete(cleanupCtx, objectKey.String())
		_, markErr := conn.Exec(cleanupCtx, `UPDATE core_objects SET state='failed',updated_at=now() WHERE id=$1 AND state='pending'`, objectID)
		if markErr == nil {
			_, markErr = conn.Exec(cleanupCtx, `UPDATE core_upload_requests SET state='failed',updated_at=now() WHERE principal_id=$1 AND request_key=$2 AND object_id=$3`, principalID, requestID, objectID)
		}
		cancel()
		if markErr != nil {
			return uuid.Nil, fmt.Errorf("upload failed: %v; mark failed reservation: %w", err, markErr)
		}
		return uuid.Nil, fmt.Errorf("upload object: %w", err)
	}
	if expectedDigest != nil && !bytes.Equal(bodyHash.Sum(nil), expectedDigest) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = store.Delete(cleanupCtx, objectKey.String())
		_, markErr := conn.Exec(cleanupCtx, `UPDATE core.core_objects SET state='failed',updated_at=now() WHERE id=$1 AND state='pending'`, objectID)
		if markErr == nil {
			_, markErr = conn.Exec(cleanupCtx, `UPDATE core.core_upload_requests SET state='failed',updated_at=now() WHERE principal_id=$1 AND request_key=$2 AND object_id=$3`, principalID, requestID, objectID)
		}
		cancel()
		if markErr != nil {
			return uuid.Nil, fmt.Errorf("checksum mismatch; mark failed reservation: %w", markErr)
		}
		return uuid.Nil, storage.ErrChecksumMismatch
	}
	requestHash := combineUploadHash(descriptorHash, bodyHash)
	finalTx, err := conn.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = finalTx.Rollback(context.Background()) }()
	if err := requireUploadAccess(ctx, finalTx, principalID, groups); err != nil {
		_ = finalTx.Rollback(context.Background())
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = store.Delete(cleanupCtx, objectKey.String())
		_, markErr := conn.Exec(cleanupCtx, `UPDATE core_objects SET state='failed',updated_at=now() WHERE id=$1 AND state='pending'`, objectID)
		if markErr == nil {
			_, markErr = conn.Exec(cleanupCtx, `UPDATE core_upload_requests SET state='failed',updated_at=now() WHERE principal_id=$1 AND request_key=$2 AND object_id=$3`, principalID, requestID, objectID)
		}
		cancel()
		if markErr != nil {
			return uuid.Nil, fmt.Errorf("upload authorization at commit: %v; mark failed reservation: %w", err, markErr)
		}
		return uuid.Nil, fmt.Errorf("upload authorization at commit: %w", err)
	}
	if expectedDigest == nil {
		// Legacy Core callers still persist the computed digest. Loader uses the
		// verified path and must present the digest it committed against.
		expectedDigest = bodyHash.Sum(nil)
	}
	tag, err := finalTx.Exec(ctx, `UPDATE core_objects SET state='ready',sha256=$2,updated_at=now() WHERE id=$1 AND state='pending'`, objectID, expectedDigest)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() != 1 {
		return uuid.Nil, errors.New("upload reservation lost")
	}
	tag, err = finalTx.Exec(ctx, `UPDATE core_upload_requests SET request_hash=$3,state='ready',updated_at=now()
		WHERE principal_id=$1 AND request_key=$2 AND object_id=$4 AND state='pending'`, principalID, requestID, requestHash, objectID)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() != 1 {
		return uuid.Nil, errors.New("upload request reservation lost")
	}
	if _, err := finalTx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation',$1)`, objectID.String()); err != nil {
		return uuid.Nil, err
	}
	if err := finalTx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return objectID, nil
}

func requireUploadAccess(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, groups []string) error {
	if err := requireActivePrincipal(ctx, tx, principalID); err != nil {
		return err
	}
	for _, group := range groups {
		if err := requireGroupWriteForActive(ctx, tx, principalID, group); err != nil {
			return err
		}
	}
	return nil
}

func compareIdempotentBody(body io.Reader, size int64, descriptorHash [32]byte, storedHash []byte, objectID string) (uuid.UUID, error) {
	if size < 0 || body == nil {
		return uuid.Nil, storage.ErrRequestConflict
	}
	bodyHash := sha256.New()
	count, err := io.Copy(bodyHashWriter{bodyHash}, io.LimitReader(body, size+1))
	if err != nil {
		return uuid.Nil, fmt.Errorf("check idempotent upload body: %w", err)
	}
	if count != size {
		return uuid.Nil, storage.ErrRequestConflict
	}
	var extra [1]byte
	n, err := body.Read(extra[:])
	if n != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return uuid.Nil, storage.ErrRequestConflict
	}
	want := combineUploadHash(descriptorHash, bodyHash)
	if !bytes.Equal(want, storedHash) {
		return uuid.Nil, storage.ErrRequestConflict
	}
	id, err := uuid.Parse(objectID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid stored upload result")
	}
	return id, nil
}

type bodyHashWriter struct{ hash.Hash }

func combineUploadHash(descriptor [32]byte, body hash.Hash) []byte {
	combined := sha256.New()
	_, _ = combined.Write(descriptor[:])
	_, _ = combined.Write(body.Sum(nil))
	return combined.Sum(nil)
}

func lockAdvisory(ctx context.Context, conn *pgxpool.Conn, key string) error {
	_, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key)
	return err
}

func releaseAdvisoryLocks(conn *pgxpool.Conn, keys []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := len(keys) - 1; i >= 0; i-- {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, keys[i]).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Hijack().Close(ctx)
			return
		}
	}
	conn.Release()
}
