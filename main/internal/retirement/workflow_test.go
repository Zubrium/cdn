package retirement

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

const (
	retirePageID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	retireJobID  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	retireRouteA = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	retireRouteB = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

	retireIfMatch     = `"7"`
	retireIdemKey     = "retirement-key-0123456789abcdef"
	retireExpectedRev = 7
)

func retirementPayload() contracts.RetirementWorkflowPayload {
	return contracts.RetirementWorkflowPayload{
		JobID: retireJobID, PageID: retirePageID, ExpectedRevision: retireExpectedRev, IfMatch: retireIfMatch,
		IdempotencyKey: retireIdemKey,
		Routes:         []string{retireRouteA, retireRouteB},
	}
}

func TestRetirementStartHandlerEmitsOneRouteChildPerRouteAndDependentFinalize(t *testing.T) {
	handler, err := NewRetirementStartHandler(RetirementStartConfig{MaxAttempts: 4})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(retirementPayload())
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := handler.WorkerHandler(context.Background(), contracts.OperationTask{TaskID: uuid.NewString(), Payload: raw})
	if err != nil {
		t.Fatalf("WorkerHandler: %v", err)
	}
	if len(outcome.Children) != 3 {
		t.Fatalf("children = %d, want 3", len(outcome.Children))
	}
	routes := map[string]contracts.OperationTaskChild{}
	var finalize contracts.OperationTaskChild
	for _, child := range outcome.Children {
		if child.Version != contracts.RetirementChildContractVersion || child.MaxAttempts != 4 {
			t.Fatalf("child %s has version %d attempts %d", child.OperationID, child.Version, child.MaxAttempts)
		}
		switch child.OperationID {
		case contracts.RetirementRouteRemoveOperationID:
			payload, err := contracts.DecodePayload[contracts.RetirementRouteRemovePayload](child.Payload)
			if err != nil {
				t.Fatal(err)
			}
			routes[payload.RouteID] = child
			if child.EmissionKey != contracts.RetirementRouteEmissionKey(retireJobID, payload.RouteID) {
				t.Fatalf("unexpected route emission %q", child.EmissionKey)
			}
			if len(child.DependsOnEmissions) != 0 {
				t.Fatalf("route child must be independent")
			}
		case contracts.RetirementPageFinalizeOperationID:
			finalize = child
		default:
			t.Fatalf("unexpected child operation %q", child.OperationID)
		}
	}
	for _, routeID := range []string{retireRouteA, retireRouteB} {
		if _, ok := routes[routeID]; !ok {
			t.Fatalf("missing route child for %s", routeID)
		}
	}
	if finalize.OperationID != contracts.RetirementPageFinalizeOperationID ||
		finalize.EmissionKey != contracts.RetirementFinalizeEmissionKey(retireJobID) || len(finalize.DependsOnEmissions) != 2 {
		t.Fatalf("unexpected finalize child: %+v", finalize)
	}
	for _, key := range finalize.DependsOnEmissions {
		if key != contracts.RetirementRouteEmissionKey(retireJobID, retireRouteA) && key != contracts.RetirementRouteEmissionKey(retireJobID, retireRouteB) {
			t.Fatalf("finalize depends on unexpected key %q", key)
		}
	}
}

func TestRetirementStartHandlerRejectsSubstitutedIdentities(t *testing.T) {
	handler, err := NewRetirementStartHandler(RetirementStartConfig{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*contracts.RetirementWorkflowPayload){
		"page":     func(p *contracts.RetirementWorkflowPayload) { p.PageID = "not-a-page-id" },
		"job":      func(p *contracts.RetirementWorkflowPayload) { p.JobID = "bad" },
		"route":    func(p *contracts.RetirementWorkflowPayload) { p.Routes[0] = "bad" },
		"revision": func(p *contracts.RetirementWorkflowPayload) { p.ExpectedRevision = 0 },
		"key":      func(p *contracts.RetirementWorkflowPayload) { p.IdempotencyKey = "short" },
		"no routes": func(p *contracts.RetirementWorkflowPayload) {
			p.Routes = nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			payload := retirementPayload()
			mutate(&payload)
			raw, _ := json.Marshal(payload)
			if _, err := handler.WorkerHandler(context.Background(), contracts.OperationTask{TaskID: uuid.NewString(), Payload: raw}); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestRetirementChildPayloadCannotSelectClassOrModule(t *testing.T) {
	raw := []byte(`{"job_id":"` + retireJobID + `","page_id":"` + retirePageID + `","route_id":"` + retireRouteA + `","class_id":"engine.router"}`)
	if _, err := contracts.DecodePayload[contracts.RetirementRouteRemovePayload](raw); err == nil {
		t.Fatal("expected rejection of class_id in child payload")
	}
}

func TestRetirementWorkflowStartInputIsIdempotent(t *testing.T) {
	first := retirementWorkflowStartInput(retirementPayload())
	second := retirementWorkflowStartInput(retirementPayload())
	if strings.Contains(string(first.Payload), `"branch_id"`) {
		t.Fatalf("workflow payload contains a retired field: %s", first.Payload)
	}

	if first.OperationID != contracts.RetirementStartOperationID || first.ContractVersion != contracts.RetirementStartContractVersion {
		t.Fatalf("unexpected operation: %+v", first)
	}
	if first.OperationID != second.OperationID || first.ContractVersion != second.ContractVersion ||
		first.IdempotencyKey != second.IdempotencyKey || string(first.Payload) != string(second.Payload) ||
		!strings.HasPrefix(first.IdempotencyKey, "retirement:") {
		t.Fatalf("unexpected idempotency keys %q / %q", first.IdempotencyKey, second.IdempotencyKey)
	}
}
