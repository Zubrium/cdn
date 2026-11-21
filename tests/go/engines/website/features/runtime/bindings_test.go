package runtime_test

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func TestBindingsAcceptOnlyDirectCompatiblePorts(t *testing.T) {
	manifest, err := runtime.ValidateManifest(mustValidManifest(t), featureFiles())
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]runtime.Binding{
		"items": {Port: "items", EntityKey: "product", Field: "title", OutputType: "text"},
	}
	if err := runtime.ValidateBindings(manifest, valid); err != nil {
		t.Fatalf("valid direct binding rejected: %v", err)
	}

	cases := map[string]map[string]runtime.Binding{
		"unknown port":      {"missing": {Port: "missing", EntityKey: "product", Field: "title", OutputType: "text"}},
		"relation ids":      {"items": {Port: "items", EntityKey: "product", Field: "title", OutputType: "text", RelationIDs: []string{"other"}}},
		"record selection":  {"items": {Port: "items", EntityKey: "product", Field: "title", OutputType: "text", Selection: []string{"record-1"}}},
		"incompatible type": {"items": {Port: "items", EntityKey: "product", Field: "title", OutputType: "number"}},
		"undeclared field":  {"items": {Port: "items", EntityKey: "product", Field: "secret_hint", OutputType: "text"}},
		"missing field":     {"items": {Port: "items", EntityKey: "product", OutputType: "text"}},
		"secret entity":     {"items": {Port: "items", EntityKey: "api_key", Field: "title", OutputType: "text"}},
		"port mismatch":     {"items": {Port: "other", EntityKey: "product", Field: "title", OutputType: "text"}},
	}
	for name, bindings := range cases {
		t.Run(name, func(t *testing.T) {
			err := runtime.ValidateBindings(manifest, bindings)
			if err == nil {
				t.Fatal("invalid binding was accepted")
			}
			if name == "secret entity" && !errors.Is(err, runtime.ErrSecret) {
				t.Fatalf("err=%v, want ErrSecret", err)
			}
		})
	}
}

func web006BindingsRejectRelationAndInvalidPorts(t *testing.T) {
	TestBindingsAcceptOnlyDirectCompatiblePorts(t)
}
