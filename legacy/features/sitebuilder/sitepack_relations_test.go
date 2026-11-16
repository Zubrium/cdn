package sitebuilder

import (
	"testing"

	"github.com/google/uuid"
)

func TestSitepackRejectsRetiredPerFeatureCMSShape(t *testing.T) {
	for _, raw := range []string{
		`{"formatVersion":1,"name":"old","content":[]}`,
		`{"formatVersion":1,"name":"old","publishedContent":[]}`,
		`{"formatVersion":1,"name":"old","contentRelations":{}}`,
		`{"formatVersion":1,"name":"old","drafts":[]}`,
	} {
		if !retiredSitepackContract([]byte(raw)) {
			t.Fatalf("retired sitepack contract was accepted: %s", raw)
		}
	}
	if retiredSitepackContract([]byte(`{"formatVersion":2,"name":"model","modelContent":{}}`)) {
		t.Fatal("model-backed sitepack was rejected")
	}
}

func TestSiteConfigRejectsRetiredPerFeatureBindings(t *testing.T) {
	for _, raw := range []string{
		`{"formatVersion":3,"pages":[{"id":"home","name":"Home","kind":"page","placements":[{"id":"detail","collectionSource":{}}]}],"routeTree":{"id":"root","type":"site"}}`,
		`{"formatVersion":3,"pages":[{"id":"home","name":"Home","kind":"page","placements":[{"id":"card","contentBindings":[]}]}],"routeTree":{"id":"root","type":"site"}}`,
	} {
		if _, err := ParseSiteConfig([]byte(raw)); err == nil {
			t.Fatalf("retired per-feature binding was accepted: %s", raw)
		}
	}
}

func TestModelIDsAreGeneratedAndDistinct(t *testing.T) {
	index, _ := sitepackModelFixture()
	oldEntityID := index.Payload.Config.DataModel.Entities[0].ID
	remap, err := remapSitepackModelIdentities(index)
	if err != nil {
		t.Fatal(err)
	}
	newEntityID := index.Payload.Config.DataModel.Entities[0].ID
	if newEntityID == oldEntityID || remap.entities[oldEntityID] != newEntityID {
		t.Fatalf("entity IDs were not regenerated: %s -> %s", oldEntityID, newEntityID)
	}
	if _, err := uuid.Parse(newEntityID); err != nil {
		t.Fatalf("generated model ID is not a UUID: %v", err)
	}
}
