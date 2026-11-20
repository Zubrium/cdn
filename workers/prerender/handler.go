package prerender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	core "github.com/Zubrium/cdn/core/presentation"
)

// FailClosedWorker validates the versioned plan envelope but deliberately does
// not render or persist output. A production renderer cannot be enabled until a
// typed page/runtime projection and OS-enforced Chromium supervisor are wired.
type FailClosedWorker struct {
	Limits Limits
}

func (worker FailClosedWorker) Render(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		return Result{}, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := ValidatePlan(request, worker.Limits); err != nil {
		return Result{}, err
	}
	return Result{}, ErrRenderUnavailable
}

// WorkerHandler exposes only the statically registered render-page/v1 identity.
// This fail-closed worker never returns success because no supervised renderer
// and Core artifact writer are available in this package.
func (worker FailClosedWorker) WorkerHandler(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
	if task.Kind != core.TaskKindWorker || task.Worker == nil || task.Worker.ComponentID != DataModuleID ||
		task.Worker.OperationID != OperationID || task.Worker.ContractVersion != 1 || len(task.Payload) == 0 || len(task.Payload) > 23<<20 {
		return core.TaskOutcome{}, ErrInvalidPlan
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return core.TaskOutcome{}, ErrInvalidPlan
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		(request.TaskID != "" && request.TaskID != task.ID.String()) || request.IdempotencyKey != task.IdempotencyKey {
		return core.TaskOutcome{}, ErrInvalidPlan
	}
	request.TaskID = task.ID.String()
	result, err := worker.Render(ctx, request)
	if err != nil {
		return core.TaskOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return core.TaskOutcome{}, ErrInvalidResult
	}
	return core.TaskOutcome{Result: encoded}, nil
}
