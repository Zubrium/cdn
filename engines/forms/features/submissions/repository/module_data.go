package repository

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Zubrium/cdn/engines/forms/features/submissions/model"
	submissionpkg "github.com/Zubrium/cdn/engines/forms/features/submissions/pkg"
	"time"
)

const (
	namespace = "forms/submissions"
	scopeType = "global"
	scopeID   = "global"
)

type Store interface {
	Get(context.Context, recorddata.RecordKey) (recorddata.Record, error)
	Put(context.Context, recorddata.RecordKey, recorddata.RecordValue, int64) (recorddata.Record, error)
	Delete(context.Context, recorddata.RecordKey, int64) error
	List(context.Context, string, string, string, string, []recorddata.RecordSelector, int) ([]recorddata.RecordKey, []recorddata.Record, string, error)
	MaxPageSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
}

type Repository struct{ store Store }

func New(store Store) (*Repository, error) {
	if store == nil || store.MaxPageSize() < 1 || store.MaxPayloadBytes() < 1 || store.MaxIndexBytes() < 1 {
		return nil, recorddata.ErrRecordUnavailable
	}
	return &Repository{store: store}, nil
}

func (repo *Repository) Get(ctx context.Context, id string) (model.StoredSubmission, error) {
	record, err := repo.store.Get(ctx, dataKey(id))
	if err != nil {
		return model.StoredSubmission{}, err
	}
	return decode(record)
}

func (repo *Repository) Create(ctx context.Context, payload submissionpkg.PayloadV1) (model.StoredSubmission, error) {
	value, err := encode(payload)
	if err != nil {
		return model.StoredSubmission{}, err
	}
	record, err := repo.store.Put(ctx, dataKey(payload.ID), value, 0)
	if errors.Is(err, recorddata.ErrRecordConflict) {
		existing, readErr := repo.Get(ctx, payload.ID)
		if readErr == nil && existing.Submission.IdempotencyKeyHash == payload.IdempotencyKeyHash &&
			existing.Submission.IdempotencyFingerprint == payload.IdempotencyFingerprint && existing.Submission.FormID == payload.FormID &&
			existing.Submission.SchemaVersion == payload.SchemaVersion {
			return existing, nil
		}
		if readErr != nil && !errors.Is(readErr, recorddata.ErrRecordNotFound) {
			return model.StoredSubmission{}, readErr
		}
		return model.StoredSubmission{}, recorddata.ErrRecordConflict
	}
	if err != nil {
		return model.StoredSubmission{}, err
	}
	return decode(record)
}

func (repo *Repository) List(ctx context.Context, formID, formKey, state, after string, limit int) ([]model.StoredSubmission, string, error) {
	selectors := []recorddata.RecordSelector{{Field: "form_id", Operator: recorddata.RecordSelectorEqual, Values: []any{formID}}}
	if formKey != "" {
		selectors = append(selectors, recorddata.RecordSelector{Field: "form_key", Operator: recorddata.RecordSelectorEqual, Values: []any{formKey}})
	}
	if state != "" {
		selectors = append(selectors, recorddata.RecordSelector{Field: "state", Operator: recorddata.RecordSelectorEqual, Values: []any{state}})
	}
	_, records, next, err := repo.store.List(ctx, namespace, scopeType, scopeID, after, selectors, limit)
	if err != nil {
		return nil, "", err
	}
	items := make([]model.StoredSubmission, 0, len(records))
	for _, record := range records {
		entry, err := decode(record)
		if err != nil {
			return nil, "", err
		}
		items = append(items, entry)
	}
	return items, next, nil
}

func (repo *Repository) SetState(ctx context.Context, id, state string, expectedRevision int64) (model.StoredSubmission, error) {
	entry, err := repo.Get(ctx, id)
	if err != nil {
		return model.StoredSubmission{}, err
	}
	if entry.DeletePending {
		return model.StoredSubmission{}, recorddata.ErrRecordConflict
	}
	payload := submissionpkg.PayloadV1{
		ID: entry.Submission.ID, FormID: entry.Submission.FormID, FormKey: entry.Submission.FormKey,
		SchemaVersion: entry.Submission.SchemaVersion, State: state,
		Values: entry.Submission.Values, CreatedAt: entry.Submission.CreatedAt,
		IdempotencyKeyHash: entry.Submission.IdempotencyKeyHash, IdempotencyFingerprint: entry.Submission.IdempotencyFingerprint,
		AttachmentIDs: entry.Attachments,
	}
	value, err := encode(payload)
	if err != nil {
		return model.StoredSubmission{}, err
	}
	record, err := repo.store.Put(ctx, dataKey(id), value, expectedRevision)
	if err != nil {
		return model.StoredSubmission{}, err
	}
	return decode(record)
}

func (repo *Repository) BeginDelete(ctx context.Context, id string, expectedRevision int64) (model.StoredSubmission, error) {
	entry, err := repo.Get(ctx, id)
	if err != nil {
		return model.StoredSubmission{}, err
	}
	if entry.DeletePending {
		if expectedRevision != entry.DeleteStartedFromRevision && expectedRevision != entry.Revision {
			return model.StoredSubmission{}, recorddata.ErrRecordConflict
		}
		return entry, nil
	}
	if entry.Revision != expectedRevision {
		return model.StoredSubmission{}, recorddata.ErrRecordConflict
	}
	payload := submissionpkg.PayloadV1{
		ID: entry.Submission.ID, FormID: entry.Submission.FormID, FormKey: entry.Submission.FormKey,
		SchemaVersion: entry.Submission.SchemaVersion, State: entry.Submission.State,
		Values: entry.Submission.Values, CreatedAt: entry.Submission.CreatedAt,
		IdempotencyKeyHash: entry.Submission.IdempotencyKeyHash, IdempotencyFingerprint: entry.Submission.IdempotencyFingerprint,
		AttachmentIDs: entry.Attachments,
		DeletePending: true, DeleteStartedFromRevision: expectedRevision,
	}
	value, err := encode(payload)
	if err != nil {
		return model.StoredSubmission{}, err
	}
	record, err := repo.store.Put(ctx, dataKey(id), value, expectedRevision)
	if err != nil {
		return model.StoredSubmission{}, err
	}
	return decode(record)
}

func (repo *Repository) FinishDelete(ctx context.Context, id string, expectedRevision int64) error {
	return repo.store.Delete(ctx, dataKey(id), expectedRevision)
}

func dataKey(id string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: id}
}

func encode(payload submissionpkg.PayloadV1) (recorddata.RecordValue, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return recorddata.RecordValue{}, recorddata.ErrRecordInvalid
	}
	index, err := json.Marshal(map[string]any{
		"form_id":        payload.FormID,
		"form_key":       payload.FormKey,
		"schema_version": payload.SchemaVersion,
		"state":          payload.State,
		"created_at":     payload.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return recorddata.RecordValue{}, recorddata.ErrRecordInvalid
	}
	return recorddata.RecordValue{Payload: encoded, PayloadVersion: submissionpkg.PayloadVersion, IndexData: index}, nil
}

func decode(record recorddata.Record) (model.StoredSubmission, error) {
	if record.PayloadVersion != submissionpkg.PayloadVersion {
		return model.StoredSubmission{}, recorddata.ErrRecordUnavailable
	}
	var payload submissionpkg.PayloadV1
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload.ID == "" || payload.FormID == "" || payload.SchemaVersion < 1 || payload.State == "" {
		return model.StoredSubmission{}, recorddata.ErrRecordUnavailable
	}
	if payload.Values == nil {
		payload.Values = map[string]any{}
	}
	if payload.CreatedAt.IsZero() && !record.CreatedAt.IsZero() {
		payload.CreatedAt = record.CreatedAt.UTC()
	}
	seen := make(map[string]struct{}, len(payload.AttachmentIDs))
	for _, id := range payload.AttachmentIDs {
		if id == "" {
			return model.StoredSubmission{}, recorddata.ErrRecordUnavailable
		}
		if _, exists := seen[id]; exists {
			return model.StoredSubmission{}, recorddata.ErrRecordUnavailable
		}
		seen[id] = struct{}{}
	}
	if payload.DeletePending && payload.DeleteStartedFromRevision < 1 {
		return model.StoredSubmission{}, recorddata.ErrRecordUnavailable
	}
	return model.StoredSubmission{
		Submission: model.Submission{ID: payload.ID, FormID: payload.FormID, FormKey: payload.FormKey,
			SchemaVersion: payload.SchemaVersion, State: payload.State, Values: payload.Values, CreatedAt: payload.CreatedAt,
			IdempotencyKeyHash: payload.IdempotencyKeyHash, IdempotencyFingerprint: payload.IdempotencyFingerprint},
		Revision: record.Revision, Attachments: append([]string(nil), payload.AttachmentIDs...), DeletePending: payload.DeletePending,
		DeleteStartedFromRevision: payload.DeleteStartedFromRevision,
	}, nil
}

func IsNotFound(err error) bool { return errors.Is(err, recorddata.ErrRecordNotFound) }
