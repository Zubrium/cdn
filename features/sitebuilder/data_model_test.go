package sitebuilder

import (
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validDataModelFixture() DataModel {
	productID := uuid.NewString()
	categoryID := uuid.NewString()
	slugID := uuid.NewString()
	statusID := uuid.NewString()
	descriptionID := uuid.NewString()
	categoryNameID := uuid.NewString()
	return DataModel{
		FormatVersion: 1,
		Entities: []DataModelEntity{
			{
				ID: productID, Key: "product", Name: "Product",
				Fields: []DataModelField{
					{ID: slugID, Key: "slug", Name: "Slug", Type: "text", Unique: true},
					{ID: statusID, Key: "status", Name: "Status", Type: "enum", Options: []string{"draft", "published"}},
					{ID: descriptionID, Key: "description", Name: "Description", Type: "longText"},
				},
			},
			{
				ID: categoryID, Key: "category", Name: "Category",
				Fields: []DataModelField{{ID: categoryNameID, Key: "name", Name: "Name", Type: "text"}},
			},
		},
		Relations: []DataModelRelation{
			{ID: uuid.NewString(), Name: "Primary category", FromEntityID: productID, ToEntityID: categoryID, Cardinality: "one-to-one"},
			{ID: uuid.NewString(), Name: "Related categories", FromEntityID: productID, ToEntityID: categoryID, Cardinality: "one-to-many"},
			{ID: uuid.NewString(), Name: "Recommended by", FromEntityID: categoryID, ToEntityID: productID, Cardinality: "many-to-many"},
		},
		Positions: map[string]DataModelPosition{
			productID:  {X: 20, Y: 40},
			categoryID: {X: 420, Y: 40},
		},
	}
}

func TestValidateDataModelAcceptsValidModels(t *testing.T) {
	tests := []struct {
		name  string
		model DataModel
	}{
		{name: "empty versioned model", model: DataModel{FormatVersion: 1}},
		{name: "entities fields relations and positions", model: validDataModelFixture()},
		{name: "many entity cardinality", model: func() DataModel {
			model := validDataModelFixture()
			model.Entities[0].Cardinality = DataModelEntityCardinalityMany
			return model
		}()},
		{name: "singleton entity cardinality", model: func() DataModel {
			model := validDataModelFixture()
			model.Entities[0].Cardinality = DataModelEntityCardinalitySingleton
			return model
		}()},
		{name: "same field key in separate entities", model: func() DataModel {
			model := validDataModelFixture()
			model.Entities[0].Fields[0].Key = "name"
			return model
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateDataModel(test.model); err != nil {
				t.Fatalf("ValidateDataModel() error = %v", err)
			}
		})
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
		{name: "duplicate field ID across entities", mutate: func(model *DataModel) { model.Entities[1].Fields[0].ID = model.Entities[0].Fields[0].ID }},
		{name: "invalid field ID", mutate: func(model *DataModel) { model.Entities[0].Fields[0].ID = "field-id" }},
		{name: "reserved generated record id key", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Key = "id" }},
		{name: "duplicate field key", mutate: func(model *DataModel) { model.Entities[0].Fields[1].Key = model.Entities[0].Fields[0].Key }},
		{name: "invalid field key", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Key = "slug-name" }},
		{name: "blank field name", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Name = "" }},
		{name: "unsupported field type", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Type = "json" }},
		{name: "unique unsupported field type", mutate: func(model *DataModel) { model.Entities[0].Fields[2].Unique = true }},
		{name: "enum without options", mutate: func(model *DataModel) { model.Entities[0].Fields[1].Options = nil }},
		{name: "empty enum option", mutate: func(model *DataModel) { model.Entities[0].Fields[1].Options[0] = " " }},
		{name: "duplicate enum option", mutate: func(model *DataModel) {
			model.Entities[0].Fields[1].Options[1] = model.Entities[0].Fields[1].Options[0]
		}},
		{name: "options on non-enum field", mutate: func(model *DataModel) { model.Entities[0].Fields[0].Options = []string{"one"} }},
		{name: "relation ID collides with field ID", mutate: func(model *DataModel) { model.Relations[0].ID = model.Entities[0].Fields[0].ID }},
		{name: "duplicate relation ID", mutate: func(model *DataModel) { model.Relations[1].ID = model.Relations[0].ID }},
		{name: "invalid relation ID", mutate: func(model *DataModel) { model.Relations[0].ID = "relation-id" }},
		{name: "blank relation name", mutate: func(model *DataModel) { model.Relations[0].Name = " " }},
		{name: "unknown relation source", mutate: func(model *DataModel) { model.Relations[0].FromEntityID = uuid.NewString() }},
		{name: "unknown relation target", mutate: func(model *DataModel) { model.Relations[0].ToEntityID = "unknown" }},
		{name: "unsupported cardinality", mutate: func(model *DataModel) { model.Relations[0].Cardinality = "many-to-one" }},
		{name: "position for unknown entity", mutate: func(model *DataModel) { model.Positions[uuid.NewString()] = DataModelPosition{} }},
		{name: "negative position", mutate: func(model *DataModel) { model.Positions[model.Entities[0].ID] = DataModelPosition{X: -1, Y: 0} }},
		{name: "non-finite position", mutate: func(model *DataModel) { model.Positions[model.Entities[0].ID] = DataModelPosition{X: math.NaN(), Y: 0} }},
		{name: "out-of-range position", mutate: func(model *DataModel) { model.Positions[model.Entities[0].ID] = DataModelPosition{X: 100001, Y: 0} }},
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
