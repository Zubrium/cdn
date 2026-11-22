package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

func TestGetCoordinatorTaskResultValidatesOwnerScopesWithoutDatabase(t *testing.T) {
	owner, workflowID, taskID := uuid.New(), uuid.New(), uuid.New()
	for _, test := range []struct {
		name     string
		ctx      context.Context
		pool     bool
		engineID string
		owner    uuid.UUID
		epoch    int64
		workflow uuid.UUID
		task     uuid.UUID
	}{
		{name: "nil context", ctx: nil, pool: true, engineID: "engine.website", owner: owner, epoch: 1, workflow: workflowID, task: taskID},
		{name: "nil pool", ctx: context.Background(), engineID: "engine.website", owner: owner, epoch: 1, workflow: workflowID, task: taskID},
		{name: "invalid engine", ctx: context.Background(), pool: true, engineID: "engine.website!", owner: owner, epoch: 1, workflow: workflowID, task: taskID},
		{name: "nil owner", ctx: context.Background(), pool: true, engineID: "engine.website", epoch: 1, workflow: workflowID, task: taskID},
		{name: "invalid epoch", ctx: context.Background(), pool: true, engineID: "engine.website", owner: owner, workflow: workflowID, task: taskID},
		{name: "nil workflow", ctx: context.Background(), pool: true, engineID: "engine.website", owner: owner, epoch: 1, task: taskID},
		{name: "nil task", ctx: context.Background(), pool: true, engineID: "engine.website", owner: owner, epoch: 1, workflow: workflowID},
	} {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.pool {
				_, err = GetCoordinatorTaskResult(test.ctx, nil, test.engineID, test.owner, test.epoch, test.workflow, test.task)
			} else {
				_, err = CoordinatorStore{}.GetTaskResult(test.ctx, test.engineID, test.owner, test.epoch, test.workflow, test.task)
			}
			if !errors.Is(err, coordination.ErrInvalid) {
				t.Fatalf("invalid owner-scoped read was not rejected: %v", err)
			}
		})
	}
}
