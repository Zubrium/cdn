package retirement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// FindReceipt returns the durable job already committed for an identical
// server-owned trigger. It lets the composition adapter check Website state
// only for first-time requests without weakening retry idempotency.
func (s *Service) FindReceipt(ctx context.Context, request contracts.RetirePageRequest) (Job, bool, error) {
	if s == nil || ctx == nil {
		return Job{}, false, ErrUnavailable
	}
	if err := contracts.RetirePageOperation().Validate(request); err != nil {
		return Job{}, false, err
	}
	record, err := s.config.Store.Get(ctx, idempotencyKey(request.PageID, request.IdempotencyKey))
	if errors.Is(err, contracts.ErrRecordNotFound) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, mapStoreError(err)
	}
	var receipt receiptRecord
	if record.PayloadVersion != dataVersion || jsonUnmarshal(record.Payload, &receipt) != nil ||
		receipt.Kind != recordTypeReceipt || receipt.PageID != request.PageID || !uuidPattern.MatchString(receipt.JobID) {
		return Job{}, false, fmt.Errorf("%w: invalid retirement receipt", ErrUnavailable)
	}
	job, err := s.LoadJob(ctx, receipt.JobID, request.PageID)
	if err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

// PageIsRetired reports whether the durable identity tombstone already
// forbids reuse of this page identity.
func (s *Service) PageIsRetired(ctx context.Context, pageID string) (bool, error) {
	if s == nil || ctx == nil || !contracts.ValidWorkflowID(pageID) {
		return false, ErrInvalid
	}
	_, err := s.config.Store.Get(ctx, tombstoneKey(pageID))
	if errors.Is(err, contracts.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, mapStoreError(err)
	}
	return true, nil
}

// Prepare validates the server-owned trigger and commits the durable job,
// receipt and page-identity tombstone. It does not start any engine work.
func (s *Service) Prepare(ctx context.Context, request contracts.RetirePageRequest) (Job, error) {
	if s == nil || ctx == nil {
		return Job{}, ErrUnavailable
	}
	if err := contracts.RetirePageOperation().Validate(request); err != nil {
		return Job{}, err
	}
	expected, err := parseIfMatch(request.IfMatch)
	if err != nil {
		return Job{}, err
	}
	// Receipts are checked before the identity tombstone so an identical retry
	// stays idempotent after the workflow has completed.
	if record, err := s.config.Store.Get(ctx, idempotencyKey(request.PageID, request.IdempotencyKey)); err == nil {
		var receipt receiptRecord
		if record.PayloadVersion != dataVersion || jsonUnmarshal(record.Payload, &receipt) != nil ||
			receipt.Kind != recordTypeReceipt || receipt.PageID != request.PageID || !uuidPattern.MatchString(receipt.JobID) {
			return Job{}, fmt.Errorf("%w: invalid retirement receipt", ErrUnavailable)
		}
		return s.LoadJob(ctx, receipt.JobID, request.PageID)
	} else if !errors.Is(err, contracts.ErrRecordNotFound) {
		return Job{}, mapStoreError(err)
	}
	if _, err := s.config.Store.Get(ctx, tombstoneKey(request.PageID)); err == nil {
		return Job{}, fmt.Errorf("%w: page identity is retired and cannot be reused", ErrConflict)
	} else if !errors.Is(err, contracts.ErrRecordNotFound) {
		return Job{}, mapStoreError(err)
	}
	routes, registered := s.config.Routes[request.PageID]
	if !registered || len(routes) == 0 {
		return Job{}, fmt.Errorf("%w: no registered route targets", ErrInvalid)
	}
	job := jobRecord{
		Kind: recordTypeJob, ID: newUUID(), PageID: request.PageID, ExpectedRevision: expected, IfMatch: request.IfMatch, IdempotencyKey: request.IdempotencyKey,
		State: stateRunning, CreatedAt: timestamp(), UpdatedAt: timestamp(),
	}
	for _, routeID := range routes {
		job.Routes = append(job.Routes, routeRecord{RouteID: routeID, State: routeStatePending})
	}
	mutations, err := jobCreationMutations(request, job)
	if err != nil {
		return Job{}, err
	}
	if err := s.config.Store.Batch(ctx, mutations); err != nil {
		if errors.Is(err, contracts.ErrRecordConflict) {
			if record, readErr := s.config.Store.Get(ctx, idempotencyKey(request.PageID, request.IdempotencyKey)); readErr == nil {
				var receipt receiptRecord
				if record.PayloadVersion == dataVersion && jsonUnmarshal(record.Payload, &receipt) == nil && receipt.PageID == request.PageID {
					return s.LoadJob(ctx, receipt.JobID, request.PageID)
				}
			}
		}
		return Job{}, mapStoreError(err)
	}
	return job, nil
}

// Launch starts the class-scoped root task. Its idempotency key is derived
// from the durable job identity, so a retry cannot create a second workflow.
func (s *Service) Launch(ctx context.Context, job Job) (contracts.PageRetirement, error) {
	if s == nil || ctx == nil {
		return contracts.PageRetirement{}, ErrUnavailable
	}
	if s.config.Starter == nil {
		return contracts.PageRetirement{}, ErrUnavailable
	}
	key := jobKey(job.ID)
	record, err := s.config.Store.Get(ctx, key)
	if err != nil {
		return contracts.PageRetirement{}, mapStoreError(err)
	}
	job, err = decodeJob(record, job.ID, job.PageID)
	if err != nil {
		return contracts.PageRetirement{}, ErrUnavailable
	}
	if job.State == stateSucceeded {
		return JobView(job), nil
	}
	routes := make([]string, 0, len(job.Routes))
	for _, route := range job.Routes {
		routes = append(routes, route.RouteID)
	}
	payload := contracts.RetirementWorkflowPayload{
		JobID: job.ID, PageID: job.PageID, ExpectedRevision: job.ExpectedRevision, IfMatch: job.IfMatch,
		IdempotencyKey: job.IdempotencyKey, Routes: routes,
	}
	if err := contracts.ValidateRetirementWorkflowPayload(payload); err != nil {
		return contracts.PageRetirement{}, err
	}
	if _, err := s.config.Starter.Start(ctx, retirementWorkflowStartInput(payload)); err != nil {
		if updateErr := s.markLaunchOutcome(ctx, key, record.Revision, job, stateRetryable); updateErr != nil {
			return contracts.PageRetirement{}, updateErr
		}
		return contracts.PageRetirement{}, err
	}
	if job.State == stateRetryable {
		if err := s.markLaunchOutcome(ctx, key, record.Revision, job, stateRunning); err != nil {
			return contracts.PageRetirement{}, err
		}
	}
	return s.status(ctx, job.ID, job.PageID)
}

// An asynchronous worker may have changed the job before Start returns.
// Never overwrite a newer outcome with the start response.
func (s *Service) markLaunchOutcome(ctx context.Context, key contracts.RecordKey, revision int64, job Job, state string) error {
	if job.State == state {
		return nil
	}
	job.State = state
	job.UpdatedAt = timestamp()
	value, err := encodeRecord(job, jobIndex(job))
	if err != nil {
		return err
	}
	if _, err := s.config.Store.Put(ctx, key, *value, revision); err != nil {
		if errors.Is(err, contracts.ErrRecordConflict) {
			_, readErr := s.LoadJob(ctx, job.ID, job.PageID)
			return readErr
		}
		return mapStoreError(err)
	}
	return nil
}

func retirementWorkflowStartInput(payload contracts.RetirementWorkflowPayload) contracts.WorkflowStartInput {
	encoded, _ := json.Marshal(payload)
	return contracts.WorkflowStartInput{
		OperationID:     contracts.RetirementStartOperationID,
		ContractVersion: contracts.RetirementStartContractVersion,
		IdempotencyKey:  "retirement:" + payload.JobID + ":" + payload.IdempotencyKey,
		Payload:         encoded,
	}
}

// RetirePage is the tractable process-local entry point. The composition root
// marks the page "retiring" between Prepare and Launch so no route child can
// run while the page is still active.
func (s *Service) RetirePage(ctx context.Context, request contracts.RetirePageRequest) (contracts.PageRetirement, error) {
	job, err := s.Prepare(ctx, request)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	return s.Launch(ctx, job)
}

// GetPageRetirement reads the durable status addressed by the Location header.
func (s *Service) GetPageRetirement(ctx context.Context, query contracts.PageRetirementQuery) (contracts.PageRetirement, error) {
	if s == nil || ctx == nil || !contracts.ValidWorkflowID(query.PageID) || !contracts.ValidWorkflowID(query.JobID) {
		return contracts.PageRetirement{}, fmt.Errorf("%w: invalid retirement query", ErrInvalid)
	}
	return s.status(ctx, query.JobID, query.PageID)
}

// Retry resumes a retryable job after the underlying Router dependency has
// recovered. Completed jobs are returned unchanged.
func (s *Service) Retry(ctx context.Context, jobID string) (contracts.PageRetirement, error) {
	if s == nil || ctx == nil || !contracts.ValidWorkflowID(jobID) {
		return contracts.PageRetirement{}, fmt.Errorf("%w: invalid job identity", ErrInvalid)
	}
	job, err := s.updateJob(ctx, jobID, func(current *jobRecord) error {
		switch current.State {
		case stateSucceeded, stateRunning:
			return errRetryAlreadySettled
		case stateRetryable:
			current.State = stateRunning
			current.UpdatedAt = timestamp()
			return nil
		default:
			return fmt.Errorf("%w: job is not retryable", ErrConflict)
		}
	})
	if errors.Is(err, errRetryAlreadySettled) {
		current, loadErr := s.LoadJob(ctx, jobID, "")
		if loadErr != nil {
			return contracts.PageRetirement{}, loadErr
		}
		return JobView(current), nil
	}
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	return s.Launch(ctx, job)
}

var errRetryAlreadySettled = errors.New("retirement retry already settled")

// LoadJob returns the durable job projection for a composition adapter.
func (s *Service) LoadJob(ctx context.Context, jobID, pageID string) (Job, error) {
	if s == nil || ctx == nil || !contracts.ValidWorkflowID(jobID) {
		return Job{}, fmt.Errorf("%w: invalid job identity", ErrInvalid)
	}
	record, err := s.config.Store.Get(ctx, jobKey(jobID))
	if err != nil {
		return Job{}, mapStoreError(err)
	}
	job, err := decodeJob(record, jobID, pageID)
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

// SetJobState persists a bounded job state transition with a CAS retry loop.
func (s *Service) SetJobState(ctx context.Context, jobID, state string) (Job, error) {
	if state != stateRunning && state != stateSucceeded && state != stateRetryable {
		return Job{}, ErrInvalid
	}
	job, err := s.updateJob(ctx, jobID, func(current *jobRecord) error {
		if current.State == stateSucceeded || current.State == state {
			return errJobStateUnchanged
		}
		current.State = state
		current.UpdatedAt = timestamp()
		return nil
	})
	if errors.Is(err, errJobStateUnchanged) {
		return s.LoadJob(ctx, jobID, "")
	}
	return job, err
}

var errJobStateUnchanged = errors.New("retirement job state unchanged")

// MarkRetryable records a failed Website lifecycle closure without losing the
// already-withdrawn routes.
func (s *Service) MarkRetryable(ctx context.Context, jobID string) error {
	_, err := s.SetJobState(ctx, jobID, stateRetryable)
	return err
}

// RecordRoute persists one route outcome. A retry may record the same removed
// route again; that is a no-op for the durable projection.
func (s *Service) RecordRoute(ctx context.Context, jobID, routeID, state string, revision int64, errorCode string) error {
	if state != routeStatePending && state != routeStateRemoved && state != routeStateRetryable {
		return ErrInvalid
	}
	_, err := s.updateJob(ctx, jobID, func(job *jobRecord) error {
		for index := range job.Routes {
			if job.Routes[index].RouteID != routeID {
				continue
			}
			if job.Routes[index].State == routeStateRemoved {
				return errRouteAlreadyRemoved
			}
			job.Routes[index].State = state
			job.Routes[index].Revision = revision
			job.Routes[index].ErrorCode = errorCode
			job.UpdatedAt = timestamp()
			return nil
		}
		return fmt.Errorf("%w: unknown retirement route", ErrUnavailable)
	})
	if errors.Is(err, errRouteAlreadyRemoved) {
		return nil
	}
	return err
}

var errRouteAlreadyRemoved = errors.New("retirement route already removed")

func (s *Service) updateJob(ctx context.Context, jobID string, mutate func(*jobRecord) error) (Job, error) {
	key := jobKey(jobID)
	for attempt := 0; attempt < 8; attempt++ {
		record, err := s.config.Store.Get(ctx, key)
		if err != nil {
			return Job{}, mapStoreError(err)
		}
		job, err := decodeJob(record, jobID, "")
		if err != nil {
			return Job{}, err
		}
		if err := mutate(&job); err != nil {
			return Job{}, err
		}
		value, err := encodeRecord(job, jobIndex(job))
		if err != nil {
			return Job{}, err
		}
		if _, err := s.config.Store.Put(ctx, key, *value, record.Revision); err != nil {
			if errors.Is(err, contracts.ErrRecordConflict) && attempt+1 < 8 {
				continue
			}
			return Job{}, mapStoreError(err)
		}
		return job, nil
	}
	return Job{}, ErrConflict
}

func (s *Service) status(ctx context.Context, jobID, pageID string) (contracts.PageRetirement, error) {
	job, err := s.LoadJob(ctx, jobID, pageID)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	return JobView(job), nil
}

// JobView converts the durable job into the stable process-local contract.
func JobView(job Job) contracts.PageRetirement {
	view := contracts.PageRetirement{ID: job.ID, PageID: job.PageID, State: job.State, Routes: make([]contracts.PageRetirementRoute, 0, len(job.Routes))}
	for _, route := range job.Routes {
		view.Routes = append(view.Routes, contracts.PageRetirementRoute{
			RouteID: route.RouteID, State: route.State, ErrorCode: route.ErrorCode,
		})
	}
	return view
}
