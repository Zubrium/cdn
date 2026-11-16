package sitebuilder

import (
	"encoding/json"
	"fmt"

	"websiteengine/features/media"
)

// runtimeInputContractFromDataInputs creates the runtime validation shape
// from the typed feature contract. It does not add CMS labels, required fields,
// or editor settings to the manifest.
func runtimeInputContractFromDataInputs(ports []DataInputPort, form *FormContract) (RuntimeInputContract, error) {
	mode := "single"
	if form != nil {
		mode = "form"
	} else if len(ports) == 1 && ports[0].Kind == "collection" {
		mode = "collection"
	}

	properties := make(map[string]any, len(ports))
	var onlyCollection *DataInputPort
	for i := range ports {
		port := &ports[i]
		if port.Kind == "collection" && onlyCollection == nil {
			onlyCollection = port
		}
		var value any
		switch port.Kind {
		case "scalar":
			value = dataInputTypeSchema(port.Type, port.Profile, port.Values)
		case "record":
			value = dataInputFieldsSchema(port.Fields)
		case "collection":
			value = map[string]any{"type": "array", "items": dataInputFieldsSchema(port.Fields)}
		default:
			return RuntimeInputContract{}, fmt.Errorf("data input %q has unsupported kind %q", port.Key, port.Kind)
		}
		properties[port.Key] = value
	}
	if mode == "collection" && onlyCollection != nil {
		properties = schemaObjectProperties(dataInputFieldsSchema(onlyCollection.Fields))
	}
	encoded, err := json.Marshal(map[string]any{"type": "object", "properties": properties, "additionalProperties": false})
	if err != nil {
		return RuntimeInputContract{}, fmt.Errorf("build runtime input schema: %w", err)
	}
	return RuntimeInputContract{Mode: mode, Schema: encoded}, nil
}

func dataInputFieldsSchema(fields []DataInputField) map[string]any {
	root := objectSchema()
	for _, field := range fields {
		segments, err := parseJSONPointer(field.Path)
		if err != nil || len(segments) == 0 {
			continue
		}
		leaf := dataInputTypeSchema(field.Type, field.Profile, field.Values)
		if segments[len(segments)-1] == "id" && hasSegment(segments, "*") {
			leaf["x-engine-record-id"] = true
		}
		dataInputSchemaInsert(root, segments, leaf)
	}
	return root
}

func hasSegment(segments []string, value string) bool {
	for _, segment := range segments {
		if segment == value {
			return true
		}
	}
	return false
}

func dataInputTypeSchema(fieldType string, profile *media.FieldProfile, values []string) map[string]any {
	schema := map[string]any{}
	switch fieldType {
	case "integer":
		schema["type"] = "integer"
	case "number":
		schema["type"] = "number"
	case "boolean":
		schema["type"] = "boolean"
	case "date":
		schema["type"], schema["format"] = "string", "date"
	case "dateTime":
		schema["type"], schema["format"] = "string", "date-time"
	case "url":
		schema["type"], schema["format"] = "string", "uri"
	case "email":
		schema["type"], schema["format"] = "string", "email"
	default:
		schema["type"] = "string"
	}
	if fieldType == "longText" {
		schema["x-ui"] = map[string]any{"widget": "textarea"}
	}
	if fieldType == "media" {
		schema["format"] = "media"
		if profile != nil {
			if raw, err := json.Marshal(profile); err == nil {
				var decoded any
				if json.Unmarshal(raw, &decoded) == nil {
					schema["x-media"] = decoded
				}
			}
		}
	}
	if fieldType == "enum" {
		schema["enum"] = values
	}
	return schema
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func schemaObjectProperties(value map[string]any) map[string]any {
	properties, _ := value["properties"].(map[string]any)
	if properties == nil {
		return map[string]any{}
	}
	return properties
}

func dataInputSchemaInsert(node map[string]any, segments []string, leaf map[string]any) {
	if len(segments) == 0 {
		return
	}
	if segments[0] == "*" {
		items, _ := node["items"].(map[string]any)
		dataInputSchemaInsert(items, segments[1:], leaf)
		return
	}
	properties := schemaObjectProperties(node)
	if len(segments) == 1 {
		properties[segments[0]] = leaf
		return
	}
	child, ok := properties[segments[0]].(map[string]any)
	if !ok {
		if segments[1] == "*" {
			child = map[string]any{"type": "array", "items": objectSchema()}
			properties[segments[0]] = child
			items, _ := child["items"].(map[string]any)
			dataInputSchemaInsert(items, segments[2:], leaf)
			return
		}
		child = objectSchema()
		properties[segments[0]] = child
	}
	dataInputSchemaInsert(child, segments[1:], leaf)
}
