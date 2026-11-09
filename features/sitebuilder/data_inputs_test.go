package sitebuilder

import (
	"encoding/json"
	"testing"

	"websiteengine/features/media"
)

func validDataInputMediaProfile() *media.FieldProfile {
	return &media.FieldProfile{
		Accept:     "raster-image",
		Renditions: []media.Rendition{{Name: "card", Width: 480, Height: 320, Fit: "cover"}},
	}
}

func TestValidateDataInputPortsAcceptsTypedShapes(t *testing.T) {
	tests := []struct {
		name  string
		ports []DataInputPort
	}{
		{name: "empty declaration", ports: []DataInputPort{}},
		{name: "scalar", ports: []DataInputPort{{Key: "headline", Kind: "scalar", Type: "text"}}},
		{name: "scalar media value and profile", ports: []DataInputPort{{Key: "image", Kind: "scalar", Type: "media", Profile: validDataInputMediaProfile()}}},
		{name: "record with nested JSON pointer fields", ports: []DataInputPort{{
			Key: "author", Kind: "record", Fields: []DataInputField{
				{Path: "/name", Type: "text"},
				{Path: "/profile/website", Type: "url"},
				{Path: "/profile/avatar", Type: "media", Profile: validDataInputMediaProfile()},
			},
		}}},
		{name: "collection with typed fields", ports: []DataInputPort{{
			Key: "products", Kind: "collection", Fields: []DataInputField{
				{Path: "/slug", Type: "text"},
				{Path: "/price", Type: "number"},
				{Path: "/image", Type: "media", Profile: validDataInputMediaProfile()},
			},
		}}},
		{name: "same path in separate ports", ports: []DataInputPort{
			{Key: "primary", Kind: "record", Fields: []DataInputField{{Path: "/title", Type: "text"}}},
			{Key: "secondary", Kind: "record", Fields: []DataInputField{{Path: "/title", Type: "text"}}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDataInputPorts(test.ports); err != nil {
				t.Fatalf("validateDataInputPorts() error = %v", err)
			}
		})
	}
}

func TestValidateDataInputPortsRejectsInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		ports []DataInputPort
	}{
		{name: "invalid port key", ports: []DataInputPort{{Key: "not-a-key", Kind: "scalar", Type: "text"}}},
		{name: "duplicate port key", ports: []DataInputPort{
			{Key: "product", Kind: "scalar", Type: "text"},
			{Key: "product", Kind: "record", Fields: []DataInputField{{Path: "/title", Type: "text"}}},
		}},
		{name: "unknown kind", ports: []DataInputPort{{Key: "product", Kind: "object", Fields: []DataInputField{{Path: "/title", Type: "text"}}}}},
		{name: "scalar missing type", ports: []DataInputPort{{Key: "headline", Kind: "scalar"}}},
		{name: "scalar unsupported type", ports: []DataInputPort{{Key: "headline", Kind: "scalar", Type: "object"}}},
		{name: "scalar with fields", ports: []DataInputPort{{Key: "headline", Kind: "scalar", Type: "text", Fields: []DataInputField{{Path: "/title", Type: "text"}}}}},
		{name: "scalar with empty fields property", ports: []DataInputPort{{Key: "headline", Kind: "scalar", Type: "text", Fields: []DataInputField{}}}},
		{name: "profile on scalar non-media type", ports: []DataInputPort{{Key: "headline", Kind: "scalar", Type: "text", Profile: validDataInputMediaProfile()}}},
		{name: "invalid scalar media profile", ports: []DataInputPort{{Key: "image", Kind: "scalar", Type: "media", Profile: &media.FieldProfile{Accept: "unsupported"}}}},
		{name: "record with scalar type", ports: []DataInputPort{{Key: "author", Kind: "record", Type: "text", Fields: []DataInputField{{Path: "/name", Type: "text"}}}}},
		{name: "enum values on composite port", ports: []DataInputPort{{Key: "author", Kind: "record", Values: []string{"one"}, Fields: []DataInputField{{Path: "/name", Type: "text"}}}}},
		{name: "empty values on non-enum field", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{{Path: "/name", Type: "text", Values: []string{}}}}}},
		{name: "profile on record port", ports: []DataInputPort{{Key: "author", Kind: "record", Profile: validDataInputMediaProfile(), Fields: []DataInputField{{Path: "/name", Type: "text"}}}}},
		{name: "record without fields", ports: []DataInputPort{{Key: "author", Kind: "record"}}},
		{name: "collection without fields", ports: []DataInputPort{{Key: "products", Kind: "collection"}}},
		{name: "profile on collection port", ports: []DataInputPort{{Key: "products", Kind: "collection", Profile: validDataInputMediaProfile(), Fields: []DataInputField{{Path: "/image", Type: "media"}}}}},
		{name: "relative field path", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{{Path: "name", Type: "text"}}}}},
		{name: "invalid pointer escape", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{{Path: "/na~2me", Type: "text"}}}}},
		{name: "duplicate field path", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{
			{Path: "/name", Type: "text"}, {Path: "/name", Type: "text"},
		}}}},
		{name: "parent and child paths overlap", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{
			{Path: "/profile", Type: "text"}, {Path: "/profile/name", Type: "text"},
		}}}},
		{name: "child and parent paths overlap", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{
			{Path: "/profile/name", Type: "text"}, {Path: "/profile", Type: "text"},
		}}}},
		{name: "unsupported composite field type", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{{Path: "/metadata", Type: "json"}}}}},
		{name: "profile on non-media field", ports: []DataInputPort{{Key: "author", Kind: "record", Fields: []DataInputField{{Path: "/name", Type: "text", Profile: validDataInputMediaProfile()}}}}},
		{name: "invalid media field profile", ports: []DataInputPort{
			{Key: "author", Kind: "record", Fields: []DataInputField{
				{Path: "/avatar", Type: "media", Profile: &media.FieldProfile{Accept: "video"}},
			}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDataInputPorts(test.ports); err == nil {
				t.Fatal("validateDataInputPorts() error = nil, want invalid declaration error")
			}
		})
	}
}

func TestDataInputPortDecodesScalarType(t *testing.T) {
	var port DataInputPort
	if err := json.Unmarshal([]byte(`{"key":"heroImage","kind":"scalar","type":"media","profile":{"accept":"raster-image","renditions":[{"name":"card","width":480,"height":320,"fit":"cover"}]}}`), &port); err != nil {
		t.Fatal(err)
	}
	if port.Key != "heroImage" || port.Kind != "scalar" || port.Type != "media" || port.Profile == nil || port.Profile.Accept != "raster-image" || len(port.Profile.Renditions) != 1 || port.Profile.Renditions[0].Name != "card" {
		t.Fatalf("decoded scalar input = %+v", port)
	}
	if err := validateDataInputPorts([]DataInputPort{port}); err != nil {
		t.Fatalf("decoded scalar input failed validation: %v", err)
	}
}

func TestDataInputFieldDecodesMediaProfile(t *testing.T) {
	var port DataInputPort
	if err := json.Unmarshal([]byte(`{"key":"product","kind":"record","fields":[{"path":"/image","type":"media","profile":{"accept":"raster-image","renditions":[{"name":"card","width":480,"height":320,"fit":"cover"}]}}]}`), &port); err != nil {
		t.Fatal(err)
	}
	if len(port.Fields) != 1 || port.Fields[0].Profile == nil || port.Fields[0].Profile.Accept != "raster-image" || len(port.Fields[0].Profile.Renditions) != 1 {
		t.Fatalf("decoded media input field = %+v", port.Fields)
	}
	if err := validateDataInputPorts([]DataInputPort{port}); err != nil {
		t.Fatalf("decoded media field failed validation: %v", err)
	}
}

func TestDataInputMediaProfileResolvesTypedPaths(t *testing.T) {
	profile := validDataInputMediaProfile()
	inputs := []DataInputPort{
		{Key: "cover", Kind: "scalar", Type: "media", Profile: profile},
		{Key: "products", Kind: "collection", Fields: []DataInputField{
			{Path: "/visual/imageSrc", Type: "media", Profile: profile},
			{Path: "/title", Type: "text"},
		}},
	}
	tests := []struct {
		path    string
		want    bool
		wantErr bool
	}{
		{path: "/cover", want: true},
		{path: "/products/visual/imageSrc", want: true},
		{path: "/products/title", wantErr: true},
		{path: "/products/missing", wantErr: true},
		{path: "/products", wantErr: true},
		{path: "products/imageSrc", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			got, exists, err := dataInputMediaProfile(inputs, test.path)
			if (err != nil) != test.wantErr || exists != test.want {
				t.Fatalf("dataInputMediaProfile(%q) = (%+v, %v, %v), want exists=%v error=%v", test.path, got, exists, err, test.want, test.wantErr)
			}
			if test.want && got.Accept != profile.Accept {
				t.Fatalf("resolved profile = %+v, want %+v", got, *profile)
			}
		})
	}
}
