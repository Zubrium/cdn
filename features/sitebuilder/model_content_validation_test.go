package sitebuilder

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type modelContentFixture struct {
	model                       DataModel
	productEntityID             string
	categoryEntityID            string
	productAID                  string
	productBID                  string
	categoryAID                 string
	categoryBID                 string
	slugFieldID                 string
	titleFieldID                string
	quantityFieldID             string
	ratingFieldID               string
	enabledFieldID              string
	dateFieldID                 string
	dateTimeFieldID             string
	urlFieldID                  string
	emailFieldID                string
	statusFieldID               string
	imageFieldID                string
	categoryNameFieldID         string
	primaryCategoryRelationID   string
	productCategoriesRelationID string
	relatedProductsRelationID   string
	placements                  []ModelContentPlacement
	records                     []ModelContentRecord
	selections                  []ModelContentSelection
}

func newModelContentFixture() modelContentFixture {
	fixture := modelContentFixture{
		productEntityID:             uuid.NewString(),
		categoryEntityID:            uuid.NewString(),
		productAID:                  uuid.NewString(),
		productBID:                  uuid.NewString(),
		categoryAID:                 uuid.NewString(),
		categoryBID:                 uuid.NewString(),
		slugFieldID:                 uuid.NewString(),
		titleFieldID:                uuid.NewString(),
		quantityFieldID:             uuid.NewString(),
		ratingFieldID:               uuid.NewString(),
		enabledFieldID:              uuid.NewString(),
		dateFieldID:                 uuid.NewString(),
		dateTimeFieldID:             uuid.NewString(),
		urlFieldID:                  uuid.NewString(),
		emailFieldID:                uuid.NewString(),
		statusFieldID:               uuid.NewString(),
		imageFieldID:                uuid.NewString(),
		categoryNameFieldID:         uuid.NewString(),
		primaryCategoryRelationID:   uuid.NewString(),
		productCategoriesRelationID: uuid.NewString(),
		relatedProductsRelationID:   uuid.NewString(),
	}
	fixture.model = DataModel{
		FormatVersion: 1,
		Entities: []DataModelEntity{
			{
				ID: fixture.productEntityID, Key: "product", Name: "Product",
				Fields: []DataModelField{
					{ID: fixture.slugFieldID, Key: "slug", Name: "Slug", Type: "text", Unique: true},
					{ID: fixture.titleFieldID, Key: "title", Name: "Title", Type: "text"},
					{ID: fixture.quantityFieldID, Key: "quantity", Name: "Quantity", Type: "integer"},
					{ID: fixture.ratingFieldID, Key: "rating", Name: "Rating", Type: "number", Unique: true},
					{ID: fixture.enabledFieldID, Key: "enabled", Name: "Enabled", Type: "boolean"},
					{ID: fixture.dateFieldID, Key: "date", Name: "Date", Type: "date"},
					{ID: fixture.dateTimeFieldID, Key: "date_time", Name: "Date time", Type: "dateTime"},
					{ID: fixture.urlFieldID, Key: "url", Name: "URL", Type: "url"},
					{ID: fixture.emailFieldID, Key: "email", Name: "Email", Type: "email"},
					{ID: fixture.statusFieldID, Key: "status", Name: "Status", Type: "enum", Options: []string{"draft", "live"}},
					{ID: fixture.imageFieldID, Key: "image", Name: "Image", Type: "media"},
				},
			},
			{
				ID: fixture.categoryEntityID, Key: "category", Name: "Category",
				Fields: []DataModelField{{ID: fixture.categoryNameFieldID, Key: "name", Name: "Name", Type: "text"}},
			},
		},
		Relations: []DataModelRelation{
			{ID: fixture.primaryCategoryRelationID, Name: "Primary category", FromEntityID: fixture.productEntityID, ToEntityID: fixture.categoryEntityID, Cardinality: "one-to-one"},
			{ID: fixture.productCategoriesRelationID, Name: "Categories", FromEntityID: fixture.productEntityID, ToEntityID: fixture.categoryEntityID, Cardinality: "one-to-many"},
			{ID: fixture.relatedProductsRelationID, Name: "Related products", FromEntityID: fixture.productEntityID, ToEntityID: fixture.productEntityID, Cardinality: "many-to-many"},
		},
	}

	collectionPort := DataInputPort{
		Key: "products", Kind: "collection",
		Fields: []DataInputField{
			{Path: "/slug", Type: "text"},
			{Path: "/title", Type: "text"},
			{Path: "/status", Type: "enum", Values: []string{"draft", "live"}},
		},
	}
	scalarPort := DataInputPort{Key: "rating", Kind: "scalar", Type: "number"}
	recordPort := DataInputPort{Key: "product", Kind: "record", Fields: []DataInputField{{Path: "/title", Type: "text"}}}
	fixture.placements = []ModelContentPlacement{
		{
			ID: "catalog-placement", Ports: []DataInputPort{collectionPort},
			Bindings: []DataInputBinding{{
				InputKey: "products", EntityID: fixture.productEntityID,
				Fields: []DataInputFieldBinding{
					{InputPath: "/slug", FieldID: fixture.slugFieldID},
					{InputPath: "/title", FieldID: fixture.titleFieldID},
					{InputPath: "/status", FieldID: fixture.statusFieldID},
				},
			}},
		},
		{
			ID: "rating-placement", Ports: []DataInputPort{scalarPort},
			Bindings: []DataInputBinding{{InputKey: "rating", EntityID: fixture.productEntityID, FieldID: fixture.ratingFieldID}},
		},
		{
			ID: "detail-placement", Ports: []DataInputPort{recordPort},
			Bindings: []DataInputBinding{{
				InputKey: "product", EntityID: fixture.productEntityID,
				Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fixture.titleFieldID}},
				Lookup: &DataInputLookupBinding{Argument: "slug", FieldID: fixture.slugFieldID},
			}},
			ArgumentBindings: map[string]ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}},
		},
	}
	fixture.records = []ModelContentRecord{
		{
			ID: fixture.productAID, EntityID: fixture.productEntityID, SortOrder: 0,
			Values: map[string]any{
				"slug": "product-a", "title": "Product A", "quantity": json.Number("4"), "rating": json.Number("4.5"),
				"enabled": true, "date": "2026-11-07", "date_time": "2026-11-07T10:30:00Z",
				"url": "https://example.test/product-a", "email": "sales@example.test", "status": "live", "image": uuid.NewString(),
			},
			Relations: map[string][]string{
				fixture.primaryCategoryRelationID:   {fixture.categoryAID},
				fixture.productCategoriesRelationID: {fixture.categoryAID},
				fixture.relatedProductsRelationID:   {fixture.productBID},
			},
		},
		{
			ID: fixture.productBID, EntityID: fixture.productEntityID, SortOrder: 1,
			Values: map[string]any{"slug": "product-b", "title": "Product B", "rating": float64(2), "status": "draft"},
			Relations: map[string][]string{
				fixture.primaryCategoryRelationID:   {fixture.categoryBID},
				fixture.productCategoriesRelationID: {fixture.categoryBID},
				fixture.relatedProductsRelationID:   {fixture.productAID},
			},
		},
		{
			ID: fixture.categoryAID, EntityID: fixture.categoryEntityID, SortOrder: 0,
			Values: map[string]any{"name": "Category A"},
		},
		{
			ID: fixture.categoryBID, EntityID: fixture.categoryEntityID, SortOrder: 1,
			Values: map[string]any{"name": "Category B"},
		},
	}
	fixture.selections = []ModelContentSelection{
		{PlacementID: "catalog-placement", InputKey: "products", Mode: "ids", RecordIDs: []string{fixture.productBID, fixture.productAID}},
		{PlacementID: "rating-placement", InputKey: "rating", Mode: "ids", RecordIDs: []string{fixture.productAID}},
	}
	return fixture
}

func TestModelContentValidationAcceptsValidAndEmptyContent(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelContentFixture)
	}{
		{name: "typed values relations bindings and ordered selections", mutate: func(*modelContentFixture) {}},
		{name: "nil workspace with empty valid model", mutate: func(f *modelContentFixture) {
			f.model = DataModel{FormatVersion: 1}
			f.placements = nil
			f.records = nil
			f.selections = nil
		}},
		{name: "nil records values relations and selections", mutate: func(f *modelContentFixture) {
			f.records = nil
			f.selections = nil
		}},
		{name: "empty optional values and equal nonnegative sort order", mutate: func(f *modelContentFixture) {
			f.records[0].Values = map[string]any{"slug": "", "status": "", "email": "", "rating": nil}
			f.records[1].Values["slug"] = ""
			f.records[1].SortOrder = 0
		}},
		{name: "explicit all and empty ordered collection", mutate: func(f *modelContentFixture) {
			f.selections = []ModelContentSelection{{PlacementID: "catalog-placement", InputKey: "products", Mode: "all"}}
		}},
		{name: "explicit empty collection IDs", mutate: func(f *modelContentFixture) {
			f.selections = []ModelContentSelection{{PlacementID: "catalog-placement", InputKey: "products", Mode: "ids", RecordIDs: []string{}}}
		}},
		{name: "one-to-many source can reference multiple targets", mutate: func(f *modelContentFixture) {
			f.records[0].Relations[f.productCategoriesRelationID] = []string{f.categoryAID, f.categoryBID}
			delete(f.records[1].Relations, f.productCategoriesRelationID)
		}},
		{name: "missing selections produce empty static inputs", mutate: func(f *modelContentFixture) {
			f.selections = nil
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

func TestModelContentEntityCardinalityAllowsOnlyOneSingletonRecord(t *testing.T) {
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: uuid.NewString(), Key: "site_settings", Name: "Site settings", Cardinality: DataModelEntityCardinalitySingleton,
	}}}
	one := ModelContentRecord{ID: uuid.NewString(), EntityID: model.Entities[0].ID}
	if err := validateModelContentEntityCardinality(model, []ModelContentRecord{one}, true); err != nil {
		t.Fatalf("one singleton draft row should be valid: %v", err)
	}
	if err := validateModelContentEntityCardinality(model, nil, false); err != nil {
		t.Fatalf("an unpublished singleton may have no published row: %v", err)
	}
	if err := validateModelContentEntityCardinality(model, nil, true); err == nil {
		t.Fatal("singleton draft without a row must be rejected")
	}
	duplicate := one
	duplicate.ID = uuid.NewString()
	if err := validateModelContentEntityCardinality(model, []ModelContentRecord{one, duplicate}, false); err == nil {
		t.Fatal("duplicate singleton rows must be rejected in every state")
	}
}

func TestSingletonCollectionSelectionAcceptsZeroOrOneRecordOnly(t *testing.T) {
	entityID, fieldID, recordID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: entityID, Key: "site_settings", Name: "Site settings", Cardinality: DataModelEntityCardinalitySingleton,
		Fields: []DataModelField{{ID: fieldID, Key: "title", Name: "Title", Type: "text"}},
	}}}
	port := DataInputPort{Key: "settings", Kind: "collection", Fields: []DataInputField{{Path: "/title", Type: "text"}}}
	placements := []ModelContentPlacement{{ID: "home", Ports: []DataInputPort{port}, Bindings: []DataInputBinding{{
		InputKey: "settings", EntityID: entityID, Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fieldID}},
	}}}}
	records := []ModelContentRecord{{ID: recordID, EntityID: entityID, Values: map[string]any{"title": "Home"}}}
	for name, selection := range map[string]ModelContentSelection{
		"all":  {PlacementID: "home", InputKey: "settings", Mode: "all"},
		"none": {PlacementID: "home", InputKey: "settings", Mode: "ids", RecordIDs: []string{}},
		"one":  {PlacementID: "home", InputKey: "settings", Mode: "ids", RecordIDs: []string{recordID}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateModelContent(model, placements, records, []ModelContentSelection{selection}); err != nil {
				t.Fatalf("valid singleton collection selection rejected: %v", err)
			}
		})
	}
	for name, ids := range map[string][]string{
		"two":      {recordID, uuid.NewString()},
		"repeated": {recordID, recordID},
	} {
		t.Run(name, func(t *testing.T) {
			selection := ModelContentSelection{PlacementID: "home", InputKey: "settings", Mode: "ids", RecordIDs: ids}
			if err := ValidateModelContent(model, placements, records, []ModelContentSelection{selection}); err == nil {
				t.Fatal("singleton collection selection with multiple IDs must be rejected")
			}
		})
	}
}

func TestModelContentValidationRejectsInvalidRecordsAndValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelContentFixture)
	}{
		{name: "invalid record ID", mutate: func(f *modelContentFixture) { f.records[0].ID = "not-a-uuid" }},
		{name: "nil record ID", mutate: func(f *modelContentFixture) { f.records[0].ID = uuid.Nil.String() }},
		{name: "non-canonical record ID", mutate: func(f *modelContentFixture) { f.records[0].ID = strings.ToUpper(f.records[0].ID) }},
		{name: "duplicate record ID", mutate: func(f *modelContentFixture) { f.records[1].ID = f.records[0].ID }},
		{name: "unknown record entity", mutate: func(f *modelContentFixture) { f.records[0].EntityID = uuid.NewString() }},
		{name: "negative sort order", mutate: func(f *modelContentFixture) { f.records[0].SortOrder = -1 }},
		{name: "unknown field key", mutate: func(f *modelContentFixture) { f.records[0].Values["unknown"] = "value" }},
		{name: "wrong text type", mutate: func(f *modelContentFixture) { f.records[0].Values["title"] = 7 }},
		{name: "fractional integer", mutate: func(f *modelContentFixture) { f.records[0].Values["quantity"] = 2.5 }},
		{name: "non-finite number", mutate: func(f *modelContentFixture) { f.records[0].Values["rating"] = math.NaN() }},
		{name: "wrong boolean type", mutate: func(f *modelContentFixture) { f.records[0].Values["enabled"] = "true" }},
		{name: "invalid date", mutate: func(f *modelContentFixture) { f.records[0].Values["date"] = "07/11/2026" }},
		{name: "invalid date-time", mutate: func(f *modelContentFixture) { f.records[0].Values["date_time"] = "2026-11-07 10:30:00" }},
		{name: "invalid URI", mutate: func(f *modelContentFixture) { f.records[0].Values["url"] = "not a url" }},
		{name: "invalid email", mutate: func(f *modelContentFixture) { f.records[0].Values["email"] = "not an email" }},
		{name: "invalid enum", mutate: func(f *modelContentFixture) { f.records[0].Values["status"] = "archived" }},
		{name: "invalid media ID", mutate: func(f *modelContentFixture) { f.records[0].Values["image"] = "media-key" }},
		{name: "duplicate explicit unique value", mutate: func(f *modelContentFixture) { f.records[1].Values["slug"] = f.records[0].Values["slug"] }},
		{name: "duplicate numeric unique value", mutate: func(f *modelContentFixture) { f.records[1].Values["rating"] = float64(4.5) }},
		{name: "model enum option not accepted by input", mutate: func(f *modelContentFixture) { f.placements[0].Ports[0].Fields[2].Values = []string{"live"} }},
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

func TestModelContentValidationRejectsInvalidRelations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelContentFixture)
	}{
		{name: "unknown relation ID", mutate: func(f *modelContentFixture) { f.records[0].Relations[uuid.NewString()] = []string{f.categoryAID} }},
		{name: "relation on wrong source entity", mutate: func(f *modelContentFixture) {
			f.records[2].Relations = map[string][]string{f.primaryCategoryRelationID: {f.categoryAID}}
		}},
		{name: "missing target record", mutate: func(f *modelContentFixture) {
			f.records[0].Relations[f.primaryCategoryRelationID] = []string{uuid.NewString()}
		}},
		{name: "wrong target entity", mutate: func(f *modelContentFixture) {
			f.records[0].Relations[f.primaryCategoryRelationID] = []string{f.productBID}
		}},
		{name: "duplicate target ID", mutate: func(f *modelContentFixture) {
			f.records[0].Relations[f.productCategoriesRelationID] = []string{f.categoryAID, f.categoryAID}
		}},
		{name: "one-to-one source has multiple targets", mutate: func(f *modelContentFixture) {
			f.records[0].Relations[f.primaryCategoryRelationID] = []string{f.categoryAID, f.categoryBID}
		}},
		{name: "one-to-one target has multiple sources", mutate: func(f *modelContentFixture) {
			f.records[1].Relations[f.primaryCategoryRelationID] = []string{f.categoryAID}
		}},
		{name: "one-to-many target has multiple sources", mutate: func(f *modelContentFixture) {
			f.records[1].Relations[f.productCategoriesRelationID] = []string{f.categoryAID}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelContentFixture()
			test.mutate(&fixture)
			if err := ValidateModelContent(fixture.model, fixture.placements, fixture.records, fixture.selections); err == nil {
				t.Fatal("ValidateModelContent() error = nil, want invalid relation error")
			}
		})
	}
}

func TestModelContentValidationRejectsInvalidPlacementsAndSelections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelContentFixture)
	}{
		{name: "duplicate placement ID", mutate: func(f *modelContentFixture) { f.placements[1].ID = f.placements[0].ID }},
		{name: "missing port binding", mutate: func(f *modelContentFixture) { f.placements[0].Bindings = nil }},
		{name: "selection unknown placement", mutate: func(f *modelContentFixture) { f.selections[0].PlacementID = "missing" }},
		{name: "selection unknown input", mutate: func(f *modelContentFixture) { f.selections[0].InputKey = "missing" }},
		{name: "duplicate selection", mutate: func(f *modelContentFixture) { f.selections = append(f.selections, f.selections[0]) }},
		{name: "unsupported selection mode", mutate: func(f *modelContentFixture) { f.selections[0].Mode = "lookup" }},
		{name: "all only for collection", mutate: func(f *modelContentFixture) { f.selections[1].Mode = "all" }},
		{name: "all cannot list IDs", mutate: func(f *modelContentFixture) {
			f.selections[0].Mode = "all"
			f.selections[0].RecordIDs = []string{f.productAID}
		}},
		{name: "duplicate selected ID", mutate: func(f *modelContentFixture) { f.selections[0].RecordIDs = []string{f.productAID, f.productAID} }},
		{name: "selection missing record", mutate: func(f *modelContentFixture) { f.selections[0].RecordIDs = []string{uuid.NewString()} }},
		{name: "selection wrong entity", mutate: func(f *modelContentFixture) { f.selections[0].RecordIDs = []string{f.categoryAID} }},
		{name: "scalar requires exactly one ID", mutate: func(f *modelContentFixture) { f.selections[1].RecordIDs = nil }},
		{name: "record lookup cannot also select", mutate: func(f *modelContentFixture) {
			f.selections = append(f.selections, ModelContentSelection{PlacementID: "detail-placement", InputKey: "product", Mode: "ids", RecordIDs: []string{f.productAID}})
		}},
		{name: "selection input binding wrong type", mutate: func(f *modelContentFixture) { f.placements[1].Bindings[0].FieldID = f.titleFieldID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModelContentFixture()
			test.mutate(&fixture)
			if err := ValidateModelContent(fixture.model, fixture.placements, fixture.records, fixture.selections); err == nil {
				t.Fatal("ValidateModelContent() error = nil, want invalid placement or selection error")
			}
		})
	}
}

func TestModelContentValidationPreservesSelectionOrder(t *testing.T) {
	fixture := newModelContentFixture()
	want := append([]string(nil), fixture.selections[0].RecordIDs...)
	if err := ValidateModelContent(fixture.model, fixture.placements, fixture.records, fixture.selections); err != nil {
		t.Fatalf("ValidateModelContent() error = %v", err)
	}
	for index := range want {
		if fixture.selections[0].RecordIDs[index] != want[index] {
			t.Fatalf("selection order changed at index %d: got %q, want %q", index, fixture.selections[0].RecordIDs[index], want[index])
		}
	}
}
