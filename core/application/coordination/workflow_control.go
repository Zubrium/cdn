package coordinator

import (
	"context"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

const maxWorkflowPageLimit = 200
const maxWorkflowTaskPageLimit = 1000

type WorkflowControlStore = corecoord.WorkflowControlRepository

type WorkflowListFilter struct {
	State  string
	Limit  int
	Before *corecoord.WorkflowCursor
}

type WorkflowDetailFilter struct {
	Limit  int
	Before *corecoord.TaskCursor
}

// WorkflowControl is a system-scoped capability for the authorized Admin API.
// It exposes workflow summaries, a bounded task graph, and task retry only.
type WorkflowControl struct {
	store       WorkflowControlStore
	owner       uuid.UUID
	configEpoch int64
}

type WorkflowControlConfig struct {
	Store       WorkflowControlStore
	Owner       uuid.UUID
	ConfigEpoch int64
}

func NewWorkflowControl(config WorkflowControlConfig) (*WorkflowControl, error) {
	if isNilInterface(config.Store) || config.Owner == uuid.Nil || config.ConfigEpoch <= 0 {
		return nil, ErrInvalid
	}
	return &WorkflowControl{store: config.Store, owner: config.Owner, configEpoch: config.ConfigEpoch}, nil
}

func (c *WorkflowControl) List(ctx context.Context, filter WorkflowListFilter) (corecoord.WorkflowPage, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil || c.owner == uuid.Nil || c.configEpoch <= 0 ||
		filter.Limit < 1 || filter.Limit > maxWorkflowPageLimit || (filter.State != "" && !validWorkflowState(filter.State)) {
		return corecoord.WorkflowPage{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return corecoord.WorkflowPage{}, err
	}
	var before *corecoord.WorkflowCursor
	if filter.Before != nil {
		if filter.Before.ID == uuid.Nil || filter.Before.CreatedAt.IsZero() {
			return corecoord.WorkflowPage{}, ErrInvalid
		}
		cursor := *filter.Before
		before = &cursor
	}
	return c.store.ListWorkflows(ctx, corecoord.WorkflowListRequest{Owner: c.owner, ConfigEpoch: c.configEpoch,
		State: filter.State, Limit: filter.Limit, Before: before})
}

func (c *WorkflowControl) Get(ctx context.Context, id uuid.UUID, filter WorkflowDetailFilter) (corecoord.WorkflowDetail, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil || c.owner == uuid.Nil || c.configEpoch <= 0 || id == uuid.Nil ||
		filter.Limit < 1 || filter.Limit > maxWorkflowTaskPageLimit {
		return corecoord.WorkflowDetail{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return corecoord.WorkflowDetail{}, err
	}
	if filter.Before != nil && (filter.Before.ID == uuid.Nil || filter.Before.CreatedAt.IsZero()) {
		return corecoord.WorkflowDetail{}, ErrInvalid
	}
	return c.store.GetWorkflowDetail(ctx, c.owner, c.configEpoch, id, filter.Limit, filter.Before)
}

func (c *WorkflowControl) RetryTask(ctx context.Context, workflowID, taskID uuid.UUID) error {
	if c == nil || isNilInterface(c.store) || ctx == nil || c.owner == uuid.Nil || c.configEpoch <= 0 ||
		workflowID == uuid.Nil || taskID == uuid.Nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.RetryTask(ctx, c.owner, workflowID, taskID, c.configEpoch)
}

func validWorkflowState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}
