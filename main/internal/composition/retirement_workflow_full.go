//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/main/internal/retirement"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	retirementWorkflowService atomic.Pointer[retirement.Service]
	retirementHandlerRouter   atomic.Pointer[routerEngineComponent]
)

func setRetirementWorkflowService(service *retirement.Service) {
	retirementWorkflowService.Store(service)
}

func setRetirementHandlerRouter(router *routerEngineComponent) {
	retirementHandlerRouter.Store(router)
}

type retirementWorkflowStarter struct{ dispatcher contracts.Dispatcher }

func (starter retirementWorkflowStarter) Start(ctx context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	if starter.dispatcher == nil {
		return contracts.WorkflowStartReceipt{}, contracts.ErrWorkflowUnavailable
	}
	return contracts.CallWorkflowStart(ctx, starter.dispatcher, input)
}

func newRetirementRootHandler(maxAttempts int) (bus.TaskOperationHandler, error) {
	handler, err := retirement.NewRetirementStartHandler(retirement.RetirementStartConfig{MaxAttempts: maxAttempts})
	if err != nil {
		return nil, err
	}
	return handler.WorkerHandler, nil
}

type retirementRouteRemoveHandler struct{ component *coreComponent }

func (handler retirementRouteRemoveHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	payload, err := contracts.DecodePayload[contracts.RetirementRouteRemovePayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if err := contracts.ValidateRetirementRouteRemovePayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	service := retirementWorkflowService.Load()
	if service == nil {
		return contracts.OperationOutcome{}, retirement.ErrUnavailable
	}
	job, err := service.LoadJob(ctx, payload.JobID, payload.PageID)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if job.Attempt != payload.Attempt || job.State == contracts.RetirementJobStateSucceeded {
		return contracts.OperationOutcome{Result: json.RawMessage(`{"state":"unchanged"}`)}, nil
	}
	if job.IdempotencyKey != payload.IdempotencyKey {
		return contracts.OperationOutcome{}, retirement.ErrInvalid
	}
	result, removeErr := handler.removeRoute(ctx, payload)
	result.State, result.Revision, result.ErrorCode = retirementRouteOutcome(removeErr, result.Revision)
	if err := service.RecordRoute(ctx, payload.JobID, payload.RouteID, payload.Attempt, result.State, result.Revision, result.ErrorCode); err != nil {
		return contracts.OperationOutcome{}, err
	}
	// A route failure is an outcome of this task. Returning it as an executor
	// error would cancel other routes and prevent the finalizer from running.
	encoded, err := json.Marshal(result)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	return contracts.OperationOutcome{Result: encoded}, nil
}

func (handler retirementRouteRemoveHandler) removeRoute(ctx context.Context, payload contracts.RetirementRouteRemovePayload) (contracts.RetirementRouteRemoveResult, error) {
	result := contracts.RetirementRouteRemoveResult{
		Format: contracts.RetirementRouteRemoveResultFormat, JobID: payload.JobID,
		RouteID: payload.RouteID, State: contracts.RetirementRouteStateRemoved,
	}
	if handler.component == nil || retirementHandlerRouter.Load() == nil || retirementHandlerRouter.Load().api == nil ||
		handler.component.config.Retirement.RouterCredential == "" {
		return result, retirement.ErrUnavailable
	}
	routerCtx, err := retirementHandlerRouter.Load().api.Authenticate(ctx, handler.component.config.Retirement.RouterCredential)
	if err != nil {
		return result, retirement.ErrUnavailable
	}
	route, err := retirementHandlerRouter.Load().api.GetRoute(routerCtx, payload.RouteID)
	if errors.Is(err, routerapi.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("retirement route read: %w", err)
	}
	if route.ID != payload.RouteID || route.State == "removed" {
		if err == nil {
			err = retirement.ErrInvalid
		}
		return result, err
	}
	err = retirementHandlerRouter.Load().api.DeleteRoute(routerCtx, routerapi.DeleteRouteRequest{
		RouteID: payload.RouteID, ExpectedRevision: route.Revision,
	})
	if err != nil && !errors.Is(err, routerapi.ErrNotFound) {
		return result, fmt.Errorf("retirement route delete: %w", err)
	}
	result.Revision = route.Revision
	return result, nil
}

func retirementRouteOutcome(err error, revision int64) (state string, recordedRevision int64, code string) {
	if err == nil {
		return contracts.RetirementRouteStateRemoved, revision, ""
	}
	if errors.Is(err, retirement.ErrInvalid) {
		return contracts.RetirementRouteStateRetryable, revision, "invalid_route"
	}
	if errors.Is(err, routerapi.ErrConflict) {
		return contracts.RetirementRouteStateRetryable, revision, "revision_conflict"
	}
	if errors.Is(err, routerapi.ErrUnavailable) {
		return contracts.RetirementRouteStateRetryable, revision, "unavailable"
	}
	return contracts.RetirementRouteStateRetryable, revision, contracts.RetirementRouteErrorCode(err)
}

type retirementFinalizeHandler struct{ component *coreComponent }

func (handler retirementFinalizeHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	payload, err := contracts.DecodePayload[contracts.RetirementPageFinalizePayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if err := contracts.ValidateRetirementPageFinalizePayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	service := retirementWorkflowService.Load()
	if service == nil {
		return contracts.OperationOutcome{}, retirement.ErrUnavailable
	}
	job, err := service.LoadJob(ctx, payload.JobID, payload.PageID)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if job.Attempt != payload.Attempt || job.State == contracts.RetirementJobStateSucceeded {
		return contracts.OperationOutcome{Result: json.RawMessage(`{"state":"unchanged"}`)}, nil
	}
	state := contracts.RetirementJobStateSucceeded
	for _, route := range job.Routes {
		if route.State != contracts.RetirementRouteStateRemoved {
			state = contracts.RetirementJobStateRetryable
			break
		}
	}
	if state == contracts.RetirementJobStateSucceeded {
		if err := handler.finalizePage(ctx, payload, job); err != nil {
			state = contracts.RetirementJobStateRetryable
		}
	}
	if _, err := service.SetJobState(ctx, payload.JobID, payload.Attempt, state); err != nil {
		return contracts.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(contracts.RetirementPageFinalizeResult{
		Format: contracts.RetirementFinalizeResultFormat, JobID: payload.JobID,
		PageID: payload.PageID, State: state,
	})
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	return contracts.OperationOutcome{Result: encoded}, nil
}

func (handler retirementFinalizeHandler) finalizePage(ctx context.Context, payload contracts.RetirementPageFinalizePayload, job retirement.Job) error {
	if handler.component == nil || handler.component.config.Retirement.WebsiteCredential == "" {
		return retirement.ErrUnavailable
	}
	website := handler.component.websiteHandle.Load()
	if website == nil || website.service == nil {
		return retirement.ErrUnavailable
	}
	websiteCtx, err := website.service.Authenticate(ctx, handler.component.config.Retirement.WebsiteCredential)
	if err != nil {
		return retirement.ErrUnavailable
	}
	page, err := website.service.GetPage(websiteCtx, payload.PageID)
	if err != nil {
		return fmt.Errorf("retirement page read: %w", err)
	}
	done, err := validateRetirementFinalization(payload, job, page)
	if err != nil || done {
		return err
	}
	_, err = website.service.RetirePage(websiteCtx, websiteapi.RetirePageRequest{
		PageID: payload.PageID, ExpectedRevision: page.Revision,
		LifecycleState: contracts.RetirementPageStateRetired, RetirementJobID: payload.JobID,
		IdempotencyKey: contracts.RetirementFinalizeIdempotencyKey(payload),
	})
	if err != nil {
		return fmt.Errorf("retirement page finalize: %w", err)
	}
	return nil
}

// The request revision belongs to the active page before retirement began.
// Entering retiring writes the page and assigns a new global record revision.
func validateRetirementFinalization(payload contracts.RetirementPageFinalizePayload, job retirement.Job, page websiteapi.Page) (bool, error) {
	if job.ID != payload.JobID || job.PageID != payload.PageID ||
		job.ExpectedRevision != payload.ExpectedRevision || job.IdempotencyKey != payload.IdempotencyKey ||
		page.ID != payload.PageID || page.RetirementJobID != payload.JobID || page.Revision <= job.ExpectedRevision {
		return false, retirement.ErrInvalid
	}
	switch page.LifecycleState {
	case contracts.RetirementPageStateRetiring:
		return false, nil
	case contracts.RetirementPageStateRetired:
		return true, nil
	default:
		return false, retirement.ErrInvalid
	}
}

// markPageRetiring ensures the durable job owns the page before any route work.
func markPageRetiring(ctx context.Context, component *coreComponent, job retirement.Job) error {
	if component == nil || component.config.Retirement.WebsiteCredential == "" {
		return retirement.ErrUnavailable
	}
	website := component.websiteHandle.Load()
	if website == nil || website.service == nil {
		return retirement.ErrUnavailable
	}
	websiteCtx, err := website.service.Authenticate(ctx, component.config.Retirement.WebsiteCredential)
	if err != nil {
		return retirement.ErrUnavailable
	}
	return ensureRetiringPage(websiteCtx, job, website.service.GetPage, website.service.RetirePage)
}

func ensureRetiringPage(ctx context.Context, job retirement.Job,
	read func(context.Context, string) (websiteapi.Page, error),
	write func(context.Context, websiteapi.RetirePageRequest) (websiteapi.Page, error),
) error {
	page, err := read(ctx, job.PageID)
	if err != nil {
		return fmt.Errorf("retirement page read: %w", err)
	}
	owned := func(page websiteapi.Page) bool {
		return page.ID == job.PageID && page.RetirementJobID == job.ID && page.Revision > job.ExpectedRevision &&
			(page.LifecycleState == contracts.RetirementPageStateRetiring || page.LifecycleState == contracts.RetirementPageStateRetired)
	}
	if owned(page) {
		return nil
	}
	if page.ID != job.PageID || page.Revision != job.ExpectedRevision ||
		page.LifecycleState != contracts.RetirementPageStateActive || page.RetirementJobID != "" {
		return retirement.ErrConflict
	}
	_, err = write(ctx, websiteapi.RetirePageRequest{
		PageID: page.ID, ExpectedRevision: page.Revision,
		LifecycleState: contracts.RetirementPageStateRetiring, RetirementJobID: job.ID,
		IdempotencyKey: job.IdempotencyKey,
	})
	if err != nil {
		if errors.Is(err, websiteapi.ErrConflict) {
			current, readErr := read(ctx, job.PageID)
			if readErr != nil {
				return fmt.Errorf("retirement page read after conflict: %w", readErr)
			}
			if owned(current) {
				return nil
			}
		}
		if errors.Is(err, websiteapi.ErrInvalid) {
			return retirement.ErrInvalid
		}
		if errors.Is(err, websiteapi.ErrConflict) || errors.Is(err, websiteapi.ErrNotFound) {
			return retirement.ErrConflict
		}
		return retirement.ErrUnavailable
	}
	return nil
}
