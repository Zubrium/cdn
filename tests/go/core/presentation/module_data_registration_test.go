package presentation

import (
	"testing"

	"github.com/Zubrium/cdn/core/domain/engine_data"
	"github.com/jackc/pgx/v5/pgxpool"
)

type testEngineDataRepository struct{ engine_data.Repository }

func TestRegisterModuleDataRequiresRegisteredEngineAndIssuesOnce(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20, MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	runtime := &Runtime{
		database:             &pgxpool.Pool{},
		Registry:             NewRegistry(),
		moduleDataLimits:     limits,
		engineDataRepository: testEngineDataRepository{},
	}
	descriptor := ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{{Name: "records", ScopeTypes: []string{"global"}}}}
	if _, err := runtime.RegisterDataModule(descriptor); err == nil {
		t.Fatal("Core issued data capability to an unregistered module")
	}
	if err := runtime.Registry.Register("engine.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RegisterDataModule(descriptor); err != nil {
		t.Fatalf("registered engine could not receive its data capability: %v", err)
	}
	if _, err := runtime.RegisterDataModule(descriptor); err == nil {
		t.Fatal("Core issued the same module data capability twice")
	}
}
