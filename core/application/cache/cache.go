package cache

import (
	"hash/fnv"
	"time"
)

// ByteView is a zero-copy handle to an immutable cached value. Intact detects
// illegal mutation of the exposed buffer without depending on an outward layer.
type ByteView struct {
	data   []byte
	digest uint64
}

// NewByteView takes a view over data and records its integrity digest.
func NewByteView(data []byte) ByteView {
	hash := fnv.New64a()
	_, _ = hash.Write(data)
	return ByteView{data: data, digest: hash.Sum64()}
}

// Bytes exposes the immutable cached buffer without allocating a copy.
func (v ByteView) Bytes() []byte { return v.data }

// Intact reports whether the exposed buffer is unchanged.
func (v ByteView) Intact() bool {
	hash := fnv.New64a()
	_, _ = hash.Write(v.data)
	return hash.Sum64() == v.digest
}

// CacheStats is the low-cardinality cache inventory needed by Core's metrics.
type CacheStats struct {
	Entries  int
	Bytes    int
	Leader   int64
	Joined   int64
	Failed   int64
	Overflow int64
}

// BoundedCache is the application-owned port for a process-local cache. The
// concrete sharded implementation is injected by infrastructure; application
// code never constructs or imports it.
type BoundedCache interface {
	GetView(key string) (ByteView, bool)
	GetOrLoad(key string, ttl time.Duration, source func() ([]byte, error)) ([]byte, error)
	Put(key string, value []byte, ttl time.Duration)
	Delete(key string)
	Clear()
	Stats() CacheStats
}

// CacheBounds are the explicit startup bounds passed to the injected factory.
// No field may be inferred at runtime.
type CacheBounds struct {
	Shards             int
	MaxEntries         int
	MaxBytes           int
	PromotionInterval  int
	CoalesceEnabled    bool
	CoalesceMaxWaiters int
	CoalesceTimeout    time.Duration
}

// CacheFactory constructs bounded caches for Core's authorization, metadata
// and byte caches from fully specified bounds.
type CacheFactory interface {
	NewBoundedCache(bounds CacheBounds) BoundedCache
}
