package sitebuilder

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type modelContentFixture struct {
	model        DataModel
	entityID     string
	slugID       string
	titleID      string
	quantityID   string
	ratingID     string
	statusID     string
	imageID      string
	productAID   string
	productBID   string
	placements   []ModelContentPlacement
	records      []ModelContentRecord
	selections   []ModelContentSelection
	categoryName string
}

func newModelContentFixture() modelContentFixture {
	fixture := modelContentFixture{
		entityID: uuid.NewString(), slugID: uuid.NewString(), titleID: uuid.NewString(),
		quantityID: uuid.NewString(), ratingID: uuid.NewString(), statusID: uuid.NewString(),
		imageID: uuid.NewString(), productAID: uuid.NewString(), productBID: uuid.NewString(),
	}
	fixture.model = DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: fixture.entityID, Key: "product", Name: "Product",
		Fields: []DataModelField{
			{ID: fixture.slugID, Key: "slug", Name: "Slug", Type: "text", Unique: true},
			{ID: fixture.titleID, Key: "title", Name: "Title", Type: "text"},
			{ID: fixture.quantityID, Key: "quantity", Name: "Quantity", Type: "integer"},
			{ID: fixture.ratingID, Key: "rating", Name: "Rating", Type: "number", Unique: true},
			{ID: fixture.statusID, Key: "status", Name: "Status", Type: "enum", Options: []string{"draft", "live"}},
			{ID: fixture.imageID, Key: "image", Name: "Image", Type: "media"},
		},
	}}}
	fixture.placements = []ModelContentPlacement{
		{
			ID: "catalog",
			Ports: []DataInputPort{{Key: "products", Kind: "collection", Fields: []DataInputField{
				{Path: "/slug", Type: "text"}, {Path: "/title", Type: "text"}, {Path: "/status", Type: "enum", Values: []string{"draft", "live"}},
			}}},
			Bindings: []DataInputBinding{{
				InputKey: "products", EntityID: fixture.entityID,
				Fields: []DataInputFieldBinding{
					{InputPath: "/slug", FieldID: fixture.slugID},
					{InputPath: "/title", FieldID: fixture.titleID},
					{InputPath: "/status", FieldID: fixture.statusID},
				},
			}},
		},
		{
			ID:       "rating",
			Ports:    []DataInputPort{{Key: "rating", Kind: "scalar", Type: "number"}},
			Bindings: []DataInputBinding{{InputKey: "rating", EntityID: fixture.entityID, FieldID: fixture.ratingID}},
		},
		{
			ID:    "detail",
			Ports: []DataInputPort{{Key: "product", Kind: "record", Fields: []DataInputField{{Path: "/title", Type: "text"}}}},
			Bindings: []DataInputBinding{{
				InputKey: "product", EntityID: fixture.entityID,
				Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fixture.titleID}},
				Lookup: &DataInputLookupBinding{Argument: "slug", FieldID: fixture.slugID},
			}},
			ArgumentBindings: map[string]ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}},
		},
	}
	fixture.records = []ModelContentRecord{
		{
			ID: fixture.productAID, EntityID: fixture.entityID, SortOrder: 0,
			Values: map[string]any{
				"slug": "product-a", "title": "Product A", "quantity": json.Number("4"), "rating": json.Number("4.5"),
				"status": "live", "image": uuid.NewString(),
			},
		},
		{ID: fixture.productBID, EntityID: fixture.entityID, SortOrder: 1, Values: map[string]any{"slug": "product-b", "title": "Product B", "rating": float64(2), "status": "draft"}},
	}
	fixture.selections = []ModelContentSelection{
		{PlacementID: "catalog", InputKey: "products", Mode: "ids", RecordIDs: []string{fixture.productBID, fixture.productAID}},
		{PlacementID: "rating", InputKey: "rating", Mode: "ids", RecordIDs: []string{fixture.productAID}},
	}
	return fixture
}

func TestModelContentValidationAcceptsFlatContent(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelContentFixture)
	}{
		{name: "typed values bindings and ordered selections", mutate: func(*modelContentFixture) {}},
		{name: "empty model and workspace", mutate: func(f *modelContentFixture) {
			f.model = DataModel{FormatVersion: 1}
			f.placements, f.records, f.selections = nil, nil, nil
		}},
		{name: "nil records and selections", mutate: func(f *modelContentFixture) { f.records, f.selections = nil, nil }},
		{name: "empty optional values", mutate: func(f *modelContentFixture) {
			f.records[0].Values = map[string]any{"slug": "", "status": "", "rating": nil}
			f.records[1].Values["slug"] = ""
		}},
		{name: "all collection input", mutate: func(f *modelContentFixture) {
			f.selections = []ModelContentSelection{{PlacementID: "catalog", InputKey: "products", Mode: "all"}}
		}},
		{name: "empty ordered collection", mutate: func(f *modelContentFixture) {
			f.selections = []ModelContentSelection{{PlacementID: "catalog", InputKey: "products", Mode: "ids", RecordIDs: []string{}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelContentFixture()
			test.mutate(&fixture)
			if err := ValidateModelContent(fixture.model, fixture.placements, fixture.records, fixture.selections); err != nil {
				t.Fatalf("ValidateModelContent() error = %v", err)
			}
		})
	}
}

func TestModelContentRejectsInvalidRecordsAndValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelContentFixture)
	}{
		{name: "invalid record ID", mutate: func(f *modelContentFixture) { f.records[0].ID = "not-a-uuid" }},
		{name: "duplicate record ID", mutate: func(f *modelContentFixture) { f.records[1].ID = f.records[0].ID }},
		{name: "unknown entity", mutate: func(f *modelContentFixture) { f.records[0].EntityID = uuid.NewString() }},
		{name: "negative sort order", mutate: func(f *modelContentFixture) { f.records[0].SortOrder = -1 }},
		{name: "unknown field", mutate: func(f *modelContentFixture) { f.records[0].Values["unknown"] = "value" }},
		{name: "wrong text type", mutate: func(f *modelContentFixture) { f.records[0].Values["title"] = 7 }},
		{name: "fractional integer", mutate: func(f *modelContentFixture) { f.records[0].Values["quantity"] = 2.5 }},
		{name: "non-finite number", mutate: func(f *modelContentFixture) { f.records[0].Values["rating"] = math.NaN() }},
		{name: "invalid enum", mutate: func(f *modelContentFixture) { f.records[0].Values["status"] = "archived" }},
		{name: "invalid media ID", mutate: func(f *modelContentFixture) { f.records[0].Values["image"] = "media-key" }},
		{name: "duplicate unique text", mutate: func(f *modelContentFixture) { f.records[1].Values["slug"] = f.records[0].Values["slug"] }},
		{name: "duplicate unique number", mutate: func(f *modelContentFixture) { f.records[1].Values["rating"] = float64(4.5) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelContentFixture()
			test.mutate(&fixture)
			if err := ValidateModelContent(fixture.model, fixture.placements, fixture.records, fixture.selections); err == nil {
				t.Fatal("ValidateModelContent() error = nil, want invalid content error")
			}
		})
	}
}

func TestModelContentRejectsInvalidPlacementsAndSelections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelContentFixture)
	}{
		{name: "duplicate placement", mutate: func(f *modelContentFixture) { f.placements[1].ID = f.placements[0].ID }},
		{name: "missing binding", mutate: func(f *modelContentFixture) { f.placements[0].Bindings = nil }},
		{name: "unknown placement", mutate: func(f *modelContentFixture) { f.selections[0].PlacementID = "missing" }},
		{name: "unknown input", mutate: func(f *modelContentFixture) { f.selections[0].InputKey = "missing" }},
		{name: "duplicate selection", mutate: func(f *modelContentFixture) { f.selections = append(f.selections, f.selections[0]) }},
		{name: "invalid mode", mutate: func(f *modelContentFixture) { f.selections[0].Mode = "lookup" }},
		{name: "all on scalar", mutate: func(f *modelContentFixture) { f.selections[1].Mode = "all" }},
		{name: "all with IDs", mutate: func(f *modelContentFixture) { f.selections[0].Mode = "all" }},
		{name: "duplicate selected ID", mutate: func(f *modelContentFixture) { f.selections[0].RecordIDs = []string{f.productAID, f.productAID} }},
		{name: "missing selected record", mutate: func(f *modelContentFixture) { f.selections[0].RecordIDs = []string{uuid.NewString()} }},
		{name: "scalar requires one ID", mutate: func(f *modelContentFixture) { f.selections[1].RecordIDs = nil }},
		{name: "lookup cannot select", mutate: func(f *modelContentFixture) {
			f.selections = append(f.selections, ModelContentSelection{PlacementID: "detail", InputKey: "product", Mode: "ids", RecordIDs: []string{f.productAID}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelContentFixture()
			test.mutate(&fixture)
			if err := ValidateModelContent(fixture.model, fixture.placements, fixture.records, fixture.selections); err == nil {
				t.Fatal("ValidateModelContent() error = nil, want invalid placement or selection")
			}
		})
	}
}

func TestModelContentSingletonCardinalityAndSelection(t *testing.T) {
	entityID, fieldID, recordID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: entityID, Key: "settings", Name: "Settings", Cardinality: DataModelEntityCardinalitySingleton,
		Fields: []DataModelField{{ID: fieldID, Key: "title", Name: "Title", Type: "text"}},
	}}}
	record := ModelContentRecord{ID: recordID, EntityID: entityID, Values: map[string]any{"title": "Home"}}
	if err := validateModelContentEntityCardinality(model, []ModelContentRecord{record}, true); err != nil {
		t.Fatal(err)
	}
	if err := validateModelContentEntityCardinality(model, nil, true); err == nil {
		t.Fatal("missing singleton draft was accepted")
	}
	placements := []ModelContentPlacement{{ID: "home", Ports: []DataInputPort{{Key: "settings", Kind: "collection", Fields: []DataInputField{{Path: "/title", Type: "text"}}}}, Bindings: []DataInputBinding{{
		InputKey: "settings", EntityID: entityID, Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fieldID}},
	}}}}
	if err := ValidateModelContent(model, placements, []ModelContentRecord{record}, []ModelContentSelection{{PlacementID: "home", InputKey: "settings", Mode: "ids", RecordIDs: []string{recordID}}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelContent(model, placements, []ModelContentRecord{record}, []ModelContentSelection{{PlacementID: "home", InputKey: "settings", Mode: "ids", RecordIDs: []string{recordID, recordID}}}); err == nil {
		t.Fatal("duplicate singleton selection was accepted")
	}
}

func TestModelContentSelectionOrderIsStable(t *testing.T) {
	fixture := newModelContentFixture()
	want := append([]string(nil), fixture.selections[0].RecordIDs...)
	if err := ValidateModelContent(fixture.model, fixture.placements, fixture.records, fixture.selections); err != nil {
		t.Fatal(err)
	}
	if strings.Join(fixture.selections[0].RecordIDs, ",") != strings.Join(want, ",") {
		t.Fatal("selection order changed")
	}
}
