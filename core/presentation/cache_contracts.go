package presentation

import appcache "github.com/Zubrium/cdn/core/application/cache"

// Cache configuration and observer contracts are re-exported through the
// public Core facade. Composition modules must never import Core application
// or infrastructure packages directly.
type (
	CacheConfig          = appcache.Config
	CacheMode            = appcache.Mode
	CacheTier            = appcache.Tier
	CacheRetry           = appcache.Retry
	CacheSources         = appcache.Sources
	CacheTargets         = appcache.Targets
	CachePrincipalAction = appcache.PrincipalAction
	CacheKey             = appcache.Key
	CacheStoreConfig     = appcache.StoreConfig
	CacheObserver        = appcache.Observer
	CacheLabel           = appcache.Label
)

const (
	CacheModeStrict     = appcache.ModeStrict
	CacheModeBestEffort = appcache.ModeBestEffort
	CacheModeOff        = appcache.ModeOff

	CacheTierRequired = appcache.TierRequired
	CacheTierOptional = appcache.TierOptional
)
