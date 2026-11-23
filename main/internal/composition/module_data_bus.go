package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var errModuleDataScopeUnavailable = errors.New("module data scope is unavailable")

// registerModuleData binds the neutral record descriptor to Core storage in
// the composition root and returns only a scope-bound DataBus client.
func (component *coreComponent) registerModuleData(descriptor contracts.RecordDescriptor) (contracts.RecordDataStore, error) {
	if component == nil || component.runtime == nil || component.dispatcher == nil || descriptor.OwnerID == "" {
		return nil, errModuleDataScopeUnavailable
	}
	coreDescriptor := platform.ModuleDataDescriptor{ModuleID: descriptor.OwnerID, Namespaces: make([]platform.ModuleDataNamespace, 0, len(descriptor.Namespaces))}
	for _, namespace := range descriptor.Namespaces {
		converted := platform.ModuleDataNamespace{Name: namespace.Name, ScopeTypes: append([]string(nil), namespace.ScopeTypes...), Fields: make(map[string]platform.SelectorField, len(namespace.Fields))}
		for field, rule := range namespace.Fields {
			converted.Fields[field] = platform.SelectorField{Type: platform.SelectorType(rule.Type), MaxValues: rule.MaxValues}
		}
		coreDescriptor.Namespaces = append(coreDescriptor.Namespaces, converted)
	}
	store, err := component.runtime.RegisterDataModule(coreDescriptor)
	if err != nil {
		return nil, err
	}
	component.recordStoreMu.Lock()
	if component.recordStores == nil {
		component.recordStores = make(map[string]contracts.RecordDataStore)
	}
	if _, exists := component.recordStores[descriptor.OwnerID]; exists {
		component.recordStoreMu.Unlock()
		return nil, errModuleDataScopeUnavailable
	}
	component.recordStores[descriptor.OwnerID] = coreRecordDataStore{store: store}
	component.recordStoreMu.Unlock()

	settings := component.config.Core.ModuleData
	return contracts.NewRecordDataStore(bus.NewModuleDispatcher(component.dispatcher, descriptor.OwnerID), contracts.RecordLimits{
		MaxPayloadBytes: settings.MaxPayloadBytes, MaxIndexBytes: settings.MaxIndexBytes, MaxBatchBytes: settings.MaxBatchBytes,
		MaxPageBytes: settings.MaxPageBytes, MaxSelectors: settings.MaxSelectors, MaxSelectorValues: settings.MaxSelectorValues,
		MaxPageSize: settings.MaxPageSize, MaxBatchOperations: settings.MaxBatchOperations,
	})
}

func hasModuleDataConsumers(selected map[string]struct{}) bool {
	for _, moduleID := range []string{"engine.loader", "engine.website", "engine.content", "engine.forms", "engine.router", "worker.archive", "worker.media"} {
		if _, ok := selected[moduleID]; ok {
			return true
		}
	}
	return false
}

func (component *coreComponent) recordDataStore(_ context.Context, moduleID string) (contracts.RecordDataStore, error) {
	if component == nil || moduleID == "" {
		return nil, errModuleDataScopeUnavailable
	}
	component.recordStoreMu.RLock()
	store := component.recordStores[moduleID]
	component.recordStoreMu.RUnlock()
	if store == nil {
		return nil, errModuleDataScopeUnavailable
	}
	return store, nil
}

// coreRecordDataStore adapts the private Core capability to the neutral
// DataBus DTOs. It is retained only by main's Core operation handler.
type coreRecordDataStore struct{ store *platform.ModuleDataStore }

func (adapter coreRecordDataStore) Get(ctx context.Context, key contracts.RecordKey) (contracts.Record, error) {
	value, err := adapter.store.Get(ctx, coreRecordKey(key))
	return contractRecord(value), mapCoreRecordError(err)
}

func (adapter coreRecordDataStore) GetExpired(ctx context.Context, key contracts.RecordKey) (contracts.Record, error) {
	value, err := adapter.store.GetExpired(ctx, coreRecordKey(key))
	return contractRecord(value), mapCoreRecordError(err)
}

func (adapter coreRecordDataStore) Put(ctx context.Context, key contracts.RecordKey, value contracts.RecordValue, revision int64) (contracts.Record, error) {
	result, err := adapter.store.Put(ctx, coreRecordKey(key), coreRecordValue(value), revision)
	return contractRecord(result), mapCoreRecordError(err)
}

func (adapter coreRecordDataStore) Delete(ctx context.Context, key contracts.RecordKey, revision int64) error {
	return mapCoreRecordError(adapter.store.Delete(ctx, coreRecordKey(key), revision))
}

func (adapter coreRecordDataStore) Batch(ctx context.Context, mutations []contracts.RecordMutation) error {
	converted := make([]platform.ModuleDataMutation, len(mutations))
	for i, mutation := range mutations {
		converted[i] = platform.ModuleDataMutation{Key: coreRecordKey(mutation.Key), ExpectedRevision: mutation.ExpectedRevision}
		if mutation.Value != nil {
			value := coreRecordValue(*mutation.Value)
			converted[i].Value = &value
		}
	}
	return mapCoreRecordError(adapter.store.Batch(ctx, converted))
}

func (adapter coreRecordDataStore) List(ctx context.Context, namespace, scopeType, scopeID, after string, selectors []contracts.RecordSelector, limit int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	converted := make([]platform.ModuleDataSelector, len(selectors))
	for i, selector := range selectors {
		converted[i] = platform.ModuleDataSelector{Field: selector.Field, Operator: platform.SelectorOperator(selector.Operator), Values: selector.Values}
	}
	keys, values, cursor, err := adapter.store.List(ctx, namespace, scopeType, scopeID, after, converted, limit)
	return contractRecordKeys(keys), contractRecords(values), cursor, mapCoreRecordError(err)
}

func (adapter coreRecordDataStore) ListExpired(ctx context.Context, namespace, scopeType, scopeID, after string, limit int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	keys, values, cursor, err := adapter.store.ListExpired(ctx, namespace, scopeType, scopeID, after, limit)
	return contractRecordKeys(keys), contractRecords(values), cursor, mapCoreRecordError(err)
}

func mapCoreRecordError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platform.ErrModuleDataNotFound):
		return contracts.ErrRecordNotFound
	case errors.Is(err, platform.ErrModuleDataConflict):
		return contracts.ErrRecordConflict
	case errors.Is(err, platform.ErrModuleDataInvalid):
		return contracts.ErrRecordInvalid
	case errors.Is(err, platform.ErrModuleDataLimitExceeded):
		return contracts.ErrRecordLimit
	case errors.Is(err, platform.ErrModuleDataUnavailable):
		return contracts.ErrRecordUnavailable
	default:
		return err
	}
}

func (adapter coreRecordDataStore) MaxPageSize() int     { return adapter.store.MaxPageSize() }
func (adapter coreRecordDataStore) MaxBatchSize() int    { return adapter.store.MaxBatchSize() }
func (adapter coreRecordDataStore) MaxPayloadBytes() int { return adapter.store.MaxPayloadBytes() }
func (adapter coreRecordDataStore) MaxIndexBytes() int   { return adapter.store.MaxIndexBytes() }
func (adapter coreRecordDataStore) MaxBatchBytes() int   { return adapter.store.MaxBatchBytes() }

func coreRecordKey(key contracts.RecordKey) platform.ModuleDataKey {
	return platform.ModuleDataKey{Namespace: key.Namespace, ScopeType: key.ScopeType, ScopeID: key.ScopeID, RecordKey: key.RecordKey}
}

func coreRecordValue(value contracts.RecordValue) platform.ModuleDataValue {
	return platform.ModuleDataValue{Payload: value.Payload, PayloadVersion: value.PayloadVersion, IndexData: value.IndexData, TTL: value.TTL}
}

func contractRecordKeys(keys []platform.ModuleDataKey) []contracts.RecordKey {
	result := make([]contracts.RecordKey, len(keys))
	for i, key := range keys {
		result[i] = contracts.RecordKey{Namespace: key.Namespace, ScopeType: key.ScopeType, ScopeID: key.ScopeID, RecordKey: key.RecordKey}
	}
	return result
}

func contractRecords(values []platform.ModuleDataRecord) []contracts.Record {
	result := make([]contracts.Record, len(values))
	for i, value := range values {
		result[i] = contractRecord(value)
	}
	return result
}

func contractRecord(value platform.ModuleDataRecord) contracts.Record {
	return contracts.Record{
		Payload: value.Payload, PayloadVersion: value.PayloadVersion, IndexData: value.IndexData,
		Revision: value.Revision, ExpiresAt: value.ExpiresAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

var _ contracts.RecordDataStore = coreRecordDataStore{}
