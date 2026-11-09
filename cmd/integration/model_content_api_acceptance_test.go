package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/files"
	pkgconfig "websiteengine/pkg/config"
)

// This test intentionally requires a task-unique local database because
// newTestEnv migrates and truncates every integration table before use.
func TestModelContentAPIWorkspaceLifecycle(t *testing.T) {
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	ctx := context.Background()

	modelIDs := map[string]string{
		"product": uuid.NewString(), "slug": uuid.NewString(), "title": uuid.NewString(),
		"price": uuid.NewString(), "status": uuid.NewString(), "image": uuid.NewString(),
		"category": uuid.NewString(), "categoryName": uuid.NewString(), "categoryProducts": uuid.NewString(),
	}
	manifest := map[string]any{
		"formatVersion": 2,
		"key":           "model-content-acceptance",
		"name":          "Model content acceptance",
		"version":       "1.0.0",
		"engineApi":     "2",
		"runtime": map[string]any{
			"abi": "static-module-v1", "entry": "entry.js",
		},
		"slots":     []string{"main"},
		"arguments": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"dataInputs": []any{map[string]any{
			"key": "products", "kind": "collection", "fields": []any{
				map[string]any{"path": "/slug", "type": "text"},
				map[string]any{"path": "/title", "type": "text"},
				map[string]any{"path": "/price", "type": "number"},
				map[string]any{"path": "/status", "type": "enum", "values": []string{"active", "hidden"}},
				map[string]any{"path": "/image", "type": "media"},
			},
		}},
	}
	modelAcceptanceUploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")

	model := map[string]any{
		"formatVersion": 1,
		"entities": []any{
			map[string]any{"id": modelIDs["product"], "key": "product", "name": "Product", "fields": []any{
				map[string]any{"id": modelIDs["slug"], "key": "slug", "name": "Slug", "type": "text", "unique": true},
				map[string]any{"id": modelIDs["title"], "key": "title", "name": "Title", "type": "text"},
				map[string]any{"id": modelIDs["price"], "key": "price", "name": "Price", "type": "number"},
				map[string]any{"id": modelIDs["status"], "key": "status", "name": "Status", "type": "enum", "options": []string{"active", "hidden"}},
				map[string]any{"id": modelIDs["image"], "key": "image", "name": "Image", "type": "media"},
			}},
			map[string]any{"id": modelIDs["category"], "key": "category", "name": "Category", "fields": []any{
				map[string]any{"id": modelIDs["categoryName"], "key": "name", "name": "Name", "type": "text"},
			}},
		},
		"relations": []any{map[string]any{
			"id": modelIDs["categoryProducts"], "name": "Products", "fromEntityId": modelIDs["category"],
			"toEntityId": modelIDs["product"], "cardinality": "one-to-many",
		}},
	}
	bindings := []any{map[string]any{
		"inputKey": "products", "entityId": modelIDs["product"],
		"fields": []any{
			map[string]any{"inputPath": "/slug", "fieldId": modelIDs["slug"]},
			map[string]any{"inputPath": "/title", "fieldId": modelIDs["title"]},
			map[string]any{"inputPath": "/price", "fieldId": modelIDs["price"]},
			map[string]any{"inputPath": "/status", "fieldId": modelIDs["status"]},
			map[string]any{"inputPath": "/image", "fieldId": modelIDs["image"]},
		},
	}}
	config := map[string]any{
		"formatVersion": 2,
		"dataModel":     model,
		"pages": []any{map[string]any{
			"id": "home", "name": "Home", "kind": "page",
			"placements": []any{map[string]any{
				"id": "products-placement", "build": map[string]any{"featureKey": "model-content-acceptance", "version": "1.0.0"},
				"slot": "main", "dataBindings": bindings,
			}},
		}},
		"routeTree": map[string]any{"id": "root", "type": "index", "pageId": "home"},
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, config, "Model content API acceptance")
	snapshotID := snapshot["id"].(string)
	base := "/api/v2/admin/content/snapshots/" + snapshotID + "/model"
	assertRetiredContentAPIUnavailable(t, env, snapshotID)

	status, _, _ := env.request(http.MethodGet, base, "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous GET should be unauthorized: %d", status)
	}
	userStatus, userBody, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken,
		map[string]any{"name": "model content editor", "role": "user"})
	if userStatus != http.StatusCreated {
		t.Fatalf("create content editor: status=%d body=%v", userStatus, userBody)
	}
	userToken := userBody["token"].(string)

	status, empty, _ := env.request(http.MethodGet, base, userToken, nil)
	if status != http.StatusOK {
		t.Fatalf("empty model workspace: status=%d body=%v", status, empty)
	}
	if empty["revision"] != float64(0) || empty["publishedRevision"] != float64(0) || len(empty["records"].([]any)) != 0 || len(empty["selections"].([]any)) != 0 {
		t.Fatalf("new workspace should have zero revisions and no records/selections: %v", empty)
	}
	var workspaceRows int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM site_snapshot_model_workspaces WHERE snapshot_id=$1`, snapshotID).Scan(&workspaceRows); err != nil || workspaceRows != 1 {
		t.Fatalf("every model-backed snapshot must have one lockable workspace row: rows=%d err=%v", workspaceRows, err)
	}
	for _, retiredPayload := range []map[string]any{
		{"revision": 0, "items": []any{}},
		{"revision": 0, "records": []any{}, "relationEnvelope": map[string]any{"version": 1, "targets": map[string]any{}}},
	} {
		status, response, _ := env.request(http.MethodPut, base, userToken, retiredPayload)
		if status != http.StatusBadRequest {
			t.Fatalf("retired per-feature content or relation-envelope payload should be rejected: status=%d body=%v payload=%v", status, response, retiredPayload)
		}
	}
	status, unchanged, _ := env.request(http.MethodGet, base, userToken, nil)
	if status != http.StatusOK || unchanged["revision"] != float64(0) {
		t.Fatalf("rejected legacy payload changed model workspace: status=%d body=%v", status, unchanged)
	}

	fileService, err := files.NewService(env.pool, env.store, pkgconfig.FilesConfig{
		MaxBytes: 20 << 20, PartBytes: 20 << 20, UploadTTL: time.Hour, CleanupInterval: time.Minute,
		ProcessingTimeout: time.Minute, OwnerQuotaBytes: 40 << 20, MaxSessions: 4, ListLimit: 20,
		RateLimit: 100, RateMaxClients: 100, RateWindow: time.Minute, LinkMaxTTL: time.Minute,
		PublicBaseURL: "https://files.example.test", TempDir: t.TempDir(), SigningKey: bytes.Repeat([]byte("f"), 32),
	})
	if err != nil {
		t.Fatalf("create disposable file API service: %v", err)
	}
	if err := fileService.InitOwners(ctx); err != nil {
		t.Fatalf("initialize disposable file owners: %v", err)
	}
	fileService.SetReferenceManager(env.snapshots)
	imageBytes := minimalPNG(t)
	image, err := fileService.UploadStream(ctx, fileService.AdminOwner(), "model-product.png", int64(len(imageBytes)), "public", bytes.NewReader(imageBytes))
	if err != nil {
		t.Fatalf("upload disposable model image: %v", err)
	}
	imageID := image.ID.String()
	category := map[string]any{
		"entityId": modelIDs["category"], "sortOrder": 0,
		"values": map[string]any{"name": "Fruit"},
	}
	firstProduct := map[string]any{
		"entityId": modelIDs["product"], "sortOrder": 0,
		"values": map[string]any{"slug": "pear", "title": "Pear", "price": 2.5, "status": "active", "image": imageID},
	}
	secondProduct := map[string]any{
		"entityId": modelIDs["product"], "sortOrder": 1,
		"values": map[string]any{"slug": "apple", "title": "Apple", "price": 3, "status": "active", "image": imageID},
	}
	status, draft, _ := env.request(http.MethodPut, base, userToken, map[string]any{
		"revision": 0, "records": []any{category, firstProduct, secondProduct},
	})
	if status != http.StatusOK {
		t.Fatalf("save first model draft: status=%d body=%v", status, draft)
	}
	if draft["revision"] != float64(1) || draft["publishedRevision"] != float64(0) {
		t.Fatalf("draft save should advance only the draft revision: %v", draft)
	}
	records := draft["records"].([]any)
	if len(records) != 3 {
		t.Fatalf("expected three saved model records, got %d", len(records))
	}
	idsByEntityAndSlug := map[string]string{}
	categoryID := ""
	for _, raw := range records {
		record := raw.(map[string]any)
		id, ok := record["id"].(string)
		if !ok || uuid.Validate(id) != nil {
			t.Fatalf("server should generate canonical record UUIDs: %v", record)
		}
		if record["entityId"] == modelIDs["category"] {
			categoryID = id
			continue
		}
		values := record["values"].(map[string]any)
		idsByEntityAndSlug[values["slug"].(string)] = id
	}
	if categoryID == "" || idsByEntityAndSlug["pear"] == "" || idsByEntityAndSlug["apple"] == "" {
		t.Fatalf("draft response omitted generated record identities: %v", records)
	}
	status, overview, _ := env.request(http.MethodGet, "/api/v1/admin/overview", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("overview after saving model draft: status=%d body=%v", status, overview)
	}
	draftCounts := overview["counts"].(map[string]any)
	draftAttention := overview["attention"].(map[string]any)["draft_model_snapshots"].([]any)
	if draftCounts["draft_model_snapshots"] != float64(1) || draftCounts["draft_model_records"] != float64(3) || len(draftAttention) != 1 {
		t.Fatalf("dashboard should report the model draft by snapshot and record count: %v", overview)
	}
	draftSummary := draftAttention[0].(map[string]any)
	if draftSummary["snapshot_id"] != snapshotID || draftSummary["name"] != "Model content API acceptance" {
		t.Fatalf("dashboard draft should link to the owning snapshot: %v", draftSummary)
	}

	// Invalid drafts must not advance revisions or replace the caller's current data.
	invalidDrafts := []struct {
		name   string
		mutate func([]any)
		want   string
	}{
		{name: "unique slug collision", mutate: func(items []any) {
			items[2].(map[string]any)["values"].(map[string]any)["slug"] = "pear"
		}, want: "unique"},
		{name: "wrong supplied scalar type", mutate: func(items []any) {
			items[1].(map[string]any)["values"].(map[string]any)["price"] = "not-a-number"
		}, want: "price"},
		{name: "unknown enum choice", mutate: func(items []any) {
			items[1].(map[string]any)["values"].(map[string]any)["status"] = "retired"
		}, want: "status"},
		{name: "unknown media object", mutate: func(items []any) {
			items[1].(map[string]any)["values"].(map[string]any)["image"] = uuid.NewString()
		}, want: "image"},
		{name: "relation points to missing record", mutate: func(items []any) {
			items[0].(map[string]any)["relations"] = map[string]any{modelIDs["categoryProducts"]: []string{uuid.NewString()}}
		}, want: "relation"},
	}
	for _, test := range invalidDrafts {
		t.Run(test.name, func(t *testing.T) {
			items := cloneModelContentJSONArray(t, records)
			test.mutate(items)
			status, body, _ := env.request(http.MethodPut, base, userToken, map[string]any{"revision": 1, "records": items})
			if status != http.StatusBadRequest {
				t.Fatalf("invalid draft status=%d body=%v", status, body)
			}
			status, current, _ := env.request(http.MethodGet, base, userToken, nil)
			if status != http.StatusOK || current["revision"] != float64(1) {
				t.Fatalf("invalid draft changed workspace revision/data: status=%d body=%v", status, current)
			}
			errorEnvelope, _ := body["error"].(map[string]any)
			message, _ := errorEnvelope["message"].(string)
			if test.want != "" && !strings.Contains(strings.ToLower(message), test.want) {
				t.Fatalf("invalid draft error should identify %q: %v", test.want, body)
			}
		})
	}

	relatedRecords := cloneModelContentJSONArray(t, records)
	relatedRecords[0].(map[string]any)["relations"] = map[string]any{modelIDs["categoryProducts"]: []string{idsByEntityAndSlug["pear"], idsByEntityAndSlug["apple"]}}
	selection := map[string]any{
		"placementId": "products-placement", "inputKey": "products", "mode": "ids",
		"recordIds": []string{idsByEntityAndSlug["apple"], idsByEntityAndSlug["pear"]},
	}
	status, relatedDraft, _ := env.request(http.MethodPut, base, userToken, map[string]any{
		"revision": 1, "records": relatedRecords, "selections": []any{selection},
	})
	if status != http.StatusOK || relatedDraft["revision"] != float64(2) {
		t.Fatalf("save valid relation and ordered selection: status=%d body=%v", status, relatedDraft)
	}

	staleRecords := cloneModelContentJSONArray(t, relatedDraft["records"].([]any))
	staleRecords[1].(map[string]any)["values"].(map[string]any)["title"] = "Stale local edit"
	status, conflict, _ := env.request(http.MethodPut, base, userToken, map[string]any{"revision": 1, "records": staleRecords})
	if status != http.StatusConflict {
		t.Fatalf("stale revision should conflict: status=%d body=%v", status, conflict)
	}
	status, current, _ := env.request(http.MethodGet, base, userToken, nil)
	if status != http.StatusOK || current["revision"] != float64(2) {
		t.Fatalf("stale write changed server revision: status=%d body=%v", status, current)
	}
	currentPear := modelContentRecordByID(t, current["records"].([]any), idsByEntityAndSlug["pear"])
	if currentPear["values"].(map[string]any)["title"] != "Pear" {
		t.Fatalf("stale write replaced the current server value: %v", currentPear)
	}
	if title := staleRecords[1].(map[string]any)["values"].(map[string]any)["title"]; title != "Stale local edit" {
		t.Fatalf("client-local value was unexpectedly mutated by request helper: %v", title)
	}

	status, _, _ = env.request(http.MethodPost, base+"/publish", userToken, map[string]any{"revision": 2})
	if status != http.StatusOK {
		t.Fatalf("content editor must be able to publish: %d", status)
	}
	status, published, _ := env.request(http.MethodGet, base, env.adminToken, nil)
	if status != http.StatusOK || published["publishedRevision"] != float64(2) {
		t.Fatalf("published model revision should be visible to admin: status=%d body=%v", status, published)
	}
	status, overview, _ = env.request(http.MethodGet, "/api/v1/admin/overview", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("overview after model publication: status=%d body=%v", status, overview)
	}
	if overview["counts"].(map[string]any)["draft_model_snapshots"] != float64(0) {
		t.Fatalf("published model workspace should leave dashboard draft queue: %v", overview)
	}
	assertModelProductsRuntime(t, env, "Apple", "Pear")

	changedRecords := cloneModelContentJSONArray(t, published["records"].([]any))
	modelContentRecordByID(t, changedRecords, idsByEntityAndSlug["pear"])["values"].(map[string]any)["title"] = "Green Pear"
	status, secondDraft, _ := env.request(http.MethodPut, base, userToken, map[string]any{
		"revision": 2, "records": changedRecords, "selections": []any{selection},
	})
	if status != http.StatusOK || secondDraft["revision"] != float64(3) {
		t.Fatalf("save second draft: status=%d body=%v", status, secondDraft)
	}
	assertModelProductsRuntime(t, env, "Apple", "Pear")
	status, secondPublished, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": 3})
	if status != http.StatusOK || secondPublished["publishedRevision"] != float64(3) {
		t.Fatalf("publish second revision: status=%d body=%v", status, secondPublished)
	}
	assertModelProductsRuntime(t, env, "Apple", "Green Pear")

	status, history, _ := env.request(http.MethodGet, base+"/history", userToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read model content history: status=%d body=%v", status, history)
	}
	historyItems := history["items"].([]any)
	if len(historyItems) != 2 {
		t.Fatalf("expected two model publications, got %v", historyItems)
	}
	firstPublicationID := historyItems[1].(map[string]any)["id"].(string)
	status, restored, _ := env.request(http.MethodPost, base+"/history/"+firstPublicationID+"/restore", userToken, map[string]any{"revision": 3})
	if status != http.StatusOK || restored["revision"] != float64(4) || restored["publishedRevision"] != float64(3) {
		t.Fatalf("restore should write a new draft without publishing: status=%d body=%v", status, restored)
	}
	restoredPear := modelContentRecordByID(t, restored["records"].([]any), idsByEntityAndSlug["pear"])
	if restoredPear["values"].(map[string]any)["title"] != "Pear" {
		t.Fatalf("restored draft did not recover historical value: %v", restoredPear)
	}
	assertModelProductsRuntime(t, env, "Apple", "Green Pear")

	child := modelAcceptanceCreateChildSnapshot(t, env, snapshotID, "Model content child")
	childID := child["id"].(string)
	childBase := "/api/v2/admin/content/snapshots/" + childID + "/model"
	status, childWorkspace, _ := env.request(http.MethodGet, childBase, userToken, nil)
	if status != http.StatusOK || childWorkspace["revision"] != float64(4) || childWorkspace["publishedRevision"] != float64(3) {
		t.Fatalf("fork should copy model draft and published revisions: status=%d body=%v", status, childWorkspace)
	}
	if len(childWorkspace["records"].([]any)) != 3 {
		t.Fatalf("fork should copy model records: %v", childWorkspace["records"])
	}
	childRecords := cloneModelContentJSONArray(t, childWorkspace["records"].([]any))
	modelContentRecordByID(t, childRecords, idsByEntityAndSlug["pear"])["values"].(map[string]any)["title"] = "Child Pear"
	status, _, _ = env.request(http.MethodPut, childBase, userToken, map[string]any{"revision": 4, "records": childRecords, "selections": childWorkspace["selections"]})
	if status != http.StatusOK {
		t.Fatalf("save child model content: %d", status)
	}
	status, parentAfterChild, _ := env.request(http.MethodGet, base, userToken, nil)
	if status != http.StatusOK || parentAfterChild["revision"] != float64(4) {
		t.Fatalf("child write changed parent model workspace: status=%d body=%v", status, parentAfterChild)
	}
	parentPear := modelContentRecordByID(t, parentAfterChild["records"].([]any), idsByEntityAndSlug["pear"])
	if parentPear["values"].(map[string]any)["title"] != "Pear" {
		t.Fatalf("child changes leaked back to parent snapshot: %v", parentPear)
	}
	branch, err := env.snapshots.Branch(ctx, uuid.MustParse(string(env.devBranch)))
	if err != nil || branch.SnapshotID == nil || branch.SnapshotID.String() != snapshotID {
		t.Fatalf("snapshot fork unexpectedly changed branch assignment: branch=%+v err=%v", branch, err)
	}

	imageUUID := uuid.MustParse(imageID)
	owner := fileService.AdminOwner()
	imageReferences, err := fileService.References(ctx, owner, imageUUID)
	if err != nil {
		t.Fatalf("read model media references: %v", err)
	}
	for _, kind := range []string{"model-content-draft", "model-content-published", "model-content-history"} {
		found := false
		for _, reference := range imageReferences {
			if reference.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("file reference warning omitted %q model data: %+v", kind, imageReferences)
		}
	}
	if err := fileService.DeleteIfReferencesUnchanged(ctx, owner, imageUUID, false, ""); !errors.Is(err, files.ErrInUse) {
		t.Fatalf("referenced model media deletion should require confirmation, got %v", err)
	}
	referencesRevision := files.ReferencesRevision(imageReferences)
	if err := fileService.DeleteIfReferencesUnchanged(ctx, owner, imageUUID, true, referencesRevision); err != nil {
		t.Fatalf("confirmed model media deletion: %v", err)
	}
	for _, snapshot := range []struct {
		id, path string
		revision float64
	}{{snapshotID, base, 5}, {childID, childBase, 6}} {
		status, workspace, _ := env.request(http.MethodGet, snapshot.path, userToken, nil)
		if status != http.StatusOK {
			t.Fatalf("load snapshot %s after media cleanup: status=%d body=%v", snapshot.id, status, workspace)
		}
		if workspace["revision"] != snapshot.revision {
			t.Fatalf("media cleanup should advance the model revision for snapshot %s: %v", snapshot.id, workspace)
		}
		for _, raw := range workspace["records"].([]any) {
			values := raw.(map[string]any)["values"].(map[string]any)
			if values["image"] == imageID {
				t.Fatalf("deleted file ID remains in snapshot %s draft values: %v", snapshot.id, values)
			}
		}
	}
	var retainedHistoryReferences int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM snapshot_model_content_publications WHERE records::text LIKE '%'||$1||'%'`, imageID).Scan(&retainedHistoryReferences); err != nil {
		t.Fatal(err)
	}
	if retainedHistoryReferences != 0 {
		t.Fatalf("deleted file remained in %d historical model publications", retainedHistoryReferences)
	}
}

func assertRetiredContentAPIUnavailable(t *testing.T, env *testEnv, snapshotID string) {
	t.Helper()
	for _, endpoint := range []string{
		"/api/v2/admin/content/snapshots/" + snapshotID + "/features",
		"/api/v2/admin/content/snapshots/" + snapshotID + "/features/catalog",
		"/api/v2/admin/site/snapshots/" + snapshotID + "/content-relations/placement/unlink-preview",
	} {
		status, body, _ := env.request(http.MethodGet, endpoint, "", nil)
		if status != http.StatusNotFound {
			t.Fatalf("retired per-feature/relations API %s should be absent, got status=%d body=%v", endpoint, status, body)
		}
	}
}

func modelContentRecordByID(t *testing.T, records []any, id string) map[string]any {
	t.Helper()
	for _, raw := range records {
		if record, ok := raw.(map[string]any); ok && record["id"] == id {
			return record
		}
	}
	t.Fatalf("model content record %q not found in %v", id, records)
	return nil
}

func cloneModelContentJSONArray(t *testing.T, values []any) []any {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var cloned []any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func assertModelProductsRuntime(t *testing.T, env *testEnv, titles ...string) {
	t.Helper()
	placement := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/")
	products := placement["content"].(map[string]any)["products"].([]any)
	if len(products) != len(titles) {
		t.Fatalf("public runtime products count = %d, want %d: %v", len(products), len(titles), products)
	}
	for index, raw := range products {
		product := raw.(map[string]any)
		if product["title"] != titles[index] {
			t.Fatalf("public runtime product[%d].title = %v, want %q", index, product["title"], titles[index])
		}
	}
}

func TestModelContentForkRequiresExplicitCleanupWhenStructureDropsField(t *testing.T) {
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	ctx := context.Background()
	entityID, titleID, retiredID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	manifest := map[string]any{
		"formatVersion": 2,
		"key":           "model-field-removal-acceptance",
		"name":          "Model field removal acceptance",
		"version":       "1.0.0",
		"engineApi":     "2",
		"runtime":       map[string]any{"abi": "static-module-v1", "entry": "entry.js"},
		"slots":         []string{"main"},
		"arguments":     map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"dataInputs": []any{map[string]any{
			"key": "items", "kind": "collection", "fields": []any{
				map[string]any{"path": "/title", "type": "text"},
				map[string]any{"path": "/legacyCaption", "type": "text"},
			},
		}},
	}
	modelAcceptanceUploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")
	makeConfig := func(keepRetiredField bool) map[string]any {
		fields := []any{map[string]any{"id": titleID, "key": "title", "name": "Название", "type": "text"}}
		retiredBindingFieldID := titleID
		if keepRetiredField {
			fields = append(fields, map[string]any{"id": retiredID, "key": "legacy_caption", "name": "Старый подзаголовок", "type": "text"})
			retiredBindingFieldID = retiredID
		}
		fieldBindings := []any{
			map[string]any{"inputPath": "/title", "fieldId": titleID},
			map[string]any{"inputPath": "/legacyCaption", "fieldId": retiredBindingFieldID},
		}
		return map[string]any{
			"formatVersion": 2,
			"dataModel": map[string]any{"formatVersion": 1, "entities": []any{map[string]any{
				"id": entityID, "key": "item", "name": "Элемент", "fields": fields,
			}}, "relations": []any{}},
			"pages": []any{map[string]any{
				"id": "home", "name": "Главная", "kind": "page",
				"placements": []any{map[string]any{
					"id": "items-placement", "build": map[string]any{"featureKey": "model-field-removal-acceptance", "version": "1.0.0"},
					"slot": "main", "dataBindings": []any{map[string]any{
						"inputKey": "items", "entityId": entityID, "fields": fieldBindings,
					}},
				}},
			}},
			"routeTree": map[string]any{"id": "root", "type": "index", "pageId": "home"},
		}
	}
	parent := modelAcceptanceSaveAndDeploySnapshot(t, env, makeConfig(true), "Before field removal")
	parentID := parent["id"].(string)
	parentBase := "/api/v2/admin/content/snapshots/" + parentID + "/model"
	status, savedParent, _ := env.request(http.MethodPut, parentBase, env.adminToken, map[string]any{
		"revision": 0,
		"records": []any{map[string]any{
			"entityId": entityID, "sortOrder": 0,
			"values": map[string]any{"title": "Сохранённый товар", "legacy_caption": "Старое значение"},
		}},
	})
	if status != http.StatusOK || savedParent["revision"] != float64(1) {
		t.Fatalf("save parent model content: status=%d body=%v", status, savedParent)
	}
	recordID := savedParent["records"].([]any)[0].(map[string]any)["id"].(string)
	status, publishedParent, _ := env.request(http.MethodPost, parentBase+"/publish", env.adminToken, map[string]any{"revision": 1})
	if status != http.StatusOK || publishedParent["publishedRevision"] != float64(1) {
		t.Fatalf("publish parent model content: status=%d body=%v", status, publishedParent)
	}
	status, child, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{
		"name": "After field removal", "parentSnapshotId": parentID, "config": makeConfig(false),
	})
	if status != http.StatusCreated {
		t.Fatalf("create child without field: status=%d body=%v", status, child)
	}
	warnings, _ := child["warnings"].([]any)
	if !strings.Contains(strings.Join(anyStrings(warnings), " "), "устаревшие данные") {
		t.Fatalf("child snapshot should warn that inherited data needs explicit cleanup: %v", child)
	}
	childID := child["id"].(string)
	childBase := "/api/v2/admin/content/snapshots/" + childID + "/model"
	status, inherited, _ := env.request(http.MethodGet, childBase, env.adminToken, nil)
	if status != http.StatusOK || inherited["revision"] != float64(1) {
		t.Fatalf("read copied child content: status=%d body=%v", status, inherited)
	}
	inheritedRecord := modelContentRecordByID(t, inherited["records"].([]any), recordID)
	if inheritedRecord["values"].(map[string]any)["legacy_caption"] != "Старое значение" {
		t.Fatalf("fork silently discarded a field value: %v", inheritedRecord)
	}
	status, rejected, _ := env.request(http.MethodPut, childBase, env.adminToken, map[string]any{
		"revision": inherited["revision"], "records": inherited["records"], "selections": inherited["selections"],
	})
	if status != http.StatusBadRequest {
		t.Fatalf("child must reject stale field data until explicitly cleaned: status=%d body=%v", status, rejected)
	}
	values := inheritedRecord["values"].(map[string]any)
	delete(values, "legacy_caption")
	status, cleaned, _ := env.request(http.MethodPut, childBase, env.adminToken, map[string]any{
		"revision": inherited["revision"], "records": inherited["records"], "selections": inherited["selections"],
	})
	if status != http.StatusOK || cleaned["revision"] != float64(2) {
		t.Fatalf("save explicit field cleanup: status=%d body=%v", status, cleaned)
	}
	status, publishedChild, _ := env.request(http.MethodPost, childBase+"/publish", env.adminToken, map[string]any{"revision": 2})
	if status != http.StatusOK || publishedChild["publishedRevision"] != float64(2) {
		t.Fatalf("publish cleaned child content: status=%d body=%v", status, publishedChild)
	}
	status, unchangedParent, _ := env.request(http.MethodGet, parentBase, env.adminToken, nil)
	if status != http.StatusOK || modelContentRecordByID(t, unchangedParent["records"].([]any), recordID)["values"].(map[string]any)["legacy_caption"] != "Старое значение" {
		t.Fatalf("field cleanup in the child changed its parent: status=%d body=%v", status, unchangedParent)
	}
	if _, err := env.snapshots.Snapshot(ctx, uuid.MustParse(childID)); err != nil {
		t.Fatalf("load child after explicit cleanup: %v", err)
	}
}

func anyStrings(values []any) []string {
	strings := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if ok {
			strings = append(strings, text)
		}
	}
	return strings
}

func requireDisposableModelContentDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	if raw == "" || err != nil {
		t.Skip("set TEST_DATABASE_URL to a unique local website_engine_model_content_qa_* disposable database")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(databaseName, "website_engine_model_content_qa_") || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Skip("model content integration requires a local website_engine_model_content_qa_* database; refusing any other target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to disposable model-content database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var otherSessions int
	if err := connection.QueryRow(ctx, `SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&currentDatabase, &otherSessions); err != nil {
		t.Fatalf("verify disposable model-content database: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("connected database %q differs from requested disposable database %q", currentDatabase, databaseName)
	}
	if otherSessions != 0 {
		t.Fatalf("refusing integration TRUNCATE: disposable database %q already has %d other active sessions", databaseName, otherSessions)
	}
	t.Logf("verified local disposable database %s has no other sessions before integration setup", currentDatabase)
}
