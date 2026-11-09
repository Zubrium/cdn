package sitebuilder

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSnapshotDigestIgnoresCreationSummary(t *testing.T) {
	payload := SnapshotPayload{FormatVersion: 4, Config: SiteConfig{FormatVersion: 3}}
	withoutSummary, err := SnapshotDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload.CreationSummary = &SnapshotCreationSummary{Origin: snapshotOriginChild, Changes: []string{"pages_routes"}}
	withSummary, err := SnapshotDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if withSummary != withoutSummary {
		t.Fatal("creation provenance must not change the structural snapshot digest")
	}
}

func TestSnapshotCreationSummaryFindsStructuralAndBuildChanges(t *testing.T) {
	parent := SnapshotPayload{
		Config: SiteConfig{
			FormatVersion: 3,
			Features:      []BuildRef{{FeatureKey: "catalog", Version: "1.0.4"}},
			Pages:         []Page{{ID: "home", Name: "Home", Kind: "page"}},
		},
		Builds: []PinnedBuild{{Key: "catalog", Version: "1.0.4", Digest: "old"}},
	}
	next := SnapshotPayload{
		Config: SiteConfig{
			FormatVersion: 3,
			Features:      []BuildRef{{FeatureKey: "catalog", Version: "1.0.5"}, {FeatureKey: "header", Version: "1.0.0"}},
			Pages:         []Page{{ID: "home", Name: "Home", Kind: "page"}, {ID: "about", Name: "About", Kind: "page"}},
		},
		Builds: []PinnedBuild{{Key: "catalog", Version: "1.0.5", Digest: "new"}, {Key: "header", Version: "1.0.0", Digest: "header"}},
	}
	summary := snapshotCreationSummary(&parent, next)
	if summary.Origin != snapshotOriginChild {
		t.Fatalf("unexpected origin: %q", summary.Origin)
	}
	if !reflect.DeepEqual(summary.Changes, []string{"pages_routes", "features"}) {
		t.Fatalf("unexpected change categories: %#v", summary.Changes)
	}
	want := []SnapshotFeatureChange{
		{FeatureKey: "catalog", Action: "updated", PreviousVersion: "1.0.4", Version: "1.0.5"},
		{FeatureKey: "header", Action: "added", Version: "1.0.0"},
	}
	if !reflect.DeepEqual(summary.FeatureChanges, want) {
		t.Fatalf("unexpected feature changes: %#v", summary.FeatureChanges)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" {
		t.Fatal("summary should be serializable for the payload")
	}
}
