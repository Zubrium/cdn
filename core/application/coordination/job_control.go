package coordinator

import (
	"context"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

const maxJobListLimit = 200

// JobControlStore is the narrow Core surface needed by an already-authorized
// operator API. It cannot claim work or read payload/result details.
type JobControlStore = corecoord.JobControlRepository

type JobControlConfig struct {
	Store       JobControlStore
	Owner       uuid.UUID
	ConfigEpoch int64
}

type JobListFilter struct {
	Type   string
	State  string
	Limit  int
	Before *corecoord.CoordinatorJobCursor
}

// JobControl is a scoped capability for an Admin API. Construction does not
// authenticate callers: the presentation layer must authorize every request
// before invoking List or Retry.
type JobControl struct {
	store       JobControlStore
	owner       uuid.UUID
	configEpoch int64
}

func NewJobControl(config JobControlConfig) (*JobControl, error) {
	if isNilInterface(config.Store) || config.Owner == uuid.Nil || config.ConfigEpoch <= 0 {
		return nil, ErrInvalid
	}
	return &JobControl{store: config.Store, owner: config.Owner,
		configEpoch: config.ConfigEpoch}, nil
}

func (c *JobControl) List(ctx context.Context, filter JobListFilter) (corecoord.CoordinatorJobPage, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil ||
		c.owner == uuid.Nil || c.configEpoch <= 0 || filter.Limit < 1 || filter.Limit > maxJobListLimit ||
		(filter.Type != "" && !taskTypePattern.MatchString(filter.Type)) ||
		(filter.State != "" && !validCoordinatorJobState(filter.State)) {
		return corecoord.CoordinatorJobPage{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return corecoord.CoordinatorJobPage{}, err
	}
	var before *corecoord.CoordinatorJobCursor
	if filter.Before != nil {
		if filter.Before.ID == uuid.Nil || filter.Before.CreatedAt.IsZero() {
			return corecoord.CoordinatorJobPage{}, ErrInvalid
		}
		cursor := *filter.Before
		before = &cursor
	}
	return c.store.ListJobs(ctx, corecoord.CoordinatorJobListRequest{
		Owner: c.owner, ConfigEpoch: c.configEpoch,
		Type: filter.Type, State: filter.State, Limit: filter.Limit, Before: before,
	})
}

func (c *JobControl) Retry(ctx context.Context, jobID uuid.UUID) error {
	if c == nil || isNilInterface(c.store) || ctx == nil ||
		c.owner == uuid.Nil || c.configEpoch <= 0 || jobID == uuid.Nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.Retry(ctx, c.owner, jobID, c.configEpoch)
}

func validCoordinatorJobState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}
