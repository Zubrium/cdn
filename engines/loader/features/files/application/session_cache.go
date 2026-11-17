package application

import (
	"container/list"
	"sync"
	"time"
)

// sessionCache is a bounded, process-local decode cache. A hit is valid only
// when the authoritative Core record still has the same database revision.
type sessionCache struct {
	mu       sync.Mutex
	max      int
	maxBytes int64
	bytes    int64
	ttl      time.Duration
	entries  map[string]*list.Element
	order    *list.List
}

type cachedSession struct {
	id        string
	principal string
	revision  int64
	size      int64
	value     storedUpload
	expiresAt time.Time
}

func newSessionCache(max int, maxBytes int64, ttl time.Duration) *sessionCache {
	return &sessionCache{max: max, maxBytes: maxBytes, ttl: ttl, entries: make(map[string]*list.Element, max), order: list.New()}
}

func (c *sessionCache) get(id, principal string, revision int64, now time.Time) (storedUpload, bool) {
	if c == nil {
		return storedUpload{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[id]
	if !ok {
		return storedUpload{}, false
	}
	entry := element.Value.(cachedSession)
	if !now.Before(entry.expiresAt) || entry.principal != principal || entry.revision != revision {
		c.removeElement(id, element)
		return storedUpload{}, false
	}
	c.order.MoveToFront(element)
	return entry.value, true
}

func (c *sessionCache) put(id, principal string, value storedUpload, revision int64, size int64, now time.Time) {
	if c == nil || c.max <= 0 || c.maxBytes <= 0 || revision <= 0 || size <= 0 || size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[id]; ok {
		c.removeElement(id, old)
	}
	entry := cachedSession{id: id, principal: principal, revision: revision, size: size, value: value, expiresAt: now.Add(c.ttl)}
	c.entries[id] = c.order.PushFront(entry)
	c.bytes += size
	for len(c.entries) > c.max || c.bytes > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.removeElement(oldest.Value.(cachedSession).id, oldest)
	}
}

func (c *sessionCache) remove(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[id]; ok {
		c.removeElement(id, element)
	}
}

func (c *sessionCache) removeElement(id string, element *list.Element) {
	entry := element.Value.(cachedSession)
	c.bytes -= entry.size
	if c.bytes < 0 {
		c.bytes = 0
	}
	delete(c.entries, id)
	c.order.Remove(element)
}
