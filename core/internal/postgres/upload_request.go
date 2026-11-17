package postgres

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/core/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UploadRequestStatus struct {
	State  string
	FileID string
}

// FindUploadRequest resolves a Core idempotency key for Loader recovery. The
// caller can provide only its own random request key; object keys and SQL stay
// inside Core.
func FindUploadRequest(ctx context.Context, pool *pgxpool.Pool, principalID, requestKey string) (UploadRequestStatus, error) {
	principal, principalErr := uuid.Parse(principalID)
	request, requestErr := uuid.Parse(requestKey)
	if pool == nil || principalErr != nil || principal == uuid.Nil || requestErr != nil || request == uuid.Nil {
		return UploadRequestStatus{}, errors.New("invalid upload request lookup")
	}
	var status UploadRequestStatus
	var objectID string
	err := pool.QueryRow(ctx, `SELECT state,COALESCE(object_id::text,'') FROM core_upload_requests WHERE principal_id=$1 AND request_key=$2`, principal, request).
		Scan(&status.State, &objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return UploadRequestStatus{}, storage.ErrNotFound
	}
	if err != nil {
		return UploadRequestStatus{}, err
	}
	status.FileID = objectID
	return status, nil
}
