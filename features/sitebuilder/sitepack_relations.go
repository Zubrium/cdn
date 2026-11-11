package sitebuilder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type sitepackModelIDRemap struct {
	entities     map[string]string
	fields       map[string]string
	relations    map[string]string
	records      map[string]string
	publications map[uuid.UUID]uuid.UUID
}

func validateSitepackModelContent(index *sitepackIndex) error {
	if index == nil {
		return fmt.Errorf("%w: missing sitepack index", ErrInvalid)
	}
	model := index.Payload.Config.DataModel
	if model == nil {
		if index.ModelContent != nil {
			return fmt.Errorf("%w: model content requires snapshot dataModel", ErrInvalid)
		}
		return nil
	}
	if err := ValidateDataModel(*model); err != nil {
		return fmt.Errorf("%w: invalid snapshot dataModel: %v", ErrInvalid, err)
	}
	if index.ModelContent == nil {
		// A model may not yet have persisted workspace rows. Export still emits
		// an explicit empty v2 workspace so this does not lose editorial state.
		index.ModelContent = &packModelContent{
			DraftRecords: []ModelContentRecord{}, DraftSelections: []ModelContentSelection{},
			PublishedRecords: []ModelContentRecord{}, PublishedSelections: []ModelContentSelection{},
			History: []packModelPublication{},
		}
	}
	workspace := index.ModelContent
	if workspace.Revision < 0 || workspace.PublishedRevision < 0 || workspace.PublishedRevision > workspace.Revision || workspace.ContentRevision < 0 {
		return fmt.Errorf("%w: invalid model-content revision metadata", ErrInvalid)
	}
	workspace.DraftRecords = nonNilModelRecords(workspace.DraftRecords)
	workspace.DraftSelections = nonNilModelSelections(workspace.DraftSelections)
	workspace.PublishedRecords = nonNilModelRecords(workspace.PublishedRecords)
	workspace.PublishedSelections = nonNilModelSelections(workspace.PublishedSelections)
	if workspace.History == nil {
		workspace.History = []packModelPublication{}
	}
	placements := modelValidationPlacements(index.Payload)
	validateState := func(label string, records []ModelContentRecord, selections []ModelContentSelection) error {
		if err := ValidateModelContent(*model, placements, records, selections); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		return nil
	}
	if err := validateState("draft", workspace.DraftRecords, workspace.DraftSelections); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := validateState("published", workspace.PublishedRecords, workspace.PublishedSelections); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	seenPublicationIDs := map[string]bool{}
	seenDraftRevisions := map[int64]bool{}
	seenContentRevisions := map[int64]bool{}
	var previousContentRevision int64
	var previousDraftRevision int64
	var latestDraftRevision int64
	for i := range workspace.History {
		publication := &workspace.History[i]
		if publication.ID == uuid.Nil || seenPublicationIDs[publication.ID.String()] ||
			publication.DraftRevision < 1 || publication.DraftRevision > workspace.Revision ||
			publication.ContentRevision < 1 || publication.ContentRevision > workspace.ContentRevision ||
			publication.CreatedAt.IsZero() || seenDraftRevisions[publication.DraftRevision] ||
			seenContentRevisions[publication.ContentRevision] || publication.ContentRevision != previousContentRevision+1 ||
			publication.DraftRevision <= previousDraftRevision {
			return fmt.Errorf("%w: invalid or unordered model-content publication history", ErrInvalid)
		}
		publication.Records = nonNilModelRecords(publication.Records)
		publication.Selections = nonNilModelSelections(publication.Selections)
		if err := validateSitepackHistoricalModelState(*model, placements, publication.Records, publication.Selections); err != nil {
			return fmt.Errorf("%w: history[%d]: %v", ErrInvalid, i, err)
		}
		seenPublicationIDs[publication.ID.String()] = true
		seenDraftRevisions[publication.DraftRevision] = true
		seenContentRevisions[publication.ContentRevision] = true
		previousContentRevision = publication.ContentRevision
		previousDraftRevision = publication.DraftRevision
		latestDraftRevision = publication.DraftRevision
	}
	if len(workspace.History) == 0 {
		if workspace.PublishedRevision != 0 || workspace.ContentRevision != 0 {
			return fmt.Errorf("%w: published revisions require complete model-content history", ErrInvalid)
		}
	} else if previousContentRevision != workspace.ContentRevision {
		return fmt.Errorf("%w: model-content history does not include the latest publication revision", ErrInvalid)
	}
	if len(workspace.History) > 0 && latestDraftRevision != workspace.PublishedRevision {
		return fmt.Errorf("%w: published revision does not match the latest model-content history", ErrInvalid)
	}
	if len(workspace.History) > 0 {
		latest := workspace.History[len(workspace.History)-1]
		for _, pair := range [][2]any{
			{workspace.PublishedRecords, latest.Records},
			{workspace.PublishedSelections, latest.Selections},
		} {
			current, err := json.Marshal(pair[0])
			if err != nil {
				return err
			}
			published, err := json.Marshal(pair[1])
			if err != nil {
				return err
			}
			if !bytes.Equal(current, published) {
				return fmt.Errorf("%w: current published model content differs from its latest history entry", ErrInvalid)
			}
		}
	}
	return nil
}

// Historical publication rows are immutable content snapshots. A later child
// snapshot may remove placements or change a typed input's binding, so validate
// historical selections by their intrinsic shape and record references without
// requiring the old structural contract to still exist. Draft and published
// workspace states always use strict current-payload checks.
func validateSitepackHistoricalModelState(model DataModel, placements []ModelContentPlacement, records []ModelContentRecord, selections []ModelContentSelection) error {
	if err := validateModelContentWithoutEntityCardinality(model, placements, records, nil); err != nil {
		return err
	}
	recordsByID := make(map[string]ModelContentRecord, len(records))
	for _, record := range records {
		recordsByID[record.ID] = record
	}
	seen := make(map[string]bool, len(selections))
	for index, selection := range selections {
		path := fmt.Sprintf("modelContent.selections[%d]", index)
		if strings.TrimSpace(selection.PlacementID) == "" || strings.TrimSpace(selection.InputKey) == "" {
			return fmt.Errorf("%s: placementId and inputKey are required", path)
		}
		key := selection.PlacementID + "\x00" + selection.InputKey
		if seen[key] {
			return fmt.Errorf("%s: duplicate historical selection", path)
		}
		seen[key] = true
		switch selection.Mode {
		case "all":
			if len(selection.RecordIDs) != 0 {
				return fmt.Errorf("%s.recordIds: all mode must not list record IDs", path)
			}
		case "ids":
			seenIDs := make(map[string]bool, len(selection.RecordIDs))
			for recordIndex, recordID := range selection.RecordIDs {
				recordPath := fmt.Sprintf("%s.recordIds[%d]", path, recordIndex)
				if err := validateModelContentUUID(recordID); err != nil {
					return fmt.Errorf("%s: %w", recordPath, err)
				}
				if seenIDs[recordID] {
					return fmt.Errorf("%s: duplicate selected record ID %q", recordPath, recordID)
				}
				seenIDs[recordID] = true
				if _, exists := recordsByID[recordID]; !exists {
					return fmt.Errorf("%s: selected record does not exist", recordPath)
				}
			}
		default:
			return fmt.Errorf("%s.mode: must be all or ids", path)
		}
	}
	return nil
}

func remapSitepackModelIdentities(index *sitepackIndex) (sitepackModelIDRemap, error) {
	result := sitepackModelIDRemap{
		entities: map[string]string{}, fields: map[string]string{}, relations: map[string]string{},
		records: map[string]string{}, publications: map[uuid.UUID]uuid.UUID{},
	}
	if index == nil || index.Payload.Config.DataModel == nil || index.ModelContent == nil {
		return result, nil
	}
	model := index.Payload.Config.DataModel
	newUUIDMap := func(ids []string) (map[string]string, error) {
		mapping := make(map[string]string, len(ids))
		for _, rawID := range ids {
			id, err := uuid.Parse(rawID)
			if err != nil || id == uuid.Nil || id.String() != rawID {
				return nil, fmt.Errorf("%w: invalid source model UUID", ErrInvalid)
			}
			if _, exists := mapping[rawID]; exists {
				return nil, fmt.Errorf("%w: duplicate source model UUID", ErrInvalid)
			}
			mapping[rawID] = uuid.NewString()
		}
		return mapping, nil
	}
	entityKeys := make([]string, 0, len(model.Entities))
	fieldKeys := []string{}
	relationKeys := make([]string, 0, len(model.Relations))
	for _, entity := range model.Entities {
		entityKeys = append(entityKeys, entity.ID)
		for _, field := range entity.Fields {
			fieldKeys = append(fieldKeys, field.ID)
		}
	}
	for _, relation := range model.Relations {
		relationKeys = append(relationKeys, relation.ID)
	}
	var err error
	if result.entities, err = newUUIDMap(entityKeys); err != nil {
		return result, err
	}
	if result.fields, err = newUUIDMap(fieldKeys); err != nil {
		return result, err
	}
	if result.relations, err = newUUIDMap(relationKeys); err != nil {
		return result, err
	}
	recordEntityIDs := map[string]string{}
	allRecordIDs := []string{}
	collectRecords := func(records []ModelContentRecord) error {
		for _, record := range records {
			if previous, exists := recordEntityIDs[record.ID]; exists {
				if previous != record.EntityID {
					return fmt.Errorf("%w: one model record ID is used by different entities", ErrInvalid)
				}
				continue
			}
			recordEntityIDs[record.ID] = record.EntityID
			allRecordIDs = append(allRecordIDs, record.ID)
		}
		return nil
	}
	workspace := index.ModelContent
	for _, records := range [][]ModelContentRecord{workspace.DraftRecords, workspace.PublishedRecords} {
		if err := collectRecords(records); err != nil {
			return result, err
		}
	}
	for _, publication := range workspace.History {
		id := publication.ID
		if id == uuid.Nil {
			return result, fmt.Errorf("%w: publication identity is empty", ErrInvalid)
		}
		if _, exists := result.publications[id]; exists {
			return result, fmt.Errorf("%w: duplicate publication identity", ErrInvalid)
		}
		result.publications[id] = uuid.New()
		if err := collectRecords(publication.Records); err != nil {
			return result, err
		}
	}
	result.records, err = newUUIDMap(allRecordIDs)
	if err != nil {
		return result, err
	}
	remapID := func(mapping map[string]string, value string) (string, error) {
		mapped, ok := mapping[value]
		if !ok {
			return "", fmt.Errorf("%w: model reference is outside the snapshot package", ErrInvalid)
		}
		return mapped, nil
	}
	for i := range model.Entities {
		entity := &model.Entities[i]
		entity.ID, err = remapID(result.entities, entity.ID)
		if err != nil {
			return result, err
		}
		for j := range entity.Fields {
			entity.Fields[j].ID, err = remapID(result.fields, entity.Fields[j].ID)
			if err != nil {
				return result, err
			}
		}
	}
	for i := range model.Relations {
		relation := &model.Relations[i]
		relation.ID, err = remapID(result.relations, relation.ID)
		if err != nil {
			return result, err
		}
		relation.FromEntityID, err = remapID(result.entities, relation.FromEntityID)
		if err != nil {
			return result, err
		}
		relation.ToEntityID, err = remapID(result.entities, relation.ToEntityID)
		if err != nil {
			return result, err
		}
	}
	positions := make(map[string]DataModelPosition, len(model.Positions))
	for rawID, position := range model.Positions {
		mapped, ok := result.entities[rawID]
		if !ok {
			return result, fmt.Errorf("%w: data-model layout references unknown entity", ErrInvalid)
		}
		positions[mapped] = position
	}
	if model.Positions != nil {
		model.Positions = positions
	}
	for pi := range index.Payload.Config.Pages {
		for li := range index.Payload.Config.Pages[pi].Placements {
			placement := &index.Payload.Config.Pages[pi].Placements[li]
			for bi := range placement.DataBindings {
				binding := &placement.DataBindings[bi]
				binding.EntityID, err = remapID(result.entities, binding.EntityID)
				if err != nil {
					return result, err
				}
				if binding.FieldID != "" {
					binding.FieldID, err = remapID(result.fields, binding.FieldID)
					if err != nil {
						return result, err
					}
				}
				for ri := range binding.RelationIDs {
					binding.RelationIDs[ri], err = remapID(result.relations, binding.RelationIDs[ri])
					if err != nil {
						return result, err
					}
				}
				for fi := range binding.Fields {
					binding.Fields[fi].FieldID, err = remapID(result.fields, binding.Fields[fi].FieldID)
					if err != nil {
						return result, err
					}
				}
				for fi := range binding.Fields {
					for ri := range binding.Fields[fi].RelationIDs {
						binding.Fields[fi].RelationIDs[ri], err = remapID(result.relations, binding.Fields[fi].RelationIDs[ri])
						if err != nil {
							return result, err
						}
					}
				}
				if binding.Lookup != nil {
					binding.Lookup.FieldID, err = remapID(result.fields, binding.Lookup.FieldID)
					if err != nil {
						return result, err
					}
				}
			}
		}
	}
	remapRecords := func(records []ModelContentRecord) error {
		for i := range records {
			record := &records[i]
			record.ID, err = remapID(result.records, record.ID)
			if err != nil {
				return err
			}
			record.EntityID, err = remapID(result.entities, record.EntityID)
			if err != nil {
				return err
			}
			relations := make(map[string][]string, len(record.Relations))
			for relationID, oldTargets := range record.Relations {
				newRelationID, ok := result.relations[relationID]
				if !ok {
					return fmt.Errorf("%w: record references relation outside the data model", ErrInvalid)
				}
				newTargets := make([]string, len(oldTargets))
				for i, oldID := range oldTargets {
					newTargets[i], err = remapID(result.records, oldID)
					if err != nil {
						return err
					}
				}
				relations[newRelationID] = newTargets
			}
			record.Relations = relations
		}
		return nil
	}
	remapSelections := func(selections []ModelContentSelection) error {
		for i := range selections {
			for j := range selections[i].RecordIDs {
				selections[i].RecordIDs[j], err = remapID(result.records, selections[i].RecordIDs[j])
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, records := range [][]ModelContentRecord{workspace.DraftRecords, workspace.PublishedRecords} {
		if err := remapRecords(records); err != nil {
			return result, err
		}
	}
	for _, selections := range [][]ModelContentSelection{workspace.DraftSelections, workspace.PublishedSelections} {
		if err := remapSelections(selections); err != nil {
			return result, err
		}
	}
	for i := range workspace.History {
		publication := &workspace.History[i]
		publication.ID = result.publications[publication.ID]
		if err := remapRecords(publication.Records); err != nil {
			return result, err
		}
		if err := remapSelections(publication.Selections); err != nil {
			return result, err
		}
	}
	if err := ValidateDataModel(*model); err != nil {
		return result, fmt.Errorf("%w: remapped data model is invalid: %v", ErrInvalid, err)
	}
	return result, nil
}

func addSitepackModelReportItems(report *SitepackReport, remap sitepackModelIDRemap) {
	if report == nil {
		return
	}
	for _, item := range []SitepackReportItem{
		{Kind: "model_entity", Path: "dataModel.entities", Action: "ids_remapped", Count: len(remap.entities), Message: "ID сущностей модели переназначены"},
		{Kind: "model_field", Path: "dataModel.fields", Action: "ids_remapped", Count: len(remap.fields), Message: "ID полей модели переназначены"},
		{Kind: "model_relation", Path: "dataModel.relations", Action: "ids_remapped", Count: len(remap.relations), Message: "ID связей модели переназначены"},
		{Kind: "model_record", Path: "modelContent.records", Action: "ids_remapped", Count: len(remap.records), Message: "ID записей модели переназначены"},
	} {
		if item.Count > 0 {
			addSitepackReportItem(report, item)
		}
	}
}

func sitepackModelMediaReferences(content *packModelContent, model *DataModel) ([]uuid.UUID, error) {
	if content == nil || model == nil {
		return nil, nil
	}
	fields := map[string]map[string]bool{}
	for _, entity := range model.Entities {
		fields[entity.ID] = map[string]bool{}
		for _, field := range entity.Fields {
			if field.Type == "media" {
				fields[entity.ID][field.Key] = true
			}
		}
	}
	seen := map[uuid.UUID]bool{}
	ids := []uuid.UUID{}
	collect := func(records []ModelContentRecord) error {
		for _, record := range records {
			for key := range fields[record.EntityID] {
				value, exists := record.Values[key]
				if !exists || value == nil {
					continue
				}
				text, ok := value.(string)
				if !ok {
					return fmt.Errorf("%w: model media reference must be a string", ErrInvalid)
				}
				if text == "" {
					continue
				}
				id, err := uuid.Parse(text)
				if err != nil || id == uuid.Nil || id.String() != text {
					return fmt.Errorf("%w: model media reference is invalid", ErrInvalid)
				}
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		return nil
	}
	for _, records := range [][]ModelContentRecord{content.DraftRecords, content.PublishedRecords} {
		if err := collect(records); err != nil {
			return nil, err
		}
	}
	for _, publication := range content.History {
		if err := collect(publication.Records); err != nil {
			return nil, err
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids, nil
}

func rewriteSitepackModelMediaReferences(content *packModelContent, model *DataModel, replacements map[string]string) error {
	if content == nil || model == nil {
		return nil
	}
	fields := map[string]map[string]bool{}
	for _, entity := range model.Entities {
		fields[entity.ID] = map[string]bool{}
		for _, field := range entity.Fields {
			if field.Type == "media" {
				fields[entity.ID][field.Key] = true
			}
		}
	}
	rewrite := func(records []ModelContentRecord) error {
		for i := range records {
			record := &records[i]
			for key := range fields[record.EntityID] {
				value, exists := record.Values[key]
				if !exists || value == nil {
					continue
				}
				text, ok := value.(string)
				if !ok {
					return fmt.Errorf("%w: model media reference must be a string", ErrInvalid)
				}
				if text == "" {
					continue
				}
				id, err := uuid.Parse(text)
				if err != nil || id == uuid.Nil || id.String() != text {
					return fmt.Errorf("%w: model media reference is invalid", ErrInvalid)
				}
				mapped, ok := replacements[text]
				if !ok {
					return fmt.Errorf("%w: model media reference is outside the package", ErrInvalid)
				}
				record.Values[key] = mapped
			}
		}
		return nil
	}
	for _, records := range [][]ModelContentRecord{content.DraftRecords, content.PublishedRecords} {
		if err := rewrite(records); err != nil {
			return err
		}
	}
	for i := range content.History {
		if err := rewrite(content.History[i].Records); err != nil {
			return err
		}
	}
	return nil
}
