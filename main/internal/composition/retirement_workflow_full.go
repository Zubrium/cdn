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
	retirementHandlerCore     atomic.Pointer[coreComponent]
	retirementHandlerRouter   atomic.Pointer[routerEngineComponent]
)

func setRetirementWorkflowService(service *retirement.Service) {
	retirementWorkflowService.Store(service)
}

func setRetirementHandlerCore(component *coreComponent) {
	retirementHandlerCore.Store(component)
}

func setRetirementHandlerRouter(router *routerEngineComponent) {
	retirementHandlerRouter.Store(router)
}

func currentRetirementHandlerCore() *coreComponent { return retirementHandlerCore.Load() }

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
	result, removeErr := handler.removeRoute(ctx, payload)
	state, revision, code := retirementRouteOutcome(removeErr, result.Revision)
	if err := service.RecordRoute(ctx, payload.JobID, payload.RouteID, state, revision, code); err != nil {
		return contracts.OperationOutcome{}, err
	}
	if removeErr != nil {
		return contracts.OperationOutcome{}, removeErr
	}
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
	if job.PageID != payload.PageID {
		return contracts.OperationOutcome{}, fmt.Errorf("%w: finalize payload does not match durable job", retirement.ErrInvalid)
	}
	for _, route := range job.Routes {
		if route.State != contracts.RetirementRouteStateRemoved {
			return contracts.OperationOutcome{}, fmt.Errorf("%w: routes are still active", retirement.ErrConflict)
		}
	}
	finalizeErr := handler.finalizePage(ctx, payload, job)
	if finalizeErr != nil {
		if _, err := service.SetJobState(ctx, payload.JobID, contracts.RetirementJobStateRetryable); err != nil {
			return contracts.OperationOutcome{}, err
		}
		return contracts.OperationOutcome{}, finalizeErr
	}
	if _, err := service.SetJobState(ctx, payload.JobID, contracts.RetirementJobStateSucceeded); err != nil {
		return contracts.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(contracts.RetirementPageFinalizeResult{
		Format: contracts.RetirementFinalizeResultFormat, JobID: payload.JobID,
		PageID: payload.PageID, State: contracts.RetirementJobStateSucceeded,
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
	if page.ID != payload.PageID || page.RetirementJobID != payload.JobID ||
		page.LifecycleState != contracts.RetirementPageStateRetiring || page.Revision != payload.ExpectedRevision {
		return retirement.ErrInvalid
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

// markPageRetiring advances Website lifecycle only after the durable job has
// been committed and only for a first-time trigger. A retry must not overwrite
// the lifecycle token that the durable job already owns.
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
	page, err := website.service.GetPage(websiteCtx, job.PageID)
	if err != nil {
		return fmt.Errorf("retirement page read: %w", err)
	}
	if page.ID != job.PageID || page.Revision != job.ExpectedRevision ||
		page.LifecycleState != contracts.RetirementPageStateActive || page.RetirementJobID != "" {
		return retirement.ErrConflict
	}
	_, err = website.service.RetirePage(websiteCtx, websiteapi.RetirePageRequest{
		PageID: page.ID, ExpectedRevision: page.Revision,
		LifecycleState: contracts.RetirementPageStateRetiring, RetirementJobID: job.ID,
		IdempotencyKey: job.IdempotencyKey,
	})
	if err != nil {
		if _, updateErr := retirementWorkflowService.Load().SetJobState(ctx, job.ID, contracts.RetirementJobStateRetryable); updateErr != nil {
			return updateErr
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
