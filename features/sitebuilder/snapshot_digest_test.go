package sitebuilder

import (
	"encoding/json"
	"testing"
)

func TestSnapshotDigestPreservesLargeJSONNumbers(t *testing.T) {
	a := SnapshotPayload{FormatVersion: 4, Builds: []PinnedBuild{{Key: "sample", Manifest: json.RawMessage(`{"properties":{"amount":{"type":"integer","default":9007199254740992}}}`)}}}
	b := a
	b.Builds = append([]PinnedBuild(nil), a.Builds...)
	b.Builds[0].Manifest = json.RawMessage(`{"properties":{"amount":{"default":9007199254740993,"type":"integer"}}}`)
	first, err := SnapshotDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SnapshotDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("distinct schema defaults produced the same structural digest")
	}
	b.Builds[0].Manifest = json.RawMessage(`{"properties":{"amount":{"default":9007199254740992,"type":"integer"}}}`)
	third, err := SnapshotDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if first != third {
		t.Fatal("JSON object order changed the structural digest")
	}
}
