package composition

import (
	"context"

	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/presentation/router"
)

// routerCacheObserver adapts the process-wide exact-metric sink to the narrow
// Router cache contract. It never adds identity attributes, so warm-up series
// keep only the fixed cache/tier/result vocabulary from SPEC-caching.md §8.
type routerCacheObserver struct {
	observer *observability.Observer
}

func (adapter routerCacheObserver) IncCounterExact(ctx context.Context, name string, value float64, labels ...router.CacheLabel) {
	if adapter.observer == nil {
		return
	}
	adapter.observer.IncCounterExact(ctx, name, value, cacheObserverAttributes(labels)...)
}

func (adapter routerCacheObserver) SetGaugeExact(ctx context.Context, name string, value float64, labels ...router.CacheLabel) {
	if adapter.observer == nil {
		return
	}
	adapter.observer.SetGaugeExact(ctx, name, value, cacheObserverAttributes(labels)...)
}

func (adapter routerCacheObserver) ObserveHistogramExact(ctx context.Context, name string, value float64, labels ...router.CacheLabel) {
	if adapter.observer == nil {
		return
	}
	adapter.observer.ObserveHistogramExact(ctx, name, value, cacheObserverAttributes(labels)...)
}

func cacheObserverAttributes(labels []router.CacheLabel) []observability.Attribute {
	attributes := make([]observability.Attribute, 0, len(labels))
	for _, label := range labels {
		attributes = append(attributes, observability.String(label.Key, label.Value))
	}
	return attributes
}

// routerWarmupTargets derives Router identities from the explicitly configured
// artifact slots. No database read, HTTP call or visitor input is involved.
func routerWarmupTargets(config appconfig.Resolved) []router.RouterWarmupTarget {
	targets := make([]router.RouterWarmupTarget, 0, len(config.PublishedArtifacts))
	for _, artifact := range config.PublishedArtifacts {
		targets = append(targets, router.RouterWarmupTarget{
			RouteID: artifact.RouteID,
			Locale:  artifact.Locale,
			FileID:  artifact.FileID,
			Tier:    router.CacheTier(artifact.Tier),
			Paths:   append([]string(nil), artifact.Paths...),
		})
	}
	return targets
}

// routerWarmupConfig gates both Router targets with their startup flags and
// reuses the explicit Core time bounds for the Router-owned part of the
// pipeline. Disabled warm-up and off mode return an all-inactive contract.
func routerWarmupConfig(config appconfig.Resolved) router.RouterWarmupConfig {
	warmup := config.Core.Warmup
	if !warmup.Enabled || warmup.Mode == platform.CacheModeOff {
		return router.RouterWarmupConfig{}
	}
	cfg := router.RouterWarmupConfig{
		Manifests:       warmup.Targets.RouterManifests,
		Routes:          warmup.Targets.RouterRoutes,
		Timeout:         config.Scales.WarmupTimeouts.Timeout,
		PerKeyTimeout:   config.Scales.WarmupTimeouts.PerKeyTimeout,
		RefreshInterval: config.Scales.WarmupTimeouts.RefreshInterval,
	}
	if !cfg.Manifests && !cfg.Routes {
		return router.RouterWarmupConfig{}
	}
	return cfg
}
