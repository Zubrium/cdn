package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

type fakeJobControlStore struct {
	listRequest coreplatform.CoordinatorJobListRequest
	listCalls   int
	retrySystem string
	retryOwner  uuid.UUID
	retryJob    uuid.UUID
	retryEpoch  int64
	retryCalls  int
	err         error
}

func (s *fakeJobControlStore) ListJobs(_ context.Context, request coreplatform.CoordinatorJobListRequest) (coreplatform.CoordinatorJobPage, error) {
	s.listCalls++
	s.listRequest = request
	if s.err != nil {
		return coreplatform.CoordinatorJobPage{}, s.err
	}
	return coreplatform.CoordinatorJobPage{Items: []coreplatform.CoordinatorJobSummary{{ID: uuid.New(), Type: "worker.inspect/v1", State: "failed"}}}, nil
}

func (s *fakeJobControlStore) Retry(_ context.Context, systemID string, owner, jobID uuid.UUID, epoch int64) error {
	s.retryCalls++
	s.retrySystem, s.retryOwner, s.retryJob, s.retryEpoch = systemID, owner, jobID, epoch
	return s.err
}

func TestJobControlScopesListAndRetryToActiveCoordinator(t *testing.T) {
	store := &fakeJobControlStore{}
	owner, beforeID := uuid.New(), uuid.New()
	control, err := NewJobControl(JobControlConfig{Store: store, SystemID: "test-system", Owner: owner, ConfigEpoch: 7})
	if err != nil {
		t.Fatal(err)
	}
	before := &coreplatform.CoordinatorJobCursor{CreatedAt: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), ID: beforeID}
	page, err := control.List(context.Background(), JobListFilter{Type: "worker.inspect/v1", State: "failed", Limit: 25, Before: before})
	if err != nil || store.listCalls != 1 || store.listRequest.SystemID != "test-system" || store.listRequest.Type != "worker.inspect/v1" ||
		store.listRequest.Owner != owner || store.listRequest.ConfigEpoch != 7 ||
		store.listRequest.State != "failed" || store.listRequest.Limit != 25 || store.listRequest.Before == nil ||
		store.listRequest.Before.ID != beforeID || len(page.Items) != 1 || page.Items[0].State != "failed" {
		t.Fatalf("job list escaped its system scope or lost pagination: request=%+v page=%+v err=%v", store.listRequest, page, err)
	}

	jobID := uuid.New()
	if err := control.Retry(context.Background(), jobID); err != nil || store.retryCalls != 1 || store.retrySystem != "test-system" ||
		store.retryOwner != owner || store.retryJob != jobID || store.retryEpoch != 7 {
		t.Fatalf("job retry escaped its active Coordinator scope: store=%+v err=%v", store, err)
	}
}

func TestJobControlRejectsInvalidQueriesBeforeCore(t *testing.T) {
	store := &fakeJobControlStore{}
	control, err := NewJobControl(JobControlConfig{Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	invalidCursor := &coreplatform.CoordinatorJobCursor{CreatedAt: time.Time{}, ID: uuid.New()}
	for _, filter := range []JobListFilter{{Limit: 0}, {Limit: 201}, {Type: "bad type", Limit: 10}, {State: "unknown", Limit: 10}, {Limit: 10, Before: invalidCursor}} {
		if _, err := control.List(context.Background(), filter); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid job list filter accepted: %+v err=%v", filter, err)
		}
	}
	if err := control.Retry(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("zero job ID accepted for retry: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := control.List(canceled, JobListFilter{Limit: 10}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled list context was ignored: %v", err)
	}
	if store.listCalls != 0 || store.retryCalls != 0 {
		t.Fatalf("invalid request reached Core: list=%d retry=%d", store.listCalls, store.retryCalls)
	}
}

func TestNewJobControlRejectsInvalidCoordinatorScope(t *testing.T) {
	for _, config := range []JobControlConfig{
		{Store: (*fakeJobControlStore)(nil), SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1},
		{Store: &fakeJobControlStore{}, SystemID: "bad/system", Owner: uuid.New(), ConfigEpoch: 1},
		{Store: &fakeJobControlStore{}, SystemID: "test-system", Owner: uuid.Nil, ConfigEpoch: 1},
		{Store: &fakeJobControlStore{}, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 0},
	} {
		if _, err := NewJobControl(config); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid Coordinator scope accepted: %+v err=%v", config, err)
		}
	}
}
