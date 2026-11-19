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

// TaskHandler exposes only the statically registered render-page/v1 identity.
// This fail-closed worker never returns success because no supervised renderer
// and Core artifact writer are available in this package.
func (worker FailClosedWorker) TaskHandler(ctx context.Context, job core.CoreJob) (core.CoordinatorTaskOutcome, error) {
	if job.ComponentID != DataModuleID || job.OperationID != OperationID || job.ContractVersion != 1 || len(job.Payload) == 0 || len(job.Payload) > 23<<20 {
		return core.CoordinatorTaskOutcome{}, ErrInvalidPlan
	}
	decoder := json.NewDecoder(bytes.NewReader(job.Payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return core.CoordinatorTaskOutcome{}, ErrInvalidPlan
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || request.JobID != job.ID.String() || request.IdempotencyKey != job.IdempotencyKey {
		return core.CoordinatorTaskOutcome{}, ErrInvalidPlan
	}
	result, err := worker.Render(ctx, request)
	if err != nil {
		return core.CoordinatorTaskOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return core.CoordinatorTaskOutcome{}, ErrInvalidResult
	}
	return core.CoordinatorTaskOutcome{Result: encoded}, nil
}
