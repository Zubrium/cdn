package application

import (
	"context"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestRouteFingerprintTracksAuthoritativeRevision(t *testing.T) {
	target := model.Target{
		Kind:          model.TargetStaticSiteArtifact,
		DefaultLocale: "en-US",
		LocaleTargets: []model.LocaleArtifactTarget{{Locale: "en-US", ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125301", Revision: 1}},
	}
	route := model.Route{
		ID: "01938f7a-68af-7c20-a51d-78b6ca125399", Host: "www.example.invalid",
		Path: "/site/", Match: model.MatchPrefix, Target: target, State: model.StateActive,
		Revision: 1, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
	}
	first := RouteFingerprint(route)
	second := route
	second.Revision = 2
	if RouteFingerprint(second) == first {
		t.Fatal("route fingerprint did not change with revision")
	}
	third := route
	third.Target.LocaleTargets = append([]model.LocaleArtifactTarget(nil), route.Target.LocaleTargets...)
	third.Target.LocaleTargets[0].ArtifactManifestFileID = "01938f7a-68af-7c10-a51d-78b6ca125305"
	if RouteFingerprint(third) == first {
		t.Fatal("route fingerprint did not change with assignment")
	}

	repo := newFakeRouteRepository()
	if _, err := repo.Create(context.Background(), route, "cache-identity-key-0001", "fingerprint"); err != nil {
		t.Fatal(err)
	}
	service, err := New(repo, fakeIdentity{}, &fakeAuthorizer{maximum: contracts.AccessLevelRead}, fakeTargetValidator{}, Config{
		AccessGroup: "router_routes", DefaultPageSize: 20, MaxPageSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.ResolveRouteFingerprint(context.Background(), route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("resolved fingerprint = %q, want %q", got, first)
	}
	if _, err := service.ResolveRouteFingerprint(context.Background(), "01938f7a-68af-7c20-a51d-78b6ca125398"); err == nil {
		t.Fatal("unknown route fingerprint resolved")
	}
	if _, err := service.ResolveRouteFingerprint(nil, route.ID); err == nil {
		t.Fatal("nil context fingerprint resolved")
	}
}
