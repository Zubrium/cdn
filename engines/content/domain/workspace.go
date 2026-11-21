package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Placement struct {
	ID     string
	Inputs []PlacementInput
}

type PlacementInput struct {
	InputKey string
	Kind     string
	EntityID string
	Fields   []PlacementField
	Lookup   *PlacementInputLookup
}

type PlacementField struct {
	InputPath string
	FieldID   string
}

type PlacementInputLookup struct {
	Argument string
	FieldID  string
}

type Record struct {
	ID            string         `json:"id"`
	TranslationID string         `json:"translation_id"`
	Locale        string         `json:"locale"`
	EntityID      string         `json:"entity_id"`
	SortOrder     int            `json:"sort_order"`
	Values        map[string]any `json:"values"`
}

type Selection struct {
	PlacementID string   `json:"placement_id"`
	InputKey    string   `json:"input_key"`
	Mode        string   `json:"mode"`
	RecordIDs   []string `json:"record_ids,omitempty"`
}

type WorkspaceData struct {
	Locale     string      `json:"locale"`
	Records    []Record    `json:"records"`
	Selections []Selection `json:"selections"`
}

func ValidateWorkspace(model DataModel, placements []Placement, data WorkspaceData, previous *WorkspaceData, publishing bool) error {
	if err := model.Validate(); err != nil {
		return err
	}
	entities := make(map[string]Entity, len(model.Entities))
	for _, entity := range model.Entities {
		entities[entity.ID] = entity
	}
	previousRecords := make(map[string]Record)
	if previous != nil {
		for _, record := range previous.Records {
			previousRecords[record.ID] = record
		}
	}
	placementMap, err := validatePlacements(placements, entities)
	if err != nil {
		return err
	}
	recordMap := make(map[string]Record, len(data.Records))
	entityCounts := make(map[string]int)
	translationGroups := make(map[string]string, len(data.Records))
	uniqueValues := make(map[string]map[string]string)
	locale, localeErr := CanonicalLocale(data.Locale)
	if localeErr != nil || locale != data.Locale {
		return ErrInvalid
	}
	for _, record := range data.Records {
		if !canonicalUUID(record.ID) || !canonicalUUID(record.TranslationID) || record.Locale != locale || !canonicalUUID(record.EntityID) || record.SortOrder < 0 {
			return ErrInvalid
		}
		groupEntity, grouped := translationGroups[record.TranslationID]
		if grouped && groupEntity != record.EntityID {
			return ErrInvalid
		}
		if grouped {
			return ErrInvalid
		}
		translationGroups[record.TranslationID] = record.EntityID
		if _, exists := recordMap[record.ID]; exists {
			return ErrInvalid
		}
		entity, exists := entities[record.EntityID]
		if !exists {
			old, hadBefore := previousRecords[record.ID]
			if publishing || !hadBefore || old.EntityID != record.EntityID || !reflect.DeepEqual(old, record) {
				return ErrInvalid
			}
			recordMap[record.ID] = cloneRecord(record)
			continue
		}
		entityCounts[entity.ID]++
		if record.Values == nil {
			record.Values = map[string]any{}
		}
		fields := make(map[string]Field, len(entity.Fields))
		for _, field := range entity.Fields {
			fields[field.Key] = field
		}
		old, hadBefore := previousRecords[record.ID]
		for key, value := range record.Values {
			field, known := fields[key]
			if !known {
				oldValue, existed := old.Values[key]
				if publishing || !hadBefore || old.EntityID != record.EntityID || !existed || !reflect.DeepEqual(oldValue, value) {
					return ErrInvalid
				}
				continue
			}
			if err := validateFieldValue(field, value); err != nil {
				oldValue, existed := old.Values[key]
				if publishing || !hadBefore || old.EntityID != record.EntityID || !existed || !reflect.DeepEqual(oldValue, value) {
					return fmt.Errorf("%w: field %s", err, key)
				}
				continue
			}
			if !field.Unique || isEmptyValue(value) {
				continue
			}
			uniqueKey, err := canonicalUniqueValue(field, value)
			if err != nil {
				return ErrInvalid
			}
			key := entity.ID + "\x00" + field.ID
			if uniqueValues[key] == nil {
				uniqueValues[key] = make(map[string]string)
			}
			if _, exists := uniqueValues[key][uniqueKey]; exists {
				return ErrInvalid
			}
			uniqueValues[key][uniqueKey] = record.ID
		}
		recordMap[record.ID] = cloneRecord(record)
	}
	for _, entity := range model.Entities {
		if entity.Cardinality == "singleton" && entityCounts[entity.ID] != 1 {
			return ErrInvalid
		}
	}
	return validateSelections(placementMap, entities, recordMap, data.Selections, previous, publishing)
}

func validatePlacements(placements []Placement, entities map[string]Entity) (map[string]Placement, error) {
	result := make(map[string]Placement, len(placements))
	for _, placement := range placements {
		if placement.ID == "" || len(placement.ID) > 256 {
			return nil, ErrInvalid
		}
		if _, exists := result[placement.ID]; exists {
			return nil, ErrInvalid
		}
		inputs := make(map[string]struct{}, len(placement.Inputs))
		for _, input := range placement.Inputs {
			if input.InputKey == "" || len(input.InputKey) > 128 || (input.Kind != "scalar" && input.Kind != "record" && input.Kind != "collection") {
				return nil, ErrInvalid
			}
			if _, exists := inputs[input.InputKey]; exists {
				return nil, ErrInvalid
			}
			inputs[input.InputKey] = struct{}{}
			entity, exists := entities[input.EntityID]
			if !exists {
				return nil, ErrInvalid
			}
			fieldIDs := make(map[string]struct{}, len(entity.Fields))
			for _, field := range entity.Fields {
				fieldIDs[field.ID] = struct{}{}
			}
			for _, mapping := range input.Fields {
				if mapping.InputPath == "" {
					return nil, ErrInvalid
				}
				if _, exists := fieldIDs[mapping.FieldID]; !exists {
					return nil, ErrInvalid
				}
			}
			if input.Lookup != nil {
				if input.Kind != "record" || input.Lookup.Argument == "" {
					return nil, ErrInvalid
				}
				if _, exists := fieldIDs[input.Lookup.FieldID]; !exists {
					return nil, ErrInvalid
				}
			}
		}
		result[placement.ID] = placement
	}
	return result, nil
}

func validateSelections(placements map[string]Placement, entities map[string]Entity, records map[string]Record, selections []Selection, previous *WorkspaceData, publishing bool) error {
	oldSelections := make(map[string]Selection)
	if previous != nil {
		for _, selection := range previous.Selections {
			oldSelections[selection.PlacementID+"\x00"+selection.InputKey] = selection
		}
	}
	seen := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		key := selection.PlacementID + "\x00" + selection.InputKey
		if _, exists := seen[key]; exists {
			return ErrInvalid
		}
		seen[key] = struct{}{}
		if selection.Mode != "all" && selection.Mode != "ids" {
			return ErrInvalid
		}
		seenRecordIDs := make(map[string]struct{}, len(selection.RecordIDs))
		for _, recordID := range selection.RecordIDs {
			if !canonicalUUID(recordID) {
				return ErrInvalid
			}
			if _, exists := seenRecordIDs[recordID]; exists {
				return ErrInvalid
			}
			seenRecordIDs[recordID] = struct{}{}
		}
		placement, exists := placements[selection.PlacementID]
		if !exists {
			old, hadBefore := oldSelections[key]
			if !publishing && hadBefore && reflect.DeepEqual(old, selection) {
				continue
			}
			return ErrInvalid
		}
		var input *PlacementInput
		for index := range placement.Inputs {
			if placement.Inputs[index].InputKey == selection.InputKey {
				input = &placement.Inputs[index]
				break
			}
		}
		if input == nil || input.Lookup != nil {
			old, hadBefore := oldSelections[key]
			if !publishing && hadBefore && reflect.DeepEqual(old, selection) {
				continue
			}
			return ErrInvalid
		}
		entity := entities[input.EntityID]
		old, hadBefore := oldSelections[key]
		unchangedOldSelection := !publishing && hadBefore && reflect.DeepEqual(old, selection)
		shapeValid := true
		if selection.Mode == "all" {
			shapeValid = input.Kind == "collection" && len(selection.RecordIDs) == 0
		} else {
			if (input.Kind == "scalar" || input.Kind == "record") && len(selection.RecordIDs) != 1 {
				shapeValid = false
			}
			if input.Kind == "collection" && entity.Cardinality == "singleton" && len(selection.RecordIDs) > 1 {
				shapeValid = false
			}
			// A scalar or record input backed by a singleton resolves directly
			// from its only published record; a persisted manual choice is orphaned.
			if entity.Cardinality == "singleton" && input.Kind != "collection" {
				shapeValid = false
			}
		}
		if !shapeValid {
			if unchangedOldSelection {
				continue
			}
			return ErrInvalid
		}
		orphaned := false
		for _, recordID := range selection.RecordIDs {
			record, exists := records[recordID]
			if !exists || record.EntityID != input.EntityID {
				orphaned = true
			}
		}
		if orphaned && !unchangedOldSelection {
			return ErrInvalid
		}
	}
	return nil
}

func validateFieldValue(field Field, value any) error {
	if value == nil {
		return nil
	}
	if text, ok := value.(string); ok && text == "" {
		return nil
	}
	switch field.Type {
	case "text", "longText":
		_, ok := value.(string)
		if !ok {
			return ErrInvalid
		}
	case "integer":
		number, ok := numericValue(value)
		if !ok || math.Trunc(number) != number {
			return ErrInvalid
		}
	case "number":
		if _, ok := numericValue(value); !ok {
			return ErrInvalid
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return ErrInvalid
		}
	case "date":
		text, ok := value.(string)
		if !ok {
			return ErrInvalid
		}
		if _, err := time.Parse("2006-01-02", text); err != nil {
			return ErrInvalid
		}
	case "dateTime":
		text, ok := value.(string)
		if !ok {
			return ErrInvalid
		}
		if _, err := time.Parse(time.RFC3339, text); err != nil {
			return ErrInvalid
		}
	case "url":
		text, ok := value.(string)
		if !ok || strings.ContainsAny(text, " \t\r\n") {
			return ErrInvalid
		}
		parsed, err := url.Parse(text)
		if err != nil || parsed.Scheme == "" {
			return ErrInvalid
		}
	case "email":
		text, ok := value.(string)
		if !ok {
			return ErrInvalid
		}
		parsed, err := mail.ParseAddress(text)
		if err != nil || parsed.Address != text {
			return ErrInvalid
		}
	case "enum":
		text, ok := value.(string)
		if !ok {
			return ErrInvalid
		}
		for _, option := range field.Options {
			if option == text {
				return nil
			}
		}
		return ErrInvalid
	case "media":
		text, ok := value.(string)
		if !ok || !canonicalUUID(text) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func numericValue(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case int32:
		number = float64(typed)
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func canonicalUniqueValue(field Field, value any) (string, error) {
	if field.Type == "number" || field.Type == "integer" {
		number, ok := numericValue(value)
		if !ok {
			return "", ErrInvalid
		}
		return "number:" + strconv.FormatFloat(number, 'g', -1, 64), nil
	}
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) {
		return "", ErrInvalid
	}
	return "string:" + text, nil
}

func isEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}

func cloneRecord(record Record) Record {
	cloned := record
	cloned.Values = cloneMap(record.Values)
	return cloned
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
