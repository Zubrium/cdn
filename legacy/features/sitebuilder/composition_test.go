package sitebuilder

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type compositionBuildLookup map[string]BuildInfo

func (l compositionBuildLookup) Build(key, version string) (BuildInfo, bool) {
	build, ok := l[key+"@"+version]
	return build, ok
}

func TestParseSiteConfigAcceptsRouteForestAndSharedPageAliases(t *testing.T) {
	cfg, err := ParseSiteConfig([]byte(`{
		"formatVersion":3,
		"pages":[
			{"id":"home","name":"Home","kind":"page","placements":[]},
			{"id":"detail","name":"Detail","kind":"detail","placements":[]}
		],
		"routeTree":{"id":"site","type":"site","children":[
			{"id":"home-route","type":"path","path":"/","pageId":"home"},
			{"id":"product-route","type":"path","path":"/products/:slug","pageId":"detail"},
			{"id":"item-alias","type":"path","path":"/item/~(?<slug>[a-z0-9-]+)","pageId":"detail"}
		]}
	}`))
	if err != nil {
		t.Fatalf("route forest with compatible aliases was rejected: %v", err)
	}
	if cfg.FormatVersion != 3 || cfg.RouteTree.Type != "site" || len(cfg.RouteTree.Children) != 3 {
		t.Fatalf("unexpected parsed site config: %#v", cfg)
	}
}

func TestParseSiteConfigRejectsIncompatibleAliasesButKeepsV2(t *testing.T) {
	invalid := []byte(`{
		"formatVersion":3,
		"pages":[{"id":"detail","name":"Detail","kind":"detail","placements":[]}],
		"routeTree":{"id":"site","type":"site","children":[
			{"id":"by-slug","type":"path","path":"/products/:slug","pageId":"detail"},
			{"id":"by-id","type":"path","path":"/items/:id","pageId":"detail"}
		]}
	}`)
	if _, err := ParseSiteConfig(invalid); err == nil {
		t.Fatal("expected aliases with different path argument names to be rejected")
	}

	legacy := []byte(`{"formatVersion":2,"pages":[{"id":"home","name":"Home","kind":"page","placements":[]}],"routeTree":{"id":"root","type":"index","pageId":"home"}}`)
	if _, err := ParseSiteConfig(legacy); err != nil {
		t.Fatalf("legacy v2 configuration should remain supported: %v", err)
	}
}

func TestSiteConfigRejectsRetiredContentBindings(t *testing.T) {
	legacy := []byte(`{
		"formatVersion":3,
		"pages":[{"id":"home","name":"Home","kind":"page","placements":[{
			"id":"target","build":{"featureKey":"home-data","version":"1.0.0"},
			"slot":"main","contentBindings":[{"source":{"featureKey":"catalog","version":"1.0.0"},"targetArrayPointer":"/products"}]
		}]}],
		"routeTree":{"id":"site","type":"site","children":[{"id":"home-route","type":"path","path":"/","pageId":"home"}]}
	}`)
	_, err := ParseSiteConfig(legacy)
	if err == nil || !strings.Contains(err.Error(), `unknown field "contentBindings"`) {
		t.Fatalf("expected retired contentBindings field to be rejected by the new snapshot contract, got %v", err)
	}
}

func TestSiteConfigRejectsMixedVersionsForOneFeatureKey(t *testing.T) {
	cfg := SiteConfig{
		FormatVersion: 3,
		Pages: []Page{{
			ID: "home", Name: "Home", Kind: "page",
			Placements: []Placement{
				{ID: "catalog-v1", Build: BuildRef{FeatureKey: "catalog", Version: "1.0.0"}},
				{ID: "catalog-v2", Build: BuildRef{FeatureKey: "catalog", Version: "2.0.0"}},
			},
		}},
		RouteTree: RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home"}}},
	}
	if err := cfg.Validate(nil); err == nil {
		t.Fatal("snapshot must not mix two versions of one logical feature")
	}
}

func TestSiteConfigValidatesDataInputBindingsAgainstSnapshotModel(t *testing.T) {
	entityID, fieldID := uuid.NewString(), uuid.NewString()
	manifest := Manifest{
		FormatVersion: 2,
		Key:           "hero",
		Version:       "2.0.0",
		Slots:         []string{"main"},
		Arguments:     json.RawMessage(`{"type":"object","properties":{}}`),
		DataInputs:    []DataInputPort{{Key: "hero", Kind: "record", Fields: []DataInputField{{Path: "/title", Type: "text"}}}},
	}
	lookup := compositionBuildLookup{"hero@2.0.0": {Manifest: manifest}}
	cfg := SiteConfig{
		FormatVersion: 3,
		DataModel: &DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
			ID: entityID, Key: "hero", Name: "Hero", Fields: []DataModelField{{ID: fieldID, Key: "title", Name: "Title", Type: "text"}},
		}}},
		Pages: []Page{{ID: "home", Name: "Home", Kind: "page", Placements: []Placement{{
			ID: "hero-placement", Build: BuildRef{FeatureKey: "hero", Version: "2.0.0"}, Slot: "main",
			DataBindings: []DataInputBinding{{InputKey: "hero", EntityID: entityID, Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fieldID}}}},
		}}}},
		RouteTree: RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home"}}},
	}
	if err := cfg.Validate(lookup); err != nil {
		t.Fatalf("valid snapshot model binding was rejected: %v", err)
	}

	cfg.Pages[0].Placements[0].DataBindings = nil
	if err := cfg.Validate(lookup); err == nil {
		t.Fatal("missing feature input binding was accepted")
	}

	cfg.Pages[0].Placements[0].DataBindings = []DataInputBinding{{InputKey: "hero", EntityID: entityID, Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fieldID}}}}
	cfg.DataModel = nil
	if err := cfg.Validate(lookup); err == nil {
		t.Fatal("feature inputs without a snapshot data model were accepted")
	}
}
