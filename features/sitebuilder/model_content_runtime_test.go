package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	modelRuntimeEntityID = "00000000-0000-4000-8000-000000000001"
	modelRuntimeSlugID   = "00000000-0000-4000-8000-000000000002"
	modelRuntimeTitleID  = "00000000-0000-4000-8000-000000000003"
	modelRuntimeAID      = "00000000-0000-4000-8000-00000000000a"
	modelRuntimeBID      = "00000000-0000-4000-8000-00000000000b"
)

type publicMediaArgumentRow struct{ readyPublic bool }

func (row publicMediaArgumentRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("expected one destination")
	}
	value, ok := dest[0].(*bool)
	if !ok {
		return fmt.Errorf("expected a boolean destination")
	}
	*value = row.readyPublic
	return nil
}

type publicMediaArgumentQuery struct {
	readyPublic map[uuid.UUID]bool
	calls       int
}

func (query *publicMediaArgumentQuery) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	query.calls++
	id, _ := args[0].(uuid.UUID)
	return publicMediaArgumentRow{readyPublic: query.readyPublic[id]}
}

func TestProjectModelContentSharesCollectionAndHonorsPlacementSelection(t *testing.T) {
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: modelRuntimeEntityID, Key: "product", Name: "Товар", Fields: []DataModelField{
			{ID: modelRuntimeSlugID, Key: "slug", Name: "Адрес", Type: "text", Unique: true},
			{ID: modelRuntimeTitleID, Key: "title", Name: "Название", Type: "text"},
		},
	}}}
	manifest := Manifest{DataInputs: []DataInputPort{{Key: "products", Kind: "collection", Fields: []DataInputField{{Path: "/slug", Type: "text"}, {Path: "/title", Type: "text"}}}}}
	makePlacement := func(id string) Placement {
		return Placement{ID: id, DataBindings: []DataInputBinding{{
			InputKey: "products", EntityID: modelRuntimeEntityID,
			Fields: []DataInputFieldBinding{{InputPath: "/slug", FieldID: modelRuntimeSlugID}, {InputPath: "/title", FieldID: modelRuntimeTitleID}},
		}}}
	}
	records := []ModelContentRecord{
		{ID: modelRuntimeAID, EntityID: modelRuntimeEntityID, SortOrder: 2, Values: map[string]any{"slug": "pear", "title": "Груша"}},
		{ID: modelRuntimeBID, EntityID: modelRuntimeEntityID, SortOrder: 1, Values: map[string]any{"slug": "apple", "title": "Яблоко"}},
	}

	catalog, _, _, err := projectModelContent(model, manifest, makePlacement("catalog"), json.RawMessage(`{}`), records, nil)
	if err != nil {
		t.Fatalf("catalog projection: %v", err)
	}
	var catalogContent struct {
		Products []map[string]any `json:"products"`
	}
	if err := json.Unmarshal(catalog, &catalogContent); err != nil {
		t.Fatalf("decode catalog projection: %v", err)
	}
	if len(catalogContent.Products) != 2 || catalogContent.Products[0]["id"] != modelRuntimeBID || catalogContent.Products[0]["title"] != "Яблоко" || catalogContent.Products[1]["id"] != modelRuntimeAID {
		t.Fatalf("catalog should expose the shared entity in editorial order, got %s", catalog)
	}

	slider, _, _, err := projectModelContent(model, manifest, makePlacement("slider"), json.RawMessage(`{}`), records, []ModelContentSelection{{
		PlacementID: "slider", InputKey: "products", Mode: "ids", RecordIDs: []string{modelRuntimeAID, modelRuntimeBID},
	}})
	if err != nil {
		t.Fatalf("slider projection: %v", err)
	}
	var sliderContent struct {
		Products []map[string]any `json:"products"`
	}
	if err := json.Unmarshal(slider, &sliderContent); err != nil {
		t.Fatalf("decode slider projection: %v", err)
	}
	if len(sliderContent.Products) != 2 || sliderContent.Products[0]["id"] != modelRuntimeAID || sliderContent.Products[1]["id"] != modelRuntimeBID {
		t.Fatalf("slider should use its own ordered selection of the same records, got %s", slider)
	}
}

func TestProjectModelContentResolvesRecordFromPublishedUniqueRouteField(t *testing.T) {
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: modelRuntimeEntityID, Key: "product", Name: "Товар", Fields: []DataModelField{
			{ID: modelRuntimeSlugID, Key: "slug", Name: "Адрес", Type: "text", Unique: true},
			{ID: modelRuntimeTitleID, Key: "title", Name: "Название", Type: "text"},
		},
	}}}
	manifest := Manifest{DataInputs: []DataInputPort{{Key: "product", Kind: "record", Fields: []DataInputField{{Path: "/slug", Type: "text"}, {Path: "/title", Type: "text"}}}}}
	placement := Placement{ID: "detail", Arguments: map[string]ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}}, DataBindings: []DataInputBinding{{
		InputKey: "product", EntityID: modelRuntimeEntityID,
		Fields:    []DataInputFieldBinding{{InputPath: "/slug", FieldID: modelRuntimeSlugID}, {InputPath: "/title", FieldID: modelRuntimeTitleID}},
		Lookup:    &DataInputLookupBinding{Argument: "slug", FieldID: modelRuntimeSlugID},
		DetailSEO: &DetailSEOFieldBindings{TitleFieldID: modelRuntimeTitleID},
	}}}
	records := []ModelContentRecord{{ID: modelRuntimeAID, EntityID: modelRuntimeEntityID, Values: map[string]any{"slug": "pear", "title": "Груша"}}}

	content, _, detailSEO, err := projectModelContent(model, manifest, placement, json.RawMessage(`{"slug":"pear"}`), records, nil)
	if err != nil {
		t.Fatalf("record projection: %v", err)
	}
	var result map[string]map[string]any
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatalf("decode record projection: %v", err)
	}
	if result["product"]["id"] != modelRuntimeAID || result["product"]["title"] != "Груша" || string(detailSEO) != `{"title":"Груша"}` {
		t.Fatalf("route lookup should project the matching record and SEO source, got content=%s seo=%s", content, detailSEO)
	}

	_, _, _, err = projectModelContent(model, manifest, placement, json.RawMessage(`{"slug":"missing"}`), records, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unresolved published detail slug must be a not-found error, got %v", err)
	}
	_, _, _, err = projectModelContent(model, manifest, placement, json.RawMessage(`{}`), records, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing detail lookup argument must not select an empty field, got %v", err)
	}
}

func TestModelContentReadIndexPreservesLookupOrderAndEntityBoundaries(t *testing.T) {
	const otherEntityID = "00000000-0000-4000-8000-000000000099"
	records := []ModelContentRecord{
		{ID: modelRuntimeAID, EntityID: modelRuntimeEntityID, SortOrder: 2, Values: map[string]any{"slug": "shared"}},
		{ID: modelRuntimeBID, EntityID: modelRuntimeEntityID, SortOrder: 1, Values: map[string]any{"slug": "shared"}},
		{ID: "00000000-0000-4000-8000-00000000000c", EntityID: otherEntityID, SortOrder: 0, Values: map[string]any{"slug": "shared"}},
	}
	index := newModelContentReadIndex(records)

	if record := index.recordByLookup(modelRuntimeEntityID, "slug", "shared"); record == nil || record.ID != modelRuntimeBID {
		t.Fatalf("lookup should preserve the established sort-order winner for duplicate non-unique values, got %+v", record)
	}
	if record := index.recordByID(modelRuntimeEntityID, "00000000-0000-4000-8000-00000000000c"); record != nil {
		t.Fatalf("record ID lookup crossed entity boundary: %+v", record)
	}
	if actual := index.recordsForEntity(modelRuntimeEntityID); len(actual) != 2 || actual[0].ID != modelRuntimeBID || actual[1].ID != modelRuntimeAID {
		t.Fatalf("indexed collection order differs from the existing stable ordering: %+v", actual)
	}
}

func BenchmarkProjectModelContentDetailLookup50K(b *testing.B) {
	const (
		entityID = "00000000-0000-4000-8000-000000000041"
		slugID   = "00000000-0000-4000-8000-000000000042"
		titleID  = "00000000-0000-4000-8000-000000000043"
	)
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: entityID, Key: "product", Name: "Product", Fields: []DataModelField{
			{ID: slugID, Key: "slug", Name: "Slug", Type: "text", Unique: true},
			{ID: titleID, Key: "title", Name: "Title", Type: "text"},
		},
	}}}
	manifest := Manifest{DataInputs: []DataInputPort{{
		Key: "product", Kind: "record", Fields: []DataInputField{{Path: "/slug", Type: "text"}, {Path: "/title", Type: "text"}},
	}}}
	placement := Placement{ID: "detail", DataBindings: []DataInputBinding{{
		InputKey: "product", EntityID: entityID,
		Fields: []DataInputFieldBinding{{InputPath: "/slug", FieldID: slugID}, {InputPath: "/title", FieldID: titleID}},
		Lookup: &DataInputLookupBinding{Argument: "slug", FieldID: slugID},
	}}}
	const recordCount = 50_000
	records := make([]ModelContentRecord, recordCount)
	for i := range records {
		records[i] = ModelContentRecord{
			ID: fmt.Sprintf("product-%05d", i), EntityID: entityID, SortOrder: i,
			Values: map[string]any{"slug": fmt.Sprintf("product-%05d", i), "title": fmt.Sprintf("Product %05d", i)},
		}
	}
	readIndex := newModelContentReadIndex(records)
	if readIndex.recordByLookup(entityID, "slug", "product-49999") == nil {
		b.Fatal("benchmark fixture is missing the last product lookup")
	}
	arguments := json.RawMessage(`{"slug":"product-49999"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, _, err := projectModelContentWithIndex(model, manifest, placement, arguments, records, nil, readIndex); err != nil {
			b.Fatal(err)
		}
	}
}

func TestProjectModelContentAutomaticallyResolvesSingletonInputs(t *testing.T) {
	const (
		entityID = "00000000-0000-4000-8000-000000000031"
		fieldID  = "00000000-0000-4000-8000-000000000032"
		recordID = "00000000-0000-4000-8000-000000000033"
	)
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: entityID, Key: "site_settings", Name: "Site settings", Cardinality: DataModelEntityCardinalitySingleton,
		Fields: []DataModelField{{ID: fieldID, Key: "title", Name: "Title", Type: "text"}},
	}}}
	manifest := Manifest{DataInputs: []DataInputPort{
		{Key: "siteTitle", Kind: "scalar", Type: "text"},
		{Key: "settings", Kind: "record", Fields: []DataInputField{{Path: "/title", Type: "text"}}},
		{Key: "settingsList", Kind: "collection", Fields: []DataInputField{{Path: "/title", Type: "text"}}},
	}}
	placement := Placement{ID: "home", DataBindings: []DataInputBinding{
		{InputKey: "siteTitle", EntityID: entityID, FieldID: fieldID},
		{InputKey: "settings", EntityID: entityID, Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fieldID}}},
		{InputKey: "settingsList", EntityID: entityID, Fields: []DataInputFieldBinding{{InputPath: "/title", FieldID: fieldID}}},
	}}
	record := ModelContentRecord{ID: recordID, EntityID: entityID, Values: map[string]any{"title": "Herbal shop"}}
	content, _, _, err := projectModelContent(model, manifest, placement, json.RawMessage(`{}`), []ModelContentRecord{record}, nil)
	if err != nil {
		t.Fatalf("singleton runtime projection: %v", err)
	}
	var projected map[string]any
	if err := json.Unmarshal(content, &projected); err != nil {
		t.Fatalf("decode projected singleton: %v", err)
	}
	if projected["siteTitle"] != "Herbal shop" || projected["settings"].(map[string]any)["title"] != "Herbal shop" {
		t.Fatalf("scalar and record inputs should resolve the singleton automatically: %s", content)
	}
	items := projected["settingsList"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != recordID {
		t.Fatalf("singleton collection must remain an array with one record: %s", content)
	}

	empty, _, _, err := projectModelContent(model, manifest, placement, json.RawMessage(`{}`), nil, nil)
	if err != nil {
		t.Fatalf("unpublished singleton should have empty runtime outputs: %v", err)
	}
	var emptyProjected map[string]any
	if err := json.Unmarshal(empty, &emptyProjected); err != nil {
		t.Fatalf("decode empty singleton projection: %v", err)
	}
	if _, exists := emptyProjected["siteTitle"]; exists || len(emptyProjected["settingsList"].([]any)) != 0 || len(emptyProjected["settings"].(map[string]any)) != 0 {
		t.Fatalf("unpublished singleton outputs must omit scalar, empty record and empty array: %s", empty)
	}
	detail := placement
	detail.DataBindings = append([]DataInputBinding(nil), placement.DataBindings...)
	detail.DataBindings[1].Lookup = &DataInputLookupBinding{Argument: "slug", FieldID: fieldID}
	emptyDetail, _, _, err := projectModelContent(model, manifest, detail, json.RawMessage(`{"slug":"shop"}`), nil, nil)
	if err != nil {
		t.Fatalf("an unpublished singleton with route lookup should project an empty record: %v", err)
	}
	var detailContent map[string]any
	if err := json.Unmarshal(emptyDetail, &detailContent); err != nil {
		t.Fatalf("decode empty singleton route lookup: %v", err)
	}
	detailRecord, ok := detailContent["settings"].(map[string]any)
	if !ok || len(detailRecord) != 0 {
		t.Fatalf("unpublished singleton route lookup should resolve to an empty object: %s", emptyDetail)
	}

	duplicate := record
	duplicate.ID = "00000000-0000-4000-8000-000000000034"
	if _, _, _, err := projectModelContent(model, manifest, placement, json.RawMessage(`{}`), []ModelContentRecord{record, duplicate}, nil); err == nil {
		t.Fatal("runtime must fail closed when published singleton state has duplicate rows")
	}
}

func TestProjectDetailSEOFieldsUsesOnlyValuesFromSelectedRecord(t *testing.T) {
	entity := DataModelEntity{ID: modelRuntimeEntityID, Fields: []DataModelField{
		{ID: modelRuntimeTitleID, Key: "title", Type: "text"},
		{ID: modelRuntimeSlugID, Key: "description", Type: "longText"},
	}}
	record := ModelContentRecord{ID: modelRuntimeAID, EntityID: modelRuntimeEntityID, Values: map[string]any{"title": " Опубликовано ", "description": "Описание"}}
	seo, err := projectDetailSEOFields(entity, record, DetailSEOFieldBindings{TitleFieldID: modelRuntimeTitleID, DescriptionFieldID: modelRuntimeSlugID})
	if err != nil {
		t.Fatalf("project detail SEO fields: %v", err)
	}
	if string(seo) != `{"description":"Описание","title":"Опубликовано"}` {
		t.Fatalf("SEO projection should include configured, non-empty fields from this record only, got %s", seo)
	}
	empty, err := projectDetailSEOFields(entity, ModelContentRecord{Values: map[string]any{}}, DetailSEOFieldBindings{TitleFieldID: modelRuntimeTitleID})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty published value should preserve metadata fallback, projection=%s err=%v", empty, err)
	}
}

func TestPublicMediaIDsFromArgumentSchemaFindsNestedAndArrayReferences(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"logo":{"type":"string","format":"media"},"gallery":{"type":"array","items":{"type":"object","properties":{"image":{"type":"string","format":"media"}}}}}}`)
	data := json.RawMessage(`{"logo":"00000000-0000-4000-8000-000000000001","gallery":[{"image":"00000000-0000-4000-8000-000000000002"},{"image":""}]}`)
	ids, err := publicMediaIDsFromSchema(schema, data)
	if err != nil {
		t.Fatalf("collect nested media IDs: %v", err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id.String()] = true
	}
	if len(got) != 2 || !got["00000000-0000-4000-8000-000000000001"] || !got["00000000-0000-4000-8000-000000000002"] {
		t.Fatalf("nested and array media references were not collected: %v", got)
	}
}

func TestPublicMediaIDsFromArgumentSchemaRejectsNonMediaIDs(t *testing.T) {
	_, err := publicMediaIDsFromSchema(json.RawMessage(`{"type":"string","format":"media"}`), json.RawMessage(`"private-file"`))
	if err == nil {
		t.Fatal("non-UUID media arguments must be rejected before public projection")
	}
}

func TestValidatePublicMediaArgumentsRejectsPrivateOrUnavailableFiles(t *testing.T) {
	id, err := uuid.Parse("00000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(`{"type":"string","format":"media"}`)
	data := json.RawMessage(`"00000000-0000-4000-8000-000000000001"`)
	public := &publicMediaArgumentQuery{readyPublic: map[uuid.UUID]bool{id: true}}
	if err := validatePublicMediaArguments(context.Background(), public, schema, data); err != nil {
		t.Fatalf("ready public media argument was rejected: %v", err)
	}
	if public.calls != 1 {
		t.Fatalf("expected one visibility lookup, got %d", public.calls)
	}
	private := &publicMediaArgumentQuery{readyPublic: map[uuid.UUID]bool{}}
	if err := validatePublicMediaArguments(context.Background(), private, schema, data); err == nil {
		t.Fatal("private or unavailable media argument must not reach public runtime")
	}
}

func TestProjectModelContentTraversesOneToOneAndNestedCollections(t *testing.T) {
	const (
		productID          = "00000000-0000-4000-8000-000000000101"
		categoryID         = "00000000-0000-4000-8000-000000000102"
		reviewID           = "00000000-0000-4000-8000-000000000103"
		tagID              = "00000000-0000-4000-8000-000000000104"
		productTitleID     = "00000000-0000-4000-8000-000000000105"
		categoryNameID     = "00000000-0000-4000-8000-000000000106"
		reviewBodyID       = "00000000-0000-4000-8000-000000000107"
		tagLabelID         = "00000000-0000-4000-8000-000000000108"
		categoryRelationID = "00000000-0000-4000-8000-000000000109"
		reviewsRelationID  = "00000000-0000-4000-8000-000000000110"
		tagsRelationID     = "00000000-0000-4000-8000-000000000111"
		productRecordID    = "00000000-0000-4000-8000-000000000112"
		categoryRecordID   = "00000000-0000-4000-8000-000000000113"
		reviewOneID        = "00000000-0000-4000-8000-000000000114"
		reviewTwoID        = "00000000-0000-4000-8000-000000000115"
		tagOneID           = "00000000-0000-4000-8000-000000000116"
		tagTwoID           = "00000000-0000-4000-8000-000000000117"
	)
	model := DataModel{FormatVersion: 1,
		Entities: []DataModelEntity{
			{ID: productID, Key: "product", Name: "Product", Fields: []DataModelField{{ID: productTitleID, Key: "title", Name: "Title", Type: "text"}}},
			{ID: categoryID, Key: "category", Name: "Category", Fields: []DataModelField{{ID: categoryNameID, Key: "name", Name: "Name", Type: "text"}}},
			{ID: reviewID, Key: "review", Name: "Review", Fields: []DataModelField{{ID: reviewBodyID, Key: "body", Name: "Body", Type: "text"}}},
			{ID: tagID, Key: "tag", Name: "Tag", Fields: []DataModelField{{ID: tagLabelID, Key: "label", Name: "Label", Type: "text"}}},
		},
		Relations: []DataModelRelation{
			{ID: categoryRelationID, Name: "Category", FromEntityID: productID, ToEntityID: categoryID, Cardinality: "one-to-one"},
			{ID: reviewsRelationID, Name: "Reviews", FromEntityID: productID, ToEntityID: reviewID, Cardinality: "one-to-many"},
			{ID: tagsRelationID, Name: "Tags", FromEntityID: reviewID, ToEntityID: tagID, Cardinality: "many-to-many"},
		},
	}
	manifest := Manifest{DataInputs: []DataInputPort{
		{Key: "products", Kind: "collection", Fields: []DataInputField{
			{Path: "/title", Type: "text"},
			{Path: "/category/name", Type: "text"},
			{Path: "/reviews/*/body", Type: "text"},
			{Path: "/reviews/*/tags/*/label", Type: "text"},
		}},
		{Key: "badge", Kind: "scalar", Type: "text"},
	}}
	placement := Placement{ID: "catalog", DataBindings: []DataInputBinding{
		{InputKey: "products", EntityID: productID, Fields: []DataInputFieldBinding{
			{InputPath: "/title", FieldID: productTitleID},
			{InputPath: "/category/name", FieldID: categoryNameID, RelationIDs: []string{categoryRelationID}},
			{InputPath: "/reviews/*/body", FieldID: reviewBodyID, RelationIDs: []string{reviewsRelationID}},
			{InputPath: "/reviews/*/tags/*/label", FieldID: tagLabelID, RelationIDs: []string{reviewsRelationID, tagsRelationID}},
		}},
		{InputKey: "badge", EntityID: productID, FieldID: categoryNameID, RelationIDs: []string{categoryRelationID}},
	}}
	records := []ModelContentRecord{
		{ID: productRecordID, EntityID: productID, Values: map[string]any{"title": "Tea"}, Relations: map[string][]string{categoryRelationID: {categoryRecordID}, reviewsRelationID: {reviewOneID, reviewTwoID}}},
		{ID: categoryRecordID, EntityID: categoryID, Values: map[string]any{"name": "Herbal"}},
		{ID: reviewOneID, EntityID: reviewID, Values: map[string]any{"body": "Aroma"}, Relations: map[string][]string{tagsRelationID: {tagOneID}}},
		{ID: reviewTwoID, EntityID: reviewID, Values: map[string]any{"body": "Taste"}, Relations: map[string][]string{tagsRelationID: {tagTwoID, tagOneID}}},
		{ID: tagOneID, EntityID: tagID, Values: map[string]any{"label": "fresh"}},
		{ID: tagTwoID, EntityID: tagID, Values: map[string]any{"label": "mild"}},
	}
	content, _, _, err := projectModelContent(model, manifest, placement, json.RawMessage(`{}`), records, []ModelContentSelection{{PlacementID: "catalog", InputKey: "badge", Mode: "ids", RecordIDs: []string{productRecordID}}})
	if err != nil {
		t.Fatalf("project related records: %v", err)
	}
	var result struct {
		Badge    string `json:"badge"`
		Products []struct {
			Title    string `json:"title"`
			Category struct {
				Name string `json:"name"`
			} `json:"category"`
			Reviews []struct {
				Body string `json:"body"`
				Tags []struct {
					Label string `json:"label"`
				} `json:"tags"`
			} `json:"reviews"`
		} `json:"products"`
	}
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatalf("decode related projection %s: %v", content, err)
	}
	if result.Badge != "Herbal" || len(result.Products) != 1 || result.Products[0].Category.Name != "Herbal" {
		t.Fatalf("one-to-one relation was not projected: %+v", result)
	}
	reviews := result.Products[0].Reviews
	if len(reviews) != 2 || reviews[0].Body != "Aroma" || reviews[1].Body != "Taste" {
		t.Fatalf("one-to-many relation order was not preserved: %+v", reviews)
	}
	if len(reviews[1].Tags) != 2 || reviews[1].Tags[0].Label != "mild" || reviews[1].Tags[1].Label != "fresh" {
		t.Fatalf("many-to-many nested relation order was not preserved: %+v", reviews[1].Tags)
	}
}
