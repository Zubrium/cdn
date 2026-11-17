package platform

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/internal/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCoordinatorConflict        = postgres.ErrCoordinatorConflict
	ErrCoordinatorLeaseLost       = postgres.ErrCoordinatorLeaseLost
	ErrCoordinatorConfigDiff      = postgres.ErrCoordinatorConfigDiff
	ErrCoordinatorJobMissing      = postgres.ErrCoordinatorJobMissing
	ErrCoordinatorJobOwned        = postgres.ErrCoordinatorJobNotOwned
	ErrCoordinatorStale           = postgres.ErrCoordinatorStale
	ErrRouteTargetMissing         = postgres.ErrRouteTargetMissing
	ErrRouteTargetFileUnavailable = postgres.ErrRouteTargetFileUnavailable
	ErrCoordinatorInvalid         = postgres.ErrCoordinatorInvalid
)

type CoordinatorJob = postgres.CoordinatorJob
type CoordinatorJobRequest = postgres.CoordinatorJobRequest
type CoordinatorPublicationRequest = postgres.CoordinatorPublicationRequest
type CoordinatorRouteTarget = postgres.CoordinatorRouteTarget
type RouteTarget = postgres.CoordinatorRouteTarget
type CoordinatorJobCursor = postgres.CoordinatorJobCursor
type CoordinatorJobSummary = postgres.CoordinatorJobSummary
type CoordinatorJobListRequest = postgres.CoordinatorJobListRequest
type CoordinatorJobPage = postgres.CoordinatorJobPage

// CoordinatorStore exposes the small cross-instance coordination surface
// needed by the composition root. It has no SQL, schema, or S3 capabilities.
type CoordinatorStore struct{ pool *pgxpool.Pool }

// CoordinatorStore returns the Core-owned durable state capability. Core
// remains the only module that owns database access.
func (r *Runtime) CoordinatorStore() (*CoordinatorStore, error) {
	if err := r.valid(); err != nil {
		return nil, err
	}
	if r.Registry == nil {
		return nil, ErrCoordinatorInvalid
	}
	if _, err := r.Registry.Lookup("coordinator"); err != nil {
		return nil, ErrCoordinatorInvalid
	}
	r.moduleDataMu.Lock()
	defer r.moduleDataMu.Unlock()
	if r.coordinatorStoreIssued {
		return nil, ErrCoordinatorInvalid
	}
	r.coordinatorStoreIssued = true
	return &CoordinatorStore{pool: r.database}, nil
}

func (s *CoordinatorStore) RegisterInstance(ctx context.Context, systemID string, instanceID uuid.UUID, fingerprint string, lease time.Duration) (int64, error) {
	if s == nil {
		return 0, ErrCoordinatorInvalid
	}
	return postgres.RegisterCoordinatorInstance(ctx, s.pool, systemID, instanceID, fingerprint, lease)
}

func (s *CoordinatorStore) RenewInstance(ctx context.Context, systemID string, instanceID uuid.UUID, epoch int64, lease time.Duration) error {
	if s == nil {
		return ErrCoordinatorInvalid
	}
	return postgres.RenewCoordinatorInstance(ctx, s.pool, systemID, instanceID, epoch, lease)
}

func (s *CoordinatorStore) ReleaseInstance(ctx context.Context, systemID string, instanceID uuid.UUID, epoch int64) error {
	if s == nil {
		return ErrCoordinatorInvalid
	}
	return postgres.ReleaseCoordinatorInstance(ctx, s.pool, systemID, instanceID, epoch)
}

func (s *CoordinatorStore) Enqueue(ctx context.Context, request CoordinatorJobRequest) (CoordinatorJob, bool, error) {
	if s == nil {
		return CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	return postgres.EnqueueCoordinatorJob(ctx, s.pool, request)
}

func (s *CoordinatorStore) Claim(ctx context.Context, systemID string, owner uuid.UUID, epoch int64, limit int, lease time.Duration) ([]CoordinatorJob, error) {
	if s == nil {
		return nil, ErrCoordinatorInvalid
	}
	return postgres.ClaimCoordinatorJobs(ctx, s.pool, systemID, owner, epoch, limit, lease)
}

func (s *CoordinatorStore) Extend(ctx context.Context, systemID string, jobID, owner uuid.UUID, epoch, token int64, lease time.Duration) error {
	if s == nil {
		return ErrCoordinatorInvalid
	}
	return postgres.ExtendCoordinatorJobLease(ctx, s.pool, systemID, jobID, owner, epoch, token, lease)
}

func (s *CoordinatorStore) Complete(ctx context.Context, systemID string, jobID, owner uuid.UUID, epoch, token int64, result json.RawMessage) error {
	if s == nil {
		return ErrCoordinatorInvalid
	}
	return postgres.CompleteCoordinatorJob(ctx, s.pool, systemID, jobID, owner, epoch, token, result)
}

func (s *CoordinatorStore) Fail(ctx context.Context, systemID string, jobID, owner uuid.UUID, epoch, token int64, errorCode string, retryable bool, retryAfter time.Duration) error {
	if s == nil {
		return ErrCoordinatorInvalid
	}
	return postgres.FailCoordinatorJob(ctx, s.pool, systemID, jobID, owner, epoch, token, errorCode, retryable, retryAfter)
}

func (s *CoordinatorStore) Retry(ctx context.Context, systemID string, owner, jobID uuid.UUID, epoch int64) error {
	if s == nil {
		return ErrCoordinatorInvalid
	}
	return postgres.RetryCoordinatorJob(ctx, s.pool, systemID, owner, jobID, epoch)
}

func (s *CoordinatorStore) GetJob(ctx context.Context, systemID string, jobID uuid.UUID) (CoordinatorJob, error) {
	if s == nil {
		return CoordinatorJob{}, ErrCoordinatorInvalid
	}
	return postgres.GetCoordinatorJob(ctx, s.pool, systemID, jobID)
}

func (s *CoordinatorStore) ListJobs(ctx context.Context, request CoordinatorJobListRequest) (CoordinatorJobPage, error) {
	if s == nil {
		return CoordinatorJobPage{}, ErrCoordinatorInvalid
	}
	return postgres.ListCoordinatorJobs(ctx, s.pool, request)
}

func (s *CoordinatorStore) SchedulePublication(ctx context.Context, request CoordinatorPublicationRequest) (int64, CoordinatorJob, bool, error) {
	if s == nil {
		return 0, CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	return postgres.ScheduleCoordinatorPublication(ctx, s.pool, request)
}

// ActivatePublication performs a fenced generation check and target switch in
// one Core transaction; separate check-then-write calls are not equivalent.
func (s *CoordinatorStore) ActivatePublication(ctx context.Context, systemID string, routeID, jobID, owner uuid.UUID, epoch, fencingToken, generation int64, fileID uuid.UUID) (CoordinatorRouteTarget, error) {
	if s == nil {
		return CoordinatorRouteTarget{}, ErrCoordinatorInvalid
	}
	return postgres.ActivateCoordinatorPublication(ctx, s.pool, systemID, routeID, jobID, owner, epoch, fencingToken, generation, fileID)
}

func IsCoordinatorConflict(err error) bool {
	return errors.Is(err, ErrCoordinatorConflict) || errors.Is(err, ErrCoordinatorStale)
}
