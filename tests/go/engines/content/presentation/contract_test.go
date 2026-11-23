package presentation_test

import (
	"testing"

	"github.com/Zubrium/cdn/engines/content"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
)

func TestContentDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	component := (contentapi.Descriptor{}).ComponentDescriptor()
	if component.ID != "engine.content" || len(component.AccessGroups) != 1 || component.AccessGroups[0] != "content" {
		t.Fatalf("invalid neutral Content profile metadata: %#v", component)
	}
	descriptor := content.RecordDescriptor()
	if descriptor.OwnerID != "content" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Content module-data descriptor: %#v", descriptor)
	}
	fields := descriptor.Namespaces[0].Fields
	for name, wantType := range map[string]string{"record_type": "string", "content_revision": "number", "locale": "string", "page_snapshot_id": "string"} {
		if field, ok := fields[name]; !ok || string(field.Type) != wantType {
			t.Errorf("Content module-data descriptor field %q = %#v, want type %q", name, field, wantType)
		}
	}
}
