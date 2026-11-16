package validate

import "encoding/json"

// EditorialSchema removes presence requirements from schema nodes only. It
// deliberately leaves defaults/examples (which are data) and constraints on
// supplied values intact. The uploaded manifest is never modified.
func EditorialSchema(raw json.RawMessage) (json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var walk func(any)
	walk = func(value any) {
		node, ok := value.(map[string]any)
		if !ok {
			return
		}
		delete(node, "required")
		for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
			if children, ok := node[keyword].(map[string]any); ok {
				for _, child := range children {
					walk(child)
				}
			}
		}
		for _, keyword := range []string{"items", "additionalProperties", "unevaluatedProperties", "contains", "propertyNames", "not", "if", "then", "else"} {
			walk(node[keyword])
		}
		for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
			if children, ok := node[keyword].([]any); ok {
				for _, child := range children {
					walk(child)
				}
			}
		}
	}
	walk(root)
	return json.Marshal(root)
}

func ValidateEditorial(schema, data json.RawMessage) ([]FieldError, error) {
	optional, err := EditorialSchema(schema)
	if err != nil {
		return nil, err
	}
	return Validate(optional, data)
}
