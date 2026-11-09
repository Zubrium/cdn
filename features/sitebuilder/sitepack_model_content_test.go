package sitebuilder

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sitepackModelFixture() (*sitepackIndex, map[string]string) {
	productID, categoryID := uuid.NewString(), uuid.NewString()
	titleID, imageID, categoryNameID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	relationID := uuid.NewString()
	productRecordID, categoryRecordID := uuid.NewString(), uuid.NewString()
	publicationID := uuid.New()
	fileID := uuid.NewString()
	model := &DataModel{
		FormatVersion: 1,
		Entities: []DataModelEntity{
			{ID: productID, Key: "product", Name: "Product", Fields: []DataModelField{
				{ID: titleID, Key: "title", Name: "Title", Type: "text"},
				{ID: imageID, Key: "image", Name: "Image", Type: "media"},
			}},
			{ID: categoryID, Key: "category", Name: "Category", Fields: []DataModelField{
				{ID: categoryNameID, Key: "name", Name: "Name", Type: "text"},
			}},
		},
		Relations: []DataModelRelation{{ID: relationID, Name: "Category", FromEntityID: productID, ToEntityID: categoryID, Cardinality: "one-to-one"}},
		Positions: map[string]DataModelPosition{productID: {X: 20, Y: 30}, categoryID: {X: 420, Y: 30}},
	}
	product := func(title string) ModelContentRecord {
		return ModelContentRecord{ID: productRecordID, EntityID: productID, SortOrder: 0,
			Values:    map[string]any{"title": title, "image": fileID},
			Relations: map[string][]string{relationID: {categoryRecordID}},
		}
	}
	category := ModelContentRecord{ID: categoryRecordID, EntityID: categoryID, SortOrder: 0, Values: map[string]any{"name": "Skin"}}
	content := &packModelContent{
		Revision: 2, PublishedRevision: 1, ContentRevision: 1,
		DraftRecords:        []ModelContentRecord{product("Draft"), category},
		DraftSelections:     []ModelContentSelection{{PlacementID: "slider", InputKey: "products", Mode: "ids", RecordIDs: []string{productRecordID}}},
		PublishedRecords:    []ModelContentRecord{product("Published"), category},
		PublishedSelections: []ModelContentSelection{{PlacementID: "slider", InputKey: "products", Mode: "ids", RecordIDs: []string{productRecordID}}},
		History:             []packModelPublication{{ID: publicationID, DraftRevision: 1, ContentRevision: 1, CreatedAt: time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC), Records: []ModelContentRecord{product("Published"), category}, Selections: []ModelContentSelection{{PlacementID: "slider", InputKey: "products", Mode: "ids", RecordIDs: []string{productRecordID}}}}},
	}
	payload := SnapshotPayload{Config: SiteConfig{DataModel: model, Pages: []Page{
		{Placements: []Placement{{ID: "slider", DataBindings: []DataInputBinding{
			{InputKey: "products", EntityID: productID, Fields: []DataInputFieldBinding{
				{InputPath: "/title", FieldID: titleID},
				{InputPath: "/category/name", FieldID: categoryNameID, RelationIDs: []string{relationID}},
			}},
			{InputKey: "categoryName", EntityID: productID, RelationIDs: []string{relationID}, FieldID: categoryNameID},
		}}}},
	}}}
	index := &sitepackIndex{Payload: payload, ModelContent: content}
	return index, map[string]string{
		"product": productID, "category": categoryID, "title": titleID, "image": imageID,
		"categoryName": categoryNameID, "relation": relationID, "productRecord": productRecordID,
		"categoryRecord": categoryRecordID, "publication": publicationID.String(),
	}
}

func TestRemapSitepackModelIdentitiesRemapsSchemaBindingsRecordsRelationsAndHistory(t *testing.T) {
	index, old := sitepackModelFixture()
	mapping, err := remapSitepackModelIdentities(index)
	if err != nil {
		t.Fatal(err)
	}
	model := index.Payload.Config.DataModel
	if model.Entities[0].ID == old["product"] || model.Entities[1].ID == old["category"] ||
		model.Entities[0].Fields[0].ID == old["title"] || model.Entities[0].Fields[1].ID == old["image"] ||
		model.Relations[0].ID == old["relation"] || len(mapping.entities) != 2 || len(mapping.fields) != 3 || len(mapping.relations) != 1 || len(mapping.records) != 2 {
		t.Fatalf("schema identities were not remapped: %#v, %#v", mapping, model)
	}
	if err := ValidateDataModel(*model); err != nil {
		t.Fatalf("remapped model invalid: %v", err)
	}
	newProductID, newCategoryID := model.Entities[0].ID, model.Entities[1].ID
	if model.Relations[0].FromEntityID != newProductID || model.Relations[0].ToEntityID != newCategoryID {
		t.Fatalf("relation endpoints were not remapped: %#v", model.Relations[0])
	}
	if model.Positions[old["product"]] != (DataModelPosition{}) || len(model.Positions) != 2 {
		t.Fatalf("canvas positions were not moved with entity IDs: %#v", model.Positions)
	}
	binding := index.Payload.Config.Pages[0].Placements[0].DataBindings[0]
	if binding.EntityID != newProductID || binding.Fields[0].FieldID != model.Entities[0].Fields[0].ID ||
		binding.Fields[1].FieldID != model.Entities[1].Fields[0].ID || binding.Fields[1].RelationIDs[0] != model.Relations[0].ID ||
		index.Payload.Config.Pages[0].Placements[0].DataBindings[1].RelationIDs[0] != model.Relations[0].ID {
		t.Fatalf("placement binding was not remapped: %#v", binding)
	}
	for label, state := range map[string][]ModelContentRecord{
		"draft":     index.ModelContent.DraftRecords,
		"published": index.ModelContent.PublishedRecords,
		"history":   index.ModelContent.History[0].Records,
	} {
		if len(state) != 2 || state[0].ID != mapping.records[old["productRecord"]] || state[0].EntityID != newProductID ||
			state[1].ID != mapping.records[old["categoryRecord"]] || state[1].EntityID != newCategoryID {
			t.Fatalf("%s record identity not remapped: %#v", label, state)
		}
		if state[0].Relations[model.Relations[0].ID][0] != mapping.records[old["categoryRecord"]] {
			t.Fatalf("%s relation key/target not remapped: %#v", label, state[0].Relations)
		}
	}
	if got := index.ModelContent.DraftSelections[0].RecordIDs; len(got) != 1 || got[0] != mapping.records[old["productRecord"]] {
		t.Fatalf("draft selection was not remapped: %v", got)
	}
	if got := index.ModelContent.PublishedSelections[0].RecordIDs; len(got) != 1 || got[0] != mapping.records[old["productRecord"]] {
		t.Fatalf("published selection was not remapped: %v", got)
	}
	publication := index.ModelContent.History[0]
	if publication.ID == uuid.Nil || publication.ID.String() == old["publication"] {
		t.Fatalf("publication identity is invalid after remap: %s", publication.ID)
	}
	if err := ValidateModelContent(*model, nil, index.ModelContent.PublishedRecords, nil); err != nil {
		t.Fatalf("published records invalid after remap: %v", err)
	}
}

func TestValidateSitepackModelContentChecksHistoryAndAllStates(t *testing.T) {
	index, _ := sitepackModelFixture()
	index.Payload.Config.Pages = nil
	index.ModelContent.DraftSelections = nil
	index.ModelContent.PublishedSelections = nil
	index.ModelContent.History[0].Selections = nil
	if err := validateSitepackModelContent(index); err != nil {
		t.Fatalf("valid model workspace rejected: %v", err)
	}
	broken := *index
	content := *index.ModelContent
	broken.ModelContent = &content
	broken.ModelContent.History = append([]packModelPublication(nil), content.History...)
	broken.ModelContent.History[0].Records = append([]ModelContentRecord(nil), content.History[0].Records...)
	broken.ModelContent.History[0].Records[0].Relations = map[string][]string{uuid.NewString(): {uuid.NewString()}}
	if err := validateSitepackModelContent(&broken); err == nil {
		t.Fatal("invalid historical relation target was accepted")
	}
	broken = *index
	content = *index.ModelContent
	broken.ModelContent = &content
	broken.ModelContent.History = append([]packModelPublication(nil), content.History...)
	broken.ModelContent.History[0].DraftRevision = 2
	if err := validateSitepackModelContent(&broken); err == nil {
		t.Fatal("history revision inconsistent with published revision was accepted")
	}
}

func TestSitepackSeedsSingletonDraftWithoutPublishingIt(t *testing.T) {
	entityID, fieldID := uuid.NewString(), uuid.NewString()
	recordID := uuid.NewString()
	index := &sitepackIndex{
		Payload: SnapshotPayload{Config: SiteConfig{DataModel: &DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
			ID: entityID, Key: "site_settings", Name: "Site settings", Cardinality: DataModelEntityCardinalitySingleton,
			Fields: []DataModelField{{ID: fieldID, Key: "title", Name: "Title", Type: "text"}},
		}}}}},
		ModelContent: &packModelContent{Revision: 5, PublishedRevision: 3},
	}
	if err := seedSitepackSingletonDraft(index); err != nil {
		t.Fatalf("seed sitepack singleton draft: %v", err)
	}
	if index.ModelContent.Revision != 6 || index.ModelContent.PublishedRevision != 3 || len(index.ModelContent.DraftRecords) != 1 || len(index.ModelContent.PublishedRecords) != 0 {
		t.Fatalf("seeding must create a new draft revision without publishing: %+v", index.ModelContent)
	}
	seeded := index.ModelContent.DraftRecords[0]
	if seeded.ID == "" || seeded.EntityID != entityID || len(seeded.Values) != 0 || len(seeded.Relations) != 0 {
		t.Fatalf("imported singleton draft must be a generated empty record: %+v", seeded)
	}
	if _, err := uuid.Parse(seeded.ID); err != nil {
		t.Fatalf("imported singleton ID is not a UUID: %v", err)
	}

	index.ModelContent.DraftRecords = []ModelContentRecord{{ID: recordID, EntityID: entityID, Values: map[string]any{"title": "Saved"}}}
	index.ModelContent.Revision = 7
	if err := seedSitepackSingletonDraft(index); err != nil {
		t.Fatalf("re-seed existing singleton: %v", err)
	}
	if len(index.ModelContent.DraftRecords) != 1 || index.ModelContent.DraftRecords[0].ID != recordID || index.ModelContent.DraftRecords[0].Values["title"] != "Saved" || index.ModelContent.Revision != 7 {
		t.Fatalf("import must preserve existing singleton identity and values: %+v", index.ModelContent)
	}
}

func TestSitepackPreservesManyHistoryAfterEntityBecomesSingletonButRestoreRejectsIt(t *testing.T) {
	index, ids := sitepackModelFixture()
	index.Payload.Config.Pages = nil
	index.ModelContent.DraftSelections = nil
	index.ModelContent.PublishedSelections = nil
	index.ModelContent.History[0].Selections = nil
	index.Payload.Config.DataModel.Entities[0].Cardinality = DataModelEntityCardinalitySingleton
	older := index.ModelContent.History[0].Records[0]
	older.ID = uuid.NewString()
	older.SortOrder = 1
	older.Relations = map[string][]string{}
	index.ModelContent.History[0].Records = append(index.ModelContent.History[0].Records, older)
	index.ModelContent.Revision = 3
	index.ModelContent.PublishedRevision = 2
	index.ModelContent.ContentRevision = 2
	latest := packModelPublication{
		ID: uuid.New(), DraftRevision: 2, ContentRevision: 2,
		CreatedAt:  index.ModelContent.History[0].CreatedAt.Add(time.Minute),
		Records:    append([]ModelContentRecord(nil), index.ModelContent.PublishedRecords...),
		Selections: []ModelContentSelection{},
	}
	index.ModelContent.History = append(index.ModelContent.History, latest)
	if err := validateSitepackModelContent(index); err != nil {
		t.Fatalf("sitepack should preserve structurally valid pre-singleton history: %v", err)
	}
	if err := validateModelContentPublicationForRestore(*index.Payload.Config.DataModel, nil, index.ModelContent.History[0].Records, nil); err == nil {
		t.Fatal("restore must reject historical many state incompatible with singleton without mutating the archive")
	}
	if len(index.ModelContent.History[0].Records) != 3 || index.ModelContent.History[0].Records[2].ID != older.ID || ids["productRecord"] == older.ID {
		t.Fatal("rejected restore validation must preserve sitepack history rows and identities")
	}
}

func TestSitepackModelMediaInventoryAndRemapCoversPublishedDraftAndHistory(t *testing.T) {
	entityID, imageFieldID, textFieldID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	fileA, fileB := uuid.New(), uuid.New()
	model := &DataModel{FormatVersion: 1, Entities: []DataModelEntity{{ID: entityID, Key: "asset", Name: "Asset", Fields: []DataModelField{
		{ID: imageFieldID, Key: "image", Name: "Image", Type: "media"},
		{ID: textFieldID, Key: "note", Name: "Note", Type: "text"},
	}}}}
	makeRecord := func(id, image, note string) ModelContentRecord {
		return ModelContentRecord{ID: id, EntityID: entityID, Values: map[string]any{"image": image, "note": note}}
	}
	recordID := uuid.NewString()
	content := &packModelContent{
		DraftRecords:     []ModelContentRecord{makeRecord(recordID, fileA.String(), fileB.String())},
		PublishedRecords: []ModelContentRecord{makeRecord(recordID, fileA.String(), fileB.String())},
		History:          []packModelPublication{{Records: []ModelContentRecord{makeRecord(recordID, fileB.String(), fileA.String())}}},
	}
	refs, err := sitepackModelMediaReferences(content, model)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0] != fileA && refs[1] != fileA || refs[0] != fileB && refs[1] != fileB {
		t.Fatalf("media inventory missed a model state: %v", refs)
	}
	newA, newB := uuid.NewString(), uuid.NewString()
	if err := rewriteSitepackModelMediaReferences(content, model, map[string]string{fileA.String(): newA, fileB.String(): newB}); err != nil {
		t.Fatal(err)
	}
	for _, records := range [][]ModelContentRecord{content.DraftRecords, content.PublishedRecords, content.History[0].Records} {
		image := records[0].Values["image"]
		note := records[0].Values["note"]
		if image != newA && image != newB {
			t.Fatalf("media value was not remapped: %v", image)
		}
		if records == nil || note != fileA.String() && note != fileB.String() {
			t.Fatalf("non-media value was changed: %v", note)
		}
	}
}

func TestSitepackModelContentJSONPreservesRevisionHistory(t *testing.T) {
	index, _ := sitepackModelFixture()
	encoded, err := json.Marshal(index.ModelContent)
	if err != nil {
		t.Fatal(err)
	}
	var decoded packModelContent
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Revision != 2 || decoded.PublishedRevision != 1 || len(decoded.DraftRecords) != 2 || len(decoded.PublishedRecords) != 2 || len(decoded.History) != 1 || !decoded.History[0].CreatedAt.Equal(index.ModelContent.History[0].CreatedAt) {
		t.Fatalf("sitepack model content did not roundtrip: %#v", decoded)
	}
}
