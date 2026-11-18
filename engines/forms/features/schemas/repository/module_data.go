package repository

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

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
	Delete(context.Context, core.ModuleDataKey, int64) error
	Batch(context.Context, []core.ModuleDataMutation) error
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}

type Repository struct{ store Store }

type Entry struct {
	Schema model.Schema
	Record core.ModuleDataRecord
}

func New(store Store) (*Repository, error) {
	if store == nil || store.MaxPageSize() < 1 || store.MaxPayloadBytes() < 1 || store.MaxIndexBytes() < 1 ||
		store.MaxBatchSize() < 2 || store.MaxBatchBytes() < 1 {
		return nil, core.ErrModuleDataUnavailable
	}
	return &Repository{store: store}, nil
}

func KeyForFormKey(value string) string {
	return "schema:" + base64.RawURLEncoding.EncodeToString([]byte(value))
}

func (repo *Repository) GetByKey(ctx context.Context, snapshotID, formKey string) (model.Schema, core.ModuleDataRecord, error) {
	key := dataKey(snapshotID, KeyForFormKey(formKey))
	record, err := repo.store.Get(ctx, key)
	if err != nil {
		return model.Schema{}, core.ModuleDataRecord{}, err
	}
	schema, err := decode(record)
	if err != nil {
		return model.Schema{}, core.ModuleDataRecord{}, err
	}
	if schema.Key != formKey {
		return model.Schema{}, core.ModuleDataRecord{}, core.ErrModuleDataUnavailable
	}
	return schema, record, nil
}

func (repo *Repository) GetByID(ctx context.Context, snapshotID, id string) (model.Schema, core.ModuleDataRecord, string, error) {
	limit := 2
	if repo.store.MaxPageSize() < limit {
		limit = repo.store.MaxPageSize()
	}
	keys, records, next, err := repo.store.List(ctx, namespace, scopeType, snapshotID, "", []core.ModuleDataSelector{{
		Field: "form_id", Operator: core.SelectorEqual, Values: []any{id},
	}}, limit)
	if err != nil {
		return model.Schema{}, core.ModuleDataRecord{}, "", err
	}
	if len(keys) == 0 {
		return model.Schema{}, core.ModuleDataRecord{}, "", core.ErrModuleDataNotFound
	}
	if len(keys) != 1 || len(records) != 1 || next != "" {
		return model.Schema{}, core.ModuleDataRecord{}, "", core.ErrModuleDataUnavailable
	}
	schema, err := decode(records[0])
	if err != nil {
		return model.Schema{}, core.ModuleDataRecord{}, "", err
	}
	if schema.ID != id {
		return model.Schema{}, core.ModuleDataRecord{}, "", core.ErrModuleDataUnavailable
	}
	return schema, records[0], keys[0].RecordKey, nil
}

func (repo *Repository) Create(ctx context.Context, snapshotID string, schema model.Schema) (int64, error) {
	value, err := encode(schema)
	if err != nil {
		return 0, err
	}
	record, err := repo.store.Put(ctx, dataKey(snapshotID, KeyForFormKey(schema.Key)), value, 0)
	if err != nil {
		return 0, err
	}
	return record.Revision, nil
}

func (repo *Repository) Update(ctx context.Context, snapshotID, oldRecordKey string, schema model.Schema, expectedRevision int64) (int64, error) {
	value, err := encode(schema)
	if err != nil {
		return 0, err
	}
	newKey := dataKey(snapshotID, KeyForFormKey(schema.Key))
	oldKey := dataKey(snapshotID, oldRecordKey)
	if oldRecordKey == newKey.RecordKey {
		updated, err := repo.store.Put(ctx, oldKey, value, expectedRevision)
		if err != nil {
			return 0, err
		}
		return updated.Revision, nil
	}
	if repo.store.MaxBatchSize() < 2 {
		return 0, core.ErrModuleDataLimitExceeded
	}
	if repo.store.MaxBatchBytes() < len(value.Payload)+len(value.IndexData) {
		return 0, core.ErrModuleDataLimitExceeded
	}
	if _, err := repo.store.Get(ctx, newKey); err == nil {
		return 0, core.ErrModuleDataConflict
	} else if !errors.Is(err, core.ErrModuleDataNotFound) {
		return 0, err
	}
	mutations := []core.ModuleDataMutation{
		{Key: oldKey, ExpectedRevision: expectedRevision},
		{Key: newKey, ExpectedRevision: 0, Value: &value},
	}
	if err := repo.store.Batch(ctx, mutations); err != nil {
		return 0, err
	}
	updated, err := repo.store.Get(ctx, newKey)
	if err != nil {
		return 0, err
	}
	return updated.Revision, nil
}

func (repo *Repository) List(ctx context.Context, snapshotID, after string, limit int) ([]Entry, string, error) {
	_, records, next, err := repo.store.List(ctx, namespace, scopeType, snapshotID, after, nil, limit)
	if err != nil {
		return nil, "", err
	}
	items := make([]Entry, 0, len(records))
	for _, record := range records {
		schema, err := decode(record)
		if err != nil {
			return nil, "", err
		}
		items = append(items, Entry{Schema: schema, Record: record})
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
	index, err := json.Marshal(map[string]any{"form_id": schema.ID, "revision": schema.Version})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	return core.ModuleDataValue{Payload: payload, PayloadVersion: schemapkg.PayloadVersion, IndexData: index}, nil
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
