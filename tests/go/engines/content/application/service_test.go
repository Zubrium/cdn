package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/content/application"
	"github.com/Zubrium/cdn/engines/content/domain"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
)

func TestSaveReplaysGeneratedIDsAndRejectsSameKeyWithDifferentPayload(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	schema := readySchema(false)
	ctx := context.Background()
	initial, err := service.GetWorkspace(ctx, core.Session{}, schema, snapshotID)
	if err != nil || initial.Revision != 0 {
		t.Fatalf("GetWorkspace() = revision %d, err %v", initial.Revision, err)
	}
	request := contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-key-1", ExpectedRevision: 0,
		Records:    []contentapi.RecordInput{{ClientKey: "row-new", EntityID: entityID, SortOrder: 0, Values: map[string]any{"title": "One"}}},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{"row-new"}}},
	}
	first, err := service.SaveWorkspaceDraft(ctx, core.Session{}, schema, request)
	if err != nil || first.Revision != 1 || len(first.ClientRecordIDs) != 1 {
		t.Fatalf("SaveWorkspaceDraft() = %#v, err %v", first, err)
	}
	firstID := first.ClientRecordIDs["row-new"]
	if firstID == "" || first.Records[0].ID != firstID || first.Selections[0].RecordIDs[0] != firstID {
		t.Fatalf("save did not resolve the request-local client key: %#v", first)
	}
	replayed, err := service.SaveWorkspaceDraft(ctx, core.Session{}, schema, request)
	if err != nil || replayed.Revision != first.Revision || replayed.ClientRecordIDs["row-new"] != firstID {
		t.Fatalf("idempotent replay = %#v, err %v", replayed, err)
	}
	request.Records[0].Values["title"] = "Different"
	if _, err := service.SaveWorkspaceDraft(ctx, core.Session{}, schema, request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same key with different payload error = %v, want conflict", err)
	}
	request.Records[0].Values["title"] = "One"
	second, err := service.SaveWorkspaceDraft(ctx, core.Session{}, schema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-key-2", ExpectedRevision: first.Revision,
		Records: request.Records, Selections: request.Selections,
	})
	if err != nil || second.Revision != 2 {
		t.Fatalf("later SaveWorkspaceDraft() = %#v, err %v", second, err)
	}
	if _, err := service.SaveWorkspaceDraft(ctx, core.Session{}, schema, request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old idempotency key after a later mutation error = %v, want conflict", err)
	}
}

func TestPublishCreatesImmutableHistoryAndRestoreOnlyChangesDraft(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	schema := readySchema(false)
	if _, err := service.GetWorkspace(ctx, core.Session{}, schema, snapshotID); err != nil {
		t.Fatalf("GetWorkspace() error = %v", err)
	}
	savedA, err := service.SaveWorkspaceDraft(ctx, core.Session{}, schema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-a", ExpectedRevision: 0,
		Records:    []contentapi.RecordInput{{ClientKey: "a", EntityID: entityID, Values: map[string]any{"title": "A"}}},
		Selections: []contentapi.Selection{},
	})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}
	publishedA, err := service.PublishWorkspace(ctx, core.Session{}, schema, contentapi.PublishWorkspaceRequest{SnapshotID: snapshotID, IdempotencyKey: "publish-a", ExpectedRevision: savedA.Revision})
	if err != nil || publishedA.Revision != 2 || publishedA.PublishedRevision != 1 {
		t.Fatalf("Publish A = %#v, err %v", publishedA, err)
	}
	replayedPublish, err := service.PublishWorkspace(ctx, core.Session{}, schema, contentapi.PublishWorkspaceRequest{SnapshotID: snapshotID, IdempotencyKey: "publish-a", ExpectedRevision: savedA.Revision})
	if err != nil || replayedPublish.Revision != publishedA.Revision {
		t.Fatalf("idempotent Publish A = %#v, err %v", replayedPublish, err)
	}
	savedB, err := service.SaveWorkspaceDraft(ctx, core.Session{}, schema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-b", ExpectedRevision: publishedA.Revision,
		Records:    []contentapi.RecordInput{{ID: savedA.Records[0].ID, EntityID: entityID, Values: map[string]any{"title": "B"}}},
		Selections: []contentapi.Selection{},
	})
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}
	publishedB, err := service.PublishWorkspace(ctx, core.Session{}, schema, contentapi.PublishWorkspaceRequest{SnapshotID: snapshotID, IdempotencyKey: "publish-b", ExpectedRevision: savedB.Revision})
	if err != nil || publishedB.PublishedRevision != 2 {
		t.Fatalf("Publish B = %#v, err %v", publishedB, err)
	}
	history, err := service.ListHistory(ctx, schema, snapshotID, "", 10)
	if err != nil || len(history.Items) != 2 {
		t.Fatalf("ListHistory() = %#v, err %v", history, err)
	}
	var firstPublication domain.HistorySummary
	for _, item := range history.Items {
		if item.ContentRevision == 1 {
			firstPublication = item
		}
	}
	restored, err := service.RestoreHistory(ctx, core.Session{}, schema, contentapi.RestoreHistoryRequest{
		SnapshotID: snapshotID, HistoryID: firstPublication.ID, IdempotencyKey: "restore-a", ExpectedRevision: publishedB.Revision,
	})
	if err != nil || restored.Revision != 5 || restored.PublishedRevision != 2 || restored.Records[0].Values["title"] != "A" {
		t.Fatalf("RestoreHistory() = %#v, err %v", restored, err)
	}
	replayedRestore, err := service.RestoreHistory(ctx, core.Session{}, schema, contentapi.RestoreHistoryRequest{
		SnapshotID: snapshotID, HistoryID: firstPublication.ID, IdempotencyKey: "restore-a", ExpectedRevision: publishedB.Revision,
	})
	if err != nil || replayedRestore.Revision != restored.Revision {
		t.Fatalf("idempotent RestoreHistory() = %#v, err %v", replayedRestore, err)
	}
	public, err := service.ReadPublishedContent(ctx, schema, snapshotID)
	if err != nil || public.ContentRevision != 2 || public.Records[0].Values["title"] != "B" {
		t.Fatalf("ReadPublishedContent() = %#v, err %v", public, err)
	}
}

func TestCopySnapshotContentCopiesDraftAndHistoryAndReplaysReceipt(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	sourceSchema := readySchema(false)
	initial, err := service.GetWorkspace(ctx, core.Session{}, sourceSchema, snapshotID)
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	draft, err := service.SaveWorkspaceDraft(ctx, core.Session{}, sourceSchema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "copy-source-save-1", ExpectedRevision: initial.Revision,
		Records:    []contentapi.RecordInput{{ClientKey: "article", EntityID: entityID, SortOrder: 0, Values: map[string]any{"title": "Published"}}},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{"article"}}},
	})
	if err != nil {
		t.Fatalf("save source draft: %v", err)
	}
	published, err := service.PublishWorkspace(ctx, core.Session{}, sourceSchema, contentapi.PublishWorkspaceRequest{
		SnapshotID: snapshotID, IdempotencyKey: "copy-source-publish", ExpectedRevision: draft.Revision,
	})
	if err != nil {
		t.Fatalf("publish source: %v", err)
	}
	currentDraft, err := service.SaveWorkspaceDraft(ctx, core.Session{}, sourceSchema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "copy-source-save-2", ExpectedRevision: published.Revision,
		Records:    []contentapi.RecordInput{{ID: published.Records[0].ID, EntityID: entityID, SortOrder: 0, Values: map[string]any{"title": "Draft only"}}},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{published.Records[0].ID}}},
	})
	if err != nil {
		t.Fatalf("save changed source draft: %v", err)
	}

	targetID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	targetSchema := readySchema(false)
	targetSchema.State = "provisioning"
	request := contentapi.CopySnapshotContentRequest{
		SourceSnapshotID: snapshotID, TargetSnapshotID: targetID, ExpectedSourceRevision: currentDraft.Revision,
		IdempotencyKey: "copy-target-key",
	}
	first, err := service.CopySnapshotContent(ctx, core.Session{}, sourceSchema, targetSchema, request)
	if err != nil || first.TargetSnapshotID != targetID || first.SourceRevision != currentDraft.Revision || first.CopiedRecords != 1 || first.CopiedHistory != 1 {
		t.Fatalf("CopySnapshotContent() = %#v, err %v", first, err)
	}
	targetSchema.State = "ready"
	target, err := service.GetWorkspace(ctx, core.Session{}, targetSchema, targetID)
	if err != nil || target.Records[0].Values["title"] != "Draft only" || target.Selections[0].RecordIDs[0] != target.Records[0].ID {
		t.Fatalf("copied draft = %#v, err %v", target, err)
	}
	public, err := service.ReadPublishedContent(ctx, targetSchema, targetID)
	if err != nil || public.ContentRevision != 1 || public.Records[0].Values["title"] != "Published" {
		t.Fatalf("copied publication = %#v, err %v", public, err)
	}
	replayed, err := service.CopySnapshotContent(ctx, core.Session{}, sourceSchema, targetSchema, request)
	if err != nil || replayed != first {
		t.Fatalf("idempotent copy = %#v, err %v", replayed, err)
	}
}

func TestCopySnapshotContentRejectsStaleSourceBeforePublishingTargetWorkspace(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	sourceSchema := readySchema(false)
	initial, err := service.GetWorkspace(ctx, core.Session{}, sourceSchema, snapshotID)
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	if _, err := service.SaveWorkspaceDraft(ctx, core.Session{}, sourceSchema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "stale-source-save", ExpectedRevision: initial.Revision,
		Records: []contentapi.RecordInput{}, Selections: []contentapi.Selection{},
	}); err != nil {
		t.Fatalf("advance source revision: %v", err)
	}
	targetID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	targetSchema := readySchema(false)
	targetSchema.State = "provisioning"
	_, err = service.CopySnapshotContent(ctx, core.Session{}, sourceSchema, targetSchema, contentapi.CopySnapshotContentRequest{
		SourceSnapshotID: snapshotID, TargetSnapshotID: targetID, ExpectedSourceRevision: initial.Revision,
		IdempotencyKey: "stale-copy-key",
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale source copy error = %v, want conflict", err)
	}
	if _, err := repo.Get(ctx, targetID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stale copy exposed target workspace: err = %v", err)
	}
}

func TestCopySnapshotContentSupportsUntouchedSourceRevisionZero(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	sourceSchema := readySchema(false)
	source, err := service.GetWorkspace(ctx, core.Session{}, sourceSchema, snapshotID)
	if err != nil || source.Revision != 0 {
		t.Fatalf("initial workspace = %#v, err = %v; want revision zero", source, err)
	}
	targetID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	targetSchema := readySchema(false)
	targetSchema.State = "provisioning"
	result, err := service.CopySnapshotContent(ctx, core.Session{}, sourceSchema, targetSchema, contentapi.CopySnapshotContentRequest{
		SourceSnapshotID: snapshotID, TargetSnapshotID: targetID, ExpectedSourceRevision: 0, IdempotencyKey: "copy-empty-source-0001",
	})
	if err != nil || result.SourceRevision != 0 || result.TargetSnapshotID != targetID {
		t.Fatalf("copy empty revision-zero workspace = %#v, err = %v", result, err)
	}
}

const (
	snapshotID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	entityID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	fieldID    = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func readySchema(singleton bool) domain.Schema {
	cardinality := "many"
	if singleton {
		cardinality = "singleton"
	}
	raw := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"` + entityID + `","key":"article","name":"Article","cardinality":"` + cardinality + `","fields":[{"id":"` + fieldID + `","key":"title","name":"Title","type":"text"}]}]}`)
	model, err := domain.ParseDataModel(raw)
	if err != nil {
		panic(err)
	}
	return domain.Schema{
		State: "ready", DataModel: raw, Model: model,
		Placements: []domain.PlacementView{{ID: "placement-one", Inputs: []domain.PlacementInput{{InputKey: "articles", Kind: "collection", EntityID: entityID}}}},
	}
}

type inertFiles struct{}

func (inertFiles) StatFile(context.Context, core.Session, string) (core.File, error) {
	return core.File{}, core.ErrFileNotFound
}

type memoryRepository struct {
	workspaces map[string]domain.StoredWorkspace
	histories  map[string]map[string]domain.Publication
	markers    map[string]domain.StoredCopyMarker
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{workspaces: map[string]domain.StoredWorkspace{}, histories: map[string]map[string]domain.Publication{}, markers: map[string]domain.StoredCopyMarker{}}
}
func (*memoryRepository) MaxPageSize() int     { return 100 }
func (*memoryRepository) MaxPayloadBytes() int { return 1 << 20 }
func (repo *memoryRepository) Get(_ context.Context, snapshot string) (domain.StoredWorkspace, error) {
	value, exists := repo.workspaces[snapshot]
	if !exists {
		return domain.StoredWorkspace{}, domain.ErrNotFound
	}
	return cloneStored(value), nil
}
func (repo *memoryRepository) Create(_ context.Context, snapshot string, aggregate domain.Aggregate) (domain.StoredWorkspace, error) {
	if _, exists := repo.workspaces[snapshot]; exists {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	value := domain.StoredWorkspace{Aggregate: cloneAggregate(aggregate), StoreRevision: 1}
	repo.workspaces[snapshot] = value
	return cloneStored(value), nil
}
func (repo *memoryRepository) Commit(_ context.Context, snapshot string, expected int64, aggregate domain.Aggregate, publication *domain.Publication) (domain.StoredWorkspace, error) {
	current, exists := repo.workspaces[snapshot]
	if !exists || current.StoreRevision != expected {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	if publication != nil {
		if repo.histories[snapshot] == nil {
			repo.histories[snapshot] = map[string]domain.Publication{}
		}
		if _, exists := repo.histories[snapshot][publication.ID]; exists {
			return domain.StoredWorkspace{}, domain.ErrConflict
		}
		repo.histories[snapshot][publication.ID] = clonePublication(*publication)
	}
	stored := domain.StoredWorkspace{Aggregate: cloneAggregate(aggregate), StoreRevision: expected + 1}
	repo.workspaces[snapshot] = stored
	return cloneStored(stored), nil
}
func (repo *memoryRepository) GetHistoryByID(_ context.Context, snapshot, id string) (domain.Publication, error) {
	value, exists := repo.histories[snapshot][id]
	if !exists {
		return domain.Publication{}, domain.ErrNotFound
	}
	return clonePublication(value), nil
}
func (repo *memoryRepository) GetHistoryByContentRevision(_ context.Context, snapshot string, revision int64) (domain.Publication, error) {
	for _, value := range repo.histories[snapshot] {
		if value.ContentRevision == revision {
			return clonePublication(value), nil
		}
	}
	return domain.Publication{}, domain.ErrNotFound
}
func (repo *memoryRepository) ListHistory(_ context.Context, snapshot, after string, limit int) ([]domain.Publication, string, error) {
	items := make([]domain.Publication, 0, len(repo.histories[snapshot]))
	for _, value := range repo.histories[snapshot] {
		if fmt.Sprintf("history/%020d", value.ContentRevision) > after {
			items = append(items, clonePublication(value))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ContentRevision < items[j].ContentRevision })
	if len(items) > limit {
		return items[:limit], fmt.Sprintf("history/%020d", items[limit-1].ContentRevision), nil
	}
	return items, "", nil
}
func (repo *memoryRepository) GetCopyMarker(_ context.Context, target string) (domain.StoredCopyMarker, error) {
	marker, exists := repo.markers[target]
	if !exists {
		return domain.StoredCopyMarker{}, domain.ErrNotFound
	}
	return marker, nil
}
func (repo *memoryRepository) BeginCopy(_ context.Context, target string, marker domain.CopyMarker) (domain.StoredCopyMarker, error) {
	if _, exists := repo.markers[target]; exists {
		return domain.StoredCopyMarker{}, domain.ErrConflict
	}
	stored := domain.StoredCopyMarker{Marker: marker, StoreRevision: 1}
	repo.markers[target] = stored
	return stored, nil
}
func (repo *memoryRepository) PutCopiedHistory(_ context.Context, target string, publication domain.Publication) error {
	if repo.histories[target] == nil {
		repo.histories[target] = map[string]domain.Publication{}
	}
	if previous, exists := repo.histories[target][publication.ID]; exists {
		if !jsonEquivalent(previous, publication) {
			return domain.ErrConflict
		}
		return nil
	}
	repo.histories[target][publication.ID] = clonePublication(publication)
	return nil
}
func (repo *memoryRepository) CompleteCopy(_ context.Context, target string, markerRevision int64, aggregate domain.Aggregate, marker domain.CopyMarker) (domain.StoredWorkspace, error) {
	current := repo.markers[target]
	if current.StoreRevision != markerRevision || current.Marker.State != "copying" {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	if _, exists := repo.workspaces[target]; exists {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	current.Marker = marker
	current.StoreRevision++
	repo.markers[target] = current
	stored := domain.StoredWorkspace{Aggregate: cloneAggregate(aggregate), StoreRevision: 1}
	repo.workspaces[target] = stored
	return cloneStored(stored), nil
}

func cloneStored(value domain.StoredWorkspace) domain.StoredWorkspace {
	value.Aggregate = cloneAggregate(value.Aggregate)
	return value
}
func cloneAggregate(value domain.Aggregate) domain.Aggregate {
	value.Records = cloneRecords(value.Records)
	value.Selections = cloneSelections(value.Selections)
	if value.LastMutation != nil {
		receipt := *value.LastMutation
		receipt.ClientRecordIDs = cloneStringMap(receipt.ClientRecordIDs)
		value.LastMutation = &receipt
	}
	return value
}
func clonePublication(value domain.Publication) domain.Publication {
	value.Records = cloneRecords(value.Records)
	value.Selections = cloneSelections(value.Selections)
	return value
}
func cloneRecords(records []domain.Record) []domain.Record {
	result := make([]domain.Record, len(records))
	for index, record := range records {
		values := map[string]any{}
		for key, value := range record.Values {
			values[key] = value
		}
		record.Values = values
		result[index] = record
	}
	return result
}
func cloneSelections(selections []domain.Selection) []domain.Selection {
	result := make([]domain.Selection, len(selections))
	for index, selection := range selections {
		selection.RecordIDs = append([]string(nil), selection.RecordIDs...)
		result[index] = selection
	}
	return result
}
func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
func jsonEquivalent(a, b domain.Publication) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
