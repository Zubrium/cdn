package presentation

import (
	"context"
	"testing"
	"time"

	appcache "github.com/Zubrium/cdn/core/application/cache"
	"github.com/Zubrium/cdn/core/domain/access"
)

type wiringEpoch struct{}

func (wiringEpoch) AccessState(context.Context, string, string) (int64, int64, error) {
	return 1, 1, nil
}

func (wiringEpoch) Revision(context.Context, string) (int64, error) { return 1, nil }

type wiringObserver struct{}

func (wiringObserver) IncCounter(context.Context, string, float64, ...appcache.Label) {
}

func (wiringObserver) SetGauge(context.Context, string, float64, ...appcache.Label) {}

func (wiringObserver) ObserveHistogram(context.Context, string, float64, ...appcache.Label) {
}

func TestCacheStoreWiringInjectsDeployedObserver(t *testing.T) {
	observer := wiringObserver{}
	epoch := wiringEpoch{}
	cfg := RuntimeConfig{
		CacheEnabled:  true,
		CacheObserver: observer,
		CacheStore: appcache.StoreConfig{
			BytesMaxBytes: 1 << 20, MetadataMaxBytes: 1 << 20, MaxObjectBytes: 1 << 16,
			BytesTTL: time.Minute, MetadataTTL: time.Minute,
			Shards: 4, PromotionInterval: 1,
			CoalesceEnabled: true, CoalesceMaxWaiters: 8, CoalesceTimeout: time.Second,
			AccessMaxEntries: 64, AccessMaxBytes: 1 << 16, AccessTTL: time.Minute,
			InvalidationBatchSize: 8, InvalidationBatchWindow: 10 * time.Millisecond,
		},
	}
	storeConfig := newCacheStoreConfig(cfg, epoch)
	if storeConfig.Observer == nil {
		t.Fatal("runtime observer was not injected into the production cache store")
	}
	if storeConfig.Epoch == nil {
		t.Fatal("runtime epoch source was not injected into the production cache store")
	}
	if storeConfig.CacheFactory == nil {
		t.Fatal("runtime cache factory was not injected into the production cache store")
	}
	if err := storeConfig.Validate(); err != nil {
		t.Fatalf("wired store configuration is invalid: %v", err)
	}
	store, err := appcache.NewStore(storeConfig)
	if err != nil {
		t.Fatalf("wired store cannot be constructed: %v", err)
	}
	if store == nil {
		t.Fatal("wired store is nil")
	}

	missing := cfg
	missing.CacheObserver = nil
	if err := newCacheStoreConfig(missing, epoch).Validate(); err == nil {
		t.Fatal("missing runtime observer was silently accepted")
	}
}

func validCacheEnabledRuntimeConfig(observer appcache.Observer) RuntimeConfig {
	return RuntimeConfig{
		Database: DatabaseConfig{
			URL:            "postgres://runtime:secret@127.0.0.1:5432/zubrium_core_qa_unit",
			MaxConnections: 32,
		},
		MigrationDatabaseURL: "postgres://migrator:secret@127.0.0.1:5432/zubrium_core_qa_unit",
		AtlasBinary:          "atlas",
		MigrationLockTimeout: time.Second,
		AccessGroups:         map[string]access.Level{"content": access.LevelWrite},
		CacheMaxBytes:        1 << 20,
		CacheTTL:             time.Minute,
		CacheEnabled:         true,
		CacheObserver:        observer,
		CacheStore: appcache.StoreConfig{
			BytesMaxBytes: 1 << 20, MetadataMaxBytes: 1 << 20, MaxObjectBytes: 1 << 16,
			BytesTTL: time.Minute, MetadataTTL: time.Minute,
			Shards: 4, PromotionInterval: 1,
			CoalesceEnabled: true, CoalesceMaxWaiters: 8, CoalesceTimeout: time.Second,
			AccessMaxEntries: 64, AccessMaxBytes: 1 << 16, AccessTTL: time.Minute,
			InvalidationBatchSize: 8, InvalidationBatchWindow: 10 * time.Millisecond,
		},
		Warmup: appcache.Config{
			Enabled: true, Mode: appcache.ModeStrict,
			Timeout: 30 * time.Second, PerKeyTimeout: 5 * time.Second,
			Concurrency: 2, MaxKeys: 10,
			Retry:   appcache.Retry{Attempts: 1, Backoff: 10 * time.Millisecond},
			Sources: appcache.Sources{PublishedArtifacts: true},
			Targets: appcache.Targets{Metadata: true},
		},
		MaxMetadataBytes:     1 << 16,
		MaxObjectBytes:       1 << 20,
		MaxCachedObjectBytes: 512,
		MaintenanceInterval:  time.Minute,
		MaintenanceTimeout:   time.Second,
		PendingTTL:           time.Hour,
		MaintenanceBatchSize: 10,
		SessionIdleTTL:       8 * time.Hour,
		SessionAbsoluteTTL:   24 * time.Hour,
		ModuleData: ModuleDataLimits{
			MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20,
			MaxPageBytes: 4 << 20, MaxSelectors: 16, MaxSelectorValues: 64,
			MaxPageSize: 100, MaxBatchOperations: 64,
		},
	}
}

func TestValidateRuntimeConfigDefersInternalCacheDependencies(t *testing.T) {
	cfg := validCacheEnabledRuntimeConfig(wiringObserver{})
	if err := validateRuntimeConfig(cfg); err != nil {
		t.Fatalf("operator configuration with internally injected dependencies was rejected: %v", err)
	}
	cfg.CacheObserver = nil
	if err := validateRuntimeConfig(cfg); err == nil {
		t.Fatal("runtime configuration without a cache observer was accepted")
	}
}

func TestLegacyByteCacheIsNotATypedNilWhenStoreOwnsBytes(t *testing.T) {
	enabled := validCacheEnabledRuntimeConfig(wiringObserver{})
	enabled.CacheShards = 4
	enabled.CachePromotionInterval = 1
	enabled.CoalesceMaxWaiters = 8
	enabled.CoalesceTimeout = time.Second
	if cache := legacyByteCache(enabled); cache != nil {
		t.Fatal("legacy byte cache was passed to storage while the bounded store owns bytes")
	}

	disabled := enabled
	disabled.CacheEnabled = false
	if cache := legacyByteCache(disabled); cache == nil {
		t.Fatal("legacy byte cache was omitted when the bounded store is disabled")
	}
}
