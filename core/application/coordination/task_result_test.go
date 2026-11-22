package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

type taskResultCall struct {
	ownerEngineID string
	owner         uuid.UUID
	configEpoch   int64
	workflowID    uuid.UUID
	taskID        uuid.UUID
}

// taskResultStore is an in-memory stand-in for the narrow owner-scoped result
// port. It enforces the same scopes as the SQL implementation so a client that
// leaks a caller-supplied identity cannot pass these tests.
type taskResultStore struct {
	engineID  string
	owner     uuid.UUID
	epoch     int64
	results   map[uuid.UUID]map[uuid.UUID]corecoord.TaskResult
	calls     []taskResultCall
	getErr    error
	oversized bool
}

func (s *taskResultStore) StartWorkflow(context.Context, corecoord.WorkflowRequest) (corecoord.Workflow, bool, error) {
	panic("unexpected StartWorkflow call")
}

func (s *taskResultStore) GetWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID) (corecoord.Workflow, error) {
	panic("unexpected GetWorkflow call")
}

func (s *taskResultStore) CancelWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID, time.Duration) error {
	panic("unexpected CancelWorkflow call")
}

func (s *taskResultStore) GetTaskResult(ctx context.Context, ownerEngineID string, owner uuid.UUID, epoch int64, workflowID, taskID uuid.UUID) (corecoord.TaskResult, error) {
	if err := ctx.Err(); err != nil {
		return corecoord.TaskResult{}, err
	}
	if s.getErr != nil {
		return corecoord.TaskResult{}, s.getErr
	}
	s.calls = append(s.calls, taskResultCall{ownerEngineID: ownerEngineID, owner: owner,
		configEpoch: epoch, workflowID: workflowID, taskID: taskID})
	if ownerEngineID != s.engineID || owner != s.owner || epoch != s.epoch {
		return corecoord.TaskResult{}, corecoord.ErrTaskMissing
	}
	tasks := s.results[workflowID]
	result, ok := tasks[taskID]
	if !ok {
		return corecoord.TaskResult{}, corecoord.ErrTaskMissing
	}
	if s.oversized {
		result.Result = json.RawMessage(`{"artifact":"` + string(make([]byte, MaxTaskResultBytes)) + `"}`)
	}
	return result, nil
}

func taskResultClient(t *testing.T, store *taskResultStore, engineID string, owner uuid.UUID, epoch int64) *WorkflowClient {
	t.Helper()
	catalog, err := BuildWorkerCatalog(BuildPlan{Components: []string{"core", engineID}}, []ComponentWorkerSet{{
		ComponentID: engineID, Handlers: map[string]WorkerHandler{"process/v1": func(context.Context, Task) (corecoord.TaskOutcome, error) {
			return corecoord.TaskOutcome{Result: json.RawMessage(`{}`)}, nil
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewWorkflowClient(WorkflowClientConfig{Store: store, Workers: catalog, OwnerEngineID: engineID,
		InstanceID: owner, ConfigEpoch: epoch, MaxAttempts: 3, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func taskResultFixture(engineID string, owner uuid.UUID, epoch int64, workflowID, taskID uuid.UUID) *taskResultStore {
	expected := corecoord.TaskResult{TaskID: taskID, WorkflowID: workflowID,
		Worker: &corecoord.WorkerRef{ComponentID: "worker.prerender", OperationID: "render", ContractVersion: 1},
		State:  "succeeded", Result: json.RawMessage(`{"artifact":"sha256:abc"}`)}
	return &taskResultStore{engineID: engineID, owner: owner, epoch: epoch,
		results: map[uuid.UUID]map[uuid.UUID]corecoord.TaskResult{
			workflowID: {taskID: expected},
		}}
}

func TestWorkflowClientGetTaskResultReturnsChildResultToOwner(t *testing.T) {
	workflowID, taskID, owner := uuid.New(), uuid.New(), uuid.New()
	store := taskResultFixture("engine.website", owner, 7, workflowID, taskID)
	client := taskResultClient(t, store, "engine.website", owner, 7)

	result, err := client.GetTaskResult(context.Background(), workflowID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != taskID || result.WorkflowID != workflowID || result.State != "succeeded" ||
		string(result.Result) != `{"artifact":"sha256:abc"}` || result.Worker == nil ||
		result.Worker.ComponentID != "worker.prerender" || result.Worker.OperationID != "render" {
		t.Fatalf("child result was not returned to the workflow owner: %+v", result)
	}
	if len(store.calls) != 1 || store.calls[0] != (taskResultCall{ownerEngineID: "engine.website", owner: owner,
		configEpoch: 7, workflowID: workflowID, taskID: taskID}) {
		t.Fatalf("store did not receive strictly client-scoped identity: %+v", store.calls)
	}
}

func TestWorkflowClientGetTaskResultRejectsCallerSuppliedScopes(t *testing.T) {
	workflowID, taskID, owner := uuid.New(), uuid.New(), uuid.New()
	store := taskResultFixture("engine.website", owner, 7, workflowID, taskID)
	for _, test := range []struct {
		name     string
		engineID string
		owner    uuid.UUID
		epoch    int64
	}{
		{name: "other engine", engineID: "engine.other", owner: owner, epoch: 7},
		{name: "other instance owner", engineID: "engine.website", owner: uuid.New(), epoch: 7},
		{name: "other config epoch", engineID: "engine.website", owner: owner, epoch: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := taskResultClient(t, store, test.engineID, test.owner, test.epoch)
			if _, err := client.GetTaskResult(context.Background(), workflowID, taskID); !errors.Is(err, corecoord.ErrTaskMissing) {
				t.Fatalf("foreign scope was not rejected: %v", err)
			}
		})
	}
}

func TestWorkflowClientGetTaskResultRejectsTaskFromAnotherWorkflow(t *testing.T) {
	workflowID, taskID, owner := uuid.New(), uuid.New(), uuid.New()
	store := taskResultFixture("engine.website", owner, 7, workflowID, taskID)
	client := taskResultClient(t, store, "engine.website", owner, 7)

	if _, err := client.GetTaskResult(context.Background(), uuid.New(), taskID); !errors.Is(err, corecoord.ErrTaskMissing) {
		t.Fatalf("task from another workflow was not rejected: %v", err)
	}
}

func TestWorkflowClientGetTaskResultMissingTask(t *testing.T) {
	workflowID, owner := uuid.New(), uuid.New()
	store := taskResultFixture("engine.website", owner, 7, workflowID, uuid.New())
	client := taskResultClient(t, store, "engine.website", owner, 7)

	if _, err := client.GetTaskResult(context.Background(), workflowID, uuid.New()); !errors.Is(err, corecoord.ErrTaskMissing) {
		t.Fatalf("missing task was not reported as ErrTaskMissing: %v", err)
	}
}

func TestWorkflowClientGetTaskResultRejectsOversizedResult(t *testing.T) {
	workflowID, taskID, owner := uuid.New(), uuid.New(), uuid.New()
	store := taskResultFixture("engine.website", owner, 7, workflowID, taskID)
	store.oversized = true
	client := taskResultClient(t, store, "engine.website", owner, 7)

	if _, err := client.GetTaskResult(context.Background(), workflowID, taskID); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized task result was not rejected: %v", err)
	}
}

func TestWorkflowClientGetTaskResultHonorsCanceledContext(t *testing.T) {
	workflowID, taskID, owner := uuid.New(), uuid.New(), uuid.New()
	store := taskResultFixture("engine.website", owner, 7, workflowID, taskID)
	client := taskResultClient(t, store, "engine.website", owner, 7)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.GetTaskResult(ctx, workflowID, taskID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context was not propagated: %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("canceled read reached the store: %+v", store.calls)
	}
}

func TestWorkflowClientGetTaskResultRejectsNilArguments(t *testing.T) {
	workflowID, taskID, owner := uuid.New(), uuid.New(), uuid.New()
	store := taskResultFixture("engine.website", owner, 7, workflowID, taskID)
	client := taskResultClient(t, store, "engine.website", owner, 7)
	var nilClient *WorkflowClient

	for _, test := range []struct {
		name   string
		client *WorkflowClient
		ctx    context.Context
		a, b   uuid.UUID
	}{
		{name: "nil client", client: nilClient, ctx: context.Background(), a: workflowID, b: taskID},
		{name: "nil context", client: client, ctx: nil, a: workflowID, b: taskID},
		{name: "nil workflow ID", client: client, ctx: context.Background(), a: uuid.Nil, b: taskID},
		{name: "nil task ID", client: client, ctx: context.Background(), a: workflowID, b: uuid.Nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.client.GetTaskResult(test.ctx, test.a, test.b); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid argument was not rejected: %v", err)
			}
		})
	}
}
