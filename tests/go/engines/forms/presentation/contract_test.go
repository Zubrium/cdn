package presentation_test

import (
	"testing"

	"github.com/Zubrium/cdn/engines/forms"
	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
)

func TestFormsDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	component := (formapi.Descriptor{}).ComponentDescriptor()
	if component.ID != "engine.forms" || len(component.AccessGroups) != 2 {
		t.Fatalf("invalid neutral Forms profile metadata: %#v", component)
	}
	descriptor := forms.RecordDescriptor()
	if descriptor.OwnerID != "forms" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Forms module-data descriptor: %#v", descriptor)
	}
}
