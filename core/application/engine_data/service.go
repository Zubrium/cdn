package engine_data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	domainengine "github.com/Zubrium/cdn/core/domain/engine_data"
)

var (
	ErrModuleDataNotFound      = domainengine.ErrNotFound
	ErrModuleDataConflict      = domainengine.ErrConflict
	ErrModuleDataInvalid       = domainengine.ErrInvalid
	ErrModuleDataLimitExceeded = domainengine.ErrLimit
	ErrModuleDataUnavailable   = errors.New("module data storage unavailable")
	moduleNamePattern          = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	moduleNamespacePattern     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}(/[a-z][a-z0-9_-]{0,63})*$`)
	moduleScopePattern         = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	moduleFieldPattern         = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type SelectorType string

const (
	SelectorString      SelectorType = "string"
	SelectorNumber      SelectorType = "number"
	SelectorBoolean     SelectorType = "boolean"
	SelectorStringArray SelectorType = "string_array"
)

type SelectorOperator string

const (
	SelectorEqual    SelectorOperator = "eq"
	SelectorIn       SelectorOperator = "in"
	SelectorContains SelectorOperator = "contains"
)

type SelectorField struct {
	Type      SelectorType
	MaxValues int
}

type ModuleDataNamespace struct {
	Name       string
	ScopeTypes []string
	Fields     map[string]SelectorField
}

type ModuleDataDescriptor struct {
	ModuleID   string
	Namespaces []ModuleDataNamespace
}

type ModuleDataLimits struct {
	MaxPayloadBytes    int
	MaxIndexBytes      int
	MaxBatchBytes      int
	MaxPageBytes       int
	MaxSelectors       int
	MaxSelectorValues  int
	MaxPageSize        int
	MaxBatchOperations int
}

type ModuleDataKey = domainengine.Key

type ModuleDataValue = domainengine.Value

type ModuleDataRecord = domainengine.Record

type ModuleDataMutation = domainengine.Mutation

type ModuleDataSelector struct {
	Field    string
	Operator SelectorOperator
	Values   []any
}

type moduleDataNamespace struct {
	scopes map[string]struct{}
	fields map[string]SelectorField
}

// ModuleDataStore is a capability bound to one registered module ID and its
// declared namespaces. It deliberately exposes neither module ID nor SQL.
type Store struct {
	moduleID   string
	namespaces map[string]moduleDataNamespace
	repository domainengine.Repository
	limits     ModuleDataLimits
}

// NewStore creates one engine-scoped capability after its descriptor has been
// registered by the Core composition root.
func NewStore(repository domainengine.Repository, descriptor ModuleDataDescriptor, limits ModuleDataLimits) (*Store, error) {
	if repository == nil {
		return nil, ErrModuleDataUnavailable
	}
	namespaces, err := validateModuleDataDescriptor(descriptor, limits)
	if err != nil {
		return nil, err
	}
	return &Store{moduleID: descriptor.ModuleID, namespaces: namespaces, repository: repository, limits: limits}, nil
}

func validateModuleDataDescriptor(descriptor ModuleDataDescriptor, limits ModuleDataLimits) (map[string]moduleDataNamespace, error) {
	if !moduleNamePattern.MatchString(descriptor.ModuleID) || len(descriptor.Namespaces) == 0 || !validModuleDataLimits(limits) {
		return nil, ErrModuleDataInvalid
	}
	result := make(map[string]moduleDataNamespace, len(descriptor.Namespaces))
	for _, namespace := range descriptor.Namespaces {
		if len(namespace.Name) > 255 || !moduleNamespacePattern.MatchString(namespace.Name) || len(namespace.ScopeTypes) == 0 {
			return nil, ErrModuleDataInvalid
		}
		if _, exists := result[namespace.Name]; exists {
			return nil, ErrModuleDataInvalid
		}
		scopes := make(map[string]struct{}, len(namespace.ScopeTypes))
		for _, scope := range namespace.ScopeTypes {
			if !moduleScopePattern.MatchString(scope) {
				return nil, ErrModuleDataInvalid
			}
			if _, exists := scopes[scope]; exists {
				return nil, ErrModuleDataInvalid
			}
			scopes[scope] = struct{}{}
		}
		fields := make(map[string]SelectorField, len(namespace.Fields))
		for name, field := range namespace.Fields {
			if !moduleFieldPattern.MatchString(name) || !validSelectorField(field, limits.MaxSelectorValues) {
				return nil, ErrModuleDataInvalid
			}
			fields[name] = field
		}
		result[namespace.Name] = moduleDataNamespace{scopes: scopes, fields: fields}
	}
	return result, nil
}

func validModuleDataLimits(limits ModuleDataLimits) bool {
	return limits.MaxPayloadBytes > 0 && limits.MaxPayloadBytes <= 16<<20 &&
		limits.MaxIndexBytes > 0 && limits.MaxIndexBytes <= 1<<20 &&
		limits.MaxBatchBytes >= limits.MaxPayloadBytes+limits.MaxIndexBytes && limits.MaxBatchBytes <= 256<<20 &&
		limits.MaxPageBytes >= moduleDataRecordMemoryBound(limits) && limits.MaxPageBytes <= 64<<20 &&
		limits.MaxSelectors > 0 && limits.MaxSelectors <= 32 &&
		limits.MaxSelectorValues > 0 && limits.MaxSelectorValues <= 256 &&
		limits.MaxPageSize > 0 && limits.MaxPageSize <= 1000 &&
		limits.MaxBatchOperations > 0 && limits.MaxBatchOperations <= 256
}

func moduleDataStoreError(err error) error {
	if err == nil || errors.Is(err, ErrModuleDataNotFound) || errors.Is(err, ErrModuleDataConflict) ||
		errors.Is(err, ErrModuleDataInvalid) || errors.Is(err, ErrModuleDataLimitExceeded) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ErrModuleDataUnavailable
}

// moduleDataRecordMemoryBound budgets normalized JSONB payload/index data and
// the record envelope before a page is read. Core rejects normalized values
// above the configured payload/index limits on every write.
func moduleDataRecordMemoryBound(limits ModuleDataLimits) int {
	return limits.MaxPayloadBytes + limits.MaxIndexBytes + 1024
}

func moduleDataPageLimit(limits ModuleDataLimits, requested int) int {
	if requested < 1 || !validModuleDataLimits(limits) {
		return 0
	}
	maxRows := limits.MaxPageBytes / moduleDataRecordMemoryBound(limits)
	if maxRows < 1 {
		return 0
	}
	return min(requested, min(limits.MaxPageSize, maxRows))
}

func validModuleDataText(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validModuleDataString(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func ValidModuleDataLimits(limits ModuleDataLimits) bool { return validModuleDataLimits(limits) }

func validSelectorField(field SelectorField, maxValues int) bool {
	switch field.Type {
	case SelectorString, SelectorNumber, SelectorBoolean:
		return field.MaxValues == 0
	case SelectorStringArray:
		return field.MaxValues > 0 && field.MaxValues <= maxValues
	default:
		return false
	}
}

func (s *Store) namespace(name, scopeType string) (moduleDataNamespace, bool) {
	if s == nil || s.repository == nil {
		return moduleDataNamespace{}, false
	}
	namespace, ok := s.namespaces[name]
	if !ok {
		return moduleDataNamespace{}, false
	}
	_, scopeOK := namespace.scopes[scopeType]
	return namespace, scopeOK
}

// MaxPageSize exposes the registered operational bound so a module can page
// through records without guessing or issuing a request Core will reject.
func (s *Store) MaxPageSize() int {
	if s == nil {
		return 0
	}
	return s.limits.MaxPageSize
}

func (s *Store) MaxBatchSize() int {
	if s == nil {
		return 0
	}
	return s.limits.MaxBatchOperations
}

func (s *Store) MaxPayloadBytes() int {
	if s == nil {
		return 0
	}
	return s.limits.MaxPayloadBytes
}

func (s *Store) MaxIndexBytes() int {
	if s == nil {
		return 0
	}
	return s.limits.MaxIndexBytes
}

func (s *Store) MaxBatchBytes() int {
	if s == nil {
		return 0
	}
	return s.limits.MaxBatchBytes
}

func (s *Store) validateKey(key ModuleDataKey) bool {
	_, ok := s.namespace(key.Namespace, key.ScopeType)
	return ok && validModuleDataText(key.ScopeID, 256) && validModuleDataText(key.RecordKey, 256)
}

func (s *Store) Get(ctx context.Context, key ModuleDataKey) (ModuleDataRecord, error) {
	if !s.validateKey(key) {
		return ModuleDataRecord{}, ErrModuleDataInvalid
	}
	record, err := s.repository.Get(ctx, s.moduleID, domainengine.Key(key))
	return ModuleDataRecord(record), moduleDataStoreError(err)
}

// GetExpired is only for engine-owned cleanup and recovery. User-facing reads
// must use Get, which conceals records after their configured TTL.
func (s *Store) GetExpired(ctx context.Context, key ModuleDataKey) (ModuleDataRecord, error) {
	if !s.validateKey(key) {
		return ModuleDataRecord{}, ErrModuleDataInvalid
	}
	record, err := s.repository.GetExpired(ctx, s.moduleID, domainengine.Key(key))
	return ModuleDataRecord(record), moduleDataStoreError(err)
}

func (s *Store) Put(ctx context.Context, key ModuleDataKey, value ModuleDataValue, expectedRevision int64) (ModuleDataRecord, error) {
	if !s.validateKey(key) || expectedRevision < 0 {
		return ModuleDataRecord{}, ErrModuleDataInvalid
	}
	converted, _, err := s.validateValue(key, value)
	if err != nil {
		return ModuleDataRecord{}, err
	}
	row, err := s.repository.Put(ctx, s.moduleID, domainengine.Key(key), converted,
		expectedRevision, s.limits.MaxPayloadBytes, s.limits.MaxIndexBytes)
	return ModuleDataRecord(row), moduleDataStoreError(err)
}

func (s *Store) validateValue(key ModuleDataKey, value ModuleDataValue) (domainengine.Value, int, error) {
	if value.TTL < 0 || value.PayloadVersion <= 0 || value.PayloadVersion > 1<<31-1 || len(value.Payload) == 0 {
		return domainengine.Value{}, 0, ErrModuleDataInvalid
	}
	if len(value.Payload) > s.limits.MaxPayloadBytes || len(value.IndexData) > s.limits.MaxIndexBytes {
		return domainengine.Value{}, 0, ErrModuleDataLimitExceeded
	}
	payloadBytes, err := moduleDataJSONBSize(value.Payload, s.limits.MaxPayloadBytes)
	if err != nil {
		return domainengine.Value{}, 0, err
	}
	namespace, ok := s.namespace(key.Namespace, key.ScopeType)
	if !ok {
		return domainengine.Value{}, 0, ErrModuleDataInvalid
	}
	indexBytes, err := validateModuleDataProjectionSize(namespace, value.IndexData, s.limits.MaxIndexBytes)
	if err != nil {
		return domainengine.Value{}, 0, err
	}
	return domainengine.Value{
		Payload: value.Payload, PayloadVersion: value.PayloadVersion, IndexData: value.IndexData, TTL: value.TTL,
	}, payloadBytes + indexBytes, nil
}

func (s *Store) Delete(ctx context.Context, key ModuleDataKey, expectedRevision int64) error {
	if !s.validateKey(key) || expectedRevision <= 0 {
		return ErrModuleDataInvalid
	}
	return moduleDataStoreError(s.repository.Delete(ctx, s.moduleID, domainengine.Key(key), expectedRevision))
}

func (s *Store) Batch(ctx context.Context, mutations []ModuleDataMutation) error {
	if s == nil || len(mutations) == 0 {
		return ErrModuleDataInvalid
	}
	if len(mutations) > s.limits.MaxBatchOperations {
		return ErrModuleDataLimitExceeded
	}
	if err := validateModuleDataBatch(mutations); err != nil {
		return err
	}
	converted := make([]domainengine.Mutation, 0, len(mutations))
	batchBytes := 0
	for _, mutation := range mutations {
		if !s.validateKey(mutation.Key) || mutation.ExpectedRevision < 0 {
			return ErrModuleDataInvalid
		}
		var value *domainengine.Value
		if mutation.Value != nil {
			convertedValue, valueBytes, err := s.validateValue(mutation.Key, *mutation.Value)
			if err != nil {
				return err
			}
			if valueBytes > s.limits.MaxBatchBytes-batchBytes {
				return ErrModuleDataLimitExceeded
			}
			batchBytes += valueBytes
			value = &convertedValue
		}
		converted = append(converted, domainengine.Mutation{Key: domainengine.Key(mutation.Key), ExpectedRevision: mutation.ExpectedRevision, Value: value})
	}
	return moduleDataStoreError(s.repository.Batch(ctx, s.moduleID, converted, s.limits.MaxBatchOperations,
		s.limits.MaxBatchBytes, s.limits.MaxPayloadBytes, s.limits.MaxIndexBytes))
}

// validateModuleDataBatch keeps a transaction inside one engine aggregate
// scope and prevents ambiguous repeated CAS operations on the same record.
func validateModuleDataBatch(mutations []ModuleDataMutation) error {
	if len(mutations) == 0 {
		return ErrModuleDataInvalid
	}
	first := mutations[0].Key
	seen := make(map[ModuleDataKey]struct{}, len(mutations))
	for _, mutation := range mutations {
		key := mutation.Key
		if key.Namespace != first.Namespace || key.ScopeType != first.ScopeType || key.ScopeID != first.ScopeID || mutation.ExpectedRevision < 0 {
			return ErrModuleDataInvalid
		}
		if mutation.Value == nil && mutation.ExpectedRevision == 0 {
			return ErrModuleDataInvalid
		}
		if _, duplicate := seen[key]; duplicate {
			return ErrModuleDataInvalid
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (s *Store) List(ctx context.Context, namespaceName, scopeType, scopeID, after string, selectors []ModuleDataSelector, limit int) ([]ModuleDataKey, []ModuleDataRecord, string, error) {
	if s == nil || !validModuleDataText(scopeID, 256) || (after != "" && !validModuleDataText(after, 256)) || limit < 1 {
		return nil, nil, "", ErrModuleDataInvalid
	}
	if limit > s.limits.MaxPageSize {
		return nil, nil, "", ErrModuleDataLimitExceeded
	}
	namespace, ok := s.namespace(namespaceName, scopeType)
	if !ok {
		return nil, nil, "", ErrModuleDataInvalid
	}
	containment, err := selectorsToContainment(namespace, selectors, s.limits.MaxSelectors, s.limits.MaxSelectorValues, s.limits.MaxIndexBytes)
	if err != nil {
		return nil, nil, "", err
	}
	boundedLimit := moduleDataPageLimit(s.limits, limit)
	if boundedLimit < 1 {
		return nil, nil, "", ErrModuleDataInvalid
	}
	keys, rows, next, err := s.repository.List(ctx, s.moduleID, namespaceName, scopeType, scopeID, after, containment, boundedLimit)
	if err != nil {
		return nil, nil, "", moduleDataStoreError(err)
	}
	resultKeys, resultRecords := moduleDataRows(keys), moduleDataRecords(rows)
	if err := validateModuleDataPage(namespaceName, scopeType, scopeID, resultKeys, resultRecords, next); err != nil {
		return nil, nil, "", err
	}
	return resultKeys, resultRecords, next, nil
}

func (s *Store) ListExpired(ctx context.Context, namespaceName, scopeType, scopeID, after string, limit int) ([]ModuleDataKey, []ModuleDataRecord, string, error) {
	if s == nil || !validModuleDataText(scopeID, 256) || (after != "" && !validModuleDataText(after, 256)) || limit < 1 {
		return nil, nil, "", ErrModuleDataInvalid
	}
	if limit > s.limits.MaxPageSize {
		return nil, nil, "", ErrModuleDataLimitExceeded
	}
	if _, ok := s.namespace(namespaceName, scopeType); !ok {
		return nil, nil, "", ErrModuleDataInvalid
	}
	boundedLimit := moduleDataPageLimit(s.limits, limit)
	if boundedLimit < 1 {
		return nil, nil, "", ErrModuleDataInvalid
	}
	keys, rows, next, err := s.repository.ListExpired(ctx, s.moduleID, namespaceName, scopeType, scopeID, after, boundedLimit)
	if err != nil {
		return nil, nil, "", moduleDataStoreError(err)
	}
	resultKeys, resultRecords := moduleDataRows(keys), moduleDataRecords(rows)
	if err := validateModuleDataPage(namespaceName, scopeType, scopeID, resultKeys, resultRecords, next); err != nil {
		return nil, nil, "", err
	}
	return resultKeys, resultRecords, next, nil
}

func moduleDataRows(source []domainengine.Key) []ModuleDataKey {
	result := make([]ModuleDataKey, len(source))
	for i := range source {
		result[i] = ModuleDataKey(source[i])
	}
	return result
}

func moduleDataRecords(source []domainengine.Record) []ModuleDataRecord {
	result := make([]ModuleDataRecord, len(source))
	for i := range source {
		result[i] = ModuleDataRecord(source[i])
	}
	return result
}

// validateModuleDataPage keeps an opaque cursor and an index projection inside
// the capability's own scope before either reaches a consumer. A malformed
// repository page is a storage integrity failure, not data to disclose.
func validateModuleDataPage(namespaceName, scopeType, scopeID string, keys []ModuleDataKey, records []ModuleDataRecord, next string) error {
	if len(keys) != len(records) {
		return ErrModuleDataUnavailable
	}
	for _, key := range keys {
		if key.Namespace != namespaceName || key.ScopeType != scopeType || key.ScopeID != scopeID || !validModuleDataText(key.RecordKey, 256) {
			return ErrModuleDataUnavailable
		}
	}
	if next != "" {
		if !validModuleDataText(next, 256) || len(keys) == 0 || next != keys[len(keys)-1].RecordKey {
			return ErrModuleDataUnavailable
		}
	}
	return nil
}

func validateModuleDataProjection(namespace moduleDataNamespace, encoded json.RawMessage, maxBytes int) error {
	_, err := validateModuleDataProjectionSize(namespace, encoded, maxBytes)
	return err
}

func validateModuleDataProjectionSize(namespace moduleDataNamespace, encoded json.RawMessage, maxBytes int) (int, error) {
	if len(encoded) == 0 {
		encoded = json.RawMessage(`{}`)
	}
	normalizedBytes, err := moduleDataJSONBSize(encoded, maxBytes)
	if err != nil {
		return 0, err
	}
	var projection map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&projection); err != nil || projection == nil {
		return 0, ErrModuleDataInvalid
	}
	for key, value := range projection {
		field, ok := namespace.fields[key]
		if !ok || !validProjectionValue(field, value) {
			return 0, ErrModuleDataInvalid
		}
	}
	return normalizedBytes, nil
}

func validateModuleDataJSONB(encoded json.RawMessage, maxBytes int) error {
	_, err := moduleDataJSONBSize(encoded, maxBytes)
	return err
}

func moduleDataJSONBSize(encoded json.RawMessage, maxBytes int) (int, error) {
	size, err := ValidateJSONBObject(encoded, maxBytes)
	if errors.Is(err, ErrModuleDataLimitExceeded) {
		return 0, ErrModuleDataLimitExceeded
	}
	if err != nil {
		return 0, ErrModuleDataInvalid
	}
	return size, nil
}

func validProjectionValue(field SelectorField, value any) bool {
	switch field.Type {
	case SelectorString:
		text, ok := value.(string)
		return ok && validModuleDataString(text)
	case SelectorNumber:
		_, ok := value.(json.Number)
		return ok
	case SelectorBoolean:
		_, ok := value.(bool)
		return ok
	case SelectorStringArray:
		values, ok := value.([]any)
		if !ok || len(values) > field.MaxValues {
			return false
		}
		for _, item := range values {
			text, ok := item.(string)
			if !ok || !validModuleDataString(text) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func selectorsToContainment(namespace moduleDataNamespace, selectors []ModuleDataSelector, maxSelectors, maxValues, maxBytes int) ([]domainengine.ContainmentSelector, error) {
	if len(selectors) > maxSelectors {
		return nil, ErrModuleDataLimitExceeded
	}
	result := make([]domainengine.ContainmentSelector, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	encodedBytes := 0
	valueCount := 0
	for _, selector := range selectors {
		field, ok := namespace.fields[selector.Field]
		if !ok || !moduleFieldPattern.MatchString(selector.Field) {
			return nil, ErrModuleDataInvalid
		}
		if _, duplicate := seen[selector.Field]; duplicate {
			return nil, ErrModuleDataInvalid
		}
		seen[selector.Field] = struct{}{}
		if len(selector.Values) == 0 {
			return nil, ErrModuleDataInvalid
		}
		if len(selector.Values) > maxValues-valueCount {
			return nil, ErrModuleDataLimitExceeded
		}
		valueCount += len(selector.Values)
		var alternatives []json.RawMessage
		switch selector.Operator {
		case SelectorEqual:
			if len(selector.Values) != 1 || field.Type == SelectorStringArray {
				return nil, ErrModuleDataInvalid
			}
			fragment, err := selectorFragment(selector.Field, field, selector.Values[0], maxBytes-encodedBytes)
			if err != nil {
				return nil, err
			}
			encodedBytes += len(fragment)
			alternatives = append(alternatives, fragment)
		case SelectorIn:
			if field.Type == SelectorStringArray {
				return nil, ErrModuleDataInvalid
			}
			for _, value := range selector.Values {
				fragment, err := selectorFragment(selector.Field, field, value, maxBytes-encodedBytes)
				if err != nil {
					return nil, err
				}
				encodedBytes += len(fragment)
				alternatives = append(alternatives, fragment)
			}
		case SelectorContains:
			if field.Type != SelectorStringArray || len(selector.Values) != 1 {
				return nil, ErrModuleDataInvalid
			}
			item, ok := selector.Values[0].(string)
			if !ok || !validModuleDataString(item) {
				return nil, ErrModuleDataInvalid
			}
			if len(item) > maxBytes-encodedBytes {
				return nil, ErrModuleDataLimitExceeded
			}
			fragment, err := json.Marshal(map[string]any{selector.Field: []string{item}})
			if err != nil {
				return nil, ErrModuleDataInvalid
			}
			if err := validateModuleDataJSONB(fragment, maxBytes-encodedBytes); err != nil {
				return nil, err
			}
			encodedBytes += len(fragment)
			alternatives = append(alternatives, fragment)
		default:
			return nil, ErrModuleDataInvalid
		}
		result = append(result, domainengine.ContainmentSelector{Alternatives: alternatives})
	}
	return result, nil
}

func selectorFragment(fieldName string, field SelectorField, value any, maxBytes int) (json.RawMessage, error) {
	if maxBytes <= 0 {
		return nil, ErrModuleDataLimitExceeded
	}
	normalized := normalizeSelectorNumber(field, value)
	switch text := normalized.(type) {
	case string:
		if len(text) > maxBytes {
			return nil, ErrModuleDataLimitExceeded
		}
	case json.Number:
		if len(text.String()) > maxBytes {
			return nil, ErrModuleDataLimitExceeded
		}
	}
	if !validProjectionValue(field, normalized) {
		return nil, ErrModuleDataInvalid
	}
	encoded, err := json.Marshal(map[string]any{fieldName: normalized})
	if err != nil {
		return nil, ErrModuleDataInvalid
	}
	if err := validateModuleDataJSONB(encoded, maxBytes); err != nil {
		return nil, err
	}
	return encoded, nil
}

func normalizeSelectorNumber(field SelectorField, value any) any {
	if field.Type != SelectorNumber {
		return value
	}
	switch number := value.(type) {
	case json.Number:
		return number
	case float64:
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return nil
		}
		return json.Number(strconv.FormatFloat(number, 'g', -1, 64))
	case float32:
		parsed := float64(number)
		if math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return nil
		}
		return json.Number(strconv.FormatFloat(parsed, 'g', -1, 32))
	case int, int8, int16, int32, int64:
		return json.Number(strconv.FormatInt(toInt64(number), 10))
	case uint, uint8, uint16, uint32, uint64:
		return json.Number(strconv.FormatUint(toUint64(number), 10))
	default:
		return value
	}
}

func toInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}

func toUint64(value any) uint64 {
	switch v := value.(type) {
	case uint:
		return uint64(v)
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	default:
		return 0
	}
}
