package presentation

import (
	"testing"

	routerengine "github.com/Zubrium/cdn/engines/router"
	routemodel "github.com/Zubrium/cdn/engines/router/features/routes/model"
)

func TestRouterDescriptorDeclaresOnlyExplicitFileOperations(t *testing.T) {
	component := (Descriptor{}).ComponentDescriptor()
	if component.ID != "engine.router" || len(component.AccessGroups) != 1 || component.AccessGroups[0] != "router_routes" {
		t.Fatalf("invalid neutral Router profile metadata: %#v", component)
	}
	descriptor := routerengine.RecordDescriptor()
	if descriptor.OwnerID != "router" || len(descriptor.Namespaces) == 0 {
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
	artifactTarget, err := routemodel.NewStaticArtifactTarget("en-US", []routemodel.LocaleArtifactTarget{
		{Locale: "en-US", ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125302", Revision: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []RouteTarget{
		{Kind: TargetCoreFile, FileID: "01938f7a-68af-7c10-a51d-78b6ca125301"},
		artifactTarget,
	} {
		if err := target.Validate(); err != nil {
			t.Errorf("RouteTarget.Validate(%+v) = %v", target, err)
		}
	}
}
