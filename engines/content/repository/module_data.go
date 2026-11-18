package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/content/domain"
)

const (
	namespace = "content/workspaces"
	scopeType = "snapshot"
	rootKey   = "workspace"
	copyKey   = "copy"
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

type Repository struct {
	store Store
}

func New(store Store) (*Repository, error) {
	if store == nil || store.MaxPageSize() < 1 || store.MaxBatchSize() < 2 || store.MaxPayloadBytes() < 1 ||
		store.MaxIndexBytes() < 1 || store.MaxBatchBytes() < store.MaxPayloadBytes()+store.MaxIndexBytes() {
		return nil, domain.ErrUnavailable
	}
	return &Repository{store: store}, nil
}

func (repo *Repository) MaxPageSize() int {
	if repo == nil || repo.store == nil {
		return 0
	}
	return repo.store.MaxPageSize()
}

func (repo *Repository) MaxPayloadBytes() int {
	if repo == nil || repo.store == nil {
		return 0
	}
	return repo.store.MaxPayloadBytes()
}

func (repo *Repository) Get(ctx context.Context, snapshotID string) (domain.StoredWorkspace, error) {
	if repo == nil || repo.store == nil {
		return domain.StoredWorkspace{}, domain.ErrUnavailable
	}
	key := workspaceKey(snapshotID)
	record, err := repo.store.Get(ctx, key)
	if err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	aggregate, err := decodeAggregate(snapshotID, record)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	return domain.StoredWorkspace{Aggregate: aggregate, StoreRevision: record.Revision}, nil
}

func (repo *Repository) Create(ctx context.Context, snapshotID string, aggregate domain.Aggregate) (domain.StoredWorkspace, error) {
	if aggregate.SnapshotID != snapshotID || !validAggregate(aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	value, err := encodeAggregate(aggregate)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	record, err := repo.store.Put(ctx, workspaceKey(snapshotID), value, 0)
	if err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	return domain.StoredWorkspace{Aggregate: aggregate, StoreRevision: record.Revision}, nil
}

// Commit atomically replaces the current workspace and, when publishing, adds
// its immutable history item in the same snapshot scope.
func (repo *Repository) Commit(ctx context.Context, snapshotID string, expectedStoreRevision int64, aggregate domain.Aggregate, publication *domain.Publication) (domain.StoredWorkspace, error) {
	if aggregate.SnapshotID != snapshotID || expectedStoreRevision < 1 || !validAggregate(aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	value, err := encodeAggregate(aggregate)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	mutations := []core.ModuleDataMutation{{Key: workspaceKey(snapshotID), ExpectedRevision: expectedStoreRevision, Value: &value}}
	if publication != nil {
		if publication.ContentRevision != aggregate.PublishedRevision || publication.ID == "" || publication.ContentRevision < 1 || publication.DraftRevision < 0 {
			return domain.StoredWorkspace{}, domain.ErrInvalid
		}
		historyValue, err := encodePublication(*publication)
		if err != nil {
			return domain.StoredWorkspace{}, err
		}
		mutations = append(mutations, core.ModuleDataMutation{Key: historyKeyForRevision(snapshotID, publication.ContentRevision), ExpectedRevision: 0, Value: &historyValue})
	}
	if err := repo.store.Batch(ctx, mutations); err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	return domain.StoredWorkspace{Aggregate: aggregate, StoreRevision: expectedStoreRevision + 1}, nil
}

func (repo *Repository) GetHistoryByID(ctx context.Context, snapshotID, historyID string) (domain.Publication, error) {
	after := ""
	for {
		items, next, err := repo.ListHistory(ctx, snapshotID, after, repo.MaxPageSize())
		if err != nil {
			return domain.Publication{}, err
		}
		for _, item := range items {
			if item.ID == historyID {
				return item, nil
			}
		}
		if next == "" {
			return domain.Publication{}, domain.ErrNotFound
		}
		if next == after {
			return domain.Publication{}, domain.ErrUnavailable
		}
		after = next
	}
}

func (repo *Repository) GetHistoryByContentRevision(ctx context.Context, snapshotID string, revision int64) (domain.Publication, error) {
	if revision < 1 {
		return domain.Publication{}, domain.ErrInvalid
	}
	key := historyKeyForRevision(snapshotID, revision)
	record, err := repo.store.Get(ctx, key)
	if err != nil {
		return domain.Publication{}, mapStoreError(err)
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "history" || index.ContentRevision != revision {
		return domain.Publication{}, domain.ErrUnavailable
	}
	var publication domain.Publication
	if err := decodeStrict(record.Payload, &publication); err != nil {
		return domain.Publication{}, domain.ErrUnavailable
	}
	return decodePublication(publication.ID, record)
}

func (repo *Repository) ListHistory(ctx context.Context, snapshotID, after string, limit int) ([]domain.Publication, string, error) {
	if limit < 1 || limit > repo.MaxPageSize() || (after != "" && !validAfterCursor(after)) {
		return nil, "", domain.ErrInvalid
	}
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"history"}}}
	keys, records, next, err := repo.store.List(ctx, namespace, scopeType, snapshotID, after, selectors, limit)
	if err != nil {
		return nil, "", mapStoreError(err)
	}
	if len(keys) != len(records) {
		return nil, "", domain.ErrUnavailable
	}
	items := make([]domain.Publication, len(records))
	for index, record := range records {
		if !validHistoryKey(keys[index].RecordKey) {
			return nil, "", domain.ErrUnavailable
		}
		var payload domain.Publication
		if err := decodeStrict(record.Payload, &payload); err != nil {
			return nil, "", domain.ErrUnavailable
		}
		if keys[index].RecordKey != historyKeyForRevision(snapshotID, payload.ContentRevision).RecordKey {
			return nil, "", domain.ErrUnavailable
		}
		items[index], err = decodePublication(payload.ID, record)
		if err != nil {
			return nil, "", err
		}
	}
	return items, next, nil
}

func (repo *Repository) GetCopyMarker(ctx context.Context, snapshotID string) (domain.StoredCopyMarker, error) {
	record, err := repo.store.Get(ctx, markerKey(snapshotID))
	if err != nil {
		return domain.StoredCopyMarker{}, mapStoreError(err)
	}
	marker, err := decodeMarker(record)
	if err != nil {
		return domain.StoredCopyMarker{}, err
	}
	if marker.TargetSnapshotID != snapshotID {
		return domain.StoredCopyMarker{}, domain.ErrUnavailable
	}
	return domain.StoredCopyMarker{Marker: marker, StoreRevision: record.Revision}, nil
}

func (repo *Repository) BeginCopy(ctx context.Context, snapshotID string, marker domain.CopyMarker) (domain.StoredCopyMarker, error) {
	if marker.TargetSnapshotID != snapshotID || marker.State != "copying" || marker.SourceSnapshotID == "" || marker.KeyHash == "" || marker.Fingerprint == "" {
		return domain.StoredCopyMarker{}, domain.ErrInvalid
	}
	value, err := encodeMarker(marker)
	if err != nil {
		return domain.StoredCopyMarker{}, err
	}
	record, err := repo.store.Put(ctx, markerKey(snapshotID), value, 0)
	if err != nil {
		return domain.StoredCopyMarker{}, mapStoreError(err)
	}
	return domain.StoredCopyMarker{Marker: marker, StoreRevision: record.Revision}, nil
}

func (repo *Repository) PutCopiedHistory(ctx context.Context, snapshotID string, publication domain.Publication) error {
	key := historyKeyForRevision(snapshotID, publication.ContentRevision)
	expected, err := encodePublication(publication)
	if err != nil {
		return err
	}
	existing, err := repo.store.Get(ctx, key)
	if err == nil {
		actual, decodeErr := decodePublication(publication.ID, existing)
		if decodeErr != nil {
			return decodeErr
		}
		if reflect.DeepEqual(actual, publication) {
			return nil
		}
		return domain.ErrConflict
	}
	if !errors.Is(err, core.ErrModuleDataNotFound) {
		return mapStoreError(err)
	}
	if _, err := repo.store.Put(ctx, key, expected, 0); err != nil {
		if !errors.Is(err, core.ErrModuleDataConflict) {
			return mapStoreError(err)
		}
		existing, getErr := repo.store.Get(ctx, key)
		if getErr != nil {
			return mapStoreError(getErr)
		}
		actual, decodeErr := decodePublication(publication.ID, existing)
		if decodeErr != nil {
			return decodeErr
		}
		if !reflect.DeepEqual(actual, publication) {
			return domain.ErrConflict
		}
	}
	return nil
}

func (repo *Repository) CompleteCopy(ctx context.Context, snapshotID string, markerRevision int64, aggregate domain.Aggregate, marker domain.CopyMarker) (domain.StoredWorkspace, error) {
	if aggregate.SnapshotID != snapshotID || marker.TargetSnapshotID != snapshotID || marker.State != "complete" || marker.Result == nil || markerRevision < 1 || !validAggregate(aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	workspaceValue, err := encodeAggregate(aggregate)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	markerValue, err := encodeMarker(marker)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	if err := repo.store.Batch(ctx, []core.ModuleDataMutation{
		{Key: workspaceKey(snapshotID), ExpectedRevision: 0, Value: &workspaceValue},
		{Key: markerKey(snapshotID), ExpectedRevision: markerRevision, Value: &markerValue},
	}); err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	return domain.StoredWorkspace{Aggregate: aggregate, StoreRevision: 1}, nil
}

func workspaceKey(snapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: rootKey}
}
func markerKey(snapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: copyKey}
}
func historyKeyForRevision(snapshotID string, revision int64) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: fmt.Sprintf("history/%020d", revision)}
}

func encodeAggregate(aggregate domain.Aggregate) (core.ModuleDataValue, error) {
	if !validAggregate(aggregate) {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	return encodePayload(aggregate, map[string]any{"record_type": "workspace", "content_revision": aggregate.PublishedRevision})
}

func decodeAggregate(snapshotID string, record core.ModuleDataRecord) (domain.Aggregate, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	var aggregate domain.Aggregate
	if err := decodeStrict(record.Payload, &aggregate); err != nil || !validAggregate(aggregate) || aggregate.SnapshotID != snapshotID {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "workspace" || index.ContentRevision != aggregate.PublishedRevision {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	return aggregate, nil
}

func encodePublication(publication domain.Publication) (core.ModuleDataValue, error) {
	if publication.ID == "" || publication.DraftRevision < 0 || publication.ContentRevision < 1 || publication.CreatedAt.IsZero() || publication.Records == nil || publication.Selections == nil {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	return encodePayload(publication, map[string]any{"record_type": "history", "content_revision": publication.ContentRevision})
}

func decodePublication(id string, record core.ModuleDataRecord) (domain.Publication, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return domain.Publication{}, domain.ErrUnavailable
	}
	var publication domain.Publication
	if err := decodeStrict(record.Payload, &publication); err != nil || publication.ID != id || publication.ContentRevision < 1 || publication.CreatedAt.IsZero() || publication.Records == nil || publication.Selections == nil {
		return domain.Publication{}, domain.ErrUnavailable
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "history" || index.ContentRevision != publication.ContentRevision {
		return domain.Publication{}, domain.ErrUnavailable
	}
	return publication, nil
}

func encodeMarker(marker domain.CopyMarker) (core.ModuleDataValue, error) {
	if marker.SourceSnapshotID == "" || marker.TargetSnapshotID == "" || marker.SourceRevision < 0 || marker.KeyHash == "" || marker.Fingerprint == "" || (marker.State != "copying" && marker.State != "complete") {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	if marker.State == "complete" && marker.Result == nil {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	return encodePayload(marker, map[string]any{"record_type": "copy-marker", "content_revision": marker.SourceRevision})
}

func decodeMarker(record core.ModuleDataRecord) (domain.CopyMarker, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return domain.CopyMarker{}, domain.ErrUnavailable
	}
	var marker domain.CopyMarker
	if err := decodeStrict(record.Payload, &marker); err != nil || marker.SourceSnapshotID == "" || marker.TargetSnapshotID == "" || marker.KeyHash == "" || marker.Fingerprint == "" || (marker.State != "copying" && marker.State != "complete") || (marker.State == "complete" && marker.Result == nil) {
		return domain.CopyMarker{}, domain.ErrUnavailable
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "copy-marker" || index.ContentRevision != marker.SourceRevision {
		return domain.CopyMarker{}, domain.ErrUnavailable
	}
	return marker, nil
}

func encodePayload(payload any, index map[string]any) (core.ModuleDataValue, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	if len(encoded) > 0 && len(indexBytes) > 0 {
		return core.ModuleDataValue{Payload: encoded, PayloadVersion: domain.PayloadVersion, IndexData: indexBytes}, nil
	}
	return core.ModuleDataValue{}, domain.ErrInvalid
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing or malformed engine payload")
	}
	return nil
}

type recordIndex struct {
	RecordType      string `json:"record_type"`
	ContentRevision int64  `json:"content_revision"`
}

func validAggregate(aggregate domain.Aggregate) bool {
	return aggregate.SnapshotID != "" && aggregate.Revision >= 0 && aggregate.PublishedRevision >= 0 &&
		aggregate.Records != nil && aggregate.Selections != nil && (aggregate.LastMutation == nil ||
		(aggregate.LastMutation.Operation != "" && aggregate.LastMutation.KeyHash != "" && aggregate.LastMutation.Fingerprint != ""))
}

func validHistoryKey(value string) bool {
	return len(value) > len("history/") && value[:len("history/")] == "history/"
}
func validAfterCursor(value string) bool { return len(value) <= 256 && !stringsContainsControl(value) }
func stringsContainsControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrModuleDataNotFound):
		return domain.ErrNotFound
	case errors.Is(err, core.ErrModuleDataConflict), errors.Is(err, core.ErrRevisionConflict):
		return domain.ErrConflict
	case errors.Is(err, core.ErrModuleDataInvalid):
		return domain.ErrInvalid
	case errors.Is(err, core.ErrModuleDataLimitExceeded):
		return domain.ErrTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return domain.ErrUnavailable
	}
}
