package repository

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/forms/features/schemas/model"
	schemapkg "github.com/Zubrium/cdn/engines/forms/features/schemas/pkg"
)

const (
	namespace = "forms/schemas"
	scopeType = "snapshot"
)

type Store interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
	Batch(context.Context, []core.ModuleDataMutation) error
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}

type Repository struct{ store Store }

func New(store Store) (*Repository, error) {
	if store == nil || store.MaxPageSize() < 1 || store.MaxPayloadBytes() < 1 || store.MaxIndexBytes() < 1 ||
		store.MaxBatchSize() < 2 || store.MaxBatchBytes() < 1 {
		return nil, core.ErrModuleDataUnavailable
	}
	return &Repository{store: store}, nil
}

func KeyForFormKey(value string) string {
	return "schema-key:" + base64.RawURLEncoding.EncodeToString([]byte(value))
}

func KeyForSchemaID(value string) string {
	return "schema-id:" + value
}

func (repo *Repository) GetByKey(ctx context.Context, snapshotID, formKey string) (model.StoredSchema, error) {
	key := dataKey(snapshotID, KeyForFormKey(formKey))
	indexRecord, err := repo.store.Get(ctx, key)
	if err != nil {
		return model.StoredSchema{}, err
	}
	index, err := decodeKeyIndex(indexRecord)
	if err != nil {
		return model.StoredSchema{}, err
	}
	stored, err := repo.GetByID(ctx, snapshotID, index.ID)
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return model.StoredSchema{}, core.ErrModuleDataUnavailable
		}
		return model.StoredSchema{}, err
	}
	if stored.Schema.Key != formKey {
		return model.StoredSchema{}, core.ErrModuleDataUnavailable
	}
	return stored, nil
}

func (repo *Repository) GetByID(ctx context.Context, snapshotID, id string) (model.StoredSchema, error) {
	key := dataKey(snapshotID, KeyForSchemaID(id))
	record, err := repo.store.Get(ctx, key)
	if err != nil {
		return model.StoredSchema{}, err
	}
	schema, err := decode(record)
	if err != nil {
		return model.StoredSchema{}, err
	}
	if schema.ID != id {
		return model.StoredSchema{}, core.ErrModuleDataUnavailable
	}
	return model.StoredSchema{Schema: schema, Revision: record.Revision, RecordKey: key.RecordKey}, nil
}

func (repo *Repository) Create(ctx context.Context, snapshotID string, schema model.Schema) (int64, error) {
	value, err := encode(schema)
	if err != nil {
		return 0, err
	}
	keyIndex, err := encodeKeyIndex(schema)
	if err != nil {
		return 0, err
	}
	if repo.store.MaxBatchSize() < 2 || batchPayloadBytes(value, keyIndex) > repo.store.MaxBatchBytes() {
		return 0, core.ErrModuleDataLimitExceeded
	}
	if err := repo.store.Batch(ctx, []core.ModuleDataMutation{
		{Key: dataKey(snapshotID, KeyForSchemaID(schema.ID)), ExpectedRevision: 0, Value: &value},
		{Key: dataKey(snapshotID, KeyForFormKey(schema.Key)), ExpectedRevision: 0, Value: &keyIndex},
	}); err != nil {
		return 0, err
	}
	return 1, nil
}

func (repo *Repository) Update(ctx context.Context, snapshotID, oldRecordKey string, schema model.Schema, expectedRevision int64) (int64, error) {
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 || oldRecordKey != KeyForSchemaID(schema.ID) {
		return 0, core.ErrModuleDataInvalid
	}
	value, err := encode(schema)
	if err != nil {
		return 0, err
	}
	schemaKey := dataKey(snapshotID, KeyForSchemaID(schema.ID))
	current, err := repo.store.Get(ctx, schemaKey)
	if err != nil {
		return 0, err
	}
	currentSchema, err := decode(current)
	if err != nil {
		return 0, err
	}
	if currentSchema.ID != schema.ID || currentSchema.Key == "" {
		return 0, core.ErrModuleDataUnavailable
	}
	oldIndexKey := dataKey(snapshotID, KeyForFormKey(currentSchema.Key))
	oldIndex, err := repo.store.Get(ctx, oldIndexKey)
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return 0, core.ErrModuleDataUnavailable
		}
		return 0, err
	}
	oldIndexValue, err := decodeKeyIndex(oldIndex)
	if err != nil || oldIndexValue.ID != schema.ID {
		return 0, core.ErrModuleDataUnavailable
	}
	if currentSchema.Key == schema.Key {
		updated, err := repo.store.Put(ctx, schemaKey, value, expectedRevision)
		if err != nil {
			return 0, err
		}
		return updated.Revision, nil
	}
	keyIndex, err := encodeKeyIndex(schema)
	if err != nil {
		return 0, err
	}
	newIndexKey := dataKey(snapshotID, KeyForFormKey(schema.Key))
	if repo.store.MaxBatchSize() < 3 || batchPayloadBytes(value, keyIndex) > repo.store.MaxBatchBytes() {
		return 0, core.ErrModuleDataLimitExceeded
	}
	mutations := []core.ModuleDataMutation{
		{Key: oldIndexKey, ExpectedRevision: 1},
		{Key: newIndexKey, ExpectedRevision: 0, Value: &keyIndex},
		{Key: schemaKey, ExpectedRevision: expectedRevision, Value: &value},
	}
	if err := repo.store.Batch(ctx, mutations); err != nil {
		return 0, err
	}
	return expectedRevision + 1, nil
}

func (repo *Repository) List(ctx context.Context, snapshotID, after string, limit int) ([]model.StoredSchema, string, error) {
	keys, records, next, err := repo.store.List(ctx, namespace, scopeType, snapshotID, after, []core.ModuleDataSelector{{
		Field: "record_type", Operator: core.SelectorEqual, Values: []any{"schema"},
	}}, limit)
	if err != nil {
		return nil, "", err
	}
	if len(keys) != len(records) {
		return nil, "", core.ErrModuleDataUnavailable
	}
	items := make([]model.StoredSchema, 0, len(records))
	for index, record := range records {
		schema, err := decode(record)
		if err != nil {
			return nil, "", err
		}
		items = append(items, model.StoredSchema{Schema: schema, Revision: record.Revision, RecordKey: keys[index].RecordKey})
	}
	return items, next, nil
}

func dataKey(snapshotID, recordKey string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: recordKey}
}

func encode(schema model.Schema) (core.ModuleDataValue, error) {
	payload, err := json.Marshal(schemapkg.PayloadV1{ID: schema.ID, Key: schema.Key, Version: schema.Version, Fields: schema.Fields})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	index, err := json.Marshal(map[string]any{"record_type": "schema", "form_id": schema.ID, "form_key": schema.Key, "revision": schema.Version})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	return core.ModuleDataValue{Payload: payload, PayloadVersion: schemapkg.PayloadVersion, IndexData: index}, nil
}

func encodeKeyIndex(schema model.Schema) (core.ModuleDataValue, error) {
	payload, err := json.Marshal(schemapkg.KeyIndexV1{ID: schema.ID})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	index, err := json.Marshal(map[string]any{"record_type": "schema_key", "form_id": schema.ID, "form_key": schema.Key})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	return core.ModuleDataValue{Payload: payload, PayloadVersion: schemapkg.PayloadVersion, IndexData: index}, nil
}

func decodeKeyIndex(record core.ModuleDataRecord) (schemapkg.KeyIndexV1, error) {
	var index schemapkg.KeyIndexV1
	if record.PayloadVersion != schemapkg.PayloadVersion {
		return index, core.ErrModuleDataUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	if err := decoder.Decode(&index); err != nil || index.ID == "" {
		return schemapkg.KeyIndexV1{}, core.ErrModuleDataUnavailable
	}
	return index, nil
}

func batchPayloadBytes(values ...core.ModuleDataValue) int {
	total := 0
	for _, value := range values {
		total += len(value.Payload) + len(value.IndexData)
	}
	return total
}

func decode(record core.ModuleDataRecord) (model.Schema, error) {
	if record.PayloadVersion != schemapkg.PayloadVersion {
		return model.Schema{}, core.ErrModuleDataUnavailable
	}
	var payload schemapkg.PayloadV1
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload.ID == "" || payload.Key == "" || payload.Version < 1 || payload.Fields == nil {
		return model.Schema{}, core.ErrModuleDataUnavailable
	}
	return model.Schema{ID: payload.ID, Key: payload.Key, Version: payload.Version, Fields: payload.Fields}, nil
}
