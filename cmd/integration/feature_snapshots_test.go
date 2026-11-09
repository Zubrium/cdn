package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

func featureArtifact(t *testing.T, manifest map[string]any, entry string) []byte {
	t.Helper()
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, asset := range []struct {
		name string
		data []byte
	}{{name: "manifest.json", data: manifestRaw}, {name: "entry.js", data: []byte(entry)}} {
		file, err := writer.Create(asset.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(asset.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func uploadFeatureBuild(t *testing.T, env *testEnv, manifest map[string]any, entry string) map[string]any {
	t.Helper()
	status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken,
		bytes.NewReader(featureArtifact(t, manifest, entry)), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("upload feature build: status=%d body=%v", status, body)
	}
	return body
}

// testFeatureManifestFor builds a current format-2 feature with no CMS inputs.
// Model-backed fixtures declare dataInputs explicitly and bind them in config.
func testFeatureManifestFor(key, version string) map[string]any {
	return map[string]any{
		"formatVersion": 2,
		"key":           key,
		"name":          key,
		"version":       version,
		"engineApi":     "2",
		"runtime":       map[string]any{"abi": "static-module-v1", "entry": "entry.js"},
		"slots":         []string{"main"},
		"arguments":     map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"dataInputs":    []any{},
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func singlePageConfig(key, version string) map[string]any {
	return map[string]any{
		"formatVersion": 3,
		"pages": []any{map[string]any{
			"id": "home", "name": "Home", "kind": "page",
			"placements": []any{map[string]any{
				"id": "single", "build": map[string]any{"featureKey": key, "version": version}, "slot": "main",
			}},
		}},
		"routeTree": map[string]any{"id": "root", "type": "site", "children": []any{
			map[string]any{"id": "home-route", "type": "path", "path": "/", "pageId": "home", "children": []any{}},
		}},
	}
}

func saveAndDeploySnapshot(t *testing.T, env *testEnv, config map[string]any, name string) map[string]any {
	t.Helper()
	status, snapshot, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{"name": name, "config": config})
	if status != http.StatusCreated {
		t.Fatalf("create site snapshot: status=%d body=%v", status, snapshot)
	}
	snapshotID, err := uuid.Parse(snapshot["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	branchID, err := uuid.Parse(string(env.devBranch))
	if err != nil {
		t.Fatal(err)
	}
	branch, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil {
		t.Fatalf("load integration branch: %v", err)
	}
	contentRevision := int64(snapshot["contentRevision"].(float64))
	if _, err := env.snapshots.AssignBranch(context.Background(), testActor(t, env), branchID, snapshotID, branch.Revision, &contentRevision); err != nil {
		t.Fatalf("assign snapshot to integration branch: %v", err)
	}
	return snapshot
}

func assignSnapshotToBranch(t *testing.T, env *testEnv, branchID, snapshotID string) map[string]any {
	t.Helper()
	parsedBranchID, err := uuid.Parse(branchID)
	if err != nil {
		t.Fatal(err)
	}
	parsedSnapshotID, err := uuid.Parse(snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := env.snapshots.Branch(context.Background(), parsedBranchID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := env.snapshots.Snapshot(context.Background(), parsedSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	status, assigned, _ := env.request(http.MethodPut, "/api/v2/admin/branches/"+branchID+"/snapshot", env.adminToken,
		map[string]any{"snapshotId": snapshotID, "revision": branch.Revision, "contentRevision": snapshot.Snapshot.ContentRevision})
	if status != http.StatusOK || assigned["snapshotId"] != snapshotID {
		t.Fatalf("assign snapshot %s to branch %s: status=%d body=%v", snapshotID, branchID, status, assigned)
	}
	return assigned
}

func createChildSnapshot(t *testing.T, env *testEnv, parentID, name string) map[string]any {
	t.Helper()
	status, snapshot, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{"name": name, "parentSnapshotId": parentID})
	if status != http.StatusCreated {
		t.Fatalf("create child snapshot: status=%d body=%v", status, snapshot)
	}
	return snapshot
}

func runtimePlacement(t *testing.T, env *testEnv, serverURL, path string) map[string]any {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+"/api/v2/runtime?path="+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get runtime composition: status=%d body=%s", response.StatusCode, raw)
	}
	var composition map[string]any
	if err := json.Unmarshal(raw, &composition); err != nil {
		t.Fatal(err)
	}
	placements, ok := composition["placements"].([]any)
	if !ok || len(placements) != 1 {
		t.Fatalf("expected one runtime placement, got %v", composition["placements"])
	}
	return placements[0].(map[string]any)
}

func testActor(t *testing.T, env *testEnv) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.pool.QueryRow(context.Background(), `SELECT id FROM access_tokens WHERE name='bootstrap admin' AND revoked_at IS NULL`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFeatureBuildListReturnsEmptyArray(t *testing.T) {
	env := newTestEnv(t)
	status, body, _ := env.request(http.MethodGet, "/api/v2/admin/feature-builds", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list feature builds: status=%d body=%v", status, body)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("feature build items must be an empty array, got %T (%v)", body["items"], body["items"])
	}
}

func TestSnapshotEventHistoryPagesAreIndependentAndStable(t *testing.T) {
	env := newTestEnv(t)
	key := "history-pagination"
	uploadFeatureBuild(t, env, testFeatureManifestFor(key, "2.0.0"), "export default function Feature() {}")
	snapshot := saveAndDeploySnapshot(t, env, singlePageConfig(key, "2.0.0"), "history")
	id := snapshot["id"].(string)
	child := createChildSnapshot(t, env, id, "history child")
	childID := child["id"].(string)
	assignSnapshotToBranch(t, env, string(env.devBranch), childID)
	assignSnapshotToBranch(t, env, string(env.prodBranch), id)

	status, first, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshot-events?branchId="+string(env.devBranch)+"&limit=1", env.adminToken, nil)
	if status != http.StatusOK || len(first["items"].([]any)) != 1 || first["nextCursor"] == nil {
		t.Fatalf("first history page: %d %v", status, first)
	}
	firstID := first["items"].([]any)[0].(map[string]any)["id"].(string)
	cursor := first["nextCursor"].(string)
	assignSnapshotToBranch(t, env, string(env.devBranch), id)
	status, second, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshot-events?branchId="+string(env.devBranch)+"&limit=1&before="+cursor, env.adminToken, nil)
	if status != http.StatusOK || len(second["items"].([]any)) != 1 || second["items"].([]any)[0].(map[string]any)["id"] == firstID {
		t.Fatalf("stable second page: %d %v", status, second)
	}
	status, invalid, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshot-events?branchId="+string(env.prodBranch)+"&before="+cursor, env.adminToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("cursor from another branch must fail: %d %v", status, invalid)
	}
	status, production, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshot-events?branchId="+string(env.prodBranch), env.adminToken, nil)
	if status != http.StatusOK || len(production["items"].([]any)) != 1 || production["items"].([]any)[0].(map[string]any)["environment"] != string(env.prodBranch) {
		t.Fatalf("production history: %d %v", status, production)
	}
	status, token, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken, map[string]any{"name": "content user", "role": "user"})
	if status != http.StatusCreated {
		t.Fatalf("create user: %d %v", status, token)
	}
	status, forbidden, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshot-events?branchId="+string(env.devBranch), token["token"].(string), nil)
	if status != http.StatusForbidden {
		t.Fatalf("history requires site manager: %d %v", status, forbidden)
	}
}

func assertSnapshotStructuralDigest(t *testing.T, env *testEnv, snapshotID string) {
	t.Helper()
	detail, err := env.snapshots.Snapshot(context.Background(), uuid.MustParse(snapshotID))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := sitebuilder.SnapshotDigest(detail.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if digest != detail.Snapshot.Digest {
		t.Fatalf("structural digest mismatch: %s != %s", digest, detail.Snapshot.Digest)
	}
}
