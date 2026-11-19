package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/content/domain"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
)

type Files interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
}

type Repository interface {
	Get(context.Context, string) (domain.StoredWorkspace, error)
	Create(context.Context, string, domain.Aggregate) (domain.StoredWorkspace, error)
	Commit(context.Context, string, int64, domain.Aggregate, *domain.Publication) (domain.StoredWorkspace, error)
	CommitPageSelections(context.Context, string, string, int64, []domain.Selection, []domain.Selection, *domain.MutationReceipt) (domain.StoredPageSelections, error)
	GetHistoryByID(context.Context, string, string) (domain.Publication, error)
	GetHistoryByContentRevision(context.Context, string, int64) (domain.Publication, error)
	ListHistory(context.Context, string, string, int) ([]domain.Publication, string, error)
	GetCopyMarker(context.Context, string) (domain.StoredCopyMarker, error)
	BeginCopy(context.Context, string, domain.CopyMarker) (domain.StoredCopyMarker, error)
	PutCopiedHistory(context.Context, string, domain.Publication) error
	CompleteCopy(context.Context, string, int64, domain.Aggregate, domain.CopyMarker) (domain.StoredWorkspace, error)
	MaxPageSize() int
	MaxPayloadBytes() int
}

type Service struct {
	repository Repository
	files      Files
	maxPage    int
	maxPayload int
}

func New(repository Repository, files Files) (*Service, error) {
	if repository == nil || files == nil || repository.MaxPageSize() < 1 || repository.MaxPayloadBytes() < 1 {
		return nil, domain.ErrUnavailable
	}
	return &Service{repository: repository, files: files, maxPage: repository.MaxPageSize(), maxPayload: repository.MaxPayloadBytes()}, nil
}

func (service *Service) GetWorkspace(ctx context.Context, session core.Session, schema domain.Schema, snapshotID string) (domain.WorkspaceView, error) {
	if err := ctx.Err(); err != nil {
		return domain.WorkspaceView{}, err
	}
	if err := service.validateSchema(schema, snapshotID, "ready"); err != nil {
		return domain.WorkspaceView{}, err
	}
	stored, err := service.loadOrInitialize(ctx, schema, snapshotID)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	data := workspaceData(stored.Aggregate)
	if err := domain.ValidateWorkspace(schema.Model, validationPlacements(schema.Placements), data, &data, false); err != nil {
		return domain.WorkspaceView{}, domain.ErrUnavailable
	}
	if err := service.validateMediaReferences(ctx, session, schema.Model, stored.Aggregate.Records); err != nil {
		return domain.WorkspaceView{}, err
	}
	return view(stored.Aggregate, schema, nil), nil
}

func (service *Service) SaveWorkspaceDraft(ctx context.Context, session core.Session, schema domain.Schema, request contentapi.SaveWorkspaceDraftRequest) (domain.WorkspaceView, error) {
	if err := ctx.Err(); err != nil {
		return domain.WorkspaceView{}, err
	}
	if err := service.validateSchema(schema, request.SnapshotID, "ready"); err != nil {
		return domain.WorkspaceView{}, err
	}
	if request.ExpectedRevision < 0 || request.ExpectedSelectionRevision < 0 || !domain.ValidID(request.SnapshotID) || request.Records == nil || request.Selections == nil {
		return domain.WorkspaceView{}, domain.ErrInvalid
	}
	keyHash, fingerprint, err := mutationIdentity("save", request.IdempotencyKey, struct {
		ExpectedRevision          int64                    `json:"expected_revision"`
		ExpectedSelectionRevision int64                    `json:"expected_selection_revision"`
		Records                   []contentapi.RecordInput `json:"records"`
		Selections                []contentapi.Selection   `json:"selections"`
	}{request.ExpectedRevision, request.ExpectedSelectionRevision, request.Records, request.Selections}, service.maxPayload)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	stored, err := service.loadOrInitialize(ctx, schema, request.SnapshotID)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	if result, replayed, replayErr := service.replay(ctx, session, stored, schema, "save", keyHash, fingerprint); replayed || replayErr != nil {
		return result, replayErr
	}
	if stored.Aggregate.Revision != request.ExpectedRevision {
		return domain.WorkspaceView{}, domain.ErrConflict
	}
	data, clientRecordIDs, err := normalizeDraftRequest(request, stored.Aggregate)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	previous := workspaceData(stored.Aggregate)
	data.Selections = dropDeletedRecordSelections(data.Selections, previous, data.Records, schema.Placements)
	if err := domain.ValidateWorkspace(schema.Model, validationPlacements(schema.Placements), data, &previous, false); err != nil {
		return domain.WorkspaceView{}, err
	}
	if err := service.validateMediaReferences(ctx, session, schema.Model, data.Records); err != nil {
		return domain.WorkspaceView{}, err
	}
	if pageSelectionRevision(stored.Aggregate, schema.PageSnapshotID) != request.ExpectedSelectionRevision {
		return domain.WorkspaceView{}, domain.ErrConflict
	}
	if reflect.DeepEqual(data.Records, stored.Aggregate.Records) {
		receipt := &domain.MutationReceipt{Operation: "save", KeyHash: keyHash, Fingerprint: fingerprint, ClientRecordIDs: cloneStringMap(clientRecordIDs)}
		committedPage, commitErr := service.repository.CommitPageSelections(ctx, schema.SiteID, schema.PageSnapshotID, request.ExpectedSelectionRevision,
			cloneSelections(data.Selections), cloneSelections(stored.Aggregate.PublishedPageSelections[schema.PageSnapshotID]), receipt)
		if errors.Is(commitErr, domain.ErrConflict) {
			return service.replayAfterConflict(ctx, session, schema.SiteID, schema, "save", keyHash, fingerprint)
		}
		if commitErr != nil {
			return domain.WorkspaceView{}, commitErr
		}
		stored.Aggregate.PageSelections[schema.PageSnapshotID] = committedPage.Draft
		stored.Aggregate.PublishedPageSelections[schema.PageSnapshotID] = committedPage.Published
		stored.Aggregate.PageSelectionRevisions[schema.PageSnapshotID] = committedPage.StoreRevision
		stored.Aggregate.PageMutationReceipts[schema.PageSnapshotID] = committedPage.LastMutation
		return view(stored.Aggregate, schema, clientRecordIDs), nil
	}
	if stored.Aggregate.Revision == math.MaxInt64 {
		return domain.WorkspaceView{}, domain.ErrUnavailable
	}
	next := cloneAggregate(stored.Aggregate)
	next.Revision++
	next.Records = cloneRecords(data.Records)
	next.PageSelections = clonePageSelections(stored.Aggregate.PageSelections)
	next.PageSelections[schema.PageSnapshotID] = cloneSelections(data.Selections)
	next.ChangedPageSelectionIDs = []string{schema.PageSnapshotID}
	next.Selections = []domain.Selection{}
	receipt := &domain.MutationReceipt{Operation: "save", KeyHash: keyHash, Fingerprint: fingerprint, ClientRecordIDs: cloneStringMap(clientRecordIDs)}
	next.PageMutationReceipts[schema.PageSnapshotID] = receipt
	next.LastMutation = receipt
	committed, err := service.repository.Commit(ctx, schema.SiteID, stored.StoreRevision, next, nil)
	if errors.Is(err, domain.ErrConflict) {
		return service.replayAfterConflict(ctx, session, schema.SiteID, schema, "save", keyHash, fingerprint)
	}
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	return view(committed.Aggregate, schema, clientRecordIDs), nil
}

func (service *Service) PublishWorkspace(ctx context.Context, session core.Session, schema domain.Schema, request contentapi.PublishWorkspaceRequest) (domain.WorkspaceView, error) {
	if err := ctx.Err(); err != nil {
		return domain.WorkspaceView{}, err
	}
	if err := service.validateSchema(schema, request.SnapshotID, "ready"); err != nil {
		return domain.WorkspaceView{}, err
	}
	if request.ExpectedRevision < 0 || request.ExpectedSelectionRevision < 0 || !domain.ValidID(request.SnapshotID) {
		return domain.WorkspaceView{}, domain.ErrInvalid
	}
	keyHash, fingerprint, err := mutationIdentity("publish", request.IdempotencyKey, struct {
		ExpectedRevision          int64 `json:"expected_revision"`
		ExpectedSelectionRevision int64 `json:"expected_selection_revision"`
	}{request.ExpectedRevision, request.ExpectedSelectionRevision}, service.maxPayload)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	stored, err := service.loadOrInitialize(ctx, schema, request.SnapshotID)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	if result, replayed, replayErr := service.replay(ctx, session, stored, schema, "publish", keyHash, fingerprint); replayed || replayErr != nil {
		return result, replayErr
	}
	if pageSelectionRevision(stored.Aggregate, schema.PageSnapshotID) != request.ExpectedSelectionRevision {
		return domain.WorkspaceView{}, domain.ErrConflict
	}
	if stored.Aggregate.Revision != request.ExpectedRevision {
		return domain.WorkspaceView{}, domain.ErrConflict
	}
	data := workspaceData(stored.Aggregate)
	if err := domain.ValidateWorkspace(schema.Model, validationPlacements(schema.Placements), data, &data, true); err != nil {
		return domain.WorkspaceView{}, err
	}
	if err := service.validateMediaReferences(ctx, session, schema.Model, stored.Aggregate.Records); err != nil {
		return domain.WorkspaceView{}, err
	}
	if stored.Aggregate.Revision == math.MaxInt64 || stored.Aggregate.PublishedRevision == math.MaxInt64 {
		return domain.WorkspaceView{}, domain.ErrUnavailable
	}
	historyID, err := newUUID()
	if err != nil {
		return domain.WorkspaceView{}, domain.ErrUnavailable
	}
	now := time.Now().UTC()
	publication := domain.Publication{
		ID: historyID, DraftRevision: stored.Aggregate.Revision, ContentRevision: stored.Aggregate.PublishedRevision + 1,
		CreatedAt: now, Records: cloneRecords(stored.Aggregate.Records), Selections: cloneSelections(stored.Aggregate.Selections),
	}
	next := cloneAggregate(stored.Aggregate)
	next.Revision++
	next.PublishedRevision = publication.ContentRevision
	next.PageSelections = clonePageSelections(stored.Aggregate.PageSelections)
	next.PublishedPageSelections = clonePageSelections(stored.Aggregate.PublishedPageSelections)
	next.PublishedPageSelections[schema.PageSnapshotID] = cloneSelections(stored.Aggregate.PageSelections[schema.PageSnapshotID])
	next.ChangedPageSelectionIDs = []string{schema.PageSnapshotID}
	next.Selections = []domain.Selection{}
	receipt := &domain.MutationReceipt{Operation: "publish", KeyHash: keyHash, Fingerprint: fingerprint, HistoryID: historyID}
	next.PageMutationReceipts[schema.PageSnapshotID] = receipt
	next.LastMutation = receipt
	committed, err := service.repository.Commit(ctx, schema.SiteID, stored.StoreRevision, next, &publication)
	if errors.Is(err, domain.ErrConflict) {
		return service.replayAfterConflict(ctx, session, schema.SiteID, schema, "publish", keyHash, fingerprint)
	}
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	return view(committed.Aggregate, schema, nil), nil
}

func (service *Service) ListHistory(ctx context.Context, schema domain.Schema, snapshotID, cursor string, limit int) (domain.HistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.HistoryPage{}, err
	}
	if err := service.validateSchema(schema, snapshotID, "ready"); err != nil {
		return domain.HistoryPage{}, err
	}
	if limit < 1 || limit > service.maxPage {
		return domain.HistoryPage{}, domain.ErrTooLarge
	}
	after, err := decodeCursor(cursor)
	if err != nil {
		return domain.HistoryPage{}, domain.ErrInvalid
	}
	items, next, err := service.repository.ListHistory(ctx, schema.SiteID, after, limit)
	if err != nil {
		return domain.HistoryPage{}, err
	}
	page := domain.HistoryPage{Items: make([]domain.HistorySummary, len(items))}
	for index, publication := range items {
		page.Items[index] = domain.HistorySummary{
			ID: publication.ID, DraftRevision: publication.DraftRevision, ContentRevision: publication.ContentRevision,
			CreatedAt: publication.CreatedAt, RecordCount: len(publication.Records),
		}
	}
	if next != "" {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(next))
	}
	return page, nil
}

func (service *Service) RestoreHistory(ctx context.Context, session core.Session, schema domain.Schema, request contentapi.RestoreHistoryRequest) (domain.WorkspaceView, error) {
	if err := ctx.Err(); err != nil {
		return domain.WorkspaceView{}, err
	}
	if err := service.validateSchema(schema, request.SnapshotID, "ready"); err != nil {
		return domain.WorkspaceView{}, err
	}
	if !domain.ValidID(request.SnapshotID) || !domain.ValidID(request.HistoryID) || request.ExpectedRevision < 0 || request.ExpectedSelectionRevision < 0 {
		return domain.WorkspaceView{}, domain.ErrInvalid
	}
	keyHash, fingerprint, err := mutationIdentity("restore", request.IdempotencyKey, struct {
		HistoryID                 string `json:"history_id"`
		ExpectedRevision          int64  `json:"expected_revision"`
		ExpectedSelectionRevision int64  `json:"expected_selection_revision"`
	}{request.HistoryID, request.ExpectedRevision, request.ExpectedSelectionRevision}, service.maxPayload)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	stored, err := service.loadOrInitialize(ctx, schema, request.SnapshotID)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	if result, replayed, replayErr := service.replay(ctx, session, stored, schema, "restore", keyHash, fingerprint); replayed || replayErr != nil {
		return result, replayErr
	}
	if pageSelectionRevision(stored.Aggregate, schema.PageSnapshotID) != request.ExpectedSelectionRevision {
		return domain.WorkspaceView{}, domain.ErrConflict
	}
	if stored.Aggregate.Revision != request.ExpectedRevision {
		return domain.WorkspaceView{}, domain.ErrConflict
	}
	publication, err := service.repository.GetHistoryByID(ctx, schema.SiteID, request.HistoryID)
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	data := domain.WorkspaceData{Records: cloneRecords(publication.Records), Selections: cloneSelections(publication.Selections)}
	if err := domain.ValidateWorkspace(schema.Model, validationPlacements(schema.Placements), data, &data, false); err != nil {
		return domain.WorkspaceView{}, err
	}
	if err := service.validateMediaReferences(ctx, session, schema.Model, data.Records); err != nil {
		return domain.WorkspaceView{}, err
	}
	if stored.Aggregate.Revision == math.MaxInt64 {
		return domain.WorkspaceView{}, domain.ErrUnavailable
	}
	next := cloneAggregate(stored.Aggregate)
	next.Revision++
	next.Records = data.Records
	next.PageSelections = clonePageSelections(stored.Aggregate.PageSelections)
	next.PageSelections[schema.PageSnapshotID] = cloneSelections(data.Selections)
	next.ChangedPageSelectionIDs = []string{schema.PageSnapshotID}
	next.Selections = []domain.Selection{}
	receipt := &domain.MutationReceipt{Operation: "restore", KeyHash: keyHash, Fingerprint: fingerprint, HistoryID: request.HistoryID}
	next.PageMutationReceipts[schema.PageSnapshotID] = receipt
	next.LastMutation = receipt
	committed, err := service.repository.Commit(ctx, schema.SiteID, stored.StoreRevision, next, nil)
	if errors.Is(err, domain.ErrConflict) {
		return service.replayAfterConflict(ctx, session, schema.SiteID, schema, "restore", keyHash, fingerprint)
	}
	if err != nil {
		return domain.WorkspaceView{}, err
	}
	return view(committed.Aggregate, schema, nil), nil
}

func (service *Service) ReadPublishedContent(ctx context.Context, schema domain.Schema, snapshotID string) (domain.PublishedContent, error) {
	if err := ctx.Err(); err != nil {
		return domain.PublishedContent{}, err
	}
	if err := service.validateSchema(schema, snapshotID, "ready"); err != nil {
		return domain.PublishedContent{}, err
	}
	stored, err := service.repository.Get(ctx, schema.SiteID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.PublishedContent{SnapshotID: schema.PageSnapshotID, Records: []domain.Record{}, Selections: []domain.Selection{}}, nil
	}
	if err != nil {
		return domain.PublishedContent{}, err
	}
	if stored.Aggregate.PublishedRevision == 0 {
		return domain.PublishedContent{SnapshotID: schema.PageSnapshotID, Records: []domain.Record{}, Selections: []domain.Selection{}}, nil
	}
	publication, err := service.repository.GetHistoryByContentRevision(ctx, schema.SiteID, stored.Aggregate.PublishedRevision)
	if err != nil || publication.ContentRevision != stored.Aggregate.PublishedRevision {
		return domain.PublishedContent{}, domain.ErrUnavailable
	}
	return domain.PublishedContent{
		SnapshotID: schema.PageSnapshotID, ContentRevision: publication.ContentRevision,
		Records: cloneRecords(publication.Records), Selections: cloneSelections(stored.Aggregate.PublishedPageSelections[schema.PageSnapshotID]),
	}, nil
}

func (service *Service) CopySnapshotContent(ctx context.Context, _ core.Session, sourceSchema, targetSchema domain.Schema, request contentapi.CopySnapshotContentRequest) (domain.CopyReceipt, error) {
	if err := ctx.Err(); err != nil {
		return domain.CopyReceipt{}, err
	}
	if !contentapi.ValidSelectionPolicy(request.SelectionPolicy) {
		return domain.CopyReceipt{}, domain.ErrInvalid
	}
	if err := service.validateSchema(sourceSchema, request.SourceSnapshotID, "ready"); err != nil {
		return domain.CopyReceipt{}, err
	}
	if err := validateCopyTargetSchema(targetSchema, request.TargetSnapshotID); err != nil {
		return domain.CopyReceipt{}, err
	}
	if sourceSchema.SiteID != targetSchema.SiteID || request.SourceSnapshotID == request.TargetSnapshotID || request.ExpectedSourceRevision < 0 || request.ExpectedSourceSelectionRevision < 0 {
		return domain.CopyReceipt{}, domain.ErrInvalid
	}
	keyHash, fingerprint, err := mutationIdentity("copy", request.IdempotencyKey, struct {
		SourceSnapshotID                string `json:"source_snapshot_id"`
		TargetSnapshotID                string `json:"target_snapshot_id"`
		ExpectedSourceRevision          int64  `json:"expected_source_revision"`
		ExpectedSourceSelectionRevision int64  `json:"expected_source_selection_revision"`
		SelectionPolicy                 string `json:"selection_policy"`
	}{request.SourceSnapshotID, request.TargetSnapshotID, request.ExpectedSourceRevision, request.ExpectedSourceSelectionRevision, request.SelectionPolicy}, service.maxPayload)
	if err != nil {
		return domain.CopyReceipt{}, err
	}
	source, err := service.loadOrInitialize(ctx, sourceSchema, request.SourceSnapshotID)
	if err != nil {
		return domain.CopyReceipt{}, err
	}
	if receipt := source.Aggregate.PageMutationReceipts[request.TargetSnapshotID]; receipt != nil && receipt.Operation == "copy" && receipt.KeyHash == keyHash {
		if receipt.Fingerprint != fingerprint {
			return domain.CopyReceipt{}, domain.ErrConflict
		}
		return domain.CopyReceipt{TargetSnapshotID: request.TargetSnapshotID, SourceRevision: request.ExpectedSourceRevision}, nil
	}
	if source.Aggregate.Revision != request.ExpectedSourceRevision || pageSelectionRevision(source.Aggregate, request.SourceSnapshotID) != request.ExpectedSourceSelectionRevision {
		return domain.CopyReceipt{}, domain.ErrConflict
	}
	targetRevision := pageSelectionRevision(source.Aggregate, request.TargetSnapshotID)
	receipt := &domain.MutationReceipt{Operation: "copy", KeyHash: keyHash, Fingerprint: fingerprint}
	draftSelections := []domain.Selection{}
	publishedSelections := []domain.Selection{}
	if request.SelectionPolicy == contentapi.SelectionPolicyCopy {
		draftSelections = cloneSelections(source.Aggregate.PageSelections[request.SourceSnapshotID])
		publishedSelections = cloneSelections(source.Aggregate.PublishedPageSelections[request.SourceSnapshotID])
	}
	copied, err := service.repository.CommitPageSelections(ctx, sourceSchema.SiteID, request.TargetSnapshotID, targetRevision,
		draftSelections, publishedSelections, receipt)
	if errors.Is(err, domain.ErrConflict) {
		latest, getErr := service.repository.Get(ctx, sourceSchema.SiteID)
		latestReceipt := latest.Aggregate.PageMutationReceipts[request.TargetSnapshotID]
		if getErr == nil && latestReceipt != nil && latestReceipt.KeyHash == keyHash && latestReceipt.Fingerprint == fingerprint {
			return domain.CopyReceipt{TargetSnapshotID: request.TargetSnapshotID, SourceRevision: request.ExpectedSourceRevision}, nil
		}
	}
	if err != nil {
		return domain.CopyReceipt{}, err
	}
	_ = copied
	return domain.CopyReceipt{TargetSnapshotID: request.TargetSnapshotID, SourceRevision: request.ExpectedSourceRevision}, nil
}

func (service *Service) validateSchema(schema domain.Schema, snapshotID string, requiredState string) error {
	if !domain.ValidID(snapshotID) || snapshotID != schema.PageSnapshotID || !domain.ValidID(schema.SiteID) ||
		schema.ModelRevision < 1 || schema.PageRevision < 1 || schema.ModelDigest == "" || schema.PageDigest == "" {
		return domain.ErrNotFound
	}
	if schema.State != requiredState {
		return domain.ErrNotFound
	}
	if schema.Model.Validate() != nil {
		return domain.ErrUnavailable
	}
	return nil
}

func validateCopyTargetSchema(schema domain.Schema, snapshotID string) error {
	if !domain.ValidID(snapshotID) || snapshotID != schema.PageSnapshotID || !domain.ValidID(schema.SiteID) ||
		(schema.State != "provisioning" && schema.State != "ready") || schema.ModelRevision < 1 || schema.PageRevision < 1 ||
		schema.ModelDigest == "" || schema.PageDigest == "" || schema.Model.Validate() != nil {
		return domain.ErrNotFound
	}
	return nil
}

func (service *Service) loadOrInitialize(ctx context.Context, schema domain.Schema, pageSnapshotID string) (domain.StoredWorkspace, error) {
	if pageSnapshotID != schema.PageSnapshotID || !domain.ValidID(schema.SiteID) || !domain.ValidID(schema.PageSnapshotID) {
		return domain.StoredWorkspace{}, domain.ErrInvalid
	}
	stored, err := service.repository.Get(ctx, schema.SiteID)
	if err == nil {
		if stored.Aggregate.SnapshotID != schema.SiteID {
			return domain.StoredWorkspace{}, domain.ErrUnavailable
		}
		stored, err = service.ensurePageSelections(ctx, stored, schema.SiteID, pageSnapshotID)
		if err != nil {
			return domain.StoredWorkspace{}, err
		}
		stored.Aggregate.Selections = cloneSelections(stored.Aggregate.PageSelections[pageSnapshotID])
		return stored, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.StoredWorkspace{}, err
	}
	records, err := initialSingletons(schema.Model)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	initial := domain.Aggregate{SnapshotID: schema.SiteID, Revision: 0, PublishedRevision: 0, Records: records,
		Selections: []domain.Selection{}, PageSelections: map[string][]domain.Selection{pageSnapshotID: {}},
		PublishedPageSelections: map[string][]domain.Selection{pageSnapshotID: {}}}
	created, err := service.repository.Create(ctx, schema.SiteID, initial)
	if errors.Is(err, domain.ErrConflict) {
		created, err = service.repository.Get(ctx, schema.SiteID)
	}
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	created, err = service.ensurePageSelections(ctx, created, schema.SiteID, pageSnapshotID)
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	created.Aggregate.Selections = cloneSelections(created.Aggregate.PageSelections[pageSnapshotID])
	return created, nil
}

func (service *Service) ensurePageSelections(ctx context.Context, stored domain.StoredWorkspace, siteID, pageID string) (domain.StoredWorkspace, error) {
	if stored.Aggregate.PageSelections == nil {
		stored.Aggregate.PageSelections = map[string][]domain.Selection{}
	}
	if stored.Aggregate.PublishedPageSelections == nil {
		stored.Aggregate.PublishedPageSelections = map[string][]domain.Selection{}
	}
	if stored.Aggregate.PageSelectionRevisions == nil {
		stored.Aggregate.PageSelectionRevisions = map[string]int64{}
	}
	if stored.Aggregate.PageMutationReceipts == nil {
		stored.Aggregate.PageMutationReceipts = map[string]*domain.MutationReceipt{}
	}
	if _, exists := stored.Aggregate.PageSelectionRevisions[pageID]; exists {
		return stored, nil
	}
	created, err := service.repository.CommitPageSelections(ctx, siteID, pageID, 0, []domain.Selection{}, []domain.Selection{}, nil)
	if errors.Is(err, domain.ErrConflict) {
		stored, err = service.repository.Get(ctx, siteID)
		if err != nil {
			return domain.StoredWorkspace{}, err
		}
		if _, exists := stored.Aggregate.PageSelectionRevisions[pageID]; !exists {
			return domain.StoredWorkspace{}, domain.ErrConflict
		}
		return stored, nil
	}
	if err != nil {
		return domain.StoredWorkspace{}, err
	}
	stored.Aggregate.PageSelections[pageID] = created.Draft
	stored.Aggregate.PublishedPageSelections[pageID] = created.Published
	stored.Aggregate.PageSelectionRevisions[pageID] = created.StoreRevision
	stored.Aggregate.PageMutationReceipts[pageID] = created.LastMutation
	return stored, nil
}

func initialSingletons(model domain.DataModel) ([]domain.Record, error) {
	records := make([]domain.Record, 0)
	for _, entity := range model.Entities {
		if entity.Cardinality != "singleton" {
			continue
		}
		id, err := newUUID()
		if err != nil {
			return nil, domain.ErrUnavailable
		}
		records = append(records, domain.Record{ID: id, EntityID: entity.ID, SortOrder: 0, Values: map[string]any{}})
	}
	return records, nil
}

func addTargetSingletons(model domain.DataModel, source []domain.Record) ([]domain.Record, error) {
	result := cloneRecords(source)
	counts := make(map[string]int)
	for _, record := range result {
		counts[record.EntityID]++
	}
	for _, entity := range model.Entities {
		if entity.Cardinality != "singleton" {
			continue
		}
		if counts[entity.ID] > 1 {
			return nil, domain.ErrConflict
		}
		if counts[entity.ID] == 0 {
			id, err := newUUID()
			if err != nil {
				return nil, domain.ErrUnavailable
			}
			result = append(result, domain.Record{ID: id, EntityID: entity.ID, SortOrder: 0, Values: map[string]any{}})
		}
	}
	return result, nil
}

func normalizeDraftRequest(request contentapi.SaveWorkspaceDraftRequest, current domain.Aggregate) (domain.WorkspaceData, map[string]string, error) {
	oldRecords := make(map[string]domain.Record, len(current.Records))
	for _, record := range current.Records {
		oldRecords[record.ID] = record
	}
	clientIDs := make(map[string]string)
	seenIDs := make(map[string]struct{}, len(request.Records))
	records := make([]domain.Record, 0, len(request.Records))
	for _, input := range request.Records {
		if !domain.ValidID(input.EntityID) || input.SortOrder < 0 {
			return domain.WorkspaceData{}, nil, domain.ErrInvalid
		}
		if input.ID != "" && (!domain.ValidID(input.ID) || input.ClientKey != "") {
			return domain.WorkspaceData{}, nil, domain.ErrInvalid
		}
		if input.ClientKey != "" && (!validClientKey(input.ClientKey) || input.ID != "") {
			return domain.WorkspaceData{}, nil, domain.ErrInvalid
		}
		id := input.ID
		if id == "" {
			var err error
			id, err = newUUID()
			if err != nil {
				return domain.WorkspaceData{}, nil, domain.ErrUnavailable
			}
			if input.ClientKey != "" {
				if _, exists := clientIDs[input.ClientKey]; exists {
					return domain.WorkspaceData{}, nil, domain.ErrInvalid
				}
				clientIDs[input.ClientKey] = id
			}
		} else {
			old, exists := oldRecords[id]
			if !exists || old.EntityID != input.EntityID {
				return domain.WorkspaceData{}, nil, domain.ErrInvalid
			}
		}
		if _, exists := seenIDs[id]; exists {
			return domain.WorkspaceData{}, nil, domain.ErrInvalid
		}
		seenIDs[id] = struct{}{}
		values := make(map[string]any, len(input.Values))
		for key, value := range input.Values {
			values[key] = value
		}
		records = append(records, domain.Record{ID: id, EntityID: input.EntityID, SortOrder: input.SortOrder, Values: values})
	}
	selections := make([]domain.Selection, len(request.Selections))
	for index, input := range request.Selections {
		selection := domain.Selection{PlacementID: input.PlacementID, InputKey: input.InputKey, Mode: input.Mode, RecordIDs: append([]string(nil), input.RecordIDs...)}
		for recordIndex, recordID := range selection.RecordIDs {
			if generated, exists := clientIDs[recordID]; exists {
				selection.RecordIDs[recordIndex] = generated
			}
		}
		selections[index] = selection
	}
	return domain.WorkspaceData{Records: records, Selections: selections}, clientIDs, nil
}

func dropDeletedRecordSelections(selections []domain.Selection, previous domain.WorkspaceData, current []domain.Record, placements []domain.PlacementView) []domain.Selection {
	previousIDs := make(map[string]struct{}, len(previous.Records))
	currentIDs := make(map[string]struct{}, len(current))
	for _, record := range previous.Records {
		previousIDs[record.ID] = struct{}{}
	}
	for _, record := range current {
		currentIDs[record.ID] = struct{}{}
	}
	result := make([]domain.Selection, 0, len(selections))
	for _, selection := range selections {
		if selection.Mode != "ids" {
			result = append(result, selection)
			continue
		}
		filtered := make([]string, 0, len(selection.RecordIDs))
		for _, id := range selection.RecordIDs {
			if _, existed := previousIDs[id]; existed {
				if _, remains := currentIDs[id]; !remains {
					continue
				}
			}
			filtered = append(filtered, id)
		}
		selection.RecordIDs = filtered
		if len(filtered) == 0 && isNonCollectionSelection(placements, selection.PlacementID, selection.InputKey) {
			continue
		}
		result = append(result, selection)
	}
	return result
}

func isNonCollectionSelection(placements []domain.PlacementView, placementID, inputKey string) bool {
	for _, placement := range placements {
		if placement.ID != placementID {
			continue
		}
		for _, input := range placement.Inputs {
			if input.InputKey == inputKey {
				return input.Kind == "scalar" || input.Kind == "record"
			}
		}
	}
	return false
}

func (service *Service) validateMediaReferences(ctx context.Context, session core.Session, model domain.DataModel, records []domain.Record) error {
	entities := make(map[string]domain.Entity, len(model.Entities))
	for _, entity := range model.Entities {
		entities[entity.ID] = entity
	}
	checked := make(map[string]struct{})
	for _, record := range records {
		entity, exists := entities[record.EntityID]
		if !exists {
			continue
		}
		fields := make(map[string]domain.Field, len(entity.Fields))
		for _, field := range entity.Fields {
			fields[field.Key] = field
		}
		for key, value := range record.Values {
			field, exists := fields[key]
			if !exists || field.Type != "media" || isEmptyMediaValue(value) {
				continue
			}
			fileID, ok := value.(string)
			if !ok || !domain.ValidID(fileID) {
				return domain.ErrInvalid
			}
			if _, exists := checked[fileID]; exists {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := service.files.StatFile(ctx, session, fileID); err != nil {
				if errors.Is(err, core.ErrFileNotFound) {
					return domain.ErrInvalid
				}
				return err
			}
			checked[fileID] = struct{}{}
		}
	}
	return nil
}

func (service *Service) replay(ctx context.Context, session core.Session, stored domain.StoredWorkspace, schema domain.Schema, operation, keyHash, fingerprint string) (domain.WorkspaceView, bool, error) {
	receipt := stored.Aggregate.PageMutationReceipts[schema.PageSnapshotID]
	if receipt == nil || receipt.KeyHash != keyHash {
		receipt = stored.Aggregate.LastMutation
	}
	if receipt == nil || receipt.KeyHash != keyHash {
		return domain.WorkspaceView{}, false, nil
	}
	if receipt.Operation != operation || receipt.Fingerprint != fingerprint {
		return domain.WorkspaceView{}, true, domain.ErrConflict
	}
	if err := service.validateMediaReferences(ctx, session, schema.Model, stored.Aggregate.Records); err != nil {
		return domain.WorkspaceView{}, true, err
	}
	return view(stored.Aggregate, schema, receipt.ClientRecordIDs), true, nil
}

func (service *Service) replayAfterConflict(ctx context.Context, session core.Session, snapshotID string, schema domain.Schema, operation, keyHash, fingerprint string) (domain.WorkspaceView, error) {
	current, err := service.repository.Get(ctx, snapshotID)
	if err != nil {
		return domain.WorkspaceView{}, domain.ErrConflict
	}
	result, replayed, replayErr := service.replay(ctx, session, current, schema, operation, keyHash, fingerprint)
	if replayed || replayErr != nil {
		return result, replayErr
	}
	return domain.WorkspaceView{}, domain.ErrConflict
}

func mutationIdentity(operation, key string, input any, maximum int) (string, string, error) {
	if len(key) < 1 || len(key) > 256 || !utf8.ValidString(key) || strings.TrimSpace(key) != key || containsControl(key) {
		return "", "", domain.ErrInvalid
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", "", domain.ErrInvalid
	}
	if len(encoded) > maximum {
		return "", "", domain.ErrTooLarge
	}
	keyDigest := sha256.Sum256([]byte(key))
	requestDigest := sha256.Sum256(append([]byte(operation+"\x00"), encoded...))
	return hex.EncodeToString(keyDigest[:]), hex.EncodeToString(requestDigest[:]), nil
}

func decodeCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	if len(cursor) > 1024 {
		return "", domain.ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(decoded) == 0 || len(decoded) > 256 || containsControl(string(decoded)) {
		return "", domain.ErrInvalid
	}
	return string(decoded), nil
}

func pageSelectionRevision(aggregate domain.Aggregate, pageID string) int64 {
	if aggregate.PageSelectionRevisions == nil {
		return 0
	}
	return aggregate.PageSelectionRevisions[pageID]
}

func view(aggregate domain.Aggregate, schema domain.Schema, clientIDs map[string]string) domain.WorkspaceView {
	dataModel := append(json.RawMessage(nil), schema.DataModel...)
	if dataModel == nil {
		dataModel = json.RawMessage(`{"formatVersion":1,"entities":[]}`)
	}
	placements := make([]domain.PlacementView, len(schema.Placements))
	for index, placement := range schema.Placements {
		placements[index] = clonePlacement(placement)
	}
	return domain.WorkspaceView{
		SnapshotID: schema.PageSnapshotID, SiteID: schema.SiteID, PageSnapshotID: schema.PageSnapshotID,
		ModelRevision: schema.ModelRevision, ModelDigest: schema.ModelDigest, PageRevision: schema.PageRevision, PageDigest: schema.PageDigest,
		DataModel: dataModel, Revision: aggregate.Revision, SelectionRevision: pageSelectionRevision(aggregate, schema.PageSnapshotID), PublishedRevision: aggregate.PublishedRevision,
		Records: cloneRecords(aggregate.Records), Selections: cloneSelections(aggregate.PageSelections[schema.PageSnapshotID]), Placements: placements,
		ClientRecordIDs: cloneStringMap(clientIDs),
	}
}

func validationPlacements(placements []domain.PlacementView) []domain.Placement {
	result := make([]domain.Placement, len(placements))
	for index, placement := range placements {
		result[index] = domain.Placement{ID: placement.ID, Inputs: placement.Inputs}
	}
	return result
}

func workspaceData(aggregate domain.Aggregate) domain.WorkspaceData {
	return domain.WorkspaceData{Records: cloneRecords(aggregate.Records), Selections: cloneSelections(aggregate.Selections)}
}

func cloneAggregate(aggregate domain.Aggregate) domain.Aggregate {
	aggregate.Records = cloneRecords(aggregate.Records)
	aggregate.Selections = cloneSelections(aggregate.Selections)
	aggregate.PageSelections = clonePageSelections(aggregate.PageSelections)
	aggregate.PublishedPageSelections = clonePageSelections(aggregate.PublishedPageSelections)
	if aggregate.PageSelectionRevisions != nil {
		revisions := make(map[string]int64, len(aggregate.PageSelectionRevisions))
		for pageID, revision := range aggregate.PageSelectionRevisions {
			revisions[pageID] = revision
		}
		aggregate.PageSelectionRevisions = revisions
	}
	if aggregate.PageMutationReceipts != nil {
		receipts := make(map[string]*domain.MutationReceipt, len(aggregate.PageMutationReceipts))
		for pageID, receipt := range aggregate.PageMutationReceipts {
			if receipt == nil {
				continue
			}
			copy := *receipt
			copy.ClientRecordIDs = cloneStringMap(receipt.ClientRecordIDs)
			receipts[pageID] = &copy
		}
		aggregate.PageMutationReceipts = receipts
	}
	if aggregate.LastMutation != nil {
		receipt := *aggregate.LastMutation
		receipt.ClientRecordIDs = cloneStringMap(receipt.ClientRecordIDs)
		aggregate.LastMutation = &receipt
	}
	return aggregate
}

func cloneRecords(records []domain.Record) []domain.Record {
	result := make([]domain.Record, len(records))
	for index, record := range records {
		values := make(map[string]any, len(record.Values))
		for key, value := range record.Values {
			values[key] = value
		}
		record.Values = values
		result[index] = record
	}
	return result
}

func clonePageSelections(source map[string][]domain.Selection) map[string][]domain.Selection {
	if source == nil {
		return map[string][]domain.Selection{}
	}
	result := make(map[string][]domain.Selection, len(source))
	for pageID, selections := range source {
		result[pageID] = cloneSelections(selections)
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

func clonePlacement(placement domain.PlacementView) domain.PlacementView {
	result := placement
	result.Inputs = make([]domain.PlacementInput, len(placement.Inputs))
	for index, input := range placement.Inputs {
		result.Inputs[index] = input
		result.Inputs[index].Fields = append([]domain.PlacementField(nil), input.Fields...)
		if input.Lookup != nil {
			lookup := *input.Lookup
			result.Inputs[index].Lookup = &lookup
		}
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func newUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func validClientKey(value string) bool {
	return utf8.ValidString(value) && len(value) <= 128 && strings.TrimSpace(value) == value && !containsControl(value)
}

func containsControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func isEmptyMediaValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}
