package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
)

var snapshotIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)

const maxSnapshotConfigBytes = 8 << 20
const maxSnapshotMetadataBytes = 1 << 20

type snapshotCreateFingerprintInput struct {
	PageID                  string                     `json:"page_id"`
	ParentID                string                     `json:"parent_id,omitempty"`
	Name                    string                     `json:"name"`
	ExpectedRevision        int64                      `json:"expected_revision"`
	ExpectedContentRevision int64                      `json:"expected_content_revision"`
	Payload                 websiteapi.SnapshotPayload `json:"payload"`
}

type snapshotCreateReceipt struct {
	Fingerprint string `json:"fingerprint"`
	SnapshotID  string `json:"snapshot_id"`
}

func (s *Service) CreateSnapshot(ctx context.Context, request websiteapi.CreateSnapshotRequest) (websiteapi.Snapshot, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.Snapshot{}, err
	}
	if !identifierPattern.MatchString(request.PageID) {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	pageRecordValue, pageRecord, err := s.loadPage(ctx, request.PageID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	request.PageID = pageRecordValue.ID
	if !validSnapshotName(request.Name) || !snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) ||
		!validJSONObjectWithin(request.Payload.Config, maxSnapshotConfigBytes) || !validJSONObjectWithin(request.Payload.Metadata, maxSnapshotMetadataBytes) {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	if request.ParentID == "" {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	if !snapshotIDPattern.MatchString(request.ParentID) || request.ExpectedRevision < 1 || request.ExpectedContentRevision < 0 {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	request.ParentID = strings.ToLower(request.ParentID)
	featurePins, err := featurePinsFromConfig(request.Payload.Config)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	page, err := snapshotPageFromConfig(request.Payload.Config)
	if err != nil || (page.ID != "" && page.ID != request.PageID) {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	fingerprint, err := snapshotCreateFingerprint(request)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}

	if existing, found, err := s.readSnapshotCreateReceipt(ctx, request.PageID, request.IdempotencyKey, fingerprint); err != nil {
		return websiteapi.Snapshot{}, err
	} else if found {
		return existing, nil
	}
	if s.store.MaxBatchSize() < 4 {
		return websiteapi.Snapshot{}, websiteapi.ErrTooLarge
	}
	if pageRecord.Revision == math.MaxInt64 {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	registryRecord, registryState, err := s.loadPageTreeRegistry(ctx, request.PageID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	registryState.LastSequence, err = s.ensurePageTreeSequence(ctx, request.PageID, registryState)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	if registryState.LastSequence == math.MaxInt64 {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}

	var parent *websiteapi.Snapshot
	if request.ParentID != "" {
		value, record, err := s.snapshotInPage(ctx, request.PageID, request.ParentID)
		if err != nil {
			return websiteapi.Snapshot{}, err
		}
		if value.State != "ready" || record.Revision != request.ExpectedRevision {
			return websiteapi.Snapshot{}, websiteapi.ErrConflict
		}
		if pageRecordValue.LatestSnapshotID != request.ParentID {
			return websiteapi.Snapshot{}, websiteapi.ErrConflict
		}
		parent = &value
	}
	if err := ctx.Err(); err != nil {
		return websiteapi.Snapshot{}, err
	}
	snapshotID, err := randomUUID()
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	now := time.Now().UTC()
	state := "ready"
	if parent != nil {
		state = "provisioning"
	}
	snapshot := websiteapi.Snapshot{
		ID: snapshotID, PageID: request.PageID, ParentID: request.ParentID, Name: request.Name,
		Sequence: registryState.LastSequence + 1, Revision: 1, FeaturePins: featurePins, State: state, CreatedAt: now,
		Payload: cloneSnapshotPayload(request.Payload),
	}
	if parent != nil {
		snapshot.ParentName = parent.Name
		snapshot.ParentSequence = parent.Sequence
	}
	snapshotPayload, err := json.Marshal(snapshot)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	if len(snapshotPayload) > s.store.MaxPayloadBytes() {
		return websiteapi.Snapshot{}, websiteapi.ErrTooLarge
	}
	snapshotIndex, err := json.Marshal(map[string]any{
		"record_type": "snapshot", "snapshot_id": snapshot.ID, "page_id": snapshot.PageID, "state": state, "revision": int64(1), "parent_id": request.ParentID,
	})
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	receipt := snapshotCreateReceipt{Fingerprint: fingerprint, SnapshotID: snapshot.ID}
	receiptPayload, err := json.Marshal(receipt)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	receiptIndex, err := json.Marshal(map[string]any{
		"record_type": "snapshot_idempotency", "state": "created", "snapshot_id": snapshot.ID, "revision": int64(1),
	})
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}
	registryMutation, err := pageTreeRegistryMutation(request.PageID, registryRecord, snapshot.Sequence)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	pageRecordValue.Name = snapshot.Name
	pageRecordValue.LatestSnapshotID = snapshot.ID
	pageRecordValue.LatestSnapshotState = snapshot.State
	pageValue, err := pageModuleDataValue(pageRecordValue, pageRecord.Revision+1)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	snapshotRecordKey := snapshotKey(request.PageID, snapshot.ID)
	receiptKey := snapshotCreateReceiptKey(request.PageID, request.IdempotencyKey)
	mutations := []core.ModuleDataMutation{
		{Key: snapshotRecordKey, ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: snapshotPayload, PayloadVersion: 1, IndexData: snapshotIndex}},
		{Key: receiptKey, ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: receiptPayload, PayloadVersion: 1, IndexData: receiptIndex}},
		registryMutation,
		{Key: websiterepo.PageKey(request.PageID), ExpectedRevision: pageRecord.Revision, Value: pageValue},
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		mapped := mapCoreError(err)
		if errors.Is(mapped, websiteapi.ErrConflict) {
			if existing, found, readErr := s.readSnapshotCreateReceipt(ctx, request.PageID, request.IdempotencyKey, fingerprint); readErr != nil {
				return websiteapi.Snapshot{}, readErr
			} else if found {
				return existing, nil
			}
		}
		return websiteapi.Snapshot{}, mapped
	}
	return snapshot, nil
}

func (s *Service) readSnapshotCreateReceipt(ctx context.Context, pageID, idempotencyKey, fingerprint string) (websiteapi.Snapshot, bool, error) {
	key := snapshotCreateReceiptKey(pageID, idempotencyKey)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return websiteapi.Snapshot{}, false, nil
		}
		return websiteapi.Snapshot{}, false, mapCoreError(err)
	}
	var receipt snapshotCreateReceipt
	var index struct {
		RecordType string `json:"record_type"`
		State      string `json:"state"`
		SnapshotID string `json:"snapshot_id"`
		Revision   int64  `json:"revision"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &receipt) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "snapshot_idempotency" || index.State != "created" || index.Revision < 1 || index.SnapshotID != receipt.SnapshotID ||
		!snapshotIDPattern.MatchString(receipt.SnapshotID) || !sha256HexPattern.MatchString(receipt.Fingerprint) {
		return websiteapi.Snapshot{}, false, websiteapi.ErrUnavailable
	}
	if receipt.Fingerprint != fingerprint {
		return websiteapi.Snapshot{}, false, websiteapi.ErrConflict
	}
	snapshot, _, err := s.snapshotInPage(ctx, pageID, receipt.SnapshotID)
	if err != nil {
		if errors.Is(err, websiteapi.ErrNotFound) {
			return websiteapi.Snapshot{}, false, websiteapi.ErrConflict
		}
		return websiteapi.Snapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Service) maxSnapshotSequence(ctx context.Context, pageID string) (int64, error) {
	selectors := []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}},
		{Field: "page_id", Operator: core.SelectorEqual, Values: []any{pageID}},
	}
	after := ""
	var maximum int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "global", "global", after, selectors, s.store.MaxPageSize())
		if err != nil {
			return 0, mapCoreError(err)
		}
		if len(keys) != len(records) {
			return 0, websiteapi.ErrUnavailable
		}
		for i, record := range records {
			snapshot, err := decodeSnapshotRecord(keys[i], record)
			if err != nil {
				return 0, err
			}
			if snapshot.Sequence > maximum {
				maximum = snapshot.Sequence
			}
		}
		if next == "" {
			return maximum, nil
		}
		if next == after {
			return 0, websiteapi.ErrUnavailable
		}
		after = next
	}
}

func snapshotCreateFingerprint(request websiteapi.CreateSnapshotRequest) (string, error) {
	canonicalPayload := request.Payload
	var err error
	if canonicalPayload.Config, err = canonicalJSONObject(request.Payload.Config); err != nil {
		return "", err
	}
	if canonicalPayload.Metadata, err = canonicalJSONObject(request.Payload.Metadata); err != nil {
		return "", err
	}
	data, err := json.Marshal(snapshotCreateFingerprintInput{
		PageID: request.PageID, ParentID: request.ParentID, Name: request.Name,
		ExpectedRevision: request.ExpectedRevision, ExpectedContentRevision: request.ExpectedContentRevision, Payload: canonicalPayload,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalJSONObject(raw json.RawMessage) (json.RawMessage, error) {
	if !validJSONObject(raw) {
		return nil, websiteapi.ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validJSONObjectWithin(raw json.RawMessage, maximum int) bool {
	return len(raw) <= maximum && validJSONObject(raw)
}

func cloneSnapshotPayload(payload websiteapi.SnapshotPayload) *websiteapi.SnapshotPayload {
	return &websiteapi.SnapshotPayload{
		Config: append(json.RawMessage(nil), payload.Config...), Metadata: append(json.RawMessage(nil), payload.Metadata...),
	}
}

func snapshotCreateReceiptKey(pageID, idempotencyKey string) core.ModuleDataKey {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "global", ScopeID: "global",
		RecordKey: "idempotency/" + pageID + "/" + hex.EncodeToString(digest[:])}
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	var text [36]byte
	hex.Encode(text[0:8], value[0:4])
	text[8] = '-'
	hex.Encode(text[9:13], value[4:6])
	text[13] = '-'
	hex.Encode(text[14:18], value[6:8])
	text[18] = '-'
	hex.Encode(text[19:23], value[8:10])
	text[23] = '-'
	hex.Encode(text[24:36], value[10:16])
	return string(text[:]), nil
}

var sha256HexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
