package platform

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/access"
)

func TestIdentityFacadeRejectsUninitializedRuntime(t *testing.T) {
	var r *Runtime
	ctx := context.Background()
	actor := Session{}
	if _, err := r.Authenticate(ctx, "secret"); err == nil {
		t.Fatal("authenticate accepted nil runtime")
	}
	if _, _, err := r.CreatePrincipal(ctx, actor, "test", nil); err == nil {
		t.Fatal("create accepted nil runtime")
	}
	if err := r.Close(); err == nil {
		t.Fatal("close accepted nil runtime")
	}
	if _, err := parseID("invalid"); err == nil {
		t.Fatal("invalid id accepted")
	}
}

func TestSessionFormattingDoesNotExposeCredential(t *testing.T) {
	session := Session{hash: []byte("private credential")}
	for _, value := range []string{fmt.Sprintf("%v", session), fmt.Sprintf("%#v", session), fmt.Sprintf("%+v", session)} {
		if strings.Contains(value, "private") {
			t.Fatalf("session exposed: %q", value)
		}
	}
}

func TestMaintenanceStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{done: make(chan struct{}), errors: make(chan error, 1)}
	cancel()
	go r.maintain(ctx, RuntimeConfig{MaintenanceInterval: time.Hour})
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop")
	}
	if r.Errors() == nil {
		t.Fatal("missing maintenance error channel")
	}
}

func TestRuntimeRejectsCacheEntryLargerThanTotalCacheBudget(t *testing.T) {
	cfg := RuntimeConfig{
		Database:             DatabaseConfig{URL: "postgres://runtime:secret@127.0.0.1:5432/zubrium_core_qa_unit", MaxConnections: 3},
		MigrationDatabaseURL: "postgres://migrator:secret@127.0.0.1:5432/zubrium_core_qa_unit",
		AtlasBinary:          "atlas", MigrationLockTimeout: time.Second,
		AccessGroups:  map[string]access.Level{"content": access.LevelWrite},
		CacheMaxBytes: 1 << 20, CacheTTL: time.Minute, MaxMetadataBytes: 1 << 16,
		MaxObjectBytes: (1 << 20) + 2, MaxCachedObjectBytes: (1 << 20) + 1,
		MaintenanceInterval: time.Minute, MaintenanceTimeout: time.Second,
		PendingTTL: time.Hour, MaintenanceBatchSize: 10,
	}
	if err := validateRuntimeConfig(cfg); err == nil {
		t.Fatal("cache entry larger than the total cache memory budget accepted")
	}
}

func TestRuntimeRejectsPoolTooSmallForStagedStreaming(t *testing.T) {
	cfg := RuntimeConfig{
		Database:             DatabaseConfig{URL: "postgres://runtime:secret@127.0.0.1:5432/zubrium_core_qa_unit", MaxConnections: 2},
		MigrationDatabaseURL: "postgres://migrator:secret@127.0.0.1:5432/zubrium_core_qa_unit",
		AtlasBinary:          "atlas", MigrationLockTimeout: time.Second,
		AccessGroups:  map[string]access.Level{"content": access.LevelWrite},
		CacheMaxBytes: 1 << 20, CacheTTL: time.Minute, MaxMetadataBytes: 1 << 16,
		MaxObjectBytes: 1 << 20, MaxCachedObjectBytes: 512,
		MaintenanceInterval: time.Minute, MaintenanceTimeout: time.Second,
		PendingTTL: time.Hour, MaintenanceBatchSize: 10,
		ModuleData: ModuleDataLimits{MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20, MaxPageBytes: 4 << 20,
			MaxSelectors: 16, MaxSelectorValues: 64, MaxPageSize: 100, MaxBatchOperations: 64},
	}
	if err := validateRuntimeConfig(cfg); err == nil {
		t.Fatal("runtime pool without room for staged reads was accepted")
	}
}

func TestRuntimeAndMigrationURLsMustTargetTheSamePostgresServer(t *testing.T) {
	if err := ValidateDatabaseURLs(
		"postgres://runtime:secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"postgres://migrator:secret@127.0.0.2:5432/zubrium_core_qa_unit",
	); err == nil {
		t.Fatal("runtime and migration users targeting different PostgreSQL hosts were accepted")
	}
	if err := ValidateDatabaseURLs(
		"postgres://runtime:secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"postgres://migrator:secret@127.0.0.1:5433/zubrium_core_qa_unit",
	); err == nil {
		t.Fatal("runtime and migration users targeting different PostgreSQL ports were accepted")
	}
}
