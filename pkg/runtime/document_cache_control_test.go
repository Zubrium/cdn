package runtime

import (
	"testing"
	"time"
)

func TestRuntimeAssetCacheControlRejectsInvalidTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second, 1500 * time.Millisecond} {
		t.Run(ttl.String(), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("runtimeAssetCacheControl(%s) should fail closed", ttl)
				}
			}()
			runtimeAssetCacheControl(ttl)
		})
	}
}
