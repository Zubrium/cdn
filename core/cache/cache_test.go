package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestGetReturnsCopiesOfCachedBytes(t *testing.T) {
	cache := New(4, 64, nil)
	input := []byte("asset")
	cache.Put("file", input, time.Minute)
	input[0] = 'X'
	first, ok := cache.Get("file")
	if !ok || string(first) != "asset" {
		t.Fatalf("cache changed with caller input: %q, %v", first, ok)
	}
	first[0] = 'Y'
	second, ok := cache.Get("file")
	if !ok || string(second) != "asset" {
		t.Fatalf("cache value was mutated by caller output: %q, %v", second, ok)
	}
}

func TestExpiredEntriesAreNeverReturned(t *testing.T) {
	now := time.Unix(100, 0)
	cache := New(4, 64, func() time.Time { return now })
	cache.Put("file", []byte("asset"), time.Second)
	if _, ok := cache.Get("file"); !ok {
		t.Fatal("live entry missed")
	}
	now = now.Add(time.Second)
	if _, ok := cache.Get("file"); ok {
		t.Fatal("expired entry returned")
	}
}

func TestCapacityEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := New(2, 64, nil)
	cache.Put("a", []byte("one"), time.Minute)
	cache.Put("b", []byte("two"), time.Minute)
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("expected initial cache hit")
	}
	cache.Put("c", []byte("three"), time.Minute)
	if _, ok := cache.Get("b"); ok {
		t.Fatal("least recently used entry was retained")
	}
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("recently used entry was evicted")
	}
	if _, ok := cache.Get("c"); !ok {
		t.Fatal("new entry was not cached")
	}
}

func TestByteLimitEvictsUntilEntriesFit(t *testing.T) {
	cache := New(10, 12, nil)
	cache.Put("a", []byte("1234"), time.Minute) // 5 bytes
	cache.Put("b", []byte("5678"), time.Minute) // 5 bytes
	cache.Put("c", []byte("90ab"), time.Minute) // 5 bytes
	if _, ok := cache.Get("a"); ok {
		t.Fatal("oldest entry exceeded the byte limit")
	}
	if _, ok := cache.Get("b"); !ok {
		t.Fatal("entry within byte limit was evicted")
	}
	if _, ok := cache.Get("c"); !ok {
		t.Fatal("new entry within byte limit was not cached")
	}
}

func TestInvalidOrOversizedEntriesAreNotCached(t *testing.T) {
	cache := New(2, 8, nil)
	cache.Put("", []byte("x"), time.Minute)
	cache.Put("zero-ttl", []byte("x"), 0)
	cache.Put("negative-ttl", []byte("x"), -time.Second)
	cache.Put("oversized", []byte("123456789"), time.Minute)
	cache.Put("key-too-large", []byte("x"), time.Minute)
	for _, key := range []string{"", "zero-ttl", "negative-ttl", "oversized", "key-too-large"} {
		if _, ok := cache.Get(key); ok {
			t.Errorf("invalid entry %q was cached", key)
		}
	}
}

func TestDeleteRemovesCachedEntry(t *testing.T) {
	cache := New(1, 64, nil)
	cache.Put("file", []byte("asset"), time.Minute)
	cache.Delete("file")
	if _, ok := cache.Get("file"); ok {
		t.Fatal("deleted entry returned")
	}
	cache.Delete("missing")
}

func TestByteBudgetUsesBytesWithoutEntryCountLimitAndCanClear(t *testing.T) {
	cache := NewByteBudget(24, nil)
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		cache.Put(key, []byte("x"), time.Minute)
	}
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		if _, ok := cache.Get(key); !ok {
			t.Fatalf("byte-only cache unexpectedly limited entry count at %q", key)
		}
	}
	cache.Clear()
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		if _, ok := cache.Get(key); ok {
			t.Fatalf("cache returned %q after clear", key)
		}
	}
}

func TestConcurrentCacheOperationsRemainSafe(t *testing.T) {
	cache := New(64, 1<<16, nil)
	const goroutines, iterations = 12, 500
	var group sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("file-%d", (worker+i)%32)
				cache.Put(key, []byte(fmt.Sprintf("payload-%d", i)), time.Minute)
				if value, ok := cache.Get(key); ok && len(value) > 0 {
					value[0] = 'X'
				}
				if i%7 == 0 {
					cache.Delete(key)
				}
			}
		}(worker)
	}
	group.Wait()
}
