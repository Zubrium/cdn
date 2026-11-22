package retirement

import (
	"context"
	"errors"
	"fmt"

	core "github.com/Zubrium/cdn/core/presentation"
	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// RetirePage starts or resumes the durable withdrawal for one server-owned
// page identity. The visitor never supplies a page ID: the trusted Admin
// handler resolves it from the URL after authorization.
func (s *Service) RetirePage(ctx context.Context, request contracts.RetirePageRequest) (contracts.PageRetirement, error) {
	return s.retire(ctx, request)
}

// GetPageRetirement reads the durable status addressed by the Location header.
func (s *Service) GetPageRetirement(ctx context.Context, query contracts.PageRetirementQuery) (contracts.PageRetirement, error) {
	if s == nil || ctx == nil || !uuidPattern.MatchString(query.PageID) || !uuidPattern.MatchString(query.JobID) {
		return contracts.PageRetirement{}, fmt.Errorf("%w: invalid retirement query", ErrInvalid)
	}
	return s.status(ctx, query.JobID, query.PageID)
}

// Retry resumes a retryable job after the underlying Router dependency has
// recovered. Completed jobs are returned unchanged.
func (s *Service) Retry(ctx context.Context, jobID string) (contracts.PageRetirement, error) {
	if s == nil || ctx == nil || !uuidPattern.MatchString(jobID) {
		return contracts.PageRetirement{}, fmt.Errorf("%w: invalid job identity", ErrInvalid)
	}
	job, err := s.loadJob(ctx, jobID, "")
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	if job.State == stateSucceeded {
		return jobView(job), nil
	}
	if job.State != stateRetryable {
		return contracts.PageRetirement{}, fmt.Errorf("%w: job is not retryable", ErrConflict)
	}
	job, err = s.updateJob(ctx, job.ID, func(current *jobRecord) error {
		current.State = stateRunning
		current.UpdatedAt = timestamp()
		return nil
	})
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	return s.advance(ctx, job)
}

func (s *Service) retire(ctx context.Context, request contracts.RetirePageRequest) (contracts.PageRetirement, error) {
	if s == nil || ctx == nil || !uuidPattern.MatchString(request.PageID) ||
		!ifMatchPattern.MatchString(request.IfMatch) || !idempotentKey.MatchString(request.IdempotencyKey) {
		return contracts.PageRetirement{}, fmt.Errorf("%w: invalid retirement trigger", ErrInvalid)
	}
	expected, err := parseIfMatch(request.IfMatch)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	branch := s.config.BranchID
	// Receipts are checked before the identity tombstone so an identical retry
	// stays idempotent after the workflow has completed.
	if record, err := s.config.Store.Get(ctx, withBranch(idempotencyKey(request.PageID, request.IdempotencyKey), branch)); err == nil {
		var receipt receiptRecord
		if record.PayloadVersion != dataVersion || jsonUnmarshal(record.Payload, &receipt) != nil ||
			receipt.Kind != recordTypeReceipt || receipt.PageID != request.PageID || !uuidPattern.MatchString(receipt.JobID) {
			return contracts.PageRetirement{}, fmt.Errorf("%w: invalid retirement receipt", ErrUnavailable)
		}
		return s.status(ctx, receipt.JobID, request.PageID)
	} else if !errors.Is(err, core.ErrModuleDataNotFound) {
		return contracts.PageRetirement{}, mapStoreError(err)
	}
	if _, err := s.config.Store.Get(ctx, withBranch(tombstoneKey(request.PageID), branch)); err == nil {
		return contracts.PageRetirement{}, fmt.Errorf("%w: page identity is retired and cannot be reused", ErrConflict)
	} else if !errors.Is(err, core.ErrModuleDataNotFound) {
		return contracts.PageRetirement{}, mapStoreError(err)
	}
	routes, registered := s.config.Routes[request.PageID]
	if !registered {
		return contracts.PageRetirement{}, fmt.Errorf("%w: no registered route targets", ErrInvalid)
	}
	websiteCtx, err := s.config.Website.Authenticate(ctx, s.config.WebsiteCredential)
	if err != nil {
		return contracts.PageRetirement{}, ErrUnavailable
	}
	page, err := s.config.Website.GetPage(websiteCtx, request.PageID)
	if err != nil || page.ID != request.PageID {
		if err == nil {
			return contracts.PageRetirement{}, ErrUnavailable
		}
		return contracts.PageRetirement{}, mapWebsiteError(err)
	}
	if page.LifecycleState == "retired" || page.LifecycleState != "active" || page.Revision != expected {
		return contracts.PageRetirement{}, fmt.Errorf("%w: page revision or lifecycle mismatch", ErrConflict)
	}
	job := jobRecord{
		Kind: recordTypeJob, ID: newUUID(), PageID: request.PageID, BranchID: branch,
		IdempotencyKey: request.IdempotencyKey, State: stateRunning,
		CreatedAt: timestamp(), UpdatedAt: timestamp(),
	}
	for _, routeID := range routes {
		job.Routes = append(job.Routes, routeRecord{RouteID: routeID, State: routeStatePending})
	}
	mutations, err := jobCreationMutations(branch, request, job)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	if err := s.config.Store.Batch(ctx, mutations); err != nil {
		if errors.Is(err, core.ErrModuleDataConflict) {
			// A concurrent start with the same key committed first; the
			// durable receipt is authoritative.
			if record, readErr := s.config.Store.Get(ctx, withBranch(idempotencyKey(request.PageID, request.IdempotencyKey), branch)); readErr == nil {
				var receipt receiptRecord
				if record.PayloadVersion == dataVersion && jsonUnmarshal(record.Payload, &receipt) == nil && receipt.PageID == request.PageID {
					return s.status(ctx, receipt.JobID, request.PageID)
				}
			}
		}
		return contracts.PageRetirement{}, mapStoreError(err)
	}
	// Website lifecycle is durable but secondary to the job record: a failure
	// here leaves the job retryable and the page active, never a retired page
	// with live route targets.
	if _, err := s.config.Website.RetirePage(websiteCtx, websiteapi.RetirePageRequest{
		PageID: page.ID, ExpectedRevision: page.Revision, LifecycleState: "retiring",
		RetirementJobID: job.ID, IdempotencyKey: request.IdempotencyKey,
	}); err != nil {
		if _, putErr := s.updateJob(ctx, job.ID, func(current *jobRecord) error {
			current.State = stateRetryable
			current.UpdatedAt = timestamp()
			return nil
		}); putErr != nil {
			return contracts.PageRetirement{}, putErr
		}
		return contracts.PageRetirement{}, mapWebsiteError(err)
	}
	return s.advance(ctx, job)
}

func (s *Service) advance(ctx context.Context, job jobRecord) (contracts.PageRetirement, error) {
	routerCtx, err := s.config.Router.Authenticate(ctx, s.config.RouterCredential)
	if err != nil {
		return contracts.PageRetirement{}, ErrUnavailable
	}
	for index := range job.Routes {
		if job.Routes[index].State == routeStateRemoved {
			continue
		}
		routeID := job.Routes[index].RouteID
		route, err := s.config.Router.GetRoute(routerCtx, routeID)
		if errors.Is(err, routerapi.ErrNotFound) {
			err = s.recordRoute(ctx, job.ID, routeID, routeStateRemoved, 0, "")
		} else if err != nil {
			err = s.recordRoute(ctx, job.ID, routeID, routeStateRetryable, 0, routeErrorCode(err))
		} else {
			deleteErr := s.config.Router.DeleteRoute(routerCtx, routerapi.DeleteRouteRequest{
				RouteID: routeID, ExpectedRevision: route.Revision,
			})
			switch {
			case deleteErr == nil, errors.Is(deleteErr, routerapi.ErrNotFound):
				err = s.recordRoute(ctx, job.ID, routeID, routeStateRemoved, route.Revision, "")
			default:
				err = s.recordRoute(ctx, job.ID, routeID, routeStateRetryable, route.Revision, routeErrorCode(deleteErr))
			}
		}
		if err != nil {
			return contracts.PageRetirement{}, err
		}
	}
	job, err = s.loadJob(ctx, job.ID, job.PageID)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	for _, route := range job.Routes {
		if route.State != routeStateRemoved {
			job, err = s.updateJob(ctx, job.ID, func(current *jobRecord) error {
				current.State = stateRetryable
				current.UpdatedAt = timestamp()
				return nil
			})
			if err != nil {
				return contracts.PageRetirement{}, err
			}
			return jobView(job), nil
		}
	}
	websiteCtx, err := s.config.Website.Authenticate(ctx, s.config.WebsiteCredential)
	if err != nil {
		return contracts.PageRetirement{}, ErrUnavailable
	}
	page, err := s.config.Website.GetPage(websiteCtx, job.PageID)
	if err != nil {
		return contracts.PageRetirement{}, mapWebsiteError(err)
	}
	if page.LifecycleState == "retired" && page.RetirementJobID == job.ID {
		return s.finish(ctx, job)
	}
	if page.LifecycleState != "retiring" || page.RetirementJobID != job.ID {
		job, err = s.updateJob(ctx, job.ID, func(current *jobRecord) error {
			current.State = stateRetryable
			current.UpdatedAt = timestamp()
			return nil
		})
		if err != nil {
			return contracts.PageRetirement{}, err
		}
		return jobView(job), nil
	}
	if _, err := s.config.Website.RetirePage(websiteCtx, websiteapi.RetirePageRequest{
		PageID: page.ID, ExpectedRevision: page.Revision, LifecycleState: "retired",
		RetirementJobID: job.ID, IdempotencyKey: job.IdempotencyKey,
	}); err != nil {
		job, err = s.updateJob(ctx, job.ID, func(current *jobRecord) error {
			current.State = stateRetryable
			current.UpdatedAt = timestamp()
			return nil
		})
		if err != nil {
			return contracts.PageRetirement{}, err
		}
		return jobView(job), nil
	}
	return s.finish(ctx, job)
}

func (s *Service) finish(ctx context.Context, job jobRecord) (contracts.PageRetirement, error) {
	updated, err := s.updateJob(ctx, job.ID, func(current *jobRecord) error {
		current.State = stateSucceeded
		current.UpdatedAt = timestamp()
		return nil
	})
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	return jobView(updated), nil
}

func (s *Service) recordRoute(ctx context.Context, jobID, routeID, state string, revision int64, errorCode string) error {
	_, err := s.updateJob(ctx, jobID, func(job *jobRecord) error {
		for index := range job.Routes {
			if job.Routes[index].RouteID != routeID {
				continue
			}
			job.Routes[index].State = state
			job.Routes[index].Revision = revision
			job.Routes[index].ErrorCode = errorCode
			job.UpdatedAt = timestamp()
			return nil
		}
		return fmt.Errorf("%w: unknown retirement route", ErrUnavailable)
	})
	return err
}

func (s *Service) loadJob(ctx context.Context, jobID, pageID string) (jobRecord, error) {
	record, err := s.config.Store.Get(ctx, withBranch(jobKey(jobID), s.config.BranchID))
	if err != nil {
		return jobRecord{}, mapStoreError(err)
	}
	job, err := decodeJob(record, jobID, pageID)
	if err != nil {
		return jobRecord{}, err
	}
	return job, nil
}

func (s *Service) updateJob(ctx context.Context, jobID string, mutate func(*jobRecord) error) (jobRecord, error) {
	key := withBranch(jobKey(jobID), s.config.BranchID)
	for attempt := 0; attempt < 8; attempt++ {
		record, err := s.config.Store.Get(ctx, key)
		if err != nil {
			return jobRecord{}, mapStoreError(err)
		}
		job, err := decodeJob(record, jobID, "")
		if err != nil {
			return jobRecord{}, err
		}
		if err := mutate(&job); err != nil {
			return jobRecord{}, err
		}
		value, err := encodeRecord(job, jobIndex(job))
		if err != nil {
			return jobRecord{}, err
		}
		if _, err := s.config.Store.Put(ctx, key, *value, record.Revision); err != nil {
			if errors.Is(err, core.ErrModuleDataConflict) && attempt+1 < 8 {
				continue
			}
			return jobRecord{}, mapStoreError(err)
		}
		return job, nil
	}
	return jobRecord{}, ErrConflict
}

func (s *Service) status(ctx context.Context, jobID, pageID string) (contracts.PageRetirement, error) {
	job, err := s.loadJob(ctx, jobID, pageID)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	return jobView(job), nil
}

func jobView(job jobRecord) contracts.PageRetirement {
	view := contracts.PageRetirement{ID: job.ID, PageID: job.PageID, State: job.State, Routes: make([]contracts.PageRetirementRoute, 0, len(job.Routes))}
	for _, route := range job.Routes {
		view.Routes = append(view.Routes, contracts.PageRetirementRoute{
			RouteID: route.RouteID, State: route.State, ErrorCode: route.ErrorCode,
		})
	}
	return view
}

func routeErrorCode(err error) string {
	switch {
	case errors.Is(err, routerapi.ErrNotFound):
		return "not_found"
	case errors.Is(err, routerapi.ErrConflict):
		return "revision_conflict"
	case errors.Is(err, routerapi.ErrUnavailable):
		return "unavailable"
	default:
		return "unavailable"
	}
}

func mapWebsiteError(err error) error {
	switch {
	case err == nil:
		return ErrUnavailable
	case errors.Is(err, websiteapi.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, websiteapi.ErrConflict):
		return ErrConflict
	case errors.Is(err, websiteapi.ErrNotFound):
		return ErrNotFound
	default:
		return ErrUnavailable
	}
}
