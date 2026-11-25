package bus

import (
	"context"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestModuleDispatcherBindsSourceIdentityOutsidePayload(t *testing.T) {
	dispatcher, err := NewAllowlisted(ConcurrencyLimits{MaxInFlight: 1}, contracts.CoreReadinessOperation().Key)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	if err := RegisterCoreReadiness(dispatcher, func(ctx context.Context) error {
		source = ModuleIDFromContext(ctx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	client := NewModuleDispatcher(dispatcher, "engine.website")
	if _, err := contracts.Call(context.Background(), client, contracts.CoreReadinessOperation(), contracts.CoreReadinessRequest{}); err != nil {
		t.Fatal(err)
	}
	if source != "engine.website" {
		t.Fatalf("source module = %q, want engine.website", source)
	}
}

func TestModuleDispatcherRejectsEmptyOwner(t *testing.T) {
	dispatcher := NewModuleDispatcher(nil, " ")
	if dispatcher != nil {
		t.Fatal("dispatcher accepted an empty module identity")
	}
}

func TestModuleDispatcherRejectsOperationOutsideModuleAllowlist(t *testing.T) {
	dispatcher, err := NewAllowlisted(ConcurrencyLimits{MaxInFlight: 1},
		contracts.CoreReadinessOperation().Key, contracts.WorkflowListOperation().Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	client := NewModuleDispatcher(dispatcher, "engine.website")
	_, err = contracts.Call(context.Background(), client, contracts.WorkflowListOperation(), contracts.WorkflowListRequest{})
	if !errors.Is(err, ErrOperationNotAllowedForModule) {
		t.Fatalf("error = %v, want ErrOperationNotAllowedForModule", err)
	}
}

func TestRecordOperationsResolveCapabilityFromBoundModuleScope(t *testing.T) {
	dispatcher, err := NewAllowlisted(ConcurrencyLimits{MaxInFlight: 1},
		contracts.RecordGetOperation().Key, contracts.RecordGetExpiredOperation().Key,
		contracts.RecordPutOperation().Key, contracts.RecordDeleteOperation().Key,
		contracts.RecordBatchOperation().Key, contracts.RecordListOperation().Key,
		contracts.RecordListExpiredOperation().Key,
	)
	if err != nil {
		t.Fatal(err)
	}
	var resolved string
	if err := RegisterRecordDataStore(dispatcher, func(_ context.Context, moduleID string) (contracts.RecordDataStore, error) {
		resolved = moduleID
		return recordStoreForBusTest{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	client, err := contracts.NewRecordDataStore(NewModuleDispatcher(dispatcher, "worker.media"), contracts.RecordLimits{
		MaxPageSize: 10, MaxBatchOperations: 10, MaxPayloadBytes: 1024, MaxIndexBytes: 1024,
		MaxBatchBytes: 4096, MaxPageBytes: 4096, MaxSelectors: 8, MaxSelectorValues: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), contracts.RecordKey{Namespace: "media/jobs", ScopeType: "global", ScopeID: "global", RecordKey: "job/1"}); err != nil {
		t.Fatal(err)
	}
	if resolved != "worker.media" {
		t.Fatalf("resolved module = %q, want worker.media", resolved)
	}
}

type recordStoreForBusTest struct{}

func (recordStoreForBusTest) Get(context.Context, contracts.RecordKey) (contracts.Record, error) {
	return contracts.Record{}, nil
}
func (recordStoreForBusTest) GetExpired(context.Context, contracts.RecordKey) (contracts.Record, error) {
	return contracts.Record{}, nil
}
func (recordStoreForBusTest) Put(context.Context, contracts.RecordKey, contracts.RecordValue, int64) (contracts.Record, error) {
	return contracts.Record{}, nil
}
func (recordStoreForBusTest) Delete(context.Context, contracts.RecordKey, int64) error { return nil }
func (recordStoreForBusTest) Batch(context.Context, []contracts.RecordMutation) error  { return nil }
func (recordStoreForBusTest) List(context.Context, string, string, string, string, []contracts.RecordSelector, int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	return nil, nil, "", nil
}
func (recordStoreForBusTest) ListExpired(context.Context, string, string, string, string, int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	return nil, nil, "", nil
}
func (recordStoreForBusTest) MaxPageSize() int     { return 10 }
func (recordStoreForBusTest) MaxBatchSize() int    { return 10 }
func (recordStoreForBusTest) MaxPayloadBytes() int { return 1024 }
func (recordStoreForBusTest) MaxIndexBytes() int   { return 1024 }
func (recordStoreForBusTest) MaxBatchBytes() int   { return 4096 }
