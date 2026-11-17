package presentation

import (
	"testing"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/engines/content"
)

func TestContentDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	operations := (Descriptor{}).Operations()
	if len(operations) == 0 {
		t.Fatal("Content descriptor must expose its typed operation inventory")
	}
	for _, operation := range operations {
		if operation.Name == "" || operation.ResourceType == "" || !access.ValidAction(operation.Action) {
			t.Fatalf("invalid Content operation: %#v", operation)
		}
	}
	descriptor := content.ModuleDataDescriptor()
	if descriptor.ModuleID != "content" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Content module-data descriptor: %#v", descriptor)
	}
}
