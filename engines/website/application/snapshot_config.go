package application

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

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

type snapshotConfigProjection struct {
	FormatVersion int `json:"format_version"`
	Page          *snapshotPageProjection `json:"page"`
	Pages         []snapshotPageProjection `json:"pages"`
}

type snapshotPageProjection struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Placements []struct {
		ID          string `json:"id"`
		Slot        string `json:"slot"`
		Build       *struct {
			FeatureKey string `json:"featureKey"`
			Version    string `json:"version"`
		} `json:"build"`
		DataBindings []snapshotDataBinding `json:"dataBindings"`
	} `json:"placements"`
}

type snapshotDataBinding struct {
	InputKey  string `json:"inputKey"`
	EntityID  string `json:"entityId"`
	FieldID   string `json:"fieldId"`
	Fields    []struct {
		InputPath string `json:"inputPath"`
		FieldID   string `json:"fieldId"`
	} `json:"fields"`
	Lookup *struct {
		Argument string `json:"argument"`
		FieldID  string `json:"fieldId"`
	} `json:"lookup"`
	DetailSEO *struct {
		TitleFieldID       string `json:"titleFieldId"`
		DescriptionFieldID string `json:"descriptionFieldId"`
		OGTitleFieldID     string `json:"ogTitleFieldId"`
		OGDescriptionFieldID string `json:"ogDescriptionFieldId"`
		OGImageFieldID     string `json:"ogImageFieldId"`
	} `json:"detailSEO"`
}

func snapshotPageFromConfig(raw json.RawMessage) (snapshotPageProjection, error) {
	if !validJSONObject(raw) {
		return snapshotPageProjection{}, websiteapi.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return snapshotPageProjection{}, websiteapi.ErrInvalid
	}
	if _, hasModel := fields["dataModel"]; hasModel {
		return snapshotPageProjection{}, websiteapi.ErrInvalid
	}
	var config snapshotConfigProjection
	if err := json.Unmarshal(raw, &config); err != nil {
		return snapshotPageProjection{}, websiteapi.ErrInvalid
	}
	if config.Page != nil {
		if len(config.Pages) != 0 {
			return snapshotPageProjection{}, websiteapi.ErrInvalid
		}
		return *config.Page, nil
	}
	if len(config.Pages) != 1 {
		return snapshotPageProjection{}, websiteapi.ErrInvalid
	}
	return config.Pages[0], nil
}

var dataModelKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type dataModelContract struct {
	FormatVersion int                 `json:"formatVersion"`
	Entities      *[]dataModelEntity  `json:"entities"`
	Positions     map[string]position `json:"positions,omitempty"`
	Relations     json.RawMessage     `json:"relations"`
}

type dataModelEntity struct {
	ID          string            `json:"id"`
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Cardinality string            `json:"cardinality,omitempty"`
	Fields      *[]dataModelField `json:"fields"`
}

type dataModelField struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Unique  bool     `json:"unique,omitempty"`
	Options []string `json:"options,omitempty"`
}

type position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func featurePinsFromConfig(raw json.RawMessage) (map[string]string, error) {
	if !validJSONObject(raw) {
		return nil, websiteapi.ErrInvalid
	}
	page, err := snapshotPageFromConfig(raw)
	if err != nil {
		return nil, err
	}
	pins := make(map[string]string)
	for _, placement := range page.Placements {
		if placement.Build == nil || strings.TrimSpace(placement.Build.FeatureKey) != placement.Build.FeatureKey ||
			!identifierPattern.MatchString(placement.Build.FeatureKey) || strings.TrimSpace(placement.Build.Version) != placement.Build.Version ||
			!featureVersionPattern.MatchString(placement.Build.Version) {
			return nil, websiteapi.ErrInvalid
		}
		if current, exists := pins[placement.Build.FeatureKey]; exists && current != placement.Build.Version {
			return nil, websiteapi.ErrInvalid
		}
		pins[placement.Build.FeatureKey] = placement.Build.Version
	}
	return pins, nil
}

func validateDataModel(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var contract dataModelContract
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return websiteapi.ErrInvalid
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) || contract.FormatVersion != 1 || contract.Entities == nil {
		return websiteapi.ErrInvalid
	}
	if nonEmptyLegacyRelations(contract.Relations) {
		return websiteapi.ErrInvalid
	}
	entities := *contract.Entities
	ids := make(map[string]struct{}, len(entities))
	entityIDs := make(map[string]struct{}, len(entities))
	entityKeys := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		if !canonicalUUID(entity.ID) || !dataModelKeyPattern.MatchString(entity.Key) || !validDataModelName(entity.Name) || entity.Fields == nil ||
			(entity.Cardinality != "many" && entity.Cardinality != "singleton") {
			return websiteapi.ErrInvalid
		}
		if _, duplicate := ids[entity.ID]; duplicate {
			return websiteapi.ErrInvalid
		}
		ids[entity.ID] = struct{}{}
		entityIDs[entity.ID] = struct{}{}
		if _, duplicate := entityKeys[entity.Key]; duplicate {
			return websiteapi.ErrInvalid
		}
		entityKeys[entity.Key] = struct{}{}
		fieldKeys := make(map[string]struct{}, len(*entity.Fields))
		for _, field := range *entity.Fields {
			if !canonicalUUID(field.ID) || field.Key == "id" || !dataModelKeyPattern.MatchString(field.Key) || !validDataModelName(field.Name) || !validDataModelFieldType(field.Type) {
				return websiteapi.ErrInvalid
			}
			if _, duplicate := ids[field.ID]; duplicate {
				return websiteapi.ErrInvalid
			}
			ids[field.ID] = struct{}{}
			if _, duplicate := fieldKeys[field.Key]; duplicate {
				return websiteapi.ErrInvalid
			}
			fieldKeys[field.Key] = struct{}{}
			if field.Unique && (field.Type == "longText" || field.Type == "media" || field.Type == "boolean") {
				return websiteapi.ErrInvalid
			}
			if field.Type == "enum" {
				if len(field.Options) == 0 {
					return websiteapi.ErrInvalid
				}
				options := make(map[string]struct{}, len(field.Options))
				for _, option := range field.Options {
					if strings.TrimSpace(option) == "" {
						return websiteapi.ErrInvalid
					}
					if _, duplicate := options[option]; duplicate {
						return websiteapi.ErrInvalid
					}
					options[option] = struct{}{}
				}
			} else if len(field.Options) != 0 {
				return websiteapi.ErrInvalid
			}
		}
	}
	for entityID, point := range contract.Positions {
		if _, exists := entityIDs[entityID]; !exists || math.IsNaN(point.X) || math.IsInf(point.X, 0) || math.IsNaN(point.Y) || math.IsInf(point.Y, 0) ||
			point.X < 0 || point.Y < 0 || point.X > 100000 || point.Y > 100000 {
			return websiteapi.ErrInvalid
		}
	}
	return nil
}

func nonEmptyLegacyRelations(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	switch trimmed[0] {
	case '[':
		var values []json.RawMessage
		return json.Unmarshal(raw, &values) != nil || len(values) != 0
	default:
		return true
	}
}

func canonicalUUID(value string) bool {
	return snapshotIDPattern.MatchString(value) && value == strings.ToLower(value)
}

func validDataModelName(value string) bool {
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

func validDataModelFieldType(value string) bool {
	switch value {
	case "text", "longText", "integer", "number", "boolean", "date", "dateTime", "url", "email", "enum", "media":
		return true
	default:
		return false
	}
}

func sameFeaturePins(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for featureKey, version := range left {
		if right[featureKey] != version {
			return false
		}
	}
	return true
}

func validSnapshotName(name string) bool {
	if !utf8.ValidString(name) || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 120 {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if !utf8.Valid(raw) || len(trimmed) < 2 || trimmed[0] != '{' {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeUniqueJSONValue(decoder, 0); err != nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func consumeUniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return websiteapi.ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return websiteapi.ErrInvalid
			}
			if _, duplicate := seen[key]; duplicate {
				return websiteapi.ErrInvalid
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return websiteapi.ErrInvalid
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return websiteapi.ErrInvalid
		}
	default:
		return websiteapi.ErrInvalid
	}
	return nil
}
