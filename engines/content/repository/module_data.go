package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/content/domain"
)

const (
	namespace = "content/workspaces"
	scopeType     = "site"
	pageScopeType = "page_snapshot"
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
	if err != nil { return domain.StoredWorkspace{}, err }
	pageIDs, err := repo.listPageSelectionIDs(ctx, snapshotID)
	if err != nil { return domain.StoredWorkspace{}, err }
	aggregate.PageSelectionIDs = pageIDs
	aggregate.PageSelections = make(map[string][]domain.Selection, len(pageIDs))
	aggregate.PublishedPageSelections = make(map[string][]domain.Selection, len(pageIDs))
	aggregate.PageSelectionRevisions = make(map[string]int64, len(pageIDs))
	aggregate.PageMutationReceipts = make(map[string]*domain.MutationReceipt, len(pageIDs))
	for _, pageID := range pageIDs {
		selectionRecord, getErr := repo.store.Get(ctx, pageSelectionKey(snapshotID, pageID))
		if getErr != nil { return domain.StoredWorkspace{}, mapStoreError(getErr) }
		draft, published, receipt, decodeErr := decodePageSelections(pageID, selectionRecord)
		if decodeErr != nil { return domain.StoredWorkspace{}, decodeErr }
		aggregate.PageSelections[pageID] = draft
		aggregate.PublishedPageSelections[pageID] = published
		aggregate.PageSelectionRevisions[pageID] = selectionRecord.Revision
		aggregate.PageMutationReceipts[pageID] = receipt
	}
	return domain.StoredWorkspace{Aggregate: aggregate, StoreRevision: record.Revision}, nil
}

func (repo *Repository) Create(ctx context.Context, snapshotID string, aggregate domain.Aggregate) (domain.StoredWorkspace, error) {
	if aggregate.SnapshotID != snapshotID || !validAggregate(aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	aggregate.PageSelectionIDs = pageSelectionIDs(aggregate.PageSelections, aggregate.PublishedPageSelections)
	if !validPageSelectionIDs(aggregate.PageSelectionIDs) { return domain.StoredWorkspace{}, domain.ErrInvalid }
	if len(aggregate.PageSelectionIDs)+1 > repo.store.MaxBatchSize() { return domain.StoredWorkspace{}, domain.ErrTooLarge }
	aggregate.PageSelectionRevisions = make(map[string]int64, len(aggregate.PageSelectionIDs))
	aggregate.PageMutationReceipts = make(map[string]*domain.MutationReceipt, len(aggregate.PageSelectionIDs))
	value, err := encodeAggregate(aggregate)
	if err != nil { return domain.StoredWorkspace{}, err }
	mutations := []core.ModuleDataMutation{{Key: workspaceKey(snapshotID), ExpectedRevision: 0, Value: &value}}
	for _, pageID := range aggregate.PageSelectionIDs {
		selectionValue, encodeErr := encodePageSelections(pageID, aggregate.PageSelections[pageID], aggregate.PublishedPageSelections[pageID], nil)
		if encodeErr != nil { return domain.StoredWorkspace{}, encodeErr }
		mutations = append(mutations,
			core.ModuleDataMutation{Key: pageSelectionKey(snapshotID, pageID), ExpectedRevision: 0, Value: &selectionValue},
			core.ModuleDataMutation{Key: pageIndexKey(snapshotID, pageID), ExpectedRevision: 0, Value: pageIndexValue(pageID)})
	}
	if len(mutations) > repo.store.MaxBatchSize() { return domain.StoredWorkspace{}, domain.ErrTooLarge }
	if err := repo.store.Batch(ctx, mutations); err != nil { return domain.StoredWorkspace{}, mapStoreError(err) }
	for _, pageID := range aggregate.PageSelectionIDs {
		aggregate.PageSelectionRevisions[pageID] = 1
	}
	return domain.StoredWorkspace{Aggregate: aggregate, StoreRevision: 1}, nil
}

// Commit atomically replaces the current workspace and, when publishing, adds
// its immutable history item in the same snapshot scope.
func (repo *Repository) Commit(ctx context.Context, snapshotID string, expectedStoreRevision int64, aggregate domain.Aggregate, publication *domain.Publication) (domain.StoredWorkspace, error) {
	if aggregate.SnapshotID != snapshotID || expectedStoreRevision < 1 || !validAggregate(aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	aggregate.PageSelectionIDs = pageSelectionIDs(aggregate.PageSelections, aggregate.PublishedPageSelections)
	if !validPageSelectionIDs(aggregate.PageSelectionIDs) { return domain.StoredWorkspace{}, domain.ErrInvalid }
	value, err := encodeAggregate(aggregate)
	if err != nil { return domain.StoredWorkspace{}, err }
	mutations := []core.ModuleDataMutation{{Key: workspaceKey(snapshotID), ExpectedRevision: expectedStoreRevision, Value: &value}}
	changedIDs := aggregate.ChangedPageSelectionIDs
	if changedIDs == nil { changedIDs = aggregate.PageSelectionIDs }
	changedIDs = append([]string(nil), changedIDs...)
	sort.Strings(changedIDs)
	if len(changedIDs) > repo.store.MaxBatchSize()-len(mutations) { return domain.StoredWorkspace{}, domain.ErrTooLarge }
	seenChanged := make(map[string]struct{}, len(changedIDs))
	for _, pageID := range changedIDs {
		if !domain.ValidID(pageID) { return domain.StoredWorkspace{}, domain.ErrInvalid }
		if _, exists := seenChanged[pageID]; exists { return domain.StoredWorkspace{}, domain.ErrInvalid }
		seenChanged[pageID] = struct{}{}
		selectionKey := pageSelectionKey(snapshotID, pageID)
		expected := aggregate.PageSelectionRevisions[pageID]
		draft, draftExists := aggregate.PageSelections[pageID]
		published, publishedExists := aggregate.PublishedPageSelections[pageID]
		if !draftExists || !publishedExists { return domain.StoredWorkspace{}, domain.ErrInvalid }
		receipt := aggregate.PageMutationReceipts[pageID]
		selectionValue, encodeErr := encodePageSelections(pageID, draft, published, receipt)
		if encodeErr != nil { return domain.StoredWorkspace{}, encodeErr }
		mutations = append(mutations, core.ModuleDataMutation{Key: selectionKey, ExpectedRevision: expected, Value: &selectionValue})
		indexKey := pageIndexKey(snapshotID, pageID)
		_, indexErr := repo.store.Get(ctx, indexKey)
		if errors.Is(indexErr, core.ErrModuleDataNotFound) {
			mutations = append(mutations, core.ModuleDataMutation{Key: indexKey, ExpectedRevision: 0, Value: pageIndexValue(pageID)})
		} else if indexErr != nil { return domain.StoredWorkspace{}, mapStoreError(indexErr) }
		aggregate.PageSelectionRevisions[pageID] = expected + 1
	}
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
	if len(mutations) > repo.store.MaxBatchSize() { return domain.StoredWorkspace{}, domain.ErrTooLarge }
	if err := repo.store.Batch(ctx, mutations); err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	return domain.StoredWorkspace{Aggregate: aggregate, StoreRevision: expectedStoreRevision + 1}, nil
}

func (repo *Repository) CommitPageSelections(ctx context.Context, siteID, pageID string, expectedRevision int64, draft, published []domain.Selection, receipt *domain.MutationReceipt) (domain.StoredPageSelections, error) {
	if !domain.ValidID(siteID) || !domain.ValidID(pageID) || expectedRevision < 0 { return domain.StoredPageSelections{}, domain.ErrInvalid }
	value, err := encodePageSelections(pageID, draft, published, receipt)
	if err != nil { return domain.StoredPageSelections{}, err }
	mutations := []core.ModuleDataMutation{{Key: pageSelectionKey(siteID, pageID), ExpectedRevision: expectedRevision, Value: &value}}
	indexKey := pageIndexKey(siteID, pageID)
	_, indexErr := repo.store.Get(ctx, indexKey)
	if errors.Is(indexErr, core.ErrModuleDataNotFound) {
		mutations = append(mutations, core.ModuleDataMutation{Key: indexKey, ExpectedRevision: 0, Value: pageIndexValue(pageID)})
	} else if indexErr != nil { return domain.StoredPageSelections{}, mapStoreError(indexErr) }
	if len(mutations) > repo.store.MaxBatchSize() { return domain.StoredPageSelections{}, domain.ErrTooLarge }
	if err := repo.store.Batch(ctx, mutations); err != nil { return domain.StoredPageSelections{}, mapStoreError(err) }
	return domain.StoredPageSelections{Draft: draft, Published: published, StoreRevision: expectedRevision + 1, LastMutation: receipt}, nil
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
func pageSelectionKey(_ string, pageSnapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: pageScopeType, ScopeID: pageSnapshotID, RecordKey: "page-selections"}
}
func pageIndexKey(siteID, pageSnapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: siteID, RecordKey: "page-index/" + pageSnapshotID}
}
func pageIndexValue(pageSnapshotID string) *core.ModuleDataValue {
	value, _ := encodePayload(struct { PageSnapshotID string `json:"page_snapshot_id"` }{pageSnapshotID}, map[string]any{"record_type": "page-index", "content_revision": 0})
	return &value
}
func (repo *Repository) listPageSelectionIDs(ctx context.Context, siteID string) ([]string, error) {
	ids := []string{}
	after := ""
	for {
		keys, records, next, err := repo.store.List(ctx, namespace, scopeType, siteID, after,
			[]core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"page-index"}}}, repo.MaxPageSize())
		if err != nil { return nil, mapStoreError(err) }
		if len(keys) != len(records) { return nil, domain.ErrUnavailable }
		for i, key := range keys {
			const prefix = "page-index/"
			if len(key.RecordKey) <= len(prefix) || key.RecordKey[:len(prefix)] != prefix { return nil, domain.ErrUnavailable }
			pageID := key.RecordKey[len(prefix):]
			var payload struct { PageSnapshotID string `json:"page_snapshot_id"` }
			var index recordIndex
			if !domain.ValidID(pageID) || decodeStrict(records[i].Payload, &payload) != nil || payload.PageSnapshotID != pageID ||
				json.Unmarshal(records[i].IndexData, &index) != nil || index.RecordType != "page-index" || index.ContentRevision != 0 {
				return nil, domain.ErrUnavailable
			}
			ids = append(ids, pageID)
		}
		if next == "" { break }
		if next == after { return nil, domain.ErrUnavailable }
		after = next
	}
	sort.Strings(ids)
	return ids, nil
}
func pageSelectionIDs(draft, published map[string][]domain.Selection) []string {
	set := make(map[string]struct{}, len(draft)+len(published))
	for id := range draft { set[id] = struct{}{} }
	for id := range published { set[id] = struct{}{} }
	ids := make([]string, 0, len(set))
	for id := range set { ids = append(ids, id) }
	sort.Strings(ids)
	return ids
}
func encodePageSelections(pageSnapshotID string, draft, published []domain.Selection, receipt *domain.MutationReceipt) (core.ModuleDataValue, error) {
	if draft == nil { draft = []domain.Selection{} }
	if published == nil { published = []domain.Selection{} }
	return encodePayload(struct {
		Draft []domain.Selection `json:"draft"`
		Published []domain.Selection `json:"published"`
		LastMutation *domain.MutationReceipt `json:"last_mutation,omitempty"`
	}{draft, published, receipt}, map[string]any{"record_type": "page-selections", "content_revision": 0, "page_snapshot_id": pageSnapshotID})
}
func decodePageSelections(pageSnapshotID string, record core.ModuleDataRecord) ([]domain.Selection, []domain.Selection, *domain.MutationReceipt, error) {
	if record.PayloadVersion != domain.PayloadVersion { return nil, nil, nil, domain.ErrUnavailable }
	var payload struct {
		Draft []domain.Selection `json:"draft"`
		Published []domain.Selection `json:"published"`
		LastMutation *domain.MutationReceipt `json:"last_mutation,omitempty"`
	}
	if err := decodeStrict(record.Payload, &payload); err != nil || payload.Draft == nil || payload.Published == nil { return nil, nil, nil, domain.ErrUnavailable }
	var index struct {
		RecordType string `json:"record_type"`
		ContentRevision int64 `json:"content_revision"`
		PageSnapshotID string `json:"page_snapshot_id"`
	}
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "page-selections" || index.ContentRevision != 0 || index.PageSnapshotID != pageSnapshotID { return nil, nil, nil, domain.ErrUnavailable }
	return payload.Draft, payload.Published, payload.LastMutation, nil
}
func markerKey(snapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: copyKey}
}
func historyKeyForRevision(snapshotID string, revision int64) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: fmt.Sprintf("history/%020d", revision)}
}

func encodeAggregate(aggregate domain.Aggregate) (core.ModuleDataValue, error) {
	if !validAggregate(aggregate) { return core.ModuleDataValue{}, domain.ErrInvalid }
	stored := aggregate
	stored.Selections = []domain.Selection{}
	stored.PageSelections = nil
	if stored.PageSelectionIDs == nil { stored.PageSelectionIDs = []string{} }
	return encodePayload(stored, map[string]any{"record_type": "workspace", "content_revision": aggregate.PublishedRevision})
}

func decodeAggregate(snapshotID string, record core.ModuleDataRecord) (domain.Aggregate, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	var aggregate domain.Aggregate
	if err := decodeStrict(record.Payload, &aggregate); err != nil || !validAggregate(aggregate) || aggregate.SnapshotID != snapshotID || !validPageSelectionIDs(aggregate.PageSelectionIDs) {
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

func validPageSelectionIDs(ids []string) bool {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !domain.ValidID(id) { return false }
		if _, exists := seen[id]; exists { return false }
		seen[id] = struct{}{}
	}
	return true
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
