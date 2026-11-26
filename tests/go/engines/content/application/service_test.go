package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/engines/content/application"
	"github.com/Zubrium/cdn/engines/content/domain"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestSaveReplaysGeneratedIDsAndRejectsSameKeyWithDifferentPayload(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	schema := readySchema(false)
	ctx := context.Background()
	initial, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schema, snapshotID)
	if err != nil || initial.Revision != 0 {
		t.Fatalf("GetWorkspace() = revision %d, err %v", initial.Revision, err)
	}
	request := contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-key-1", ExpectedRevision: 0, ExpectedSelectionRevision: initial.SelectionRevision,
		Records:    []contentapi.RecordInput{{ClientKey: "row-new", EntityID: entityID, SortOrder: 0, Values: map[string]any{"title": "One"}}},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{"row-new"}}},
	}
	first, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schema, request)
	if err != nil || first.Revision != 1 || len(first.ClientRecordIDs) != 1 {
		t.Fatalf("SaveWorkspaceDraft() = %#v, err %v", first, err)
	}
	firstID := first.ClientRecordIDs["row-new"]
	if firstID == "" || first.Records[0].ID != firstID || first.Selections[0].RecordIDs[0] != firstID {
		t.Fatalf("save did not resolve the request-local client key: %#v", first)
	}
	replayed, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schema, request)
	if err != nil || replayed.Revision != first.Revision || replayed.ClientRecordIDs["row-new"] != firstID {
		t.Fatalf("idempotent replay = %#v, err %v", replayed, err)
	}
	request.Records[0].Values["title"] = "Different"
	if _, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schema, request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same key with different payload error = %v, want conflict", err)
	}
	request.Records[0].Values["title"] = "One"
	second, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-key-2", ExpectedRevision: first.Revision,
		ExpectedSelectionRevision: first.SelectionRevision, Records: request.Records, Selections: request.Selections,
	})
	if err != nil || second.Revision != 2 {
		t.Fatalf("later SaveWorkspaceDraft() = %#v, err %v", second, err)
	}
	if _, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schema, request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old idempotency key after a later mutation error = %v, want conflict", err)
	}
}

func TestPageSelectionWritesAreIndependentAndReplayWithinPageScope(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	schemaA := readySchema(false)
	schemaB := schemaA
	schemaB.PageSnapshotID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

	initialA, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schemaA, schemaA.PageSnapshotID)
	if err != nil {
		t.Fatalf("initialize page A: %v", err)
	}
	initialB, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schemaB, schemaB.PageSnapshotID)
	if err != nil {
		t.Fatalf("initialize page B: %v", err)
	}
	if initialA.Revision != initialB.Revision || initialA.Revision != 0 {
		t.Fatalf("global revision changed during page initialization: A=%d B=%d", initialA.Revision, initialB.Revision)
	}

	requestA := contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: schemaA.PageSnapshotID, IdempotencyKey: "page-a-selection", ExpectedRevision: initialA.Revision,
		ExpectedSelectionRevision: initialA.SelectionRevision, Records: []contentapi.RecordInput{},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "all"}},
	}
	firstA, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schemaA, requestA)
	if err != nil {
		t.Fatalf("save page A selection: %v", err)
	}
	requestB := contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: schemaB.PageSnapshotID, IdempotencyKey: "page-b-selection", ExpectedRevision: firstA.Revision,
		ExpectedSelectionRevision: initialB.SelectionRevision, Records: []contentapi.RecordInput{},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{}}},
	}
	firstB, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schemaB, requestB)
	if err != nil {
		t.Fatalf("page-scope B selection save conflicted: %v", err)
	}
	if firstA.Revision != initialA.Revision+1 || firstB.Revision != firstA.Revision+1 {
		t.Fatalf("global revision did not advance for page writes: A=%d B=%d initial=%d", firstA.Revision, firstB.Revision, initialA.Revision)
	}
	if firstA.SelectionRevision != initialA.SelectionRevision+1 || firstB.SelectionRevision != initialB.SelectionRevision+1 {
		t.Fatalf("page revisions did not advance independently: A=%d B=%d", firstA.SelectionRevision, firstB.SelectionRevision)
	}

	replayedA, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schemaA, requestA)
	if err != nil || replayedA.SelectionRevision != firstA.SelectionRevision || len(replayedA.Selections) != 1 || replayedA.Selections[0].Mode != "all" {
		t.Fatalf("page A idempotent replay after page B write = %#v, err %v", replayedA, err)
	}
	changedA := requestA
	changedA.Selections = []contentapi.Selection{}
	if _, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schemaA, changedA); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("page A reused idempotency key with changed payload: %v, want conflict", err)
	}
	staleA := requestA
	staleA.IdempotencyKey = "page-a-stale-selection"
	if _, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schemaA, staleA); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale same-page selection write: %v, want conflict", err)
	}

	latestA, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schemaA, schemaA.PageSnapshotID)
	if err != nil {
		t.Fatalf("read page A after writes: %v", err)
	}
	latestB, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schemaB, schemaB.PageSnapshotID)
	if err != nil {
		t.Fatalf("read page B after writes: %v", err)
	}
	if latestA.Selections[0].Mode != "all" || latestB.Selections[0].Mode != "ids" || latestA.Revision != latestB.Revision {
		t.Fatalf("page selection writes were not isolated: A=%#v B=%#v", latestA, latestB)
	}
}

func TestOldPagePublicationReplayDoesNotUseNewerGlobalPublication(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatal(err)
	}
	schemaA := readySchema(false)
	schemaB := schemaA
	schemaB.PageSnapshotID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	initialA, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schemaA, schemaA.PageSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	initialB, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schemaB, schemaB.PageSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	requestA := contentapi.PublishWorkspaceRequest{SnapshotID: schemaA.PageSnapshotID, IdempotencyKey: "publish-page-a", ExpectedRevision: initialA.Revision, ExpectedSelectionRevision: initialA.SelectionRevision}
	first, err := service.PublishWorkspace(ctx, contracts.AuthorizationSubject{}, schemaA, requestA)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PublishWorkspace(ctx, contracts.AuthorizationSubject{}, schemaB, contentapi.PublishWorkspaceRequest{
		SnapshotID: schemaB.PageSnapshotID, IdempotencyKey: "publish-page-b", ExpectedRevision: first.Revision, ExpectedSelectionRevision: initialB.SelectionRevision,
	})
	if err != nil || second.PublishedRevision <= first.PublishedRevision {
		t.Fatalf("publish B: %+v %v", second, err)
	}
	if replay, err := service.PublishWorkspace(ctx, contracts.AuthorizationSubject{}, schemaA, requestA); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old receipt returned newer publication: revision=%d err=%v", replay.PublishedRevision, err)
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
	initial, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, schema, snapshotID)
	if err != nil {
		t.Fatalf("GetWorkspace() error = %v", err)
	}
	savedA, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-a", ExpectedRevision: 0, ExpectedSelectionRevision: initial.SelectionRevision,
		Records:    []contentapi.RecordInput{{ClientKey: "a", EntityID: entityID, Values: map[string]any{"title": "A"}}},
		Selections: []contentapi.Selection{},
	})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}
	publishedA, err := service.PublishWorkspace(ctx, contracts.AuthorizationSubject{}, schema, contentapi.PublishWorkspaceRequest{SnapshotID: snapshotID, IdempotencyKey: "publish-a", ExpectedRevision: savedA.Revision, ExpectedSelectionRevision: savedA.SelectionRevision})
	if err != nil || publishedA.Revision != 2 || publishedA.PublishedRevision != 1 {
		t.Fatalf("Publish A = %#v, err %v", publishedA, err)
	}
	if repo.lastIntent == nil || repo.lastIntent.Locale != schema.Locale || repo.lastIntent.ContentRevision != 1 ||
		repo.lastIntent.SystemModelRevision != schema.ModelRevision || repo.lastIntent.WorkflowKey != contracts.ContentPublicationTriggerKey("publish-a") || repo.lastIntent.State != "pending" {
		t.Fatalf("publication intent = %+v", repo.lastIntent)
	}
	replayedPublish, err := service.PublishWorkspace(ctx, contracts.AuthorizationSubject{}, schema, contentapi.PublishWorkspaceRequest{SnapshotID: snapshotID, IdempotencyKey: "publish-a", ExpectedRevision: savedA.Revision, ExpectedSelectionRevision: savedA.SelectionRevision})
	if err != nil || replayedPublish.Revision != publishedA.Revision {
		t.Fatalf("idempotent Publish A = %#v, err %v", replayedPublish, err)
	}
	savedB, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, schema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "save-b", ExpectedRevision: publishedA.Revision,
		ExpectedSelectionRevision: publishedA.SelectionRevision, Records: []contentapi.RecordInput{{ID: savedA.Records[0].ID, EntityID: entityID, Values: map[string]any{"title": "B"}}},
		Selections: []contentapi.Selection{},
	})
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}
	publishedB, err := service.PublishWorkspace(ctx, contracts.AuthorizationSubject{}, schema, contentapi.PublishWorkspaceRequest{SnapshotID: snapshotID, IdempotencyKey: "publish-b", ExpectedRevision: savedB.Revision, ExpectedSelectionRevision: savedB.SelectionRevision})
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
	restored, err := service.RestoreHistory(ctx, contracts.AuthorizationSubject{}, schema, contentapi.RestoreHistoryRequest{
		SnapshotID: snapshotID, HistoryID: firstPublication.ID, IdempotencyKey: "restore-a", ExpectedRevision: publishedB.Revision,
		ExpectedSelectionRevision: publishedB.SelectionRevision,
	})
	if err != nil || restored.Revision != 5 || restored.PublishedRevision != 2 || restored.Records[0].Values["title"] != "A" {
		t.Fatalf("RestoreHistory() = %#v, err %v", restored, err)
	}
	replayedRestore, err := service.RestoreHistory(ctx, contracts.AuthorizationSubject{}, schema, contentapi.RestoreHistoryRequest{
		SnapshotID: snapshotID, HistoryID: firstPublication.ID, IdempotencyKey: "restore-a", ExpectedRevision: publishedB.Revision,
		ExpectedSelectionRevision: publishedB.SelectionRevision,
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
	initial, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, sourceSchema, snapshotID)
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	draft, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, sourceSchema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "copy-source-save-1", ExpectedRevision: initial.Revision,
		ExpectedSelectionRevision: initial.SelectionRevision, Records: []contentapi.RecordInput{{ClientKey: "article", EntityID: entityID, SortOrder: 0, Values: map[string]any{"title": "Published"}}},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{"article"}}},
	})
	if err != nil {
		t.Fatalf("save source draft: %v", err)
	}
	published, err := service.PublishWorkspace(ctx, contracts.AuthorizationSubject{}, sourceSchema, contentapi.PublishWorkspaceRequest{
		SnapshotID: snapshotID, IdempotencyKey: "copy-source-publish", ExpectedRevision: draft.Revision,
		ExpectedSelectionRevision: draft.SelectionRevision,
	})
	if err != nil {
		t.Fatalf("publish source: %v", err)
	}
	currentDraft, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, sourceSchema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "copy-source-save-2", ExpectedRevision: published.Revision,
		ExpectedSelectionRevision: published.SelectionRevision, Records: []contentapi.RecordInput{{ID: published.Records[0].ID, EntityID: entityID, SortOrder: 0, Values: map[string]any{"title": "Draft only"}}},
		Selections: []contentapi.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{published.Records[0].ID}}},
	})
	if err != nil {
		t.Fatalf("save changed source draft: %v", err)
	}

	targetID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	targetSchema := readySchema(false)
	targetSchema.State = "provisioning"
	targetSchema.PageSnapshotID = targetID
	request := contentapi.CopySnapshotContentRequest{
		SourceSnapshotID: snapshotID, TargetSnapshotID: targetID, ExpectedSourceRevision: currentDraft.Revision,
		ExpectedSourceSelectionRevision: currentDraft.SelectionRevision, IdempotencyKey: "copy-target-key", SelectionPolicy: contentapi.SelectionPolicyCopy,
	}
	first, err := service.CopySnapshotContent(ctx, contracts.AuthorizationSubject{}, sourceSchema, targetSchema, request)
	if err != nil || first.TargetSnapshotID != targetID || first.SourceRevision != currentDraft.Revision || first.CopiedRecords != 0 || first.CopiedHistory != 0 {
		t.Fatalf("CopySnapshotContent() = %#v, err %v", first, err)
	}
	targetSchema.State = "ready"
	target, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, targetSchema, targetID)
	if err != nil || target.Records[0].Values["title"] != "Draft only" || target.Selections[0].RecordIDs[0] != target.Records[0].ID {
		t.Fatalf("copied draft = %#v, err %v", target, err)
	}
	public, err := service.ReadPublishedContent(ctx, targetSchema, targetID)
	if err != nil || public.ContentRevision != 1 || public.Records[0].Values["title"] != "Published" {
		t.Fatalf("copied publication = %#v, err %v", public, err)
	}
	replayed, err := service.CopySnapshotContent(ctx, contracts.AuthorizationSubject{}, sourceSchema, targetSchema, request)
	if err != nil || replayed != first {
		t.Fatalf("idempotent copy = %#v, err %v", replayed, err)
	}
	resetWithSameKey := request
	resetWithSameKey.SelectionPolicy = contentapi.SelectionPolicyReset
	if _, err := service.CopySnapshotContent(ctx, contracts.AuthorizationSubject{}, sourceSchema, targetSchema, resetWithSameKey); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same idempotency key with different policy error = %v, want conflict", err)
	}
	reset := request
	reset.IdempotencyKey = "reset-target-key"
	reset.SelectionPolicy = contentapi.SelectionPolicyReset
	if _, err := service.CopySnapshotContent(ctx, contracts.AuthorizationSubject{}, sourceSchema, targetSchema, reset); err != nil {
		t.Fatalf("reset selections: %v", err)
	}
	resetWorkspace, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, targetSchema, targetID)
	if err != nil || resetWorkspace.Records[0].Values["title"] != "Draft only" || len(resetWorkspace.Selections) != 0 {
		t.Fatalf("reset target draft/workspace = %#v, err %v", resetWorkspace, err)
	}
	resetPublic, err := service.ReadPublishedContent(ctx, targetSchema, targetID)
	if err != nil || resetPublic.ContentRevision != public.ContentRevision || resetPublic.Records[0].Values["title"] != "Published" || len(resetPublic.Selections) != 0 {
		t.Fatalf("reset target publication = %#v, err %v", resetPublic, err)
	}
	history, _, err := repo.ListHistory(ctx, sourceSchema.Locale, "", 100)
	if err != nil || len(history) != 1 {
		t.Fatalf("shared publication history changed after reset: %d, err %v", len(history), err)
	}
}

func TestCopySnapshotContentRejectsMalformedSelectionPolicyBeforeMutation(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	sourceSchema := readySchema(false)
	targetID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	targetSchema := readySchema(false)
	targetSchema.State = "provisioning"
	targetSchema.PageSnapshotID = targetID
	_, err = service.CopySnapshotContent(context.Background(), contracts.AuthorizationSubject{}, sourceSchema, targetSchema, contentapi.CopySnapshotContentRequest{
		SourceSnapshotID: snapshotID, TargetSnapshotID: targetID, IdempotencyKey: "invalid-policy-key", SelectionPolicy: "merge",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("malformed selection policy error = %v, want invalid", err)
	}
	if len(repo.workspaces) != 0 {
		t.Fatalf("malformed policy mutated workspace: %#v", repo.workspaces)
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
	initial, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, sourceSchema, snapshotID)
	if err != nil {
		t.Fatalf("initialize source: %v", err)
	}
	if _, err := service.SaveWorkspaceDraft(ctx, contracts.AuthorizationSubject{}, sourceSchema, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: snapshotID, IdempotencyKey: "stale-source-save", ExpectedRevision: initial.Revision,
		ExpectedSelectionRevision: initial.SelectionRevision, Records: []contentapi.RecordInput{}, Selections: []contentapi.Selection{},
	}); err != nil {
		t.Fatalf("advance source revision: %v", err)
	}
	targetID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	targetSchema := readySchema(false)
	targetSchema.State = "provisioning"
	targetSchema.PageSnapshotID = targetID
	_, err = service.CopySnapshotContent(ctx, contracts.AuthorizationSubject{}, sourceSchema, targetSchema, contentapi.CopySnapshotContentRequest{
		SourceSnapshotID: snapshotID, TargetSnapshotID: targetID, ExpectedSourceRevision: initial.Revision,
		ExpectedSourceSelectionRevision: initial.SelectionRevision, IdempotencyKey: "stale-copy-key", SelectionPolicy: contentapi.SelectionPolicyCopy,
	})
	if !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "source revision mismatch") {
		t.Fatalf("stale source copy error = %v, want conflict", err)
	}
	latest, err := repo.Get(ctx, sourceSchema.Locale)
	if err != nil {
		t.Fatalf("read source workspace after stale copy: %v", err)
	}
	if _, exists := latest.PageSelections[targetID]; exists {
		t.Fatalf("stale copy exposed target page selections: %#v", latest.PageSelections)
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
	source, err := service.GetWorkspace(ctx, contracts.AuthorizationSubject{}, sourceSchema, snapshotID)
	if err != nil || source.Revision != 0 {
		t.Fatalf("initial workspace = %#v, err = %v; want revision zero", source, err)
	}
	targetID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	targetSchema := readySchema(false)
	targetSchema.State = "provisioning"
	targetSchema.PageSnapshotID = targetID
	result, err := service.CopySnapshotContent(ctx, contracts.AuthorizationSubject{}, sourceSchema, targetSchema, contentapi.CopySnapshotContentRequest{
		SourceSnapshotID: snapshotID, TargetSnapshotID: targetID, ExpectedSourceRevision: 0,
		ExpectedSourceSelectionRevision: source.SelectionRevision, IdempotencyKey: "copy-empty-source-0001", SelectionPolicy: contentapi.SelectionPolicyCopy,
	})
	if err != nil || result.SourceRevision != 0 || result.TargetSnapshotID != targetID {
		t.Fatalf("copy empty revision-zero workspace = %#v, err = %v", result, err)
	}
}

func TestLocaleConfigServiceValidatesAndDelegates(t *testing.T) {
	repo := newMemoryRepository()
	service, err := application.New(repo, inertFiles{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	request := domain.ReplaceLocaleConfigRequest{
		ExpectedRevision: 0, Locales: []string{"EN-us", "fr"}, DefaultLocale: "en-us", IdempotencyKey: "locale-key-1",
	}
	config, err := service.ReplaceLocaleConfig(ctx, request)
	if err != nil || config.Revision != 1 || config.DefaultLocale != "en-US" {
		t.Fatalf("ReplaceLocaleConfig() = %#v, err %v", config, err)
	}
	loaded, err := service.GetLocaleConfig(ctx)
	if err != nil || loaded.Revision != 1 || len(loaded.Locales) != 2 || loaded.Locales[0] != "en-US" {
		t.Fatalf("GetLocaleConfig() = %#v, err %v", loaded, err)
	}
	request.Locales = []string{"en-US", "EN-us"}
	if _, err := service.ReplaceLocaleConfig(ctx, request); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("duplicate canonical locale error = %v, want invalid", err)
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
		State: "ready", PageSnapshotID: snapshotID, Locale: "en-US", LocaleCatalogRevision: 1,
		ModelRevision: 1, ModelDigest: "model-digest", PageRevision: 1, PageDigest: "page-digest",
		DataModel: raw, Model: model,
		Placements: []domain.PlacementView{{ID: "placement-one", Inputs: []domain.PlacementInput{{InputKey: "articles", Kind: "collection", EntityID: entityID}}}},
	}
}

type inertFiles struct{}

func (inertFiles) StatFile(context.Context, contracts.AuthorizationSubject, string) (contracts.File, error) {
	return contracts.File{}, contracts.ErrFileNotFound
}

const memoryGlobalKey = "global"

type memoryRepository struct {
	workspaces   map[string]domain.StoredWorkspace
	histories    map[string]map[string]domain.Publication
	markers      map[string]domain.StoredCopyMarker
	localeConfig domain.LocaleConfig
	lastIntent   *domain.PublicationIntent
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{workspaces: map[string]domain.StoredWorkspace{}, histories: map[string]map[string]domain.Publication{}, markers: map[string]domain.StoredCopyMarker{}}
}
func (repo *memoryRepository) GetLocaleConfig(context.Context) (domain.LocaleConfig, error) {
	return repo.localeConfig, nil
}
func (repo *memoryRepository) ReplaceLocaleConfig(_ context.Context, request domain.ReplaceLocaleConfigRequest, _, _ string) (domain.LocaleConfig, error) {
	config, err := domain.NormalizeLocaleConfig(request, request.ExpectedRevision+1)
	if err != nil {
		return domain.LocaleConfig{}, err
	}
	repo.localeConfig = config
	return config, nil
}
func (*memoryRepository) MaxPageSize() int     { return 100 }
func (*memoryRepository) MaxPayloadBytes() int { return 1 << 20 }
func (repo *memoryRepository) Get(_ context.Context, locale string) (domain.StoredWorkspace, error) {
	value, exists := repo.workspaces[locale]
	if !exists {
		return domain.StoredWorkspace{}, domain.ErrNotFound
	}
	return cloneStored(value), nil
}
func (repo *memoryRepository) Create(_ context.Context, value domain.StoredWorkspace) (domain.StoredWorkspace, error) {
	locale := value.Aggregate.Locale
	if _, exists := repo.workspaces[locale]; exists {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	if value.PageSelections == nil {
		value.PageSelections = map[string]domain.StoredPageSelections{}
	}
	for pageID, page := range value.PageSelections {
		page.StoreRevision = 1
		value.PageSelections[pageID] = page
	}
	value.StoreRevision = 1
	repo.workspaces[locale] = cloneStored(value)
	return cloneStored(value), nil
}
func (repo *memoryRepository) Commit(_ context.Context, expected int64, value domain.StoredWorkspace, publication *domain.Publication) (domain.StoredWorkspace, error) {
	locale := value.Aggregate.Locale
	current, exists := repo.workspaces[locale]
	if !exists || current.StoreRevision != expected {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	if publication != nil {
		if value.PublicationIntent == nil {
			return domain.StoredWorkspace{}, domain.ErrInvalid
		}
		intent := *value.PublicationIntent
		repo.lastIntent = &intent
		if repo.histories[locale] == nil {
			repo.histories[locale] = map[string]domain.Publication{}
		}
		if _, exists := repo.histories[locale][publication.ID]; exists {
			return domain.StoredWorkspace{}, domain.ErrConflict
		}
		repo.histories[locale][publication.ID] = clonePublication(*publication)
	}
	for _, pageID := range value.ChangedPageSelectionIDs {
		page, ok := value.PageSelections[pageID]
		if !ok {
			return domain.StoredWorkspace{}, domain.ErrConflict
		}
		if page.StoreRevision != current.PageSelections[pageID].StoreRevision {
			return domain.StoredWorkspace{}, domain.ErrConflict
		}
		page.StoreRevision++
		value.PageSelections[pageID] = page
	}
	value.StoreRevision = expected + 1
	repo.workspaces[locale] = cloneStored(value)
	return cloneStored(value), nil
}
func (repo *memoryRepository) CommitPageSelections(_ context.Context, pageID, locale string, expected int64, draft, published []domain.Selection, receipt *domain.MutationReceipt) (domain.StoredPageSelections, error) {
	stored, exists := repo.workspaces[locale]
	if !exists {
		return domain.StoredPageSelections{}, domain.ErrNotFound
	}
	if stored.PageSelections == nil {
		stored.PageSelections = map[string]domain.StoredPageSelections{}
	}
	if stored.PageSelections[pageID].StoreRevision != expected {
		return domain.StoredPageSelections{}, domain.ErrConflict
	}
	page := domain.StoredPageSelections{Draft: cloneSelections(draft), Published: cloneSelections(published), StoreRevision: expected + 1, LastMutation: receipt}
	stored.PageSelections[pageID] = page
	repo.workspaces[locale] = cloneStored(stored)
	return page, nil
}
func (repo *memoryRepository) GetHistoryByID(_ context.Context, locale, id string) (domain.Publication, error) {
	value, exists := repo.histories[locale][id]
	if !exists {
		return domain.Publication{}, domain.ErrNotFound
	}
	return clonePublication(value), nil
}
func (repo *memoryRepository) GetHistoryByContentRevision(_ context.Context, locale string, revision int64) (domain.Publication, error) {
	for _, value := range repo.histories[locale] {
		if value.ContentRevision == revision {
			return clonePublication(value), nil
		}
	}
	return domain.Publication{}, domain.ErrNotFound
}
func (repo *memoryRepository) ListHistory(_ context.Context, locale, after string, limit int) ([]domain.Publication, string, error) {
	items := make([]domain.Publication, 0, len(repo.histories[locale]))
	for _, value := range repo.histories[locale] {
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
func (repo *memoryRepository) GetCopyMarker(_ context.Context) (domain.StoredCopyMarker, error) {
	marker, exists := repo.markers[memoryGlobalKey]
	if !exists {
		return domain.StoredCopyMarker{}, domain.ErrNotFound
	}
	return marker, nil
}
func (repo *memoryRepository) BeginCopy(_ context.Context, marker domain.CopyMarker) (domain.StoredCopyMarker, error) {
	if _, exists := repo.markers[memoryGlobalKey]; exists {
		return domain.StoredCopyMarker{}, domain.ErrConflict
	}
	stored := domain.StoredCopyMarker{Marker: marker, StoreRevision: 1}
	repo.markers[memoryGlobalKey] = stored
	return stored, nil
}
func (repo *memoryRepository) PutCopiedHistory(_ context.Context, publication domain.Publication) error {
	locale := publication.Locale
	if repo.histories[locale] == nil {
		repo.histories[locale] = map[string]domain.Publication{}
	}
	if previous, exists := repo.histories[locale][publication.ID]; exists {
		if !jsonEquivalent(previous, publication) {
			return domain.ErrConflict
		}
		return nil
	}
	repo.histories[locale][publication.ID] = clonePublication(publication)
	return nil
}
func (repo *memoryRepository) CompleteCopy(_ context.Context, markerRevision int64, value domain.StoredWorkspace, marker domain.CopyMarker) (domain.StoredWorkspace, error) {
	current := repo.markers[memoryGlobalKey]
	if current.StoreRevision != markerRevision || current.Marker.State != "copying" {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	locale := value.Aggregate.Locale
	if _, exists := repo.workspaces[locale]; exists {
		return domain.StoredWorkspace{}, domain.ErrConflict
	}
	current.Marker = marker
	current.StoreRevision++
	repo.markers[memoryGlobalKey] = current
	value.StoreRevision = 1
	repo.workspaces[locale] = cloneStored(value)
	return cloneStored(value), nil
}

func cloneStored(value domain.StoredWorkspace) domain.StoredWorkspace {
	value.Aggregate.Records = cloneRecords(value.Aggregate.Records)
	if value.Aggregate.LastMutation != nil {
		receipt := *value.Aggregate.LastMutation
		receipt.ClientRecordIDs = cloneStringMap(receipt.ClientRecordIDs)
		value.Aggregate.LastMutation = &receipt
	}
	sourcePages := value.PageSelections
	value.PageSelections = make(map[string]domain.StoredPageSelections, len(sourcePages))
	for pageID, page := range sourcePages {
		page.Draft = cloneSelections(page.Draft)
		page.Published = cloneSelections(page.Published)
		if page.LastMutation != nil {
			receipt := *page.LastMutation
			receipt.ClientRecordIDs = cloneStringMap(receipt.ClientRecordIDs)
			page.LastMutation = &receipt
		}
		value.PageSelections[pageID] = page
	}
	value.ChangedPageSelectionIDs = append([]string(nil), value.ChangedPageSelectionIDs...)
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
