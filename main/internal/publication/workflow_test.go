package publication

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

const (
	testRunID      = "11111111-1111-4111-8111-111111111111"
	testRouteA     = "22222222-2222-4222-8222-222222222222"
	testRouteB     = "33333333-3333-4333-8333-333333333333"
	testSnapshotID = "44444444-4444-4444-8444-444444444444"
	testLocale     = "en-US"
	testKey        = "publication-key-0123456789abcdef"
)

func workflowTestPayload() contracts.PublicationWorkflowPayload {
	return contracts.PublicationWorkflowPayload{
		RunID:                      testRunID,
		Locale:                     testLocale,
		ContentPublicationRevision: 4,
		SystemModelRevision:        9,
		IdempotencyKey:             testKey,
		Targets: []contracts.PublicationWorkflowTarget{
			{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale},
			{RouteID: testRouteB, PageSnapshotID: testSnapshotID, Locale: testLocale},
		},
	}
}

func TestPublishStartHandlerEmitsExactChildren(t *testing.T) {
	handler, err := NewPublishStartHandler(PublishStartConfig{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewPublishStartHandler: %v", err)
	}
	payload := workflowTestPayload()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := handler.WorkerHandler(context.Background(), contracts.OperationTask{TaskID: uuid.NewString(), Payload: raw})
	if err != nil {
		t.Fatalf("WorkerHandler: %v", err)
	}
	result, err := contracts.DecodePayload[contracts.PublicationStartResult](outcome.Result)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Format != contracts.PublicationWorkflowFormat || result.RunID != testRunID || len(result.BuildEmissionKeys) != 2 || len(result.ActivateEmissionKeys) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(outcome.Children) != 5 {
		t.Fatalf("children = %d, want 5", len(outcome.Children))
	}

	buildByRoute := make(map[string]contracts.OperationTaskChild)
	activateByRoute := make(map[string]contracts.OperationTaskChild)
	var finalize contracts.OperationTaskChild
	for _, child := range outcome.Children {
		if child.Version != contracts.PublicationChildContractVersion || child.MaxAttempts != 3 {
			t.Fatalf("child %s has version %d attempts %d", child.OperationID, child.Version, child.MaxAttempts)
		}
		switch child.OperationID {
		case contracts.PublicationBuildOperationID:
			childPayload, err := contracts.DecodePayload[contracts.PublicationBuildPayload](child.Payload)
			if err != nil {
				t.Fatalf("decode build payload: %v", err)
			}
			buildByRoute[childPayload.RouteID] = child
		case contracts.PublicationActivateOperationID:
			childPayload, err := contracts.DecodePayload[contracts.PublicationActivatePayload](child.Payload)
			if err != nil {
				t.Fatalf("decode activate payload: %v", err)
			}
			activateByRoute[childPayload.RouteID] = child
		case contracts.PublicationFinalizeOperationID:
			finalize = child
		default:
			t.Fatalf("unexpected child operation %q", child.OperationID)
		}
	}
	for _, routeID := range []string{testRouteA, testRouteB} {
		build, ok := buildByRoute[routeID]
		if !ok {
			t.Fatalf("missing build child for %s", routeID)
		}
		activate, ok := activateByRoute[routeID]
		if !ok {
			t.Fatalf("missing activate child for %s", routeID)
		}
		if build.EmissionKey != contracts.PublicationBuildEmissionKey(testRunID, routeID) ||
			activate.EmissionKey != contracts.PublicationActivateEmissionKey(testRunID, routeID) {
			t.Fatalf("unexpected emission keys %q / %q", build.EmissionKey, activate.EmissionKey)
		}
		if len(build.DependsOnEmissions) != 0 {
			t.Fatalf("build must be independent: %v", build.DependsOnEmissions)
		}
		if len(activate.DependsOnEmissions) != 1 || activate.DependsOnEmissions[0] != build.EmissionKey {
			t.Fatalf("activate must wait for its build: %v", activate.DependsOnEmissions)
		}
		if len(finalize.DependsOnEmissions) != 2 {
			t.Fatalf("finalize dependencies = %v", finalize.DependsOnEmissions)
		}
	}
	if finalize.OperationID != contracts.PublicationFinalizeOperationID ||
		finalize.EmissionKey != contracts.PublicationFinalizeEmissionKey(testRunID) {
		t.Fatalf("unexpected finalize child: %+v", finalize)
	}
	for _, key := range finalize.DependsOnEmissions {
		if key != contracts.PublicationActivateEmissionKey(testRunID, testRouteA) && key != contracts.PublicationActivateEmissionKey(testRunID, testRouteB) {
			t.Fatalf("finalize depends on unexpected key %q", key)
		}
	}
}

func TestPublishStartHandlerRejectsServerOwnedPayloadSubstitution(t *testing.T) {
	handler, err := NewPublishStartHandler(PublishStartConfig{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*contracts.PublicationWorkflowPayload){
		"run_id": func(p *contracts.PublicationWorkflowPayload) { p.RunID = "not-a-uuid" },
		"page_snapshot_id": func(p *contracts.PublicationWorkflowPayload) {
			p.Targets[0].PageSnapshotID = "not-a-snapshot-id"
		},
		"route_id":   func(p *contracts.PublicationWorkflowPayload) { p.Targets[0].RouteID = "bad" },
		"locale":     func(p *contracts.PublicationWorkflowPayload) { p.Targets[0].Locale = "fr-FR" },
		"revision":   func(p *contracts.PublicationWorkflowPayload) { p.ContentPublicationRevision = -1 },
		"empty key":  func(p *contracts.PublicationWorkflowPayload) { p.IdempotencyKey = "" },
		"no targets": func(p *contracts.PublicationWorkflowPayload) { p.Targets = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			payload := workflowTestPayload()
			mutate(&payload)
			raw, _ := json.Marshal(payload)
			if _, err := handler.WorkerHandler(context.Background(), contracts.OperationTask{TaskID: uuid.NewString(), Payload: raw}); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublicationChildPayloadCannotSelectClassOrModule(t *testing.T) {
	build := []byte(`{"run_id":"` + testRunID + `","route_id":"` + testRouteA + `","page_snapshot_id":"` + testSnapshotID +
		`","locale":"` + testLocale + `","content_publication_revision":4,"system_model_revision":9,"idempotency_key":"` + testKey + `","module_id":"engine.router"}`)
	if _, err := contracts.DecodePayload[contracts.PublicationBuildPayload](build); err == nil {
		t.Fatal("expected rejection of module_id in build payload")
	}
	activate := []byte(`{"run_id":"` + testRunID + `","route_id":"` + testRouteA + `","locale":"` + testLocale +
		`","artifact_manifest_file_id":"file","idempotency_key":"` + testKey + `","task_class":"engine.website"}`)
	if _, err := contracts.DecodePayload[contracts.PublicationActivatePayload](activate); err == nil {
		t.Fatal("expected rejection of task_class in activate payload")
	}
	root := []byte(`{"run_id":"` + testRunID + `","module":"engine.content"}`)
	if _, err := contracts.DecodePayload[contracts.PublicationWorkflowPayload](root); err == nil {
		t.Fatal("expected rejection of module in root payload")
	}
}

func TestPublicationWorkflowStartInputIsIdempotent(t *testing.T) {
	trigger := Trigger{Locale: testLocale, ContentPublicationRevision: 4, SystemModelRevision: 9, IdempotencyKey: testKey}
	first := workflowStartInput(trigger, testLocale, testRunID, workflowTestPayload().Targets)
	second := workflowStartInput(trigger, testLocale, testRunID, workflowTestPayload().Targets)
	if strings.Contains(string(first.Payload), `"unexpected_field"`) {
		t.Fatalf("workflow payload contains an unexpected field: %s", first.Payload)
	}

	if first.OperationID != second.OperationID || first.ContractVersion != second.ContractVersion ||
		first.IdempotencyKey != second.IdempotencyKey || string(first.Payload) != string(second.Payload) {
		t.Fatalf("start inputs differ: %+v vs %+v", first, second)
	}
	if first.OperationID != contracts.PublicationStartOperationID || first.ContractVersion != contracts.PublicationStartContractVersion {
		t.Fatalf("unexpected operation: %+v", first)
	}
	if !strings.HasPrefix(first.IdempotencyKey, "publication:") {
		t.Fatalf("unexpected idempotency key %q", first.IdempotencyKey)
	}
}

func TestPublicationServiceRetryRestartsSameWorkflow(t *testing.T) {
	store := newMemoryRecordStore()
	starter := &recordingWorkflowStarter{}
	service, err := NewService(Config{
		Store: store, Starter: starter, MaxAttempts: 3,
		Targets: []Target{{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale}},
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger := Trigger{Locale: testLocale, ContentPublicationRevision: 4, SystemModelRevision: 9, IdempotencyKey: testKey}
	published, err := service.Publish(context.Background(), trigger)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(starter.inputs) != 1 {
		t.Fatalf("workflow starts = %d, want 1", len(starter.inputs))
	}
	if _, err := service.Retry(context.Background(), published.RunID); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if len(starter.inputs) != 2 || starter.inputs[0].IdempotencyKey != starter.inputs[1].IdempotencyKey ||
		string(starter.inputs[0].Payload) != string(starter.inputs[1].Payload) {
		t.Fatalf("retry did not reuse start input: %+v", starter.inputs)
	}
}

func TestPublicationRetryRejectsUnknownRun(t *testing.T) {
	service, err := NewService(Config{Store: newMemoryRecordStore(), MaxAttempts: 3,
		Targets: []Target{{RouteID: testRouteA, PageSnapshotID: testSnapshotID, Locale: testLocale}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Retry(context.Background(), testRunID); !errors.Is(err, contracts.ErrRecordNotFound) {
		t.Fatalf("Retry error = %v, want not found", err)
	}
}

type recordingWorkflowStarter struct {
	inputs []contracts.WorkflowStartInput
}

func (starter *recordingWorkflowStarter) Start(_ context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	starter.inputs = append(starter.inputs, input)
	return contracts.WorkflowStartReceipt{WorkflowID: uuid.New(), RootTaskID: uuid.New(), State: "running"}, nil
}

type memoryRecordStore struct {
	records map[contracts.RecordKey]contracts.Record
}

func newMemoryRecordStore() *memoryRecordStore {
	return &memoryRecordStore{records: make(map[contracts.RecordKey]contracts.Record)}
}

func (store *memoryRecordStore) Get(_ context.Context, key contracts.RecordKey) (contracts.Record, error) {
	record, ok := store.records[key]
	if !ok {
		return contracts.Record{}, contracts.ErrRecordNotFound
	}
	return record, nil
}

func (store *memoryRecordStore) GetExpired(ctx context.Context, key contracts.RecordKey) (contracts.Record, error) {
	return store.Get(ctx, key)
}

func (store *memoryRecordStore) Put(_ context.Context, key contracts.RecordKey, value contracts.RecordValue, expected int64) (contracts.Record, error) {
	current, exists := store.records[key]
	if expected == 0 && exists || expected > 0 && (!exists || current.Revision != expected) {
		return contracts.Record{}, contracts.ErrRecordConflict
	}
	revision := int64(1)
	if exists {
		revision = current.Revision + 1
	}
	record := contracts.Record{Payload: append([]byte(nil), value.Payload...), PayloadVersion: value.PayloadVersion,
		IndexData: append([]byte(nil), value.IndexData...), Revision: revision}
	store.records[key] = record
	return record, nil
}

func (store *memoryRecordStore) Delete(_ context.Context, key contracts.RecordKey, expected int64) error {
	current, exists := store.records[key]
	if !exists || (expected > 0 && current.Revision != expected) {
		return contracts.ErrRecordConflict
	}
	delete(store.records, key)
	return nil
}

func (store *memoryRecordStore) Batch(ctx context.Context, mutations []contracts.RecordMutation) error {
	for _, mutation := range mutations {
		if _, err := store.Put(ctx, mutation.Key, *mutation.Value, mutation.ExpectedRevision); err != nil {
			return err
		}
	}
	return nil
}

func (store *memoryRecordStore) List(context.Context, string, string, string, string, []contracts.RecordSelector, int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	return nil, nil, "", nil
}

func (store *memoryRecordStore) ListExpired(context.Context, string, string, string, string, int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	return nil, nil, "", nil
}

func (store *memoryRecordStore) MaxPageSize() int     { return 100 }
func (store *memoryRecordStore) MaxBatchSize() int    { return 100 }
func (store *memoryRecordStore) MaxPayloadBytes() int { return 1 << 20 }
func (store *memoryRecordStore) MaxIndexBytes() int   { return 1 << 16 }
func (store *memoryRecordStore) MaxBatchBytes() int   { return 4 << 20 }
