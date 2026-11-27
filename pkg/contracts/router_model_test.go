package contracts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCanonicalLocaleIsNeutralAndStrict(t *testing.T) {
	for input, want := range map[string]string{
		"en-US":      "en-US",
		"EN-us":      "en-US",
		"fr":         "fr",
		"zh-Hant-TW": "zh-Hant-tw",
		"de-419":     "de-419",
	} {
		got, err := CanonicalLocale(input)
		if err != nil || got != want {
			t.Errorf("CanonicalLocale(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", " en-US", "en-US ", "und", "e", "en--US", "en_US", "en/US", strings.Repeat("a", 65)} {
		if got, err := CanonicalLocale(input); !errors.Is(err, ErrInvalidRoute) || got != "" {
			t.Errorf("CanonicalLocale(%q) = %q, %v; want invalid route error", input, got, err)
		}
	}
}

func TestRouterPathMatchingHonorsWholeSegments(t *testing.T) {
	for _, scenario := range []struct {
		routePath, requestPath, match string
		want                          bool
	}{
		{"/site/", "/site/", MatchExact, true},
		{"/site/", "/site/child", MatchExact, false},
		{"/site", "/sitewise", MatchPrefix, false},
		{"/site", "/site/child", MatchPrefix, true},
		{"/site/", "/site/child", MatchPrefix, true},
		{"/", "/anything", MatchPrefix, true},
	} {
		got := PathMatches(scenario.routePath, scenario.requestPath, scenario.match)
		if got != scenario.want {
			t.Errorf("PathMatches(%q, %q, %q) = %v, want %v", scenario.routePath, scenario.requestPath, scenario.match, got, scenario.want)
		}
	}
	normalized, err := NormalizePath("/assets/site%20one/")
	if err != nil || normalized != "/assets/site%20one/" {
		t.Fatalf("NormalizePath = %q, %v", normalized, err)
	}
	for _, input := range []string{"/assets/../site/", "/site%2Fhidden", "relative/path", "/site?query"} {
		if _, err := NormalizePath(input); !errors.Is(err, ErrInvalidRoute) {
			t.Errorf("ambiguous path %q was accepted: %v", input, err)
		}
	}
}

func TestRouterTargetDispositionAndArtifactCASAreNeutral(t *testing.T) {
	fileID := "01938f7a-68af-7c10-a51d-78b6ca125301"
	manifestID := "01938f7a-68af-7c10-a51d-78b6ca125302"
	for _, disposition := range []string{"", DispositionInline, DispositionAttachment} {
		target := Target{Kind: TargetCoreFile, FileID: fileID, Disposition: disposition}
		if err := target.Validate(); err != nil {
			t.Errorf("core file disposition %q rejected: %v", disposition, err)
		}
	}
	if err := (Target{Kind: TargetCoreFile, FileID: fileID, Disposition: "unsafe"}).Validate(); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("unsafe disposition accepted: %v", err)
	}

	artifact, err := NewStaticArtifactTarget("en-US", []LocaleArtifactTarget{
		{Locale: "en-US", ArtifactManifestFileID: manifestID, Revision: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	slots := artifact.LocaleSlots()
	encoded, err := json.Marshal(slots)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "revision") {
		t.Fatalf("artifact slots exposed internal revision: %s", encoded)
	}
	route, err := NewRoute("01938f7a-68af-7c20-a51d-78b6ca125399", "www.example.invalid", "/site/", MatchPrefix, artifact, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if route.Revision != 1 || route.Target.LocaleTargets[0].Revision != 1 {
		t.Fatalf("initial CAS revisions were not assigned: %#v", route)
	}
	if err := ValidateRoute(route); err != nil {
		t.Fatalf("valid route rejected: %v", err)
	}
	corrupted := route
	corrupted.Target.LocaleTargets = append([]LocaleArtifactTarget(nil), route.Target.LocaleTargets...)
	corrupted.Target.LocaleTargets[0].Revision = 0
	if err := ValidateRoute(corrupted); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("lost artifact CAS revision accepted: %v", err)
	}
}

func TestPublishedContentJSONFieldsAreStable(t *testing.T) {
	content := PublishedContent{
		SnapshotID: "01938f7a-68af-7c10-a51d-78b6ca125303", Locale: "en-US", ContentRevision: 7,
		Records:    []PublishedContentRecord{{ID: "record", TranslationID: "translation", Locale: "en-US", EntityID: "entity", SortOrder: 2, Values: map[string]any{"title": "Title"}}},
		Selections: []PublishedContentSelection{{PlacementID: "placement", InputKey: "items", Mode: "manual", RecordIDs: []string{"record"}}},
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"snapshot_id"`, `"locale"`, `"content_revision"`, `"records"`, `"selections"`, `"translation_id"`, `"entity_id"`, `"sort_order"`, `"values"`, `"placement_id"`, `"input_key"`, `"mode"`, `"record_ids"`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("published content payload lost %s: %s", field, encoded)
		}
	}
	var decoded PublishedContent
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.ContentRevision != 7 || len(decoded.Records) != 1 || len(decoded.Selections) != 1 {
		t.Fatalf("published content payload did not round-trip: %#v, %v", decoded, err)
	}
}
