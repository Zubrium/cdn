package application

import (
	"testing"
	"time"
)

func TestSessionCacheEnforcesByteBudgetAndRevision(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	cache := newSessionCache(10, 5, time.Minute)
	first := storedUpload{ID: "first", State: "uploading"}
	second := storedUpload{ID: "second", State: "uploading"}
	cache.put("first", "principal", first, 1, 3, now)
	cache.put("second", "principal", second, 1, 3, now)
	if _, ok := cache.get("first", "principal", 1, now); ok {
		t.Fatal("least-recently-used entry survived an over-budget insertion")
	}
	if got, ok := cache.get("second", "principal", 1, now); !ok || got.ID != "second" {
		t.Fatalf("most-recently-used entry was not retained: got=%+v ok=%v", got, ok)
	}
	cache.put("too-large", "principal", storedUpload{ID: "too-large"}, 1, 6, now)
	if _, ok := cache.get("too-large", "principal", 1, now); ok {
		t.Fatal("entry larger than the total byte budget was cached")
	}
	if _, ok := cache.get("second", "principal", 2, now); ok {
		t.Fatal("cache returned an entry from an obsolete database revision")
	}
	cache.put("second", "principal", second, 1, 3, now)
	if _, ok := cache.get("second", "principal", 1, now.Add(time.Minute)); ok {
		t.Fatal("expired cache entry remained available")
	}
}
