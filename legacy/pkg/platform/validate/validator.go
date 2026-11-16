// Package validate contains JSON Schema validation and public-data projection.
package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// FieldError is a safe, UI-friendly validation error.
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SchemaError describes a malformed or unsupported JSON Schema.
type SchemaError struct {
	Message string
}

func (e *SchemaError) Error() string { return e.Message }

func newCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	c.AssertFormat = true
	c.Formats["media"] = func(v interface{}) bool {
		s, ok := v.(string)
		if !ok {
			return true // type is checked by the schema itself
		}
		if s == "" {
			return true // optional empty values are allowed; required fields are checked separately
		}
		_, err := uuid.Parse(s)
		return err == nil
	}
	// User schemas must be self-contained: remote references are not resolved.
	c.LoadURL = func(string) (io.ReadCloser, error) {
		return nil, errors.New("remote schema references are not allowed")
	}
	return c
}

// Compile validates that raw is a syntactically valid JSON Schema and returns
// a compiled schema.
func Compile(raw json.RawMessage) (*jsonschema.Schema, error) {
	var root map[string]any
	if err := decodeJSON(raw, &root); err != nil || root == nil {
		return nil, &SchemaError{Message: "schema must be a JSON object"}
	}
	if err := validateExtensions(root); err != nil {
		return nil, &SchemaError{Message: err.Error()}
	}

	c := newCompiler()
	if err := c.AddResource("inline://schema.json", bytes.NewReader(raw)); err != nil {
		return nil, &SchemaError{Message: err.Error()}
	}
	sch, err := c.Compile("inline://schema.json")
	if err != nil {
		var ce *jsonschema.SchemaError
		if errors.As(err, &ce) {
			return nil, &SchemaError{Message: fmt.Sprintf("invalid JSON Schema: %v", ce.Err)}
		}
		return nil, &SchemaError{Message: fmt.Sprintf("invalid JSON Schema: %v", err)}
	}
	return sch, nil
}

// Validate checks data against raw and returns safe field errors.
func Validate(raw json.RawMessage, data json.RawMessage) ([]FieldError, error) {
	sch, err := Compile(raw)
	if err != nil {
		return nil, err
	}

	var value any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode data: %w", err)
	}
	if err := sch.Validate(value); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return flattenErrors(ve), nil
		}
		return nil, err
	}
	return nil, nil
}

func flattenErrors(ve *jsonschema.ValidationError) []FieldError {
	seen := make(map[string]bool)
	out := make([]FieldError, 0, 8)
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, cause := range e.Causes {
				walk(cause)
			}
			return
		}
		path := strings.TrimPrefix(e.InstanceLocation, "/")
		path = strings.ReplaceAll(path, "/", ".")
		code := e.KeywordLocation
		if i := strings.LastIndex(code, "/"); i >= 0 && i < len(code)-1 {
			code = code[i+1:]
		}
		if code == "" {
			code = "validation"
		}
		key := path + "|" + code
		if seen[key] {
			return
		}
		seen[key] = true
		msg := e.Message
		if msg == "" {
			msg = "value does not match the schema"
		}
		out = append(out, FieldError{Path: path, Code: code, Message: msg})
	}
	walk(ve)
	if len(out) == 0 {
		out = append(out, FieldError{Path: "", Code: "validation", Message: "value does not match the schema"})
	}
	return out
}

// StripPrivate removes object properties marked with `x-private: true` or
// `x-public: false`, recursively following properties and arrays. Local $refs
// at the root of the supplied schema are supported.
func StripPrivate(value any, schema map[string]any) any {
	return stripPrivate(value, schema, schema, 0)
}

func stripPrivate(value any, schema, root map[string]any, depth int) any {
	if value == nil || schema == nil || depth > 32 {
		return value
	}
	if ref, ok := schema["$ref"].(string); ok {
		if target, ok := resolveLocalRef(root, ref); ok {
			schema = target
		}
	}

	switch v := value.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		out := make(map[string]any, len(v))
		for key, val := range v {
			var propertySchema map[string]any
			if props != nil {
				propertySchema, _ = props[key].(map[string]any)
			}
			if propertySchema != nil {
				if private, _ := propertySchema["x-private"].(bool); private {
					continue
				}
				if public, ok := propertySchema["x-public"].(bool); ok && !public {
					continue
				}
			}
			out[key] = stripPrivate(val, propertySchema, root, depth+1)
		}
		return out
	case []any:
		items, _ := schema["items"].(map[string]any)
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, stripPrivate(item, items, root, depth+1))
		}
		return out
	default:
		return value
	}
}

// StripPrivateRaw is a convenience wrapper around StripPrivate.
func StripPrivateRaw(data json.RawMessage, schema json.RawMessage) (json.RawMessage, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var s map[string]any
	if err := decodeJSON(schema, &s); err != nil {
		return nil, err
	}
	projected := StripPrivate(value, s)
	return json.Marshal(projected)
}

func resolveLocalRef(root map[string]any, ref string) (map[string]any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var cur any = root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	m, ok := cur.(map[string]any)
	return m, ok
}

// validateExtensions checks the small, deliberately supported set of schema
// extensions. Unknown keywords are ignored, as JSON Schema allows.
func validateExtensions(node map[string]any) error {
	if node == nil {
		return nil
	}
	if v, ok := node["x-private"]; ok {
		if _, ok := v.(bool); !ok {
			return errors.New("x-private must be a boolean")
		}
	}
	if v, ok := node["x-public"]; ok {
		if _, ok := v.(bool); !ok {
			return errors.New("x-public must be a boolean")
		}
	}
	if v, ok := node["x-ui"]; ok {
		if _, ok := v.(map[string]any); !ok {
			return errors.New("x-ui must be an object")
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for name, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if err := validateExtensions(child); err != nil {
				return fmt.Errorf("properties.%s: %w", name, err)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		if err := validateExtensions(items); err != nil {
			return fmt.Errorf("items: %w", err)
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if list, ok := node[keyword].([]any); ok {
			for i, raw := range list {
				child, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if err := validateExtensions(child); err != nil {
					return fmt.Errorf("%s[%d]: %w", keyword, i, err)
				}
			}
		}
	}
	return nil
}

func decodeJSON(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(dst)
}
