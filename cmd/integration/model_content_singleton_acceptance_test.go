package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// TestModelContentSingletonAPIAcceptance covers singleton record lifecycle,
// runtime projection, cardinality-changing snapshots, historical restores,
// and Sitepack portability. The disposable database guard must remain first:
// newTestEnv migrates and truncates integration tables.
func TestModelContentSingletonAPIAcceptance(t *testing.T) {
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	ctx := context.Background()

	ids := map[string]string{
		"singleton": uuid.NewString(),
		"title":     uuid.NewString(),
		"many":      uuid.NewString(),
		"label":     uuid.NewString(),
	}
	manifest := map[string]any{
		"formatVersion": 2,
		"key":           "singleton-model-acceptance",
		"name":          "Singleton model acceptance",
		"version":       "1.0.0",
		"engineApi":     "2",
		"runtime":       map[string]any{"abi": "static-module-v1", "entry": "entry.js"},
		"slots":         []string{"main"},
		"arguments":     map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"dataInputs": []any{
			map[string]any{"key": "record", "kind": "record", "fields": []any{
				map[string]any{"path": "/title", "type": "text"},
			}},
			map[string]any{"key": "scalar", "kind": "scalar", "type": "text"},
			map[string]any{"key": "collection", "kind": "collection", "fields": []any{
				map[string]any{"path": "/title", "type": "text"},
			}},
		},
	}
	modelAcceptanceUploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")

	model := map[string]any{
		"formatVersion": 1,
		"entities": []any{
			map[string]any{
				"id": ids["singleton"], "key": "site_settings", "name": "Site settings", "cardinality": "singleton",
				"fields": []any{map[string]any{"id": ids["title"], "key": "title", "name": "Title", "type": "text"}},
			},
			map[string]any{
				"id": ids["many"], "key": "article", "name": "Article", "cardinality": "many",
				"fields": []any{map[string]any{"id": ids["label"], "key": "label", "name": "Label", "type": "text"}},
			},
		},
	}
	fieldMapping := []any{map[string]any{"inputPath": "/title", "fieldId": ids["title"]}}
	bindings := []any{
		map[string]any{"inputKey": "record", "entityId": ids["singleton"], "fields": fieldMapping},
		map[string]any{"inputKey": "scalar", "entityId": ids["singleton"], "fieldId": ids["title"]},
		map[string]any{"inputKey": "collection", "entityId": ids["singleton"], "fields": fieldMapping},
	}
	config := map[string]any{
		"formatVersion": 2,
		"dataModel":     model,
		"pages": []any{map[string]any{
			"id": "home", "name": "Home", "kind": "page",
			"placements": []any{map[string]any{
				"id": "singleton-placement", "build": map[string]any{"featureKey": "singleton-model-acceptance", "version": "1.0.0"},
				"slot": "main", "dataBindings": bindings,
			}},
		}},
		"routeTree": map[string]any{"id": "root", "type": "index", "pageId": "home"},
	}
	root := modelAcceptanceSaveAndDeploySnapshot(t, env, config, "Singleton model acceptance")
	rootID := root["id"].(string)
	base := "/api/v2/admin/content/snapshots/" + rootID + "/model"

	status, initial, _ := env.request(http.MethodGet, base, env.adminToken, nil)
	if status != http.StatusOK || initial["revision"] != float64(1) || initial["publishedRevision"] != float64(0) {
		t.Fatalf("new singleton workspace should seed only an unpublished draft: status=%d body=%v", status, initial)
	}
	initialRecords := initial["records"].([]any)
	if len(initialRecords) != 1 {
		t.Fatalf("new singleton model must seed exactly one draft record, got %v", initialRecords)
	}
	seeded := initialRecords[0].(map[string]any)
	seededID, ok := seeded["id"].(string)
	if !ok || uuid.Validate(seededID) != nil || seeded["entityId"] != ids["singleton"] || len(seeded["values"].(map[string]any)) != 0 {
		t.Fatalf("singleton seed must have a generated ID and empty values: %v", seeded)
	}
	if relations, ok := seeded["relations"].(map[string]any); ok && len(relations) != 0 {
		t.Fatalf("singleton seed must not carry relations: %v", seeded)
	}
	var initialPublications int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM snapshot_model_content_publications WHERE snapshot_id=$1`, rootID).Scan(&initialPublications); err != nil {
		t.Fatalf("count initial singleton publications: %v", err)
	}
	if initialPublications != 0 {
		t.Fatalf("new singleton workspace must not create publication history before explicit publish: %d rows", initialPublications)
	}
	initialRuntime := modelSingletonAcceptanceRuntimeContent(t, env)
	if record := initialRuntime["record"].(map[string]any); len(record) != 0 {
		t.Fatalf("unpublished singleton record leaked into runtime: %v", initialRuntime)
	}
	if scalar, exists := initialRuntime["scalar"]; exists && scalar != nil {
		t.Fatalf("unpublished singleton scalar leaked into runtime: %v", initialRuntime)
	}
	if collection := initialRuntime["collection"].([]any); len(collection) != 0 {
		t.Fatalf("unpublished singleton collection should be empty: %v", initialRuntime)
	}

	secondRecord := map[string]any{
		"entityId": ids["singleton"], "sortOrder": 1,
		"values": map[string]any{"title": "Must be rejected"},
	}
	duplicateRecords := cloneModelContentJSONArray(t, initialRecords)
	duplicateRecords = append(duplicateRecords, secondRecord)
	status, rejected, _ := env.request(http.MethodPut, base, env.adminToken, map[string]any{
		"revision": initial["revision"], "records": duplicateRecords,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("second singleton draft record should be rejected: status=%d body=%v", status, rejected)
	}
	status, afterDuplicate, _ := env.request(http.MethodGet, base, env.adminToken, nil)
	if status != http.StatusOK || afterDuplicate["revision"] != initial["revision"] ||
		!reflect.DeepEqual(afterDuplicate["records"], initial["records"]) {
		t.Fatalf("rejected duplicate changed singleton revision or data: status=%d before=%v after=%v", status, initial, afterDuplicate)
	}

	status, saved, _ := env.request(http.MethodPut, base, env.adminToken, map[string]any{
		"revision": initial["revision"],
		"records": []any{
			map[string]any{"id": seededID, "entityId": ids["singleton"], "sortOrder": 0, "values": map[string]any{"title": "Singleton title"}},
			map[string]any{"entityId": ids["many"], "sortOrder": 0, "values": map[string]any{"label": "First article"}},
			map[string]any{"entityId": ids["many"], "sortOrder": 1, "values": map[string]any{"label": "Second article"}},
		},
		"selections": []any{map[string]any{"placementId": "singleton-placement", "inputKey": "collection", "mode": "ids", "recordIds": []string{}}},
	})
	if status != http.StatusOK || saved["revision"] != float64(2) {
		t.Fatalf("save singleton and many draft records: status=%d body=%v", status, saved)
	}
	singletonRecord := modelContentRecordByID(t, saved["records"].([]any), seededID)
	if singletonRecord["values"].(map[string]any)["title"] != "Singleton title" {
		t.Fatalf("singleton draft lost its value: %v", singletonRecord)
	}
	manyIDs := modelSingletonAcceptanceRecordIDs(t, saved["records"].([]any), ids["many"])
	if len(manyIDs) != 2 {
		t.Fatalf("expected two many draft records, got %v", saved["records"])
	}
	for _, id := range manyIDs {
		if uuid.Validate(id) != nil {
			t.Fatalf("many record ID was not generated as a canonical UUID: %q", id)
		}
	}

	rootConfigWithSingletonMany := modelSingletonAcceptanceChildConfig(t, config, ids["many"])
	beforeFailedForkCount := modelSingletonAcceptanceSnapshotCount(t, env)
	modelSingletonAcceptanceAssertDevAssignment(t, env, rootID)
	status, failedDraftTransition, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{
		"name": "Rejected draft many to singleton", "parentSnapshotId": rootID, "config": rootConfigWithSingletonMany,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("many-to-singleton transition should reject two current draft rows: status=%d body=%v", status, failedDraftTransition)
	}
	if after := modelSingletonAcceptanceSnapshotCount(t, env); after != beforeFailedForkCount {
		t.Fatalf("rejected draft cardinality transition created a snapshot: before=%d after=%d", beforeFailedForkCount, after)
	}
	modelSingletonAcceptanceAssertDevAssignment(t, env, rootID)

	status, published, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": saved["revision"]})
	if status != http.StatusOK || published["publishedRevision"] != saved["revision"] {
		t.Fatalf("publish singleton with many history rows: status=%d body=%v", status, published)
	}
	assertedSingletonRuntime := modelSingletonAcceptanceRuntimeContent(t, env)
	// This draft explicitly stores an empty ID selection, so the published
	// singleton is omitted from the collection until selected below.
	modelSingletonAcceptanceAssertProjection(t, assertedSingletonRuntime, seededID, "Singleton title", 0)

	status, selected, _ := env.request(http.MethodPut, base, env.adminToken, map[string]any{
		"revision": published["revision"], "records": published["records"],
		"selections": []any{map[string]any{
			"placementId": "singleton-placement", "inputKey": "collection", "mode": "ids", "recordIds": []string{seededID},
		}},
	})
	if status != http.StatusOK || selected["revision"] != float64(3) {
		t.Fatalf("select the singleton in collection input: status=%d body=%v", status, selected)
	}
	status, selectedPublished, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": selected["revision"]})
	if status != http.StatusOK || selectedPublished["publishedRevision"] != selected["revision"] {
		t.Fatalf("publish singleton collection selection: status=%d body=%v", status, selectedPublished)
	}
	modelSingletonAcceptanceAssertProjection(t, modelSingletonAcceptanceRuntimeContent(t, env), seededID, "Singleton title", 1)

	clearedRecords := cloneModelContentJSONArray(t, selectedPublished["records"].([]any))
	modelContentRecordByID(t, clearedRecords, seededID)["values"] = map[string]any{}
	beforeClearRuntime := modelSingletonAcceptanceRuntimeContent(t, env)
	status, clearedDraft, _ := env.request(http.MethodPut, base, env.adminToken, map[string]any{
		"revision": selectedPublished["revision"], "records": clearedRecords,
		"selections": selectedPublished["selections"],
	})
	if status != http.StatusOK || clearedDraft["revision"] != float64(4) {
		t.Fatalf("clear singleton draft values: status=%d body=%v", status, clearedDraft)
	}
	clearedDraftSingleton := modelContentRecordByID(t, clearedDraft["records"].([]any), seededID)
	if clearedDraftSingleton["id"] != seededID || len(clearedDraftSingleton["values"].(map[string]any)) != 0 {
		t.Fatalf("clearing singleton values must preserve its generated record ID: %v", clearedDraftSingleton)
	}
	if afterClearRuntime := modelSingletonAcceptanceRuntimeContent(t, env); !reflect.DeepEqual(beforeClearRuntime, afterClearRuntime) {
		t.Fatalf("saving cleared draft changed published runtime before publish: before=%v after=%v", beforeClearRuntime, afterClearRuntime)
	}
	status, clearedPublished, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": clearedDraft["revision"]})
	if status != http.StatusOK || clearedPublished["publishedRevision"] != clearedDraft["revision"] {
		t.Fatalf("publish cleared singleton values: status=%d body=%v", status, clearedPublished)
	}
	clearedRuntime := modelSingletonAcceptanceRuntimeContent(t, env)
	clearedRecord := clearedRuntime["record"].(map[string]any)
	if clearedRecord["id"] != seededID || len(clearedRecord) != 1 {
		t.Fatalf("published cleared record should retain only its stable ID: %v", clearedRecord)
	}
	if scalar, exists := clearedRuntime["scalar"]; exists && scalar != nil {
		t.Fatalf("cleared singleton scalar should not project a value: %v", clearedRuntime)
	}
	clearedCollection := clearedRuntime["collection"].([]any)
	if len(clearedCollection) != 1 || clearedCollection[0].(map[string]any)["id"] != seededID || len(clearedCollection[0].(map[string]any)) != 1 {
		t.Fatalf("cleared singleton selection should remain a one-item ID-only array: %v", clearedCollection)
	}

	// The draft is now <=1 while its published state still has two many rows.
	// A separate failed fork proves the published side of the guard.
	oneManyRecords := make([]any, 0, len(clearedPublished["records"].([]any))-1)
	removedOneMany := false
	for _, raw := range clearedPublished["records"].([]any) {
		record := raw.(map[string]any)
		if record["entityId"] == ids["many"] && !removedOneMany {
			removedOneMany = true
			continue
		}
		oneManyRecords = append(oneManyRecords, record)
	}
	if !removedOneMany {
		t.Fatal("test setup lost both many records before published-state transition check")
	}
	status, reducedDraft, _ := env.request(http.MethodPut, base, env.adminToken, map[string]any{
		"revision": clearedPublished["revision"], "records": oneManyRecords,
		"selections": clearedPublished["selections"],
	})
	if status != http.StatusOK || reducedDraft["revision"] != float64(5) {
		t.Fatalf("reduce draft many entity to one record: status=%d body=%v", status, reducedDraft)
	}
	beforeFailedPublishedForkCount := modelSingletonAcceptanceSnapshotCount(t, env)
	status, failedPublishedTransition, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{
		"name": "Rejected published many to singleton", "parentSnapshotId": rootID, "config": rootConfigWithSingletonMany,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("many-to-singleton transition should reject two published rows even with one draft row: status=%d body=%v", status, failedPublishedTransition)
	}
	if after := modelSingletonAcceptanceSnapshotCount(t, env); after != beforeFailedPublishedForkCount {
		t.Fatalf("rejected published cardinality transition created a snapshot: before=%d after=%d", beforeFailedPublishedForkCount, after)
	}
	modelSingletonAcceptanceAssertDevAssignment(t, env, rootID)

	status, reducedPublished, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": reducedDraft["revision"]})
	if status != http.StatusOK || reducedPublished["publishedRevision"] != reducedDraft["revision"] {
		t.Fatalf("publish reduced many entity: status=%d body=%v", status, reducedPublished)
	}
	parentContentRevision := modelSingletonAcceptanceContentRevision(t, env, rootID)
	status, child, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{
		"name":                          "Many entity becomes singleton",
		"parentSnapshotId":              rootID,
		"expectedParentContentRevision": parentContentRevision,
		"config":                        rootConfigWithSingletonMany,
	})
	if status != http.StatusCreated {
		t.Fatalf("many-to-singleton transition should succeed after both states contain at most one row: status=%d body=%v", status, child)
	}
	childID := child["id"].(string)
	childBase := "/api/v2/admin/content/snapshots/" + childID + "/model"
	modelSingletonAcceptanceAssertDevAssignment(t, env, rootID)
	status, childWorkspace, _ := env.request(http.MethodGet, childBase, env.adminToken, nil)
	if status != http.StatusOK || childWorkspace["revision"] != reducedPublished["revision"] || childWorkspace["publishedRevision"] != reducedPublished["publishedRevision"] {
		t.Fatalf("child should inherit independent current singleton states: status=%d body=%v", status, childWorkspace)
	}
	if len(childWorkspace["records"].([]any)) != 2 {
		t.Fatalf("child should preserve singleton plus one formerly-many record: %v", childWorkspace["records"])
	}
	if cardinality := modelSingletonAcceptanceEntityCardinality(t, childWorkspace["dataModel"].(map[string]any), "article"); cardinality != "singleton" {
		t.Fatalf("child data model did not record the structural singleton transition: %q", cardinality)
	}
	if childSingleton := modelContentRecordByID(t, childWorkspace["records"].([]any), seededID); childSingleton["entityId"] != ids["singleton"] {
		t.Fatalf("child changed the singleton's generated record identity: %v", childSingleton)
	}
	if got := modelSingletonAcceptanceRecordIDs(t, childWorkspace["records"].([]any), ids["many"]); len(got) != 1 {
		t.Fatalf("child cardinality transition should retain its single many record: %v", got)
	}

	status, history, _ := env.request(http.MethodGet, childBase+"/history", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read child publication history: status=%d body=%v", status, history)
	}
	historyItems := history["items"].([]any)
	if len(historyItems) != 4 {
		t.Fatalf("child should preserve four parent model publications: %v", historyItems)
	}
	firstManyHistoryID := ""
	for _, raw := range historyItems {
		item := raw.(map[string]any)
		if item["contentRevision"] == float64(1) {
			firstManyHistoryID = item["id"].(string)
		}
	}
	if firstManyHistoryID == "" {
		t.Fatalf("child history omitted the original many publication: %v", historyItems)
	}
	var firstPublicationRecordsRaw []byte
	if err := env.pool.QueryRow(ctx, `SELECT records FROM snapshot_model_content_publications WHERE snapshot_id=$1 AND content_revision=1`, childID).Scan(&firstPublicationRecordsRaw); err != nil {
		t.Fatalf("read first child history state: %v", err)
	}
	var firstPublicationRecords []any
	if err := json.Unmarshal(firstPublicationRecordsRaw, &firstPublicationRecords); err != nil {
		t.Fatalf("decode first child history state: %v", err)
	}
	if got := modelSingletonAcceptanceRecordIDs(t, firstPublicationRecords, ids["many"]); len(got) != 2 {
		t.Fatalf("child history must retain the earlier two-row many publication: %v", firstPublicationRecords)
	}

	beforeRejectedRestore := childWorkspace
	status, rejectedRestore, _ := env.request(http.MethodPost, childBase+"/history/"+firstManyHistoryID+"/restore", env.adminToken, map[string]any{"revision": childWorkspace["revision"]})
	if status != http.StatusBadRequest {
		t.Fatalf("restoring a historical two-row many publication into the current singleton model should fail: status=%d body=%v", status, rejectedRestore)
	}
	status, afterRejectedRestore, _ := env.request(http.MethodGet, childBase, env.adminToken, nil)
	if status != http.StatusOK || afterRejectedRestore["revision"] != beforeRejectedRestore["revision"] ||
		afterRejectedRestore["publishedRevision"] != beforeRejectedRestore["publishedRevision"] ||
		!reflect.DeepEqual(afterRejectedRestore["records"], beforeRejectedRestore["records"]) ||
		!reflect.DeepEqual(afterRejectedRestore["selections"], beforeRejectedRestore["selections"]) {
		t.Fatalf("rejected incompatible restore mutated child content: before=%v after=%v", beforeRejectedRestore, afterRejectedRestore)
	}
	modelSingletonAcceptanceAssertDevAssignment(t, env, rootID)

	// Export and import stay on this guarded disposable installation. No object
	// store is required because this fixture has no media; the harness uses its
	// in-memory test store and reuses the uploaded build digest.
	exportURL := env.server.URL + "/api/v2/admin/site/snapshots/" + childID + "/export"
	exportRequest, err := http.NewRequest(http.MethodGet, exportURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	exportRequest.Header.Set("Authorization", "Bearer "+env.adminToken)
	exportResponse, err := http.DefaultClient.Do(exportRequest)
	if err != nil {
		t.Fatalf("export child sitepack: %v", err)
	}
	archive, readErr := io.ReadAll(exportResponse.Body)
	_ = exportResponse.Body.Close()
	if readErr != nil {
		t.Fatalf("read child sitepack archive: %v", readErr)
	}
	if exportResponse.StatusCode != http.StatusOK {
		t.Fatalf("export child sitepack: status=%d body=%s", exportResponse.StatusCode, archive)
	}
	status, imported, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", env.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated {
		t.Fatalf("import child sitepack into the same disposable harness: status=%d body=%v", status, imported)
	}
	if imported["modelHistoryRevisions"] != float64(4) {
		t.Fatalf("sitepack report should retain four model history revisions: %v", imported)
	}
	importedSnapshot := imported["snapshot"].(map[string]any)
	importedID := importedSnapshot["id"].(string)
	importedBase := "/api/v2/admin/content/snapshots/" + importedID + "/model"
	status, importedWorkspace, _ := env.request(http.MethodGet, importedBase, env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read imported singleton workspace: status=%d body=%v", status, importedWorkspace)
	}
	importedModel := importedWorkspace["dataModel"].(map[string]any)
	importedManyID := modelSingletonAcceptanceEntityID(t, importedModel, "article")
	if cardinality := modelSingletonAcceptanceEntityCardinality(t, importedModel, "article"); cardinality != "singleton" {
		t.Fatalf("sitepack import lost the current singleton cardinality: %q", cardinality)
	}
	var importedFirstHistoryRaw []byte
	if err := env.pool.QueryRow(ctx, `SELECT records FROM snapshot_model_content_publications WHERE snapshot_id=$1 AND content_revision=1`, importedID).Scan(&importedFirstHistoryRaw); err != nil {
		t.Fatalf("read imported first publication history: %v", err)
	}
	var importedFirstHistory []any
	if err := json.Unmarshal(importedFirstHistoryRaw, &importedFirstHistory); err != nil {
		t.Fatalf("decode imported first publication history: %v", err)
	}
	if got := modelSingletonAcceptanceRecordIDs(t, importedFirstHistory, importedManyID); len(got) != 2 {
		t.Fatalf("sitepack should preserve historic two-row many data despite current singleton cardinality: %v", importedFirstHistory)
	}
	modelSingletonAcceptanceAssertDevAssignment(t, env, rootID)
}

func modelSingletonAcceptanceRuntimeContent(t *testing.T, env *testEnv) map[string]any {
	t.Helper()
	placement := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/")
	content, ok := placement["content"].(map[string]any)
	if !ok {
		t.Fatalf("runtime placement omitted model-backed content: %v", placement)
	}
	return content
}

func modelSingletonAcceptanceAssertProjection(t *testing.T, content map[string]any, recordID, title string, collectionCount int) {
	t.Helper()
	record, ok := content["record"].(map[string]any)
	if !ok || record["id"] != recordID || record["title"] != title {
		t.Fatalf("singleton record projection mismatch: %v", content)
	}
	if content["scalar"] != title {
		t.Fatalf("singleton scalar projection mismatch: %v", content)
	}
	collection, ok := content["collection"].([]any)
	if !ok || len(collection) != collectionCount {
		t.Fatalf("singleton collection projection count = %v, want %d", content["collection"], collectionCount)
	}
	if collectionCount == 1 {
		item, ok := collection[0].(map[string]any)
		if !ok || item["id"] != recordID || item["title"] != title {
			t.Fatalf("singleton collection projection mismatch: %v", collection)
		}
	}
}

func modelSingletonAcceptanceRecordIDs(t *testing.T, records []any, entityID string) []string {
	t.Helper()
	ids := make([]string, 0)
	for _, raw := range records {
		record, ok := raw.(map[string]any)
		if !ok || record["entityId"] != entityID {
			continue
		}
		id, ok := record["id"].(string)
		if !ok {
			t.Fatalf("model content record has no string ID: %v", raw)
		}
		ids = append(ids, id)
	}
	return ids
}

func modelSingletonAcceptanceChildConfig(t *testing.T, source map[string]any, entityID string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	model := config["dataModel"].(map[string]any)
	entities := model["entities"].([]any)
	for _, rawEntity := range entities {
		entity := rawEntity.(map[string]any)
		if entity["id"] == entityID {
			entity["cardinality"] = "singleton"
			return config
		}
	}
	t.Fatalf("model fixture has no entity %q", entityID)
	return nil
}

func modelSingletonAcceptanceSnapshotCount(t *testing.T, env *testEnv) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM site_snapshots`).Scan(&count); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return count
}

func modelSingletonAcceptanceAssertDevAssignment(t *testing.T, env *testEnv, snapshotID string) {
	t.Helper()
	var assigned *uuid.UUID
	if err := env.pool.QueryRow(context.Background(), `SELECT active_snapshot_id FROM site_environments WHERE key=$1`, string(env.devBranch)).Scan(&assigned); err != nil {
		t.Fatalf("read dev branch snapshot assignment: %v", err)
	}
	if assigned == nil || assigned.String() != snapshotID {
		t.Fatalf("content, failed forks, or Sitepack import changed dev branch assignment: got=%v want=%s", assigned, snapshotID)
	}
}

func modelSingletonAcceptanceContentRevision(t *testing.T, env *testEnv, snapshotID string) int64 {
	t.Helper()
	var revision int64
	if err := env.pool.QueryRow(context.Background(), `SELECT content_revision FROM site_snapshots WHERE id=$1`, snapshotID).Scan(&revision); err != nil {
		t.Fatalf("read parent content revision: %v", err)
	}
	return revision
}

func modelSingletonAcceptanceEntityID(t *testing.T, model map[string]any, key string) string {
	t.Helper()
	entities := model["entities"].([]any)
	for _, raw := range entities {
		entity := raw.(map[string]any)
		if entity["key"] == key {
			id, ok := entity["id"].(string)
			if !ok || uuid.Validate(id) != nil {
				t.Fatalf("imported entity %q has invalid remapped ID: %v", key, entity)
			}
			return id
		}
	}
	t.Fatalf("imported model omitted entity key %q: %v", key, model)
	return ""
}

func modelSingletonAcceptanceEntityCardinality(t *testing.T, model map[string]any, key string) string {
	t.Helper()
	for _, raw := range model["entities"].([]any) {
		entity := raw.(map[string]any)
		if entity["key"] == key {
			cardinality, _ := entity["cardinality"].(string)
			return cardinality
		}
	}
	t.Fatalf("model omitted entity key %q", key)
	return ""
}
