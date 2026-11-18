package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"
)

var fieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// FieldDefinition is the small, explicit field contract used by public Forms.
// The admin contract continues to carry fields as JSON objects so it can
// evolve without accepting a general-purpose executable schema language.
type FieldDefinition struct {
	Type                string   `json:"type"`
	Required            bool     `json:"required"`
	MinLength           *int     `json:"min_length,omitempty"`
	MaxLength           *int     `json:"max_length,omitempty"`
	Pattern             string   `json:"pattern,omitempty"`
	Minimum             *float64 `json:"minimum,omitempty"`
	Maximum             *float64 `json:"maximum,omitempty"`
	Options             []string `json:"options,omitempty"`
	MaxFiles            int      `json:"max_files,omitempty"`
	MaxFileBytes        int64    `json:"max_file_bytes,omitempty"`
	AllowedContentTypes []string `json:"allowed_content_types,omitempty"`
}

// ParseFieldDefinitions validates the persisted JSON shape and every
// field-specific constraint before it becomes active in a schema.
func ParseFieldDefinitions(fields map[string]any) (map[string]FieldDefinition, error) {
	if len(fields) == 0 || len(fields) > 128 {
		return nil, errors.New("form schema must define between 1 and 128 fields")
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, errors.New("form field definitions are not JSON values")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil || len(raw) != len(fields) {
		return nil, errors.New("invalid form field definitions")
	}
	result := make(map[string]FieldDefinition, len(raw))
	for name, value := range raw {
		if !fieldNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid form field name %q", name)
		}
		var shape map[string]json.RawMessage
		if err := json.Unmarshal(value, &shape); err != nil || shape == nil {
			return nil, fmt.Errorf("field %q must be an object", name)
		}
		if _, ok := shape["type"]; !ok {
			return nil, fmt.Errorf("field %q must declare type", name)
		}
		if _, ok := shape["required"]; !ok {
			return nil, fmt.Errorf("field %q must declare required", name)
		}
		decoder := json.NewDecoder(strings.NewReader(string(value)))
		decoder.DisallowUnknownFields()
		var definition FieldDefinition
		if err := decoder.Decode(&definition); err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		result[name] = definition
	}
	return result, nil
}

func (definition FieldDefinition) Validate() error {
	switch definition.Type {
	case "text", "email":
		if definition.MinLength != nil && (*definition.MinLength < 0 || *definition.MinLength > 1<<20) {
			return errors.New("min_length is outside the supported range")
		}
		if definition.MaxLength != nil && (*definition.MaxLength < 1 || *definition.MaxLength > 1<<20) {
			return errors.New("max_length is outside the supported range")
		}
		if definition.MinLength != nil && definition.MaxLength != nil && *definition.MinLength > *definition.MaxLength {
			return errors.New("min_length exceeds max_length")
		}
		if len(definition.Pattern) > 256 || (definition.Pattern != "" && !validRE2(definition.Pattern)) {
			return errors.New("pattern must be a valid Go RE2 expression of at most 256 bytes")
		}
		if definition.Minimum != nil || definition.Maximum != nil || len(definition.Options) != 0 || definition.MaxFiles != 0 ||
			definition.MaxFileBytes != 0 || len(definition.AllowedContentTypes) != 0 {
			return errors.New("field contains constraints that do not apply to text or email")
		}
	case "number":
		if definition.Minimum != nil && definition.Maximum != nil && *definition.Minimum > *definition.Maximum {
			return errors.New("minimum exceeds maximum")
		}
		if definition.MinLength != nil || definition.MaxLength != nil || definition.Pattern != "" || len(definition.Options) != 0 ||
			definition.MaxFiles != 0 || definition.MaxFileBytes != 0 || len(definition.AllowedContentTypes) != 0 {
			return errors.New("field contains constraints that do not apply to number")
		}
	case "boolean":
		if definition.MinLength != nil || definition.MaxLength != nil || definition.Pattern != "" || definition.Minimum != nil ||
			definition.Maximum != nil || len(definition.Options) != 0 || definition.MaxFiles != 0 || definition.MaxFileBytes != 0 || len(definition.AllowedContentTypes) != 0 {
			return errors.New("field contains constraints that do not apply to boolean")
		}
	case "select":
		if len(definition.Options) < 1 || len(definition.Options) > 128 {
			return errors.New("select requires between 1 and 128 options")
		}
		seen := make(map[string]struct{}, len(definition.Options))
		for _, option := range definition.Options {
			if option == "" || len(option) > 256 || !utf8.ValidString(option) || strings.TrimSpace(option) != option {
				return errors.New("select options must be trimmed non-empty strings of at most 256 bytes")
			}
			if _, exists := seen[option]; exists {
				return errors.New("select options must be unique")
			}
			seen[option] = struct{}{}
		}
		if definition.MinLength != nil || definition.MaxLength != nil || definition.Pattern != "" || definition.Minimum != nil ||
			definition.Maximum != nil || definition.MaxFiles != 0 || definition.MaxFileBytes != 0 || len(definition.AllowedContentTypes) != 0 {
			return errors.New("field contains constraints that do not apply to select")
		}
	case "file":
		if definition.MaxFiles < 1 || definition.MaxFiles > 20 || definition.MaxFileBytes < 1 || len(definition.AllowedContentTypes) == 0 || len(definition.AllowedContentTypes) > 32 {
			return errors.New("file requires bounded max_files, max_file_bytes, and allowed_content_types")
		}
		seen := make(map[string]struct{}, len(definition.AllowedContentTypes))
		for _, raw := range definition.AllowedContentTypes {
			mediaType, params, err := mime.ParseMediaType(raw)
			if err != nil || len(params) != 0 || strings.Contains(mediaType, "*") || strings.TrimSpace(raw) != raw || activeMediaType(mediaType) {
				return errors.New("file content types must be exact, non-active media types")
			}
			mediaType = strings.ToLower(mediaType)
			if _, exists := seen[mediaType]; exists {
				return errors.New("file content types must be unique")
			}
			seen[mediaType] = struct{}{}
		}
		if definition.MinLength != nil || definition.MaxLength != nil || definition.Pattern != "" || definition.Minimum != nil ||
			definition.Maximum != nil || len(definition.Options) != 0 {
			return errors.New("field contains constraints that do not apply to file")
		}
	default:
		return errors.New("type must be text, email, number, boolean, select, or file")
	}
	return nil
}

func validRE2(pattern string) bool {
	_, err := regexp.Compile(pattern)
	return err == nil
}

func activeMediaType(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "application/ecmascript", "application/javascript", "application/x-httpd-php", "application/xhtml+xml", "application/xml",
		"image/svg+xml", "text/css", "text/html", "text/javascript", "text/xml":
		return true
	default:
		return false
	}
}

func IsActiveMediaType(mediaType string) bool { return activeMediaType(mediaType) }
