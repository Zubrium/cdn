package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	cacheRouterManifests = "router.manifests"
	cacheRouterRoutes    = "router.routes"

	metricCacheOperations      = "zubrium_cache_operations_total"
	metricCacheEntries         = "zubrium_cache_entries"
	metricCacheBytes           = "zubrium_cache_bytes"
	metricCacheInvalidationLag = "zubrium_cache_invalidation_lag_seconds"
	metricCacheWarmupKeys      = "zubrium_cache_warmup_keys_total"
	metricCacheWarmupDuration  = "zubrium_cache_warmup_duration_seconds"
)

// CacheTier mirrors the Core warm-up tier vocabulary without importing Core
// application packages into Router.
type CacheTier string

const (
	CacheTierRequired CacheTier = "REQUIRED"
	CacheTierOptional CacheTier = "OPTIONAL"
)

// CacheLabel is one fixed, low-cardinality observability label.
type CacheLabel struct {
	Key   string
	Value string
}

// StringCacheLabel builds a string-valued cache metric label.
func StringCacheLabel(key, value string) CacheLabel { return CacheLabel{Key: key, Value: value} }

// CacheObserver is the narrow metrics sink used by Router warm-up. Identity
// values never enter these exact series.
type CacheObserver interface {
	IncCounterExact(context.Context, string, float64, ...CacheLabel)
	SetGaugeExact(context.Context, string, float64, ...CacheLabel)
	ObserveHistogramExact(context.Context, string, float64, ...CacheLabel)
}

// RouterWarmupTarget binds one server-owned artifact slot to a configured
// Router route. A visitor cannot supply any of these values.
type RouterWarmupTarget struct {
	RouteID string
	Locale  string
	FileID  string
	Tier    CacheTier
	Paths   []string
}

// WarmupResult reports the bounded work performed by one Router warm-up cycle.
type WarmupResult struct {
	Artifacts int
	Routes    int
	Failed    int
	Skipped   int
}

// RouterWarmupConfig selects the Router-owned targets enabled by startup YAML
// and carries the same explicit time bounds as the Core warm-up pipeline.
type RouterWarmupConfig struct {
	Manifests       bool
	Routes          bool
	Timeout         time.Duration
	PerKeyTimeout   time.Duration
	RefreshInterval time.Duration
}

// initCacheMetrics publishes every fixed low-cardinality series with a zero
// value. Disabled targets therefore stay observable without exposing identity.
func (delivery *CoreDelivery) initCacheMetrics() {
	ctx := context.Background()
	for _, cache := range []string{cacheRouterManifests, cacheRouterRoutes} {
		label := StringCacheLabel("cache", cache)
		delivery.observer.SetGaugeExact(ctx, metricCacheEntries, 0, label)
		delivery.observer.SetGaugeExact(ctx, metricCacheBytes, 0, label)
		delivery.observer.SetGaugeExact(ctx, metricCacheInvalidationLag, 0, label)
		for _, result := range []string{"HIT", "MISS", "EVICTION", "INVALIDATED"} {
			delivery.observer.IncCounterExact(ctx, metricCacheOperations, 0,
				label, StringCacheLabel("result", result))
		}
		for _, tier := range []string{string(CacheTierRequired), string(CacheTierOptional)} {
			for _, result := range []string{"WARMED", "FAILED", "SKIPPED"} {
				delivery.observer.IncCounterExact(ctx, metricCacheWarmupKeys, 0,
					label, StringCacheLabel("tier", tier), StringCacheLabel("result", result))
			}
		}
	}
}

// WarmupRouteSource is satisfied by the Router route service. It reads a
// server-owned route record by its configured identity.
type WarmupRouteSource interface {
	GetRoute(context.Context, string) (contracts.Route, error)
}

var (
	errRouterWarmupTarget = errors.New("invalid router warm-up target")
	errRouterWarmupRoute  = errors.New("router warm-up route identity mismatch")
)

// Warmup primes router.manifests and router.routes through the same validated
// paths used by delivery. No cache entry is inserted without a fresh Core
// metadata check and an authoritative route revision check.
func (delivery *CoreDelivery) Warmup(ctx context.Context, targets []RouterWarmupTarget, config RouterWarmupConfig) (WarmupResult, error) {
	if delivery == nil || ctx == nil {
		return WarmupResult{}, contracts.ErrPublicInvalid
	}
	result := WarmupResult{}
	if len(targets) == 0 {
		return result, nil
	}
	if delivery.manifests == nil || delivery.routeCache == nil {
		return result, contracts.ErrPublicInvalid
	}
	runCtx := ctx
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}
	var failures []error
	for _, target := range targets {
		if err := runCtx.Err(); err != nil {
			return result, err
		}
		targetCtx := runCtx
		if config.PerKeyTimeout > 0 {
			var cancel context.CancelFunc
			targetCtx, cancel = context.WithTimeout(runCtx, config.PerKeyTimeout)
			defer cancel()
		}
		if err := validateWarmupTarget(target); err != nil {
			if target.Tier == CacheTierRequired {
				failures = append(failures, err)
			}
			delivery.warmupKey(targetCtx, cacheRouterManifests, target.Tier, "FAILED")
			result.Failed++
			continue
		}
		if !config.Manifests {
			delivery.warmupKey(targetCtx, cacheRouterManifests, target.Tier, "SKIPPED")
			result.Skipped++
		} else {
			started := time.Now()
			_, _, err := delivery.manifests.read(targetCtx, target.FileID)
			if err != nil {
				if target.Tier == CacheTierRequired {
					failures = append(failures, err)
				}
				delivery.warmupKey(targetCtx, cacheRouterManifests, target.Tier, "FAILED")
				result.Failed++
				continue
			}
			delivery.warmupDuration(targetCtx, cacheRouterManifests, time.Since(started))
			result.Artifacts++
			delivery.warmupKey(targetCtx, cacheRouterManifests, target.Tier, "WARMED")
		}
		if !config.Routes {
			delivery.warmupKey(targetCtx, cacheRouterRoutes, target.Tier, "SKIPPED")
			result.Skipped++
			continue
		}
		started := time.Now()
		err := delivery.warmupRoute(targetCtx, target)
		if err != nil {
			if target.Tier == CacheTierRequired {
				failures = append(failures, err)
			}
			delivery.warmupKey(targetCtx, cacheRouterRoutes, target.Tier, "FAILED")
			result.Failed++
			continue
		}
		delivery.warmupDuration(targetCtx, cacheRouterRoutes, time.Since(started))
		result.Routes++
		delivery.warmupKey(targetCtx, cacheRouterRoutes, target.Tier, "WARMED")
	}
	return result, errors.Join(failures...)
}

// WarmupRefreshLoop repeats the same bounded pipeline at the configured
// interval. Each cycle completes before the next begins; cancellation closes
// the loop without leaving a partially applied cycle behind.
func (delivery *CoreDelivery) WarmupRefreshLoop(ctx context.Context, targets []RouterWarmupTarget, config RouterWarmupConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if config.RefreshInterval <= 0 || len(targets) == 0 {
		return nil
	}
	ticker := time.NewTicker(config.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_, _ = delivery.Warmup(ctx, targets, config)
		}
	}
}

func validateWarmupTarget(target RouterWarmupTarget) error {
	if target.RouteID == "" || target.Locale == "" || target.FileID == "" {
		return errRouterWarmupTarget
	}
	if _, err := contracts.CanonicalLocale(target.Locale); err != nil {
		return errRouterWarmupTarget
	}
	if len(target.Paths) == 0 {
		return errRouterWarmupTarget
	}
	for _, path := range target.Paths {
		normalized, err := contracts.NormalizePath(path)
		if err != nil || normalized != path {
			return errRouterWarmupTarget
		}
	}
	switch target.Tier {
	case CacheTierRequired, CacheTierOptional:
	default:
		return errRouterWarmupTarget
	}
	return nil
}

func (delivery *CoreDelivery) warmupRoute(ctx context.Context, target RouterWarmupTarget) error {
	source, ok := delivery.routes.(WarmupRouteSource)
	if !ok || isNilAPI(source) {
		return errRouterWarmupRoute
	}
	route, err := source.GetRoute(ctx, target.RouteID)
	if err != nil || route.ID != target.RouteID ||
		route.State != contracts.RouteStateActive || contracts.ValidateRoute(route) != nil {
		return errRouterWarmupRoute
	}
	if !matchesPublishedSlot(route, target) {
		return errRouterWarmupRoute
	}
	for _, path := range target.Paths {
		if !contracts.PathMatches(route.Path, path, route.Match) {
			return errRouterWarmupRoute
		}
		key := routeCacheKey{host: route.Host, path: path, method: http.MethodGet}
		resolved, err := delivery.routeCache.resolve(ctx, key, route.Host, path)
		if err != nil || resolved.ID != route.ID ||
			!matchesPublishedSlot(resolved, target) {
			return errRouterWarmupRoute
		}
	}
	return nil
}

func matchesPublishedSlot(route contracts.Route, target RouterWarmupTarget) bool {
	for _, slot := range route.Target.LocaleTargets {
		if slot.Locale == target.Locale {
			return slot.ArtifactManifestFileID == target.FileID
		}
	}
	return false
}

func (delivery *CoreDelivery) warmupKey(ctx context.Context, cache string, tier CacheTier, result string) {
	if delivery.observer == nil {
		return
	}
	delivery.observer.IncCounterExact(ctx, metricCacheWarmupKeys, 1,
		StringCacheLabel("cache", cache), StringCacheLabel("tier", string(tier)), StringCacheLabel("result", result))
}

func (delivery *CoreDelivery) warmupDuration(ctx context.Context, cache string, elapsed time.Duration) {
	if delivery.observer == nil {
		return
	}
	delivery.observer.ObserveHistogramExact(ctx, metricCacheWarmupDuration, elapsed.Seconds(),
		StringCacheLabel("cache", cache))
}
