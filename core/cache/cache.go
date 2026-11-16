// Package cache contains an in-memory bounded cache. Callers must authorize
// each request separately; cached values carry no authorization decision.
package cache

import (
	"container/list"
	"sync"
	"time"
)

type entry struct {
	key    string
	value  []byte
	expiry time.Time
}
type Cache struct {
	mu                   sync.Mutex
	max, maxBytes, bytes int
	now                  func() time.Time
	items                map[string]*list.Element
	lru                  *list.List
}

func New(max, maxBytes int, now func() time.Time) *Cache {
	if max < 0 {
		max = 0
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	if now == nil {
		now = time.Now
	}
	return &Cache{max: max, maxBytes: maxBytes, now: now, items: make(map[string]*list.Element), lru: list.New()}
}

// NewByteBudget constructs a cache constrained only by its configured byte
// budget. A zero entry limit means that bytes, not entry count, bound growth.
func NewByteBudget(maxBytes int, now func() time.Time) *Cache {
	return New(0, maxBytes, now)
}
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, false
	}
	v := e.Value.(entry)
	if !c.now().Before(v.expiry) {
		c.remove(e)
		return nil, false
	}
	c.lru.MoveToFront(e)
	return append([]byte(nil), v.value...), true
}
func (c *Cache) Put(key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.remove(e)
	}
	if c.maxBytes == 0 || ttl <= 0 || key == "" || len(key) > c.maxBytes || len(value) > c.maxBytes-len(key) {
		return
	}
	e := c.lru.PushFront(entry{key: key, value: append([]byte(nil), value...), expiry: c.now().Add(ttl)})
	c.items[key] = e
	c.bytes += len(key) + len(value)
	for (c.max > 0 && c.lru.Len() > c.max) || c.bytes > c.maxBytes {
		c.remove(c.lru.Back())
	}
}
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.remove(e)
	}
}

func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.lru.Init()
	c.bytes = 0
}
func (c *Cache) remove(e *list.Element) {
	v := e.Value.(entry)
	delete(c.items, v.key)
	c.bytes -= len(v.key) + len(v.value)
	c.lru.Remove(e)
}
