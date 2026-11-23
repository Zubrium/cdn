package prerender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/Zubrium/cdn/pkg/contracts"
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
func (worker FailClosedWorker) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if task.TaskID == "" || len(task.Payload) == 0 || len(task.Payload) > 23<<20 {
		return contracts.OperationOutcome{}, ErrInvalidPlan
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return contracts.OperationOutcome{}, ErrInvalidPlan
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		(request.TaskID != "" && request.TaskID != task.TaskID) || request.IdempotencyKey != task.IdempotencyKey {
		return contracts.OperationOutcome{}, ErrInvalidPlan
	}
	request.TaskID = task.TaskID
	result, err := worker.Render(ctx, request)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return contracts.OperationOutcome{}, ErrInvalidResult
	}
	return contracts.OperationOutcome{Result: encoded}, nil
}
