package postgres

import (
	"errors"
	"testing"
)

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
