package validate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateAndPrivateProjection(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string", "minLength": 1},
			"cover": {"type": "string", "format": "media"},
			"secret": {"type": "string", "x-private": true},
			"meta": {
				"type": "object",
				"properties": {"internal": {"type": "string", "x-public": false}}
			}
		},
		"required": ["title"],
		"additionalProperties": false
	}`)
	if _, err := Compile(schema); err != nil {
		t.Fatalf("compile: %v", err)
	}

	valid := json.RawMessage(`{"title":"Hello","cover":"2f7d60c4-fcbc-4f9b-b6ca-fcdbb50d194f","secret":"s","meta":{"internal":"x"}}`)
	errs, err := Validate(schema, valid)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %#v", errs)
	}

	invalid := json.RawMessage(`{"title":"","cover":"not-a-uuid","secret":"s"}`)
	errs, err = Validate(schema, invalid)
	if err != nil {
		t.Fatalf("validate invalid: %v", err)
	}
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors, got %#v", errs)
	}
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
	}
	if !paths["title"] || !paths["cover"] {
		t.Fatalf("missing expected paths: %#v", errs)
	}

	projected, err := StripPrivateRaw(valid, schema)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(projected, &got); err != nil {
		t.Fatal(err)
	}
	if _, exists := got["secret"]; exists {
		t.Fatalf("private field leaked: %s", projected)
	}
	meta := got["meta"].(map[string]any)
	if _, exists := meta["internal"]; exists {
		t.Fatalf("x-public=false field leaked: %s", projected)
	}
	if got["title"] != "Hello" {
		t.Fatalf("title was lost: %s", projected)
	}
}

func TestCompileRejectsInvalidSchemas(t *testing.T) {
	cases := []string{
		`[]`,
		`{"type":"object","properties":{"x":{"type":"wat"}}}`,
		`{"type":"object","x-private":"yes"}`,
		`{"$ref":"https://example.com/schema.json"}`,
	}
	for _, raw := range cases {
		if _, err := Compile(json.RawMessage(raw)); err == nil {
			t.Errorf("expected schema error for %s", raw)
		} else if strings.Contains(err.Error(), "postgres") {
			t.Errorf("unexpected internal details: %v", err)
		}
	}
}
