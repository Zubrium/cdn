package sitebuilder

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSitepackRewritesOnlyDeclaredArgumentMediaReferences(t *testing.T) {
	oldID, newID := uuid.NewString(), uuid.NewString()
	manifest, err := json.Marshal(Manifest{
		FormatVersion: 2,
		Key:           "example",
		Name:          "Example",
		Version:       "2.0.0",
		EngineAPI:     "2",
		Slots:         []string{"main"},
		Arguments:     json.RawMessage(`{"type":"object","properties":{"image":{"type":"string","format":"media"},"text":{"type":"string"}}}`),
		DataInputs:    []DataInputPort{{Key: "title", Kind: "scalar", Type: "text"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	placement := Placement{
		ID: "p", Build: BuildRef{FeatureKey: "example", Version: "2.0.0"}, Slot: "main",
		Arguments: map[string]ArgumentBinding{
			"image": {Source: "literal", Value: json.RawMessage(`"` + oldID + `"`)},
			"text":  {Source: "literal", Value: json.RawMessage(`"` + oldID + `"`)},
		},
	}
	page := Page{ID: "page", Name: "Page", Kind: "page", Placements: []Placement{placement}}
	config := SiteConfig{FormatVersion: 4, Pages: []Page{page}}
	index := sitepackIndex{Payload: SnapshotPayload{
		Builds: []PinnedBuild{{Key: "example", Version: "2.0.0", Manifest: manifest}},
		Config: config,
	}}
	refs, err := checkPackReferences(&index)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].String() != oldID {
		t.Fatalf("media refs: %v", refs)
	}
	if err := rewritePackReferences(&index, map[string]string{oldID: newID}); err != nil {
		t.Fatal(err)
	}
	bindings := index.Payload.Config.Pages[0].Placements[0].Arguments
	if string(bindings["text"].Value) != `"`+oldID+`"` || string(bindings["image"].Value) != `"`+newID+`"` {
		t.Fatalf("bad literal remap: %v", bindings)
	}
	if string(index.Payload.Builds[0].Manifest) != string(manifest) {
		t.Fatal("feature manifest was altered")
	}
}

func TestSitepackRequiresAllReferencedFiles(t *testing.T) {
	id := uuid.NewString()
	manifest, err := json.Marshal(Manifest{
		FormatVersion: 2, Key: "example", Name: "Example", Version: "1.0.0", EngineAPI: "2",
		Slots: []string{"main"}, Arguments: json.RawMessage(`{"type":"object","properties":{"image":{"type":"string","format":"media"}}}`),
		DataInputs: []DataInputPort{},
	})
	if err != nil {
		t.Fatal(err)
	}
	placement := Placement{
		ID: "p", Build: BuildRef{FeatureKey: "example", Version: "1.0.0"},
		Arguments: map[string]ArgumentBinding{"image": {Source: "literal", Value: json.RawMessage(`"` + id + `"`)}},
	}
	index := sitepackIndex{Payload: SnapshotPayload{
		Builds: []PinnedBuild{{Key: "example", Version: "1.0.0", Manifest: manifest}},
		Config: SiteConfig{Pages: []Page{{Placements: []Placement{placement}}}},
	}}
	if err := rewritePackReferences(&index, map[string]string{}); err == nil {
		t.Fatal("missing media mapping accepted")
	}
}
