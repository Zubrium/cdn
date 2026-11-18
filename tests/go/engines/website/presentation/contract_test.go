package presentation

import (
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/website"
)

func TestWebsiteDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	operations := (Descriptor{}).Operations()
	if len(operations) == 0 {
		t.Fatal("Website descriptor must expose its typed operation inventory")
	}
	for _, operation := range operations {
		if operation.Name == "" || operation.ResourceType == "" || !platform.ValidAccessAction(operation.Action) {
			t.Fatalf("invalid Website operation: %#v", operation)
		}
	}
	descriptor := website.ModuleDataDescriptor()
	if descriptor.ModuleID != "website" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Website module-data descriptor: %#v", descriptor)
	}
}
