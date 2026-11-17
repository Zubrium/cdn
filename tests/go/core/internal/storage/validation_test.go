package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMetadataValidation(t *testing.T) {
	for _, name := range []string{"", ".", "..", "x/y", "x\\y", "x\n", " leading"} {
		if ValidName(name) {
			t.Errorf("accepted name %q", name)
		}
	}
	if !ValidName("изображение.png") {
		t.Fatal("valid UTF-8 name rejected")
	}
	for _, value := range []string{"", "text", "text/plain\r\nX: value"} {
		if ValidContentType(value) {
			t.Errorf("accepted content type %q", value)
		}
	}
	if !ValidContentType("text/plain; charset=utf-8") {
		t.Fatal("valid content type rejected")
	}
}

func TestGenericMetadataGroupsAndUUIDValidation(t *testing.T) {
	for _, metadata := range []map[string]string{
		{"name": "../unsafe"},
		{"bad\nkey": "value"},
		{strings.Repeat("k", 129): "value"},
		{"caption": "before\x00after"},
	} {
		if ValidMetadata(metadata, 1024) {
			t.Errorf("accepted invalid metadata %q", metadata)
		}
	}
	if !ValidMetadata(map[string]string{"name": "asset.svg", "caption": "A title"}, 128) {
		t.Fatal("valid generic metadata rejected")
	}
	if ValidMetadata(map[string]string{"caption": strings.Repeat("x", 1024)}, 128) {
		t.Fatal("metadata exceeding its byte budget accepted")
	}
	for _, metadata := range []map[string]string{
		{"simple": "plain", "name": "asset.svg"},
		{"caption": "quote: \\\"; slash: \\\\"},
		{"caption": "<>&\u2028\u2029"},
		{"caption": "line\nbreak"},
	} {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		budget := int64(len(encoded))
		if !ValidMetadata(metadata, budget) {
			t.Errorf("metadata was rejected at its exact encoded size %d: %q", budget, metadata)
		}
		if budget > 1 && ValidMetadata(metadata, budget-1) {
			t.Errorf("metadata exceeded its encoded size budget %d: %q", budget-1, metadata)
		}
	}
	for _, groups := range [][]string{nil, {}, {"Assets"}, {"assets", "assets"}, {strings.Repeat("x", 65)}} {
		if ValidGroups(groups) {
			t.Errorf("accepted invalid group list %q", groups)
		}
	}
	if !ValidGroups([]string{"assets", "feature_1"}) {
		t.Fatal("valid group list rejected")
	}
	for _, id := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"} {
		if ValidUUID(id) {
			t.Errorf("accepted invalid UUID %q", id)
		}
	}
	if !ValidUUID("00000000-0000-4000-8000-000000000001") {
		t.Fatal("valid canonical UUID rejected")
	}
}
