package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

func TestDataBusTaskExecutorDispatchesAndValidatesChildren(t *testing.T) {
	key := contracts.Key{ID: "inspect", Version: 1}
	bindings := map[contracts.Key]TaskOperationBinding{
		key: {TaskClass: "media-pool", Handler: func(_ context.Context, _ contracts.OperationTask) (contracts.OperationOutcome, error) {
			return contracts.OperationOutcome{Result: json.RawMessage(`{"ok":true}`)}, nil
		}},
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 4}, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterTaskOperations(dispatcher, handlersFor(bindings)); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	task := platform.Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: platform.TaskKindOperation,
		Operation: &platform.OperationRef{ClassID: "media-pool", ID: "inspect", Version: 1}}
	outcome, err := NewDataBusTaskExecutor(dispatcher, bindings).Execute(context.Background(), task)
	if err != nil || string(outcome.Result) != `{"ok":true}` {
		t.Fatalf("DataBus operation execution failed: outcome=%+v err=%v", outcome, err)
	}
}

func TestDataBusTaskExecutorRejectsUnknownChildOperationBeforeCommit(t *testing.T) {
	key := contracts.Key{ID: "inspect", Version: 1}
	bindings := map[contracts.Key]TaskOperationBinding{
		key: {TaskClass: "archive-pool", Handler: func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
			return contracts.OperationOutcome{Children: []contracts.OperationTaskChild{{
				OperationID: "run.missing", Version: 1, EmissionKey: "missing", Payload: json.RawMessage(`{}`), MaxAttempts: 1,
			}}}, nil
		}},
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 4}, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterTaskOperations(dispatcher, handlersFor(bindings)); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	task := platform.Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: platform.TaskKindOperation,
		Operation: &platform.OperationRef{ClassID: "archive-pool", ID: key.ID, Version: int(key.Version)}}
	_, err = NewDataBusTaskExecutor(dispatcher, bindings).Execute(context.Background(), task)
	var failure *platform.TaskFailure
	if !errors.As(err, &failure) || failure.Code != "operation_unavailable" || failure.Retryable {
		t.Fatalf("unknown child operation was not rejected non-retryably: %v", err)
	}
}

func handlersFor(bindings map[contracts.Key]TaskOperationBinding) map[contracts.Key]bus.TaskOperationHandler {
	handlers := make(map[contracts.Key]bus.TaskOperationHandler, len(bindings))
	for key, binding := range bindings {
		handlers[key] = binding.Handler
	}
	return handlers
}
