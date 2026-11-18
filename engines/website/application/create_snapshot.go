package application

import (
	"context"
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
)

var snapshotIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)

const (
	maxSnapshotConfigBytes       = 8 << 20
	maxSnapshotMetadataBytes     = 1 << 20
	maxSnapshotDesignSystemBytes = 4 << 20
)

type snapshotCreateFingerprintInput struct {
	SiteID                  string                     `json:"site_id"`
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
	if _, exists := s.sites[request.SiteID]; !exists {
		return websiteapi.Snapshot{}, websiteapi.ErrNotFound
	}
	if !validSnapshotName(request.Name) || !snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) ||
		!validJSONObjectWithin(request.Payload.Config, maxSnapshotConfigBytes) || !validJSONObjectWithin(request.Payload.Metadata, maxSnapshotMetadataBytes) ||
		!validJSONObjectWithin(request.Payload.DesignSystem, maxSnapshotDesignSystemBytes) {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	if request.ParentID == "" {
		if request.ExpectedRevision != 0 || request.ExpectedContentRevision != 0 {
			return websiteapi.Snapshot{}, websiteapi.ErrInvalid
		}
	} else {
		if !snapshotIDPattern.MatchString(request.ParentID) || request.ExpectedRevision < 1 || request.ExpectedContentRevision < 0 {
			return websiteapi.Snapshot{}, websiteapi.ErrInvalid
		}
		request.ParentID = strings.ToLower(request.ParentID)
	}
	featurePins, err := featurePinsFromConfig(request.Payload.Config)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrInvalid
	}
	fingerprint, err := snapshotCreateFingerprint(request)
	if err != nil {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}

	if existing, found, err := s.readSnapshotCreateReceipt(ctx, request.SiteID, request.IdempotencyKey, fingerprint); err != nil {
		return websiteapi.Snapshot{}, err
	} else if found {
		return existing, nil
	}
	if s.store.MaxBatchSize() < 3 {
		return websiteapi.Snapshot{}, websiteapi.ErrTooLarge
	}
	registryRecord, registryState, err := s.loadSiteRegistry(ctx, request.SiteID)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	registryState.LastSequence, err = s.ensureSiteSequence(ctx, request.SiteID, registryState)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	if registryState.LastSequence == math.MaxInt64 {
		return websiteapi.Snapshot{}, websiteapi.ErrUnavailable
	}

	var parent *websiteapi.Snapshot
	if request.ParentID != "" {
		value, record, err := s.snapshotInSite(ctx, request.SiteID, request.ParentID)
		if err != nil {
			return websiteapi.Snapshot{}, err
		}
		if value.State != "ready" || record.Revision != request.ExpectedRevision {
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
		ID: snapshotID, SiteID: request.SiteID, ParentID: request.ParentID, Name: request.Name,
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
		"record_type": "snapshot", "state": state, "revision": int64(1), "parent_id": request.ParentID,
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
	registryMutation, err := siteRegistryMutation(request.SiteID, registryRecord, snapshot.Sequence)
	if err != nil {
		return websiteapi.Snapshot{}, err
	}
	snapshotKey := snapshotKey(request.SiteID, snapshot.ID)
	receiptKey := snapshotCreateReceiptKey(request.SiteID, request.IdempotencyKey)
	mutations := []core.ModuleDataMutation{
		{Key: snapshotKey, ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: snapshotPayload, PayloadVersion: 1, IndexData: snapshotIndex}},
		{Key: receiptKey, ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: receiptPayload, PayloadVersion: 1, IndexData: receiptIndex}},
		registryMutation,
	}
	if err := s.store.Batch(ctx, mutations); err != nil {
		mapped := mapCoreError(err)
		if errors.Is(mapped, websiteapi.ErrConflict) {
			if existing, found, readErr := s.readSnapshotCreateReceipt(ctx, request.SiteID, request.IdempotencyKey, fingerprint); readErr != nil {
				return websiteapi.Snapshot{}, readErr
			} else if found {
				return existing, nil
			}
		}
		return websiteapi.Snapshot{}, mapped
	}
	return snapshot, nil
}

func (s *Service) readSnapshotCreateReceipt(ctx context.Context, siteID, idempotencyKey, fingerprint string) (websiteapi.Snapshot, bool, error) {
	key := snapshotCreateReceiptKey(siteID, idempotencyKey)
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
	if record.PayloadVersion != 1 || record.Revision != 1 || json.Unmarshal(record.Payload, &receipt) != nil || json.Unmarshal(record.IndexData, &index) != nil ||
		index.RecordType != "snapshot_idempotency" || index.State != "created" || index.Revision != record.Revision || index.SnapshotID != receipt.SnapshotID ||
		!snapshotIDPattern.MatchString(receipt.SnapshotID) || !sha256HexPattern.MatchString(receipt.Fingerprint) {
		return websiteapi.Snapshot{}, false, websiteapi.ErrUnavailable
	}
	if receipt.Fingerprint != fingerprint {
		return websiteapi.Snapshot{}, false, websiteapi.ErrConflict
	}
	snapshot, _, err := s.snapshotInSite(ctx, siteID, receipt.SnapshotID)
	if err != nil {
		if errors.Is(err, websiteapi.ErrNotFound) {
			return websiteapi.Snapshot{}, false, websiteapi.ErrConflict
		}
		return websiteapi.Snapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Service) maxSnapshotSequence(ctx context.Context, siteID string) (int64, error) {
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}}}
	after := ""
	var maximum int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "site", siteID, after, selectors, s.store.MaxPageSize())
		if err != nil {
			return 0, mapCoreError(err)
		}
		if len(keys) != len(records) {
			return 0, websiteapi.ErrUnavailable
		}
		for i, record := range records {
			snapshot, err := decodeSnapshotRecord(siteID, keys[i], record)
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
	if canonicalPayload.DesignSystem, err = canonicalJSONObject(request.Payload.DesignSystem); err != nil {
		return "", err
	}
	data, err := json.Marshal(snapshotCreateFingerprintInput{
		SiteID: request.SiteID, ParentID: request.ParentID, Name: request.Name,
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
		DesignSystem: append(json.RawMessage(nil), payload.DesignSystem...),
	}
}

func snapshotCreateReceiptKey(siteID, idempotencyKey string) core.ModuleDataKey {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return core.ModuleDataKey{Namespace: snapshotsNamespace, ScopeType: "site", ScopeID: siteID, RecordKey: "idempotency/" + hex.EncodeToString(digest[:])}
}

var sha256HexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
