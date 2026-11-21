package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/Zubrium/cdn/engines/content/domain"
)

func TestWorkspaceAcceptsOptionalTypedValuesAndOrderedSelection(t *testing.T) {
	model, err := domain.ParseDataModel(json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"article","name":"Article","cardinality":"many","fields":[{"id":"22222222-2222-4222-8222-222222222222","key":"title","name":"Title","type":"text"},{"id":"33333333-3333-4333-8333-333333333333","key":"status","name":"Status","type":"enum","options":["draft","live"]}]}]}`))
	if err != nil {
		t.Fatalf("ParseDataModel() error = %v", err)
	}

	workspace := domain.WorkspaceData{
		Locale: "en-US",
		Records: []domain.Record{
			{ID: "44444444-4444-4444-8444-444444444444", TranslationID: "44444444-4444-4444-8444-444444444444", Locale: "en-US", EntityID: "11111111-1111-4111-8111-111111111111", SortOrder: 0, Values: map[string]any{"title": "A", "status": "live"}},
			{ID: "55555555-5555-4555-8555-555555555555", TranslationID: "55555555-5555-4555-8555-555555555555", Locale: "en-US", EntityID: "11111111-1111-4111-8111-111111111111", SortOrder: 1, Values: map[string]any{"title": ""}},
		},
		Selections: []domain.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{"55555555-5555-4555-8555-555555555555", "44444444-4444-4444-8444-444444444444"}}},
	}
	placements := []domain.Placement{{ID: "placement-one", Inputs: []domain.PlacementInput{{InputKey: "articles", Kind: "collection", EntityID: "11111111-1111-4111-8111-111111111111"}}}}

	if err := domain.ValidateWorkspace(model, placements, workspace, nil, false); err != nil {
		t.Fatalf("ValidateWorkspace() error = %v", err)
	}
}

func TestWorkspaceRejectsInvalidFieldTypeEnumAndSelectionTarget(t *testing.T) {
	model, err := domain.ParseDataModel(json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"article","name":"Article","cardinality":"many","fields":[{"id":"22222222-2222-4222-8222-222222222222","key":"title","name":"Title","type":"text"},{"id":"33333333-3333-4333-8333-333333333333","key":"status","name":"Status","type":"enum","options":["draft","live"]}]}]}`))
	if err != nil {
		t.Fatalf("ParseDataModel() error = %v", err)
	}
	placements := []domain.Placement{{ID: "placement-one", Inputs: []domain.PlacementInput{{InputKey: "articles", Kind: "collection", EntityID: "11111111-1111-4111-8111-111111111111"}}}}
	base := domain.WorkspaceData{
		Locale:     "en-US",
		Records:    []domain.Record{{ID: "44444444-4444-4444-8444-444444444444", TranslationID: "44444444-4444-4444-8444-444444444444", Locale: "en-US", EntityID: "11111111-1111-4111-8111-111111111111", Values: map[string]any{"title": "A", "status": "live"}}},
		Selections: []domain.Selection{{PlacementID: "placement-one", InputKey: "articles", Mode: "ids", RecordIDs: []string{"44444444-4444-4444-8444-444444444444"}}},
	}

	for name, mutate := range map[string]func(*domain.WorkspaceData){
		"wrong value type":   func(workspace *domain.WorkspaceData) { workspace.Records[0].Values["title"] = 12 },
		"unknown enum value": func(workspace *domain.WorkspaceData) { workspace.Records[0].Values["status"] = "archived" },
		"selection for missing record": func(workspace *domain.WorkspaceData) {
			workspace.Selections[0].RecordIDs[0] = "66666666-6666-4666-8666-666666666666"
		},
	} {
		t.Run(name, func(t *testing.T) {
			workspace := cloneWorkspace(base)
			mutate(&workspace)
			if err := domain.ValidateWorkspace(model, placements, workspace, nil, false); err == nil {
				t.Fatal("ValidateWorkspace() accepted invalid workspace")
			}
		})
	}
}

func TestSingletonRequiresExactlyOneDraftRecord(t *testing.T) {
	model, err := domain.ParseDataModel(json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"settings","name":"Settings","cardinality":"singleton","fields":[]}]}`))
	if err != nil {
		t.Fatalf("ParseDataModel() error = %v", err)
	}
	if err := domain.ValidateWorkspace(model, nil, domain.WorkspaceData{Locale: "en-US"}, nil, false); err == nil {
		t.Fatal("ValidateWorkspace() accepted a singleton without its required draft record")
	}
	workspace := domain.WorkspaceData{Locale: "en-US", Records: []domain.Record{{ID: "44444444-4444-4444-8444-444444444444", TranslationID: "44444444-4444-4444-8444-444444444444", Locale: "en-US", EntityID: "11111111-1111-4111-8111-111111111111", Values: map[string]any{}}}}
	if err := domain.ValidateWorkspace(model, nil, workspace, nil, false); err != nil {
		t.Fatalf("ValidateWorkspace() rejected exactly one singleton draft: %v", err)
	}
}

func TestWorkspacePreservesKnownRecordOrphanFieldsButPublicationRejectsThem(t *testing.T) {
	model, err := domain.ParseDataModel(json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"article","name":"Article","cardinality":"many","fields":[{"id":"22222222-2222-4222-8222-222222222222","key":"title","name":"Title","type":"text"}]}]}`))
	if err != nil {
		t.Fatalf("ParseDataModel() error = %v", err)
	}
	workspace := domain.WorkspaceData{Locale: "en-US", Records: []domain.Record{{ID: "44444444-4444-4444-8444-444444444444", TranslationID: "44444444-4444-4444-8444-444444444444", Locale: "en-US", EntityID: "11111111-1111-4111-8111-111111111111", Values: map[string]any{"title": "A", "removed_field": "kept"}}}}
	previous := &domain.WorkspaceData{Locale: "en-US", Records: []domain.Record{{ID: "44444444-4444-4444-8444-444444444444", TranslationID: "44444444-4444-4444-8444-444444444444", Locale: "en-US", EntityID: "11111111-1111-4111-8111-111111111111", Values: map[string]any{"removed_field": "kept"}}}}
	if err := domain.ValidateWorkspace(model, nil, workspace, previous, false); err != nil {
		t.Fatalf("draft should retain an orphaned field: %v", err)
	}
	if err := domain.ValidateWorkspace(model, nil, workspace, previous, true); err == nil {
		t.Fatal("publication should reject an orphaned field")
	}
}

func TestSingletonRecordInputResolvesAutomaticallyAndOldManualChoiceStaysEditable(t *testing.T) {
	model, err := domain.ParseDataModel(json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"settings","name":"Settings","cardinality":"singleton","fields":[]}]}`))
	if err != nil {
		t.Fatalf("ParseDataModel() error = %v", err)
	}
	placements := []domain.Placement{{ID: "placement-one", Inputs: []domain.PlacementInput{{InputKey: "settings", Kind: "record", EntityID: "11111111-1111-4111-8111-111111111111"}}}}
	record := domain.Record{ID: "22222222-2222-4222-8222-222222222222", TranslationID: "22222222-2222-4222-8222-222222222222", Locale: "en-US", EntityID: "11111111-1111-4111-8111-111111111111", Values: map[string]any{}}
	automatic := domain.WorkspaceData{Locale: "en-US", Records: []domain.Record{record}, Selections: []domain.Selection{}}
	if err := domain.ValidateWorkspace(model, placements, automatic, nil, false); err != nil {
		t.Fatalf("singleton record input without a manual selection: %v", err)
	}
	existing := domain.WorkspaceData{
		Locale:     "en-US",
		Records:    []domain.Record{record},
		Selections: []domain.Selection{{PlacementID: "placement-one", InputKey: "settings", Mode: "ids", RecordIDs: []string{record.ID}}},
	}
	if err := domain.ValidateWorkspace(model, placements, existing, &existing, false); err != nil {
		t.Fatalf("unchanged selection from before singleton conversion should remain editable: %v", err)
	}
	if err := domain.ValidateWorkspace(model, placements, existing, &existing, true); err == nil {
		t.Fatal("publication accepted a manual choice for an automatically resolved singleton")
	}
	if err := domain.ValidateWorkspace(model, placements, existing, nil, false); err == nil {
		t.Fatal("new manual selection for a singleton record input was accepted")
	}
}

func TestDataModelRejectsUnknownRelationsField(t *testing.T) {
	for name, relations := range map[string]string{
		"null":  `,"relations":null`,
		"empty": `,"relations":[]`,
		"items": `,"relations":[{"id":"relation-one"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := `{"formatVersion":1,"entities":[]` + relations + `}`
			if _, err := domain.ParseDataModel(json.RawMessage(raw)); err == nil {
				t.Fatal("retired relations field was accepted")
			}
		})
	}
	if _, err := domain.ParseDataModel(json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"settings","name":"Settings"}]}`)); err == nil {
		t.Fatal("model entity without its fields array was accepted")
	}
}

func TestDataModelRequiresExplicitManyOrSingletonCardinality(t *testing.T) {
	for name, raw := range map[string]string{
		"missing": `{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"items","name":"Items","fields":[]}]}`,
		"empty":   `{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"items","name":"Items","cardinality":"","fields":[]}]}`,
		"unknown": `{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"items","name":"Items","cardinality":"unknown","fields":[]}]}`,
	} {
		if _, err := domain.ParseDataModel(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid cardinality variant %q was accepted", name)
		}
	}
}

func cloneWorkspace(source domain.WorkspaceData) domain.WorkspaceData {
	cloned := domain.WorkspaceData{Locale: source.Locale, Records: make([]domain.Record, len(source.Records)), Selections: make([]domain.Selection, len(source.Selections))}
	for index, record := range source.Records {
		values := make(map[string]any, len(record.Values))
		for key, value := range record.Values {
			values[key] = value
		}
		record.Values = values
		cloned.Records[index] = record
	}
	for index, selection := range source.Selections {
		selection.RecordIDs = append([]string(nil), selection.RecordIDs...)
		cloned.Selections[index] = selection
	}
	return cloned
}
