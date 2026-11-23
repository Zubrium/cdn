package presentation

import (
	"testing"

	"github.com/Zubrium/cdn/engines/website"
)

func TestWebsiteDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	component := (Descriptor{}).ComponentDescriptor()
	if component.ID != "engine.website" || len(component.AccessGroups) != 1 || component.AccessGroups[0] != "features" {
		t.Fatalf("invalid neutral Website profile metadata: %#v", component)
	}
	descriptor := website.RecordDescriptor()
	if descriptor.OwnerID != "website" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Website module-data descriptor: %#v", descriptor)
	}
}
