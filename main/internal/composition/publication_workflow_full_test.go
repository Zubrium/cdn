//go:build full && !devfixtures && !router

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

func TestPublicationAndRetirementTaskClassesAreScopedToTheirPools(t *testing.T) {
	handler := func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
		return contracts.OperationOutcome{Result: json.RawMessage(`{"ok":true}`)}, nil
	}
	startKey := contracts.Key{ID: contracts.PublicationStartOperationID, Version: contracts.PublicationStartContractVersion}
	buildKey := contracts.Key{ID: contracts.PublicationBuildOperationID, Version: contracts.PublicationChildContractVersion}
	activateKey := contracts.Key{ID: contracts.PublicationActivateOperationID, Version: contracts.PublicationChildContractVersion}
	routeKey := contracts.Key{ID: contracts.RetirementRouteRemoveOperationID, Version: contracts.RetirementChildContractVersion}
	bindings := map[contracts.Key]TaskOperationBinding{
		startKey:    {TaskClass: "engine.publication", Handler: handler},
		buildKey:    {TaskClass: "engine.website", Handler: handler},
		activateKey: {TaskClass: "engine.router", Handler: handler},
		routeKey:    {TaskClass: "engine.router", Handler: handler},
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 8}, startKey, buildKey, activateKey, routeKey)
	if err != nil {
		t.Fatal(err)
	}
	handlers := make(map[contracts.Key]bus.TaskOperationHandler, len(bindings))
	for key, binding := range bindings {
		handlers[key] = binding.Handler
	}
	if err := bus.RegisterTaskOperations(dispatcher, handlers); err != nil {
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
		Operation: &platform.OperationRef{ClassID: "engine.website", ID: contracts.PublicationBuildOperationID, Version: 1},
		Payload:   json.RawMessage(`{}`)}
	if _, err := executor.Execute(context.Background(), task); err != nil {
		t.Fatalf("own class task: %v", err)
	}
	task.Operation = &platform.OperationRef{ClassID: "engine.router", ID: contracts.PublicationActivateOperationID, Version: 1}
	_, err = executor.Execute(context.Background(), task)
	var failure *platform.TaskFailure
	if !errors.As(err, &failure) || failure.Code != "operation_unavailable" {
		t.Fatalf("foreign class error = %v, want operation_unavailable", err)
	}
	task.Operation = &platform.OperationRef{ClassID: "engine.publication", ID: contracts.PublicationStartOperationID, Version: 1}
	if _, err := executor.Execute(context.Background(), task); err == nil {
		t.Fatal("root class must not be executable by website pool")
	}
}

func TestWorkflowHandlerKeepsOwningCoreAcrossInitialization(t *testing.T) {
	component := &coreComponent{}
	other := &coreComponent{}
	var received *coreComponent
	handler := bindWorkflowCore(component, func(_ context.Context, _ contracts.OperationTask, core *coreComponent) (contracts.OperationOutcome, error) {
		received = core
		return contracts.OperationOutcome{}, nil
	})
	// Registration precedes component startup; handler execution happens later.
	if _, err := handler(context.Background(), contracts.OperationTask{}); err != nil {
		t.Fatal(err)
	}
	if received != component {
		t.Fatal("registered task did not retain the owning Core instance")
	}
	otherHandler := bindWorkflowCore(other, func(_ context.Context, _ contracts.OperationTask, core *coreComponent) (contracts.OperationOutcome, error) {
		received = core
		return contracts.OperationOutcome{}, nil
	})
	if _, err := otherHandler(context.Background(), contracts.OperationTask{}); err != nil || received != other {
		t.Fatal("second registration did not retain its own Core instance")
	}
	if _, err := handler(context.Background(), contracts.OperationTask{}); err != nil || received != component {
		t.Fatal("second registration changed the first handler's Core instance")
	}
}
