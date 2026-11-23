package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrRecordNotFound    = errors.New("record not found")
	ErrRecordConflict    = errors.New("record revision conflict")
	ErrRecordInvalid     = errors.New("invalid record operation")
	ErrRecordLimit       = errors.New("record operation limit exceeded")
	ErrRecordUnavailable = errors.New("record store unavailable")
)

type RecordSelectorType string

const (
	RecordSelectorString      RecordSelectorType = "string"
	RecordSelectorNumber      RecordSelectorType = "number"
	RecordSelectorBoolean     RecordSelectorType = "boolean"
	RecordSelectorStringArray RecordSelectorType = "string_array"
)

type RecordSelectorOperator string

const (
	RecordSelectorEqual    RecordSelectorOperator = "eq"
	RecordSelectorIn       RecordSelectorOperator = "in"
	RecordSelectorContains RecordSelectorOperator = "contains"
)

type RecordSelectorField struct {
	Type      RecordSelectorType `json:"type"`
	MaxValues int                `json:"max_values"`
}

type RecordNamespace struct {
	Name       string                         `json:"name"`
	ScopeTypes []string                       `json:"scope_types"`
	Fields     map[string]RecordSelectorField `json:"fields"`
}

func ValidRecordLimits(limits RecordLimits) bool {
	return limits.MaxPayloadBytes > 0 && limits.MaxPayloadBytes <= 16<<20 &&
		limits.MaxIndexBytes > 0 && limits.MaxIndexBytes <= 1<<20 &&
		limits.MaxBatchBytes >= limits.MaxPayloadBytes+limits.MaxIndexBytes && limits.MaxBatchBytes <= 256<<20 &&
		limits.MaxPageBytes >= limits.MaxPayloadBytes+limits.MaxIndexBytes+1024 && limits.MaxPageBytes <= 64<<20 &&
		limits.MaxSelectors > 0 && limits.MaxSelectors <= 32 &&
		limits.MaxSelectorValues > 0 && limits.MaxSelectorValues <= 256 &&
		limits.MaxPageSize > 0 && limits.MaxPageSize <= 1000 &&
		limits.MaxBatchOperations > 0 && limits.MaxBatchOperations <= 256
}

// RecordDescriptor is startup metadata for issuing an owner-scoped record
// client. Owner identity is bound by the composition root and is never sent in
// a record operation payload.
type RecordDescriptor struct {
	OwnerID    string            `json:"owner_id"`
	Namespaces []RecordNamespace `json:"namespaces"`
}

type RecordLimits struct {
	MaxPayloadBytes    int `json:"max_payload_bytes"`
	MaxIndexBytes      int `json:"max_index_bytes"`
	MaxBatchBytes      int `json:"max_batch_bytes"`
	MaxPageBytes       int `json:"max_page_bytes"`
	MaxSelectors       int `json:"max_selectors"`
	MaxSelectorValues  int `json:"max_selector_values"`
	MaxPageSize        int `json:"max_page_size"`
	MaxBatchOperations int `json:"max_batch_operations"`
}

type RecordKey struct {
	Namespace string `json:"namespace"`
	ScopeType string `json:"scope_type"`
	ScopeID   string `json:"scope_id"`
	RecordKey string `json:"record_key"`
}

type RecordValue struct {
	Payload        json.RawMessage `json:"payload"`
	PayloadVersion int             `json:"payload_version"`
	IndexData      json.RawMessage `json:"index_data"`
	TTL            time.Duration   `json:"ttl"`
}

type Record struct {
	Payload        json.RawMessage `json:"payload"`
	PayloadVersion int             `json:"payload_version"`
	IndexData      json.RawMessage `json:"index_data"`
	Revision       int64           `json:"revision"`
	ExpiresAt      *time.Time      `json:"expires_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type RecordMutation struct {
	Key              RecordKey    `json:"key"`
	ExpectedRevision int64        `json:"expected_revision"`
	Value            *RecordValue `json:"value,omitempty"`
}

type RecordSelector struct {
	Field    string                 `json:"field"`
	Operator RecordSelectorOperator `json:"operator"`
	Values   []any                  `json:"values"`
}

type RecordDataStore interface {
	Get(context.Context, RecordKey) (Record, error)
	GetExpired(context.Context, RecordKey) (Record, error)
	Put(context.Context, RecordKey, RecordValue, int64) (Record, error)
	Delete(context.Context, RecordKey, int64) error
	Batch(context.Context, []RecordMutation) error
	List(context.Context, string, string, string, string, []RecordSelector, int) ([]RecordKey, []Record, string, error)
	ListExpired(context.Context, string, string, string, string, int) ([]RecordKey, []Record, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}

type RecordGetRequest struct {
	Key RecordKey `json:"key"`
}

type RecordGetResponse struct {
	Record Record `json:"record"`
}

type RecordPutRequest struct {
	Key              RecordKey   `json:"key"`
	Value            RecordValue `json:"value"`
	ExpectedRevision int64       `json:"expected_revision"`
}

type RecordDeleteRequest struct {
	Key              RecordKey `json:"key"`
	ExpectedRevision int64     `json:"expected_revision"`
}

type RecordBatchRequest struct {
	Mutations []RecordMutation `json:"mutations"`
}

type RecordListRequest struct {
	Namespace string           `json:"namespace"`
	ScopeType string           `json:"scope_type"`
	ScopeID   string           `json:"scope_id"`
	After     string           `json:"after"`
	Selectors []RecordSelector `json:"selectors"`
	Limit     int              `json:"limit"`
}

type RecordListExpiredRequest struct {
	Namespace string `json:"namespace"`
	ScopeType string `json:"scope_type"`
	ScopeID   string `json:"scope_id"`
	After     string `json:"after"`
	Limit     int    `json:"limit"`
}

type RecordListResponse struct {
	Keys       []RecordKey `json:"keys"`
	Records    []Record    `json:"records"`
	NextCursor string      `json:"next_cursor"`
}

type RecordDeleteResponse struct{}
type RecordBatchResponse struct{}

func RecordGetOperation() Operation[RecordGetRequest, RecordGetResponse] {
	return Operation[RecordGetRequest, RecordGetResponse]{Key: Key{ID: "core.records.get", Version: 1}}
}

func RecordGetExpiredOperation() Operation[RecordGetRequest, RecordGetResponse] {
	return Operation[RecordGetRequest, RecordGetResponse]{Key: Key{ID: "core.records.get-expired", Version: 1}}
}

func RecordPutOperation() Operation[RecordPutRequest, RecordGetResponse] {
	return Operation[RecordPutRequest, RecordGetResponse]{Key: Key{ID: "core.records.put", Version: 1}}
}

func RecordDeleteOperation() Operation[RecordDeleteRequest, RecordDeleteResponse] {
	return Operation[RecordDeleteRequest, RecordDeleteResponse]{Key: Key{ID: "core.records.delete", Version: 1}}
}

func RecordBatchOperation() Operation[RecordBatchRequest, RecordBatchResponse] {
	return Operation[RecordBatchRequest, RecordBatchResponse]{Key: Key{ID: "core.records.batch", Version: 1}}
}

func RecordListOperation() Operation[RecordListRequest, RecordListResponse] {
	return Operation[RecordListRequest, RecordListResponse]{Key: Key{ID: "core.records.list", Version: 1}}
}

func RecordListExpiredOperation() Operation[RecordListExpiredRequest, RecordListResponse] {
	return Operation[RecordListExpiredRequest, RecordListResponse]{Key: Key{ID: "core.records.list-expired", Version: 1}}
}

type recordDataStoreClient struct {
	dispatcher Dispatcher
	limits     RecordLimits
}

func NewRecordDataStore(dispatcher Dispatcher, limits RecordLimits) (RecordDataStore, error) {
	if dispatcher == nil || !ValidRecordLimits(limits) {
		return nil, ErrRecordUnavailable
	}
	return &recordDataStoreClient{dispatcher: dispatcher, limits: limits}, nil
}

func (client *recordDataStoreClient) Get(ctx context.Context, key RecordKey) (Record, error) {
	response, err := Call(ctx, client.dispatcher, RecordGetOperation(), RecordGetRequest{Key: key})
	return response.Record, err
}

func (client *recordDataStoreClient) GetExpired(ctx context.Context, key RecordKey) (Record, error) {
	response, err := Call(ctx, client.dispatcher, RecordGetExpiredOperation(), RecordGetRequest{Key: key})
	return response.Record, err
}

func (client *recordDataStoreClient) Put(ctx context.Context, key RecordKey, value RecordValue, expected int64) (Record, error) {
	response, err := Call(ctx, client.dispatcher, RecordPutOperation(), RecordPutRequest{Key: key, Value: value, ExpectedRevision: expected})
	return response.Record, err
}

func (client *recordDataStoreClient) Delete(ctx context.Context, key RecordKey, expected int64) error {
	_, err := Call(ctx, client.dispatcher, RecordDeleteOperation(), RecordDeleteRequest{Key: key, ExpectedRevision: expected})
	return err
}

func (client *recordDataStoreClient) Batch(ctx context.Context, mutations []RecordMutation) error {
	_, err := Call(ctx, client.dispatcher, RecordBatchOperation(), RecordBatchRequest{Mutations: mutations})
	return err
}

func (client *recordDataStoreClient) List(ctx context.Context, namespace, scopeType, scopeID, after string, selectors []RecordSelector, limit int) ([]RecordKey, []Record, string, error) {
	response, err := Call(ctx, client.dispatcher, RecordListOperation(), RecordListRequest{
		Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, After: after, Selectors: selectors, Limit: limit,
	})
	return response.Keys, response.Records, response.NextCursor, err
}

func (client *recordDataStoreClient) ListExpired(ctx context.Context, namespace, scopeType, scopeID, after string, limit int) ([]RecordKey, []Record, string, error) {
	response, err := Call(ctx, client.dispatcher, RecordListExpiredOperation(), RecordListExpiredRequest{
		Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, After: after, Limit: limit,
	})
	return response.Keys, response.Records, response.NextCursor, err
}

func (client *recordDataStoreClient) MaxPageSize() int     { return client.limits.MaxPageSize }
func (client *recordDataStoreClient) MaxBatchSize() int    { return client.limits.MaxBatchOperations }
func (client *recordDataStoreClient) MaxPayloadBytes() int { return client.limits.MaxPayloadBytes }
func (client *recordDataStoreClient) MaxIndexBytes() int   { return client.limits.MaxIndexBytes }
func (client *recordDataStoreClient) MaxBatchBytes() int   { return client.limits.MaxBatchBytes }

var _ RecordDataStore = (*recordDataStoreClient)(nil)
