package postgres

import (
	"context"
	"errors"

	storage "github.com/Zubrium/cdn/core/domain/files"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UploadRequestStatus struct {
	State  string
	FileID string
}

// FindUploadRequest resolves an idempotent file-creation request. The caller
// provides its principal and random request key; object keys and SQL stay in Core.
func FindUploadRequest(ctx context.Context, pool *pgxpool.Pool, principalID, requestKey string) (UploadRequestStatus, error) {
	principal, principalErr := uuid.Parse(principalID)
	request, requestErr := uuid.Parse(requestKey)
	if pool == nil || principalErr != nil || principal == uuid.Nil || requestErr != nil || request == uuid.Nil {
		return UploadRequestStatus{}, errors.New("invalid upload request lookup")
	}
	var status UploadRequestStatus
	var objectID string
	err := pool.QueryRow(ctx, `SELECT state,COALESCE(object_id::text,'') FROM uploads.requests WHERE user_id=$1 AND request_key=$2`, principal, request).
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
