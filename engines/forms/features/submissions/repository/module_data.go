package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/forms/features/submissions/model"
	submissionpkg "github.com/Zubrium/cdn/engines/forms/features/submissions/pkg"
)

const (
	namespace = "forms/submissions"
	scopeType = "submission"
)

type Store interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
	Delete(context.Context, core.ModuleDataKey, int64) error
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	MaxPageSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
}

type Entry struct {
	Submission                model.Submission
	Record                    core.ModuleDataRecord
	Attachments               []string
	DeletePending             bool
	DeleteStartedFromRevision int64
}

type Repository struct{ store Store }

func New(store Store) (*Repository, error) {
	if store == nil || store.MaxPageSize() < 1 || store.MaxPayloadBytes() < 1 || store.MaxIndexBytes() < 1 {
		return nil, core.ErrModuleDataUnavailable
	}
	return &Repository{store: store}, nil
}

func (repo *Repository) Get(ctx context.Context, snapshotID, id string) (Entry, error) {
	record, err := repo.store.Get(ctx, dataKey(snapshotID, id))
	if err != nil {
		return Entry{}, err
	}
	return decode(record)
}

func (repo *Repository) List(ctx context.Context, snapshotID, formID, state, after string, limit int) ([]Entry, string, error) {
	selectors := make([]core.ModuleDataSelector, 0, 2)
	if formID != "" {
		selectors = append(selectors, core.ModuleDataSelector{Field: "form_id", Operator: core.SelectorEqual, Values: []any{formID}})
	}
	if state != "" {
		selectors = append(selectors, core.ModuleDataSelector{Field: "state", Operator: core.SelectorEqual, Values: []any{state}})
	}
	_, records, next, err := repo.store.List(ctx, namespace, scopeType, snapshotID, after, selectors, limit)
	if err != nil {
		return nil, "", err
	}
	items := make([]Entry, 0, len(records))
	for _, record := range records {
		entry, err := decode(record)
		if err != nil {
			return nil, "", err
		}
		items = append(items, entry)
	}
	return items, next, nil
}

func (repo *Repository) SetState(ctx context.Context, snapshotID, id, state string, expectedRevision int64) (Entry, error) {
	entry, err := repo.Get(ctx, snapshotID, id)
	if err != nil {
		return Entry{}, err
	}
	if entry.DeletePending {
		return Entry{}, core.ErrModuleDataConflict
	}
	payload := submissionpkg.PayloadV1{
		ID: entry.Submission.ID, FormID: entry.Submission.FormID, State: state,
		Values: entry.Submission.Values, CreatedAt: entry.Submission.CreatedAt, AttachmentIDs: entry.Attachments,
	}
	value, err := encode(payload)
	if err != nil {
		return Entry{}, err
	}
	record, err := repo.store.Put(ctx, dataKey(snapshotID, id), value, expectedRevision)
	if err != nil {
		return Entry{}, err
	}
	return decode(record)
}

func (repo *Repository) BeginDelete(ctx context.Context, snapshotID, id string, expectedRevision int64) (Entry, error) {
	entry, err := repo.Get(ctx, snapshotID, id)
	if err != nil {
		return Entry{}, err
	}
	if entry.DeletePending {
		if expectedRevision != entry.DeleteStartedFromRevision && expectedRevision != entry.Record.Revision {
			return Entry{}, core.ErrModuleDataConflict
		}
		return entry, nil
	}
	if entry.Record.Revision != expectedRevision {
		return Entry{}, core.ErrModuleDataConflict
	}
	payload := submissionpkg.PayloadV1{
		ID: entry.Submission.ID, FormID: entry.Submission.FormID, State: entry.Submission.State,
		Values: entry.Submission.Values, CreatedAt: entry.Submission.CreatedAt, AttachmentIDs: entry.Attachments,
		DeletePending: true, DeleteStartedFromRevision: expectedRevision,
	}
	value, err := encode(payload)
	if err != nil {
		return Entry{}, err
	}
	record, err := repo.store.Put(ctx, dataKey(snapshotID, id), value, expectedRevision)
	if err != nil {
		return Entry{}, err
	}
	return decode(record)
}

func (repo *Repository) FinishDelete(ctx context.Context, snapshotID, id string, expectedRevision int64) error {
	return repo.store.Delete(ctx, dataKey(snapshotID, id), expectedRevision)
}

func dataKey(snapshotID, id string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: id}
}

func encode(payload submissionpkg.PayloadV1) (core.ModuleDataValue, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	index, err := json.Marshal(map[string]any{
		"form_id":    payload.FormID,
		"state":      payload.State,
		"created_at": payload.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return core.ModuleDataValue{}, core.ErrModuleDataInvalid
	}
	return core.ModuleDataValue{Payload: encoded, PayloadVersion: submissionpkg.PayloadVersion, IndexData: index}, nil
}

func decode(record core.ModuleDataRecord) (Entry, error) {
	if record.PayloadVersion != submissionpkg.PayloadVersion {
		return Entry{}, core.ErrModuleDataUnavailable
	}
	var payload submissionpkg.PayloadV1
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload.ID == "" || payload.FormID == "" || payload.State == "" {
		return Entry{}, core.ErrModuleDataUnavailable
	}
	if payload.Values == nil {
		payload.Values = map[string]any{}
	}
	seen := make(map[string]struct{}, len(payload.AttachmentIDs))
	for _, id := range payload.AttachmentIDs {
		if id == "" {
			return Entry{}, core.ErrModuleDataUnavailable
		}
		if _, exists := seen[id]; exists {
			return Entry{}, core.ErrModuleDataUnavailable
		}
		seen[id] = struct{}{}
	}
	if payload.DeletePending && payload.DeleteStartedFromRevision < 1 {
		return Entry{}, core.ErrModuleDataUnavailable
	}
	return Entry{
		Submission: model.Submission{ID: payload.ID, FormID: payload.FormID, State: payload.State, Values: payload.Values, CreatedAt: payload.CreatedAt},
		Record:     record, Attachments: append([]string(nil), payload.AttachmentIDs...), DeletePending: payload.DeletePending,
		DeleteStartedFromRevision: payload.DeleteStartedFromRevision,
	}, nil
}

func IsNotFound(err error) bool { return errors.Is(err, core.ErrModuleDataNotFound) }
