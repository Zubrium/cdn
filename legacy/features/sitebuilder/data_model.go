package sitebuilder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var dataModelKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var ErrLegacyModelRelations = errors.New("legacy_model_relations_not_supported")

// DataModel is the schema and editor layout attached to an immutable snapshot.
// Record values live in the snapshot content workspace, not here.
type DataModel struct {
	FormatVersion int                          `json:"formatVersion"`
	Entities      []DataModelEntity            `json:"entities"`
	Positions     map[string]DataModelPosition `json:"positions,omitempty"`
}

type DataModelEntityCardinality string

const (
	DataModelEntityCardinalityMany      DataModelEntityCardinality = "many"
	DataModelEntityCardinalitySingleton DataModelEntityCardinality = "singleton"
)

type DataModelEntity struct {
	ID          string                     `json:"id"`
	Key         string                     `json:"key"`
	Name        string                     `json:"name"`
	Cardinality DataModelEntityCardinality `json:"cardinality,omitempty"`
	Fields      []DataModelField           `json:"fields"`
}

type DataModelField struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Unique  bool     `json:"unique,omitempty"`
	Options []string `json:"options,omitempty"`
}

type DataModelPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

var dataModelFieldTypes = map[string]bool{
	"text": true, "longText": true, "integer": true, "number": true,
	"boolean": true, "date": true, "dateTime": true, "url": true,
	"email": true, "enum": true, "media": true,
}

// ValidateDataModel validates snapshot schema structure without inspecting any
// record values. CMS values are optional and validated by the content service.
func ValidateDataModel(model DataModel) error {
	if model.FormatVersion != 1 {
		return errors.New("dataModel.formatVersion must be 1")
	}
	entities := make(map[string]DataModelEntity, len(model.Entities))
	entityKeys := make(map[string]bool, len(model.Entities))
	ids := make(map[uuid.UUID]string)
	for _, entity := range model.Entities {
		if err := registerDataModelID(ids, entity.ID, "entity"); err != nil {
			return err
		}
		if entity.Cardinality != "" && entity.Cardinality != DataModelEntityCardinalityMany && entity.Cardinality != DataModelEntityCardinalitySingleton {
			return fmt.Errorf("dataModel entity %q cardinality must be %q or %q", entity.ID, DataModelEntityCardinalityMany, DataModelEntityCardinalitySingleton)
		}
		if !dataModelKeyPattern.MatchString(entity.Key) {
			return fmt.Errorf("dataModel entity %q has an invalid generated key", entity.ID)
		}
		if entityKeys[entity.Key] {
			return fmt.Errorf("dataModel contains duplicate entity key %q", entity.Key)
		}
		entityKeys[entity.Key] = true
		if !validDataModelName(entity.Name) {
			return fmt.Errorf("dataModel entity %q must have a name", entity.ID)
		}
		fieldKeys := make(map[string]bool, len(entity.Fields))
		for _, field := range entity.Fields {
			if err := registerDataModelID(ids, field.ID, "field"); err != nil {
				return err
			}
			if field.Key == "id" {
				return fmt.Errorf("dataModel entity %q field key %q is reserved for the generated record ID", entity.Key, field.Key)
			}
			if !dataModelKeyPattern.MatchString(field.Key) || fieldKeys[field.Key] {
				return fmt.Errorf("dataModel entity %q contains an invalid or duplicate field key", entity.Key)
			}
			fieldKeys[field.Key] = true
			if !validDataModelName(field.Name) {
				return fmt.Errorf("dataModel field %q in entity %q must have a name", field.Key, entity.Key)
			}
			if !dataModelFieldTypes[field.Type] {
				return fmt.Errorf("dataModel field %q has unsupported type %q", field.Key, field.Type)
			}
			if field.Unique && (field.Type == "longText" || field.Type == "media" || field.Type == "boolean") {
				return fmt.Errorf("dataModel field %q type %q cannot be unique", field.Key, field.Type)
			}
			if field.Type == "enum" {
				if len(field.Options) == 0 {
					return fmt.Errorf("dataModel enum field %q must have at least one option", field.Key)
				}
				options := make(map[string]bool, len(field.Options))
				for _, option := range field.Options {
					if strings.TrimSpace(option) == "" || options[option] {
						return fmt.Errorf("dataModel enum field %q has an empty or duplicate option", field.Key)
					}
					options[option] = true
				}
			} else if len(field.Options) > 0 {
				return fmt.Errorf("dataModel field %q only enum fields may declare options", field.Key)
			}
		}
		entities[entity.ID] = entity
	}

	for entityID, position := range model.Positions {
		if _, ok := entities[entityID]; !ok {
			return fmt.Errorf("dataModel canvas references unknown entity %q", entityID)
		}
		if math.IsNaN(position.X) || math.IsInf(position.X, 0) || math.IsNaN(position.Y) || math.IsInf(position.Y, 0) || position.X < 0 || position.Y < 0 || position.X > 100000 || position.Y > 100000 {
			return fmt.Errorf("dataModel canvas position for entity %q is out of range", entityID)
		}
	}
	return nil
}

// UnmarshalJSON keeps snapshots and API requests from returning or accepting
// the retired relation model while allowing legacy empty relation fields.
func (model *DataModel) UnmarshalJSON(raw []byte) error {
	type dataModelAlias DataModel
	alias := dataModelAlias{}
	wire := struct {
		*dataModelAlias
		Relations json.RawMessage `json:"relations"`
	}{dataModelAlias: &alias}
	if err := decodeStrictModelJSON(raw, &wire); err != nil {
		return err
	}
	if nonEmpty, err := legacyJSONValueNonEmpty(wire.Relations); err != nil {
		return fmt.Errorf("%w: dataModel.relations: %v", ErrLegacyModelRelations, err)
	} else if nonEmpty {
		return fmt.Errorf("%w: dataModel.relations", ErrLegacyModelRelations)
	}
	*model = DataModel(alias)
	return nil
}

func decodeStrictModelJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func legacyJSONValueNonEmpty(raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, nil
	}
	switch trimmed[0] {
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return false, err
		}
		return len(values) > 0, nil
	case '{':
		var values map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return false, err
		}
		return len(values) > 0, nil
	default:
		return true, nil
	}
}

func registerDataModelID(ids map[uuid.UUID]string, value, kind string) error {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.String() != value {
		return fmt.Errorf("dataModel %s ID must be a generated canonical UUID", kind)
	}
	if previousKind, exists := ids[id]; exists {
		return fmt.Errorf("dataModel contains duplicate ID %q for %s and %s", value, previousKind, kind)
	}
	ids[id] = kind
	return nil
}

func validDataModelName(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 120
}

func cloneDataModel(model *DataModel) *DataModel {
	if model == nil {
		return nil
	}
	cloned := *model
	cloned.Entities = append([]DataModelEntity(nil), model.Entities...)
	for index := range cloned.Entities {
		cloned.Entities[index].Fields = append([]DataModelField(nil), model.Entities[index].Fields...)
		for fieldIndex := range cloned.Entities[index].Fields {
			cloned.Entities[index].Fields[fieldIndex].Options = append([]string(nil), model.Entities[index].Fields[fieldIndex].Options...)
		}
	}
	if model.Positions != nil {
		cloned.Positions = make(map[string]DataModelPosition, len(model.Positions))
		for id, position := range model.Positions {
			cloned.Positions[id] = position
		}
	}
	return &cloned
}
