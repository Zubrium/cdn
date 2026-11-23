package coordinator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

func TestTaskExecutorDelegatesOperationExecutionToCompositionPort(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{task: workerTestTask(owner), completeCh: make(chan corecoord.TaskOutcome, 1)}
	called := make(chan Task, 1)
	executor := OperationExecutorFunc(func(_ context.Context, task Task) (corecoord.TaskOutcome, error) {
		called <- task
		return corecoord.TaskOutcome{Result: json.RawMessage(`{"dispatched":true}`)}, nil
	})
	pool, err := NewTaskExecutor(store, workerTestConfig(owner), executor)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case task := <-called:
		if task.ID != store.task.ID {
			t.Fatalf("executor received task %s, want %s", task.ID, store.task.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("task executor did not delegate to the composition port")
	}
	select {
	case outcome := <-store.completeCh:
		if string(outcome.Result) != `{"dispatched":true}` {
			t.Fatalf("persisted result = %s", outcome.Result)
		}
	case <-time.After(time.Second):
		t.Fatal("delegated operation result was not persisted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
