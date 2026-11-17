package presentation

import (
	"testing"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/engines/website"
)

func TestWebsiteDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	operations := (Descriptor{}).Operations()
	if len(operations) == 0 {
		t.Fatal("Website descriptor must expose its typed operation inventory")
	}
	for _, operation := range operations {
		if operation.Name == "" || operation.ResourceType == "" || !access.ValidAction(operation.Action) {
			t.Fatalf("invalid Website operation: %#v", operation)
		}
	}
	descriptor := website.ModuleDataDescriptor()
	if descriptor.ModuleID != "website" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Website module-data descriptor: %#v", descriptor)
	}
}
