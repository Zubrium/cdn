package sitebuilder

import (
	"errors"
	"fmt"
	"strings"

	"websiteengine/features/media"
)

// DataInputPort is a feature's typed data boundary. It describes the shape the
// runtime component consumes; it intentionally contains no editor labels or
// record-form settings.
type DataInputPort struct {
	Key     string              `json:"key"`
	Kind    string              `json:"kind"`
	Type    string              `json:"type,omitempty"`
	Values  []string            `json:"values,omitempty"`
	Profile *media.FieldProfile `json:"profile,omitempty"`
	Fields  []DataInputField    `json:"fields,omitempty"`
}

type DataInputField struct {
	Path    string              `json:"path"`
	Type    string              `json:"type"`
	Values  []string            `json:"values,omitempty"`
	Profile *media.FieldProfile `json:"profile,omitempty"`
}

func validateDataInputPorts(ports []DataInputPort) error {
	keys := make(map[string]bool, len(ports))
	for _, port := range ports {
		if !argumentPattern.MatchString(port.Key) || keys[port.Key] {
			return errors.New("input keys must be valid and unique")
		}
		keys[port.Key] = true
		switch port.Kind {
		case "scalar":
			if !dataModelFieldTypes[port.Type] {
				return fmt.Errorf("input %q scalar type is missing or unsupported", port.Key)
			}
			if err := validateDataInputMediaProfile(port.Type, port.Profile, fmt.Sprintf("input %q", port.Key)); err != nil {
				return err
			}
			if err := validateDataInputEnum(port.Type, port.Values, fmt.Sprintf("input %q", port.Key)); err != nil {
				return err
			}
			if port.Fields != nil {
				return fmt.Errorf("input %q scalar input must not declare fields", port.Key)
			}
			continue
		case "record", "collection":
			if port.Type != "" {
				return fmt.Errorf("input %q %s input must declare field types, not a scalar type", port.Key, port.Kind)
			}
			if port.Values != nil {
				return fmt.Errorf("input %q %s input values must be declared on enum fields", port.Key, port.Kind)
			}
			if port.Profile != nil {
				return fmt.Errorf("input %q %s input cannot declare a media profile; attach it to a typed media field", port.Key, port.Kind)
			}
		default:
			return fmt.Errorf("input %q kind must be scalar, record, or collection", port.Key)
		}
		if len(port.Fields) == 0 {
			return fmt.Errorf("input %q must declare at least one typed field", port.Key)
		}
		paths := make(map[string][]string, len(port.Fields))
		for _, field := range port.Fields {
			segments, err := parseJSONPointer(field.Path)
			if err != nil || len(segments) == 0 {
				return fmt.Errorf("input %q contains an invalid field path", port.Key)
			}
			for index, segment := range segments {
				if segment == "*" {
					if index == 0 || index == len(segments)-1 || segments[index-1] == "*" {
						return fmt.Errorf("input %q field path %q has an invalid array wildcard", port.Key, field.Path)
					}
					continue
				}
				if !argumentPattern.MatchString(segment) {
					return fmt.Errorf("input %q field path %q contains an invalid property name", port.Key, field.Path)
				}
			}
			pathKey := pointerKey(segments)
			if _, exists := paths[pathKey]; exists {
				return fmt.Errorf("input %q contains duplicate field path %q", port.Key, field.Path)
			}
			for previousKey, previousSegments := range paths {
				if dataInputPathsOverlap(previousSegments, segments) {
					return fmt.Errorf("input %q contains overlapping field paths %q and %q", port.Key, previousKey, field.Path)
				}
			}
			paths[pathKey] = segments
			if !dataModelFieldTypes[field.Type] {
				return fmt.Errorf("input %q field %q has unsupported type %q", port.Key, field.Path, field.Type)
			}
			if err := validateDataInputMediaProfile(field.Type, field.Profile, fmt.Sprintf("input %q field %q", port.Key, field.Path)); err != nil {
				return err
			}
			if err := validateDataInputEnum(field.Type, field.Values, fmt.Sprintf("input %q field %q", port.Key, field.Path)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDataInputEnum(fieldType string, values []string, owner string) error {
	if fieldType != "enum" {
		if values != nil {
			return fmt.Errorf("%s values are only valid for enum type", owner)
		}
		return nil
	}
	if len(values) == 0 {
		return fmt.Errorf("%s enum values are required", owner)
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" || seen[value] {
			return fmt.Errorf("%s enum values must be non-empty and unique", owner)
		}
		seen[value] = true
	}
	return nil
}

func validateDataInputMediaProfile(fieldType string, profile *media.FieldProfile, owner string) error {
	if profile == nil {
		return nil
	}
	if fieldType != "media" {
		return fmt.Errorf("%s media profile is only valid for media input types", owner)
	}
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("%s media profile: %w", owner, err)
	}
	return nil
}

func dataInputPathsOverlap(left, right []string) bool {
	shorter, longer := left, right
	if len(shorter) > len(longer) {
		shorter, longer = right, left
	}
	for index := range shorter {
		if shorter[index] != longer[index] {
			return false
		}
	}
	return true
}
