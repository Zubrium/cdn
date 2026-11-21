package bus

import (
	"context"
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

type workflowControlStub struct {
	listFilter platform.WorkflowListFilter
	getID      uuid.UUID
	getFilter  platform.WorkflowDetailFilter
	retryPair  [2]uuid.UUID
	err        error
}

func (stub *workflowControlStub) List(_ context.Context, filter platform.WorkflowListFilter) (platform.WorkflowPage, error) {
	stub.listFilter = filter
	return platform.WorkflowPage{}, stub.err
}

func (stub *workflowControlStub) Get(_ context.Context, id uuid.UUID, filter platform.WorkflowDetailFilter) (platform.WorkflowDetail, error) {
	stub.getID, stub.getFilter = id, filter
	return platform.WorkflowDetail{}, stub.err
}

func (stub *workflowControlStub) RetryTask(_ context.Context, workflowID, taskID uuid.UUID) error {
	stub.retryPair = [2]uuid.UUID{workflowID, taskID}
	return stub.err
}

func TestWorkflowOperationsAreStaticAndOwned(t *testing.T) {
	operations := []contracts.Key{
		contracts.WorkflowListOperation().Key,
		contracts.WorkflowGetOperation().Key,
		contracts.WorkflowRetryOperation().Key,
	}
	dispatcher, err := NewAllowlisted(operations...)
	if err != nil {
		t.Fatal(err)
	}
	control := &workflowControlStub{}
	if err := RegisterWorkflowControl(dispatcher, control); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	workflowID, taskID := uuid.New(), uuid.New()

	if _, err := contracts.Call(context.Background(), dispatcher, contracts.WorkflowListOperation(), contracts.WorkflowListRequest{State: "ready", Limit: 5}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if control.listFilter.State != "ready" || control.listFilter.Limit != 5 {
		t.Fatalf("list filter = %+v", control.listFilter)
	}

	if _, err := contracts.Call(context.Background(), dispatcher, contracts.WorkflowGetOperation(), contracts.WorkflowGetRequest{WorkflowID: workflowID.String(), Limit: 3}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if control.getID != workflowID || control.getFilter.Limit != 3 {
		t.Fatalf("get = %s, %+v", control.getID, control.getFilter)
	}

	if _, err := contracts.Call(context.Background(), dispatcher, contracts.WorkflowRetryOperation(), contracts.WorkflowRetryRequest{WorkflowID: workflowID.String(), TaskID: taskID.String()}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if control.retryPair != [2]uuid.UUID{workflowID, taskID} {
		t.Fatalf("retry = %v", control.retryPair)
	}
}

func TestWorkflowOperationsRejectInvalidIdentifiers(t *testing.T) {
	dispatcher, err := NewAllowlisted(
		contracts.WorkflowListOperation().Key,
		contracts.WorkflowGetOperation().Key,
		contracts.WorkflowRetryOperation().Key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterWorkflowControl(dispatcher, &workflowControlStub{}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	_, err = contracts.Call(context.Background(), dispatcher, contracts.WorkflowGetOperation(), contracts.WorkflowGetRequest{WorkflowID: "not-a-uuid"})
	if !errors.Is(err, ErrInvalidWorkflowRequest) {
		t.Fatalf("invalid workflow id = %v", err)
	}
	_, err = contracts.Call(context.Background(), dispatcher, contracts.WorkflowRetryOperation(), contracts.WorkflowRetryRequest{WorkflowID: uuid.New().String(), TaskID: "bad"})
	if !errors.Is(err, ErrInvalidWorkflowRequest) {
		t.Fatalf("invalid task id = %v", err)
	}
}
