package sitebuilder

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateSnapshotSingletonTransitionPreservesAtMostOneParentRow(t *testing.T) {
	entityID := uuid.NewString()
	previous := &DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: entityID, Key: "product", Name: "Product", Cardinality: DataModelEntityCardinalityMany,
	}}}
	next := &DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: entityID, Key: "product", Name: "Product", Cardinality: DataModelEntityCardinalitySingleton,
	}}}
	rows := []ModelContentRecord{
		{ID: uuid.NewString(), EntityID: entityID},
		{ID: uuid.NewString(), EntityID: entityID},
	}
	if err := validateSnapshotSingletonTransition(previous, next, rows, rows[:1]); err == nil || !strings.Contains(err.Error(), "draft") || !strings.Contains(err.Error(), "many to singleton") {
		t.Fatalf("many-to-singleton with multiple draft rows must fail before copying: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("transition validation must not discard parent rows: %#v", rows)
	}
	if err := validateSnapshotSingletonTransition(previous, next, rows[:1], rows); err == nil || !strings.Contains(err.Error(), "published") {
		t.Fatalf("many-to-singleton with multiple published rows must fail: %v", err)
	}
	if err := validateSnapshotSingletonTransition(previous, next, rows[:1], rows[:1]); err != nil {
		t.Fatalf("one draft and one published record should preserve identity through transition: %v", err)
	}
}

func TestPrepareSnapshotModelContentSeedsDraftWithoutPublishingAndPreservesForkState(t *testing.T) {
	entityID := uuid.NewString()
	model := DataModel{FormatVersion: 1, Entities: []DataModelEntity{{
		ID: entityID, Key: "site_settings", Name: "Site settings", Cardinality: DataModelEntityCardinalitySingleton,
	}}}
	rootState, err := prepareSnapshotModelContentState(model, storedModelContentWorkspace{
		DraftRecords: []ModelContentRecord{}, PublishedRecords: []ModelContentRecord{},
	})
	if err != nil {
		t.Fatalf("seed root singleton draft: %v", err)
	}
	if rootState.Revision != 1 || rootState.PublishedRevision != 0 || len(rootState.DraftRecords) != 1 || len(rootState.PublishedRecords) != 0 || rootState.Exists {
		t.Fatalf("root singleton should be a new draft and stay unpublished: %+v", rootState)
	}

	draftID, publishedID := uuid.NewString(), uuid.NewString()
	forkState, err := prepareSnapshotModelContentState(model, storedModelContentWorkspace{
		Exists: true, Revision: 12, PublishedRevision: 10,
		DraftRecords:     []ModelContentRecord{{ID: draftID, EntityID: entityID, Values: map[string]any{"name": "Draft"}}},
		PublishedRecords: []ModelContentRecord{{ID: publishedID, EntityID: entityID, Values: map[string]any{"name": "Published"}}},
	})
	if err != nil {
		t.Fatalf("preserve fork singleton state: %v", err)
	}
	if forkState.Revision != 12 || forkState.PublishedRevision != 10 || forkState.Exists || len(forkState.DraftRecords) != 1 || len(forkState.PublishedRecords) != 1 ||
		forkState.DraftRecords[0].ID != draftID || forkState.DraftRecords[0].Values["name"] != "Draft" ||
		forkState.PublishedRecords[0].ID != publishedID || forkState.PublishedRecords[0].Values["name"] != "Published" {
		t.Fatalf("fork should retain independent draft/published IDs and values: %+v", forkState)
	}
}
