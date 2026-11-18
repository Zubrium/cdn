package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

type fakeJobControlStore struct {
	listRequest corecoord.CoordinatorJobListRequest
	listCalls   int
	retryOwner  uuid.UUID
	retryJob    uuid.UUID
	retryEpoch  int64
	retryCalls  int
	err         error
}

func (s *fakeJobControlStore) ListJobs(_ context.Context, request corecoord.CoordinatorJobListRequest) (corecoord.CoordinatorJobPage, error) {
	s.listCalls++
	s.listRequest = request
	if s.err != nil {
		return corecoord.CoordinatorJobPage{}, s.err
	}
	return corecoord.CoordinatorJobPage{Items: []corecoord.CoordinatorJobSummary{{ID: uuid.New(), Type: "worker.inspect/v1", State: "failed"}}}, nil
}

func (s *fakeJobControlStore) Retry(_ context.Context, owner, jobID uuid.UUID, epoch int64) error {
	s.retryCalls++
	s.retryOwner, s.retryJob, s.retryEpoch = owner, jobID, epoch
	return s.err
}

func TestJobControlScopesListAndRetryToActiveCoordinator(t *testing.T) {
	store := &fakeJobControlStore{}
	owner, beforeID := uuid.New(), uuid.New()
	control, err := NewJobControl(JobControlConfig{Store: store, Owner: owner, ConfigEpoch: 7})
	if err != nil {
		t.Fatal(err)
	}
	before := &corecoord.CoordinatorJobCursor{CreatedAt: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), ID: beforeID}
	page, err := control.List(context.Background(), JobListFilter{Type: "worker.inspect/v1", State: "failed", Limit: 25, Before: before})
	if err != nil || store.listCalls != 1 || store.listRequest.Type != "worker.inspect/v1" ||
		store.listRequest.Owner != owner || store.listRequest.ConfigEpoch != 7 ||
		store.listRequest.State != "failed" || store.listRequest.Limit != 25 || store.listRequest.Before == nil ||
		store.listRequest.Before.ID != beforeID || len(page.Items) != 1 || page.Items[0].State != "failed" {
		t.Fatalf("job list escaped its system scope or lost pagination: request=%+v page=%+v err=%v", store.listRequest, page, err)
	}

	jobID := uuid.New()
	if err := control.Retry(context.Background(), jobID); err != nil || store.retryCalls != 1 ||
		store.retryOwner != owner || store.retryJob != jobID || store.retryEpoch != 7 {
		t.Fatalf("job retry escaped its active Coordinator scope: store=%+v err=%v", store, err)
	}
}

func TestJobControlRejectsInvalidQueriesBeforeCore(t *testing.T) {
	store := &fakeJobControlStore{}
	control, err := NewJobControl(JobControlConfig{Store: store, Owner: uuid.New(), ConfigEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	invalidCursor := &corecoord.CoordinatorJobCursor{CreatedAt: time.Time{}, ID: uuid.New()}
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
		{Store: (*fakeJobControlStore)(nil), Owner: uuid.New(), ConfigEpoch: 1},
		{Store: &fakeJobControlStore{}, Owner: uuid.Nil, ConfigEpoch: 1},
		{Store: &fakeJobControlStore{}, Owner: uuid.New(), ConfigEpoch: 0},
	} {
		if _, err := NewJobControl(config); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid Coordinator scope accepted: %+v err=%v", config, err)
		}
	}
}
