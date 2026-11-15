package media

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestProfileAtSchemaNestedArrayMediaField(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"sections":{"type":"array","items":{"type":"object","properties":{"image":{"type":"string","format":"media","x-media":{"accept":"raster-image","renditions":[{"name":"card","width":600,"height":400,"fit":"cover"}]}}}}}}}`)
	profile, found, err := ProfileAtSchema(schema, "/sections/items/image")
	if err != nil {
		t.Fatal(err)
	}
	if !found || profile.Accept != "raster-image" || len(profile.Renditions) != 1 || profile.Renditions[0].Name != "card" {
		t.Fatalf("unexpected profile: found=%t profile=%+v", found, profile)
	}
}

func TestFieldProfileRejectsExcessiveRenditionCount(t *testing.T) {
	renditions := make([]Rendition, MaxRenditions+1)
	for index := range renditions {
		renditions[index] = Rendition{Name: fmt.Sprintf("size_%02d", index), Width: 100 + index, Height: 100, Fit: "cover"}
	}
	if err := (FieldProfile{Accept: "raster-image", Renditions: renditions}).Validate(); err == nil {
		t.Fatalf("profile with %d renditions must be rejected", len(renditions))
	}
}

func TestProfileHashIsStableAndChangesWithProfile(t *testing.T) {
	profile := FieldProfile{Accept: "raster-image", Renditions: []Rendition{{Name: "card", Width: 600, Height: 400, Fit: "cover"}}}
	first, err := ProfileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProfileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("profile hash changed: %s != %s", first, second)
	}
	profile.Renditions[0].Width++
	third, err := ProfileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("profile changes must create a new immutable profile hash")
	}
}

func TestFieldProfileRejectsInvalidFitAndDimensions(t *testing.T) {
	for name, profile := range map[string]FieldProfile{
		"invalid fit":    {Accept: "raster-image", Renditions: []Rendition{{Name: "card", Width: 400, Height: 300, Fit: "stretch"}}},
		"zero dimension": {Accept: "raster-image", Renditions: []Rendition{{Name: "card", Width: 0, Height: 300, Fit: "cover"}}},
		"duplicate name": {Accept: "raster-image", Renditions: []Rendition{{Name: "card", Width: 400, Height: 300, Fit: "cover"}, {Name: "card", Width: 200, Height: 100, Fit: "contain"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := profile.Validate(); err == nil {
				t.Fatal("expected invalid profile to fail validation")
			}
		})
	}
}
