package presentation

import (
	"testing"

	"github.com/Zubrium/cdn/core/access"
	routerengine "github.com/Zubrium/cdn/engines/router"
)

func TestRouterDescriptorDeclaresOnlyExplicitFileOperations(t *testing.T) {
	operations := (Descriptor{}).Operations()
	if len(operations) == 0 {
		t.Fatal("Router descriptor must expose its typed operation inventory")
	}
	for _, operation := range operations {
		if operation.Name == "" || operation.ResourceType == "" || !access.ValidAction(operation.Action) {
			t.Fatalf("invalid Router operation: %#v", operation)
		}
	}
	descriptor := routerengine.ModuleDataDescriptor()
	if descriptor.ModuleID != "router" || len(descriptor.Namespaces) == 0 {
		t.Fatalf("invalid Router module-data descriptor: %#v", descriptor)
	}
}

func TestRouteTargetRejectsUntrustedFileLocations(t *testing.T) {
	for _, target := range []RouteTarget{
		{Kind: TargetCoreFile, FileID: "https://example.invalid/asset"},
		{Kind: TargetStaticSiteArtifact},
	} {
		if target.Validate() == nil {
			t.Errorf("RouteTarget.Validate(%+v) succeeded", target)
		}
	}
	for _, target := range []RouteTarget{
		{Kind: TargetCoreFile, FileID: "01938f7a-68af-7c10-a51d-78b6ca125301"},
		{Kind: TargetStaticSiteArtifact, FileID: "01938f7a-68af-7c10-a51d-78b6ca125302"},
	} {
		if err := target.Validate(); err != nil {
			t.Errorf("RouteTarget.Validate(%+v) = %v", target, err)
		}
	}
}
