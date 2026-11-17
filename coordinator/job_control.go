package coordinator

import (
	"context"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

const maxJobListLimit = 200

// JobControlStore is the narrow Core surface needed by an already-authorized
// operator API. It cannot claim work or read payload/result details.
type JobControlStore interface {
	ListJobs(context.Context, coreplatform.CoordinatorJobListRequest) (coreplatform.CoordinatorJobPage, error)
	Retry(context.Context, string, uuid.UUID, uuid.UUID, int64) error
}

type JobControlConfig struct {
	Store       JobControlStore
	SystemID    string
	Owner       uuid.UUID
	ConfigEpoch int64
}

type JobListFilter struct {
	Type   string
	State  string
	Limit  int
	Before *coreplatform.CoordinatorJobCursor
}

// JobControl is a scoped capability for an Admin API. Construction does not
// authenticate callers: the presentation layer must authorize every request
// before invoking List or Retry.
type JobControl struct {
	store       JobControlStore
	systemID    string
	owner       uuid.UUID
	configEpoch int64
}

func NewJobControl(config JobControlConfig) (*JobControl, error) {
	if isNilInterface(config.Store) || !coordinatorSystemIDPattern.MatchString(config.SystemID) ||
		config.Owner == uuid.Nil || config.ConfigEpoch <= 0 {
		return nil, ErrInvalid
	}
	return &JobControl{store: config.Store, systemID: config.SystemID, owner: config.Owner,
		configEpoch: config.ConfigEpoch}, nil
}

func (c *JobControl) List(ctx context.Context, filter JobListFilter) (coreplatform.CoordinatorJobPage, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil || !coordinatorSystemIDPattern.MatchString(c.systemID) ||
		c.owner == uuid.Nil || c.configEpoch <= 0 || filter.Limit < 1 || filter.Limit > maxJobListLimit ||
		(filter.Type != "" && !taskTypePattern.MatchString(filter.Type)) ||
		(filter.State != "" && !validCoordinatorJobState(filter.State)) {
		return coreplatform.CoordinatorJobPage{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return coreplatform.CoordinatorJobPage{}, err
	}
	var before *coreplatform.CoordinatorJobCursor
	if filter.Before != nil {
		if filter.Before.ID == uuid.Nil || filter.Before.CreatedAt.IsZero() {
			return coreplatform.CoordinatorJobPage{}, ErrInvalid
		}
		cursor := *filter.Before
		before = &cursor
	}
	return c.store.ListJobs(ctx, coreplatform.CoordinatorJobListRequest{
		SystemID: c.systemID, Owner: c.owner, ConfigEpoch: c.configEpoch,
		Type: filter.Type, State: filter.State, Limit: filter.Limit, Before: before,
	})
}

func (c *JobControl) Retry(ctx context.Context, jobID uuid.UUID) error {
	if c == nil || isNilInterface(c.store) || ctx == nil || !coordinatorSystemIDPattern.MatchString(c.systemID) ||
		c.owner == uuid.Nil || c.configEpoch <= 0 || jobID == uuid.Nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.Retry(ctx, c.systemID, c.owner, jobID, c.configEpoch)
}

func validCoordinatorJobState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}
