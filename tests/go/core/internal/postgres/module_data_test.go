package postgres

import (
	"errors"
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

func TestModuleDataPayloadUsesJSONNumbersWithoutFloat64Loss(t *testing.T) {
	valid := ModuleDataValue{Payload: []byte(`{"amount":1e1000}`), PayloadVersion: 1}
	if err := validateModuleDataValue(valid, 4096, 1024); err != nil {
		t.Fatalf("valid bounded JSON number was rejected by generic Core payload validation: %v", err)
	}
	tooLarge := ModuleDataValue{Payload: []byte(`{"amount":1e10000}`), PayloadVersion: 1}
	if err := validateModuleDataValue(tooLarge, 4096, 1024); !errors.Is(err, ErrModuleDataLimit) {
		t.Fatalf("oversized normalized JSONB number returned %v", err)
	}
	for _, payload := range []string{`[]`, `null`, `"string"`} {
		if err := validateModuleDataValue(ModuleDataValue{Payload: []byte(payload), PayloadVersion: 1}, 4096, 1024); err == nil {
			t.Errorf("non-object module payload %s was accepted", payload)
		}
	}
}

func TestModuleDataRejectsInvalidJSONBUnicodeAndUnrepresentableVersions(t *testing.T) {
	for _, test := range []struct {
		name  string
		value ModuleDataValue
	}{
		{name: "invalid UTF-8 in payload", value: ModuleDataValue{Payload: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, PayloadVersion: 1}},
		{name: "NUL in payload", value: ModuleDataValue{Payload: []byte(`{"value":"\u0000"}`), PayloadVersion: 1}},
		{name: "unpaired surrogate in payload", value: ModuleDataValue{Payload: []byte(`{"value":"\uD800"}`), PayloadVersion: 1}},
		{name: "invalid UTF-8 in index", value: ModuleDataValue{Payload: []byte(`{"ok":true}`), IndexData: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, PayloadVersion: 1}},
		{name: "NUL in index", value: ModuleDataValue{Payload: []byte(`{"ok":true}`), IndexData: []byte(`{"value":"\u0000"}`), PayloadVersion: 1}},
		{name: "version exceeds PostgreSQL integer", value: ModuleDataValue{Payload: []byte(`{"ok":true}`), PayloadVersion: 1 << 31}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateModuleDataValue(test.value, 4096, 1024); !errors.Is(err, ErrModuleDataInvalid) {
				t.Fatalf("invalid JSONB value returned %v", err)
			}
		})
	}

	validPair := ModuleDataValue{Payload: []byte(`{"value":"\uD83D\uDE00"}`), PayloadVersion: 1}
	if err := validateModuleDataValue(validPair, 4096, 1024); err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
}

func TestModuleDataBatchEnforcesAggregateNormalizedJSONBytes(t *testing.T) {
	mutations := []ModuleDataMutation{
		{Value: &ModuleDataValue{Payload: []byte(`{"value":1e10}`), PayloadVersion: 1}},
		{Value: &ModuleDataValue{Payload: []byte(`{"value":1e10}`), PayloadVersion: 1}},
	}
	if err := validateModuleDataBatchSize(mutations, 2, 40, 128, 32); !errors.Is(err, ErrModuleDataLimit) {
		t.Fatalf("batch above aggregate JSON budget returned %v", err)
	}
	if err := validateModuleDataBatchSize(mutations, 2, 128, 128, 32); err != nil {
		t.Fatalf("bounded aggregate batch rejected: %v", err)
	}
}
