//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/main/internal/retirement"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

func TestRetirementRootClassIsNotExecutableByRouterOrWebsitePools(t *testing.T) {
	handler := func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) {
		return contracts.OperationOutcome{Result: json.RawMessage(`{"ok":true}`)}, nil
	}
	rootKey := contracts.Key{ID: contracts.RetirementStartOperationID, Version: contracts.RetirementStartContractVersion}
	routeKey := contracts.Key{ID: contracts.RetirementRouteRemoveOperationID, Version: contracts.RetirementChildContractVersion}
	finalizeKey := contracts.Key{ID: contracts.RetirementPageFinalizeOperationID, Version: contracts.RetirementChildContractVersion}
	bindings := map[contracts.Key]TaskOperationBinding{
		rootKey:     {TaskClass: "composition.retirement", Handler: handler},
		routeKey:    {TaskClass: "engine.router", Handler: handler},
		finalizeKey: {TaskClass: websiteworkflow.ComponentID, Handler: handler},
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 8}, rootKey, routeKey, finalizeKey)
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
	for _, classID := range []string{"engine.router", websiteworkflow.ComponentID} {
		executor, err := NewClassScopedDataBusTaskExecutor(dispatcher, bindings, classID)
		if err != nil {
			t.Fatal(err)
		}
		task := platform.Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: platform.TaskKindOperation,
			Operation: &platform.OperationRef{ClassID: "composition.retirement", ID: contracts.RetirementStartOperationID, Version: 1},
			Payload:   json.RawMessage(`{}`)}
		_, err = executor.Execute(context.Background(), task)
		var failure *platform.TaskFailure
		if !errors.As(err, &failure) || failure.Code != "operation_unavailable" {
			t.Fatalf("%s pool root error = %v, want operation_unavailable", classID, err)
		}
	}
}

func TestRetirementFinalizationChecksJobAndCurrentPageRevision(t *testing.T) {
	job := retirement.Job{ID: "job", PageID: "page", ExpectedRevision: 10, IdempotencyKey: "key"}
	payload := contracts.RetirementPageFinalizePayload{JobID: "job", PageID: "page", ExpectedRevision: 10, IdempotencyKey: "key"}
	page := contracts.AdminWebsitePage{ID: "page", Revision: 23, LifecycleState: "retiring", RetirementJobID: "job"}
	if done, err := validateRetirementFinalization(payload, job, page); err != nil || done {
		t.Fatalf("retiring page with later revision: done=%v err=%v", done, err)
	}
	page.LifecycleState = "retired"
	if done, err := validateRetirementFinalization(payload, job, page); err != nil || !done {
		t.Fatalf("already retired page should resume idempotently: done=%v err=%v", done, err)
	}
	page.LifecycleState = "retiring"
	for name, change := range map[string]func(){
		"foreign job":       func() { page.RetirementJobID = "other" },
		"original revision": func() { payload.ExpectedRevision = 11 },
		"idempotency key":   func() { payload.IdempotencyKey = "other" },
		"stale page":        func() { page.Revision = 10 },
	} {
		p, j, v := payload, job, page
		change()
		if _, err := validateRetirementFinalization(payload, job, page); err == nil {
			t.Errorf("%s accepted", name)
		}
		payload, job, page = p, j, v
	}
}
