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

func (s CoordinatorStore) Claim(ctx context.Context, owner uuid.UUID, epoch int64, limit int, lease, retention time.Duration) ([]coordination.Task, error) {
	if !s.valid() {
		return nil, coordination.ErrInvalid
	}
	return ClaimTasks(ctx, s.Pool, owner, epoch, limit, lease, retention)
}

func (s CoordinatorStore) Extend(ctx context.Context, taskID, owner uuid.UUID, epoch, token int64, lease time.Duration) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return ExtendTaskLease(ctx, s.Pool, taskID, owner, epoch, token, lease)
}

func (s CoordinatorStore) Complete(ctx context.Context, taskID, owner uuid.UUID, epoch, token int64, maxTasks, maxChildren int, retention time.Duration, outcome coordination.TaskOutcome) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return CompleteCoordinatorTask(ctx, s.Pool, taskID, owner, epoch, token, maxTasks, maxChildren, retention, outcome)
}

func (s CoordinatorStore) Fail(ctx context.Context, taskID, owner uuid.UUID, epoch, token int64, errorCode string, retryable bool, retryAfter, retention time.Duration) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return FailTask(ctx, s.Pool, taskID, owner, epoch, token, errorCode, retryable, retryAfter, retention)
}

func (s CoordinatorStore) RetryTask(ctx context.Context, owner, workflowID, taskID uuid.UUID, epoch int64) error {
	if !s.valid() {
		return coordination.ErrInvalid
	}
	return RetryTask(ctx, s.Pool, owner, workflowID, taskID, epoch)
}

func (s CoordinatorStore) ListWorkflows(ctx context.Context, request coordination.WorkflowListRequest) (coordination.WorkflowPage, error) {
	if !s.valid() {
		return coordination.WorkflowPage{}, coordination.ErrInvalid
	}
	return ListCoordinatorWorkflows(ctx, s.Pool, request)
}

func (s CoordinatorStore) GetWorkflowDetail(ctx context.Context, owner uuid.UUID, epoch int64, workflowID uuid.UUID, limit int, before *coordination.TaskCursor) (coordination.WorkflowDetail, error) {
	if !s.valid() {
		return coordination.WorkflowDetail{}, coordination.ErrInvalid
	}
	return GetCoordinatorWorkflowDetail(ctx, s.Pool, owner, epoch, workflowID, limit, before)
}

var _ interface {
	RegisterInstance(context.Context, uuid.UUID, string, time.Duration, time.Duration) (int64, error)
	RenewInstance(context.Context, uuid.UUID, int64, time.Duration) error
	ReleaseInstance(context.Context, uuid.UUID, int64) error
	StartWorkflow(context.Context, coordination.WorkflowRequest) (coordination.Workflow, bool, error)
	GetWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID) (coordination.Workflow, error)
	CancelWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID, time.Duration) error
	ListWorkflows(context.Context, coordination.WorkflowListRequest) (coordination.WorkflowPage, error)
	GetWorkflowDetail(context.Context, uuid.UUID, int64, uuid.UUID, int, *coordination.TaskCursor) (coordination.WorkflowDetail, error)
	RetryTask(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) error
	Claim(context.Context, uuid.UUID, int64, int, time.Duration, time.Duration) ([]coordination.Task, error)
	Extend(context.Context, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error
	Complete(context.Context, uuid.UUID, uuid.UUID, int64, int64, int, int, time.Duration, coordination.TaskOutcome) error
	Fail(context.Context, uuid.UUID, uuid.UUID, int64, int64, string, bool, time.Duration, time.Duration) error
} = CoordinatorStore{}
