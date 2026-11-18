package engine_data

import (
	"strings"
	"testing"
)

func TestJSONBTextSizeEstimateAccountsForWhitespaceAndExponentExpansion(t *testing.T) {
	compact := []byte(`{"label":"a,b:c","count":1}`)
	got, ok := estimateJSONBTextBytes(compact, 1024)
	if !ok || got != len(compact)+3 {
		t.Fatalf("estimated JSONB bytes = %d, %t; want %d, true", got, ok, len(compact)+3)
	}

	const count = 100
	expanded := []byte(`{"values":[` + strings.TrimSuffix(strings.Repeat("1e1000,", count), ",") + `]}`)
	if got, ok := estimateJSONBTextBytes(expanded, 4096); ok {
		t.Fatalf("accepted compact JSON that expands to %d JSONB text bytes", got)
	}
	if got, ok := estimateJSONBTextBytes([]byte(`{"value":1e1000}`), 2048); !ok || got < 1001 {
		t.Fatalf("rejected or under-estimated a bounded exponent: %d, %t", got, ok)
	}

	for _, number := range []string{`0.001e2`, `0.001e4`, `-0.00001e5`} {
		payload := []byte(`{"value":` + number + `}`)
		if got, ok := estimateJSONBTextBytes(payload, len(payload)+1); !ok || got != len(payload)+1 {
			t.Errorf("over-estimated normalized number %s: %d, %t; want %d, true", number, got, ok, len(payload)+1)
		}
	}

	// Leading fractional zeroes can be canceled by a positive exponent. Keep
	// them in the input budget without mistaking them for a huge output integer.
	largeCompactNumber := []byte(`{"value":0.` + strings.Repeat("0", 140000) + `1e140001}`)
	if got, ok := estimateJSONBTextBytes(largeCompactNumber, len(largeCompactNumber)+1); !ok || got != len(largeCompactNumber)+1 {
		t.Fatalf("rejected a long compact number which normalizes to 1: %d, %t", got, ok)
	}
}

func TestJSONBTextSizeEstimateRejectsOversizeAndInvalidBudgets(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
		limit   int
	}{
		{name: "raw input exceeds budget", payload: []byte(`{"value":"long"}`), limit: 4},
		{name: "zero budget", payload: []byte(`{}`), limit: 0},
		{name: "invalid exponent", payload: []byte(`{"value":1e999999999999999999999}`), limit: 4096},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := estimateJSONBTextBytes(test.payload, test.limit); ok {
				t.Fatalf("accepted payload with estimated size %d", got)
			}
		})
	}
}
