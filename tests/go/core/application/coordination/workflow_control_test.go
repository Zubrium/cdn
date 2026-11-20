package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

type fakeWorkflowControlStore struct {
	listRequest   corecoord.WorkflowListRequest
	listCalls     int
	detailCalls   int
	detailID      uuid.UUID
	detailLimit   int
	detailBefore  *corecoord.TaskCursor
	retryWorkflow uuid.UUID
	retryTask     uuid.UUID
	retryCalls    int
	err           error
}

func (s *fakeWorkflowControlStore) ListWorkflows(_ context.Context, request corecoord.WorkflowListRequest) (corecoord.WorkflowPage, error) {
	s.listCalls++
	s.listRequest = request
	if s.err != nil {
		return corecoord.WorkflowPage{}, s.err
	}
	return corecoord.WorkflowPage{Items: []corecoord.WorkflowSummary{{ID: uuid.New(), State: "failed", TaskCount: 3}}}, nil
}

func (s *fakeWorkflowControlStore) GetWorkflowDetail(_ context.Context, _ uuid.UUID, _ int64, id uuid.UUID, limit int, before *corecoord.TaskCursor) (corecoord.WorkflowDetail, error) {
	s.detailCalls++
	s.detailID, s.detailLimit, s.detailBefore = id, limit, before
	if s.err != nil {
		return corecoord.WorkflowDetail{}, s.err
	}
	return corecoord.WorkflowDetail{Workflow: corecoord.Workflow{ID: id, State: "failed"}, TaskPage: corecoord.TaskPage{}}, nil
}

func (s *fakeWorkflowControlStore) RetryTask(_ context.Context, _ uuid.UUID, workflowID, taskID uuid.UUID, _ int64) error {
	s.retryCalls++
	s.retryWorkflow, s.retryTask = workflowID, taskID
	return s.err
}

func TestWorkflowControlScopesListDetailAndRetry(t *testing.T) {
	owner := uuid.New()
	store := &fakeWorkflowControlStore{}
	control, err := NewWorkflowControl(WorkflowControlConfig{Store: store, Owner: owner, ConfigEpoch: 7})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	workflowBefore := &corecoord.WorkflowCursor{CreatedAt: createdAt, ID: uuid.New()}
	page, err := control.List(context.Background(), WorkflowListFilter{State: "failed", Limit: 25, Before: workflowBefore})
	if err != nil || len(page.Items) != 1 || store.listCalls != 1 || store.listRequest.Owner != owner ||
		store.listRequest.ConfigEpoch != 7 || store.listRequest.State != "failed" || store.listRequest.Limit != 25 ||
		store.listRequest.Before == nil || store.listRequest.Before.ID != workflowBefore.ID {
		t.Fatalf("workflow list was not scoped through the active Coordinator: page=%+v request=%+v err=%v", page, store.listRequest, err)
	}
	workflowID, taskID := uuid.New(), uuid.New()
	taskBefore := &corecoord.TaskCursor{CreatedAt: createdAt, ID: uuid.New()}
	detail, err := control.Get(context.Background(), workflowID, WorkflowDetailFilter{Limit: 200, Before: taskBefore})
	if err != nil || detail.Workflow.ID != workflowID || store.detailID != workflowID || store.detailLimit != 200 ||
		store.detailBefore == nil || store.detailBefore.ID != taskBefore.ID {
		t.Fatalf("workflow detail was not retrieved with a bounded task graph: detail=%+v err=%v", detail, err)
	}
	if err := control.RetryTask(context.Background(), workflowID, taskID); err != nil || store.retryCalls != 1 ||
		store.retryWorkflow != workflowID || store.retryTask != taskID {
		t.Fatalf("task retry was not workflow-scoped: workflow=%s task=%s err=%v", store.retryWorkflow, store.retryTask, err)
	}
}

func TestWorkflowControlRejectsInvalidQueriesBeforeCore(t *testing.T) {
	store := &fakeWorkflowControlStore{}
	control, err := NewWorkflowControl(WorkflowControlConfig{Store: store, Owner: uuid.New(), ConfigEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range []WorkflowListFilter{
		{Limit: 0}, {Limit: 201}, {Limit: 10, State: "unknown"},
		{Limit: 10, Before: &corecoord.WorkflowCursor{ID: uuid.New()}},
	} {
		if _, err := control.List(context.Background(), filter); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid workflow list filter reached Core: filter=%+v err=%v", filter, err)
		}
	}
	for _, filter := range []WorkflowDetailFilter{{Limit: 0}, {Limit: 1001},
		{Limit: 10, Before: &corecoord.TaskCursor{ID: uuid.New()}}} {
		if _, err := control.Get(context.Background(), uuid.New(), filter); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid workflow detail filter reached Core: filter=%+v err=%v", filter, err)
		}
	}
	if _, err := control.Get(context.Background(), uuid.Nil, WorkflowDetailFilter{Limit: 10}); !errors.Is(err, ErrInvalid) {
		t.Errorf("nil workflow ID reached Core: %v", err)
	}
	if err := control.RetryTask(context.Background(), uuid.New(), uuid.Nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("nil task ID reached Core: %v", err)
	}
	if store.listCalls != 0 || store.detailCalls != 0 || store.retryCalls != 0 {
		t.Fatalf("invalid filters reached Core: list=%d detail=%d retry=%d", store.listCalls, store.detailCalls, store.retryCalls)
	}
}

func TestNewWorkflowControlRejectsInvalidCoordinatorScope(t *testing.T) {
	for _, config := range []WorkflowControlConfig{
		{Store: (*fakeWorkflowControlStore)(nil), Owner: uuid.New(), ConfigEpoch: 1},
		{Store: &fakeWorkflowControlStore{}, Owner: uuid.Nil, ConfigEpoch: 1},
		{Store: &fakeWorkflowControlStore{}, Owner: uuid.New(), ConfigEpoch: 0},
	} {
		if _, err := NewWorkflowControl(config); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid workflow-control scope was accepted: %+v err=%v", config, err)
		}
	}
}
