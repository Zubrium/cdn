package validate

import (
	"encoding/json"
	"testing"
)

func TestEditorialOptionalNestedFieldsKeepValueConstraints(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["title","nested","items"],"properties":{"title":{"type":"string","minLength":2},"nested":{"type":"object","required":["email"],"properties":{"email":{"type":"string","format":"email"}}},"items":{"type":"array","items":{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}}}}}}`)
	for _, data := range []string{`{}`, `{"nested":{}}`, `{"items":[{}]}`} {
		issues, err := ValidateEditorial(schema, json.RawMessage(data))
		if err != nil || len(issues) != 0 {
			t.Fatalf("optional %s: %v %v", data, issues, err)
		}
	}
	for _, data := range []string{`{"title":""}`, `{"nested":{"email":"wrong"}}`, `{"items":[{"count":0}]}`} {
		issues, err := ValidateEditorial(schema, json.RawMessage(data))
		if err != nil || len(issues) == 0 {
			t.Fatalf("invalid supplied %s: %v %v", data, issues, err)
		}
	}
	issues, err := Validate(schema, json.RawMessage(`{}`))
	if err != nil || len(issues) == 0 {
		t.Fatalf("strict submission validation changed: %v %v", issues, err)
	}
}

func TestEditorialSchemaDoesNotModifyDataAnnotations(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","required":["x"],"default":{"required":["keep"]},"properties":{"x":{"type":"object","required":["y"]}}}`)
	optional, err := EditorialSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(optional, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema["required"]; ok {
		t.Fatal("root still required")
	}
	if schema["default"].(map[string]any)["required"] == nil {
		t.Fatal("default data changed")
	}
	if string(raw) != `{"type":"object","required":["x"],"default":{"required":["keep"]},"properties":{"x":{"type":"object","required":["y"]}}}` {
		t.Fatal("mutated manifest")
	}
}
