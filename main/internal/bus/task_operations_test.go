package bus

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestTaskOperationsDispatchThroughAllowlistedBus(t *testing.T) {
	key := contracts.Key{ID: "inspect", Version: 1}
	taskID := "task-opaque"
	handlers := map[contracts.Key]TaskOperationHandler{key: func(_ context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
		return contracts.OperationOutcome{Result: json.RawMessage(`{"task_id":"` + task.TaskID + `"}`)}, nil
	}}
	dispatcher, err := NewAllowlisted(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterTaskOperations(dispatcher, handlers); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	response, err := contracts.Call(context.Background(), dispatcher,
		contracts.Operation[contracts.OperationTask, contracts.OperationOutcome]{Key: key},
		contracts.OperationTask{TaskID: taskID, WorkflowID: "workflow-opaque"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(response.Result) != `{"task_id":"`+taskID+`"}` {
		t.Fatalf("task operation response = %s", response.Result)
	}
}
