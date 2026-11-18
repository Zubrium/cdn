package postgres

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CoordinatorStore adapts PostgreSQL persistence to the application ports.
// It stays inside infrastructure so the Core presentation facade does not
// implement persistence adapters or expose SQL concerns to callers.
type CoordinatorStore struct{ Pool *pgxpool.Pool }

func (s CoordinatorStore) valid() bool { return s.Pool != nil }

func (s CoordinatorStore) RegisterInstance(ctx context.Context, id uuid.UUID, fingerprint string, lease, retention time.Duration) (int64, error) {
	if !s.valid() {
		return 0, coordination.ErrInvalid
	}
	return RegisterCoordinatorInstance(ctx, s.Pool, id, fingerprint, lease, retention)
}

func (s CoordinatorStore) RenewInstance(ctx context.Context, id uuid.UUID, epoch int64, lease time.Duration) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return RenewCoordinatorInstance(ctx, s.Pool, id, epoch, lease)
}

func (s CoordinatorStore) ReleaseInstance(ctx context.Context, id uuid.UUID, epoch int64) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return ReleaseCoordinatorInstance(ctx, s.Pool, id, epoch)
}

func (s CoordinatorStore) StartWorkflow(ctx context.Context, request coordination.WorkflowRequest) (coordination.Workflow, bool, error) {
	if !s.valid() {
		return coordination.Workflow{}, false, coordination.ErrInvalid
	}
	return StartCoordinatorWorkflow(ctx, s.Pool, request)
}

func (s CoordinatorStore) GetWorkflow(ctx context.Context, owner string, instanceID uuid.UUID, epoch int64, workflowID uuid.UUID) (coordination.Workflow, error) {
	if !s.valid() {
		return coordination.Workflow{}, coordination.ErrInvalid
	}
	return GetCoordinatorWorkflow(ctx, s.Pool, owner, instanceID, epoch, workflowID)
}

func (s CoordinatorStore) CancelWorkflow(ctx context.Context, owner string, instanceID uuid.UUID, epoch int64, workflowID uuid.UUID, retention time.Duration) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return CancelCoordinatorWorkflow(ctx, s.Pool, owner, instanceID, epoch, workflowID, retention)
}

func (s CoordinatorStore) Claim(ctx context.Context, owner uuid.UUID, epoch int64, limit int, lease, retention time.Duration) ([]coordination.CoordinatorJob, error) {
	if !s.valid() {
		return nil, coordination.ErrInvalid
	}
	return ClaimCoordinatorJobs(ctx, s.Pool, owner, epoch, limit, lease, retention)
}

func (s CoordinatorStore) Extend(ctx context.Context, taskID, owner uuid.UUID, epoch, token int64, lease time.Duration) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return ExtendCoordinatorJobLease(ctx, s.Pool, taskID, owner, epoch, token, lease)
}

func (s CoordinatorStore) Complete(ctx context.Context, taskID, owner uuid.UUID, epoch, token int64, maxTasks, maxChildren int, retention time.Duration, outcome coordination.CoordinatorTaskOutcome) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return CompleteCoordinatorTask(ctx, s.Pool, taskID, owner, epoch, token, maxTasks, maxChildren, retention, outcome)
}

func (s CoordinatorStore) Fail(ctx context.Context, taskID, owner uuid.UUID, epoch, token int64, errorCode string, retryable bool, retryAfter, retention time.Duration) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return FailCoordinatorJob(ctx, s.Pool, taskID, owner, epoch, token, errorCode, retryable, retryAfter, retention)
}

func (s CoordinatorStore) Retry(ctx context.Context, owner, taskID uuid.UUID, epoch int64) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return RetryCoordinatorJob(ctx, s.Pool, owner, taskID, epoch)
}

func (s CoordinatorStore) GetJob(ctx context.Context, taskID uuid.UUID) (coordination.CoordinatorJob, error) {
	if !s.valid() {
		return coordination.CoordinatorJob{}, coordination.ErrInvalid
	}
	return GetCoordinatorJob(ctx, s.Pool, taskID)
}

func (s CoordinatorStore) ListJobs(ctx context.Context, request coordination.CoordinatorJobListRequest) (coordination.CoordinatorJobPage, error) {
	if !s.valid() {
		return coordination.CoordinatorJobPage{}, coordination.ErrInvalid
	}
	return ListCoordinatorJobs(ctx, s.Pool, request)
}

var _ interface {
	RegisterInstance(context.Context, uuid.UUID, string, time.Duration, time.Duration) (int64, error)
	RenewInstance(context.Context, uuid.UUID, int64, time.Duration) error
	ReleaseInstance(context.Context, uuid.UUID, int64) error
	StartWorkflow(context.Context, coordination.WorkflowRequest) (coordination.Workflow, bool, error)
	GetWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID) (coordination.Workflow, error)
	CancelWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID, time.Duration) error
	Claim(context.Context, uuid.UUID, int64, int, time.Duration, time.Duration) ([]coordination.CoordinatorJob, error)
	Extend(context.Context, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error
	Complete(context.Context, uuid.UUID, uuid.UUID, int64, int64, int, int, time.Duration, coordination.CoordinatorTaskOutcome) error
	Fail(context.Context, uuid.UUID, uuid.UUID, int64, int64, string, bool, time.Duration, time.Duration) error
	Retry(context.Context, uuid.UUID, uuid.UUID, int64) error
	GetJob(context.Context, uuid.UUID) (coordination.CoordinatorJob, error)
	ListJobs(context.Context, coordination.CoordinatorJobListRequest) (coordination.CoordinatorJobPage, error)
} = CoordinatorStore{}
