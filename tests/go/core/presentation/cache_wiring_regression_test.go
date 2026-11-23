package presentation

import (
	"context"
	"testing"
	"time"

	appcache "github.com/Zubrium/cdn/core/application/cache"
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
