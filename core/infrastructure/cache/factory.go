package cache

import (
	"time"

	appcache "github.com/Zubrium/cdn/core/application/cache"
)

// Factory adapts the sharded process-local cache to Core application's
// injected cache port. It lives in infrastructure so application never imports
// an outward layer.
type Factory struct{}

var _ appcache.CacheFactory = Factory{}

// NewBoundedCache constructs one sharded cache from explicit bounds.
func (Factory) NewBoundedCache(bounds appcache.CacheBounds) appcache.BoundedCache {
	sharded := NewSharded(
		bounds.Shards, bounds.MaxEntries, bounds.MaxBytes, bounds.PromotionInterval, time.Now,
	)
	sharded.ConfigureCoalescing(bounds.CoalesceEnabled, bounds.CoalesceMaxWaiters, bounds.CoalesceTimeout)
	return &boundedCacheAdapter{sharded: sharded}
}

type boundedCacheAdapter struct {
	sharded *ShardedCache
}

var _ appcache.BoundedCache = (*boundedCacheAdapter)(nil)

func (a *boundedCacheAdapter) GetView(key string) (appcache.ByteView, bool) {
	view, ok := a.sharded.GetView(key)
	if !ok {
		return appcache.ByteView{}, false
	}
	return appcache.NewByteView(view.Bytes()), true
}

func (a *boundedCacheAdapter) GetOrLoad(key string, ttl time.Duration, source func() ([]byte, error)) ([]byte, error) {
	return a.sharded.GetOrLoad(key, ttl, source)
}

func (a *boundedCacheAdapter) Put(key string, value []byte, ttl time.Duration) {
	a.sharded.Put(key, value, ttl)
}

func (a *boundedCacheAdapter) Delete(key string) { a.sharded.Delete(key) }

func (a *boundedCacheAdapter) Clear() { a.sharded.Clear() }

func (a *boundedCacheAdapter) Stats() appcache.CacheStats {
	stats := a.sharded.Stats()
	return appcache.CacheStats{
		Entries: stats.Entries, Bytes: stats.Bytes,
		Leader: stats.Leader, Joined: stats.Joined,
		Failed: stats.Failed, Overflow: stats.Overflow,
	}
}
