package presentation

import (
	"testing"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/engines/forms"
)

func TestFormsDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	operations := (Descriptor{}).Operations()
	if len(operations) == 0 {
		t.Fatal("Forms descriptor must expose its typed operation inventory")
	}
	for _, operation := range operations {
		if operation.Name == "" || operation.ResourceType == "" || !access.ValidAction(operation.Action) {
			t.Fatalf("invalid Forms operation: %#v", operation)
		}
	}
	descriptor := forms.ModuleDataDescriptor()
	if descriptor.ModuleID != "forms" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Forms module-data descriptor: %#v", descriptor)
	}
}
