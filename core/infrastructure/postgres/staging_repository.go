package postgres

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/files"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StagingRepository adapts the Core upload staging tables and S3 byte store
// to the application port. It exposes only opaque handles and bounded streams.
type StagingRepository struct {
	Pool     *pgxpool.Pool
	Store    ObjectWriter
	MaxBytes int64
}

func (r StagingRepository) PutStaged(ctx context.Context, actor access.Principal, id string, groups []string, size int64, checksum string, body io.Reader) error {
	return PutStagedObject(ctx, r.Pool, r.Store, actor, id, groups, r.MaxBytes, size, checksum, body)
}

func (r StagingRepository) ReadStaged(ctx context.Context, actor access.Principal, id string) (io.ReadCloser, int64, string, error) {
	return ReadStagedObject(ctx, r.Pool, r.Store, actor, id)
}

func (r StagingRepository) DeleteStaged(ctx context.Context, actor access.Principal, id string) error {
	return DeleteStagedObject(ctx, r.Pool, r.Store, actor, id)
}

func (r StagingRepository) PurgeStaged(ctx context.Context, id string) error {
	return PurgeStagedObject(ctx, r.Pool, r.Store, id)
}

func (r StagingRepository) FindUploadRequest(ctx context.Context, principalID, requestKey string) (files.UploadRequestStatus, error) {
	status, err := FindUploadRequest(ctx, r.Pool, principalID, requestKey)
	return files.UploadRequestStatus{State: status.State, FileID: status.FileID}, err
}

var _ files.StagingRepository = StagingRepository{}
