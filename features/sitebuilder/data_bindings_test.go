package sitebuilder

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

type typedBindingFixture struct {
	model            DataModel
	ports            []DataInputPort
	bindings         []DataInputBinding
	argumentBindings map[string]ArgumentBinding
	productID        string
	slugID           string
	titleID          string
	priceID          string
	imageID          string
	categoryID       string
	categoryNameID   string
}

func newTypedBindingFixture() typedBindingFixture {
	fixture := typedBindingFixture{
		productID:      uuid.NewString(),
		slugID:         uuid.NewString(),
		titleID:        uuid.NewString(),
		priceID:        uuid.NewString(),
		imageID:        uuid.NewString(),
		categoryID:     uuid.NewString(),
		categoryNameID: uuid.NewString(),
		argumentBindings: map[string]ArgumentBinding{
			"slug": {Source: "path", PathArg: "slug"},
		},
	}
	fixture.model = DataModel{
		FormatVersion: 1,
		Entities: []DataModelEntity{
			{
				ID: fixture.productID, Key: "product", Name: "Product",
				Fields: []DataModelField{
					{ID: fixture.slugID, Key: "slug", Name: "Slug", Type: "text", Unique: true},
					{ID: fixture.titleID, Key: "title", Name: "Title", Type: "text"},
					{ID: fixture.priceID, Key: "price", Name: "Price", Type: "number"},
					{ID: fixture.imageID, Key: "image", Name: "Image", Type: "media"},
				},
			},
			{
				ID: fixture.categoryID, Key: "category", Name: "Category",
				Fields: []DataModelField{{ID: fixture.categoryNameID, Key: "name", Name: "Name", Type: "text"}},
			},
		},
	}
	fixture.ports = []DataInputPort{
		{Key: "badge", Kind: "scalar", Type: "text"},
		{Key: "product", Kind: "record", Fields: []DataInputField{
			{Path: "/title", Type: "text"},
			{Path: "/price", Type: "number"},
			{Path: "/image", Type: "media"},
		}},
		{Key: "featured", Kind: "collection", Fields: []DataInputField{
			{Path: "/slug", Type: "text"},
			{Path: "/image", Type: "media"},
		}},
	}
	fixture.bindings = []DataInputBinding{
		{InputKey: "badge", EntityID: fixture.productID, FieldID: fixture.titleID},
		{
			InputKey: "product", EntityID: fixture.productID,
			Fields: []DataInputFieldBinding{
				{InputPath: "/title", FieldID: fixture.titleID},
				{InputPath: "/price", FieldID: fixture.priceID},
				{InputPath: "/image", FieldID: fixture.imageID},
			},
			Lookup: &DataInputLookupBinding{Argument: "slug", FieldID: fixture.slugID},
		},
		{
			InputKey: "featured", EntityID: fixture.productID,
			Fields: []DataInputFieldBinding{
				{InputPath: "/slug", FieldID: fixture.slugID},
				{InputPath: "/image", FieldID: fixture.imageID},
			},
		},
	}
	return fixture
}

func TestValidateDataInputBindingsAcceptsTypedBindings(t *testing.T) {
	tests := []struct {
		name     string
		ports    func(typedBindingFixture) []DataInputPort
		bindings func(typedBindingFixture) []DataInputBinding
		args     func(typedBindingFixture) map[string]ArgumentBinding
	}{
		{
			name:     "no declarations preserve legacy payloads",
			ports:    func(typedBindingFixture) []DataInputPort { return nil },
			bindings: func(typedBindingFixture) []DataInputBinding { return nil },
			args:     func(typedBindingFixture) map[string]ArgumentBinding { return nil },
		},
		{
			name:     "scalar field id binding",
			ports:    func(f typedBindingFixture) []DataInputPort { return f.ports[:1] },
			bindings: func(f typedBindingFixture) []DataInputBinding { return f.bindings[:1] },
			args:     func(typedBindingFixture) map[string]ArgumentBinding { return nil },
		},
		{
			name:     "record path mappings with path lookup",
			ports:    func(f typedBindingFixture) []DataInputPort { return f.ports[1:2] },
			bindings: func(f typedBindingFixture) []DataInputBinding { return f.bindings[1:2] },
			args:     func(f typedBindingFixture) map[string]ArgumentBinding { return f.argumentBindings },
		},
		{
			name:  "record selected from content without lookup",
			ports: func(f typedBindingFixture) []DataInputPort { return f.ports[1:2] },
			bindings: func(f typedBindingFixture) []DataInputBinding {
				binding := f.bindings[1]
				binding.Lookup = nil
				return []DataInputBinding{binding}
			},
			args: func(typedBindingFixture) map[string]ArgumentBinding { return nil },
		},
		{
			name:     "collection path mappings",
			ports:    func(f typedBindingFixture) []DataInputPort { return f.ports[2:] },
			bindings: func(f typedBindingFixture) []DataInputBinding { return f.bindings[2:] },
			args:     func(typedBindingFixture) map[string]ArgumentBinding { return nil },
		},
		{
			name:     "all declared ports",
			ports:    func(f typedBindingFixture) []DataInputPort { return f.ports },
			bindings: func(f typedBindingFixture) []DataInputBinding { return f.bindings },
			args:     func(f typedBindingFixture) map[string]ArgumentBinding { return f.argumentBindings },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTypedBindingFixture()
			if err := ValidateDataInputBindings(fixture.model, test.ports(fixture), test.bindings(fixture), test.args(fixture)); err != nil {
				t.Fatalf("ValidateDataInputBindings() error = %v", err)
			}
		})
	}
}

func TestValidateDataInputBindingsRejectsInvalidBindings(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*typedBindingFixture)
		wantError string
	}{
		{
			name: "bindings without ports",
			mutate: func(f *typedBindingFixture) {
				f.ports = nil
			},
		},
		{
			name: "missing port binding",
			mutate: func(f *typedBindingFixture) {
				f.bindings = f.bindings[:2]
			},
		},
		{
			name: "unexpected input key",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].InputKey = "unknown"
			},
		},
		{
			name: "duplicate input binding",
			mutate: func(f *typedBindingFixture) {
				f.bindings = append(f.bindings, f.bindings[0])
			},
		},
		{
			name: "duplicate port key",
			mutate: func(f *typedBindingFixture) {
				f.ports[1].Key = f.ports[0].Key
			},
		},
		{
			name:      "unknown entity",
			wantError: "references unknown entity",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].EntityID = uuid.NewString()
			},
		},
		{
			name: "scalar missing field id",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].FieldID = ""
			},
		},
		{
			name:      "scalar unknown field id",
			wantError: "references unknown field",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].FieldID = uuid.NewString()
			},
		},
		{
			name: "scalar field from another entity",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].FieldID = f.categoryNameID
			},
		},
		{
			name: "scalar type mismatch",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].FieldID = f.priceID
			},
		},
		{
			name: "scalar cannot use fields mapping",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].Fields = []DataInputFieldBinding{{InputPath: "/title", FieldID: f.titleID}}
			},
		},
		{
			name: "scalar cannot use route lookup",
			mutate: func(f *typedBindingFixture) {
				f.bindings[0].Lookup = &DataInputLookupBinding{Argument: "slug", FieldID: f.slugID}
			},
		},
		{
			name: "composite cannot use scalar field id",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].FieldID = f.titleID
			},
		},
		{
			name: "composite missing field mappings",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Fields = nil
			},
		},
		{
			name:      "missing declared path",
			wantError: "is missing a mapping",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Fields = f.bindings[1].Fields[:2]
			},
		},
		{
			name: "duplicate mapped path",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Fields = append(f.bindings[1].Fields, f.bindings[1].Fields[0])
			},
		},
		{
			name: "extra mapped path",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Fields = append(f.bindings[1].Fields, DataInputFieldBinding{InputPath: "/unexpected", FieldID: f.titleID})
			},
		},
		{
			name:      "unknown mapped field id",
			wantError: "references unknown field",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Fields[0].FieldID = uuid.NewString()
			},
		},
		{
			name: "mapped field belongs to another entity",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Fields[0].FieldID = f.categoryNameID
			},
		},
		{
			name: "mapped field type mismatch",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Fields[0].FieldID = f.priceID
			},
		},
		{
			name:      "array path needs an explicit relation projection",
			wantError: "array wildcards",
			mutate: func(f *typedBindingFixture) {
				f.ports[1].Fields[0].Path = "/gallery/*/title"
				f.bindings[1].Fields[0].InputPath = "/gallery/*/title"
			},
		},
		{
			name: "lookup on collection",
			mutate: func(f *typedBindingFixture) {
				f.bindings[2].Lookup = &DataInputLookupBinding{Argument: "slug", FieldID: f.slugID}
			},
		},
		{
			name: "lookup argument missing",
			mutate: func(f *typedBindingFixture) {
				delete(f.argumentBindings, "slug")
			},
		},
		{
			name: "lookup query argument",
			mutate: func(f *typedBindingFixture) {
				f.argumentBindings["slug"] = ArgumentBinding{Source: "query", QueryKey: "slug"}
			},
		},
		{
			name: "lookup path binding lacks capture",
			mutate: func(f *typedBindingFixture) {
				f.argumentBindings["slug"] = ArgumentBinding{Source: "path"}
			},
		},
		{
			name: "lookup invalid argument key",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Lookup.Argument = "bad-key"
			},
		},
		{
			name: "lookup unknown field",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Lookup.FieldID = uuid.NewString()
			},
		},
		{
			name: "lookup field must be unique",
			mutate: func(f *typedBindingFixture) {
				f.bindings[1].Lookup.FieldID = f.titleID
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTypedBindingFixture()
			test.mutate(&fixture)
			err := ValidateDataInputBindings(fixture.model, fixture.ports, fixture.bindings, fixture.argumentBindings)
			if err == nil {
				t.Fatal("ValidateDataInputBindings() error = nil, want invalid binding error")
			}
			if test.wantError != "" && !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateDataInputBindings() error = %v, want message containing %q", err, test.wantError)
			}
		})
	}
}

func TestValidateDataInputBindingsSupportsDirectedRelationPaths(t *testing.T) {
	productID, categoryID, brandID, reviewID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	categoryRelationID, brandRelationID, reviewsRelationID, reverseCategoryRelationID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	productTitleID, categoryNameID, brandNameID, reviewTitleID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	model := DataModel{FormatVersion: 1,
		Entities: []DataModelEntity{
			{ID: productID, Key: "product", Name: "Product", Fields: []DataModelField{{ID: productTitleID, Key: "title", Name: "Title", Type: "text"}}},
			{ID: categoryID, Key: "category", Name: "Category", Fields: []DataModelField{{ID: categoryNameID, Key: "name", Name: "Name", Type: "text"}}},
			{ID: brandID, Key: "brand", Name: "Brand", Fields: []DataModelField{{ID: brandNameID, Key: "name", Name: "Name", Type: "text"}}},
			{ID: reviewID, Key: "review", Name: "Review", Fields: []DataModelField{{ID: reviewTitleID, Key: "title", Name: "Title", Type: "text"}}},
		},
		Relations: []DataModelRelation{
			{ID: categoryRelationID, Name: "Category", FromEntityID: productID, ToEntityID: categoryID, Cardinality: "one-to-one"},
			{ID: reverseCategoryRelationID, Name: "Reverse products", FromEntityID: categoryID, ToEntityID: productID, Cardinality: "one-to-one"},
			{ID: brandRelationID, Name: "Brand", FromEntityID: categoryID, ToEntityID: brandID, Cardinality: "one-to-one"},
			{ID: reviewsRelationID, Name: "Reviews", FromEntityID: productID, ToEntityID: reviewID, Cardinality: "one-to-many"},
		},
	}
	make := func(path, fieldID string, relationIDs ...string) error {
		return ValidateDataInputBindings(model,
			[]DataInputPort{{Key: "product", Kind: "record", Fields: []DataInputField{{Path: path, Type: "text"}}}},
			[]DataInputBinding{{InputKey: "product", EntityID: productID, Fields: []DataInputFieldBinding{{InputPath: path, FieldID: fieldID, RelationIDs: relationIDs}}}}, nil)
	}
	for name, test := range map[string]struct {
		path, fieldID string
		relations     []string
		wantError     string
	}{
		"direct field":      {path: "/title", fieldID: productTitleID},
		"one-to-one":        {path: "/category/name", fieldID: categoryNameID, relations: []string{categoryRelationID}},
		"nested one-to-one": {path: "/category/brand/name", fieldID: brandNameID, relations: []string{categoryRelationID, brandRelationID}},
		"one-to-many":       {path: "/reviews/*/title", fieldID: reviewTitleID, relations: []string{reviewsRelationID}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := make(test.path, test.fieldID, test.relations...); err != nil {
				t.Fatalf("valid relation binding rejected: %v", err)
			}
		})
	}
	for name, test := range map[string]struct {
		path, fieldID string
		relations     []string
		wantError     string
	}{
		"missing wildcard":                {path: "/reviews/title", fieldID: reviewTitleID, relations: []string{reviewsRelationID}, wantError: "array wildcards"},
		"wildcard without multi relation": {path: "/category/*/name", fieldID: categoryNameID, relations: []string{categoryRelationID}, wantError: "array wildcards"},
		"reversed edge":                   {path: "/category/title", fieldID: productTitleID, relations: []string{reverseCategoryRelationID}, wantError: "does not start at entity"},
		"field outside path target":       {path: "/category/title", fieldID: productTitleID, relations: []string{categoryRelationID}, wantError: "references unknown field"},
		"unknown relation":                {path: "/category/name", fieldID: categoryNameID, relations: []string{uuid.NewString()}, wantError: "unknown relation"},
	} {
		t.Run(name, func(t *testing.T) {
			err := make(test.path, test.fieldID, test.relations...)
			if err == nil {
				t.Fatal("invalid relation binding was accepted")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("invalid relation binding error = %v, want message containing %q", err, test.wantError)
			}
		})
	}
}

func TestValidateDataInputBindingsChecksModelEnumOptionsAgainstFeatureContract(t *testing.T) {
	tests := []struct {
		name           string
		kind           string
		modelOptions   []string
		declaredValues []string
		wantError      string
	}{
		{
			name: "scalar accepts a narrower model enum",
			kind: "scalar", modelOptions: []string{"draft", "published"},
			declaredValues: []string{"draft", "published", "scheduled"},
		},
		{
			name: "scalar rejects an undeclared model option",
			kind: "scalar", modelOptions: []string{"draft", "archived"},
			declaredValues: []string{"draft", "published"}, wantError: `model enum option "archived" is not declared`,
		},
		{
			name: "scalar requires declared enum values",
			kind: "scalar", modelOptions: []string{"draft"}, wantError: "enum values are required",
		},
		{
			name: "record field accepts a narrower model enum",
			kind: "record", modelOptions: []string{"draft"},
			declaredValues: []string{"draft", "published"},
		},
		{
			name: "record field rejects an undeclared model option",
			kind: "record", modelOptions: []string{"draft", "archived"},
			declaredValues: []string{"draft", "published"}, wantError: `inputPath "/status" model enum option "archived" is not declared`,
		},
		{
			name: "record field requires declared enum values",
			kind: "record", modelOptions: []string{"draft"}, wantError: "enum values are required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTypedBindingFixture()
			statusID := uuid.NewString()
			fixture.model.Entities[0].Fields = append(fixture.model.Entities[0].Fields, DataModelField{
				ID: statusID, Key: "status", Name: "Status", Type: "enum", Options: test.modelOptions,
			})

			var port DataInputPort
			var binding DataInputBinding
			if test.kind == "scalar" {
				port = DataInputPort{Key: "status", Kind: "scalar", Type: "enum", Values: test.declaredValues}
				binding = DataInputBinding{InputKey: port.Key, EntityID: fixture.productID, FieldID: statusID}
			} else {
				port = DataInputPort{Key: "status", Kind: "record", Fields: []DataInputField{{
					Path: "/status", Type: "enum", Values: test.declaredValues,
				}}}
				binding = DataInputBinding{InputKey: port.Key, EntityID: fixture.productID, Fields: []DataInputFieldBinding{{
					InputPath: "/status", FieldID: statusID,
				}}}
			}

			err := ValidateDataInputBindings(fixture.model, []DataInputPort{port}, []DataInputBinding{binding}, nil)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateDataInputBindings() rejected a model enum subset: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateDataInputBindings() error = %v, want message containing %q", err, test.wantError)
			}
		})
	}
}
