package cache

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoreCacheScenario(t *testing.T) {
	t.Run("CONC-005", func(t *testing.T) {
		sharded := NewSharded(4, 0, 1<<20, 1, time.Now)
		sharded.coalesce.enabled = true
		sharded.coalesce.maxWaiters = 32
		sharded.coalesce.timeout = 5 * time.Second

		var loads atomic.Int64
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 24; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				value, err := sharded.GetOrLoad("cold", time.Minute, func() ([]byte, error) {
					loads.Add(1)
					time.Sleep(10 * time.Millisecond)
					return []byte("payload"), nil
				})
				if err != nil || !bytes.Equal(value, []byte("payload")) {
					t.Errorf("unexpected loaded value %q, err=%v", value, err)
				}
			}()
		}
		close(start)
		wg.Wait()
		if got := loads.Load(); got != 1 {
			t.Fatalf("cold key loaded %d times, want 1", got)
		}
		stats := sharded.Stats()
		if stats.Leader != 1 || stats.Joined != 23 {
			t.Fatalf("coalescing stats = %+v, want 1 leader and 23 joined", stats)
		}
	})

	t.Run("CONC-006", func(t *testing.T) {
		sharded := NewSharded(2, 0, 4<<20, 1, time.Now)
		small := bytes.Repeat([]byte("a"), 1<<10)
		large := bytes.Repeat([]byte("b"), 1<<20)
		sharded.Put("small", small, time.Minute)
		sharded.Put("large", large, time.Minute)

		smallView, ok := sharded.GetView("small")
		if !ok {
			t.Fatal("small view missing")
		}
		largeView, ok := sharded.GetView("large")
		if !ok {
			t.Fatal("large view missing")
		}
		if !smallView.Intact() || !largeView.Intact() {
			t.Fatal("fresh views must be intact")
		}
		allocsSmall := testing.AllocsPerRun(100, func() { _, _ = sharded.GetView("small") })
		allocsLarge := testing.AllocsPerRun(100, func() { _, _ = sharded.GetView("large") })
		if allocsLarge > allocsSmall+2 {
			t.Fatalf("zero-copy view allocations scale with object size: small=%f large=%f", allocsSmall, allocsLarge)
		}
		largeView.Bytes()[0] = 'X'
		if largeView.Intact() {
			t.Fatal("mutation of cache.View was not detected")
		}
	})

	t.Run("CONC-007", func(t *testing.T) {
		sharded := NewSharded(2, 0, 1<<20, 1, time.Now)
		for index := 0; index < 6; index++ {
			sharded.Put(fmt.Sprintf("key-%d", index), []byte("value"), time.Minute)
		}
		sharded.InvalidateBatch([]string{"key-0", "key-1", "key-2", "key-3", "key-4"}, 8, 0)
		if sharded.Stats().Entries != 1 {
			t.Fatalf("batch invalidation left %d entries, want 1", sharded.Stats().Entries)
		}
		if _, ok := sharded.Get("key-5"); !ok {
			t.Fatal("unrelated key was removed by batch invalidation")
		}
		// More notifications than one batch can carry within the window forces a
		// full clear: stale entries must never survive an overflow.
		sharded.Put("overflow", []byte("value"), time.Minute)
		sharded.InvalidateBatch([]string{"key-5", "overflow"}, 1, 0)
		if sharded.Stats().Entries != 0 {
			t.Fatalf("overflow did not clear the cache: %d entries remain", sharded.Stats().Entries)
		}
		if sharded.Stats().Overflow != 1 {
			t.Fatalf("overflow counter = %d, want 1", sharded.Stats().Overflow)
		}
	})

	t.Run("CONC-006-shards", func(t *testing.T) {
		sharded := NewSharded(4, 0, 1024, 1, time.Now)
		for index := 0; index < 100; index++ {
			sharded.Put(fmt.Sprintf("key-%d", index), bytes.Repeat([]byte("x"), 32), time.Minute)
		}
		if stats := sharded.Stats(); stats.Bytes > 1024 {
			t.Fatalf("sharded cache exceeded byte budget: %d", stats.Bytes)
		}
	})
}
