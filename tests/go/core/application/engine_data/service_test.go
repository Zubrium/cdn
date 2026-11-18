package engine_data

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	domainengine "github.com/Zubrium/cdn/core/domain/engine_data"
)

type recordingEngineDataRepository struct {
	domainengine.Repository
	putCalls   int
	batchCalls int
}

func (r *recordingEngineDataRepository) Put(_ context.Context, _ string, _ domainengine.Key, _ domainengine.Value, _ int64, _, _ int) (domainengine.Record, error) {
	r.putCalls++
	return domainengine.Record{}, nil
}

func (r *recordingEngineDataRepository) Batch(context.Context, string, []domainengine.Mutation, int, int, int, int) error {
	r.batchCalls++
	return nil
}

func TestStoreRejectsNonObjectPayloadBeforeRepositoryWrite(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20,
		MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	repository := &recordingEngineDataRepository{}
	store, err := NewStore(repository, ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{{Name: "records", ScopeTypes: []string{"global"}}}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Put(context.Background(), ModuleDataKey{Namespace: "records", ScopeType: "global", ScopeID: "all", RecordKey: "session"},
		ModuleDataValue{Payload: json.RawMessage(`[]`), PayloadVersion: 1}, 0)
	if !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("non-object engine payload returned %v", err)
	}
	if repository.putCalls != 0 {
		t.Fatalf("invalid payload reached repository %d times", repository.putCalls)
	}
	_, err = store.Put(context.Background(), ModuleDataKey{Namespace: "records", ScopeType: "global", ScopeID: "all", RecordKey: "version"},
		ModuleDataValue{Payload: json.RawMessage(`{}`), PayloadVersion: 1 << 31}, 0)
	if !errors.Is(err, ErrModuleDataInvalid) || repository.putCalls != 0 {
		t.Fatalf("unrepresentable payload version reached the repository: error=%v writes=%d", err, repository.putCalls)
	}
}

func TestStoreAcceptsDottedEngineIdentifiers(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20,
		MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	store, err := NewStore(&recordingEngineDataRepository{}, ModuleDataDescriptor{
		ModuleID:   "engine.example",
		Namespaces: []ModuleDataNamespace{{Name: "uploads/sessions", ScopeTypes: []string{"principal"}}},
	}, limits)
	if err != nil || store == nil || store.moduleID != "engine.example" {
		t.Fatalf("valid dotted engine identifier was rejected: store=%+v err=%v", store, err)
	}
}

func TestStoreRejectsBatchAboveNormalizedByteBudgetBeforeRepositoryWrite(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 64, MaxIndexBytes: 64, MaxBatchBytes: 128, MaxPageBytes: 1152,
		MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	repository := &recordingEngineDataRepository{}
	store, err := NewStore(repository, ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{{Name: "records", ScopeTypes: []string{"global"}}}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	value := ModuleDataValue{Payload: json.RawMessage(`{"value":"` + strings.Repeat("x", 52) + `"}`), PayloadVersion: 1}
	mutations := []ModuleDataMutation{
		{Key: ModuleDataKey{Namespace: "records", ScopeType: "global", ScopeID: "all", RecordKey: "first"}, Value: &value},
		{Key: ModuleDataKey{Namespace: "records", ScopeType: "global", ScopeID: "all", RecordKey: "second"}, Value: &value},
	}
	if err := store.Batch(context.Background(), mutations); !errors.Is(err, ErrModuleDataLimitExceeded) {
		t.Fatalf("batch over normalized byte budget returned %v", err)
	}
	if repository.batchCalls != 0 {
		t.Fatalf("over-budget batch reached repository %d times", repository.batchCalls)
	}
}

func TestModuleDataRegistrationRejectsDuplicateAndUnboundedDescriptors(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20, MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	descriptor := ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{
		{Name: "uploads/sessions", ScopeTypes: []string{"principal"}, Fields: map[string]SelectorField{"state": {Type: SelectorString}}},
		{Name: "uploads/sessions", ScopeTypes: []string{"principal"}, Fields: map[string]SelectorField{"state": {Type: SelectorString}}},
	}}
	if _, err := validateModuleDataDescriptor(descriptor, limits); err == nil {
		t.Fatal("duplicate module namespace was accepted")
	}
	descriptor.Namespaces = descriptor.Namespaces[:1]
	descriptor.Namespaces[0].Fields["state"] = SelectorField{Type: SelectorString, MaxValues: 9}
	if _, err := validateModuleDataDescriptor(descriptor, limits); err == nil {
		t.Fatal("selector values above the configured cap were accepted")
	}
}

func TestModuleDataNamespaceSegmentsAreWellFormed(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20, MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	for _, name := range []string{"uploads/sessions", "feature-files/v1"} {
		descriptor := ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{{Name: name, ScopeTypes: []string{"global"}}}}
		if _, err := validateModuleDataDescriptor(descriptor, limits); err != nil {
			t.Errorf("valid namespace %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"uploads/", "uploads//sessions", "uploads/_private", "uploads/Session", "/uploads", strings.Repeat("a", 256)} {
		descriptor := ModuleDataDescriptor{ModuleID: "engine.example", Namespaces: []ModuleDataNamespace{{Name: name, ScopeTypes: []string{"global"}}}}
		if _, err := validateModuleDataDescriptor(descriptor, limits); err == nil {
			t.Errorf("malformed namespace %q accepted", name)
		}
	}
}

func TestModuleDataPageLimitBoundsPayloadAndIndexMemory(t *testing.T) {
	limits := ModuleDataLimits{
		MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 3 << 20, MaxPageBytes: 5 << 20,
		MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 100, MaxBatchOperations: 10,
	}
	pageLimit := moduleDataPageLimit(limits, 100)
	if pageLimit < 1 || pageLimit >= limits.MaxPageSize {
		t.Fatalf("page limit %d does not clamp the configured record count", pageLimit)
	}
	if got := moduleDataPageLimit(limits, 1); got != 1 {
		t.Fatalf("requested single-record page changed to %d", got)
	}
	limits.MaxPageBytes = 1
	if got := moduleDataPageLimit(limits, 100); got != 0 {
		t.Fatalf("page limit accepted a page budget smaller than one bounded record: %d", got)
	}
}

func TestModuleDataLimitAccessorsAndSelectorNumberNormalization(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 8192, MaxPageBytes: 1 << 20,
		MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	store := &Store{limits: limits}
	if store.MaxPageSize() != limits.MaxPageSize || store.MaxBatchSize() != limits.MaxBatchOperations ||
		store.MaxPayloadBytes() != limits.MaxPayloadBytes || store.MaxIndexBytes() != limits.MaxIndexBytes || store.MaxBatchBytes() != limits.MaxBatchBytes {
		t.Fatalf("module-data accessors did not expose configured limits: %+v", store.limits)
	}
	var nilStore *Store
	if nilStore.MaxPageSize() != 0 || nilStore.MaxBatchSize() != 0 || nilStore.MaxPayloadBytes() != 0 || nilStore.MaxIndexBytes() != 0 || nilStore.MaxBatchBytes() != 0 {
		t.Fatal("nil module-data store exposed non-zero limits")
	}

	numberField := SelectorField{Type: SelectorNumber}
	for value, expected := range map[any]string{
		int(-3): "-3", int8(-2): "-2", int16(-1): "-1", int32(0): "0", int64(1): "1",
		uint(2): "2", uint8(3): "3", uint16(4): "4", uint32(5): "5", uint64(6): "6",
		float32(1.25): "1.25", float64(2.5): "2.5", json.Number("1e3"): "1e3",
	} {
		got, ok := normalizeSelectorNumber(numberField, value).(json.Number)
		if !ok || got.String() != expected {
			t.Errorf("normalize selector number %T(%v) = %v; want %s", value, value, got, expected)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := normalizeSelectorNumber(numberField, value); got != nil {
			t.Errorf("non-finite selector number %v normalized to %v", value, got)
		}
	}
	if got := normalizeSelectorNumber(SelectorField{Type: SelectorString}, float64(1)); got != float64(1) {
		t.Fatalf("non-number selector field was unexpectedly converted: %T(%v)", got, got)
	}
}

func TestModuleDataLimitsRequireCapacityForOneMaximumRecord(t *testing.T) {
	limits := ModuleDataLimits{MaxPayloadBytes: 4096, MaxIndexBytes: 1024, MaxBatchBytes: 5119,
		MaxPageBytes: 1 << 20, MaxSelectors: 4, MaxSelectorValues: 8, MaxPageSize: 20, MaxBatchOperations: 10}
	if validModuleDataLimits(limits) {
		t.Fatal("batch limit smaller than one maximum payload and index was accepted")
	}
	limits.MaxBatchBytes++
	if !validModuleDataLimits(limits) {
		t.Fatal("batch limit equal to one maximum payload and index was rejected")
	}
}

func TestModuleDataProjectionMustMatchRegisteredTypes(t *testing.T) {
	namespace := moduleDataNamespace{fields: map[string]SelectorField{
		"state": {Type: SelectorString}, "attempts": {Type: SelectorNumber},
		"active": {Type: SelectorBoolean}, "groups": {Type: SelectorStringArray, MaxValues: 4},
	}}
	valid := json.RawMessage(`{"state":"uploading","attempts":2,"active":true,"groups":["assets","content"]}`)
	if err := validateModuleDataProjection(namespace, valid, 1024); err != nil {
		t.Fatalf("valid projection rejected: %v", err)
	}
	largeFiniteNumber := json.RawMessage(`{"attempts":1e1000}`)
	if err := validateModuleDataProjection(namespace, largeFiniteNumber, 2048); err != nil {
		t.Fatalf("bounded finite JSONB number was rejected in index projection: %v", err)
	}
	if _, err := selectorsToContainment(namespace, []ModuleDataSelector{{Field: "attempts", Operator: SelectorEqual, Values: []any{json.Number("1e1000")}}}, 4, 8, 2048); err != nil {
		t.Fatalf("bounded finite JSONB selector number was rejected: %v", err)
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"state":2}`),
		json.RawMessage(`{"attempts":1e10000}`),
		json.RawMessage(`{"active":"true"}`),
		json.RawMessage(`{"groups":["a","b","c","d","e"]}`),
		json.RawMessage(`{"undeclared":"value"}`),
	} {
		if err := validateModuleDataProjection(namespace, invalid, 1024); err == nil {
			t.Errorf("invalid projection accepted: %s", invalid)
		}
	}
}

func TestModuleDataSelectorsRequireDeclaredFieldAndSafeOperator(t *testing.T) {
	namespace := moduleDataNamespace{fields: map[string]SelectorField{
		"state": {Type: SelectorString}, "groups": {Type: SelectorStringArray, MaxValues: 8},
	}}
	valid := []ModuleDataSelector{
		{Field: "state", Operator: SelectorIn, Values: []any{"uploading", "committing"}},
		{Field: "groups", Operator: SelectorContains, Values: []any{"assets"}},
	}
	if _, err := selectorsToContainment(namespace, valid, 4, 8, 1024); err != nil {
		t.Fatalf("valid typed selectors rejected: %v", err)
	}
	invalid := [][]ModuleDataSelector{
		{{Field: "payload", Operator: SelectorEqual, Values: []any{"x"}}},
		{{Field: "state", Operator: "sql", Values: []any{"x"}}},
		{{Field: "groups", Operator: SelectorEqual, Values: []any{"assets"}}},
		{{Field: "state", Operator: SelectorIn, Values: []any{"one", 2}}},
		{{Field: "state", Operator: SelectorIn, Values: []any{"one", "two", "three"}}},
	}
	for _, selectors := range invalid {
		if _, err := selectorsToContainment(namespace, selectors, 4, 2, 1024); err == nil {
			t.Errorf("invalid selector accepted: %#v", selectors)
		}
	}
}

func TestModuleDataSelectorsShareOneAggregateSizeBudget(t *testing.T) {
	namespace := moduleDataNamespace{fields: map[string]SelectorField{
		"state": {Type: SelectorString}, "owner": {Type: SelectorString},
	}}
	selectors := []ModuleDataSelector{
		{Field: "state", Operator: SelectorEqual, Values: []any{strings.Repeat("x", 30)}},
		{Field: "owner", Operator: SelectorEqual, Values: []any{strings.Repeat("y", 30)}},
	}
	if _, err := selectorsToContainment(namespace, selectors, 4, 8, 64); !errors.Is(err, ErrModuleDataLimitExceeded) {
		t.Fatalf("selectors exceeding the shared byte budget returned %v", err)
	}
	tooManyValues := []ModuleDataSelector{
		{Field: "state", Operator: SelectorIn, Values: []any{"ready", "pending"}},
		{Field: "owner", Operator: SelectorIn, Values: []any{"one", "two"}},
	}
	if _, err := selectorsToContainment(namespace, tooManyValues, 4, 3, 1024); !errors.Is(err, ErrModuleDataLimitExceeded) {
		t.Fatalf("selector values above the aggregate count returned %v", err)
	}
	if _, err := selectorsToContainment(namespace, []ModuleDataSelector{{Field: "state", Operator: SelectorEqual, Values: []any{"bad\x00value"}}}, 4, 8, 64); !errors.Is(err, ErrModuleDataInvalid) {
		t.Fatalf("selector containing NUL returned %v", err)
	}
}

func TestModuleDataBatchMustStayWithinOneNamespaceAndScope(t *testing.T) {
	base := ModuleDataKey{Namespace: "engine.example", ScopeType: "global", ScopeID: "global", RecordKey: "session:a"}
	valid := []ModuleDataMutation{
		{Key: base, ExpectedRevision: 1},
		{Key: ModuleDataKey{Namespace: "engine.example", ScopeType: "global", ScopeID: "global", RecordKey: "session:b"}, ExpectedRevision: 2},
	}
	if err := validateModuleDataBatch(valid); err != nil {
		t.Fatalf("same-scope batch rejected: %v", err)
	}
	invalid := [][]ModuleDataMutation{
		{valid[0], {Key: ModuleDataKey{Namespace: "other", ScopeType: "global", ScopeID: "global", RecordKey: "x"}, ExpectedRevision: 0}},
		{valid[0], {Key: ModuleDataKey{Namespace: "engine.example", ScopeType: "global", ScopeID: "other", RecordKey: "x"}, ExpectedRevision: 0}},
		{valid[0], {Key: ModuleDataKey{Namespace: "engine.example", ScopeType: "principal", ScopeID: "global", RecordKey: "x"}, ExpectedRevision: 0}},
		{valid[0], valid[0]},
		{{Key: base, ExpectedRevision: 0}},
	}
	for i, batch := range invalid {
		if err := validateModuleDataBatch(batch); err == nil {
			t.Errorf("invalid batch %d accepted: %#v", i, batch)
		}
	}
}

func TestModuleDataErrorBoundaryDistinguishesLimitsAndHidesStorageDetails(t *testing.T) {
	namespace := moduleDataNamespace{fields: map[string]SelectorField{"state": {Type: SelectorString}}}
	if _, err := selectorsToContainment(namespace, []ModuleDataSelector{{Field: "state", Operator: SelectorEqual, Values: []any{"ready"}}}, 0, 8, 1024); !errors.Is(err, ErrModuleDataLimitExceeded) {
		t.Fatalf("selector count limit returned %v", err)
	}
	if got := moduleDataStoreError(errors.New("database host and query detail")); got != ErrModuleDataUnavailable || strings.Contains(got.Error(), "database host") {
		t.Fatalf("storage error boundary leaked implementation details: %v", got)
	}
	if got := moduleDataStoreError(ErrModuleDataConflict); !errors.Is(got, ErrModuleDataConflict) {
		t.Fatalf("revision conflict was hidden as a storage error: %v", got)
	}
}
