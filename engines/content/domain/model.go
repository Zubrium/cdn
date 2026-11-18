package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	modelKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	fieldTypes      = map[string]struct{}{
		"text": {}, "longText": {}, "integer": {}, "number": {}, "boolean": {},
		"date": {}, "dateTime": {}, "url": {}, "email": {}, "enum": {}, "media": {},
	}
)

type DataModel struct {
	FormatVersion int                 `json:"formatVersion"`
	Entities      []Entity            `json:"entities"`
	Positions     map[string]Position `json:"positions,omitempty"`
	Relations     json.RawMessage     `json:"relations,omitempty"`
}

type Entity struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Cardinality string  `json:"cardinality,omitempty"`
	Fields      []Field `json:"fields"`
}

type Field struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Unique  bool     `json:"unique,omitempty"`
	Options []string `json:"options,omitempty"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func ParseDataModel(raw json.RawMessage) (DataModel, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return DataModel{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var model DataModel
	if err := decoder.Decode(&model); err != nil {
		return DataModel{}, ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return DataModel{}, ErrInvalid
	}
	if err := model.Validate(); err != nil {
		return DataModel{}, err
	}
	return model, nil
}

func (model DataModel) Validate() error {
	if model.FormatVersion != 1 || model.Entities == nil || !emptyRelations(model.Relations) {
		return ErrInvalid
	}
	entityIDs := make(map[string]struct{}, len(model.Entities))
	entityKeys := make(map[string]struct{}, len(model.Entities))
	allIDs := make(map[string]struct{})
	for _, entity := range model.Entities {
		if !canonicalUUID(entity.ID) || !modelKeyPattern.MatchString(entity.Key) || !validName(entity.Name) || entity.Fields == nil {
			return ErrInvalid
		}
		if entity.Cardinality != "many" && entity.Cardinality != "singleton" {
			return ErrInvalid
		}
		if _, exists := entityIDs[entity.ID]; exists {
			return ErrInvalid
		}
		if _, exists := entityKeys[entity.Key]; exists {
			return ErrInvalid
		}
		entityIDs[entity.ID] = struct{}{}
		entityKeys[entity.Key] = struct{}{}
		if _, exists := allIDs[entity.ID]; exists {
			return ErrInvalid
		}
		allIDs[entity.ID] = struct{}{}
		fieldKeys := make(map[string]struct{}, len(entity.Fields))
		for _, field := range entity.Fields {
			if !canonicalUUID(field.ID) || !modelKeyPattern.MatchString(field.Key) || field.Key == "id" || !validName(field.Name) {
				return ErrInvalid
			}
			if _, exists := fieldTypes[field.Type]; !exists {
				return ErrInvalid
			}
			if _, exists := fieldKeys[field.Key]; exists {
				return ErrInvalid
			}
			if _, exists := allIDs[field.ID]; exists {
				return ErrInvalid
			}
			fieldKeys[field.Key] = struct{}{}
			allIDs[field.ID] = struct{}{}
			if field.Unique && (field.Type == "longText" || field.Type == "media" || field.Type == "boolean") {
				return ErrInvalid
			}
			if field.Type == "enum" {
				if len(field.Options) == 0 {
					return ErrInvalid
				}
				options := make(map[string]struct{}, len(field.Options))
				for _, option := range field.Options {
					if strings.TrimSpace(option) == "" {
						return ErrInvalid
					}
					if _, exists := options[option]; exists {
						return ErrInvalid
					}
					options[option] = struct{}{}
				}
			} else if len(field.Options) != 0 {
				return ErrInvalid
			}
		}
	}
	for entityID, position := range model.Positions {
		if _, exists := entityIDs[entityID]; !exists || math.IsNaN(position.X) || math.IsInf(position.X, 0) ||
			math.IsNaN(position.Y) || math.IsInf(position.Y, 0) || position.X < 0 || position.Y < 0 || position.X > 100000 || position.Y > 100000 {
			return ErrInvalid
		}
	}
	return nil
}

func EmptyDataModel() DataModel { return DataModel{FormatVersion: 1, Entities: []Entity{}} }

func validName(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 120 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func emptyRelations(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	var relations []json.RawMessage
	return json.Unmarshal(trimmed, &relations) == nil && len(relations) == 0
}

func canonicalUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func ValidID(value string) bool { return canonicalUUID(value) }

func strictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}
