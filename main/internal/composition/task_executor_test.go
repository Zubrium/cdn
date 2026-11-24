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

func TestClassScopedDataBusTaskExecutorRejectsForeignClass(t *testing.T) {
	parentKey := contracts.Key{ID: "run.parent", Version: 1}
	childKey := contracts.Key{ID: "run.child", Version: 1}
	bindings := map[contracts.Key]TaskOperationBinding{
		parentKey: {TaskClass: "engine.website", Handler: func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
			return contracts.OperationOutcome{Result: json.RawMessage(`{}`)}, nil
		}},
		childKey: {TaskClass: "worker.media", Handler: func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
			return contracts.OperationOutcome{Result: json.RawMessage(`{}`)}, nil
		}},
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 4}, parentKey, childKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterTaskOperations(dispatcher, handlersFor(bindings)); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	executor, err := NewClassScopedDataBusTaskExecutor(dispatcher, bindings, "engine.website")
	if err != nil {
		t.Fatal(err)
	}
	foreign := platform.Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: platform.TaskKindOperation,
		Operation: &platform.OperationRef{ClassID: "worker.media", ID: childKey.ID, Version: int(childKey.Version)}}
	_, err = executor.Execute(context.Background(), foreign)
	var failure *platform.TaskFailure
	if !errors.As(err, &failure) || failure.Code != "operation_unavailable" || failure.Retryable {
		t.Fatalf("class-scoped executor accepted a foreign-class task: %v", err)
	}
}

func TestClassScopedDataBusTaskExecutorResolvesCrossClassChildren(t *testing.T) {
	parentKey := contracts.Key{ID: "run.parent", Version: 1}
	childKey := contracts.Key{ID: "run.child", Version: 1}
	bindings := map[contracts.Key]TaskOperationBinding{
		parentKey: {TaskClass: "engine.website", Handler: func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
			return contracts.OperationOutcome{Children: []contracts.OperationTaskChild{{
				OperationID: childKey.ID, Version: childKey.Version, EmissionKey: "child", Payload: json.RawMessage(`{}`), MaxAttempts: 1,
			}}}, nil
		}},
		childKey: {TaskClass: "worker.media", Handler: func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
			return contracts.OperationOutcome{Result: json.RawMessage(`{}`)}, nil
		}},
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 4}, parentKey, childKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterTaskOperations(dispatcher, handlersFor(bindings)); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	executor, err := NewClassScopedDataBusTaskExecutor(dispatcher, bindings, "engine.website")
	if err != nil {
		t.Fatal(err)
	}
	task := platform.Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: platform.TaskKindOperation,
		Operation: &platform.OperationRef{ClassID: "engine.website", ID: parentKey.ID, Version: int(parentKey.Version)}}
	outcome, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("class-scoped parent execution failed: %v", err)
	}
	if len(outcome.Children) != 1 || outcome.Children[0].Operation == nil || outcome.Children[0].Operation.ClassID != "worker.media" {
		t.Fatalf("cross-class child was not resolved by the parent pool: %+v", outcome.Children)
	}
}

func TestNewDataBusTaskPoolsRejectsDuplicateClasses(t *testing.T) {
	key := contracts.Key{ID: "run.one", Version: 1}
	bindings := map[contracts.Key]TaskOperationBinding{key: {TaskClass: "worker.media", Handler: func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
		return contracts.OperationOutcome{}, nil
	}}}
	if _, err := NewDataBusTaskPools(nil, bindings, []TaskPoolSpec{
		{ClassID: "worker.media", Concurrency: 1}, {ClassID: "worker.media", Concurrency: 1},
	}); err == nil {
		t.Fatal("duplicate worker pool class accepted")
	}
}

func handlersFor(bindings map[contracts.Key]TaskOperationBinding) map[contracts.Key]bus.TaskOperationHandler {
	handlers := make(map[contracts.Key]bus.TaskOperationHandler, len(bindings))
	for key, binding := range bindings {
		handlers[key] = binding.Handler
	}
	return handlers
}
