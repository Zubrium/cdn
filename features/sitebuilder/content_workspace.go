package sitebuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"websiteengine/pkg/platform/validate"
)

// SnapshotContentDraftItem is the editable copy of one record pinned by a
// snapshot. VersionID is kept only when the record still matches that immutable
// source version; edits are materialized as new versions when a child snapshot
// is created.
type SnapshotContentDraftItem struct {
	EntryID    uuid.UUID       `json:"entryId"`
	VersionID  *uuid.UUID      `json:"versionId,omitempty"`
	Version    int             `json:"version,omitempty"`
	SortOrder  int             `json:"sortOrder"`
	SingleSlot bool            `json:"singleSlot,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type SnapshotContentWorkspace struct {
	Snapshot SnapshotRecord             `json:"snapshot"`
	Feature  ContentFeature             `json:"feature"`
	Revision int64                      `json:"revision"`
	Items    []SnapshotContentDraftItem `json:"items"`
}

type SnapshotContentDraftInput struct {
	EntryID    uuid.UUID       `json:"entryId"`
	SortOrder  int             `json:"sortOrder"`
	SingleSlot bool            `json:"singleSlot,omitempty"`
	Data       json.RawMessage `json:"data"`
}

func (s *SnapshotService) ContentSnapshots(ctx context.Context) ([]SnapshotRecord, error) {
	return s.List(ctx, 201, nil)
}

func (s *SnapshotService) ContentCatalogForSnapshot(ctx context.Context, snapshotID uuid.UUID) ([]ContentFeature, error) {
	_, payload, err := s.snapshotPayload(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	items, err := s.contentFeaturesForBuilds(ctx, payload.Builds)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := s.pool.QueryRow(ctx, `SELECT revision FROM site_snapshot_content_drafts WHERE snapshot_id=$1 AND feature_key=$2`, snapshotID, items[i].Key).Scan(&items[i].DraftRevision); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	return items, nil
}

func (s *SnapshotService) ContentWorkspace(ctx context.Context, snapshotID uuid.UUID, featureKey string) (SnapshotContentWorkspace, error) {
	record, payload, err := s.snapshotPayload(ctx, snapshotID)
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	build, ok := pinnedBuildForKey(payload.Builds, featureKey)
	if !ok {
		return SnapshotContentWorkspace{}, ErrNotFound
	}
	feature, err := s.contentFeatureForBuild(ctx, build)
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	base, err := s.snapshotFeatureContent(ctx, snapshotID, featureKey)
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	var revision int64
	var raw []byte
	err = s.pool.QueryRow(ctx, `SELECT revision,items FROM site_snapshot_content_drafts WHERE snapshot_id=$1 AND feature_key=$2`, snapshotID, featureKey).Scan(&revision, &raw)
	if err == nil {
		var items []SnapshotContentDraftItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return SnapshotContentWorkspace{}, err
		}
		base = items
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SnapshotContentWorkspace{}, err
	}
	if base == nil {
		base = []SnapshotContentDraftItem{}
	}
	return SnapshotContentWorkspace{Snapshot: record, Feature: feature, Revision: revision, Items: base}, nil
}

func (s *SnapshotService) SaveContentWorkspace(ctx context.Context, actor uuid.UUID, snapshotID uuid.UUID, featureKey string, expectedRevision int64, input []SnapshotContentDraftInput) (SnapshotContentWorkspace, error) {
	if expectedRevision < 0 {
		return SnapshotContentWorkspace{}, fmt.Errorf("%w: revision must not be negative", ErrInvalid)
	}
	record, payload, err := s.snapshotPayload(ctx, snapshotID)
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	build, ok := pinnedBuildForKey(payload.Builds, featureKey)
	if !ok {
		return SnapshotContentWorkspace{}, ErrNotFound
	}
	var manifest Manifest
	if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
		return SnapshotContentWorkspace{}, err
	}
	base, err := s.snapshotFeatureContent(ctx, snapshotID, featureKey)
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	var existing []SnapshotContentDraftItem
	var existingRevision int64
	var existingRaw []byte
	err = s.pool.QueryRow(ctx, `SELECT revision,items FROM site_snapshot_content_drafts WHERE snapshot_id=$1 AND feature_key=$2`, snapshotID, featureKey).Scan(&existingRevision, &existingRaw)
	if err == nil {
		if err := json.Unmarshal(existingRaw, &existing); err != nil {
			return SnapshotContentWorkspace{}, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SnapshotContentWorkspace{}, err
	}
	if existing == nil {
		existing = base
	}
	allowed := make(map[uuid.UUID]SnapshotContentDraftItem, len(existing)+len(base))
	for _, item := range base {
		allowed[item.EntryID] = item
	}
	for _, item := range existing {
		allowed[item.EntryID] = item
	}
	items := make([]SnapshotContentDraftItem, 0, len(input))
	seen := make(map[uuid.UUID]bool, len(input))
	for i, candidate := range input {
		if candidate.EntryID == uuid.Nil {
			candidate.EntryID = uuid.New()
		}
		if seen[candidate.EntryID] {
			return SnapshotContentWorkspace{}, fmt.Errorf("%w: duplicate entry id %s", ErrInvalid, candidate.EntryID)
		}
		seen[candidate.EntryID] = true
		prior, wasKnown := allowed[candidate.EntryID]
		if !wasKnown {
			var exists bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM feature_entries WHERE id=$1)`, candidate.EntryID).Scan(&exists); err != nil {
				return SnapshotContentWorkspace{}, err
			}
			if exists {
				return SnapshotContentWorkspace{}, fmt.Errorf("%w: record %s is not part of this snapshot", ErrInvalid, candidate.EntryID)
			}
		}
		candidate.SortOrder = i
		if len(candidate.Data) == 0 || !json.Valid(candidate.Data) {
			return SnapshotContentWorkspace{}, fmt.Errorf("%w: record %s has invalid data", ErrInvalid, candidate.EntryID)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(candidate.Data, &object); err != nil || object == nil {
			return SnapshotContentWorkspace{}, fmt.Errorf("%w: record %s data must be an object", ErrInvalid, candidate.EntryID)
		}
		schema := manifest.Content.Schema
		if candidate.SingleSlot && manifest.Content.Mode == "collection" {
			if len(manifest.Content.SettingsSchema) == 0 {
				return SnapshotContentWorkspace{}, fmt.Errorf("%w: feature %q does not have collection settings", ErrInvalid, featureKey)
			}
			schema = manifest.Content.SettingsSchema
		}
		if (manifest.Content.Mode == "single" || manifest.Content.Mode == "form") && !candidate.SingleSlot {
			return SnapshotContentWorkspace{}, fmt.Errorf("%w: %s content must use its single content slot", ErrInvalid, manifest.Content.Mode)
		}
		if manifest.Content.Mode == "collection" && wasKnown && candidate.SingleSlot != prior.SingleSlot {
			return SnapshotContentWorkspace{}, fmt.Errorf("%w: record %s cannot change between settings and collection content", ErrInvalid, candidate.EntryID)
		}
		fieldErrors, err := validate.Validate(schema, candidate.Data)
		if err != nil {
			return SnapshotContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if len(fieldErrors) > 0 {
			return SnapshotContentWorkspace{}, fmt.Errorf("%w: %s: %s", ErrInvalid, fieldErrors[0].Path, fieldErrors[0].Message)
		}
		item := SnapshotContentDraftItem{
			EntryID: candidate.EntryID, SortOrder: candidate.SortOrder,
			SingleSlot: candidate.SingleSlot, Data: candidate.Data,
		}
		if wasKnown && jsonEqual(candidate.Data, prior.Data) && candidate.SingleSlot == prior.SingleSlot {
			if prior.VersionID != nil {
				item.VersionID = prior.VersionID
			}
			item.Version = prior.Version
		}
		items = append(items, item)
	}
	if manifest.Content.Mode != "collection" && len(items) > 1 {
		return SnapshotContentWorkspace{}, fmt.Errorf("%w: %s content allows only one record", ErrInvalid, manifest.Content.Mode)
	}
	if manifest.Content.Mode != "collection" && len(items) != 1 {
		return SnapshotContentWorkspace{}, fmt.Errorf("%w: %s content requires one record", ErrInvalid, manifest.Content.Mode)
	}
	settingsCount := 0
	for _, item := range items {
		if item.SingleSlot {
			settingsCount++
		}
	}
	if settingsCount > 1 {
		return SnapshotContentWorkspace{}, fmt.Errorf("%w: only one settings record is allowed", ErrInvalid)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	var revision int64
	if expectedRevision == 0 {
		err = s.pool.QueryRow(ctx, `
			INSERT INTO site_snapshot_content_drafts(snapshot_id,feature_key,revision,items,updated_by)
			VALUES($1,$2,1,$3::jsonb,$4)
			ON CONFLICT(snapshot_id,feature_key) DO NOTHING
			RETURNING revision`, snapshotID, featureKey, string(encoded), actor).Scan(&revision)
	} else {
		err = s.pool.QueryRow(ctx, `
			UPDATE site_snapshot_content_drafts
			SET revision=revision+1,items=$3::jsonb,updated_by=$4,updated_at=now()
			WHERE snapshot_id=$1 AND feature_key=$2 AND revision=$5
			RETURNING revision`, snapshotID, featureKey, string(encoded), actor, expectedRevision).Scan(&revision)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return SnapshotContentWorkspace{}, fmt.Errorf("%w: content draft changed; reload before saving", ErrConflict)
	}
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	feature, err := s.contentFeatureForBuild(ctx, build)
	if err != nil {
		return SnapshotContentWorkspace{}, err
	}
	return SnapshotContentWorkspace{Snapshot: record, Feature: feature, Revision: revision, Items: items}, nil
}

// ForkSnapshotFromContentDraft materializes every saved feature overlay as a
// new immutable snapshot. It never changes the source snapshot or active
// environment pointers.
func (s *SnapshotService) ForkSnapshotFromContentDraft(ctx context.Context, actor, sourceID uuid.UUID, name string) (SnapshotRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SnapshotRecord{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var source SnapshotRecord
	var payloadRaw []byte
	if err := tx.QueryRow(ctx, `SELECT id,parent_snapshot_id,sequence,name,digest,created_at,payload FROM site_snapshots WHERE id=$1`, sourceID).Scan(&source.ID, &source.ParentID, &source.Sequence, &source.Name, &source.Digest, &source.CreatedAt, &payloadRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SnapshotRecord{}, ErrNotFound
		}
		return SnapshotRecord{}, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return SnapshotRecord{}, err
	}
	content, err := snapshotContentTx(ctx, tx, sourceID)
	if err != nil {
		return SnapshotRecord{}, err
	}
	drafts, err := tx.Query(ctx, `SELECT feature_key,items FROM site_snapshot_content_drafts WHERE snapshot_id=$1 ORDER BY feature_key FOR UPDATE`, sourceID)
	if err != nil {
		return SnapshotRecord{}, err
	}
	draftMap := make(map[string][]SnapshotContentDraftItem)
	for drafts.Next() {
		var key string
		var raw []byte
		if err := drafts.Scan(&key, &raw); err != nil {
			drafts.Close()
			return SnapshotRecord{}, err
		}
		var items []SnapshotContentDraftItem
		if err := json.Unmarshal(raw, &items); err != nil {
			drafts.Close()
			return SnapshotRecord{}, err
		}
		draftMap[key] = items
	}
	if err := drafts.Err(); err != nil {
		drafts.Close()
		return SnapshotRecord{}, err
	}
	drafts.Close()
	if len(draftMap) == 0 {
		return SnapshotRecord{}, fmt.Errorf("%w: there are no saved content changes for this snapshot", ErrInvalid)
	}
	byFeature := make(map[string][]SnapshotContent, len(payload.Builds))
	for _, item := range content {
		byFeature[item.FeatureKey] = append(byFeature[item.FeatureKey], item)
	}
	for key, draftItems := range draftMap {
		build, ok := pinnedBuildForKey(payload.Builds, key)
		if !ok {
			return SnapshotRecord{}, fmt.Errorf("%w: draft feature %q is no longer part of source snapshot", ErrInvalid, key)
		}
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return SnapshotRecord{}, err
		}
		featureItems := make([]SnapshotContent, 0, len(draftItems))
		for _, draftItem := range draftItems {
			schema := manifest.Content.Schema
			if draftItem.SingleSlot && manifest.Content.Mode == "collection" {
				schema = manifest.Content.SettingsSchema
			}
			fieldErrors, err := validate.Validate(schema, draftItem.Data)
			if err != nil || len(fieldErrors) > 0 {
				if err != nil {
					return SnapshotRecord{}, fmt.Errorf("%w: %s content: %v", ErrInvalid, key, err)
				}
				return SnapshotRecord{}, fmt.Errorf("%w: %s content %s: %s", ErrInvalid, key, fieldErrors[0].Path, fieldErrors[0].Message)
			}
			versionID := uuid.Nil
			if draftItem.VersionID != nil {
				versionID = *draftItem.VersionID
			}
			featureItems = append(featureItems, SnapshotContent{EntryID: draftItem.EntryID, VersionID: versionID, Version: draftItem.Version, FeatureKey: key, SortOrder: draftItem.SortOrder, SingleSlot: draftItem.SingleSlot, Data: draftItem.Data})
		}
		if manifest.Content.Mode != "collection" && len(featureItems) > 1 {
			return SnapshotRecord{}, fmt.Errorf("%w: feature %q allows only one content record", ErrInvalid, key)
		}
		byFeature[key] = featureItems
	}
	content = content[:0]
	for _, build := range payload.Builds {
		content = append(content, byFeature[build.Key]...)
	}
	sort.SliceStable(content, func(i, j int) bool {
		if content[i].FeatureKey == content[j].FeatureKey {
			return content[i].SortOrder < content[j].SortOrder
		}
		return content[i].FeatureKey < content[j].FeatureKey
	})
	if err := validateDetailKeys(payload.Config, payload.Builds, content); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.prepareSnapshotMedia(ctx, payload.Config, payload.Builds, content); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if name == "" {
		name = source.Name + " · редакция"
	}
	if len([]rune(name)) > 160 {
		name = string([]rune(name)[:160])
	}
	newID := uuid.New()
	// Materialize immutable entry/version IDs before computing the digest so it
	// describes exactly the rows that will be pinned by the child snapshot.
	for index := range content {
		item := &content[index]
		build, _ := pinnedBuildForKey(payload.Builds, item.FeatureKey)
		versionID, entryID, err := s.materializeSnapshotDraftItem(ctx, tx, actor, build, *item)
		if err != nil {
			return SnapshotRecord{}, err
		}
		item.EntryID, item.VersionID = entryID, versionID
		if err := tx.QueryRow(ctx, `SELECT version FROM feature_entry_versions WHERE id=$1`, versionID).Scan(&item.Version); err != nil {
			return SnapshotRecord{}, err
		}
	}
	checksumInput, err := json.Marshal(struct {
		Payload SnapshotPayload   `json:"payload"`
		Content []SnapshotContent `json:"content"`
	}{Payload: payload, Content: content})
	if err != nil {
		return SnapshotRecord{}, err
	}
	digestBytes := sha256.Sum256(checksumInput)
	digest := hex.EncodeToString(digestBytes[:])
	if _, err := tx.Exec(ctx, `INSERT INTO site_snapshots(id,parent_snapshot_id,name,digest,payload,created_by) VALUES($1,$2,$3,$4,$5::jsonb,$6)`, newID, sourceID, name, digest, string(payloadRaw), actor); err != nil {
		return SnapshotRecord{}, err
	}
	for index := range content {
		item := &content[index]
		if _, err := tx.Exec(ctx, `INSERT INTO site_snapshot_content(snapshot_id,feature_key,entry_id,version_id,sort_order,data) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, newID, item.FeatureKey, item.EntryID, item.VersionID, item.SortOrder, string(item.Data)); err != nil {
			return SnapshotRecord{}, err
		}
	}
	if err := tx.QueryRow(ctx, `SELECT sequence,created_at FROM site_snapshots WHERE id=$1`, newID).Scan(&source.Sequence, &source.CreatedAt); err != nil {
		return SnapshotRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotRecord{}, err
	}
	return SnapshotRecord{ID: newID, ParentID: &sourceID, Sequence: source.Sequence, Name: name, Digest: digest, CreatedAt: source.CreatedAt}, nil
}

func (s *SnapshotService) materializeSnapshotDraftItem(ctx context.Context, tx pgx.Tx, actor uuid.UUID, build PinnedBuild, item SnapshotContent) (uuid.UUID, uuid.UUID, error) {
	var manifest Manifest
	if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	featureID, err := featureIDTx(ctx, tx, build.Key)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	entryID := item.EntryID
	var entryFeatureID uuid.UUID
	var singleSlot bool
	err = tx.QueryRow(ctx, `SELECT feature_id,single_slot FROM feature_entries WHERE id=$1 FOR UPDATE`, entryID).Scan(&entryFeatureID, &singleSlot)
	if errors.Is(err, pgx.ErrNoRows) {
		if item.SingleSlot {
			var existingSlot uuid.UUID
			slotErr := tx.QueryRow(ctx, `SELECT id FROM feature_entries WHERE feature_id=$1 AND single_slot=TRUE AND archived_at IS NULL ORDER BY created_at,id LIMIT 1 FOR UPDATE`, featureID).Scan(&existingSlot)
			if slotErr == nil {
				entryID = existingSlot
			} else if !errors.Is(slotErr, pgx.ErrNoRows) {
				return uuid.Nil, uuid.Nil, slotErr
			}
		}
		if entryID == item.EntryID {
			if _, err := tx.Exec(ctx, `INSERT INTO feature_entries(id,feature_id,single_slot,revision,created_by,sort_order) VALUES($1,$2,$3,1,$4,$5)`, entryID, featureID, item.SingleSlot, actor, item.SortOrder); err != nil {
				return uuid.Nil, uuid.Nil, err
			}
		} else {
			singleSlot = true
		}
	} else if err != nil {
		return uuid.Nil, uuid.Nil, err
	} else if entryFeatureID != featureID || singleSlot != item.SingleSlot {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: record %s does not belong to feature %q", ErrInvalid, entryID, build.Key)
	}
	schemaVersionID := build.SchemaVersionID
	if item.SingleSlot && build.SettingsSchemaVersionID != nil {
		schemaVersionID = *build.SettingsSchemaVersionID
	}
	if item.VersionID != uuid.Nil {
		var currentSchema uuid.UUID
		var currentData []byte
		err := tx.QueryRow(ctx, `SELECT schema_version_id,data FROM feature_entry_versions WHERE id=$1 AND entry_id=$2`, item.VersionID, entryID).Scan(&currentSchema, &currentData)
		if err == nil && currentSchema == schemaVersionID && jsonEqual(currentData, item.Data) {
			return item.VersionID, entryID, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, uuid.Nil, err
		}
	}
	versionID := uuid.New()
	err = tx.QueryRow(ctx, `INSERT INTO feature_entry_versions(id,entry_id,version,schema_version_id,data,status,created_by,published_at)
		SELECT $1,$2,COALESCE(MAX(version),0)+1,$3,$4::jsonb,'published',$5,now() FROM feature_entry_versions WHERE entry_id=$2 RETURNING id`, versionID, entryID, schemaVersionID, string(item.Data), actor).Scan(&versionID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return versionID, entryID, nil
}

func jsonEqual(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return string(a) == string(b)
	}
	la, _ := json.Marshal(left)
	ra, _ := json.Marshal(right)
	return string(la) == string(ra)
}

func (s *SnapshotService) snapshotPayload(ctx context.Context, id uuid.UUID) (SnapshotRecord, SnapshotPayload, error) {
	var record SnapshotRecord
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id,parent_snapshot_id,sequence,name,digest,created_at,payload FROM site_snapshots WHERE id=$1`, id).Scan(&record.ID, &record.ParentID, &record.Sequence, &record.Name, &record.Digest, &record.CreatedAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return SnapshotRecord{}, SnapshotPayload{}, ErrNotFound
	}
	if err != nil {
		return SnapshotRecord{}, SnapshotPayload{}, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return SnapshotRecord{}, SnapshotPayload{}, err
	}
	return record, payload, nil
}

func (s *SnapshotService) contentFeaturesForBuilds(ctx context.Context, builds []PinnedBuild) ([]ContentFeature, error) {
	items := make([]ContentFeature, 0, len(builds))
	for _, build := range builds {
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return nil, err
		}
		if !manifestHasEditableContent(manifest.Content.Schema) && len(manifest.Content.SettingsSchema) == 0 && manifest.Content.Mode != "form" {
			continue
		}
		feature, err := s.contentFeatureForBuild(ctx, build)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, feature)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (s *SnapshotService) contentFeatureForBuild(ctx context.Context, build PinnedBuild) (ContentFeature, error) {
	var manifest Manifest
	if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
		return ContentFeature{}, err
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM features WHERE key=$1`, build.Key).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ContentFeature{}, ErrNotFound
		}
		return ContentFeature{}, err
	}
	feature := ContentFeature{ID: id, Key: build.Key, Name: build.Name, Category: manifest.Category, Mode: manifest.Content.Mode, Version: build.Version, BuildDigest: build.Digest, Schema: manifest.Content.Schema, Settings: manifest.Content.SettingsSchema}
	if submission := manifest.SubmissionSchema(); len(submission) > 0 {
		feature.Submission = submission
	}
	return feature, nil
}

func (s *SnapshotService) snapshotFeatureContent(ctx context.Context, snapshotID uuid.UUID, featureKey string) ([]SnapshotContentDraftItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT sc.entry_id,sc.version_id,v.version,sc.sort_order,e.single_slot,sc.data FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id JOIN feature_entry_versions v ON v.id=sc.version_id WHERE sc.snapshot_id=$1 AND sc.feature_key=$2 ORDER BY sc.sort_order,sc.entry_id`, snapshotID, featureKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SnapshotContentDraftItem{}
	for rows.Next() {
		var item SnapshotContentDraftItem
		var versionID uuid.UUID
		var data []byte
		if err := rows.Scan(&item.EntryID, &versionID, &item.Version, &item.SortOrder, &item.SingleSlot, &data); err != nil {
			return nil, err
		}
		item.VersionID = &versionID
		item.Data = data
		items = append(items, item)
	}
	return items, rows.Err()
}

func snapshotContentTx(ctx context.Context, tx pgx.Tx, snapshotID uuid.UUID) ([]SnapshotContent, error) {
	rows, err := tx.Query(ctx, `SELECT sc.feature_key,sc.entry_id,sc.version_id,sc.sort_order,e.single_slot,v.version,sc.data FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id JOIN feature_entry_versions v ON v.id=sc.version_id WHERE sc.snapshot_id=$1 ORDER BY sc.feature_key,sc.sort_order,sc.entry_id`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SnapshotContent{}
	for rows.Next() {
		var item SnapshotContent
		var data []byte
		if err := rows.Scan(&item.FeatureKey, &item.EntryID, &item.VersionID, &item.SortOrder, &item.SingleSlot, &item.Version, &data); err != nil {
			return nil, err
		}
		item.Data = data
		items = append(items, item)
	}
	return items, rows.Err()
}

func pinnedBuildForKey(builds []PinnedBuild, key string) (PinnedBuild, bool) {
	for _, build := range builds {
		if build.Key == key {
			return build, true
		}
	}
	return PinnedBuild{}, false
}

func featureIDTx(ctx context.Context, tx pgx.Tx, key string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM features WHERE key=$1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}
