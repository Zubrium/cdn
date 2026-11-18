package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCoordinatorStoreRejectsCallsWithoutPool(t *testing.T) {
	store := CoordinatorStore{}
	ctx := context.Background()
	owner, taskID := uuid.New(), uuid.New()
	request := corecoord.WorkflowRequest{}
	outcome := corecoord.CoordinatorTaskOutcome{}
	cases := []struct {
		name string
		run  func() error
	}{
		{name: "register instance", run: func() error {
			_, err := store.RegisterInstance(ctx, owner, strings.Repeat("a", 64), time.Minute, time.Hour)
			return err
		}},
		{name: "renew instance", run: func() error { return store.RenewInstance(ctx, owner, 1, time.Minute) }},
		{name: "release instance", run: func() error { return store.ReleaseInstance(ctx, owner, 1) }},
		{name: "start workflow", run: func() error { _, _, err := store.StartWorkflow(ctx, request); return err }},
		{name: "get workflow", run: func() error { _, err := store.GetWorkflow(ctx, "engine.example", owner, 1, taskID); return err }},
		{name: "cancel workflow", run: func() error { return store.CancelWorkflow(ctx, "engine.example", owner, 1, taskID, time.Hour) }},
		{name: "claim tasks", run: func() error { _, err := store.Claim(ctx, owner, 1, 1, time.Minute, time.Hour); return err }},
		{name: "extend task lease", run: func() error { return store.Extend(ctx, taskID, owner, 1, 1, time.Minute) }},
		{name: "complete task", run: func() error { return store.Complete(ctx, taskID, owner, 1, 1, 10, 2, time.Hour, outcome) }},
		{name: "fail task", run: func() error { return store.Fail(ctx, taskID, owner, 1, 1, "failed", false, 0, time.Hour) }},
		{name: "retry task", run: func() error { return store.Retry(ctx, owner, taskID, 1) }},
		{name: "get task", run: func() error { _, err := store.GetJob(ctx, taskID); return err }},
		{name: "list tasks", run: func() error {
			_, err := store.ListJobs(ctx, corecoord.CoordinatorJobListRequest{Owner: owner, ConfigEpoch: 1, Limit: 1})
			return err
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, corecoord.ErrInvalid) {
				t.Fatalf("call without a configured PostgreSQL pool returned %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDisposableCoordinatorFencesConfigurationEpoch(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}

	retention := 24 * time.Hour
	owner := uuid.New()
	epoch, err := RegisterCoordinatorInstance(ctx, pool, owner, strings.Repeat("a", 64), time.Minute, retention)
	if err != nil || epoch != 1 {
		t.Fatalf("register initial instance: epoch=%d err=%v", epoch, err)
	}
	if _, err := RegisterCoordinatorInstance(ctx, pool, uuid.New(), strings.Repeat("b", 64), time.Minute, retention); !errors.Is(err, ErrCoordinatorConfigDiff) {
		t.Fatalf("different configuration joined active instances: %v", err)
	}
	if err := ReleaseCoordinatorInstance(ctx, pool, owner, epoch); err != nil {
		t.Fatal(err)
	}
	newEpoch, err := RegisterCoordinatorInstance(ctx, pool, uuid.New(), strings.Repeat("b", 64), time.Minute, retention)
	if err != nil || newEpoch != epoch+1 {
		t.Fatalf("configuration epoch did not advance after shutdown: epoch=%d err=%v", newEpoch, err)
	}
}

func TestDisposableWorkflowIdempotencyIsScopedAndPayloadBound(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_WORKFLOW_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"one"}`)
	first, created, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil || !created {
		t.Fatalf("start initial workflow: created=%v err=%v", created, err)
	}
	request.ID, request.Root.ID = uuid.New(), uuid.New()
	request.Root.WorkflowID = request.ID
	second, created, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil || created || second.ID != first.ID || second.RootTaskID != first.RootTaskID {
		t.Fatalf("idempotent workflow start did not return the existing workflow: result=%+v created=%v err=%v", second, created, err)
	}
	request.Root.Payload = json.RawMessage(`{"input":"different"}`)
	request.InputHash = workflowInputHash(`{"input":"different"}`, "process", 1)
	if _, _, err := StartCoordinatorWorkflow(ctx, pool, request); !errors.Is(err, ErrCoordinatorConflict) {
		t.Fatalf("idempotency key accepted a different input: %v", err)
	}
	if _, err := GetCoordinatorWorkflow(ctx, pool, "engine.content", owner, epoch, first.ID); !errors.Is(err, ErrCoordinatorJobMissing) {
		t.Fatalf("another component read an unowned workflow: %v", err)
	}
	if got, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, first.ID); err != nil || got.State != "queued" {
		t.Fatalf("owner could not read its workflow: state=%q err=%v", got.State, err)
	}
}

func TestDisposableConcurrentWorkflowStartUsesOneIdempotentWorkflow(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_WORKFLOW_RACE_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	requests := []corecoord.WorkflowRequest{
		workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"same"}`),
		workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"same"}`),
	}
	requests[1].IdempotencyKey = requests[0].IdempotencyKey
	type result struct {
		workflow corecoord.Workflow
		created  bool
		err      error
	}
	results := make(chan result, len(requests))
	start := make(chan struct{})
	for _, request := range requests {
		request := request
		go func() {
			<-start
			workflow, created, err := StartCoordinatorWorkflow(ctx, pool, request)
			results <- result{workflow: workflow, created: created, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent idempotent starts failed: first=%v second=%v", first.err, second.err)
	}
	if first.workflow.ID == uuid.Nil || first.workflow.ID != second.workflow.ID || first.workflow.RootTaskID != second.workflow.RootTaskID || first.created == second.created {
		t.Fatalf("same idempotency key created distinct workflows or inconsistent receipts: first=%+v created=%v second=%+v created=%v",
			first.workflow, first.created, second.workflow, second.created)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM coordination.workflows WHERE owner_engine_id='engine.example'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent idempotent starts stored %d workflows, want 1", count)
	}
}

func TestDisposableDynamicWorkflowChildrenCommitAtomicallyAndFinish(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_CHILDREN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"one"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	rootOutcome := corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"processed":true}`), Children: []corecoord.CoordinatorTaskChild{{
		ComponentID: "worker.example", OperationID: "transform", ContractVersion: 1, EmissionKey: "task-1",
		Payload: json.RawMessage(`{"input":"one"}`), MaxAttempts: 3,
	}}}
	if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, rootOutcome); err != nil {
		t.Fatalf("atomically complete root and emit child: %v", err)
	}
	childJobs, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour)
	if err != nil || len(childJobs) != 1 || childJobs[0].ComponentID != "worker.example" || childJobs[0].OperationID != "transform" {
		t.Fatalf("claim dynamic child: jobs=%+v err=%v", childJobs, err)
	}
	child := childJobs[0]
	childOutcome := corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"packaged":true}`), Children: []corecoord.CoordinatorTaskChild{{
		ComponentID: "engine.other", OperationID: "follow-up", ContractVersion: 2, EmissionKey: "continue",
		Payload: json.RawMessage(`{"artifact":"opaque"}`), MaxAttempts: 2,
	}}}
	if err := CompleteCoordinatorTask(ctx, pool, child.ID, owner, epoch, child.FencingToken, 10, 4, time.Hour, childOutcome); err != nil {
		t.Fatalf("child did not atomically emit its own dependent task: %v", err)
	}
	grandchildren, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour)
	if err != nil || len(grandchildren) != 1 || grandchildren[0].ComponentID != "engine.other" {
		t.Fatalf("claim second-generation task: jobs=%+v err=%v", grandchildren, err)
	}
	grandchild := grandchildren[0]
	if err := CompleteCoordinatorTask(ctx, pool, grandchild.ID, owner, epoch, grandchild.FencingToken,
		10, 4, time.Hour, corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"active":true}`)}); err != nil {
		t.Fatalf("complete terminal child: %v", err)
	}
	got, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID)
	var workflowResult struct {
		Processed bool `json:"processed"`
	}
	resultErr := json.Unmarshal(got.Result, &workflowResult)
	if err != nil || resultErr != nil || got.State != "succeeded" || !workflowResult.Processed {
		t.Fatalf("workflow did not retain root result after child completion: workflow=%+v err=%v", got, err)
	}
}

func TestDisposableWorkflowCancellationRejectsLateFencedCompletion(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_CANCEL_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"one"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour,
		corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"late":true}`)}); !errors.Is(err, ErrCoordinatorJobNotOwned) {
		t.Fatalf("cancelled workflow accepted a late completion: %v", err)
	}
	got, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID)
	if err != nil || got.State != "cancelled" {
		t.Fatalf("cancelled workflow state changed: state=%q err=%v", got.State, err)
	}
}

func TestDisposableWorkflowCapabilityExpiresWithInstanceLease(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_STALE_CAPABILITY_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"one"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseCoordinatorInstance(ctx, pool, owner, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID); !errors.Is(err, ErrCoordinatorLeaseLost) {
		t.Fatalf("stale capability read workflow after lease release: %v", err)
	}
	if err := CancelCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID, time.Hour); !errors.Is(err, ErrCoordinatorLeaseLost) {
		t.Fatalf("stale capability cancelled workflow after lease release: %v", err)
	}
}

func TestDisposableRetryRequeuesTasksCancelledByWorkflowFailure(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_RETRY_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := CoordinatorStore{Pool: pool}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"retry"}`)
	workflow, _, err := store.StartWorkflow(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	outcome := corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"started":true}`), Children: []corecoord.CoordinatorTaskChild{
		{ComponentID: "worker.one", OperationID: "process", ContractVersion: 1, EmissionKey: "first", Payload: json.RawMessage(`{"n":1}`), MaxAttempts: 3},
		{ComponentID: "worker.two", OperationID: "process", ContractVersion: 1, EmissionKey: "second", Payload: json.RawMessage(`{"n":2}`), MaxAttempts: 3},
	}}
	if err := store.Complete(ctx, root.ID, owner, epoch, root.FencingToken, 8, 4, time.Hour, outcome); err != nil {
		t.Fatal(err)
	}
	children, err := store.Claim(ctx, owner, epoch, 2, time.Minute, time.Hour)
	if err != nil || len(children) != 2 {
		t.Fatalf("claim workflow children: count=%d err=%v", len(children), err)
	}
	failed := children[0]
	if err := store.Extend(ctx, failed.ID, owner, epoch, failed.FencingToken, time.Minute); err != nil {
		t.Fatalf("extend task lease through CoordinatorStore: %v", err)
	}
	storedTask, err := store.GetJob(ctx, failed.ID)
	if err != nil || storedTask.ID != failed.ID || storedTask.State != "running" || storedTask.LeaseUntil == nil {
		t.Fatalf("read task through CoordinatorStore: task=%+v err=%v", storedTask, err)
	}
	if err := store.Fail(ctx, failed.ID, owner, epoch, failed.FencingToken, "permanent_error", false, 0, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Retry(ctx, owner, failed.ID, epoch); err != nil {
		t.Fatalf("retry failed workflow task: %v", err)
	}
	got, err := store.GetWorkflow(ctx, "engine.example", owner, epoch, workflow.ID)
	if err != nil || got.State != "queued" {
		t.Fatalf("retry did not reopen the workflow: state=%q err=%v", got.State, err)
	}
	requeued, err := store.Claim(ctx, owner, epoch, 2, time.Minute, time.Hour)
	if err != nil || len(requeued) != 2 {
		t.Fatalf("retry did not requeue both failed and cancelled tasks: count=%d err=%v", len(requeued), err)
	}
	for _, task := range requeued {
		if err := store.Complete(ctx, task.ID, owner, epoch, task.FencingToken, 8, 4, time.Hour,
			corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"done":true}`)}); err != nil {
			t.Fatalf("complete retried task: %v", err)
		}
	}
	got, err = store.GetWorkflow(ctx, "engine.example", owner, epoch, workflow.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatalf("retried workflow did not complete: state=%q err=%v", got.State, err)
	}
}

func TestDisposableDelayedWorkflowTaskWaitsUntilAvailableAt(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_DELAYED_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"delayed"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	var availableAt time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()+interval '1 second'`).Scan(&availableAt); err != nil {
		t.Fatal(err)
	}
	child := corecoord.CoordinatorTaskChild{ComponentID: "worker.example", OperationID: "deferred", ContractVersion: 1,
		EmissionKey: "deferred:one", Payload: json.RawMessage(`{"input":"scheduled"}`), MaxAttempts: 2, AvailableAt: availableAt}
	if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 2, time.Hour,
		corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"scheduled":true}`), Children: []corecoord.CoordinatorTaskChild{child}}); err != nil {
		t.Fatal(err)
	}
	if jobs, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour); err != nil || len(jobs) != 0 {
		t.Fatalf("future task was claimed before available_at: jobs=%+v err=%v", jobs, err)
	}
	if err := waitForCoordinatorTimestamp(ctx, pool, `SELECT available_at<=clock_timestamp() FROM coordination.tasks WHERE workflow_id=$1 AND emission_key=$2`, workflow.ID, child.EmissionKey); err != nil {
		t.Fatal(err)
	}
	jobs, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour)
	if err != nil || len(jobs) != 1 || jobs[0].OperationID != "deferred" {
		t.Fatalf("due task was not claimable: jobs=%+v err=%v", jobs, err)
	}
}

func TestDisposableCompletedWorkflowResultSurvivesInstanceRestart(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_RESTART_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"restart"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"durable":"result"}`)
	if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 2, time.Hour,
		corecoord.CoordinatorTaskOutcome{Result: result}); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseCoordinatorInstance(ctx, pool, owner, epoch); err != nil {
		t.Fatal(err)
	}
	newOwner := uuid.New()
	newEpoch, err := RegisterCoordinatorInstance(ctx, pool, newOwner, strings.Repeat("a", 64), time.Minute, time.Hour)
	if err != nil || newEpoch != epoch {
		t.Fatalf("register restarted instance: epoch=%d previous=%d err=%v", newEpoch, epoch, err)
	}
	got, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", newOwner, newEpoch, workflow.ID)
	var persisted struct {
		Durable string `json:"durable"`
	}
	decodeErr := json.Unmarshal(got.Result, &persisted)
	if err != nil || decodeErr != nil || got.State != "succeeded" || persisted.Durable != "result" {
		t.Fatalf("completed result did not survive instance restart: workflow=%+v err=%v", got, err)
	}
}

func TestDisposableRunningTaskResumesAfterInstanceRestart(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_RESTART_PENDING_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	firstOwner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(firstOwner, epoch, "engine.example", "process", `{"input":"resume"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	firstJobs, err := ClaimCoordinatorJobs(ctx, pool, firstOwner, epoch, 1, time.Second, time.Hour)
	if err != nil || len(firstJobs) != 1 {
		t.Fatalf("first instance failed to claim task: jobs=%+v err=%v", firstJobs, err)
	}
	firstLease := firstJobs[0]
	if err := ReleaseCoordinatorInstance(ctx, pool, firstOwner, epoch); err != nil {
		t.Fatal(err)
	}
	if err := waitForCoordinatorTimestamp(ctx, pool, `SELECT lease_until<=clock_timestamp() FROM coordination.tasks WHERE task_id=$1`, firstLease.ID); err != nil {
		t.Fatal(err)
	}
	secondOwner := uuid.New()
	secondEpoch, err := RegisterCoordinatorInstance(ctx, pool, secondOwner, strings.Repeat("a", 64), time.Minute, time.Hour)
	if err != nil || secondEpoch != epoch {
		t.Fatalf("restart changed an unchanged configuration epoch: epoch=%d previous=%d err=%v", secondEpoch, epoch, err)
	}
	secondJobs, err := ClaimCoordinatorJobs(ctx, pool, secondOwner, secondEpoch, 1, time.Minute, time.Hour)
	if err != nil || len(secondJobs) != 1 {
		t.Fatalf("restarted instance did not recover expired work: jobs=%+v err=%v", secondJobs, err)
	}
	secondLease := secondJobs[0]
	if secondLease.ID != firstLease.ID || secondLease.Attempts != firstLease.Attempts+1 || secondLease.FencingToken <= firstLease.FencingToken {
		t.Fatalf("restarted instance did not fence and retry the task: before=%+v after=%+v", firstLease, secondLease)
	}
	result := json.RawMessage(`{"resumed":true}`)
	if err := CompleteCoordinatorTask(ctx, pool, secondLease.ID, secondOwner, secondEpoch, secondLease.FencingToken, 10, 2, time.Hour,
		corecoord.CoordinatorTaskOutcome{Result: result}); err != nil {
		t.Fatalf("restarted instance could not complete recovered work: %v", err)
	}
	got, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", secondOwner, secondEpoch, workflow.ID)
	var persisted struct {
		Resumed bool `json:"resumed"`
	}
	decodeErr := json.Unmarshal(got.Result, &persisted)
	if err != nil || decodeErr != nil || got.State != "succeeded" || !persisted.Resumed {
		t.Fatalf("recovered workflow result was not durable: workflow=%+v err=%v", got, err)
	}
}

func TestDisposableExpiredTaskLeaseCanBeTakenOverWithNewFencingToken(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_TAKEOVER_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	firstOwner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(firstOwner, epoch, "engine.example", "process", `{"input":"takeover"}`)
	if _, _, err := StartCoordinatorWorkflow(ctx, pool, request); err != nil {
		t.Fatal(err)
	}
	firstJobs, err := ClaimCoordinatorJobs(ctx, pool, firstOwner, epoch, 1, time.Second, time.Hour)
	if err != nil || len(firstJobs) != 1 {
		t.Fatalf("first worker failed to claim task: jobs=%+v err=%v", firstJobs, err)
	}
	firstLease := firstJobs[0]
	secondOwner := uuid.New()
	secondEpoch, err := RegisterCoordinatorInstance(ctx, pool, secondOwner, strings.Repeat("a", 64), time.Minute, time.Hour)
	if err != nil || secondEpoch != epoch {
		t.Fatalf("register concurrent replacement worker: epoch=%d expected=%d err=%v", secondEpoch, epoch, err)
	}
	if firstLease.LeaseUntil == nil {
		t.Fatal("claimed task has no lease deadline")
	}
	timer := time.NewTimer(time.Until(*firstLease.LeaseUntil) + 200*time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	secondJobs, err := ClaimCoordinatorJobs(ctx, pool, secondOwner, secondEpoch, 1, time.Minute, time.Hour)
	if err != nil || len(secondJobs) != 1 {
		t.Fatalf("replacement worker failed to take expired task: jobs=%+v err=%v", secondJobs, err)
	}
	secondLease := secondJobs[0]
	if secondLease.ID != firstLease.ID || secondLease.LeaseOwner != secondOwner || secondLease.FencingToken <= firstLease.FencingToken {
		t.Fatalf("takeover did not advance task ownership and fencing: before=%+v after=%+v", firstLease, secondLease)
	}
	if err := CompleteCoordinatorTask(ctx, pool, firstLease.ID, firstOwner, epoch, firstLease.FencingToken, 10, 2, time.Hour,
		corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"stale":true}`)}); !errors.Is(err, ErrCoordinatorJobNotOwned) {
		t.Fatalf("stale worker committed after lease takeover: %v", err)
	}
}

func TestDisposableWorkflowRetentionPrunesExpiredResults(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_RETENTION_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"expire"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 2, time.Second,
		corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"done":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err := waitForCoordinatorTimestamp(ctx, pool, `SELECT expires_at<=clock_timestamp() FROM coordination.workflows WHERE workflow_id=$1`, workflow.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := PruneCoordinatorWorkflows(ctx, pool, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("expired workflow was not pruned: deleted=%d err=%v", deleted, err)
	}
	if _, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID); !errors.Is(err, ErrCoordinatorJobMissing) {
		t.Fatalf("pruned workflow result remained readable: %v", err)
	}
}

func TestDisposableCrossWorkflowDependencyIsRejectedAtomically(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_DEPENDENCY_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	firstRequest := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"first"}`)
	first, _, err := StartCoordinatorWorkflow(ctx, pool, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"second"}`)
	secondRequest.IdempotencyKey = "request-2"
	second, _, err := StartCoordinatorWorkflow(ctx, pool, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	firstRoot, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	secondRoot, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if firstRoot.WorkflowID != first.ID || secondRoot.WorkflowID != second.ID {
		t.Fatalf("test did not claim both workflow roots: first=%s second=%s", firstRoot.WorkflowID, secondRoot.WorkflowID)
	}
	outcome := corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"invalidDependency":true}`), Children: []corecoord.CoordinatorTaskChild{{
		ComponentID: "worker.example", OperationID: "child", ContractVersion: 1, EmissionKey: "foreign-dependency",
		Payload: json.RawMessage(`{}`), MaxAttempts: 1, DependsOn: []uuid.UUID{secondRoot.ID},
	}}}
	if err := CompleteCoordinatorTask(ctx, pool, firstRoot.ID, owner, epoch, firstRoot.FencingToken, 10, 2, time.Hour, outcome); !errors.Is(err, ErrCoordinatorInvalid) {
		t.Fatalf("cross-workflow dependency was accepted: %v", err)
	}
	var state string
	var taskCount int
	if err := pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM coordination.tasks WHERE workflow_id=$1) FROM coordination.workflows WHERE workflow_id=$1`, first.ID).Scan(&state, &taskCount); err != nil {
		t.Fatal(err)
	}
	if state != "running" || taskCount != 1 {
		t.Fatalf("rejected dependency partially committed parent or child: state=%q tasks=%d", state, taskCount)
	}
}

func TestDisposableTaskWaitsForItsExplicitDependency(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_DEPENDENCY_WAIT_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"dependency"}`)
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	rootOutcome := corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"root":true}`), Children: []corecoord.CoordinatorTaskChild{
		{ComponentID: "worker.example", OperationID: "wait-for", ContractVersion: 1, EmissionKey: "dependency:source", Payload: json.RawMessage(`{}`), MaxAttempts: 2},
		{ComponentID: "worker.example", OperationID: "emit", ContractVersion: 1, EmissionKey: "dependency:emitter", Payload: json.RawMessage(`{}`), MaxAttempts: 2},
	}}
	if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, rootOutcome); err != nil {
		t.Fatal(err)
	}
	children, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 2, time.Minute, time.Hour)
	if err != nil || len(children) != 2 {
		t.Fatalf("claim sibling tasks: jobs=%+v err=%v", children, err)
	}
	var source, emitter CoordinatorJob
	for _, child := range children {
		switch child.OperationID {
		case "wait-for":
			source = child
		case "emit":
			emitter = child
		}
	}
	if source.ID == uuid.Nil || emitter.ID == uuid.Nil {
		t.Fatalf("did not receive both dependency tasks: %+v", children)
	}
	emitterOutcome := corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"emitted":true}`), Children: []corecoord.CoordinatorTaskChild{{
		ComponentID: "worker.example", OperationID: "dependent", ContractVersion: 1, EmissionKey: "dependency:target",
		Payload: json.RawMessage(`{}`), MaxAttempts: 2, DependsOn: []uuid.UUID{source.ID},
	}}}
	if err := CompleteCoordinatorTask(ctx, pool, emitter.ID, owner, epoch, emitter.FencingToken, 10, 4, time.Hour, emitterOutcome); err != nil {
		t.Fatal(err)
	}
	if jobs, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour); err != nil || len(jobs) != 0 {
		t.Fatalf("task ran before its explicit dependency succeeded: jobs=%+v err=%v", jobs, err)
	}
	if err := CompleteCoordinatorTask(ctx, pool, source.ID, owner, epoch, source.FencingToken, 10, 4, time.Hour,
		corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"source":true}`)}); err != nil {
		t.Fatal(err)
	}
	dependent, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour)
	if err != nil || len(dependent) != 1 || dependent[0].OperationID != "dependent" {
		t.Fatalf("task did not become claimable after its dependency succeeded: jobs=%+v err=%v", dependent, err)
	}
	if err := CompleteCoordinatorTask(ctx, pool, dependent[0].ID, owner, epoch, dependent[0].FencingToken, 10, 4, time.Hour,
		corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"done":true}`)}); err != nil {
		t.Fatal(err)
	}
	got, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatalf("dependency workflow did not finish: state=%q err=%v", got.State, err)
	}
}

func TestDisposableExpiredTaskAtMaxAttemptsFailsWorkflow(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_EXHAUSTED_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner, epoch := registerWorkflowTestInstance(t, ctx, pool)
	request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"input":"exhausted"}`)
	request.Root.MaxAttempts = 1
	workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Second, time.Hour)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim final permitted attempt: jobs=%+v err=%v", jobs, err)
	}
	if err := waitForCoordinatorTimestamp(ctx, pool, `SELECT lease_until<=clock_timestamp() FROM coordination.tasks WHERE task_id=$1`, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if retried, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour); err != nil || len(retried) != 0 {
		t.Fatalf("exhausted task was incorrectly retried: jobs=%+v err=%v", retried, err)
	}
	got, err := GetCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflow.ID)
	if err != nil || got.State != "failed" {
		t.Fatalf("expired task beyond its attempt bound did not fail the workflow: state=%q err=%v", got.State, err)
	}
	var taskState, errorCode string
	if err := pool.QueryRow(ctx, `SELECT state,error_code FROM coordination.tasks WHERE task_id=$1`, jobs[0].ID).Scan(&taskState, &errorCode); err != nil {
		t.Fatal(err)
	}
	if taskState != "failed" || errorCode != "attempts_exhausted" {
		t.Fatalf("exhausted task has unsafe final state: state=%q error=%q", taskState, errorCode)
	}
}

func waitForCoordinatorTimestamp(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var due bool
		if err := pool.QueryRow(ctx, query, args...).Scan(&due); err != nil {
			return err
		}
		if due {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func registerWorkflowTestInstance(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, int64) {
	t.Helper()
	owner := uuid.New()
	epoch, err := RegisterCoordinatorInstance(ctx, pool, owner, strings.Repeat("a", 64), time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return owner, epoch
}

func workflowTestRequest(owner uuid.UUID, epoch int64, componentID, operationID, payload string) corecoord.WorkflowRequest {
	workflowID, taskID := uuid.New(), uuid.New()
	return corecoord.WorkflowRequest{ID: workflowID, Owner: owner, OwnerEngineID: componentID, IdempotencyKey: "request-1",
		InputHash: workflowInputHash(payload, operationID, 1), ConfigEpoch: epoch, Retention: time.Hour,
		Root: corecoord.TaskRequest{ID: taskID, WorkflowID: workflowID, ComponentID: componentID, OperationID: operationID,
			ContractVersion: 1, Payload: json.RawMessage(payload), MaxAttempts: 3}}
}

func workflowInputHash(payload, operation string, version int) []byte {
	material := operation + "\x00" + strconv.Itoa(version) + "\x00" + payload
	hash := sha256.Sum256([]byte(material))
	return hash[:]
}

func claimWorkflowRoot(ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID, epoch int64) (CoordinatorJob, error) {
	jobs, err := ClaimCoordinatorJobs(ctx, pool, owner, epoch, 1, time.Minute, time.Hour)
	if err != nil {
		return CoordinatorJob{}, err
	}
	if len(jobs) != 1 {
		return CoordinatorJob{}, errors.New("expected one queued workflow root task")
	}
	return jobs[0], nil
}
