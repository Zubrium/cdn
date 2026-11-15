package sitebuilder

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validDataModelFixture() DataModel {
	return DataModel{
		FormatVersion: 1,
		Entities: []DataModelEntity{
			{
				ID: uuid.NewString(), Key: "product", Name: "Product",
				Fields: []DataModelField{
					{ID: uuid.NewString(), Key: "slug", Name: "Slug", Type: "text", Unique: true},
					{ID: uuid.NewString(), Key: "status", Name: "Status", Type: "enum", Options: []string{"draft", "published"}},
					{ID: uuid.NewString(), Key: "description", Name: "Description", Type: "longText"},
				},
			},
			{ID: uuid.NewString(), Key: "category", Name: "Category", Fields: []DataModelField{{ID: uuid.NewString(), Key: "name", Name: "Name", Type: "text"}}},
		},
		Positions: map[string]DataModelPosition{},
	}
}

func TestValidateDataModelAcceptsFlatModels(t *testing.T) {
	model := validDataModelFixture()
	model.Positions[model.Entities[0].ID] = DataModelPosition{X: 20, Y: 40}
	model.Entities[0].Cardinality = DataModelEntityCardinalitySingleton
	if err := ValidateDataModel(model); err != nil {
		t.Fatalf("ValidateDataModel() error = %v", err)
	}
}

func TestValidateDataModelRejectsInvalidStructure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DataModel)
	}{
		{name: "unsupported format version", mutate: func(model *DataModel) { model.FormatVersion = 2 }},
		{name: "invalid entity ID", mutate: func(model *DataModel) { model.Entities[0].ID = "not-a-uuid" }},
		{name: "nil entity UUID", mutate: func(model *DataModel) { model.Entities[0].ID = uuid.Nil.String() }},
		{name: "non-canonical entity UUID", mutate: func(model *DataModel) { model.Entities[0].ID = strings.ToUpper(model.Entities[0].ID) }},
		{name: "duplicate entity ID", mutate: func(model *DataModel) { model.Entities[1].ID = model.Entities[0].ID }},
		{name: "duplicate entity key", mutate: func(model *DataModel) { model.Entities[1].Key = model.Entities[0].Key }},
		{name: "invalid entity key", mutate: func(model *DataModel) { model.Entities[0].Key = "Product Name" }},
		{name: "blank entity name", mutate: func(model *DataModel) { model.Entities[0].Name = " \t" }},
		{name: "overlong entity name", mutate: func(model *DataModel) { model.Entities[0].Name = strings.Repeat("x", 121) }},
		{name: "unsupported entity cardinality", mutate: func(model *DataModel) { model.Entities[0].Cardinality = "exactly-three" }},
		{name: "field ID collides with entity ID", mutate: func(model *DataModel) { model.Entities[0].Fields[0].ID = model.Entities[0].ID }},
		{name: "duplicate field ID", mutate: func(model *DataModel) { model.Entities[1].Fields[0].ID = model.Entities[0].Fields[0].ID }},
		{name: "invalid field ID", mutate: func(model *DataModel) { model.Entities[0].Fields[0].ID = "field-id" }},
		{name: "reserved field key", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Key = "id" }},
		{name: "duplicate field key", mutate: func(model *DataModel) { model.Entities[0].Fields[1].Key = model.Entities[0].Fields[0].Key }},
		{name: "invalid field key", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Key = "slug-name" }},
		{name: "unsupported field type", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Type = "json" }},
		{name: "enum without options", mutate: func(model *DataModel) { model.Entities[0].Fields[1].Options = nil }},
		{name: "duplicate enum option", mutate: func(model *DataModel) {
			model.Entities[0].Fields[1].Options[1] = model.Entities[0].Fields[1].Options[0]
		}},
		{name: "position for unknown entity", mutate: func(model *DataModel) { model.Positions[uuid.NewString()] = DataModelPosition{} }},
		{name: "negative position", mutate: func(model *DataModel) { model.Positions[model.Entities[0].ID] = DataModelPosition{X: -1} }},
		{name: "non-finite position", mutate: func(model *DataModel) { model.Positions[model.Entities[0].ID] = DataModelPosition{X: math.NaN()} }},
		{name: "out-of-range position", mutate: func(model *DataModel) { model.Positions[model.Entities[0].ID] = DataModelPosition{X: 100001} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := validDataModelFixture()
			test.mutate(&model)
			if err := ValidateDataModel(model); err == nil {
				t.Fatal("ValidateDataModel() error = nil, want invalid model error")
			}
		})
	}
}

func TestDataModelRejectsNonEmptyLegacyRelations(t *testing.T) {
	raw := `{"formatVersion":1,"entities":[],"relations":[{"id":"` + uuid.NewString() + `"}]}`
	var model DataModel
	err := json.Unmarshal([]byte(raw), &model)
	if !errors.Is(err, ErrLegacyModelRelations) {
		t.Fatalf("json.Unmarshal() error = %v, want ErrLegacyModelRelations", err)
	}
}

func TestDataModelAcceptsAndOmitsEmptyLegacyRelations(t *testing.T) {
	for _, relations := range []string{`[]`, `null`} {
		raw := `{"formatVersion":1,"entities":[],"relations":` + relations + `}`
		var model DataModel
		if err := json.Unmarshal([]byte(raw), &model); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		encoded, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"relations"`) {
			t.Fatalf("marshaled model returned relations: %s", encoded)
		}
	}
}

func TestModelContentRecordAcceptsAndOmitsEmptyLegacyRelations(t *testing.T) {
	raw := `{"id":"` + uuid.NewString() + `","entityId":"` + uuid.NewString() + `","sortOrder":0,"values":{},"relations":{}}`
	var record ModelContentRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"relations"`) {
		t.Fatalf("marshaled record returned relations: %s", encoded)
	}
	raw = `{"id":"` + uuid.NewString() + `","entityId":"` + uuid.NewString() + `","sortOrder":0,"values":{},"relations":{"` + uuid.NewString() + `":[]}}`
	if err := json.Unmarshal([]byte(raw), &record); !errors.Is(err, ErrLegacyModelRelations) {
		t.Fatalf("json.Unmarshal() error = %v, want ErrLegacyModelRelations", err)
	}
}

func TestParseSiteConfigRejectsLegacyRelations(t *testing.T) {
	raw := `{"formatVersion":3,"pages":[{"id":"page","name":"Page","kind":"page","placements":[]}],"routeTree":{"id":"root","type":"site","children":[]},"dataModel":{"formatVersion":1,"entities":[],"relations":[{"id":"` + uuid.NewString() + `"}]}}`
	_, err := ParseSiteConfig([]byte(raw))
	if !errors.Is(err, ErrLegacyModelRelations) {
		t.Fatalf("ParseSiteConfig() error = %v, want ErrLegacyModelRelations", err)
	}
}

func TestParseSiteConfigAcceptsEmptyLegacyRelationsAndRejectsUnknownModelFields(t *testing.T) {
	base := `{"formatVersion":3,"pages":[{"id":"page","name":"Page","kind":"page","placements":[]}],"routeTree":{"id":"root","type":"site","children":[{"id":"route","type":"path","path":"/","pageId":"page","children":[]}]},"dataModel":{"formatVersion":1,"entities":[]%s}}`
	if _, err := ParseSiteConfig([]byte(fmt.Sprintf(base, `,"relations":[]`))); err != nil {
		t.Fatalf("ParseSiteConfig() rejected empty legacy relations: %v", err)
	}
	if _, err := ParseSiteConfig([]byte(fmt.Sprintf(base, `,"unexpected":true`))); err == nil {
		t.Fatal("ParseSiteConfig() accepted an unknown data model field")
	}
}
