package cache

import (
	"context"
	"time"
)

const (
	cacheBytes         = "core.bytes"
	cacheMetadata      = "core.metadata"
	cacheAuthorization = "core.authorization"

	metricOperations      = "zubrium_cache_operations_total"
	metricEntries         = "zubrium_cache_entries"
	metricBytes           = "zubrium_cache_bytes"
	metricWarmupKeys      = "zubrium_cache_warmup_keys_total"
	metricWarmupDuration  = "zubrium_cache_warmup_duration_seconds"
	metricInvalidationLag = "zubrium_cache_invalidation_lag_seconds"
	metricCoalesced       = "zubrium_cache_coalesced_total"
	metricInvalidation    = "zubrium_cache_invalidation_batch_total"
)

var metricCaches = []string{cacheBytes, cacheMetadata, cacheAuthorization}

func cacheLabel(name string) Label {
	return StringLabel("cache", name)
}

func resultLabel(name string) Label {
	return StringLabel("result", name)
}

func tierLabel(tier Tier) Label {
	return StringLabel("tier", string(tier))
}

func (s *Store) initMetrics() {
	ctx := context.Background()
	for _, name := range metricCaches {
		s.observer.SetGauge(ctx, metricEntries, 0, cacheLabel(name))
		s.observer.SetGauge(ctx, metricBytes, 0, cacheLabel(name))
		s.observer.SetGauge(ctx, metricInvalidationLag, 0, cacheLabel(name))
		for _, result := range []string{"HIT", "MISS", "INVALIDATED", "EVICTION"} {
			s.observer.IncCounter(ctx, metricOperations, 0, cacheLabel(name), resultLabel(result))
		}
		for _, result := range []string{"LEADER", "JOINED", "FAILED"} {
			s.observer.IncCounter(ctx, metricCoalesced, 0, cacheLabel(name), resultLabel(result))
		}
		for _, tier := range []string{string(TierRequired), string(TierOptional)} {
			for _, result := range []string{"WARMED", "FAILED", "SKIPPED"} {
				s.observer.IncCounter(ctx, metricWarmupKeys, 0, cacheLabel(name), tierLabel(Tier(tier)), resultLabel(result))
			}
		}
	}
	for _, result := range []string{"APPLIED", "OVERFLOW", "CLEARED"} {
		s.observer.IncCounter(ctx, metricInvalidation, 0, resultLabel(result))
	}
}

func (s *Store) operation(cacheName, result string) {
	s.observer.IncCounter(context.Background(), metricOperations, 1, cacheLabel(cacheName), resultLabel(result))
}

func (s *Store) coalescing(cacheName string, leader, joined, failed int64) {
	ctx := context.Background()
	if leader > 0 {
		s.observer.IncCounter(ctx, metricCoalesced, float64(leader), cacheLabel(cacheName), resultLabel("LEADER"))
	}
	if joined > 0 {
		s.observer.IncCounter(ctx, metricCoalesced, float64(joined), cacheLabel(cacheName), resultLabel("JOINED"))
	}
	if failed > 0 {
		s.observer.IncCounter(ctx, metricCoalesced, float64(failed), cacheLabel(cacheName), resultLabel("FAILED"))
	}
}

func (s *Store) invalidationBatch(result string) {
	s.observer.IncCounter(context.Background(), metricInvalidation, 1, resultLabel(result))
}

func (s *Store) refreshGauges() {
	stats := s.Stats()
	ctx := context.Background()
	s.observer.SetGauge(ctx, metricEntries, float64(stats.BytesEntries), cacheLabel(cacheBytes))
	s.observer.SetGauge(ctx, metricBytes, float64(stats.BytesSize), cacheLabel(cacheBytes))
	s.observer.SetGauge(ctx, metricEntries, float64(stats.MetadataEntries), cacheLabel(cacheMetadata))
	s.observer.SetGauge(ctx, metricBytes, float64(stats.MetadataSize), cacheLabel(cacheMetadata))
	s.observer.SetGauge(ctx, metricEntries, float64(stats.AuthorizationEntries), cacheLabel(cacheAuthorization))
	s.observer.SetGauge(ctx, metricBytes, float64(stats.AuthorizationSize), cacheLabel(cacheAuthorization))
}

func (r *Runner) warmupKey(tier Tier, cacheName, result string) {
	r.deps.Observer.IncCounter(context.Background(), metricWarmupKeys, 1, cacheLabel(cacheName), tierLabel(tier), resultLabel(result))
}

func (r *Runner) warmupDuration(cacheName string, elapsed time.Duration) {
	r.deps.Observer.ObserveHistogram(context.Background(), metricWarmupDuration, elapsed.Seconds(), cacheLabel(cacheName))
}
