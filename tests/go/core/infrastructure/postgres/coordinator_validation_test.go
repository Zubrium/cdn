package postgres

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoordinatorIdempotencyKeyValidation(t *testing.T) {
	for _, input := range []struct {
		name string
		key  string
		want bool
	}{
		{name: "valid", key: "import:feature:42", want: true},
		{name: "unicode", key: "импорт:фича:42", want: true},
		{name: "empty", key: ""},
		{name: "edge whitespace", key: " key"},
		{name: "control", key: "bad\nkey"},
		{name: "null", key: "bad\x00key"},
		{name: "invalid utf8", key: string([]byte{0xff})},
		{name: "too long", key: strings.Repeat("a", 257)},
	} {
		t.Run(input.name, func(t *testing.T) {
			if got := validCoordinatorIdempotencyKey(input.key); got != input.want {
				t.Fatalf("validCoordinatorIdempotencyKey(%q)=%v, want %v", input.key, got, input.want)
			}
		})
	}
}

func TestCoordinatorPayloadValidationRejectsInvalidUTF8(t *testing.T) {
	if validCoordinatorJSON(json.RawMessage([]byte(`{"name":"` + string([]byte{0xff}) + `"}`))) {
		t.Fatal("invalid UTF-8 Coordinator payload was accepted")
	}
	if !validCoordinatorJSON(json.RawMessage(`{"name":"valid"}`)) {
		t.Fatal("valid UTF-8 Coordinator payload was rejected")
	}
}
