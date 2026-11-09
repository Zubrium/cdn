package sitebuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/files"
	featuremedia "websiteengine/features/media"
)

// ModelContentWorkspace is the editable data view for one snapshot. Model
// structure stays in the immutable snapshot payload; records and selections
// have independent draft and published revisions.
type ModelContentWorkspace struct {
	Snapshot          SnapshotRecord                 `json:"snapshot"`
	DataModel         *DataModel                     `json:"dataModel"`
	Revision          int64                          `json:"revision"`
	PublishedRevision int64                          `json:"publishedRevision"`
	Records           []ModelContentRecord           `json:"records"`
	Selections        []ModelContentSelection        `json:"selections"`
	Placements        []ModelContentPlacementSummary `json:"placements"`
}

type ModelContentPlacementSummary struct {
	ID          string                     `json:"id"`
	PageID      string                     `json:"pageId"`
	PageName    string                     `json:"pageName"`
	FeatureKey  string                     `json:"featureKey"`
	FeatureName string                     `json:"featureName"`
	Version     string                     `json:"version"`
	Inputs      []ModelContentInputSummary `json:"inputs"`
}

type ModelContentInputSummary struct {
	InputKey string                     `json:"inputKey"`
	Kind     string                     `json:"kind"`
	EntityID string                     `json:"entityId"`
	Fields   []ModelContentFieldSummary `json:"fields"`
	Lookup   *DataInputLookupBinding    `json:"lookup,omitempty"`
}

type ModelContentFieldSummary struct {
	Path         string                         `json:"path"`
	FieldID      string                         `json:"fieldId"`
	Type         string                         `json:"type"`
	RelationIDs  []string                       `json:"relationIds,omitempty"`
	Preparations []ModelContentMediaPreparation `json:"preparations,omitempty"`
}

// ModelContentMediaPreparation identifies a media profile declared by a
// pinned feature input and mapped to this model field. The content editor uses
// it to prepare every required rendition when an existing media item is chosen.
type ModelContentMediaPreparation struct {
	FeatureKey   string `json:"featureKey"`
	BuildVersion string `json:"buildVersion"`
	SchemaKind   string `json:"schemaKind"`
	FieldPath    string `json:"fieldPath"`
	BuildDigest  string `json:"buildDigest"`
	Accept       string `json:"accept"`
}

type ModelContentDraftInput struct {
	Revision   int64                   `json:"revision"`
	Records    []ModelContentRecord    `json:"records"`
	Selections []ModelContentSelection `json:"selections"`
}

type ModelContentPublication struct {
	ID              uuid.UUID `json:"id"`
	DraftRevision   int64     `json:"draftRevision"`
	ContentRevision int64     `json:"contentRevision"`
	CreatedAt       string    `json:"createdAt"`
	RecordCount     int       `json:"recordCount"`
}

type storedModelContentWorkspace struct {
	Exists             bool
	Revision           int64
	PublishedRevision  int64
	DraftRecords       []ModelContentRecord
	DraftSelections    []ModelContentSelection
	PublishedRecords   []ModelContentRecord
	PublishedSelection []ModelContentSelection
}

func (s *SnapshotService) ModelContentWorkspace(ctx context.Context, snapshotID uuid.UUID) (ModelContentWorkspace, error) {
	record, payload, err := s.snapshotPayload(ctx, snapshotID)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	placements, err := modelContentPlacementSummaries(payload)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	result := ModelContentWorkspace{
		Snapshot: record, DataModel: cloneDataModel(payload.Config.DataModel),
		Records: []ModelContentRecord{}, Selections: []ModelContentSelection{}, Placements: placements,
	}
	if payload.Config.DataModel == nil {
		return result, nil
	}
	state, err := readStoredModelContent(ctx, s.pool, snapshotID, false)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	result.Revision = state.Revision
	result.PublishedRevision = state.PublishedRevision
	result.Records = state.DraftRecords
	result.Selections = state.DraftSelections
	return result, nil
}

func modelContentPlacementSummaries(payload SnapshotPayload) ([]ModelContentPlacementSummary, error) {
	builds := make(map[string]Manifest, len(payload.Builds))
	buildNames := make(map[string]string, len(payload.Builds))
	versions := make(map[string]string, len(payload.Builds))
	digests := make(map[string]string, len(payload.Builds))
	for _, build := range payload.Builds {
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return nil, fmt.Errorf("decode pinned build %s@%s: %w", build.Key, build.Version, err)
		}
		builds[build.Key] = manifest
		buildNames[build.Key] = build.Name
		versions[build.Key] = build.Version
		digests[build.Key] = build.Digest
	}
	items := make([]ModelContentPlacementSummary, 0)
	for _, page := range payload.Config.Pages {
		for _, placement := range page.Placements {
			manifest, exists := builds[placement.Build.FeatureKey]
			if !exists || len(manifest.DataInputs) == 0 {
				continue
			}
			item := ModelContentPlacementSummary{
				ID: placement.ID, PageID: page.ID, PageName: page.Name,
				FeatureKey: placement.Build.FeatureKey, FeatureName: buildNames[placement.Build.FeatureKey], Version: versions[placement.Build.FeatureKey],
				Inputs: make([]ModelContentInputSummary, 0, len(manifest.DataInputs)),
			}
			for _, port := range manifest.DataInputs {
				binding, ok := dataInputBindingByKey(placement.DataBindings, port.Key)
				if !ok {
					continue
				}
				input := ModelContentInputSummary{InputKey: port.Key, Kind: port.Kind, EntityID: binding.EntityID, Fields: make([]ModelContentFieldSummary, 0)}
				if port.Kind == "scalar" {
					field := ModelContentFieldSummary{FieldID: binding.FieldID, Type: port.Type, RelationIDs: append([]string(nil), binding.RelationIDs...)}
					if port.Type == "media" && port.Profile != nil {
						field.Preparations = []ModelContentMediaPreparation{{FeatureKey: placement.Build.FeatureKey, BuildVersion: versions[placement.Build.FeatureKey], SchemaKind: "dataInputs", FieldPath: "/" + port.Key, BuildDigest: digests[placement.Build.FeatureKey], Accept: port.Profile.Accept}}
					}
					input.Fields = append(input.Fields, field)
				}
				for _, mapped := range binding.Fields {
					fieldType := ""
					var mediaProfile *featuremedia.FieldProfile
					for _, declared := range port.Fields {
						if declared.Path == mapped.InputPath {
							fieldType = declared.Type
							mediaProfile = declared.Profile
							break
						}
					}
					field := ModelContentFieldSummary{Path: mapped.InputPath, FieldID: mapped.FieldID, Type: fieldType, RelationIDs: append([]string(nil), mapped.RelationIDs...)}
					if fieldType == "media" && mediaProfile != nil {
						segments, err := parseJSONPointer(mapped.InputPath)
						if err != nil {
							return nil, fmt.Errorf("invalid bound data input path %q: %w", mapped.InputPath, err)
						}
						field.Preparations = []ModelContentMediaPreparation{{FeatureKey: placement.Build.FeatureKey, BuildVersion: versions[placement.Build.FeatureKey], SchemaKind: "dataInputs", FieldPath: pointerKey(append([]string{port.Key}, segments...)), BuildDigest: digests[placement.Build.FeatureKey], Accept: mediaProfile.Accept}}
					}
					input.Fields = append(input.Fields, field)
				}
				if binding.Lookup != nil {
					lookup := *binding.Lookup
					input.Lookup = &lookup
				}
				item.Inputs = append(item.Inputs, input)
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func dataInputBindingByKey(bindings []DataInputBinding, key string) (DataInputBinding, bool) {
	for _, binding := range bindings {
		if binding.InputKey == key {
			return binding, true
		}
	}
	return DataInputBinding{}, false
}

func readStoredModelContent(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, snapshotID uuid.UUID, forUpdate bool) (storedModelContentWorkspace, error) {
	result := storedModelContentWorkspace{
		DraftRecords: []ModelContentRecord{}, DraftSelections: []ModelContentSelection{},
		PublishedRecords: []ModelContentRecord{}, PublishedSelection: []ModelContentSelection{},
	}
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE"
	}
	var draftRecordsRaw, draftSelectionsRaw, publishedRecordsRaw, publishedSelectionsRaw []byte
	err := query.QueryRow(ctx, `SELECT revision,published_revision,draft_records,draft_selections,published_records,published_selections FROM site_snapshot_model_workspaces WHERE snapshot_id=$1`+suffix, snapshotID).
		Scan(&result.Revision, &result.PublishedRevision, &draftRecordsRaw, &draftSelectionsRaw, &publishedRecordsRaw, &publishedSelectionsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return storedModelContentWorkspace{}, err
	}
	result.Exists = true
	if err := decodeModelJSON(draftRecordsRaw, &result.DraftRecords); err != nil {
		return storedModelContentWorkspace{}, err
	}
	if err := decodeModelJSON(draftSelectionsRaw, &result.DraftSelections); err != nil {
		return storedModelContentWorkspace{}, err
	}
	if err := decodeModelJSON(publishedRecordsRaw, &result.PublishedRecords); err != nil {
		return storedModelContentWorkspace{}, err
	}
	if err := decodeModelJSON(publishedSelectionsRaw, &result.PublishedSelection); err != nil {
		return storedModelContentWorkspace{}, err
	}
	if result.DraftRecords == nil {
		result.DraftRecords = []ModelContentRecord{}
	}
	if result.DraftSelections == nil {
		result.DraftSelections = []ModelContentSelection{}
	}
	if result.PublishedRecords == nil {
		result.PublishedRecords = []ModelContentRecord{}
	}
	if result.PublishedSelection == nil {
		result.PublishedSelection = []ModelContentSelection{}
	}
	return result, nil
}

func decodeModelJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func (s *SnapshotService) SaveModelContentWorkspace(ctx context.Context, actor, snapshotID uuid.UUID, input ModelContentDraftInput) (ModelContentWorkspace, error) {
	if input.Revision < 0 {
		return ModelContentWorkspace{}, fmt.Errorf("%w: revision must not be negative", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockFileReferences(ctx, tx); err != nil {
		return ModelContentWorkspace{}, err
	}
	payload, _, err := lockModelSnapshot(ctx, tx, snapshotID)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if payload.Config.DataModel == nil {
		return ModelContentWorkspace{}, ErrNotFound
	}
	state, err := readStoredModelContent(ctx, tx, snapshotID, true)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if input.Revision != state.Revision {
		return ModelContentWorkspace{}, fmt.Errorf("%w: model content changed; reload before saving", ErrConflict)
	}
	known := knownModelRecordEntities(state.DraftRecords, state.PublishedRecords)
	inputRecords, err := preserveSingletonDraftRecordIDs(*payload.Config.DataModel, input.Records, state.DraftRecords)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	records, err := prepareModelContentDraftRecords(inputRecords, known)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	selections := nonNilModelSelections(input.Selections)
	placements := modelValidationPlacements(payload)
	if err := validateModelContentEntityCardinality(*payload.Config.DataModel, records, true); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := ValidateModelContent(*payload.Config.DataModel, placements, records, selections); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := validateModelMediaReferences(ctx, tx, *payload.Config.DataModel, records, false); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	revision := state.Revision + 1
	if err := writeModelContentWorkspace(ctx, tx, actor, snapshotID, state, revision, state.PublishedRevision, records, selections, state.PublishedRecords, state.PublishedSelection); err != nil {
		return ModelContentWorkspace{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelContentWorkspace{}, err
	}
	return s.ModelContentWorkspace(ctx, snapshotID)
}

func lockModelSnapshot(ctx context.Context, tx pgx.Tx, snapshotID uuid.UUID) (SnapshotPayload, int64, error) {
	var raw []byte
	var contentRevision int64
	err := tx.QueryRow(ctx, `SELECT payload,content_revision FROM site_snapshots WHERE id=$1 FOR UPDATE`, snapshotID).Scan(&raw, &contentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return SnapshotPayload{}, 0, ErrNotFound
	}
	if err != nil {
		return SnapshotPayload{}, 0, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return SnapshotPayload{}, 0, err
	}
	return payload, contentRevision, nil
}

func knownModelRecordEntities(groups ...[]ModelContentRecord) map[string]string {
	known := make(map[string]string)
	for _, records := range groups {
		for _, record := range records {
			known[record.ID] = record.EntityID
		}
	}
	return known
}

func prepareModelContentDraftRecords(input []ModelContentRecord, known map[string]string) ([]ModelContentRecord, error) {
	records := append([]ModelContentRecord(nil), input...)
	for i := range records {
		if records[i].ID == "" {
			records[i].ID = uuid.NewString()
		} else {
			id, err := uuid.Parse(records[i].ID)
			if err != nil || id == uuid.Nil || id.String() != records[i].ID {
				return nil, fmt.Errorf("%w: record IDs must be generated canonical UUIDs", ErrInvalid)
			}
			entityID, exists := known[records[i].ID]
			if !exists {
				return nil, fmt.Errorf("%w: new record IDs are generated by the server", ErrInvalid)
			}
			if entityID != records[i].EntityID {
				return nil, fmt.Errorf("%w: record %q cannot change its entity", ErrInvalid, records[i].ID)
			}
		}
		if records[i].Values == nil {
			records[i].Values = map[string]any{}
		}
		if records[i].Relations == nil {
			records[i].Relations = map[string][]string{}
		}
	}
	return records, nil
}

// preserveSingletonDraftRecordIDs binds every existing singleton row to its
// current identity before the generic record preparation step. This keeps a
// cleared form from silently replacing the record that relations and history
// refer to, even when a client omits the ID while serializing the form.
func preserveSingletonDraftRecordIDs(model DataModel, input, existingDraft []ModelContentRecord) ([]ModelContentRecord, error) {
	result := append([]ModelContentRecord(nil), input...)
	existing := make(map[string]string)
	for _, record := range existingDraft {
		if entity, ok := dataModelEntityByID(model, record.EntityID); ok && entity.Cardinality == DataModelEntityCardinalitySingleton {
			if _, duplicate := existing[record.EntityID]; duplicate {
				return nil, fmt.Errorf("%w: singleton entity %q already has more than one draft record", ErrInvalid, entity.Key)
			}
			existing[record.EntityID] = record.ID
		}
	}
	seen := make(map[string]bool)
	for i := range result {
		entity, ok := dataModelEntityByID(model, result[i].EntityID)
		if !ok || entity.Cardinality != DataModelEntityCardinalitySingleton {
			continue
		}
		if seen[entity.ID] {
			return nil, fmt.Errorf("%w: singleton entity %q accepts only one draft record", ErrInvalid, entity.Key)
		}
		seen[entity.ID] = true
		if currentID := existing[entity.ID]; currentID != "" {
			if result[i].ID == "" {
				result[i].ID = currentID
			} else if result[i].ID != currentID {
				return nil, fmt.Errorf("%w: singleton entity %q keeps its existing record ID", ErrInvalid, entity.Key)
			}
		}
	}
	return result, nil
}

func dataModelEntityByID(model DataModel, id string) (DataModelEntity, bool) {
	for _, entity := range model.Entities {
		if entity.ID == id {
			return entity, true
		}
	}
	return DataModelEntity{}, false
}

// seedSingletonDraftRecords adds one generated, empty draft row for every
// singleton missing from the supplied state. Existing rows are copied as-is
// so their IDs, values, relation targets and sort order remain stable.
func seedSingletonDraftRecords(model DataModel, records []ModelContentRecord) ([]ModelContentRecord, bool, error) {
	if err := validateModelContentEntityCardinality(model, records, false); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	result := append([]ModelContentRecord(nil), records...)
	counts := make(map[string]int, len(model.Entities))
	for _, record := range result {
		counts[record.EntityID]++
	}
	seeded := false
	for _, entity := range model.Entities {
		if entity.Cardinality != DataModelEntityCardinalitySingleton || counts[entity.ID] > 0 {
			continue
		}
		result = append(result, ModelContentRecord{
			ID: uuid.NewString(), EntityID: entity.ID, SortOrder: 0,
			Values: map[string]any{}, Relations: map[string][]string{},
		})
		seeded = true
	}
	return result, seeded, nil
}

func nonNilModelSelections(input []ModelContentSelection) []ModelContentSelection {
	if input == nil {
		return []ModelContentSelection{}
	}
	return append(make([]ModelContentSelection, 0, len(input)), input...)
}

func modelValidationPlacements(payload SnapshotPayload) []ModelContentPlacement {
	items := make([]ModelContentPlacement, 0)
	for _, page := range payload.Config.Pages {
		for _, placement := range page.Placements {
			var manifest Manifest
			for _, build := range payload.Builds {
				if build.Key == placement.Build.FeatureKey && build.Version == placement.Build.Version {
					if json.Unmarshal(build.Manifest, &manifest) != nil {
						continue
					}
					break
				}
			}
			if len(manifest.DataInputs) == 0 && len(placement.DataBindings) == 0 {
				continue
			}
			items = append(items, ModelContentPlacement{ID: placement.ID, Ports: manifest.DataInputs, Bindings: placement.DataBindings, ArgumentBindings: placement.Arguments})
		}
	}
	return items
}

func validateModelContentPublicationForRestore(model DataModel, placements []ModelContentPlacement, records []ModelContentRecord, selections []ModelContentSelection) error {
	return ValidateModelContent(model, placements, records, selections)
}

func writeModelContentWorkspace(ctx context.Context, tx pgx.Tx, actor, snapshotID uuid.UUID, old storedModelContentWorkspace, revision, publishedRevision int64, draftRecords []ModelContentRecord, draftSelections []ModelContentSelection, publishedRecords []ModelContentRecord, publishedSelections []ModelContentSelection) error {
	draftRaw, err := json.Marshal(nonNilModelRecords(draftRecords))
	if err != nil {
		return err
	}
	selectionRaw, err := json.Marshal(nonNilModelSelections(draftSelections))
	if err != nil {
		return err
	}
	publishedRaw, err := json.Marshal(nonNilModelRecords(publishedRecords))
	if err != nil {
		return err
	}
	publishedSelectionRaw, err := json.Marshal(nonNilModelSelections(publishedSelections))
	if err != nil {
		return err
	}
	if !old.Exists {
		_, err = tx.Exec(ctx, `INSERT INTO site_snapshot_model_workspaces(snapshot_id,revision,published_revision,draft_records,draft_selections,published_records,published_selections,updated_by) VALUES($1,$2,$3,$4::jsonb,$5::jsonb,$6::jsonb,$7::jsonb,$8)`, snapshotID, revision, publishedRevision, string(draftRaw), string(selectionRaw), string(publishedRaw), string(publishedSelectionRaw), actor)
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE site_snapshot_model_workspaces SET revision=$2,published_revision=$3,draft_records=$4::jsonb,draft_selections=$5::jsonb,published_records=$6::jsonb,published_selections=$7::jsonb,updated_by=$8,updated_at=now() WHERE snapshot_id=$1`, snapshotID, revision, publishedRevision, string(draftRaw), string(selectionRaw), string(publishedRaw), string(publishedSelectionRaw), actor)
	return err
}

func nonNilModelRecords(input []ModelContentRecord) []ModelContentRecord {
	if input == nil {
		return []ModelContentRecord{}
	}
	return input
}

func validateModelMediaReferences(ctx context.Context, tx pgx.Tx, model DataModel, records []ModelContentRecord, requirePublic bool) error {
	entities := make(map[string]DataModelEntity, len(model.Entities))
	for _, entity := range model.Entities {
		entities[entity.ID] = entity
	}
	for _, record := range records {
		entity, exists := entities[record.EntityID]
		if !exists {
			continue
		}
		for _, field := range entity.Fields {
			if field.Type != "media" {
				continue
			}
			schema, err := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{field.Key: map[string]any{"type": "string", "format": "media"}}})
			if err != nil {
				return err
			}
			data, err := json.Marshal(record.Values)
			if err != nil {
				return err
			}
			if err := files.ValidateReferencesTx(ctx, tx, schema, data, requirePublic); err != nil {
				return fmt.Errorf("record %q field %q: %w", record.ID, field.Key, err)
			}
		}
	}
	return nil
}

func validateModelContentRenditions(ctx context.Context, service *featuremedia.Service, payload SnapshotPayload, model DataModel, records []ModelContentRecord, enqueue bool) error {
	profilesByField := make(map[string]map[string]featuremedia.FieldProfile)
	for _, page := range payload.Config.Pages {
		for _, placement := range page.Placements {
			var manifest *Manifest
			for index := range payload.Builds {
				if payload.Builds[index].Key != placement.Build.FeatureKey || payload.Builds[index].Version != placement.Build.Version {
					continue
				}
				var pinned Manifest
				if err := json.Unmarshal(payload.Builds[index].Manifest, &pinned); err != nil {
					return fmt.Errorf("decode pinned build %s@%s: %w", placement.Build.FeatureKey, placement.Build.Version, err)
				}
				manifest = &pinned
				break
			}
			if manifest == nil {
				continue
			}
			for _, port := range manifest.DataInputs {
				binding, ok := dataInputBindingByKey(placement.DataBindings, port.Key)
				if !ok {
					continue
				}
				if port.Kind == "scalar" && port.Type == "media" && port.Profile != nil {
					addModelMediaProfile(profilesByField, binding.FieldID, *port.Profile)
				}
				for _, mapped := range binding.Fields {
					for _, declared := range port.Fields {
						if declared.Path == mapped.InputPath && declared.Type == "media" && declared.Profile != nil {
							addModelMediaProfile(profilesByField, mapped.FieldID, *declared.Profile)
							break
						}
					}
				}
			}
		}
	}
	if len(profilesByField) == 0 || len(records) == 0 {
		return nil
	}
	if service == nil {
		return errors.New("media rendition service is unavailable for model-bound media")
	}
	modelFields := make(map[string]struct{ entityID, key string })
	for _, entity := range model.Entities {
		for _, field := range entity.Fields {
			modelFields[field.ID] = struct{ entityID, key string }{entityID: entity.ID, key: field.Key}
		}
	}
	var pending bool
	for _, record := range records {
		for fieldID, profiles := range profilesByField {
			field, exists := modelFields[fieldID]
			if !exists || record.EntityID != field.entityID {
				continue
			}
			value, exists := record.Values[field.key]
			if !exists || value == nil {
				continue
			}
			if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
				continue
			}
			for _, profile := range profiles {
				schema, err := json.Marshal(map[string]any{
					"type": "string", "format": "media", "x-media": profile,
				})
				if err != nil {
					return err
				}
				data, err := json.Marshal(value)
				if err != nil {
					return err
				}
				if err := service.ValidateContent(ctx, schema, data, enqueue); err != nil {
					if errors.Is(err, featuremedia.ErrRenditionsPending) {
						pending = true
						continue
					}
					return fmt.Errorf("record %q field %q media profile: %w", record.ID, field.key, err)
				}
			}
		}
	}
	if pending {
		return featuremedia.ErrRenditionsPending
	}
	return nil
}

func addModelMediaProfile(profiles map[string]map[string]featuremedia.FieldProfile, fieldID string, profile featuremedia.FieldProfile) {
	if fieldID == "" {
		return
	}
	hash, err := featuremedia.ProfileHash(profile)
	if err != nil {
		return
	}
	if profiles[fieldID] == nil {
		profiles[fieldID] = make(map[string]featuremedia.FieldProfile)
	}
	profiles[fieldID][hash] = profile
}

func (s *SnapshotService) PublishModelContentWorkspace(ctx context.Context, actor, snapshotID uuid.UUID, expectedRevision int64) (ModelContentWorkspace, error) {
	if expectedRevision < 1 {
		return ModelContentWorkspace{}, fmt.Errorf("%w: save a draft before publishing", ErrInvalid)
	}
	_, payload, err := s.snapshotPayload(ctx, snapshotID)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if payload.Config.DataModel == nil {
		return ModelContentWorkspace{}, ErrNotFound
	}
	preflight, err := readStoredModelContent(ctx, s.pool, snapshotID, false)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if preflight.Revision != expectedRevision {
		return ModelContentWorkspace{}, fmt.Errorf("%w: model content changed; reload before publishing", ErrConflict)
	}
	if err := validateModelContentEntityCardinality(*payload.Config.DataModel, preflight.DraftRecords, true); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if preflight.Revision != preflight.PublishedRevision {
		if err := ValidateModelContent(*payload.Config.DataModel, modelValidationPlacements(payload), preflight.DraftRecords, preflight.DraftSelections); err != nil {
			return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if err := validateModelContentRenditions(ctx, s.media, payload, *payload.Config.DataModel, preflight.DraftRecords, true); err != nil {
			if errors.Is(err, featuremedia.ErrRenditionsPending) {
				return ModelContentWorkspace{}, fmt.Errorf("%w: required media renditions are still processing; try publishing again when they are ready", ErrConflict)
			}
			return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockFileReferences(ctx, tx); err != nil {
		return ModelContentWorkspace{}, err
	}
	payload, contentRevision, err := lockModelSnapshot(ctx, tx, snapshotID)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if payload.Config.DataModel == nil {
		return ModelContentWorkspace{}, ErrNotFound
	}
	state, err := readStoredModelContent(ctx, tx, snapshotID, true)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if state.Revision != expectedRevision {
		return ModelContentWorkspace{}, fmt.Errorf("%w: model content changed; reload before publishing", ErrConflict)
	}
	if err := validateModelContentEntityCardinality(*payload.Config.DataModel, state.DraftRecords, true); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if state.Revision == state.PublishedRevision {
		if err := tx.Commit(ctx); err != nil {
			return ModelContentWorkspace{}, err
		}
		return s.ModelContentWorkspace(ctx, snapshotID)
	}
	placements := modelValidationPlacements(payload)
	if err := ValidateModelContent(*payload.Config.DataModel, placements, state.DraftRecords, state.DraftSelections); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := validateModelMediaReferences(ctx, tx, *payload.Config.DataModel, state.DraftRecords, true); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	contentRevision++
	recordsRaw, err := json.Marshal(nonNilModelRecords(state.DraftRecords))
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	selectionsRaw, err := json.Marshal(nonNilModelSelections(state.DraftSelections))
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE site_snapshot_model_workspaces SET published_revision=revision,published_records=draft_records,published_selections=draft_selections,updated_by=$2,updated_at=now() WHERE snapshot_id=$1`, snapshotID, actor); err != nil {
		return ModelContentWorkspace{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE site_snapshots SET content_revision=$2 WHERE id=$1`, snapshotID, contentRevision); err != nil {
		return ModelContentWorkspace{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO snapshot_model_content_publications(id,snapshot_id,draft_revision,content_revision,records,selections,actor_token_id) VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7)`, uuid.New(), snapshotID, state.Revision, contentRevision, string(recordsRaw), string(selectionsRaw), actor); err != nil {
		return ModelContentWorkspace{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelContentWorkspace{}, err
	}
	return s.ModelContentWorkspace(ctx, snapshotID)
}

func (s *SnapshotService) ModelContentHistory(ctx context.Context, snapshotID uuid.UUID, limit int, before *int64) ([]ModelContentPublication, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: history limit must be positive", ErrInvalid)
	}
	_, payload, err := s.snapshotPayload(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	if payload.Config.DataModel == nil {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `SELECT id,draft_revision,content_revision,created_at,jsonb_array_length(records) FROM snapshot_model_content_publications WHERE snapshot_id=$1 AND ($2::bigint IS NULL OR content_revision<$2) ORDER BY content_revision DESC LIMIT $3`, snapshotID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ModelContentPublication, 0)
	for rows.Next() {
		var item ModelContentPublication
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.DraftRevision, &item.ContentRevision, &createdAt, &item.RecordCount); err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SnapshotService) RestoreModelContentPublication(ctx context.Context, actor, snapshotID, publicationID uuid.UUID, expectedRevision int64) (ModelContentWorkspace, error) {
	if expectedRevision < 0 {
		return ModelContentWorkspace{}, fmt.Errorf("%w: revision must not be negative", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockFileReferences(ctx, tx); err != nil {
		return ModelContentWorkspace{}, err
	}
	payload, _, err := lockModelSnapshot(ctx, tx, snapshotID)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if payload.Config.DataModel == nil {
		return ModelContentWorkspace{}, ErrNotFound
	}
	state, err := readStoredModelContent(ctx, tx, snapshotID, true)
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	if state.Revision != expectedRevision {
		return ModelContentWorkspace{}, fmt.Errorf("%w: model content changed; reload before restoring", ErrConflict)
	}
	var recordsRaw, selectionsRaw []byte
	err = tx.QueryRow(ctx, `SELECT records,selections FROM snapshot_model_content_publications WHERE id=$1 AND snapshot_id=$2`, publicationID, snapshotID).Scan(&recordsRaw, &selectionsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelContentWorkspace{}, ErrNotFound
	}
	if err != nil {
		return ModelContentWorkspace{}, err
	}
	var records []ModelContentRecord
	var selections []ModelContentSelection
	if err := decodeModelJSON(recordsRaw, &records); err != nil {
		return ModelContentWorkspace{}, err
	}
	if err := decodeModelJSON(selectionsRaw, &selections); err != nil {
		return ModelContentWorkspace{}, err
	}
	records, _, err = seedSingletonDraftRecords(*payload.Config.DataModel, records)
	if err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: historical publication cannot seed singleton drafts: %v", ErrInvalid, err)
	}
	if err := validateModelContentEntityCardinality(*payload.Config.DataModel, records, true); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: historical publication has invalid singleton draft state: %v", ErrInvalid, err)
	}
	if err := validateModelContentPublicationForRestore(*payload.Config.DataModel, modelValidationPlacements(payload), records, selections); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: historical publication is incompatible with the current model: %v", ErrInvalid, err)
	}
	if err := validateModelMediaReferences(ctx, tx, *payload.Config.DataModel, records, false); err != nil {
		return ModelContentWorkspace{}, fmt.Errorf("%w: historical media is unavailable: %v", ErrInvalid, err)
	}
	if err := writeModelContentWorkspace(ctx, tx, actor, snapshotID, state, state.Revision+1, state.PublishedRevision, records, selections, state.PublishedRecords, state.PublishedSelection); err != nil {
		return ModelContentWorkspace{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelContentWorkspace{}, err
	}
	return s.ModelContentWorkspace(ctx, snapshotID)
}
