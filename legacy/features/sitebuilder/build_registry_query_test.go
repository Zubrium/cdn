package sitebuilder

import "testing"

func TestConfigBuildRefsUsesOnlyFeaturesAndPlacements(t *testing.T) {
	cfg := SiteConfig{
		FormatVersion: 3,
		Features:      []BuildRef{{FeatureKey: "global", Version: "1.0.0"}},
		Pages: []Page{{ID: "home", Name: "Home", Kind: "page", Placements: []Placement{{
			ID: "target", Build: BuildRef{FeatureKey: "target", Version: "2.0.0"},
		}}}},
	}
	got := configBuildRefs(cfg)
	if got["target"] != "2.0.0" || got["global"] != "1.0.0" || len(got) != 2 {
		t.Fatalf("snapshot build references must include selected and placed builds only: %v", got)
	}
}
