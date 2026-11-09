package sitebuilder

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"websiteengine/pkg/platform/validate"
)

// ModelContentRecord is one snapshot-scoped CMS record. Values are keyed by
// stable model field keys; relation values are keyed by relation ID and list
// target record IDs. Generated record IDs are never model field values.
type ModelContentRecord struct {
	ID        string              `json:"id"`
	EntityID  string              `json:"entityId"`
	SortOrder int                 `json:"sortOrder"`
	Values    map[string]any      `json:"values"`
	Relations map[string][]string `json:"relations,omitempty"`
}

// ModelContentSelection is an ordered editorial selection for one placement
// input. Collection inputs use "all" or "ids"; scalar and static record
// inputs use "ids" with exactly one record ID.
type ModelContentSelection struct {
	PlacementID string   `json:"placementId"`
	InputKey    string   `json:"inputKey"`
	Mode        string   `json:"mode"`
	RecordIDs   []string `json:"recordIds,omitempty"`
}

// ModelContentPlacement is the pinned data-input contract needed to validate
// model content. The service builds it from each placement and its manifest.
type ModelContentPlacement struct {
	ID               string
	Ports            []DataInputPort
	Bindings         []DataInputBinding
	ArgumentBindings map[string]ArgumentBinding
}

// ValidateModelContent checks the snapshot model, placement bindings, records,
// relations, unique values, and editorial selections without accessing
// storage. Absent records and field values are valid; a missing collection
// selection means all published records, while a missing scalar/record
// selection is an empty runtime input. Route-looked-up record inputs must not
// also have an editorial selection.
func ValidateModelContent(model DataModel, placements []ModelContentPlacement, records []ModelContentRecord, selections []ModelContentSelection) error {
	return validateModelContent(model, placements, records, selections, true)
}

// validateModelContentWithoutEntityCardinality validates historical records
// against their retained model structure while ignoring the entity-level
// cardinality introduced by a later structural snapshot.
func validateModelContentWithoutEntityCardinality(model DataModel, placements []ModelContentPlacement, records []ModelContentRecord, selections []ModelContentSelection) error {
	return validateModelContent(model, placements, records, selections, false)
}

func validateModelContent(model DataModel, placements []ModelContentPlacement, records []ModelContentRecord, selections []ModelContentSelection, enforceEntityCardinality bool) error {
	if err := ValidateDataModel(model); err != nil {
		return fmt.Errorf("dataModel: %w", err)
	}

	entitiesByID := make(map[string]DataModelEntity, len(model.Entities))
	relationsByID := make(map[string]DataModelRelation, len(model.Relations))
	for _, entity := range model.Entities {
		entitiesByID[entity.ID] = entity
	}
	for _, relation := range model.Relations {
		relationsByID[relation.ID] = relation
	}

	placementsByID := make(map[string]ModelContentPlacement, len(placements))
	for _, placement := range placements {
		if strings.TrimSpace(placement.ID) == "" {
			return fmt.Errorf("modelContent.placements: placement ID is required")
		}
		if _, exists := placementsByID[placement.ID]; exists {
			return fmt.Errorf("modelContent.placements: duplicate placement ID %q", placement.ID)
		}
		if err := ValidateDataInputBindings(model, placement.Ports, placement.Bindings, placement.ArgumentBindings); err != nil {
			return fmt.Errorf("placement %q data bindings: %w", placement.ID, err)
		}
		if err := validateModelContentEnumBindings(placement, model, entitiesByID); err != nil {
			return fmt.Errorf("placement %q data bindings: %w", placement.ID, err)
		}
		placementsByID[placement.ID] = placement
	}

	recordsByID := make(map[string]*ModelContentRecord, len(records))
	for index := range records {
		record := &records[index]
		path := fmt.Sprintf("modelContent.records[%d]", index)
		if err := validateModelContentUUID(record.ID); err != nil {
			return fmt.Errorf("%s.id: %w", path, err)
		}
		if _, exists := recordsByID[record.ID]; exists {
			return fmt.Errorf("%s.id: duplicate record ID %q", path, record.ID)
		}
		if _, exists := entitiesByID[record.EntityID]; !exists {
			return fmt.Errorf("%s.entityId: unknown entity %q", path, record.EntityID)
		}
		if record.SortOrder < 0 {
			return fmt.Errorf("%s.sortOrder: must be nonnegative", path)
		}
		recordsByID[record.ID] = record
	}
	if enforceEntityCardinality {
		if err := validateModelContentEntityCardinality(model, records, false); err != nil {
			return err
		}
	}

	if err := validateModelContentValues(entitiesByID, records); err != nil {
		return err
	}
	if err := validateModelContentRelations(relationsByID, recordsByID, records); err != nil {
		return err
	}
	if err := validateModelContentSelections(placementsByID, entitiesByID, recordsByID, selections); err != nil {
		return err
	}
	return nil
}

// validateModelContentEntityCardinality checks the per-state record limit for
// singleton entities. Draft workspaces require exactly one row; published
// workspaces may remain empty until an explicit publication.
func validateModelContentEntityCardinality(model DataModel, records []ModelContentRecord, requireSingletonDraft bool) error {
	counts := make(map[string]int, len(model.Entities))
	for _, record := range records {
		counts[record.EntityID]++
	}
	for _, entity := range model.Entities {
		if entity.Cardinality != DataModelEntityCardinalitySingleton {
			continue
		}
		count := counts[entity.ID]
		if count > 1 {
			return fmt.Errorf("modelContent entity %q (%s) is singleton but has %d records; keep at most one record before saving the model", entity.Key, entity.Name, count)
		}
		if requireSingletonDraft && count != 1 {
			return fmt.Errorf("modelContent entity %q (%s) is singleton and requires exactly one draft record", entity.Key, entity.Name)
		}
	}
	return nil
}

func validateModelContentValues(entitiesByID map[string]DataModelEntity, records []ModelContentRecord) error {
	uniqueValues := make(map[string]map[string]string)
	compiledSchemas := make(map[string]modelContentScalarValidator)
	for index := range records {
		record := &records[index]
		entity := entitiesByID[record.EntityID]
		fieldsByKey := make(map[string]DataModelField, len(entity.Fields))
		for _, field := range entity.Fields {
			fieldsByKey[field.Key] = field
		}
		path := fmt.Sprintf("modelContent.records[%d].values", index)
		for key, value := range record.Values {
			field, exists := fieldsByKey[key]
			if !exists {
				return fmt.Errorf("%s.%s: field is not declared by entity %q", path, key, entity.Key)
			}
			if err := validateModelContentScalar(field, value, compiledSchemas); err != nil {
				return fmt.Errorf("%s.%s: %w", path, key, err)
			}
			if !field.Unique || isEmptyOptionalModelValue(value) {
				continue
			}
			valueKey, err := modelContentUniqueValue(field.Type, value)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", path, key, err)
			}
			uniqueKey := record.EntityID + "\x00" + field.ID
			if uniqueValues[uniqueKey] == nil {
				uniqueValues[uniqueKey] = make(map[string]string)
			}
			if previousID, exists := uniqueValues[uniqueKey][valueKey]; exists {
				return fmt.Errorf("%s.%s: unique value is already used by record %q", path, key, previousID)
			}
			uniqueValues[uniqueKey][valueKey] = record.ID
		}
	}
	return nil
}

type modelContentScalarValidator interface {
	Validate(interface{}) error
}

func validateModelContentScalar(field DataModelField, value any, compiled map[string]modelContentScalarValidator) error {
	if value == nil {
		return nil
	}
	if empty, ok := value.(string); ok && empty == "" {
		// Empty strings are the editor's normal representation of an optional
		// cleared text-like value, including optional formatted fields.
		return nil
	}
	schemaRaw, err := json.Marshal(dataInputTypeSchema(field.Type, nil, field.Options))
	if err != nil {
		return fmt.Errorf("could not encode scalar schema: %w", err)
	}
	key := string(schemaRaw)
	schema, exists := compiled[key]
	if !exists {
		schema, err = validate.Compile(schemaRaw)
		if err != nil {
			return fmt.Errorf("could not compile scalar schema: %w", err)
		}
		compiled[key] = schema
	}
	if err := schema.Validate(value); err != nil {
		return err
	}
	if field.Type == "media" {
		text, ok := value.(string)
		if !ok || validateModelContentUUID(text) != nil {
			return fmt.Errorf("must be a canonical media UUID")
		}
	}
	return nil
}

func modelContentNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func isEmptyOptionalModelValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}

func modelContentUniqueValue(fieldType string, value any) (string, error) {
	if fieldType == "integer" || fieldType == "number" {
		number, ok := modelContentNumber(value)
		if !ok {
			return "", fmt.Errorf("must be a finite number")
		}
		return "number:" + strconv.FormatFloat(number, 'g', -1, 64), nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("must be a string")
	}
	return "string:" + text, nil
}

func validateModelContentRelations(relationsByID map[string]DataModelRelation, recordsByID map[string]*ModelContentRecord, records []ModelContentRecord) error {
	uniqueOwners := make(map[string]string)
	for index := range records {
		record := &records[index]
		path := fmt.Sprintf("modelContent.records[%d].relations", index)
		for relationID, targetIDs := range record.Relations {
			relation, exists := relationsByID[relationID]
			if !exists {
				return fmt.Errorf("%s.%s: unknown relation ID", path, relationID)
			}
			if record.EntityID != relation.FromEntityID {
				return fmt.Errorf("%s.%s: relation source entity does not match record entity", path, relationID)
			}
			if relation.Cardinality == "one-to-one" && len(targetIDs) > 1 {
				return fmt.Errorf("%s.%s: one-to-one relation accepts at most one target", path, relationID)
			}
			seenTargets := make(map[string]bool, len(targetIDs))
			for targetIndex, targetID := range targetIDs {
				targetPath := fmt.Sprintf("%s.%s[%d]", path, relationID, targetIndex)
				if err := validateModelContentUUID(targetID); err != nil {
					return fmt.Errorf("%s: %w", targetPath, err)
				}
				if seenTargets[targetID] {
					return fmt.Errorf("%s: duplicate relation target %q", targetPath, targetID)
				}
				seenTargets[targetID] = true
				target, exists := recordsByID[targetID]
				if !exists {
					return fmt.Errorf("%s: referenced record does not exist", targetPath)
				}
				if target.EntityID != relation.ToEntityID {
					return fmt.Errorf("%s: target entity does not match relation target", targetPath)
				}
				if relation.Cardinality == "one-to-one" || relation.Cardinality == "one-to-many" {
					ownerKey := relationID + "\x00" + targetID
					if previousID, exists := uniqueOwners[ownerKey]; exists && previousID != record.ID {
						return fmt.Errorf("%s: target record %q is already linked from record %q", targetPath, targetID, previousID)
					}
					uniqueOwners[ownerKey] = record.ID
				}
			}
		}
	}
	return nil
}

func validateModelContentSelections(placements map[string]ModelContentPlacement, entities map[string]DataModelEntity, recordsByID map[string]*ModelContentRecord, selections []ModelContentSelection) error {
	type selectionKey struct {
		placementID string
		inputKey    string
	}
	seen := make(map[selectionKey]bool, len(selections))
	for index, selection := range selections {
		path := fmt.Sprintf("modelContent.selections[%d]", index)
		placement, exists := placements[selection.PlacementID]
		if !exists {
			return fmt.Errorf("%s.placementId: unknown placement %q", path, selection.PlacementID)
		}
		key := selectionKey{placementID: selection.PlacementID, inputKey: selection.InputKey}
		if seen[key] {
			return fmt.Errorf("%s: duplicate selection for placement %q input %q", path, selection.PlacementID, selection.InputKey)
		}
		seen[key] = true

		port, binding, exists := modelContentInputBinding(placement, selection.InputKey)
		if !exists {
			return fmt.Errorf("%s.inputKey: no bound data input %q on placement %q", path, selection.InputKey, selection.PlacementID)
		}
		if binding.Lookup != nil {
			return fmt.Errorf("%s: route-looked-up record inputs cannot have editorial selections", path)
		}
		entity := entities[binding.EntityID]
		if entity.Cardinality == DataModelEntityCardinalitySingleton && port.Kind == "collection" && selection.Mode == "ids" && len(selection.RecordIDs) > 1 {
			return fmt.Errorf("%s.recordIds: singleton collection input accepts at most one record ID", path)
		}
		switch selection.Mode {
		case "all":
			if port.Kind != "collection" {
				return fmt.Errorf("%s.mode: all is only valid for collection inputs", path)
			}
			if len(selection.RecordIDs) != 0 {
				return fmt.Errorf("%s.recordIds: all mode must not list record IDs", path)
			}
		case "ids":
			switch port.Kind {
			case "scalar", "record":
				if len(selection.RecordIDs) != 1 {
					return fmt.Errorf("%s.recordIds: %s input requires exactly one record ID", path, port.Kind)
				}
			case "collection":
			default:
				return fmt.Errorf("%s.inputKey: unsupported input kind %q", path, port.Kind)
			}
		default:
			return fmt.Errorf("%s.mode: must be all or ids", path)
		}

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
			record, exists := recordsByID[recordID]
			if !exists {
				return fmt.Errorf("%s: selected record does not exist", recordPath)
			}
			if record.EntityID != binding.EntityID {
				return fmt.Errorf("%s: selected record belongs to entity %q, not bound entity %q", recordPath, record.EntityID, binding.EntityID)
			}
		}
	}
	return nil
}

func modelContentInputBinding(placement ModelContentPlacement, inputKey string) (DataInputPort, DataInputBinding, bool) {
	var port DataInputPort
	portFound := false
	for _, candidate := range placement.Ports {
		if candidate.Key == inputKey {
			port = candidate
			portFound = true
			break
		}
	}
	if !portFound {
		return DataInputPort{}, DataInputBinding{}, false
	}
	for _, candidate := range placement.Bindings {
		if candidate.InputKey == inputKey {
			return port, candidate, true
		}
	}
	return DataInputPort{}, DataInputBinding{}, false
}

func validateModelContentEnumBindings(placement ModelContentPlacement, model DataModel, entitiesByID map[string]DataModelEntity) error {
	bindings := make(map[string]DataInputBinding, len(placement.Bindings))
	for _, binding := range placement.Bindings {
		bindings[binding.InputKey] = binding
	}
	for _, port := range placement.Ports {
		binding, exists := bindings[port.Key]
		if !exists {
			continue // The binding validator returns the more direct missing-binding error.
		}
		entity, exists := entitiesByID[binding.EntityID]
		if !exists {
			continue // The binding validator reports the unknown entity.
		}
		modelFields := make(map[string]DataModelField, len(entity.Fields))
		for _, field := range entity.Fields {
			modelFields[field.ID] = field
		}
		if port.Kind == "scalar" {
			modelField, exists := modelFields[binding.FieldID]
			if exists && port.Type == "enum" && modelField.Type == "enum" {
				if err := validateModelContentEnumOptions(port.Key, "", modelField, port.Values); err != nil {
					return err
				}
			}
			continue
		}
		declaredFields := make(map[string]DataInputField, len(port.Fields))
		for _, field := range port.Fields {
			segments, err := parseJSONPointer(field.Path)
			if err == nil {
				declaredFields[pointerKey(segments)] = field
			}
		}
		for _, mapping := range binding.Fields {
			segments, err := parseJSONPointer(mapping.InputPath)
			if err != nil {
				continue
			}
			declared, exists := declaredFields[pointerKey(segments)]
			fieldEntity, _, relationErr := dataInputRelationTarget(model, binding.EntityID, mapping.RelationIDs)
			if relationErr != nil {
				continue // The binding validator reports malformed relation paths.
			}
			targetFields := modelFields
			if fieldEntity.ID != entity.ID {
				targetFields = make(map[string]DataModelField, len(fieldEntity.Fields))
				for _, field := range fieldEntity.Fields {
					targetFields[field.ID] = field
				}
			}
			modelField, modelExists := targetFields[mapping.FieldID]
			if exists && modelExists && declared.Type == "enum" && modelField.Type == "enum" {
				if err := validateModelContentEnumOptions(port.Key, declared.Path, modelField, declared.Values); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateModelContentEnumOptions(inputKey, path string, modelField DataModelField, inputValues []string) error {
	accepted := make(map[string]bool, len(inputValues))
	for _, value := range inputValues {
		accepted[value] = true
	}
	for _, option := range modelField.Options {
		if !accepted[option] {
			if path == "" {
				return fmt.Errorf("input %q enum does not accept model field option %q", inputKey, option)
			}
			return fmt.Errorf("input %q field %q enum does not accept model field option %q", inputKey, path, option)
		}
	}
	return nil
}

func validateModelContentUUID(value string) error {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.String() != value {
		return fmt.Errorf("must be a canonical non-nil UUID")
	}
	return nil
}
