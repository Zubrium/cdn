package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/forms/features/sessions/model"
	sessionpkg "github.com/Zubrium/cdn/engines/forms/features/sessions/pkg"
)

const (
	namespace = "forms/sessions"
	scopeType = "global"
	scopeID   = "global"
)

type Store interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
	Delete(context.Context, core.ModuleDataKey, int64) error
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	ListExpired(context.Context, string, string, string, string, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	MaxPageSize() int
}

type Repository struct{ store Store }

func New(store Store) (*Repository, error) {
	if store == nil || store.MaxPageSize() < 1 {
		return nil, core.ErrModuleDataUnavailable
	}
	return &Repository{store: store}, nil
}

func TokenRecordKey(tokenHash string) string { return "visitor:" + tokenHash }

func (repo *Repository) Get(ctx context.Context, tokenHash string) (model.Stored, error) {
	recordKey := TokenRecordKey(tokenHash)
	record, err := repo.store.Get(ctx, dataKey(recordKey))
	if err != nil {
		return model.Stored{}, err
	}
	return decode(recordKey, record)
}

func (repo *Repository) Create(ctx context.Context, tokenHash string, session model.Session) (model.Stored, error) {
	value, err := encode(session)
	if err != nil {
		return model.Stored{}, err
	}
	recordKey := TokenRecordKey(tokenHash)
	record, err := repo.store.Put(ctx, dataKey(recordKey), value, 0)
	if err != nil {
		return model.Stored{}, err
	}
	return decode(recordKey, record)
}

func (repo *Repository) Update(ctx context.Context, stored model.Stored) (model.Stored, error) {
	if stored.Revision < 1 || stored.RecordKey == "" {
		return model.Stored{}, core.ErrModuleDataInvalid
	}
	value, err := encode(stored.Session)
	if err != nil {
		return model.Stored{}, err
	}
	record, err := repo.store.Put(ctx, dataKey(stored.RecordKey), value, stored.Revision)
	if err != nil {
		return model.Stored{}, err
	}
	return decode(stored.RecordKey, record)
}

func (repo *Repository) Delete(ctx context.Context, stored model.Stored) error {
	return repo.store.Delete(ctx, dataKey(stored.RecordKey), stored.Revision)
}

func (repo *Repository) ListByForm(ctx context.Context, formID, after string, limit int) ([]model.Stored, string, error) {
	keys, records, next, err := repo.store.List(ctx, namespace, scopeType, scopeID, after, []core.ModuleDataSelector{{
		Field: "form_id", Operator: core.SelectorEqual, Values: []any{formID},
	}}, limit)
	if err != nil {
		return nil, "", err
	}
	items, err := decodePage(keys, records)
	return items, next, err
}

func (repo *Repository) ListAll(ctx context.Context, after string, limit int) ([]model.Stored, string, error) {
	keys, records, next, err := repo.store.List(ctx, namespace, scopeType, scopeID, after, nil, limit)
	if err != nil {
		return nil, "", err
	}
	items, err := decodePage(keys, records)
	return items, next, err
}

func (repo *Repository) ListExpired(ctx context.Context, after string, limit int) ([]model.Stored, string, error) {
	keys, records, next, err := repo.store.ListExpired(ctx, namespace, scopeType, scopeID, after, limit)
	if err != nil {
		return nil, "", err
	}
	items, err := decodePage(keys, records)
	return items, next, err
}

func decodePage(keys []core.ModuleDataKey, records []core.ModuleDataRecord) ([]model.Stored, error) {
	if len(keys) != len(records) {
		return nil, core.ErrModuleDataUnavailable
	}
	items := make([]model.Stored, 0, len(records))
	for i := range records {
		item, err := decode(keys[i].RecordKey, records[i])
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func dataKey(recordKey string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: recordKey}
}

func encode(session model.Session) (core.ModuleDataValue, error) {
	if session.ID == "" || session.FormID == "" || session.SchemaVersion < 1 || session.ExpiresAt.IsZero() || len(session.Uploads) > model.MaxUploadsPerSession {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	uploads := make([]sessionpkg.Upload, len(session.Uploads))
	for i, upload := range session.Uploads {
		uploads[i] = sessionpkg.Upload{
			ID: upload.ID, FieldID: upload.FieldID, IdempotencyHash: upload.IdempotencyHash,
			ContentType: upload.ContentType, DeclaredSize: upload.DeclaredSize, MaxBytes: upload.MaxBytes,
			AccessGroup: upload.AccessGroup, SHA256: upload.SHA256, FileID: upload.FileID, Size: upload.Size, Completed: upload.Completed,
			Deleting: upload.Deleting,
		}
	}
	payload, err := json.Marshal(sessionpkg.PayloadV2{
		ID: session.ID, FormID: session.FormID, SchemaVersion: session.SchemaVersion, State: session.State,
		CreatedAt: session.CreatedAt.UTC(), ExpiresAt: session.ExpiresAt.UTC(), Uploads: uploads,
		SubmissionID: session.SubmissionID, SubmissionKeyHash: session.SubmissionKeyHash,
		SubmissionFingerprint: session.SubmissionFingerprint, UsedUploadIDs: append([]string(nil), session.UsedUploadIDs...),
		SubmissionValues: session.SubmissionValues, SubmissionAttachments: append([]string(nil), session.SubmissionAttachments...),
		SubmissionCreatedAt: session.SubmissionCreatedAt.UTC(),
	})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	index, err := json.Marshal(map[string]any{"form_id": session.FormID, "schema_version": session.SchemaVersion,
		"state": session.State})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	// Keep the record durable after visitor expiry so cleanup can always find
	// exact Core files and finish interrupted submission/deletion workflows.
	return core.ModuleDataValue{Payload: payload, PayloadVersion: sessionpkg.PayloadVersion, IndexData: index}, nil
}

func decode(recordKey string, record core.ModuleDataRecord) (model.Stored, error) {
	if record.PayloadVersion != sessionpkg.PayloadVersion || recordKey == "" {
		return model.Stored{}, core.ErrModuleDataUnavailable
	}
	var payload sessionpkg.PayloadV2
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	if err := decoder.Decode(&payload); err != nil || payload.ID == "" || payload.FormID == "" || payload.SchemaVersion < 1 ||
		(payload.State != model.StateActive && payload.State != model.StateSubmitting && payload.State != model.StateSubmitted && payload.State != model.StateCancelled) ||
		payload.ExpiresAt.IsZero() || payload.CreatedAt.IsZero() {
		return model.Stored{}, core.ErrModuleDataUnavailable
	}
	uploads := make([]model.Upload, len(payload.Uploads))
	seen := make(map[string]struct{}, len(payload.Uploads))
	for i, upload := range payload.Uploads {
		if upload.ID == "" || upload.FieldID == "" || upload.IdempotencyHash == "" || upload.ContentType == "" || upload.MaxBytes < 1 || upload.AccessGroup == "" || upload.DeclaredSize < -1 ||
			(upload.Completed && (upload.FileID == "" || upload.Size < 0 || !validSHA256(upload.SHA256))) || (!upload.Completed && (upload.FileID != "" || upload.SHA256 != "")) {
			return model.Stored{}, core.ErrModuleDataUnavailable
		}
		if upload.Deleting && !upload.Completed {
			return model.Stored{}, core.ErrModuleDataUnavailable
		}
		if _, exists := seen[upload.ID]; exists {
			return model.Stored{}, core.ErrModuleDataUnavailable
		}
		seen[upload.ID] = struct{}{}
		uploads[i] = model.Upload{
			ID: upload.ID, FieldID: upload.FieldID, IdempotencyHash: upload.IdempotencyHash,
			ContentType: upload.ContentType, DeclaredSize: upload.DeclaredSize, MaxBytes: upload.MaxBytes,
			AccessGroup: upload.AccessGroup, SHA256: upload.SHA256, FileID: upload.FileID, Size: upload.Size, Completed: upload.Completed,
			Deleting: upload.Deleting,
		}
	}
	for _, id := range payload.UsedUploadIDs {
		if _, exists := seen[id]; !exists {
			return model.Stored{}, core.ErrModuleDataUnavailable
		}
	}
	if (payload.State == model.StateSubmitting || payload.State == model.StateSubmitted) &&
		(payload.SubmissionID == "" || payload.SubmissionKeyHash == "" || payload.SubmissionFingerprint == "" ||
			payload.SubmissionValues == nil || payload.SubmissionCreatedAt.IsZero()) {
		return model.Stored{}, core.ErrModuleDataUnavailable
	}
	if payload.State == model.StateSubmitting || payload.State == model.StateSubmitted {
		if len(payload.SubmissionAttachments) != len(payload.UsedUploadIDs) {
			return model.Stored{}, core.ErrModuleDataUnavailable
		}
		for index, id := range payload.UsedUploadIDs {
			if id == "" || payload.SubmissionAttachments[index] == "" {
				return model.Stored{}, core.ErrModuleDataUnavailable
			}
		}
	} else if payload.SubmissionID != "" || payload.SubmissionKeyHash != "" || payload.SubmissionFingerprint != "" ||
		payload.SubmissionValues != nil || len(payload.SubmissionAttachments) != 0 || !payload.SubmissionCreatedAt.IsZero() || len(payload.UsedUploadIDs) != 0 {
		return model.Stored{}, core.ErrModuleDataUnavailable
	}
	return model.Stored{Session: model.Session{
		ID: payload.ID, FormID: payload.FormID, SchemaVersion: payload.SchemaVersion, State: payload.State,
		CreatedAt: payload.CreatedAt, ExpiresAt: payload.ExpiresAt, Uploads: uploads,
		SubmissionID: payload.SubmissionID, SubmissionKeyHash: payload.SubmissionKeyHash,
		SubmissionFingerprint: payload.SubmissionFingerprint, UsedUploadIDs: append([]string(nil), payload.UsedUploadIDs...),
		SubmissionValues: payload.SubmissionValues, SubmissionAttachments: append([]string(nil), payload.SubmissionAttachments...),
		SubmissionCreatedAt: payload.SubmissionCreatedAt,
	}, Revision: record.Revision, RecordKey: recordKey}, nil
}

func IsNotFound(err error) bool { return errors.Is(err, core.ErrModuleDataNotFound) }

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
