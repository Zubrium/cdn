package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
)

const (
	pageRecordVersion         = 1
	pageTemplateRecordVersion = 1
	pageCursorVersion         = 1
	maxPageNameRunes          = 120
	maxTemplateDescription    = 1000
)

var templateSlotIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type pageDocument struct {
	Page websiteapi.Page `json:"page"`
}

type pageCreateReceipt struct {
	Fingerprint string `json:"fingerprint"`
	PageID      string `json:"page_id"`
	SnapshotID  string `json:"snapshot_id"`
}

type templateCreateReceipt struct {
	Fingerprint string `json:"fingerprint"`
	TemplateID  string `json:"template_id"`
}

type templateCursor struct {
	Version int    `json:"v"`
	After   string `json:"a,omitempty"`
}

type pageCreateFingerprintInput struct {
	Name             string `json:"name"`
	TemplateID       string `json:"template_id,omitempty"`
	TemplateRevision int64  `json:"template_revision,omitempty"`
}

type templateCreateFingerprintInput struct {
	Name          string                               `json:"name"`
	Description   string                               `json:"description"`
	Configuration websiteapi.PageTemplateConfiguration `json:"configuration"`
}

// ListPages returns the dynamic page registry.
func (s *Service) ListPages(ctx context.Context, cursor string, limit int) (websiteapi.PagePage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.PagePage{}, err
	}
	if limit < 1 || limit > s.store.MaxPageSize() {
		return websiteapi.PagePage{}, websiteapi.ErrTooLarge
	}
	state := templateCursor{Version: pageCursorVersion}
	if cursor != "" {
		parsed, err := decodeTemplateCursor(cursor)
		if err != nil || !validCursorAfter(parsed.After, "page/") {
			return websiteapi.PagePage{}, websiteapi.ErrInvalid
		}
		state = parsed
	}
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"page"}}}
	keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "global", "global", state.After, selectors, limit)
	if err != nil {
		return websiteapi.PagePage{}, mapCoreError(err)
	}
	if len(keys) != len(records) {
		return websiteapi.PagePage{}, websiteapi.ErrUnavailable
	}
	page := websiteapi.PagePage{Items: make([]websiteapi.Page, 0, len(keys))}
	for i := range keys {
		value, err := decodePageRecord(keys[i], records[i])
		if err != nil {
			return websiteapi.PagePage{}, err
		}
		page.Items = append(page.Items, value)
	}
	if next != "" {
		page.NextCursor = encodeTemplateCursor(templateCursor{Version: pageCursorVersion, After: next})
	}
	return page, nil
}

// GetPage reads one dynamic page identity.
func (s *Service) GetPage(ctx context.Context, pageID string) (websiteapi.Page, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.Page{}, err
	}
	if !snapshotIDPattern.MatchString(pageID) {
		return websiteapi.Page{}, websiteapi.ErrNotFound
	}
	page, _, err := s.loadPage(ctx, pageID)
	return page, err
}

// CreatePage synchronously writes a server-generated page, a ready root
// snapshot and the idempotency receipt in one Website-scoped Core batch.
func (s *Service) CreatePage(ctx context.Context, request websiteapi.CreatePageRequest) (websiteapi.Page, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.Page{}, err
	}
	if !validSnapshotName(request.Name) || !snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		return websiteapi.Page{}, websiteapi.ErrInvalid
	}
	if (request.TemplateID == "") != (request.TemplateRevision == 0) {
		return websiteapi.Page{}, websiteapi.ErrInvalid
	}
	fingerprint, err := pageCreateFingerprint(request)
	if err != nil {
		return websiteapi.Page{}, websiteapi.ErrUnavailable
	}
	if page, found, err := s.readPageCreateReceipt(ctx, request.IdempotencyKey, fingerprint); err != nil {
		return websiteapi.Page{}, err
	} else if found {
		return page, nil
	}
	var template websiteapi.PageTemplate
	if request.TemplateID != "" {
		if !snapshotIDPattern.MatchString(request.TemplateID) || request.TemplateRevision < 1 {
			return websiteapi.Page{}, websiteapi.ErrInvalid
		}
		value, _, err := s.loadPageTemplate(ctx, request.TemplateID)
		if err != nil {
			return websiteapi.Page{}, err
		}
		if value.Revision != request.TemplateRevision {
			return websiteapi.Page{}, websiteapi.ErrConflict
		}
		value.Configuration, err = s.validateTemplateConfiguration(ctx, value.Configuration)
		if err != nil {
			return websiteapi.Page{}, err
		}
		template = value
	}
	if s.store.MaxBatchSize() < 4 {
		return websiteapi.Page{}, websiteapi.ErrTooLarge
	}
	pageID, err := randomUUID()
	if err != nil {
		return websiteapi.Page{}, websiteapi.ErrUnavailable
	}
	rootID, err := randomUUID()
	if err != nil {
		return websiteapi.Page{}, websiteapi.ErrUnavailable
	}
	payload := emptyPagePayload(pageID, request.Name)
	if request.TemplateID != "" {
		payload, err = materializeTemplate(template.Configuration, pageID, request.Name)
		if err != nil {
			return websiteapi.Page{}, err
		}
		payload.Metadata, err = addTemplateMetadata(payload.Metadata, "template_source", template.ID, template.Revision, 0)
		if err != nil {
			return websiteapi.Page{}, websiteapi.ErrUnavailable
		}
	}
	pins, err := featurePinsFromConfig(payload.Config)
	if err != nil {
		return websiteapi.Page{}, websiteapi.ErrInvalid
	}
	root := websiteapi.Snapshot{ID: rootID, PageID: pageID, Name: request.Name,
		Sequence: 1, Revision: 1, FeaturePins: pins, State: "ready", CreatedAt: time.Now().UTC(), Payload: cloneSnapshotPayload(payload)}
	rootPayload, err := json.Marshal(root)
	if err != nil {
		return websiteapi.Page{}, websiteapi.ErrUnavailable
	}
	if len(rootPayload) > s.store.MaxPayloadBytes() {
		return websiteapi.Page{}, websiteapi.ErrTooLarge
	}
	rootIndex, err := snapshotIndex(root)
	if err != nil {
		return websiteapi.Page{}, websiteapi.ErrUnavailable
	}
	page := websiteapi.Page{ID: pageID, Name: request.Name, LatestSnapshotID: rootID,
		LatestSnapshotState: "ready", CreatedAt: root.CreatedAt}
	pageValue, err := pageModuleDataValue(page, 1)
	if err != nil {
		return websiteapi.Page{}, err
	}
	treeMutation, err := pageTreeRegistryMutation(pageID, core.ModuleDataRecord{}, 1)
	if err != nil {
		return websiteapi.Page{}, err
	}
	receiptPayload, err := json.Marshal(pageCreateReceipt{Fingerprint: fingerprint, PageID: pageID, SnapshotID: rootID})
	if err != nil {
		return websiteapi.Page{}, websiteapi.ErrUnavailable
	}
	receiptIndex, _ := json.Marshal(map[string]any{"record_type": "page_idempotency", "page_id": pageID, "snapshot_id": rootID, "state": "created", "revision": int64(1)})
	mutations := []core.ModuleDataMutation{
		{Key: snapshotKey(pageID, rootID), ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: rootPayload, PayloadVersion: 1, IndexData: rootIndex}},
		{Key: websiterepo.PageKey(pageID), ExpectedRevision: 0, Value: pageValue},
		treeMutation,
		{Key: websiterepo.PageCreateReceiptKey(request.IdempotencyKey), ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: receiptPayload, PayloadVersion: 1, IndexData: receiptIndex}},
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		if errors.Is(mapCoreError(err), websiteapi.ErrConflict) {
			if existing, found, readErr := s.readPageCreateReceipt(ctx, request.IdempotencyKey, fingerprint); readErr != nil {
				return websiteapi.Page{}, readErr
			} else if found {
				return existing, nil
			}
		}
		return websiteapi.Page{}, mapCoreError(err)
	}
	return page, nil
}

func (s *Service) loadPage(ctx context.Context, pageID string) (websiteapi.Page, core.ModuleDataRecord, error) {
	key := websiterepo.PageKey(pageID)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return websiteapi.Page{}, core.ModuleDataRecord{}, mapCoreError(err)
	}
	page, err := decodePageRecord(key, record)
	if err != nil {
		return websiteapi.Page{}, core.ModuleDataRecord{}, err
	}
	latest, _, err := s.snapshotInPage(ctx, page.ID, page.LatestSnapshotID)
	if err != nil {
		if errors.Is(err, websiteapi.ErrNotFound) {
			return websiteapi.Page{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
		}
		return websiteapi.Page{}, core.ModuleDataRecord{}, err
	}
	if latest.State != page.LatestSnapshotState || latest.Name != page.Name {
		return websiteapi.Page{}, core.ModuleDataRecord{}, websiteapi.ErrUnavailable
	}
	return page, record, nil
}

func decodePageRecord(key core.ModuleDataKey, record core.ModuleDataRecord) (websiteapi.Page, error) {
	var stored pageDocument
	var index struct {
		RecordType string `json:"record_type"`
		PageID     string `json:"page_id"`
		SnapshotID string `json:"snapshot_id"`
		State      string `json:"state"`
		Revision   int64  `json:"revision"`
	}
	pageIDFromKey := strings.TrimPrefix(key.RecordKey, "page/")
	if !strings.HasPrefix(key.RecordKey, "page/") || pageIDFromKey == key.RecordKey || key != websiterepo.PageKey(pageIDFromKey) || record.PayloadVersion != pageRecordVersion || record.Revision < 1 ||
		json.Unmarshal(record.Payload, &stored) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "page" || index.PageID != stored.Page.ID || index.PageID != pageIDFromKey || index.SnapshotID != stored.Page.LatestSnapshotID ||
		index.State != stored.Page.LatestSnapshotState || index.Revision < 1 ||
		!snapshotIDPattern.MatchString(stored.Page.ID) || !validSnapshotName(stored.Page.Name) || !snapshotIDPattern.MatchString(stored.Page.LatestSnapshotID) ||
		!validSnapshotState(stored.Page.LatestSnapshotState) || stored.Page.CreatedAt.IsZero() {
		return websiteapi.Page{}, websiteapi.ErrUnavailable
	}
	return stored.Page, nil
}

func snapshotIndex(snapshot websiteapi.Snapshot) (json.RawMessage, error) {
	index := map[string]any{"record_type": "snapshot", "snapshot_id": snapshot.ID, "page_id": snapshot.PageID,
		"state": snapshot.State, "revision": snapshot.Revision, "parent_id": snapshot.ParentID}
	return json.Marshal(index)
}

func pageModuleDataValue(page websiteapi.Page, revision int64) (*core.ModuleDataValue, error) {
	payload, err := json.Marshal(pageDocument{Page: page})
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(map[string]any{"record_type": "page", "page_id": page.ID, "snapshot_id": page.LatestSnapshotID,
		"state": page.LatestSnapshotState, "revision": revision})
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	return &core.ModuleDataValue{Payload: payload, PayloadVersion: pageRecordVersion, IndexData: index}, nil
}

func (s *Service) readPageCreateReceipt(ctx context.Context, idempotencyKey, fingerprint string) (websiteapi.Page, bool, error) {
	record, err := s.store.Get(ctx, websiterepo.PageCreateReceiptKey(idempotencyKey))
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return websiteapi.Page{}, false, nil
		}
		return websiteapi.Page{}, false, mapCoreError(err)
	}
	var receipt pageCreateReceipt
	var index struct {
		RecordType string `json:"record_type"`
		PageID     string `json:"page_id"`
		SnapshotID string `json:"snapshot_id"`
		State      string `json:"state"`
		Revision   int64  `json:"revision"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &receipt) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "page_idempotency" || index.State != "created" || index.Revision < 1 || index.PageID != receipt.PageID || index.SnapshotID != receipt.SnapshotID ||
		!snapshotIDPattern.MatchString(receipt.PageID) || !snapshotIDPattern.MatchString(receipt.SnapshotID) || !sha256HexPattern.MatchString(receipt.Fingerprint) {
		return websiteapi.Page{}, false, websiteapi.ErrUnavailable
	}
	if receipt.Fingerprint != fingerprint {
		return websiteapi.Page{}, false, websiteapi.ErrConflict
	}
	page, _, err := s.loadPage(ctx, receipt.PageID)
	if err != nil {
		return websiteapi.Page{}, false, err
	}
	if page.LatestSnapshotID != receipt.SnapshotID {
		// The original create succeeded; later snapshots may become latest.
		// Replay still returns the same page identity and its current metadata.
		return page, true, nil
	}
	return page, true, nil
}

func pageCreateFingerprint(request websiteapi.CreatePageRequest) (string, error) {
	payload, err := json.Marshal(pageCreateFingerprintInput{Name: request.Name, TemplateID: strings.ToLower(request.TemplateID), TemplateRevision: request.TemplateRevision})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func emptyPagePayload(pageID, name string) websiteapi.SnapshotPayload {
	return websiteapi.SnapshotPayload{
		Config:   json.RawMessage(`{"format_version":3,"page":{"id":"` + pageID + `","name":"` + jsonEscape(name) + `","placements":[]}}`),
		Metadata: json.RawMessage(`{"formatVersion":1,"siteName":"` + jsonEscape(name) + `"}`),
	}
}

func jsonEscape(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded[1 : len(encoded)-1])
}

func (s *Service) ListPageTemplates(ctx context.Context, cursor string, limit int) (websiteapi.PageTemplatePage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.PageTemplatePage{}, err
	}
	if limit < 1 || limit > s.store.MaxPageSize() {
		return websiteapi.PageTemplatePage{}, websiteapi.ErrTooLarge
	}
	state := templateCursor{Version: pageCursorVersion}
	if cursor != "" {
		parsed, err := decodeTemplateCursor(cursor)
		if err != nil || !validCursorAfter(parsed.After, "template/") {
			return websiteapi.PageTemplatePage{}, websiteapi.ErrInvalid
		}
		state = parsed
	}
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"page_template"}}}
	keys, records, next, err := s.store.List(ctx, websiterepo.TemplateNamespace, "global", "global", state.After, selectors, limit)
	if err != nil {
		return websiteapi.PageTemplatePage{}, mapCoreError(err)
	}
	if len(keys) != len(records) {
		return websiteapi.PageTemplatePage{}, websiteapi.ErrUnavailable
	}
	page := websiteapi.PageTemplatePage{Items: make([]websiteapi.PageTemplate, 0, len(keys))}
	for i := range keys {
		value, err := decodePageTemplate(keys[i], records[i])
		if err != nil {
			return websiteapi.PageTemplatePage{}, err
		}
		page.Items = append(page.Items, value)
	}
	if next != "" {
		page.NextCursor = encodeTemplateCursor(templateCursor{Version: pageCursorVersion, After: next})
	}
	return page, nil
}

func (s *Service) GetPageTemplate(ctx context.Context, templateID string) (websiteapi.PageTemplate, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.PageTemplate{}, err
	}
	if !snapshotIDPattern.MatchString(templateID) {
		return websiteapi.PageTemplate{}, websiteapi.ErrNotFound
	}
	value, _, err := s.loadPageTemplate(ctx, templateID)
	return value, err
}

func (s *Service) CreatePageTemplate(ctx context.Context, request websiteapi.CreatePageTemplateRequest) (websiteapi.PageTemplate, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.PageTemplate{}, err
	}
	if !validSnapshotName(request.Name) || !validTemplateDescription(request.Description) || !snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		return websiteapi.PageTemplate{}, websiteapi.ErrInvalid
	}
	configuration, err := s.validateTemplateConfiguration(ctx, request.Configuration)
	if err != nil {
		return websiteapi.PageTemplate{}, err
	}
	request.Configuration = configuration
	fingerprint, err := templateCreateFingerprint(request)
	if err != nil {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	if value, found, err := s.readTemplateCreateReceipt(ctx, request.IdempotencyKey, fingerprint); err != nil {
		return websiteapi.PageTemplate{}, err
	} else if found {
		return value, nil
	}
	if s.store.MaxBatchSize() < 2 {
		return websiteapi.PageTemplate{}, websiteapi.ErrTooLarge
	}
	templateID, err := randomUUID()
	if err != nil {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	value := websiteapi.PageTemplate{ID: templateID, Name: request.Name, Description: request.Description,
		Revision: 1, Configuration: request.Configuration, UpdatedAt: time.Now().UTC()}
	value.Digest, err = pageTemplateDigest(value)
	if err != nil {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	templateValue, err := pageTemplateModuleDataValue(value)
	if err != nil {
		return websiteapi.PageTemplate{}, err
	}
	receiptPayload, err := json.Marshal(templateCreateReceipt{Fingerprint: fingerprint, TemplateID: templateID})
	if err != nil {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	receiptIndex, _ := json.Marshal(map[string]any{"record_type": "template_idempotency", "template_id": templateID, "revision": int64(1), "digest": value.Digest})
	if err := s.store.Batch(ctx, []core.ModuleDataMutation{
		{Key: websiterepo.PageTemplateKey(templateID), ExpectedRevision: 0, Value: templateValue},
		{Key: websiterepo.PageTemplateCreateReceiptKey(request.IdempotencyKey), ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: receiptPayload, PayloadVersion: 1, IndexData: receiptIndex}},
	}); err != nil {
		if errors.Is(mapCoreError(err), websiteapi.ErrConflict) {
			if existing, found, readErr := s.readTemplateCreateReceipt(ctx, request.IdempotencyKey, fingerprint); readErr != nil {
				return websiteapi.PageTemplate{}, readErr
			} else if found {
				return existing, nil
			}
		}
		return websiteapi.PageTemplate{}, mapCoreError(err)
	}
	return value, nil
}

func (s *Service) UpdatePageTemplate(ctx context.Context, request websiteapi.UpdatePageTemplateRequest) (websiteapi.PageTemplate, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.PageTemplate{}, err
	}
	if request.ExpectedRevision < 1 || !validSnapshotName(request.Name) || !validTemplateDescription(request.Description) {
		return websiteapi.PageTemplate{}, websiteapi.ErrInvalid
	}
	configuration, err := s.validateTemplateConfiguration(ctx, request.Configuration)
	if err != nil {
		return websiteapi.PageTemplate{}, err
	}
	current, record, err := s.loadPageTemplate(ctx, request.TemplateID)
	if err != nil {
		return websiteapi.PageTemplate{}, err
	}
	if record.Revision != request.ExpectedRevision {
		return websiteapi.PageTemplate{}, websiteapi.ErrConflict
	}
	if current.Revision == math.MaxInt64 {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	value := websiteapi.PageTemplate{ID: current.ID, Name: request.Name, Description: request.Description,
		Revision: current.Revision + 1, Configuration: configuration, UpdatedAt: time.Now().UTC()}
	value.Digest, err = pageTemplateDigest(value)
	if err != nil {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	moduleValue, err := pageTemplateModuleDataValue(value)
	if err != nil {
		return websiteapi.PageTemplate{}, err
	}
	if err := s.store.Batch(ctx, []core.ModuleDataMutation{{Key: websiterepo.PageTemplateKey(request.TemplateID), ExpectedRevision: record.Revision, Value: moduleValue}}); err != nil {
		return websiteapi.PageTemplate{}, mapCoreError(err)
	}
	return value, nil
}

func (s *Service) DeletePageTemplate(ctx context.Context, request websiteapi.DeletePageTemplateRequest) error {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return err
	}
	if request.ExpectedRevision < 1 {
		return websiteapi.ErrInvalid
	}
	_, record, err := s.loadPageTemplate(ctx, request.TemplateID)
	if err != nil {
		return err
	}
	if record.Revision != request.ExpectedRevision {
		return websiteapi.ErrConflict
	}
	if err := s.store.Batch(ctx, []core.ModuleDataMutation{{Key: websiterepo.PageTemplateKey(request.TemplateID), ExpectedRevision: record.Revision}}); err != nil {
		return mapCoreError(err)
	}
	return nil
}

// ApplyPageTemplate creates a provisioning child snapshot and records immutable
// template provenance. It deliberately does not invoke Content or Coordinator.
func (s *Service) ApplyPageTemplate(ctx context.Context, request websiteapi.ApplyPageTemplateRequest) (websiteapi.Snapshot, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.Snapshot{}, err
	}
	if !snapshotIDPattern.MatchString(request.PageID) || !snapshotIDPattern.MatchString(request.TemplateID) ||
		!snapshotIDPattern.MatchString(request.ExpectedSnapshotID) || request.TemplateRevision < 1 || request.ExpectedSnapshotRevision < 1 ||
		request.ExpectedContentRevision < 0 || !request.Confirmed || !snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	page, _, err := s.loadPage(ctx, request.PageID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	request.PageID = page.ID
	request.ExpectedSnapshotID = strings.ToLower(request.ExpectedSnapshotID)
	parent, parentRecord, err := s.snapshotInPage(ctx, request.PageID, request.ExpectedSnapshotID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	if parent.State != "ready" || parentRecord.Revision != request.ExpectedSnapshotRevision {
		return websiteapi.Snapshot{}, websiteapi.ErrConflict
	}
	template, _, err := s.loadPageTemplate(ctx, request.TemplateID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	if template.Revision != request.TemplateRevision {
		return websiteapi.Snapshot{}, websiteapi.ErrConflict
	}
	template.Configuration, err = s.validateTemplateConfiguration(ctx, template.Configuration)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	pageConfig, err := snapshotPageFromConfig(template.Configuration.Payload.Config)
	if err != nil || !validSnapshotName(pageConfig.Name) {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	payload, err := materializeTemplate(template.Configuration, request.PageID, pageConfig.Name)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	payload.Metadata, err = addTemplateMetadata(payload.Metadata, "template_application", template.ID, template.Revision, request.ExpectedContentRevision)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	requestToCreate := websiteapi.CreateSnapshotRequest{PageID: request.PageID,
		ParentID: parent.ID, Name: pageConfig.Name, ExpectedRevision: request.ExpectedSnapshotRevision,
		ExpectedContentRevision: request.ExpectedContentRevision, IdempotencyKey: request.IdempotencyKey, Payload: payload}
	requestToCreate.ParentID = strings.ToLower(requestToCreate.ParentID)
	fingerprint, err := snapshotCreateFingerprint(requestToCreate)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if existing, found, err := s.readSnapshotCreateReceipt(ctx, request.PageID, request.IdempotencyKey, fingerprint); err != nil {
		return websiteapi.Snapshot{}, err
	} else if found {
		return existing, nil
	}
	if page.LatestSnapshotID != strings.ToLower(request.ExpectedSnapshotID) {
		return websiteapi.Snapshot{}, websiteapi.ErrConflict
	}
	return s.CreateSnapshot(ctx, requestToCreate)
}

func (s *Service) loadPageTemplate(ctx context.Context, templateID string) (websiteapi.PageTemplate, core.ModuleDataRecord, error) {
	key := websiterepo.PageTemplateKey(templateID)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return websiteapi.PageTemplate{}, core.ModuleDataRecord{}, mapCoreError(err)
	}
	value, err := decodePageTemplate(key, record)
	return value, record, err
}

func decodePageTemplate(key core.ModuleDataKey, record core.ModuleDataRecord) (websiteapi.PageTemplate, error) {
	var value websiteapi.PageTemplate
	var index struct {
		RecordType string `json:"record_type"`
		TemplateID string `json:"template_id"`
		Revision   int64  `json:"revision"`
		Digest     string `json:"digest"`
	}
	if key.Namespace != websiterepo.TemplateNamespace || key.ScopeType != "global" || key.ScopeID != "global" || record.PayloadVersion != pageTemplateRecordVersion || record.Revision < 1 ||
		json.Unmarshal(record.Payload, &value) != nil || json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "page_template" ||
		index.TemplateID != value.ID || index.Revision < 1 || index.Digest != value.Digest ||
		!snapshotIDPattern.MatchString(value.ID) || key.RecordKey != "template/"+value.ID || !validSnapshotName(value.Name) ||
		!validTemplateDescription(value.Description) || value.UpdatedAt.IsZero() || !sha256HexPattern.MatchString(value.Digest) {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	digest, err := pageTemplateDigest(value)
	if err != nil || digest != value.Digest {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	if _, err := validatePageTemplateConfiguration(value.Configuration); err != nil {
		return websiteapi.PageTemplate{}, websiteapi.ErrUnavailable
	}
	value.Revision = record.Revision
	return value, nil
}

func pageTemplateModuleDataValue(value websiteapi.PageTemplate) (*core.ModuleDataValue, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	index, err := json.Marshal(map[string]any{"record_type": "page_template", "template_id": value.ID, "revision": value.Revision, "digest": value.Digest})
	if err != nil {
		return nil, websiteapi.ErrUnavailable
	}
	return &core.ModuleDataValue{Payload: payload, PayloadVersion: pageTemplateRecordVersion, IndexData: index}, nil
}

func (s *Service) validateTemplateConfiguration(ctx context.Context, configuration websiteapi.PageTemplateConfiguration) (websiteapi.PageTemplateConfiguration, error) {
	canonical, err := validatePageTemplateConfiguration(configuration)
	if err != nil {
		return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
	}
	page, err := snapshotPageFromConfig(canonical.Payload.Config)
	if err != nil {
		return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
	}
	hasBindings := false
	for _, placement := range page.Placements {
		if len(placement.DataBindings) != 0 {
			hasBindings = true
		}
	}
	if hasBindings {
		schema, err := s.readContentModel(ctx)
		if err != nil {
			if errors.Is(err, websiteapi.ErrNotFound) {
				return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrConflict
			}
			return websiteapi.PageTemplateConfiguration{}, err
		}
		entities, err := dataModelIndex(schema.DataModel)
		if err != nil {
			return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrUnavailable
		}
		for _, placement := range page.Placements {
			for _, binding := range placement.DataBindings {
				if _, err := convertPlacementBinding(binding, entities); err != nil {
					return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrConflict
				}
			}
		}
	}
	return canonical, nil
}

func validatePageTemplateConfiguration(configuration websiteapi.PageTemplateConfiguration) (websiteapi.PageTemplateConfiguration, error) {
	if configuration.SchemaVersion != 1 || !validJSONObjectWithin(configuration.Payload.Config, maxSnapshotConfigBytes) ||
		!validJSONObjectWithin(configuration.Payload.Metadata, maxSnapshotMetadataBytes) {
		return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
	}
	canonical := configuration
	if canonical.FeaturePins == nil {
		canonical.FeaturePins = map[string]string{}
	} else {
		pinsCopy := make(map[string]string, len(canonical.FeaturePins))
		for feature, version := range canonical.FeaturePins {
			pinsCopy[feature] = version
		}
		canonical.FeaturePins = pinsCopy
	}
	if canonical.PlacementSlots == nil {
		canonical.PlacementSlots = []websiteapi.PlacementSlot{}
	}
	var err error
	canonical.Payload.Config, err = canonicalJSONObject(configuration.Payload.Config)
	if err != nil {
		return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
	}
	canonical.Payload.Metadata, err = canonicalJSONObject(configuration.Payload.Metadata)
	if err != nil {
		return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
	}
	page, err := snapshotPageFromConfig(canonical.Payload.Config)
	if err != nil || !validSnapshotName(page.Name) {
		return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
	}
	pins, err := featurePinsFromConfig(canonical.Payload.Config)
	if err != nil || !sameFeaturePins(pins, canonical.FeaturePins) {
		return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
	}
	seen := make(map[string]struct{}, len(canonical.PlacementSlots))
	for _, slot := range canonical.PlacementSlots {
		if !templateSlotIDPattern.MatchString(slot.ID) || !validSnapshotName(slot.Label) {
			return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
		}
		if _, exists := seen[slot.ID]; exists {
			return websiteapi.PageTemplateConfiguration{}, websiteapi.ErrInvalid
		}
		seen[slot.ID] = struct{}{}
	}
	return canonical, nil
}

func materializeTemplate(configuration websiteapi.PageTemplateConfiguration, pageID, name string) (websiteapi.SnapshotPayload, error) {
	if !snapshotIDPattern.MatchString(pageID) || !validSnapshotName(name) {
		return websiteapi.SnapshotPayload{}, websiteapi.ErrInvalid
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(configuration.Payload.Config, &root); err != nil || root == nil {
		return websiteapi.SnapshotPayload{}, websiteapi.ErrInvalid
	}
	pageRaw, hasPage := root["page"]
	if hasPage {
		var page map[string]json.RawMessage
		if json.Unmarshal(pageRaw, &page) != nil || page == nil || len(root["pages"]) != 0 {
			return websiteapi.SnapshotPayload{}, websiteapi.ErrInvalid
		}
		id, _ := json.Marshal(pageID)
		pageName, _ := json.Marshal(name)
		page["id"], page["name"] = id, pageName
		root["page"], _ = json.Marshal(page)
	} else {
		var pages []map[string]json.RawMessage
		if json.Unmarshal(root["pages"], &pages) != nil || len(pages) != 1 || pages[0] == nil {
			return websiteapi.SnapshotPayload{}, websiteapi.ErrInvalid
		}
		id, _ := json.Marshal(pageID)
		pageName, _ := json.Marshal(name)
		pages[0]["id"], pages[0]["name"] = id, pageName
		root["pages"], _ = json.Marshal(pages)
	}
	encoded, err := json.Marshal(root)
	if err != nil || len(encoded) > maxSnapshotConfigBytes {
		return websiteapi.SnapshotPayload{}, websiteapi.ErrTooLarge
	}
	return websiteapi.SnapshotPayload{Config: encoded, Metadata: append(json.RawMessage(nil), configuration.Payload.Metadata...)}, nil
}

func addTemplateMetadata(raw json.RawMessage, field, templateID string, revision, contentRevision int64) (json.RawMessage, error) {
	if field != "template_source" && field != "template_application" {
		return nil, websiteapi.ErrInvalid
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(raw, &metadata) != nil || metadata == nil {
		return nil, websiteapi.ErrInvalid
	}
	provenance := map[string]any{"template_id": templateID, "template_revision": revision}
	if field == "template_application" {
		provenance["expected_content_revision"] = contentRevision
		provenance["selection_policy"] = "reset"
	}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		return nil, err
	}
	metadata[field] = encoded
	return json.Marshal(metadata)
}

func pageTemplateDigest(value websiteapi.PageTemplate) (string, error) {
	payload, err := json.Marshal(struct {
		ID            string                               `json:"id"`
		Name          string                               `json:"name"`
		Description   string                               `json:"description"`
		Revision      int64                                `json:"revision"`
		Configuration websiteapi.PageTemplateConfiguration `json:"configuration"`
	}{value.ID, value.Name, value.Description, value.Revision, value.Configuration})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func templateCreateFingerprint(request websiteapi.CreatePageTemplateRequest) (string, error) {
	payload, err := json.Marshal(templateCreateFingerprintInput{Name: request.Name, Description: request.Description, Configuration: request.Configuration})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) readTemplateCreateReceipt(ctx context.Context, idempotencyKey, fingerprint string) (websiteapi.PageTemplate, bool, error) {
	record, err := s.store.Get(ctx, websiterepo.PageTemplateCreateReceiptKey(idempotencyKey))
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return websiteapi.PageTemplate{}, false, nil
		}
		return websiteapi.PageTemplate{}, false, mapCoreError(err)
	}
	var receipt templateCreateReceipt
	var index struct {
		RecordType string `json:"record_type"`
		TemplateID string `json:"template_id"`
		Revision   int64  `json:"revision"`
		Digest     string `json:"digest"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &receipt) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "template_idempotency" || index.Revision < 1 || index.TemplateID != receipt.TemplateID || !snapshotIDPattern.MatchString(receipt.TemplateID) || !sha256HexPattern.MatchString(receipt.Fingerprint) {
		return websiteapi.PageTemplate{}, false, websiteapi.ErrUnavailable
	}
	if receipt.Fingerprint != fingerprint {
		return websiteapi.PageTemplate{}, false, websiteapi.ErrConflict
	}
	value, _, err := s.loadPageTemplate(ctx, receipt.TemplateID)
	if err != nil {
		return websiteapi.PageTemplate{}, false, err
	}
	return value, true, nil
}

func encodeTemplateCursor(cursor templateCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeTemplateCursor(value string) (templateCursor, error) {
	if len(value) > 1024 {
		return templateCursor{}, websiteapi.ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return templateCursor{}, err
	}
	var cursor templateCursor
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Version != pageCursorVersion || len(cursor.After) > 256 {
		return templateCursor{}, websiteapi.ErrInvalid
	}
	return cursor, nil
}

func validCursorAfter(after, prefix string) bool {
	if after == "" {
		return true
	}
	return strings.HasPrefix(after, prefix) && snapshotIDPattern.MatchString(strings.TrimPrefix(after, prefix))
}

func validTemplateDescription(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxTemplateDescription && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

var _ websiteapi.PageAPI = (*Service)(nil)
