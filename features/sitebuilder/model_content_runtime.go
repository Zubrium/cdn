package sitebuilder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// projectModelContent projects published snapshot records into the existing
// feature runtime content channel. It does not create per-feature record
// copies; dataInputs and placement bindings define the output shape.
func projectModelContent(model DataModel, manifest Manifest, placement Placement, arguments json.RawMessage, records []ModelContentRecord, selections []ModelContentSelection) (json.RawMessage, json.RawMessage, json.RawMessage, error) {
	return projectModelContentWithIndex(model, manifest, placement, arguments, records, selections, newModelContentReadIndex(records))
}

func projectModelContentWithIndex(model DataModel, manifest Manifest, placement Placement, arguments json.RawMessage, records []ModelContentRecord, selections []ModelContentSelection, readIndex *modelContentReadIndex) (json.RawMessage, json.RawMessage, json.RawMessage, error) {
	if readIndex == nil {
		readIndex = newModelContentReadIndex(records)
	}
	if !readIndex.cardinalityValidated {
		if err := validateModelContentEntityCardinality(model, records, false); err != nil {
			return nil, nil, nil, fmt.Errorf("published model content cardinality is invalid: %w", err)
		}
		readIndex.cardinalityValidated = true
	}
	content := make(map[string]any, len(manifest.DataInputs))
	selectionByKey := make(map[string]ModelContentSelection)
	for _, selection := range selections {
		if selection.PlacementID == placement.ID {
			selectionByKey[selection.InputKey] = selection
		}
	}
	entities := make(map[string]DataModelEntity, len(model.Entities))
	fields := make(map[string]map[string]DataModelField, len(model.Entities))
	for _, entity := range model.Entities {
		entities[entity.ID] = entity
		fields[entity.ID] = make(map[string]DataModelField, len(entity.Fields))
		for _, field := range entity.Fields {
			fields[entity.ID][field.ID] = field
		}
	}
	recordsByID := readIndex.recordsByID
	var routeArguments map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&routeArguments); err != nil && len(arguments) > 0 {
		return nil, nil, nil, fmt.Errorf("decode runtime arguments for model lookup: %w", err)
	}
	var detailSEOContent json.RawMessage
	for _, port := range manifest.DataInputs {
		binding, exists := dataInputBindingByKey(placement.DataBindings, port.Key)
		if !exists {
			return nil, nil, nil, fmt.Errorf("placement %q is missing the %q data-input binding", placement.ID, port.Key)
		}
		entity, exists := entities[binding.EntityID]
		if !exists {
			return nil, nil, nil, fmt.Errorf("placement %q input %q references a missing model entity", placement.ID, port.Key)
		}
		candidates := readIndex.recordsForEntity(entity.ID)
		selection, selected := selectionByKey[port.Key]
		var value any
		omitValue := false
		switch port.Kind {
		case "scalar":
			var chosen *ModelContentRecord
			if selected && selection.Mode == "ids" && len(selection.RecordIDs) == 1 {
				chosen = readIndex.recordByID(entity.ID, selection.RecordIDs[0])
				if chosen == nil {
					return nil, nil, nil, fmt.Errorf("published scalar input %q references a missing record", port.Key)
				}
			} else if !selected && entity.Cardinality == DataModelEntityCardinalitySingleton && len(candidates) == 1 {
				chosen = &candidates[0]
			} else if !selected && entity.Cardinality == DataModelEntityCardinalitySingleton {
				omitValue = true
			}
			if chosen != nil {
				projected, present, err := projectModelFieldValue(model, *chosen, binding.RelationIDs, binding.FieldID, recordsByID)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("placement %q scalar input %q: %w", placement.ID, port.Key, err)
				}
				if present {
					value = projected
				}
			}
		case "record":
			var chosen *ModelContentRecord
			if binding.Lookup != nil && entity.Cardinality == DataModelEntityCardinalitySingleton && len(candidates) == 0 {
				value = map[string]any{}
			} else if binding.Lookup != nil {
				field, ok := fields[entity.ID][binding.Lookup.FieldID]
				if !ok {
					return nil, nil, nil, fmt.Errorf("placement %q record input %q lookup field is missing", placement.ID, port.Key)
				}
				lookupRaw := routeArguments[binding.Lookup.Argument]
				var lookupValue any
				if len(lookupRaw) > 0 {
					lookupDecoder := json.NewDecoder(bytes.NewReader(lookupRaw))
					lookupDecoder.UseNumber()
					if err := lookupDecoder.Decode(&lookupValue); err != nil {
						return nil, nil, nil, fmt.Errorf("decode route lookup for input %q: %w", port.Key, err)
					}
				}
				if len(lookupRaw) > 0 {
					chosen = readIndex.recordByLookup(entity.ID, field.Key, lookupValue)
				}
				if chosen == nil {
					return nil, nil, nil, fmt.Errorf("%w: published record for detail input %q was not found", ErrNotFound, port.Key)
				}
			} else if selected && selection.Mode == "ids" && len(selection.RecordIDs) == 1 {
				chosen = readIndex.recordByID(entity.ID, selection.RecordIDs[0])
				if chosen == nil {
					return nil, nil, nil, fmt.Errorf("published record input %q references a missing record", port.Key)
				}
			} else if !selected && entity.Cardinality == DataModelEntityCardinalitySingleton && len(candidates) == 1 {
				chosen = &candidates[0]
			}
			if chosen == nil {
				value = map[string]any{}
			} else {
				projected, err := projectModelRecord(port, binding, model, recordsByID, *chosen)
				if err != nil {
					return nil, nil, nil, err
				}
				value = projected
				if binding.DetailSEO != nil {
					detailSEOContent, err = projectDetailSEOFields(entity, *chosen, *binding.DetailSEO)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("placement %q detail SEO: %w", placement.ID, err)
					}
				}
			}
		case "collection":
			chosen := candidates
			if selected && selection.Mode == "ids" {
				chosen = make([]ModelContentRecord, 0, len(selection.RecordIDs))
				for _, id := range selection.RecordIDs {
					record := readIndex.recordByID(entity.ID, id)
					if record == nil {
						return nil, nil, nil, fmt.Errorf("published collection input %q references a missing record", port.Key)
					}
					chosen = append(chosen, *record)
				}
			}
			projected := make([]any, 0, len(chosen))
			for _, record := range chosen {
				item, err := projectModelRecord(port, binding, model, recordsByID, record)
				if err != nil {
					return nil, nil, nil, err
				}
				projected = append(projected, item)
			}
			value = projected
		default:
			return nil, nil, nil, fmt.Errorf("placement %q input %q has unsupported kind %q", placement.ID, port.Key, port.Kind)
		}
		if !omitValue {
			content[port.Key] = value
		}
	}
	contentRaw, err := json.Marshal(content)
	if err != nil {
		return nil, nil, nil, err
	}
	schemaRaw, err := dataInputsRuntimeSchema(manifest.DataInputs)
	if err != nil {
		return nil, nil, nil, err
	}
	return contentRaw, schemaRaw, detailSEOContent, nil
}

func projectDetailSEOFields(entity DataModelEntity, record ModelContentRecord, binding DetailSEOFieldBindings) (json.RawMessage, error) {
	values := make(map[string]string, 5)
	fields := []struct {
		key     string
		fieldID string
	}{
		{key: "title", fieldID: binding.TitleFieldID},
		{key: "description", fieldID: binding.DescriptionFieldID},
		{key: "ogTitle", fieldID: binding.OGTitleFieldID},
		{key: "ogDescription", fieldID: binding.OGDescriptionFieldID},
		{key: "ogImage", fieldID: binding.OGImageFieldID},
	}
	for _, configured := range fields {
		if configured.fieldID == "" {
			continue
		}
		field, exists := dataModelFieldByID(entity, configured.fieldID)
		if !exists {
			return nil, fmt.Errorf("field %q is missing from bound entity %q", configured.fieldID, entity.ID)
		}
		raw, exists := record.Values[field.Key]
		if !exists || raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("field %q is not a string", configured.fieldID)
		}
		value = strings.TrimSpace(value)
		if value != "" {
			values[configured.key] = value
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func dataInputsRuntimeSchema(ports []DataInputPort) (json.RawMessage, error) {
	properties := make(map[string]any, len(ports))
	for _, port := range ports {
		switch port.Kind {
		case "scalar":
			properties[port.Key] = dataInputTypeSchema(port.Type, port.Profile, port.Values)
		case "record":
			properties[port.Key] = dataInputFieldsSchema(port.Fields)
		case "collection":
			properties[port.Key] = map[string]any{"type": "array", "items": dataInputFieldsSchema(port.Fields)}
		default:
			return nil, fmt.Errorf("data input %q has unsupported kind %q", port.Key, port.Kind)
		}
	}
	raw, err := json.Marshal(map[string]any{"type": "object", "properties": properties, "additionalProperties": false})
	return raw, err
}

func modelRecordsForEntity(records []ModelContentRecord, entityID string) []ModelContentRecord {
	items := make([]ModelContentRecord, 0)
	for _, record := range records {
		if record.EntityID == entityID {
			items = append(items, record)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SortOrder != items[j].SortOrder {
			return items[i].SortOrder < items[j].SortOrder
		}
		return items[i].ID < items[j].ID
	})
	return items
}

type modelContentLookupIndexKey struct {
	entityID string
	fieldKey string
}

// modelContentReadIndex is shared while composing multiple public paths for
// one immutable snapshot. A sitemap can contain tens of thousands of detail
// records, so filtering/scanning the full published record slice for each
// detail URL would make enumeration quadratic in catalog size.
type modelContentReadIndex struct {
	recordsByEntity      map[string][]ModelContentRecord
	recordsByID          map[string]*ModelContentRecord
	lookupByField        map[modelContentLookupIndexKey]map[string][]*ModelContentRecord
	cardinalityValidated bool
}

func newModelContentReadIndex(records []ModelContentRecord) *modelContentReadIndex {
	index := &modelContentReadIndex{
		recordsByEntity: make(map[string][]ModelContentRecord),
		recordsByID:     make(map[string]*ModelContentRecord, len(records)),
		lookupByField:   make(map[modelContentLookupIndexKey]map[string][]*ModelContentRecord),
	}
	for i := range records {
		record := &records[i]
		index.recordsByEntity[record.EntityID] = append(index.recordsByEntity[record.EntityID], *record)
		index.recordsByID[record.ID] = record
	}
	for entityID, items := range index.recordsByEntity {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].SortOrder != items[j].SortOrder {
				return items[i].SortOrder < items[j].SortOrder
			}
			return items[i].ID < items[j].ID
		})
		index.recordsByEntity[entityID] = items
	}
	return index
}

func (index *modelContentReadIndex) recordsForEntity(entityID string) []ModelContentRecord {
	if index == nil {
		return nil
	}
	return index.recordsByEntity[entityID]
}

func (index *modelContentReadIndex) recordByID(entityID, recordID string) *ModelContentRecord {
	if index == nil {
		return nil
	}
	record := index.recordsByID[recordID]
	if record == nil || record.EntityID != entityID {
		return nil
	}
	return record
}

func (index *modelContentReadIndex) recordByLookup(entityID, fieldKey string, value any) *ModelContentRecord {
	if index == nil {
		return nil
	}
	valueRaw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	key := modelContentLookupIndexKey{entityID: entityID, fieldKey: fieldKey}
	lookup := index.lookupByField[key]
	if lookup == nil {
		lookup = make(map[string][]*ModelContentRecord)
		for _, candidate := range index.recordsByEntity[entityID] {
			record := index.recordsByID[candidate.ID]
			if record == nil {
				continue
			}
			fieldValue, exists := record.Values[fieldKey]
			if !exists {
				continue
			}
			fieldRaw, marshalErr := json.Marshal(fieldValue)
			if marshalErr == nil {
				lookup[string(fieldRaw)] = append(lookup[string(fieldRaw)], record)
			}
		}
		index.lookupByField[key] = lookup
	}
	for _, candidate := range lookup[string(valueRaw)] {
		if equalModelValue(candidate.Values[fieldKey], value) {
			return candidate
		}
	}
	return nil
}

func findModelRecord(records []ModelContentRecord, id string) *ModelContentRecord {
	for i := range records {
		if records[i].ID == id {
			return &records[i]
		}
	}
	return nil
}

func projectModelRecord(port DataInputPort, binding DataInputBinding, model DataModel, recordsByID map[string]*ModelContentRecord, record ModelContentRecord) (map[string]any, error) {
	projected := map[string]any{"id": record.ID}
	for _, mapping := range binding.Fields {
		value, present, err := projectModelFieldValue(model, record, mapping.RelationIDs, mapping.FieldID, recordsByID)
		if err != nil {
			return nil, fmt.Errorf("data input %q references an invalid model path: %w", port.Key, err)
		}
		if !present {
			continue
		}
		segments, err := parseJSONPointer(mapping.InputPath)
		if err != nil || len(segments) == 0 {
			return nil, fmt.Errorf("data input %q has invalid input path %q", port.Key, mapping.InputPath)
		}
		if err := setModelInputPath(projected, segments, value); err != nil {
			return nil, fmt.Errorf("data input %q path %q: %w", port.Key, mapping.InputPath, err)
		}
	}
	return projected, nil
}

func projectModelFieldValue(model DataModel, record ModelContentRecord, relationIDs []string, fieldID string, recordsByID map[string]*ModelContentRecord) (any, bool, error) {
	entities := make(map[string]DataModelEntity, len(model.Entities))
	for _, entity := range model.Entities {
		entities[entity.ID] = entity
	}
	relations := make(map[string]DataModelRelation, len(model.Relations))
	for _, relation := range model.Relations {
		relations[relation.ID] = relation
	}
	var walk func(ModelContentRecord, int) (any, bool, error)
	walk = func(current ModelContentRecord, index int) (any, bool, error) {
		if index == len(relationIDs) {
			entity, exists := entities[current.EntityID]
			if !exists {
				return nil, false, fmt.Errorf("record %q belongs to an unknown entity", current.ID)
			}
			field, exists := dataModelFieldByID(entity, fieldID)
			if !exists {
				return nil, false, fmt.Errorf("field %q is missing from entity %q", fieldID, entity.ID)
			}
			value, present := current.Values[field.Key]
			return value, present, nil
		}
		relation, exists := relations[relationIDs[index]]
		if !exists || relation.FromEntityID != current.EntityID {
			return nil, false, fmt.Errorf("relation %q does not start at entity %q", relationIDs[index], current.EntityID)
		}
		targetIDs := current.Relations[relation.ID]
		multi := relation.Cardinality == "one-to-many" || relation.Cardinality == "many-to-many"
		if multi {
			values := make([]any, 0, len(targetIDs))
			for _, targetID := range targetIDs {
				target := recordsByID[targetID]
				if target == nil || target.EntityID != relation.ToEntityID {
					return nil, false, fmt.Errorf("relation %q references a missing or incompatible target", relation.ID)
				}
				value, present, err := walk(*target, index+1)
				if err != nil {
					return nil, false, err
				}
				if !present {
					value = map[string]any{}
				}
				values = append(values, value)
			}
			return values, true, nil
		}
		if len(targetIDs) == 0 {
			return nil, false, nil
		}
		if len(targetIDs) != 1 {
			return nil, false, fmt.Errorf("one-to-one relation %q has more than one target", relation.ID)
		}
		target := recordsByID[targetIDs[0]]
		if target == nil || target.EntityID != relation.ToEntityID {
			return nil, false, fmt.Errorf("relation %q references a missing or incompatible target", relation.ID)
		}
		return walk(*target, index+1)
	}
	return walk(record, 0)
}

func setModelInputPath(target map[string]any, segments []string, value any) error {
	_, err := setModelInputPathValue(target, segments, 0, value)
	return err
}

func setModelInputPathValue(current any, segments []string, index int, value any) (any, error) {
	if index >= len(segments) {
		return value, nil
	}
	switch target := current.(type) {
	case map[string]any:
		segment := segments[index]
		if segment == "*" {
			return nil, fmt.Errorf("array wildcard has no containing array")
		}
		if index == len(segments)-1 {
			target[segment] = value
			return target, nil
		}
		child := target[segment]
		if child == nil {
			if segments[index+1] == "*" {
				items, ok := value.([]any)
				if !ok {
					return nil, fmt.Errorf("path %q expects an array relation value", strings.Join(segments, "/"))
				}
				child = make([]any, len(items))
			} else {
				child = map[string]any{}
			}
		}
		updated, err := setModelInputPathValue(child, segments, index+1, value)
		if err != nil {
			return nil, err
		}
		target[segment] = updated
		return target, nil
	case []any:
		if segments[index] != "*" {
			return nil, fmt.Errorf("path %q expects an array wildcard", strings.Join(segments, "/"))
		}
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("path %q expects an array relation value", strings.Join(segments, "/"))
		}
		if len(target) != 0 && len(target) != len(items) {
			return nil, fmt.Errorf("path %q maps arrays with different lengths", strings.Join(segments, "/"))
		}
		if len(target) == 0 {
			target = make([]any, len(items))
		}
		for itemIndex := range items {
			child := target[itemIndex]
			if child == nil {
				if index+1 < len(segments) && segments[index+1] == "*" {
					nested, ok := items[itemIndex].([]any)
					if !ok {
						return nil, fmt.Errorf("path %q expects a nested array relation value", strings.Join(segments, "/"))
					}
					child = make([]any, len(nested))
				} else {
					child = map[string]any{}
				}
			}
			updated, err := setModelInputPathValue(child, segments, index+1, items[itemIndex])
			if err != nil {
				return nil, err
			}
			target[itemIndex] = updated
		}
		return target, nil
	default:
		return nil, fmt.Errorf("path %q crosses a non-object value", strings.Join(segments, "/"))
	}
}

func equalModelValue(left, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}
