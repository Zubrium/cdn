//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

func TestPublicationActivatedRevisionChecksExactLocaleCASResult(t *testing.T) {
	payload := contracts.PublicationActivatePayload{RouteID: "route", Locale: "en-US", ArtifactManifestFileID: "artifact"}
	route := contracts.OrchestrationRouteState{ID: "route", Revision: 99, Slot: &contracts.OrchestrationLocaleState{
		Locale: "en-US", Revision: 8, ArtifactManifestFileID: "artifact",
	}}
	if revision, err := publicationActivatedRevision(route, payload, 7); err != nil || revision != 8 {
		t.Fatalf("valid locale CAS: revision=%d err=%v", revision, err)
	}
	for name, mutate := range map[string]func(*contracts.OrchestrationRouteState){
		"other route":    func(r *contracts.OrchestrationRouteState) { r.ID = "other" },
		"later revision": func(r *contracts.OrchestrationRouteState) { r.Slot.Revision = 9 },
		"other artifact": func(r *contracts.OrchestrationRouteState) { r.Slot.ArtifactManifestFileID = "other" },
		"missing locale": func(r *contracts.OrchestrationRouteState) { r.Slot = nil },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := route
			slot := *route.Slot
			candidate.Slot = &slot
			mutate(&candidate)
			if _, err := publicationActivatedRevision(candidate, payload, 7); !errors.Is(err, publication.ErrStale) {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

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

type recordingBuiltArtifactResolver struct {
	id     string
	err    error
	runID  string
	route  string
	locale string
}

func (resolver *recordingBuiltArtifactResolver) BuiltArtifactFileID(_ context.Context, runID, routeID, locale string) (string, error) {
	resolver.runID, resolver.route, resolver.locale = runID, routeID, locale
	return resolver.id, resolver.err
}

func TestPublicationActivationUsesIssuedArtifactInsteadOfRootRequestKey(t *testing.T) {
	requestKey := contracts.PublicationArtifactFileID("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "en-US")
	issued := "55555555-5555-4555-8555-555555555555"
	resolver := &recordingBuiltArtifactResolver{id: issued}
	payload := contracts.PublicationActivatePayload{
		RunID: "11111111-1111-4111-8111-111111111111", RouteID: "22222222-2222-4222-8222-222222222222",
		Locale: "en-US", ArtifactManifestFileID: requestKey, IdempotencyKey: "publication-key-0123456789abcdef",
	}
	resolved, err := publicationActivationPayloadWithBuiltArtifact(context.Background(), resolver, payload)
	if err != nil || resolved.ArtifactManifestFileID != issued {
		t.Fatalf("resolved artifact = %q, %v; want %q", resolved.ArtifactManifestFileID, err, issued)
	}
	if resolver.runID != payload.RunID || resolver.route != payload.RouteID || resolver.locale != payload.Locale {
		t.Fatalf("resolver received %q/%q/%q", resolver.runID, resolver.route, resolver.locale)
	}
	if payload.ArtifactManifestFileID != requestKey {
		t.Fatal("resolution must not mutate the durable task payload")
	}
}

func TestPublicationActivationPropagatesMissingBuiltArtifact(t *testing.T) {
	resolver := &recordingBuiltArtifactResolver{err: publication.ErrStale}
	payload := contracts.PublicationActivatePayload{
		RunID: "11111111-1111-4111-8111-111111111111", RouteID: "22222222-2222-4222-8222-222222222222",
		Locale: "en-US", ArtifactManifestFileID: "33333333-3333-4333-8333-333333333333", IdempotencyKey: "publication-key-0123456789abcdef",
	}
	if _, err := publicationActivationPayloadWithBuiltArtifact(context.Background(), resolver, payload); !errors.Is(err, publication.ErrStale) {
		t.Fatalf("error = %v, want ErrStale", err)
	}
}
