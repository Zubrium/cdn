package presentation_test

import (
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/forms"
	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
)

func TestFormsDescriptorDeclaresItsOperationsAndData(t *testing.T) {
	operations := (formapi.Descriptor{}).Operations()
	if len(operations) == 0 {
		t.Fatal("Forms descriptor must expose its typed operation inventory")
	}
	exportRegistered := false
	for _, operation := range operations {
		if operation.Name == "" || operation.ResourceType == "" || !platform.ValidAccessAction(operation.Action) {
			t.Fatalf("invalid Forms operation: %#v", operation)
		}
		if operation.Name == "submission.export" && operation.Action == platform.AccessActionRead {
			exportRegistered = true
		}
	}
	if !exportRegistered {
		t.Fatal("Forms descriptor must register the read-only submission export operation")
	}
	descriptor := forms.ModuleDataDescriptor()
	if descriptor.ModuleID != "forms" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Forms module-data descriptor: %#v", descriptor)
	}
}
