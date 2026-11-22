package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	appcoord "github.com/Zubrium/cdn/core/application/coordination"
	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

// TestDisposableOwnerScopedTaskResult proves that a registered component can
// read only the durable result of a task in its own workflow. It runs only
// against a freshly provisioned disposable database named by the env variable.
func TestDisposableOwnerScopedTaskResult(t *testing.T) {
	t.Run("CORE-001", func(t *testing.T) {
		pool := openDisposablePool(t, "TEST_CORE_OWNER_TASK_RESULT_DATABASE_URL")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := migrateForTest(t, ctx, pool); err != nil {
			t.Fatal(err)
		}

		store := CoordinatorStore{Pool: pool}
		owner := uuid.New()
		fingerprint := strings.Repeat("a", 64)
		epoch, err := RegisterCoordinatorInstance(ctx, pool, owner, fingerprint, time.Minute, time.Hour)
		if err != nil || epoch <= 0 {
			t.Fatalf("register owner instance: epoch=%d err=%v", epoch, err)
		}
		t.Cleanup(func() { _ = ReleaseCoordinatorInstance(context.Background(), pool, owner, epoch) })

		registry := appcoord.NewRuntimeRegistry()
		noop := func(context.Context, appcoord.Task) (corecoord.TaskOutcome, error) {
			return corecoord.TaskOutcome{Result: json.RawMessage(`{}`)}, nil
		}
		if err := registry.RegisterWorkerHandlers("engine.website", map[string]appcoord.WorkerHandler{
			"build-page/v1": noop,
		}); err != nil {
			t.Fatal(err)
		}
		workers, err := registry.CompileWorkerCatalog(appcoord.BuildPlan{Components: []string{"engine.website"}})
		if err != nil {
			t.Fatal(err)
		}
		client, err := appcoord.NewWorkflowClient(appcoord.WorkflowClientConfig{
			Store: store, Workers: workers, OwnerEngineID: "engine.website",
			InstanceID: owner, ConfigEpoch: epoch, MaxAttempts: 3, Retention: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}

		receipt, err := client.Start(ctx, appcoord.WorkflowStartRequest{
			OperationID: "build-page", ContractVersion: 1,
			IdempotencyKey: "owner-scoped-result-001", Payload: json.RawMessage(`{"build_id":"integration"}`),
		})
		if err != nil || receipt.WorkflowID == uuid.Nil || receipt.RootTaskID == uuid.Nil {
			t.Fatalf("start workflow: %+v %v", receipt, err)
		}

		root, err := claimWorkflowRoot(ctx, pool, owner, epoch)
		if err != nil {
			t.Fatal(err)
		}
		rootOutcome := corecoord.TaskOutcome{
			Result: json.RawMessage(`{"root":"committed"}`),
			Children: []corecoord.TaskChild{{
				Kind: corecoord.TaskKindWorker,
				Worker: &corecoord.WorkerRef{
					ComponentID: "worker.prerender", OperationID: "render-page", ContractVersion: 1,
				},
				EmissionKey: "prerender:integration-build",
				Payload:     json.RawMessage(`{"plan":"pinned"}`),
				MaxAttempts: 2,
			}},
		}
		if err := store.Complete(ctx, root.ID, owner, epoch, root.FencingToken, 100, 8, time.Hour, rootOutcome); err != nil {
			t.Fatal(err)
		}

		claimed, err := store.Claim(ctx, owner, epoch, 1, time.Minute, time.Hour)
		if err != nil || len(claimed) != 1 || claimed[0].Kind != corecoord.TaskKindWorker {
			t.Fatalf("claim prerender child: %+v %v", claimed, err)
		}
		child := claimed[0]
		childResult := json.RawMessage(`{"format":"website.build-result/v1","input_digest":"abc"}`)
		if err := store.Complete(ctx, child.ID, owner, epoch, child.FencingToken, 100, 8, time.Hour,
			corecoord.TaskOutcome{Result: childResult}); err != nil {
			t.Fatal(err)
		}

		result, err := client.GetTaskResult(ctx, receipt.WorkflowID, child.ID)
		if err != nil {
			t.Fatalf("owner-scoped result read: %v", err)
		}
		if result.TaskID != child.ID || result.WorkflowID != receipt.WorkflowID || result.State != "succeeded" {
			t.Fatalf("unexpected result identity/state: %+v", result)
		}
		if result.Worker == nil || result.Worker.ComponentID != "worker.prerender" || result.Worker.OperationID != "render-page" || result.Worker.ContractVersion != 1 {
			t.Fatalf("unexpected worker reference: %+v", result.Worker)
		}
		var gotPayload, wantPayload any
		if err := json.Unmarshal(result.Result, &gotPayload); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(childResult, &wantPayload); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotPayload, wantPayload) {
			t.Fatalf("result payload = %s, want %s", result.Result, childResult)
		}

		t.Run("foreign engine cannot read", func(t *testing.T) {
			foreign, err := appcoord.NewWorkflowClient(appcoord.WorkflowClientConfig{
				Store: store, Workers: mustWorkflowCatalog(t, "engine.content", "read/v1"),
				OwnerEngineID: "engine.content", InstanceID: owner, ConfigEpoch: epoch,
				MaxAttempts: 3, Retention: time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := foreign.GetTaskResult(ctx, receipt.WorkflowID, child.ID); !errors.Is(err, corecoord.ErrTaskMissing) {
				t.Fatalf("foreign engine result read = %v, want ErrTaskMissing", err)
			}
		})

		t.Run("unknown workflow and task are rejected", func(t *testing.T) {
			if _, err := client.GetTaskResult(ctx, uuid.New(), child.ID); !errors.Is(err, corecoord.ErrTaskMissing) {
				t.Fatalf("unknown workflow result read = %v, want ErrTaskMissing", err)
			}
			if _, err := client.GetTaskResult(ctx, receipt.WorkflowID, uuid.New()); !errors.Is(err, corecoord.ErrTaskMissing) {
				t.Fatalf("unknown task result read = %v, want ErrTaskMissing", err)
			}
		})
	})
}

func mustWorkflowCatalog(t *testing.T, componentID, operation string) *appcoord.WorkerCatalog {
	t.Helper()
	registry := appcoord.NewRuntimeRegistry()
	noop := func(context.Context, appcoord.Task) (corecoord.TaskOutcome, error) {
		return corecoord.TaskOutcome{Result: json.RawMessage(`{}`)}, nil
	}
	if err := registry.RegisterWorkerHandlers(componentID, map[string]appcoord.WorkerHandler{operation: noop}); err != nil {
		t.Fatal(err)
	}
	catalog, err := registry.CompileWorkerCatalog(appcoord.BuildPlan{Components: []string{componentID}})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
