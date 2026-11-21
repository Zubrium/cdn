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
	namespace     = "content/workspaces"
	scopeType     = "site"
	pageScopeType = "page_snapshot"
	rootKey       = "workspace"
	copyKey       = "copy"
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

func (repo *Repository) GetLocaleConfig(ctx context.Context, siteID string) (domain.LocaleConfig, error) {
	if repo == nil || repo.store == nil || !domain.ValidID(siteID) {
		return domain.LocaleConfig{}, domain.ErrInvalid
	}
	record, err := repo.store.Get(ctx, localeConfigKey(siteID))
	if err != nil {
		return domain.LocaleConfig{}, mapStoreError(err)
	}
	stored, err := decodeLocaleConfig(record)
	if err != nil {
		return domain.LocaleConfig{}, err
	}
	return stored.Config, nil
}

func (repo *Repository) ReplaceLocaleConfig(ctx context.Context, siteID string, request domain.ReplaceLocaleConfigRequest, keyHash, fingerprint string) (domain.LocaleConfig, error) {
	if repo == nil || repo.store == nil || !domain.ValidID(siteID) || request.ExpectedRevision < 0 || request.ExpectedRevision == int64(^uint64(0)>>1) || keyHash == "" || fingerprint == "" {
		return domain.LocaleConfig{}, domain.ErrInvalid
	}
	config, err := domain.NormalizeLocaleConfig(request, request.ExpectedRevision+1)
	if err != nil {
		return domain.LocaleConfig{}, err
	}
	key := localeConfigKey(siteID)
	current, getErr := repo.store.Get(ctx, key)
	if getErr == nil {
		stored, decodeErr := decodeLocaleConfig(current)
		if decodeErr != nil {
			return domain.LocaleConfig{}, decodeErr
		}
		if stored.KeyHash == keyHash {
			if stored.Fingerprint != fingerprint {
				return domain.LocaleConfig{}, domain.ErrConflict
			}
			return stored.Config, nil
		}
		if current.Revision != request.ExpectedRevision {
			return domain.LocaleConfig{}, domain.ErrConflict
		}
		// Until locale-scoped records/history and Router targets are auditable,
		// shrinking an established catalog cannot be proven safe.
		for _, existing := range stored.Config.Locales {
			if !containsLocale(config.Locales, existing) {
				return domain.LocaleConfig{}, domain.ErrConflict
			}
		}
	} else if !errors.Is(getErr, core.ErrModuleDataNotFound) {
		return domain.LocaleConfig{}, mapStoreError(getErr)
	} else if request.ExpectedRevision != 0 {
		return domain.LocaleConfig{}, domain.ErrConflict
	}
	value, err := encodePayload(localeConfigPayload{Config: config, KeyHash: keyHash, Fingerprint: fingerprint}, map[string]any{"record_type": "locale-catalog", "content_revision": config.Revision})
	if err != nil {
		return domain.LocaleConfig{}, err
	}
	if _, err := repo.store.Put(ctx, key, value, request.ExpectedRevision); err != nil {
		if errors.Is(err, core.ErrModuleDataConflict) || errors.Is(err, core.ErrRevisionConflict) {
			latest, latestErr := repo.store.Get(ctx, key)
			if latestErr == nil {
				stored, decodeErr := decodeLocaleConfig(latest)
				if decodeErr == nil && stored.KeyHash == keyHash && stored.Fingerprint == fingerprint {
					return stored.Config, nil
				}
			}
		}
		return domain.LocaleConfig{}, mapStoreError(err)
	}
	return config, nil
}

type localeConfigPayload struct {
	Config      domain.LocaleConfig `json:"config"`
	KeyHash     string              `json:"key_hash"`
	Fingerprint string              `json:"fingerprint"`
}

func decodeLocaleConfig(record core.ModuleDataRecord) (localeConfigPayload, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return localeConfigPayload{}, domain.ErrUnavailable
	}
	var stored localeConfigPayload
	if err := decodeStrict(record.Payload, &stored); err != nil || stored.Config.Revision < 1 || stored.KeyHash == "" || stored.Fingerprint == "" {
		return localeConfigPayload{}, domain.ErrUnavailable
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "locale-catalog" || index.ContentRevision != stored.Config.Revision || record.Revision != stored.Config.Revision {
		return localeConfigPayload{}, domain.ErrUnavailable
	}
	normalized, err := domain.NormalizeLocaleConfig(domain.ReplaceLocaleConfigRequest{
		ExpectedRevision: stored.Config.Revision - 1, Locales: stored.Config.Locales, DefaultLocale: stored.Config.DefaultLocale,
	}, stored.Config.Revision)
	if err != nil || !reflect.DeepEqual(normalized, stored.Config) {
		return localeConfigPayload{}, domain.ErrUnavailable
	}
	return stored, nil
}

func containsLocale(locales []string, locale string) bool {
	for _, candidate := range locales {
		if candidate == locale {
			return true
		}
	}
	return false
}

func localeConfigKey(siteID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: siteID, RecordKey: "locale-catalog"}
}

func (repo *Repository) Get(ctx context.Context, snapshotID, locale string) (domain.StoredWorkspace, error) {
	canonical, localeErr := domain.CanonicalLocale(locale)
	if repo == nil || repo.store == nil || !domain.ValidID(snapshotID) || localeErr != nil || canonical != locale {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	key := workspaceKey(snapshotID, locale)
	record, err := repo.store.Get(ctx, key)
	if err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	aggregate, err := decodeAggregate(snapshotID, record)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	if aggregate.Locale != locale {
		return domain.StoredWorkspace{}, domain.ErrUnavailable
	}
	pageIDs, err := repo.listPageSelectionIDs(ctx, snapshotID, locale)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	stored := domain.StoredWorkspace{
		Aggregate:      aggregate,
		StoreRevision:  record.Revision,
		PageSelections: make(map[string]domain.StoredPageSelections, len(pageIDs)),
	}
	for _, pageID := range pageIDs {
		selectionRecord, getErr := repo.store.Get(ctx, pageSelectionKey(snapshotID, pageID, locale))
		if getErr != nil {
			return domain.StoredWorkspace{}, mapStoreError(getErr)
		}
		draft, published, receipt, decodeErr := decodePageSelections(pageID, locale, selectionRecord)
		if decodeErr != nil {
			return domain.StoredWorkspace{}, decodeErr
		}
		stored.PageSelections[pageID] = domain.StoredPageSelections{
			Draft: draft, Published: published, StoreRevision: selectionRecord.Revision, LastMutation: receipt,
		}
	}
	return stored, nil
}

func (repo *Repository) Create(ctx context.Context, snapshotID string, stored domain.StoredWorkspace) (domain.StoredWorkspace, error) {
	if stored.Aggregate.SnapshotID != snapshotID || !validAggregate(stored.Aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	pageIDs := storedPageIDs(stored)
	if !validPageSelectionIDs(pageIDs) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	if len(pageIDs)*2+1 > repo.store.MaxBatchSize() {
		return domain.StoredWorkspace{}, domain.ErrTooLarge
	}
	value, err := encodeAggregate(stored.Aggregate)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	mutations := []core.ModuleDataMutation{{Key: workspaceKey(snapshotID, stored.Aggregate.Locale), ExpectedRevision: 0, Value: &value}}
	for _, pageID := range pageIDs {
		page := stored.PageSelections[pageID]
		selectionValue, encodeErr := encodePageSelections(pageID, stored.Aggregate.Locale, page.Draft, page.Published, page.LastMutation)
		if encodeErr != nil {
			return domain.StoredWorkspace{}, encodeErr
		}
		mutations = append(mutations,
			core.ModuleDataMutation{Key: pageSelectionKey(snapshotID, pageID, stored.Aggregate.Locale), ExpectedRevision: 0, Value: &selectionValue},
			core.ModuleDataMutation{Key: pageIndexKey(snapshotID, pageID, stored.Aggregate.Locale), ExpectedRevision: 0, Value: pageIndexValue(pageID, stored.Aggregate.Locale)})
	}
	if len(mutations) > repo.store.MaxBatchSize() {
		return domain.StoredWorkspace{}, domain.ErrTooLarge
	}
	if err := repo.store.Batch(ctx, mutations); err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	created := domain.StoredWorkspace{
		Aggregate:      stored.Aggregate,
		StoreRevision:  1,
		PageSelections: make(map[string]domain.StoredPageSelections, len(pageIDs)),
	}
	for _, pageID := range pageIDs {
		page := stored.PageSelections[pageID]
		page.StoreRevision = 1
		created.PageSelections[pageID] = page
	}
	return created, nil
}

// Commit atomically replaces the current workspace and, when publishing, adds
// its immutable history item in the same snapshot scope.
func (repo *Repository) Commit(ctx context.Context, snapshotID string, expectedStoreRevision int64, stored domain.StoredWorkspace, publication *domain.Publication) (domain.StoredWorkspace, error) {
	if stored.Aggregate.SnapshotID != snapshotID || expectedStoreRevision < 1 || !validAggregate(stored.Aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	if !validPageSelectionIDs(storedPageIDs(stored)) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	value, err := encodeAggregate(stored.Aggregate)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	mutations := []core.ModuleDataMutation{{Key: workspaceKey(snapshotID, stored.Aggregate.Locale), ExpectedRevision: expectedStoreRevision, Value: &value}}
	changedIDs := stored.ChangedPageSelectionIDs
	if changedIDs == nil {
		changedIDs = storedPageIDs(stored)
	}
	changedIDs = append([]string(nil), changedIDs...)
	sort.Strings(changedIDs)
	if len(changedIDs) > repo.store.MaxBatchSize()-len(mutations) {
		return domain.StoredWorkspace{}, domain.ErrTooLarge
	}
	updatedPages := make(map[string]domain.StoredPageSelections, len(changedIDs))
	seenChanged := make(map[string]struct{}, len(changedIDs))
	for _, pageID := range changedIDs {
		if !domain.ValidID(pageID) {
			return domain.StoredWorkspace{}, domain.ErrInvalid
		}
		if _, exists := seenChanged[pageID]; exists {
			return domain.StoredWorkspace{}, domain.ErrInvalid
		}
		seenChanged[pageID] = struct{}{}
		page, exists := stored.PageSelections[pageID]
		if !exists || page.StoreRevision < 0 {
			return domain.StoredWorkspace{}, domain.ErrInvalid
		}
		selectionValue, encodeErr := encodePageSelections(pageID, stored.Aggregate.Locale, page.Draft, page.Published, page.LastMutation)
		if encodeErr != nil {
			return domain.StoredWorkspace{}, encodeErr
		}
		mutations = append(mutations, core.ModuleDataMutation{Key: pageSelectionKey(snapshotID, pageID, stored.Aggregate.Locale), ExpectedRevision: page.StoreRevision, Value: &selectionValue})
		indexKey := pageIndexKey(snapshotID, pageID, stored.Aggregate.Locale)
		_, indexErr := repo.store.Get(ctx, indexKey)
		if errors.Is(indexErr, core.ErrModuleDataNotFound) {
			mutations = append(mutations, core.ModuleDataMutation{Key: indexKey, ExpectedRevision: 0, Value: pageIndexValue(pageID, stored.Aggregate.Locale)})
		} else if indexErr != nil {
			return domain.StoredWorkspace{}, mapStoreError(indexErr)
		}
		page.StoreRevision++
		updatedPages[pageID] = page
	}
	if publication != nil {
		if publication.ContentRevision != stored.Aggregate.PublishedRevision || publication.ID == "" || publication.ContentRevision < 1 || publication.DraftRevision < 0 {
			return domain.StoredWorkspace{}, domain.ErrInvalid
		}
		historyValue, err := encodePublication(*publication)
		if err != nil {
			return domain.StoredWorkspace{}, err
		}
		mutations = append(mutations, core.ModuleDataMutation{Key: historyKeyForRevision(snapshotID, publication.Locale, publication.ContentRevision), ExpectedRevision: 0, Value: &historyValue})
	}
	if len(mutations) > repo.store.MaxBatchSize() {
		return domain.StoredWorkspace{}, domain.ErrTooLarge
	}
	if err := repo.store.Batch(ctx, mutations); err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	committed := domain.StoredWorkspace{
		Aggregate:      stored.Aggregate,
		StoreRevision:  expectedStoreRevision + 1,
		PageSelections: make(map[string]domain.StoredPageSelections, len(stored.PageSelections)),
	}
	for pageID, page := range stored.PageSelections {
		if updated, exists := updatedPages[pageID]; exists {
			page = updated
		}
		committed.PageSelections[pageID] = page
	}
	return committed, nil
}

func (repo *Repository) CommitPageSelections(ctx context.Context, siteID, pageID, locale string, expectedRevision int64, draft, published []domain.Selection, receipt *domain.MutationReceipt) (domain.StoredPageSelections, error) {
	canonicalLocale, localeErr := domain.CanonicalLocale(locale)
	if !domain.ValidID(siteID) || !domain.ValidID(pageID) || expectedRevision < 0 || localeErr != nil || canonicalLocale != locale || expectedRevision == int64(^uint64(0)>>1) {
		return domain.StoredPageSelections{}, domain.ErrInvalid
	}
	value, err := encodePageSelections(pageID, locale, draft, published, receipt)
	if err != nil {
		return domain.StoredPageSelections{}, err
	}
	mutations := []core.ModuleDataMutation{{Key: pageSelectionKey(siteID, pageID, locale), ExpectedRevision: expectedRevision, Value: &value}}
	indexKey := pageIndexKey(siteID, pageID, locale)
	_, indexErr := repo.store.Get(ctx, indexKey)
	if errors.Is(indexErr, core.ErrModuleDataNotFound) {
		mutations = append(mutations, core.ModuleDataMutation{Key: indexKey, ExpectedRevision: 0, Value: pageIndexValue(pageID, locale)})
	} else if indexErr != nil {
		return domain.StoredPageSelections{}, mapStoreError(indexErr)
	}
	if len(mutations) > repo.store.MaxBatchSize() {
		return domain.StoredPageSelections{}, domain.ErrTooLarge
	}
	if err := repo.store.Batch(ctx, mutations); err != nil {
		return domain.StoredPageSelections{}, mapStoreError(err)
	}
	return domain.StoredPageSelections{Draft: draft, Published: published, StoreRevision: expectedRevision + 1, LastMutation: receipt}, nil
}

func (repo *Repository) GetHistoryByID(ctx context.Context, snapshotID, locale, historyID string) (domain.Publication, error) {
	after := ""
	for {
		items, next, err := repo.ListHistory(ctx, snapshotID, locale, after, repo.MaxPageSize())
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

func (repo *Repository) GetHistoryByContentRevision(ctx context.Context, snapshotID, locale string, revision int64) (domain.Publication, error) {
	if revision < 1 {
		return domain.Publication{}, domain.ErrInvalid
	}
	key := historyKeyForRevision(snapshotID, locale, revision)
	record, err := repo.store.Get(ctx, key)
	if err != nil {
		return domain.Publication{}, mapStoreError(err)
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "history" || index.ContentRevision != revision || index.Locale != locale {
		return domain.Publication{}, domain.ErrUnavailable
	}
	var publication domain.Publication
	if err := decodeStrict(record.Payload, &publication); err != nil {
		return domain.Publication{}, domain.ErrUnavailable
	}
	return decodePublication(publication.ID, record)
}

func (repo *Repository) ListHistory(ctx context.Context, snapshotID, locale, after string, limit int) ([]domain.Publication, string, error) {
	if limit < 1 || limit > repo.MaxPageSize() || (after != "" && !validAfterCursor(after)) {
		return nil, "", domain.ErrInvalid
	}
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"history"}}, {Field: "locale", Operator: core.SelectorEqual, Values: []any{locale}}}
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
		if keys[index].RecordKey != historyKeyForRevision(snapshotID, locale, payload.ContentRevision).RecordKey {
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
	key := historyKeyForRevision(snapshotID, publication.Locale, publication.ContentRevision)
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

func (repo *Repository) CompleteCopy(ctx context.Context, snapshotID string, markerRevision int64, stored domain.StoredWorkspace, marker domain.CopyMarker) (domain.StoredWorkspace, error) {
	if stored.Aggregate.SnapshotID != snapshotID || marker.TargetSnapshotID != snapshotID || marker.State != "complete" || marker.Result == nil || markerRevision < 1 || !validAggregate(stored.Aggregate) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	workspaceValue, err := encodeAggregate(stored.Aggregate)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	markerValue, err := encodeMarker(marker)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	if err := repo.store.Batch(ctx, []core.ModuleDataMutation{
		{Key: workspaceKey(snapshotID, stored.Aggregate.Locale), ExpectedRevision: 0, Value: &workspaceValue},
		{Key: markerKey(snapshotID), ExpectedRevision: markerRevision, Value: &markerValue},
	}); err != nil {
		return domain.StoredWorkspace{}, mapStoreError(err)
	}
	stored.StoreRevision = 1
	return stored, nil
}

func workspaceKey(snapshotID, locale string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: "workspace/" + locale}
}
func pageSelectionKey(_ string, pageSnapshotID, locale string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: pageScopeType, ScopeID: pageSnapshotID, RecordKey: "page-selections/" + locale}
}
func pageIndexKey(siteID, pageSnapshotID, locale string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: siteID, RecordKey: "page-index/" + locale + "/" + pageSnapshotID}
}
func pageIndexValue(pageSnapshotID, locale string) *core.ModuleDataValue {
	value, _ := encodePayload(struct {
		PageSnapshotID string `json:"page_snapshot_id"`
		Locale string `json:"locale"`
	}{pageSnapshotID, locale}, map[string]any{"record_type": "page-index", "content_revision": 0, "locale": locale})
	return &value
}
func (repo *Repository) listPageSelectionIDs(ctx context.Context, siteID, locale string) ([]string, error) {
	ids := []string{}
	after := ""
	for {
		keys, records, next, err := repo.store.List(ctx, namespace, scopeType, siteID, after,
			[]core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"page-index"}}, {Field: "locale", Operator: core.SelectorEqual, Values: []any{locale}}}, repo.MaxPageSize())
		if err != nil {
			return nil, mapStoreError(err)
		}
		if len(keys) != len(records) {
			return nil, domain.ErrUnavailable
		}
		for i, key := range keys {
			const prefix = "page-index/" + locale + "/"
			if len(key.RecordKey) <= len(prefix) || key.RecordKey[:len(prefix)] != prefix {
				return nil, domain.ErrUnavailable
			}
			pageID := key.RecordKey[len(prefix):]
			var payload struct {
				PageSnapshotID string `json:"page_snapshot_id"`
			}
			var index recordIndex
			if !domain.ValidID(pageID) || decodeStrict(records[i].Payload, &payload) != nil || payload.PageSnapshotID != pageID ||
				json.Unmarshal(records[i].IndexData, &index) != nil || index.RecordType != "page-index" || index.ContentRevision != 0 || index.Locale != locale {
				return nil, domain.ErrUnavailable
			}
			ids = append(ids, pageID)
		}
		if next == "" {
			break
		}
		if next == after {
			return nil, domain.ErrUnavailable
		}
		after = next
	}
	sort.Strings(ids)
	return ids, nil
}
func storedPageIDs(stored domain.StoredWorkspace) []string {
	ids := make([]string, 0, len(stored.PageSelections))
	for pageID := range stored.PageSelections {
		ids = append(ids, pageID)
	}
	sort.Strings(ids)
	return ids
}
func encodePageSelections(pageSnapshotID, locale string, draft, published []domain.Selection, receipt *domain.MutationReceipt) (core.ModuleDataValue, error) {
	if draft == nil {
		draft = []domain.Selection{}
	}
	if published == nil {
		published = []domain.Selection{}
	}
	return encodePayload(struct {
		Draft        []domain.Selection      `json:"draft"`
		Published    []domain.Selection      `json:"published"`
		LastMutation *domain.MutationReceipt `json:"last_mutation,omitempty"`
	}{draft, published, receipt}, map[string]any{"record_type": "page-selections", "content_revision": 0, "page_snapshot_id": pageSnapshotID, "locale": locale})
}
func decodePageSelections(pageSnapshotID, locale string, record core.ModuleDataRecord) ([]domain.Selection, []domain.Selection, *domain.MutationReceipt, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return nil, nil, nil, domain.ErrUnavailable
	}
	var payload struct {
		Draft        []domain.Selection      `json:"draft"`
		Published    []domain.Selection      `json:"published"`
		LastMutation *domain.MutationReceipt `json:"last_mutation,omitempty"`
	}
	if err := decodeStrict(record.Payload, &payload); err != nil || payload.Draft == nil || payload.Published == nil {
		return nil, nil, nil, domain.ErrUnavailable
	}
	var index struct {
		RecordType      string `json:"record_type"`
		ContentRevision int64  `json:"content_revision"`
		PageSnapshotID  string `json:"page_snapshot_id"`
			Locale          string `json:"locale"`
	}
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "page-selections" || index.ContentRevision != 0 || index.PageSnapshotID != pageSnapshotID || index.Locale != locale {
		return nil, nil, nil, domain.ErrUnavailable
	}
	return payload.Draft, payload.Published, payload.LastMutation, nil
}
func markerKey(snapshotID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: copyKey}
}
func historyKeyForRevision(snapshotID, locale string, revision int64) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: snapshotID, RecordKey: fmt.Sprintf("history/%s/%020d", locale, revision)}
}

func encodeAggregate(aggregate domain.Aggregate) (core.ModuleDataValue, error) {
	if !validAggregate(aggregate) {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	return encodePayload(aggregate, map[string]any{"record_type": "workspace", "content_revision": aggregate.PublishedRevision, "locale": aggregate.Locale})
}

func decodeAggregate(snapshotID string, record core.ModuleDataRecord) (domain.Aggregate, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	// Legacy workspace payloads also carried page-scoped fields. They are
	// intentionally ignored here: those values now live in dedicated records.
	var payload struct {
		domain.Aggregate
		Selections              json.RawMessage `json:"selections,omitempty"`
		PageSelections          json.RawMessage `json:"page_selections,omitempty"`
		PublishedPageSelections json.RawMessage `json:"published_page_selections,omitempty"`
		PageSelectionIDs        json.RawMessage `json:"page_selection_ids,omitempty"`
	}
	if err := decodeStrict(record.Payload, &payload); err != nil || !validAggregate(payload.Aggregate) || payload.SnapshotID != snapshotID {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "workspace" || index.ContentRevision != payload.PublishedRevision || index.Locale != payload.Locale {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	return payload.Aggregate, nil
}

func encodePublication(publication domain.Publication) (core.ModuleDataValue, error) {
	locale, localeErr := domain.CanonicalLocale(publication.Locale)
	if localeErr != nil || locale != publication.Locale || publication.ID == "" || publication.DraftRevision < 0 || publication.ContentRevision < 1 || publication.CreatedAt.IsZero() || publication.Records == nil || publication.Selections == nil {
		return core.ModuleDataValue{}, domain.ErrInvalid
	}
	for _, record := range publication.Records {
		if record.Locale != locale {
			return core.ModuleDataValue{}, domain.ErrInvalid
		}
	}
	return encodePayload(publication, map[string]any{"record_type": "history", "content_revision": publication.ContentRevision, "locale": publication.Locale})
}

func decodePublication(id string, record core.ModuleDataRecord) (domain.Publication, error) {
	if record.PayloadVersion != domain.PayloadVersion {
		return domain.Publication{}, domain.ErrUnavailable
	}
	var publication domain.Publication
	if err := decodeStrict(record.Payload, &publication); err != nil || publication.ID != id || publication.ContentRevision < 1 || publication.CreatedAt.IsZero() || publication.Records == nil || publication.Selections == nil {
		return domain.Publication{}, domain.ErrUnavailable
	}
	locale, localeErr := domain.CanonicalLocale(publication.Locale)
	if localeErr != nil || locale != publication.Locale {
		return domain.Publication{}, domain.ErrUnavailable
	}
	for _, contentRecord := range publication.Records {
		if contentRecord.Locale != locale {
			return domain.Publication{}, domain.ErrUnavailable
		}
	}
	var index recordIndex
	if err := json.Unmarshal(record.IndexData, &index); err != nil || index.RecordType != "history" || index.ContentRevision != publication.ContentRevision || index.Locale != locale {
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
	Locale          string `json:"locale,omitempty"`
}

func validPageSelectionIDs(ids []string) bool {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !domain.ValidID(id) {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func validAggregate(aggregate domain.Aggregate) bool {
	locale, err := domain.CanonicalLocale(aggregate.Locale)
	if err != nil || locale != aggregate.Locale {
		return false
	}
	for _, record := range aggregate.Records {
		if record.Locale != locale {
			return false
		}
	}
	return aggregate.SnapshotID != "" && aggregate.Revision >= 0 && aggregate.PublishedRevision >= 0 &&
		aggregate.Records != nil && (aggregate.LastMutation == nil ||
		(aggregate.LastMutation.Operation != "" && aggregate.LastMutation.KeyHash != "" && aggregate.LastMutation.Fingerprint != ""))
}

func validHistoryKey(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 3 || parts[0] != "history" {
		return false
	}
	locale, err := domain.CanonicalLocale(parts[1])
	if err != nil || locale != parts[1] || len(parts[2]) != 20 {
		return false
	}
	for _, digit := range parts[2] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
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
