package retirement

import (
	"context"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type retirementMemoryStore struct{ records map[contracts.RecordKey]contracts.Record }

func newRetirementMemoryStore() *retirementMemoryStore {
	return &retirementMemoryStore{records: make(map[contracts.RecordKey]contracts.Record)}
}

func (store *retirementMemoryStore) Get(_ context.Context, key contracts.RecordKey) (contracts.Record, error) {
	record, ok := store.records[key]
	if !ok {
		return contracts.Record{}, contracts.ErrRecordNotFound
	}
	return record, nil
}
func (store *retirementMemoryStore) GetExpired(ctx context.Context, key contracts.RecordKey) (contracts.Record, error) {
	return store.Get(ctx, key)
}
func (store *retirementMemoryStore) Put(_ context.Context, key contracts.RecordKey, value contracts.RecordValue, expected int64) (contracts.Record, error) {
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
func (store *retirementMemoryStore) Delete(_ context.Context, key contracts.RecordKey, expected int64) error {
	current, exists := store.records[key]
	if !exists || (expected > 0 && current.Revision != expected) {
		return contracts.ErrRecordConflict
	}
	delete(store.records, key)
	return nil
}
func (store *retirementMemoryStore) Batch(ctx context.Context, mutations []contracts.RecordMutation) error {
	for _, mutation := range mutations {
		if _, err := store.Put(ctx, mutation.Key, *mutation.Value, mutation.ExpectedRevision); err != nil {
			return err
		}
	}
	return nil
}
func (store *retirementMemoryStore) List(context.Context, string, string, string, string, []contracts.RecordSelector, int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	return nil, nil, "", nil
}
func (store *retirementMemoryStore) ListExpired(context.Context, string, string, string, string, int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	return nil, nil, "", nil
}
func (store *retirementMemoryStore) MaxPageSize() int     { return 100 }
func (store *retirementMemoryStore) MaxBatchSize() int    { return 100 }
func (store *retirementMemoryStore) MaxPayloadBytes() int { return 1 << 20 }
func (store *retirementMemoryStore) MaxIndexBytes() int   { return 1 << 16 }
func (store *retirementMemoryStore) MaxBatchBytes() int   { return 4 << 20 }

type retirementStarter struct{ inputs []contracts.WorkflowStartInput }

func (starter *retirementStarter) Start(_ context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	starter.inputs = append(starter.inputs, input)
	return contracts.WorkflowStartReceipt{State: "running"}, nil
}

func TestRetirementPrepareLaunchIsIdempotentAndRejectsUnknownPage(t *testing.T) {
	starter := &retirementStarter{}
	service, err := NewService(Config{
		BranchID: retireBranchID, Store: newRetirementMemoryStore(), Starter: starter, MaxAttempts: 3,
		Routes: map[string][]string{retirePageID: {retireRouteA, retireRouteB}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.RetirePageRequest{PageID: retirePageID, IfMatch: retireIfMatch, IdempotencyKey: retireIdemKey}
	job, err := service.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	repeated, err := service.Prepare(context.Background(), request)
	if err != nil || repeated.ID != job.ID {
		t.Fatalf("Prepare idempotency = %+v, %v", repeated, err)
	}
	if _, err := service.Prepare(context.Background(), contracts.RetirePageRequest{
		PageID: "11111111-1111-4111-8111-111111111111", IfMatch: retireIfMatch, IdempotencyKey: retireIdemKey,
	}); err == nil {
		t.Fatal("accepted an unregistered page")
	}
	view, err := service.Launch(context.Background(), job)
	if err != nil || view.ID != job.ID {
		t.Fatalf("Launch = %+v, %v", view, err)
	}
	if len(starter.inputs) != 1 || starter.inputs[0].OperationID != contracts.RetirementStartOperationID {
		t.Fatalf("workflow starts: %+v", starter.inputs)
	}
	payload, err := contracts.DecodePayload[contracts.RetirementWorkflowPayload](starter.inputs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if payload.PageID != retirePageID || len(payload.Routes) != 2 || payload.ExpectedRevision != retireExpectedRev {
		t.Fatalf("root payload = %+v", payload)
	}
}

func TestRetirementRouteAndFinalizeStateTransitions(t *testing.T) {
	service, err := NewService(Config{
		BranchID: retireBranchID, Store: newRetirementMemoryStore(), Starter: &retirementStarter{}, MaxAttempts: 3,
		Routes: map[string][]string{retirePageID: {retireRouteA, retireRouteB}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.RetirePageRequest{PageID: retirePageID, IfMatch: retireIfMatch, IdempotencyKey: retireIdemKey}
	job, err := service.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := service.RecordRoute(ctx, job.ID, retireRouteA, routeStateRemoved, 1, ""); err != nil {
		t.Fatalf("RecordRoute: %v", err)
	}
	if err := service.RecordRoute(ctx, job.ID, retireRouteB, routeStateRetryable, 0, "unavailable"); err != nil {
		t.Fatalf("RecordRoute: %v", err)
	}
	if err := service.MarkRetryable(ctx, job.ID); err != nil {
		t.Fatalf("MarkRetryable: %v", err)
	}
	view, err := service.Retry(ctx, job.ID)
	if err != nil || view.State != stateRunning {
		t.Fatalf("Retry = %+v, %v", view, err)
	}
	if err := service.RecordRoute(ctx, job.ID, retireRouteB, routeStateRemoved, 1, ""); err != nil {
		t.Fatalf("RecordRoute retry: %v", err)
	}
	updated, err := service.SetJobState(ctx, job.ID, stateSucceeded)
	if err != nil || updated.State != stateSucceeded {
		t.Fatalf("SetJobState = %+v, %v", updated, err)
	}
	view = JobView(updated)
	for _, route := range view.Routes {
		if route.State != routeStateRemoved {
			t.Fatalf("route %s = %s", route.RouteID, route.State)
		}
	}
}
