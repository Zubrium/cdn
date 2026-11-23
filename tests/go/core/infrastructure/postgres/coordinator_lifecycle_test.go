package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

// TestDisposableCoordinatorLifecycleSuite consolidates the Coordinator
// multi-instance, concurrency, atomicity and outage scenarios on a single
// freshly created disposable database. It is skipped unless an explicitly
// provisioned disposable DSN is supplied.
func TestDisposableCoordinatorLifecycleSuite(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_LIFECYCLE_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := CoordinatorStore{Pool: pool}

	register := func(t *testing.T) (uuid.UUID, int64) {
		t.Helper()
		owner := uuid.New()
		epoch, err := RegisterCoordinatorInstance(ctx, pool, owner, strings.Repeat("a", 64), time.Minute, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ReleaseCoordinatorInstance(context.Background(), pool, owner, epoch) })
		return owner, epoch
	}
	startWorkflow := func(t *testing.T, owner uuid.UUID, epoch int64, key, payload string) corecoord.Workflow {
		t.Helper()
		request := workflowTestRequest(owner, epoch, "engine.example", "process", payload)
		request.IdempotencyKey = key
		workflow, _, err := StartCoordinatorWorkflow(ctx, pool, request)
		if err != nil {
			t.Fatal(err)
		}
		return workflow
	}
	cancelWorkflow := func(owner uuid.UUID, epoch int64, workflowID uuid.UUID) {
		_ = CancelCoordinatorWorkflow(ctx, pool, "engine.example", owner, epoch, workflowID, time.Hour)
	}
	scalarInt := func(t *testing.T, query string, args ...any) int {
		t.Helper()
		var value int
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	taskState := func(t *testing.T, taskID uuid.UUID) string {
		t.Helper()
		var state string
		if err := pool.QueryRow(ctx, `SELECT state FROM coordination.tasks WHERE task_id=$1`, taskID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	workflowState := func(t *testing.T, workflowID uuid.UUID) string {
		t.Helper()
		var state string
		if err := pool.QueryRow(ctx, `SELECT state FROM coordination.workflows WHERE workflow_id=$1`, workflowID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	taskIDByEmission := func(t *testing.T, workflowID uuid.UUID, emission string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT task_id FROM coordination.tasks WHERE workflow_id=$1 AND emission_key=$2`, workflowID, emission).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	dependenciesOf := func(t *testing.T, workflowID, taskID uuid.UUID) []uuid.UUID {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT depends_on FROM coordination.dependencies WHERE workflow_id=$1 AND task_id=$2 ORDER BY depends_on`, workflowID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			result = append(result, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}

	t.Run("multi_instance_registration_and_renewal", func(t *testing.T) {
		fingerprint := strings.Repeat("a", 64)
		first := uuid.New()
		epoch, err := RegisterCoordinatorInstance(ctx, pool, first, fingerprint, time.Minute, time.Hour)
		if err != nil || epoch <= 0 {
			t.Fatalf("register first instance: epoch=%d err=%v", epoch, err)
		}
		defer func() { _ = ReleaseCoordinatorInstance(context.Background(), pool, first, epoch) }()
		second := uuid.New()
		secondEpoch, err := RegisterCoordinatorInstance(ctx, pool, second, fingerprint, time.Minute, time.Hour)
		if err != nil || secondEpoch != epoch {
			t.Fatalf("second instance did not share the active fingerprint/epoch: epoch=%d expected=%d err=%v", secondEpoch, epoch, err)
		}
		defer func() { _ = ReleaseCoordinatorInstance(context.Background(), pool, second, secondEpoch) }()
		if _, err := RegisterCoordinatorInstance(ctx, pool, uuid.New(), strings.Repeat("b", 64), time.Minute, time.Hour); !errors.Is(err, ErrCoordinatorConfigDiff) {
			t.Fatalf("incompatible fingerprint joined live instances: %v", err)
		}
		if err := RenewCoordinatorInstance(ctx, pool, first, epoch, time.Minute); err != nil {
			t.Fatalf("renew first instance: %v", err)
		}
		if err := RenewCoordinatorInstance(ctx, pool, second, epoch, time.Minute); err != nil {
			t.Fatalf("renew second instance: %v", err)
		}
		if err := ReleaseCoordinatorInstance(ctx, pool, first, epoch); err != nil {
			t.Fatal(err)
		}
		if err := ReleaseCoordinatorInstance(ctx, pool, second, epoch); err != nil {
			t.Fatal(err)
		}
		restarted := uuid.New()
		restartEpoch, err := RegisterCoordinatorInstance(ctx, pool, restarted, fingerprint, time.Minute, time.Hour)
		if err != nil || restartEpoch != epoch {
			t.Fatalf("restart after all instances stopped changed the fingerprint epoch: epoch=%d expected=%d err=%v", restartEpoch, epoch, err)
		}
		if err := ReleaseCoordinatorInstance(ctx, pool, restarted, restartEpoch); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("concurrent_claims_single_winner", func(t *testing.T) {
		owner, epoch := register(t)
		workflow := startWorkflow(t, owner, epoch, "claim-race", `{"input":"race"}`)
		defer cancelWorkflow(owner, epoch, workflow.ID)
		type claimResult struct {
			tasks []Task
			err   error
		}
		gate := make(chan struct{})
		results := make(chan claimResult, 2)
		for index := 0; index < 2; index++ {
			go func() {
				<-gate
				tasks, err := ClaimTasks(ctx, pool, owner, epoch, []string{"engine.example", "engine.other", "worker.one", "worker.two", "worker.example", "worker.archive", "worker.media"}, 1, time.Minute, time.Hour)
				results <- claimResult{tasks: tasks, err: err}
			}()
		}
		close(gate)
		winners := 0
		for index := 0; index < 2; index++ {
			result := <-results
			if result.err != nil {
				t.Fatalf("concurrent claim failed: %v", result.err)
			}
			if len(result.tasks) == 1 {
				winners++
				if result.tasks[0].ID != workflow.RootTaskID || result.tasks[0].FencingToken != 1 {
					t.Fatalf("winning claim returned the wrong durable task: %+v", result.tasks[0])
				}
			} else if len(result.tasks) != 0 {
				t.Fatalf("claim returned %d tasks for a single-workflow race", len(result.tasks))
			}
		}
		if winners != 1 {
			t.Fatalf("concurrent claims produced %d winners, want exactly one", winners)
		}
	})

	t.Run("parent_default_and_sibling_refs", func(t *testing.T) {
		owner, epoch := register(t)
		workflow := startWorkflow(t, owner, epoch, "parent-refs", `{"input":"refs"}`)
		defer cancelWorkflow(owner, epoch, workflow.ID)
		root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
		if err != nil {
			t.Fatal(err)
		}
		outcome := corecoord.TaskOutcome{Result: json.RawMessage(`{"root":true}`), Children: []corecoord.TaskChild{
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "ref:a", Payload: json.RawMessage(`{}`), MaxAttempts: 2},
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "ref:b", Payload: json.RawMessage(`{}`), MaxAttempts: 2},
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "ref:c", Payload: json.RawMessage(`{}`), MaxAttempts: 2, DependsOnEmissions: []string{"ref:a", "ref:b"}},
		}}
		if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, outcome); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"ref:a", "ref:b"} {
			dependencies := dependenciesOf(t, workflow.ID, taskIDByEmission(t, workflow.ID, key))
			if len(dependencies) != 1 || dependencies[0] != root.ID {
				t.Fatalf("child %q did not inherit its parent dependency: %v parent=%s", key, dependencies, root.ID)
			}
		}
		joinDependencies := dependenciesOf(t, workflow.ID, taskIDByEmission(t, workflow.ID, "ref:c"))
		a, b := taskIDByEmission(t, workflow.ID, "ref:a"), taskIDByEmission(t, workflow.ID, "ref:b")
		if len(joinDependencies) != 2 || (joinDependencies[0] != a && joinDependencies[1] != a) || (joinDependencies[0] != b && joinDependencies[1] != b) {
			t.Fatalf("sibling emission references were not materialized as edges: %v", joinDependencies)
		}
	})

	t.Run("cycle_and_limits_roll_back", func(t *testing.T) {
		owner, epoch := register(t)
		workflow := startWorkflow(t, owner, epoch, "cycle", `{"input":"cycle"}`)
		defer cancelWorkflow(owner, epoch, workflow.ID)
		root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
		if err != nil {
			t.Fatal(err)
		}
		cyclic := corecoord.TaskOutcome{Result: json.RawMessage(`{}`), Children: []corecoord.TaskChild{
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "cycle:a", Payload: json.RawMessage(`{}`), MaxAttempts: 1, DependsOnEmissions: []string{"cycle:b"}},
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "cycle:b", Payload: json.RawMessage(`{}`), MaxAttempts: 1, DependsOnEmissions: []string{"cycle:a"}},
		}}
		if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, cyclic); !errors.Is(err, ErrCoordinatorInvalid) {
			t.Fatalf("dependency cycle was accepted: %v", err)
		}
		repeated := corecoord.TaskOutcome{Result: json.RawMessage(`{}`), Children: []corecoord.TaskChild{
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "repeat:a", Payload: json.RawMessage(`{}`), MaxAttempts: 1, DependsOn: []uuid.UUID{root.ID, root.ID}},
		}}
		if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, repeated); !errors.Is(err, ErrCoordinatorInvalid) {
			t.Fatalf("repeated dependency reference was accepted: %v", err)
		}
		if state := taskState(t, root.ID); state != "running" {
			t.Fatalf("rejected graph changed the parent task state: %q", state)
		}
		if count := scalarInt(t, `SELECT count(*) FROM coordination.tasks WHERE workflow_id=$1`, workflow.ID); count != 1 {
			t.Fatalf("rejected graph persisted %d tasks, want 1", count)
		}
		if count := scalarInt(t, `SELECT count(*) FROM coordination.dependencies WHERE workflow_id=$1`, workflow.ID); count != 0 {
			t.Fatalf("rejected graph persisted %d dependency edges", count)
		}
	})

	t.Run("commit_outage_rolls_back", func(t *testing.T) {
		owner, epoch := register(t)
		workflow := startWorkflow(t, owner, epoch, "commit-outage", `{"input":"outage"}`)
		defer cancelWorkflow(owner, epoch, workflow.ID)
		root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
		if err != nil {
			t.Fatal(err)
		}
		const functionName = "qa_coordinator_dependency_outage"
		const qualifiedFunctionName = "public.qa_coordinator_dependency_outage"
		if _, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION `+qualifiedFunctionName+`() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'qa injected dependency outage'; END; $$`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+functionName+` ON coordination.dependencies`)
			_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+qualifiedFunctionName+`()`)
		})
		if _, err := pool.Exec(ctx, `CREATE TRIGGER `+functionName+` BEFORE INSERT ON coordination.dependencies
			FOR EACH ROW EXECUTE FUNCTION `+qualifiedFunctionName+`()`); err != nil {
			t.Fatal(err)
		}
		outcome := corecoord.TaskOutcome{Result: json.RawMessage(`{"attempted":true}`), Children: []corecoord.TaskChild{
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "outage:child", Payload: json.RawMessage(`{}`), MaxAttempts: 1},
		}}
		if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, outcome); err == nil {
			t.Fatal("injected commit outage did not fail the task completion")
		}
		if state := taskState(t, root.ID); state != "running" {
			t.Fatalf("commit outage partially fixed the parent result: state=%q", state)
		}
		if count := scalarInt(t, `SELECT count(*) FROM coordination.tasks WHERE workflow_id=$1`, workflow.ID); count != 1 {
			t.Fatalf("commit outage persisted %d tasks, want 1", count)
		}
		if count := scalarInt(t, `SELECT count(*) FROM coordination.dependencies WHERE workflow_id=$1`, workflow.ID); count != 0 {
			t.Fatalf("commit outage persisted %d dependency edges", count)
		}
	})

	t.Run("join_barrier_with_failed_dependency", func(t *testing.T) {
		owner, epoch := register(t)
		workflow := startWorkflow(t, owner, epoch, "join-failure", `{"input":"join"}`)
		defer cancelWorkflow(owner, epoch, workflow.ID)
		root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
		if err != nil {
			t.Fatal(err)
		}
		outcome := corecoord.TaskOutcome{Result: json.RawMessage(`{}`), Children: []corecoord.TaskChild{
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "join:one", Payload: json.RawMessage(`{}`), MaxAttempts: 2},
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "join:two", Payload: json.RawMessage(`{}`), MaxAttempts: 2},
			{Kind: corecoord.TaskKindJoin, EmissionKey: "join:barrier", Payload: json.RawMessage(`{}`),
				DependsOnEmissions: []string{"join:one", "join:two"}},
		}}
		if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, outcome); err != nil {
			t.Fatal(err)
		}
		branches, err := ClaimTasks(ctx, pool, owner, epoch, []string{"engine.example", "engine.other", "worker.one", "worker.two", "worker.example", "worker.archive", "worker.media"}, 2, time.Minute, time.Hour)
		if err != nil || len(branches) != 2 {
			t.Fatalf("claim join dependencies: tasks=%+v err=%v", branches, err)
		}
		var failedBranch Task
		for _, branch := range branches {
			if branch.EmissionKey == "join:one" {
				failedBranch = branch
			}
			if branch.Kind != corecoord.TaskKindOperation || branch.Operation == nil {
				t.Fatalf("join task was claimed as worker work: %+v", branch)
			}
		}
		if failedBranch.ID == uuid.Nil {
			t.Fatal("test did not claim the failing branch")
		}
		if err := FailTask(ctx, pool, failedBranch.ID, owner, epoch, failedBranch.FencingToken,
			"permanent_error", false, 0, time.Hour); err != nil {
			t.Fatal(err)
		}
		if state := workflowState(t, workflow.ID); state != "failed" {
			t.Fatalf("failed dependency did not fail the workflow: state=%q", state)
		}
		joinID := taskIDByEmission(t, workflow.ID, "join:barrier")
		if state := taskState(t, joinID); state == "succeeded" {
			t.Fatal("join barrier reported success after a dependency failed")
		}
	})

	t.Run("retry_rejects_join_task", func(t *testing.T) {
		owner, epoch := register(t)
		workflow := startWorkflow(t, owner, epoch, "retry-scope", `{"input":"retry"}`)
		defer cancelWorkflow(owner, epoch, workflow.ID)
		root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
		if err != nil {
			t.Fatal(err)
		}
		outcome := corecoord.TaskOutcome{Result: json.RawMessage(`{}`), Children: []corecoord.TaskChild{
			{Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: "retry:worker", Payload: json.RawMessage(`{}`), MaxAttempts: 2},
			{Kind: corecoord.TaskKindJoin, EmissionKey: "retry:join", Payload: json.RawMessage(`{}`),
				DependsOnEmissions: []string{"retry:worker"}},
		}}
		if err := CompleteCoordinatorTask(ctx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour, outcome); err != nil {
			t.Fatal(err)
		}
		claimed, err := ClaimTasks(ctx, pool, owner, epoch, []string{"engine.example", "engine.other", "worker.one", "worker.two", "worker.example", "worker.archive", "worker.media"}, 1, time.Minute, time.Hour)
		if err != nil || len(claimed) != 1 || claimed[0].EmissionKey != "retry:worker" {
			t.Fatalf("claim retryable worker: tasks=%+v err=%v", claimed, err)
		}
		if err := FailTask(ctx, pool, claimed[0].ID, owner, epoch, claimed[0].FencingToken,
			"permanent_error", false, 0, time.Hour); err != nil {
			t.Fatal(err)
		}
		joinID := taskIDByEmission(t, workflow.ID, "retry:join")
		if err := store.RetryTask(ctx, owner, workflow.ID, joinID, epoch); !errors.Is(err, ErrTaskMissing) {
			t.Fatalf("manual retry accepted a control join task: %v", err)
		}
		if err := store.RetryTask(ctx, owner, workflow.ID, claimed[0].ID, epoch); err != nil {
			t.Fatalf("manual retry rejected the failed worker task: %v", err)
		}
		if state := workflowState(t, workflow.ID); state != "queued" {
			t.Fatalf("worker retry did not reopen the workflow: state=%q", state)
		}
	})

	t.Run("canceled_context_store_outages", func(t *testing.T) {
		owner, epoch := register(t)
		workflow := startWorkflow(t, owner, epoch, "store-outage", `{"input":"outage"}`)
		defer cancelWorkflow(owner, epoch, workflow.ID)
		outageCtx, outageCancel := context.WithCancel(ctx)
		outageCancel()
		request := workflowTestRequest(owner, epoch, "engine.example", "process", `{"outage":"start"}`)
		request.IdempotencyKey = "outage:start"
		if _, _, err := StartCoordinatorWorkflow(outageCtx, pool, request); err == nil {
			t.Fatal("start outage returned success")
		}
		if count := scalarInt(t, `SELECT count(*) FROM coordination.workflows WHERE workflow_id=$1`, request.ID); count != 0 {
			t.Fatalf("start outage persisted %d workflows", count)
		}
		if _, err := ClaimTasks(outageCtx, pool, owner, epoch, []string{"engine.example"}, 1, time.Minute, time.Hour); err == nil {
			t.Fatal("claim outage returned success")
		}
		root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
		if err != nil {
			t.Fatal(err)
		}
		if err := CompleteCoordinatorTask(outageCtx, pool, root.ID, owner, epoch, root.FencingToken, 10, 4, time.Hour,
			corecoord.TaskOutcome{Result: json.RawMessage(`{"late":true}`)}); err == nil {
			t.Fatal("commit outage returned success")
		}
		if state := taskState(t, root.ID); state != "running" {
			t.Fatalf("commit outage partially fixed the task result: state=%q", state)
		}
		if _, err := GetCoordinatorWorkflow(outageCtx, pool, "engine.example", owner, epoch, workflow.ID); err == nil {
			t.Fatal("read outage returned success")
		}
		if err := CancelCoordinatorWorkflow(outageCtx, pool, "engine.example", owner, epoch, workflow.ID, time.Hour); err == nil {
			t.Fatal("cancel outage returned success")
		}
		if state := workflowState(t, workflow.ID); state != "running" {
			t.Fatalf("cancel outage changed the workflow state to %q", state)
		}
	})
}
