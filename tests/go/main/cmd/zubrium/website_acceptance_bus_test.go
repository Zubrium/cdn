//go:build website_acceptance

package main

import (
	"context"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/main/internal/composition"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func websiteCoreRecordDescriptor(descriptor contracts.RecordDescriptor) core.ModuleDataDescriptor {
	result := core.ModuleDataDescriptor{ModuleID: descriptor.OwnerID, Namespaces: make([]core.ModuleDataNamespace, 0, len(descriptor.Namespaces))}
	for _, namespace := range descriptor.Namespaces {
		converted := core.ModuleDataNamespace{Name: namespace.Name, ScopeTypes: append([]string(nil), namespace.ScopeTypes...), Fields: make(map[string]core.SelectorField, len(namespace.Fields))}
		for name, field := range namespace.Fields {
			converted.Fields[name] = core.SelectorField{Type: core.SelectorType(field.Type), MaxValues: field.MaxValues}
		}
		result.Namespaces = append(result.Namespaces, converted)
	}
	return result
}

func websiteAcceptanceRecordStore(t *testing.T, runtime *core.Runtime, descriptor contracts.RecordDescriptor) (contracts.RecordDataStore, error) {
	t.Helper()
	coreStore, err := runtime.RegisterDataModule(websiteCoreRecordDescriptor(descriptor))
	if err != nil {
		return nil, err
	}
	keys := []contracts.Key{
		contracts.RecordGetOperation().Key,
		contracts.RecordGetExpiredOperation().Key,
		contracts.RecordPutOperation().Key,
		contracts.RecordDeleteOperation().Key,
		contracts.RecordBatchOperation().Key,
		contracts.RecordListOperation().Key,
		contracts.RecordListExpiredOperation().Key,
	}
	dispatcher, err := bus.NewAllowlisted(keys...)
	if err != nil {
		return nil, err
	}
	adapter := websiteAcceptanceCoreRecordStore{store: coreStore}
	if err := bus.RegisterRecordDataStore(dispatcher, func(_ context.Context, owner string) (contracts.RecordDataStore, error) {
		if owner != descriptor.OwnerID {
			return nil, bus.ErrInvalidModuleIdentity
		}
		return adapter, nil
	}); err != nil {
		return nil, err
	}
	if err := dispatcher.Seal(); err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := dispatcher.Close(ctx); err != nil {
			t.Errorf("close scoped record DataBus: %v", err)
		}
	})
	limits := contracts.RecordLimits{
		MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20,
		MaxPageBytes: 3 << 20, MaxSelectors: 16, MaxSelectorValues: 64,
		MaxPageSize: 100, MaxBatchOperations: 64,
	}
	client, err := contracts.NewRecordDataStore(bus.NewModuleDispatcher(dispatcher, descriptor.OwnerID), limits)
	if err != nil {
		return nil, err
	}
	return client, nil
}

type websiteAcceptanceCoreRecordStore struct{ store *core.ModuleDataStore }

func (adapter websiteAcceptanceCoreRecordStore) Get(ctx context.Context, key contracts.RecordKey) (contracts.Record, error) {
	record, err := adapter.store.Get(ctx, websiteCoreRecordKey(key))
	return websiteCoreRecord(record), err
}

func (adapter websiteAcceptanceCoreRecordStore) GetExpired(ctx context.Context, key contracts.RecordKey) (contracts.Record, error) {
	record, err := adapter.store.GetExpired(ctx, websiteCoreRecordKey(key))
	return websiteCoreRecord(record), err
}

func (adapter websiteAcceptanceCoreRecordStore) Put(ctx context.Context, key contracts.RecordKey, value contracts.RecordValue, revision int64) (contracts.Record, error) {
	record, err := adapter.store.Put(ctx, websiteCoreRecordKey(key), websiteCoreRecordValue(value), revision)
	return websiteCoreRecord(record), err
}

func (adapter websiteAcceptanceCoreRecordStore) Delete(ctx context.Context, key contracts.RecordKey, revision int64) error {
	return adapter.store.Delete(ctx, websiteCoreRecordKey(key), revision)
}

func (adapter websiteAcceptanceCoreRecordStore) Batch(ctx context.Context, mutations []contracts.RecordMutation) error {
	converted := make([]core.ModuleDataMutation, len(mutations))
	for index, mutation := range mutations {
		converted[index] = core.ModuleDataMutation{Key: websiteCoreRecordKey(mutation.Key), ExpectedRevision: mutation.ExpectedRevision}
		if mutation.Value != nil {
			value := websiteCoreRecordValue(*mutation.Value)
			converted[index].Value = &value
		}
	}
	return adapter.store.Batch(ctx, converted)
}

func (adapter websiteAcceptanceCoreRecordStore) List(ctx context.Context, namespace, scopeType, scopeID, after string, selectors []contracts.RecordSelector, limit int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	converted := make([]core.ModuleDataSelector, len(selectors))
	for index, selector := range selectors {
		converted[index] = core.ModuleDataSelector{Field: selector.Field, Operator: core.SelectorOperator(selector.Operator), Values: selector.Values}
	}
	keys, records, cursor, err := adapter.store.List(ctx, namespace, scopeType, scopeID, after, converted, limit)
	return websiteContractRecordKeys(keys), websiteContractRecords(records), cursor, err
}

func (adapter websiteAcceptanceCoreRecordStore) ListExpired(ctx context.Context, namespace, scopeType, scopeID, after string, limit int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	keys, records, cursor, err := adapter.store.ListExpired(ctx, namespace, scopeType, scopeID, after, limit)
	return websiteContractRecordKeys(keys), websiteContractRecords(records), cursor, err
}

func (adapter websiteAcceptanceCoreRecordStore) MaxPageSize() int { return adapter.store.MaxPageSize() }
func (adapter websiteAcceptanceCoreRecordStore) MaxBatchSize() int {
	return adapter.store.MaxBatchSize()
}
func (adapter websiteAcceptanceCoreRecordStore) MaxPayloadBytes() int {
	return adapter.store.MaxPayloadBytes()
}
func (adapter websiteAcceptanceCoreRecordStore) MaxIndexBytes() int {
	return adapter.store.MaxIndexBytes()
}
func (adapter websiteAcceptanceCoreRecordStore) MaxBatchBytes() int {
	return adapter.store.MaxBatchBytes()
}

func websiteCoreRecordKey(key contracts.RecordKey) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: key.Namespace, ScopeType: key.ScopeType, ScopeID: key.ScopeID, RecordKey: key.RecordKey}
}

func websiteCoreRecordValue(value contracts.RecordValue) core.ModuleDataValue {
	return core.ModuleDataValue{Payload: value.Payload, PayloadVersion: value.PayloadVersion, IndexData: value.IndexData, TTL: value.TTL}
}

func websiteCoreRecord(record core.ModuleDataRecord) contracts.Record {
	return contracts.Record{Payload: record.Payload, PayloadVersion: record.PayloadVersion, IndexData: record.IndexData, Revision: record.Revision,
		ExpiresAt: record.ExpiresAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func websiteContractRecordKeys(keys []core.ModuleDataKey) []contracts.RecordKey {
	result := make([]contracts.RecordKey, len(keys))
	for index, key := range keys {
		result[index] = contracts.RecordKey{Namespace: key.Namespace, ScopeType: key.ScopeType, ScopeID: key.ScopeID, RecordKey: key.RecordKey}
	}
	return result
}

func websiteContractRecords(records []core.ModuleDataRecord) []contracts.Record {
	result := make([]contracts.Record, len(records))
	for index, record := range records {
		result[index] = websiteCoreRecord(record)
	}
	return result
}

func websiteAcceptanceExecutor(t *testing.T, bindings map[contracts.Key]composition.TaskOperationBinding) *bus.Dispatcher {
	t.Helper()
	keys := make([]contracts.Key, 0, len(bindings))
	handlers := make(map[contracts.Key]bus.TaskOperationHandler, len(bindings))
	for key, binding := range bindings {
		keys = append(keys, key)
		handlers[key] = binding.Handler
	}
	dispatcher, err := bus.NewAllowlisted(keys...)
	if err != nil {
		t.Fatalf("build disposable DataBus allowlist: %v", err)
	}
	if err := bus.RegisterTaskOperations(dispatcher, handlers); err != nil {
		t.Fatalf("register disposable DataBus task operations: %v", err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatalf("seal disposable DataBus: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := dispatcher.Close(ctx); err != nil {
			t.Errorf("close disposable DataBus: %v", err)
		}
	})
	return dispatcher
}
