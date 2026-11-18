package maintenance

import (
	"context"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/domain/maintenance"
)

var ErrInvalid = errors.New("invalid Core maintenance operation")

type Service struct {
	repository maintenance.Repository
	pendingTTL time.Duration
	batchSize  int
}

func NewService(repository maintenance.Repository, pendingTTL time.Duration, batchSize int) (*Service, error) {
	if repository == nil || pendingTTL <= 0 || batchSize <= 0 {
		return nil, ErrInvalid
	}
	return &Service{repository: repository, pendingTTL: pendingTTL, batchSize: batchSize}, nil
}

func (s *Service) Run(ctx context.Context, now time.Time) error {
	if s == nil || s.repository == nil || ctx == nil || now.IsZero() {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.repository.RecoverPending(ctx, now.Add(-s.pendingTTL), s.batchSize); err != nil {
		return err
	}
	if _, err := s.repository.PruneSessions(ctx, now, s.batchSize); err != nil {
		return err
	}
	_, err := s.repository.PruneWorkflows(ctx, s.batchSize)
	return err
}
