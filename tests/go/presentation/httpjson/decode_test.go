package httpjson_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/presentation/httpjson"
)

type requestBody struct {
	Name string `json:"name"`
}

func TestDecodeRequestAcceptsStrictJSONObject(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	var decoded requestBody

	if err := httpjson.DecodeRequest(httptest.NewRecorder(), request, 128, &decoded); err != nil {
		t.Fatalf("DecodeRequest() error = %v", err)
	}
	if decoded.Name != "ok" {
		t.Fatalf("decoded name = %q, want ok", decoded.Name)
	}
}

func TestDecodeRequestRejectsMalformedOrAmbiguousInputs(t *testing.T) {
	tests := []struct {
		name        string
		contentType []string
		body        string
	}{
		{name: "missing content type", body: `{"name":"ok"}`},
		{name: "duplicate content type", contentType: []string{"application/json", "application/json"}, body: `{"name":"ok"}`},
		{name: "wrong content type", contentType: []string{"text/plain"}, body: `{"name":"ok"}`},
		{name: "content type outer whitespace", contentType: []string{" application/json "}, body: `{"name":"ok"}`},
		{name: "duplicate key", contentType: []string{"application/json"}, body: `{"name":"first","name":"second"}`},
		{name: "nested duplicate key", contentType: []string{"application/json"}, body: `{"name":"ok","extra":{"x":1,"x":2}}`},
		{name: "unknown field", contentType: []string{"application/json"}, body: `{"name":"ok","unexpected":true}`},
		{name: "trailing value", contentType: []string{"application/json"}, body: `{"name":"ok"} {}`},
		{name: "null document", contentType: []string{"application/json"}, body: `null`},
		{name: "array document", contentType: []string{"application/json"}, body: `[]`},
		{name: "scalar document", contentType: []string{"application/json"}, body: `"ok"`},
		{name: "empty body", contentType: []string{"application/json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			for _, value := range test.contentType {
				request.Header.Add("Content-Type", value)
			}
			var decoded requestBody
			if err := httpjson.DecodeRequest(httptest.NewRecorder(), request, 128, &decoded); !errors.Is(err, httpjson.ErrInvalid) {
				t.Fatalf("DecodeRequest() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDecodeRequestRejectsInvalidUTF8AndOversizedBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "invalid UTF-8", body: "{\"name\":\"\xff\"}", want: httpjson.ErrInvalid},
		{name: "oversized", body: `{"name":"too long"}`, want: httpjson.ErrTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			var decoded requestBody
			err := httpjson.DecodeRequest(httptest.NewRecorder(), request, 12, &decoded)
			if !errors.Is(err, test.want) {
				t.Fatalf("DecodeRequest() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecodeRequestRejectsNestedDuplicateKeysWhenDecodingMap(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"extra":{"x":1,"x":2}}`))
	request.Header.Set("Content-Type", "application/json")
	var decoded map[string]any
	if err := httpjson.DecodeRequest(httptest.NewRecorder(), request, 128, &decoded); !errors.Is(err, httpjson.ErrInvalid) {
		t.Fatalf("DecodeRequest() error = %v, want ErrInvalid", err)
	}
}

func TestValidJSONRejectsNestingBeyondLimit(t *testing.T) {
	withinLimit := strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64)
	if !httpjson.ValidJSON([]byte(withinLimit)) {
		t.Fatal("ValidJSON() rejected the configured maximum nesting depth")
	}
	deep := strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)
	if httpjson.ValidJSON([]byte(deep)) {
		t.Fatal("ValidJSON() accepted nesting beyond the configured maximum")
	}
}

func TestValidObjectAndArrayRequireUniqueBoundedJSON(t *testing.T) {
	if !httpjson.ValidObject([]byte(` {"name":"ok"} `), 32) {
		t.Fatal("ValidObject() rejected a valid object")
	}
	if httpjson.ValidObject([]byte(`{"name":1,"name":2}`), 32) {
		t.Fatal("ValidObject() accepted duplicate keys")
	}
	if httpjson.ValidObject([]byte(`[1,2]`), 32) {
		t.Fatal("ValidObject() accepted an array")
	}
	if !httpjson.ValidArray([]byte(`[1,{"name":"ok"}]`), 32) {
		t.Fatal("ValidArray() rejected a valid array")
	}
	if httpjson.ValidArray([]byte(`{"name":"ok"}`), 32) {
		t.Fatal("ValidArray() accepted an object")
	}
	if httpjson.ValidObject([]byte(`{"name":"far too long"}`), 8) {
		t.Fatal("ValidObject() accepted an oversized value")
	}
}
