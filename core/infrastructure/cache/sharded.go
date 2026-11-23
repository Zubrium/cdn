package cache

import (
	"container/list"
	"errors"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrCoalesceTimeout is returned to a waiter that exceeds the configured
// coalescing deadline. The caller may retry through its normal failure path.
var ErrCoalesceTimeout = errors.New("cache coalescing timeout")

// View is a zero-copy handle to an immutable cached value. Bytes exposes the
// stored buffer without allocating; Intact lets internal tests detect illegal
// mutation of that buffer.
type View struct {
	data   []byte
	digest uint64
}

func (v View) Bytes() []byte { return v.data }

func (v View) Intact() bool { return viewDigest(v.data) == v.digest }

func viewDigest(data []byte) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write(data)
	return hash.Sum64()
}

type shard struct {
	mu       sync.RWMutex
	items    map[string]*list.Element
	lru      *list.List
	bytes    int
	max      int
	maxBytes int
}

type flight struct {
	done    chan struct{}
	value   []byte
	err     error
	waiters int
}

type coalesceSettings struct {
	enabled    bool
	maxWaiters int
	timeout    time.Duration
}

// Stats reports low-cardinality counters required by the cache and
// concurrency specifications.
type Stats struct {
	Entries  int
	Bytes    int
	Leader   int64
	Joined   int64
	Failed   int64
	Overflow int64
}

// ShardedCache is a bounded LRU split into deterministic shards. A shard has
// its own RWMutex and exactly allocated share of the global byte budget.
type ShardedCache struct {
	shards            []*shard
	now               func() time.Time
	promotionInterval int
	promoteCounter    atomic.Uint64
	coalesce          coalesceSettings
	flightsMu         sync.Mutex
	flights           map[string]*flight
	leader            atomic.Int64
	joined            atomic.Int64
	failed            atomic.Int64
	overflow          atomic.Int64
}

// NewSharded constructs a cache with deterministic shard budgets. A zero entry
// limit means that bytes alone bound growth. The promotion interval controls
// sampled LRU promotion; 1 promotes on every read.
func NewSharded(shards, max, maxBytes, promotionInterval int, now func() time.Time) *ShardedCache {
	if shards < 1 {
		shards = 1
	}
	if max < 0 {
		max = 0
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	if promotionInterval < 1 {
		promotionInterval = 1
	}
	if now == nil {
		now = time.Now
	}
	perShardMax := max / shards
	byteShare := maxBytes / shards
	remainder := maxBytes % shards
	entries := make([]*shard, shards)
	for index := range entries {
		allocation := byteShare
		if index < remainder {
			allocation++
		}
		entryMax := perShardMax
		if perShardMax > 0 && index < max%shards {
			entryMax++
		}
		entries[index] = &shard{
			items: make(map[string]*list.Element), lru: list.New(),
			maxBytes: allocation, max: entryMax,
		}
	}
	return &ShardedCache{
		shards: entries, now: now, promotionInterval: promotionInterval,
		flights: make(map[string]*flight),
	}
}

// ConfigureCoalescing sets the bounded miss-coalescing policy. It is called
// explicitly by Core composition from validated configuration.
func (c *ShardedCache) ConfigureCoalescing(enabled bool, maxWaiters int, timeout time.Duration) {
	c.coalesce = coalesceSettings{enabled: enabled, maxWaiters: maxWaiters, timeout: timeout}
}

func (c *ShardedCache) shardFor(key string) *shard {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return c.shards[int(hash.Sum32())%len(c.shards)]
}

func (c *ShardedCache) promote() bool {
	interval := uint64(c.promotionInterval)
	if interval <= 1 {
		return true
	}
	return c.promoteCounter.Add(1)%interval == 0
}

// Get preserves the legacy copying contract for callers that may mutate bytes.
func (c *ShardedCache) Get(key string) ([]byte, bool) {
	view, ok := c.GetView(key)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), view.Bytes()...), true
}

// GetView returns an immutable zero-copy handle to the cached value.
func (c *ShardedCache) GetView(key string) (View, bool) {
	s := c.shardFor(key)
	promote := c.promote()
	if promote {
		s.mu.Lock()
		defer s.mu.Unlock()
	} else {
		s.mu.RLock()
		defer s.mu.RUnlock()
	}
	element, ok := s.items[key]
	if !ok {
		return View{}, false
	}
	cached := element.Value.(entry)
	if !c.now().Before(cached.expiry) {
		if !promote {
			s.mu.RUnlock()
			s.mu.Lock()
			if current, exists := s.items[key]; exists && !c.now().Before(current.Value.(entry).expiry) {
				s.remove(current)
			}
			s.mu.Unlock()
			s.mu.RLock()
		} else {
			s.remove(element)
		}
		return View{}, false
	}
	if promote {
		s.lru.MoveToFront(element)
	}
	return View{data: cached.value, digest: viewDigest(cached.value)}, true
}

func (c *ShardedCache) Put(key string, value []byte, ttl time.Duration) {
	if key == "" || len(value) == 0 || ttl <= 0 {
		return
	}
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.items[key]; ok {
		s.remove(existing)
	}
	if s.maxBytes == 0 || len(key) > s.maxBytes || len(value) > s.maxBytes-len(key) {
		return
	}
	stored := append([]byte(nil), value...)
	cached := entry{key: key, value: stored, expiry: c.now().Add(ttl)}
	element := s.lru.PushFront(cached)
	s.items[key] = element
	s.bytes += len(key) + len(stored)
	for (s.max > 0 && s.lru.Len() > s.max) || s.bytes > s.maxBytes {
		s.remove(s.lru.Back())
	}
}

func (c *ShardedCache) Delete(key string) {
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if element, ok := s.items[key]; ok {
		s.remove(element)
	}
}

func (c *ShardedCache) Clear() {
	for _, s := range c.shards {
		s.mu.Lock()
		s.items = make(map[string]*list.Element)
		s.lru.Init()
		s.bytes = 0
		s.mu.Unlock()
	}
	c.flightsMu.Lock()
	c.flights = make(map[string]*flight)
	c.flightsMu.Unlock()
}

func (s *shard) remove(element *list.Element) {
	entry := element.Value.(entry)
	delete(s.items, entry.key)
	s.bytes -= len(entry.key) + len(entry.value)
	s.lru.Remove(element)
}

// GetOrLoad coalesces concurrent misses for one key into a single leader load.
func (c *ShardedCache) GetOrLoad(key string, ttl time.Duration, source func() ([]byte, error)) ([]byte, error) {
	if view, ok := c.GetView(key); ok {
		return view.Bytes(), nil
	}
	if !c.coalesce.enabled || source == nil {
		return c.loadDirect(key, ttl, source)
	}
	c.flightsMu.Lock()
	if existing, ok := c.flights[key]; ok {
		if existing.waiters >= c.coalesce.maxWaiters {
			c.flightsMu.Unlock()
			c.failed.Add(1)
			return nil, ErrCoalesceTimeout
		}
		existing.waiters++
		c.joined.Add(1)
		c.flightsMu.Unlock()
		select {
		case <-existing.done:
			return existing.value, existing.err
		case <-time.After(c.coalesce.timeout):
			c.failed.Add(1)
			return nil, ErrCoalesceTimeout
		}
	}
	current := &flight{done: make(chan struct{})}
	c.flights[key] = current
	c.flightsMu.Unlock()
	c.leader.Add(1)

	value, err := source()
	if err == nil {
		c.Put(key, value, ttl)
	}
	c.flightsMu.Lock()
	current.value = value
	current.err = err
	delete(c.flights, key)
	close(current.done)
	c.flightsMu.Unlock()
	return value, err
}

func (c *ShardedCache) loadDirect(key string, ttl time.Duration, source func() ([]byte, error)) ([]byte, error) {
	if source == nil {
		return nil, errors.New("nil cache source")
	}
	value, err := source()
	if err == nil {
		c.Put(key, value, ttl)
	}
	return value, err
}

// InvalidateBatch applies bounded batches of invalidation notifications. When
// the notification set is larger than one batch, the whole cache is cleared so
// that a stale entry can never survive an overflow.
func (c *ShardedCache) InvalidateBatch(keys []string, batchSize int, _ time.Duration) {
	if len(keys) == 0 {
		return
	}
	if batchSize <= 0 || len(keys) > batchSize {
		c.overflow.Add(1)
		c.Clear()
		return
	}
	for _, key := range keys {
		c.Delete(key)
	}
}

func (c *ShardedCache) Stats() Stats {
	stats := Stats{
		Leader: c.leader.Load(), Joined: c.joined.Load(),
		Failed: c.failed.Load(), Overflow: c.overflow.Load(),
	}
	for _, s := range c.shards {
		s.mu.RLock()
		stats.Entries += len(s.items)
		stats.Bytes += s.bytes
		s.mu.RUnlock()
	}
	return stats
}
