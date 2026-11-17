package pkg

import "testing"

func TestValidIdempotencyKeyRejectsAmbiguousOrUnboundedValues(t *testing.T) {
	for _, value := range []string{"", "short", "1234567890123456/other", string(make([]byte, 129))} {
		if ValidIdempotencyKey(value) {
			t.Errorf("ValidIdempotencyKey(%q) = true", value)
		}
	}
	if !ValidIdempotencyKey("request-000000000001") {
		t.Fatal("valid idempotency key was rejected")
	}
}
