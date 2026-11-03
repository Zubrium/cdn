package integration

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// This opt-in test uses a real feature archive supplied by the caller. The
// regular integration suite remains independent of any particular site.
func TestExternalFeatureContentWorkspace(t *testing.T) {
	path := os.Getenv("TEST_FEATURE_ZIP")
	if path == "" {
		t.Skip("TEST_FEATURE_ZIP is not set")
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	env := newTestEnv(t)
	status, build, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("upload feature: %d %v", status, build)
	}
	key, version := build["key"].(string), build["version"].(string)
	if key != "header" {
		t.Fatalf("this fixture expects the header feature, got %q", key)
	}
	config := singlePageConfig(key, version)
	page := config["pages"].([]any)[0].(map[string]any)
	page["placements"].([]any)[0].(map[string]any)["slot"] = "header"
	snapshot := saveAndDeploySnapshot(t, env, config, "real feature")
	workspacePath := "/api/v2/admin/content/snapshots/" + snapshot["id"].(string) + "/features/" + key
	status, _, _ = env.request(http.MethodPut, workspacePath, env.adminToken, map[string]any{
		"revision": 0, "items": []any{map[string]any{"singleSlot": true, "data": map[string]any{
			"brand": "Vitasil", "brandTo": "/", "navigationLabel": "Navigation",
			"navigation": []any{map[string]any{"label": "Catalog", "to": "/catalog", "end": false}},
		}}},
	})
	if status != http.StatusOK {
		t.Fatalf("save real header schema: status=%d", status)
	}
	status, workspace, _ := env.request(http.MethodGet, workspacePath, env.adminToken, nil)
	if status != http.StatusOK || workspace["revision"] != float64(1) {
		t.Fatalf("reopen header workspace: %d %v", status, workspace)
	}
	items := workspace["items"].([]any)
	data := items[0].(map[string]any)["data"].(map[string]any)
	if data["brand"] != "Vitasil" || len(data["navigation"].([]any)) != 1 {
		t.Fatalf("reopened data: %v", data)
	}
	sourceID := snapshot["id"].(string)
	forkPath := "/api/v2/admin/content/snapshots/" + sourceID + "/fork"
	status, versionB, _ := env.request(http.MethodPost, forkPath, env.adminToken, map[string]any{"name": "B"})
	if status != http.StatusCreated {
		t.Fatalf("fork B: %d %v", status, versionB)
	}
	item := items[0].(map[string]any)
	data["brand"] = "Vitasil C"
	input := map[string]any{"entryId": item["entryId"], "singleSlot": true, "data": data}
	status, stale, _ := env.request(http.MethodPut, workspacePath, env.adminToken, map[string]any{"revision": 0, "items": []any{input}})
	if status != http.StatusConflict {
		t.Fatalf("stale save must conflict: %d %v", status, stale)
	}
	results := make(chan int, 2)
	for range 2 {
		go func() {
			status, _, _ := env.request(http.MethodPut, workspacePath, env.adminToken, map[string]any{"revision": 1, "items": []any{input}})
			results <- status
		}()
	}
	first, second := <-results, <-results
	if !((first == http.StatusOK && second == http.StatusConflict) || (second == http.StatusOK && first == http.StatusConflict)) {
		t.Fatalf("concurrent saves must produce one success and one conflict: %d, %d", first, second)
	}
	status, versionC, _ := env.request(http.MethodPost, forkPath, env.adminToken, map[string]any{"name": "C"})
	if status != http.StatusCreated {
		t.Fatalf("fork C: %d %v", status, versionC)
	}
	assign := func(id, environment, action string) {
		t.Helper()
		status, result, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots/"+id+"/"+action, env.adminToken, map[string]any{"environment": environment})
		if status != http.StatusOK {
			t.Fatalf("%s %s: %d %v", action, environment, status, result)
		}
	}
	bID, cID := versionB["id"].(string), versionC["id"].(string)
	assign(bID, "prod", "deploy")
	assign(cID, "dev", "deploy")
	if got := runtimePlacement(t, env, env.prodServer.URL, "/")["content"].(map[string]any)["brand"]; got != "Vitasil" {
		t.Fatalf("production changed with dev: %v", got)
	}
	if got := runtimePlacement(t, env, env.devServer.URL, "/")["content"].(map[string]any)["brand"]; got != "Vitasil C" {
		t.Fatalf("dev content: %v", got)
	}
	assign(cID, "prod", "deploy")
	assign(bID, "dev", "rollback")
	assign(bID, "prod", "rollback")
	status, history, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshots", env.adminToken, nil)
	if status != http.StatusOK || history["active"].(map[string]any)["dev"] != bID || history["active"].(map[string]any)["prod"] != bID {
		t.Fatalf("independent rollback: %d %v", status, history)
	}
	assertSnapshotDigestMatchesContent(t, env, bID)
	assertSnapshotDigestMatchesContent(t, env, cID)
}

func TestExternalNestedFeatureWorkspaces(t *testing.T) {
	dir := os.Getenv("TEST_FEATURE_DIR")
	if dir == "" {
		t.Skip("TEST_FEATURE_DIR is not set")
	}
	tests := []struct {
		name, archive, version, mode string
		items                        []any
	}{
		{"catalog", "catalog-1.0.3.zip", "1.0.3", "collection", []any{
			map[string]any{"data": map[string]any{"slug": "milk", "title": "Milk", "price": "100"}},
			map[string]any{"singleSlot": true, "data": map[string]any{"title": "Catalog", "breadcrumb": map[string]any{"homeLabel": "Home", "currentLabel": "Catalog"}, "action": map[string]any{"label": "Contact", "to": "/contact"}}},
		}},
		{"main-data", "main-data-1.0.3.zip", "1.0.3", "single", []any{
			map[string]any{"singleSlot": true, "data": map[string]any{
				"info":   map[string]any{"title": "Benefits", "items": []any{map[string]any{"id": "one", "icon": "quality", "title": "Quality", "description": "Description"}}},
				"slider": map[string]any{"eyebrow": "Products", "title": "Range", "previousLabel": "Previous", "nextLabel": "Next", "itemsLabel": "Items", "catalogActionLabel": "Catalog", "catalogActionTo": "/catalog", "products": []any{map[string]any{"id": "milk", "name": "Milk", "category": "Dairy", "summary": "Fresh"}}},
			}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			archive, err := os.ReadFile(filepath.Join(dir, tc.archive))
			if err != nil {
				t.Fatal(err)
			}
			status, build, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
			if status != http.StatusCreated && status != http.StatusOK {
				t.Fatalf("upload: %d %v", status, build)
			}
			snapshot := saveAndDeploySnapshot(t, env, singlePageConfig(tc.name, tc.version), "nested")
			path := "/api/v2/admin/content/snapshots/" + snapshot["id"].(string) + "/features/" + tc.name
			status, saved, _ := env.request(http.MethodPut, path, env.adminToken, map[string]any{"revision": 0, "items": tc.items})
			if status != http.StatusOK {
				t.Fatalf("save %s: %d %v", tc.mode, status, saved)
			}
			status, reopened, _ := env.request(http.MethodGet, path, env.adminToken, nil)
			if status != http.StatusOK || reopened["revision"] != float64(1) || len(reopened["items"].([]any)) != len(tc.items) {
				t.Fatalf("reopen: %d %v", status, reopened)
			}
			status, child, _ := env.request(http.MethodPost, "/api/v2/admin/content/snapshots/"+snapshot["id"].(string)+"/fork", env.adminToken, map[string]any{"name": "nested child"})
			if status != http.StatusCreated {
				t.Fatalf("fork: %d %v", status, child)
			}
			assertSnapshotDigestMatchesContent(t, env, child["id"].(string))
		})
	}
}
