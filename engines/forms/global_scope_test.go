package forms_test

import (
	"reflect"
	"testing"

	forms "github.com/Zubrium/cdn/engines/forms"
	schemapkg "github.com/Zubrium/cdn/engines/forms/features/schemas/pkg"
	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
)

// TestFormsGlobalContractHasNoSnapshotScope fixes the external and storage
// contract of the global Forms model before any behavior is refactored.
func TestFormsGlobalContractHasNoSnapshotScope(t *testing.T) {
	types := map[string]reflect.Type{
		"SaveSchemaRequest":       reflect.TypeOf(formapi.SaveSchemaRequest{}),
		"PublicFormBinding":       reflect.TypeOf(formapi.PublicFormBinding{}),
		"ListSubmissionsRequest":  reflect.TypeOf(formapi.ListSubmissionsRequest{}),
		"SetSubmissionStatus":     reflect.TypeOf(formapi.SetSubmissionStatusRequest{}),
		"DeleteSubmissionRequest": reflect.TypeOf(formapi.DeleteSubmissionRequest{}),
	}
	for name, typ := range types {
		if _, found := typ.FieldByName("SnapshotID"); found {
			t.Errorf("%s still exposes SnapshotID", name)
		}
		for i := 0; i < typ.NumField(); i++ {
			if tag := string(typ.Field(i).Tag); containsSnapshotTag(tag) {
				t.Errorf("%s still declares snapshot_id in JSON tag %q", name, typ.Field(i).Tag)
			}
		}
	}
	if schemapkg.PayloadVersion != 2 {
		t.Errorf("schema payload version = %d, want 2", schemapkg.PayloadVersion)
	}
	descriptor := forms.ModuleDataDescriptor()
	for _, namespace := range descriptor.Namespaces {
		if len(namespace.ScopeTypes) != 1 || namespace.ScopeTypes[0] != "global" {
			t.Errorf("namespace %q scope types = %v, want [global]", namespace.Name, namespace.ScopeTypes)
		}
	}
}

func containsSnapshotTag(tag string) bool {
	return len(tag) >= len("snapshot_id") && (indexOf(tag, "snapshot_id") >= 0)
}

func indexOf(value, substring string) int {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return i
		}
	}
	return -1
}
