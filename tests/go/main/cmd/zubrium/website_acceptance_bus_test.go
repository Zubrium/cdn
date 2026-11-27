//go:build website_acceptance || website_orchestration_acceptance || publication_delivery_acceptance

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

func websiteAcceptancePools(t *testing.T, dispatcher *bus.Dispatcher, bindings map[contracts.Key]composition.TaskOperationBinding, specs ...composition.TaskPoolSpec) []core.TaskPoolConfig {
	t.Helper()
	pools, err := composition.NewDataBusTaskPools(dispatcher, bindings, specs)
	if err != nil {
		t.Fatalf("build isolated DataBus worker pools: %v", err)
	}
	return pools
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
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 32}, keys...)
	if err != nil {
		return nil, err
	}
	adapter := composition.NewCoreRecordDataStore(coreStore)
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
	client, err := contracts.NewRecordDataStore(bus.NewRecordOwnerDispatcher(dispatcher, descriptor.OwnerID), limits)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func websiteAcceptanceExecutor(t *testing.T, bindings map[contracts.Key]composition.TaskOperationBinding) *bus.Dispatcher {
	t.Helper()
	keys := make([]contracts.Key, 0, len(bindings))
	handlers := make(map[contracts.Key]bus.TaskOperationHandler, len(bindings))
	for key, binding := range bindings {
		keys = append(keys, key)
		handlers[key] = binding.Handler
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 32}, keys...)
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
