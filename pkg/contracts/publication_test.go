package contracts

import (
	"strings"
	"testing"
)

func TestContentPublicationTriggerKeyIsValidAndStable(t *testing.T) {
	for _, input := range []string{"x", strings.Repeat("x", 256), "unicode-ключ-и-символы", strings.Repeat("a", 16), strings.Repeat("b", 128)} {
		key := ContentPublicationTriggerKey(input)
		if key != ContentPublicationTriggerKey(input) {
			t.Fatalf("unstable key for %q", input)
		}
		if err := PublicationPublishOperation().Validate(PublicationPublishRequest{Locale: "en-US", ContentPublicationRevision: 1, IdempotencyKey: key}); err != nil {
			t.Fatalf("derived key is invalid for %q: %v", input, err)
		}
	}
}
