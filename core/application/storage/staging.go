package storage

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/files"
)

type StagingRepository = files.StagingRepository
type UploadRequestStatus = files.UploadRequestStatus

// StagingService bounds Core-owned temporary bytes. Durable upload sessions
// and chunk sequencing remain in the calling component's data namespace.
type StagingService struct {
	repository StagingRepository
	maxBytes   int64
}

func NewStagingService(repository StagingRepository, maxBytes int64) StagingService {
	return StagingService{repository: repository, maxBytes: maxBytes}
}

func (s StagingService) Put(ctx context.Context, actor access.Principal, id string, groups []string, size int64, checksum string, body io.Reader) error {
	if s.repository == nil {
		return ErrUnavailable
	}
	if ctx == nil {
		return files.ErrInvalid
	}
	if !actor.Active || actor.ID == "" {
		return access.ErrUnauthorized
	}
	if body == nil || s.maxBytes <= 0 || size < 0 || size > s.maxBytes ||
		!ValidUUID(id) || !ValidGroups(groups) || !ValidSHA256(checksum) {
		return files.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repository.PutStaged(ctx, actor, id, append([]string(nil), groups...), size, checksum, body)
}

func (s StagingService) Read(ctx context.Context, actor access.Principal, id string) (io.ReadCloser, int64, string, error) {
	if s.repository == nil {
		return nil, 0, "", ErrUnavailable
	}
	if ctx == nil || !actor.Active || actor.ID == "" {
		return nil, 0, "", access.ErrUnauthorized
	}
	if !ValidUUID(id) {
		return nil, 0, "", files.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, "", err
	}
	return s.repository.ReadStaged(ctx, actor, id)
}

func (s StagingService) Delete(ctx context.Context, actor access.Principal, id string) error {
	if s.repository == nil {
		return ErrUnavailable
	}
	if ctx == nil || !actor.Active || actor.ID == "" {
		return access.ErrUnauthorized
	}
	if !ValidUUID(id) {
		return files.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repository.DeleteStaged(ctx, actor, id)
}

func (s StagingService) Purge(ctx context.Context, id string) error {
	if s.repository == nil {
		return ErrUnavailable
	}
	if ctx == nil || !ValidUUID(id) {
		return files.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repository.PurgeStaged(ctx, id)
}

func (s StagingService) ResolveUploadRequest(ctx context.Context, principalID, requestKey string) (UploadRequestStatus, error) {
	if s.repository == nil {
		return UploadRequestStatus{}, ErrUnavailable
	}
	if ctx == nil || !ValidUUID(principalID) || !ValidUUID(requestKey) {
		return UploadRequestStatus{}, files.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return UploadRequestStatus{}, err
	}
	return s.repository.FindUploadRequest(ctx, principalID, requestKey)
}
