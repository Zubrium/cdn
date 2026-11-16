package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/media"
	"websiteengine/features/sitebuilder"
)

func TestMediaRenditionsResolveSelectedSnapshotWithoutActiveBranchAssignment(t *testing.T) {
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	const featureKey = "snapshot-media-context"
	_, siteConfig := modelMediaRenditionFixture(featureKey, "1.0.0", false)
	build := modelAcceptanceUploadFeatureBuild(t, env, modelMediaRenditionManifest(featureKey, "1.0.0"), "export default function Feature() { return null; }")
	configMap := modelMediaRenditionConfigMap(t, siteConfig)
	status, snapshot, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{"name": "unassigned media snapshot", "config": configMap})
	if status != http.StatusCreated {
		t.Fatalf("create unassigned media snapshot: status=%d body=%v", status, snapshot)
	}
	snapshotID := snapshot["id"].(string)
	mediaID := env.uploadMedia("snapshot-cover.png", minimalPNG(t))["id"].(string)
	profile := map[string]any{
		"snapshotId": snapshotID, "featureKey": featureKey, "buildVersion": "1.0.0",
		"schemaKind": "dataInputs", "fieldPath": "/cover", "buildDigest": build["digest"],
	}
	status, prepared, _ := env.request(http.MethodPost, "/api/v2/admin/media/"+mediaID+"/renditions/prepare", env.adminToken, profile)
	if status != http.StatusAccepted || prepared["status"] != "pending" {
		t.Fatalf("prepare media for unassigned snapshot: status=%d body=%v", status, prepared)
	}

	query := url.Values{
		"snapshotId": {snapshotID}, "featureKey": {featureKey}, "buildVersion": {"1.0.0"},
		"schemaKind": {"dataInputs"}, "fieldPath": {"/cover"}, "buildDigest": {build["digest"].(string)},
	}
	status, state, _ := env.request(http.MethodGet, "/api/v2/admin/media/"+mediaID+"/renditions/state?"+query.Encode(), env.adminToken, nil)
	if status != http.StatusOK || state["status"] != "pending" {
		t.Fatalf("read rendition state for unassigned snapshot: status=%d body=%v", status, state)
	}

	stale := url.Values{}
	for key, values := range query {
		stale[key] = append([]string(nil), values...)
	}
	stale.Set("buildDigest", "stale-digest")
	status, conflict, _ := env.request(http.MethodGet, "/api/v2/admin/media/"+mediaID+"/renditions/state?"+stale.Encode(), env.adminToken, nil)
	if status != http.StatusConflict {
		t.Fatalf("stale build digest must conflict: status=%d body=%v", status, conflict)
	}

	_, withoutFeature := modelMediaRenditionFixture(featureKey, "1.0.0", true)
	unselectedConfig := modelMediaRenditionConfigMap(t, withoutFeature)
	status, unselected, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{"name": "snapshot without feature", "config": unselectedConfig})
	if status != http.StatusCreated {
		t.Fatalf("create snapshot without selected feature: status=%d body=%v", status, unselected)
	}
	profile["snapshotId"] = unselected["id"]
	status, missing, _ := env.request(http.MethodPost, "/api/v2/admin/media/"+mediaID+"/renditions/prepare", env.adminToken, profile)
	if status != http.StatusNotFound {
		t.Fatalf("media profile for unselected build must be rejected: status=%d body=%v", status, missing)
	}
}

func TestModelMediaRenditionsRetryRestartPublishAndRuntime(t *testing.T) {
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	ctx := context.Background()
	const featureKey = "responsive-media"
	model, siteConfig := modelMediaRenditionFixture(featureKey, "1.0.0", false)
	build := modelAcceptanceUploadFeatureBuild(t, env, modelMediaRenditionManifest(featureKey, "1.0.0"), "export default function Feature() { return null; }")
	configMap := modelMediaRenditionConfigMap(t, siteConfig)
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "model media profile")
	snapshotID := snapshot["id"].(string)
	mediaID := env.uploadMedia("cover.png", minimalPNG(t))["id"].(string)
	base := "/api/v2/admin/content/snapshots/" + snapshotID + "/model"
	status, draft, _ := env.request(http.MethodPut, base, env.adminToken, map[string]any{
		"revision": 0,
		"records":  []any{map[string]any{"entityId": model.Entities[0].ID, "sortOrder": 0, "values": map[string]any{"cover": mediaID}}},
	})
	if status != http.StatusOK || draft["revision"] != float64(1) {
		t.Fatalf("save model media draft: status=%d body=%v", status, draft)
	}
	records := draft["records"].([]any)
	if len(records) != 1 || records[0].(map[string]any)["id"] == "" {
		t.Fatalf("model media record has no generated ID: %v", records)
	}
	recordID := records[0].(map[string]any)["id"].(string)
	selection := []any{map[string]any{
		"placementId": "media-placement", "inputKey": "cover", "mode": "ids", "recordIds": []string{recordID},
	}}
	status, draft, _ = env.request(http.MethodPut, base, env.adminToken, map[string]any{"revision": 1, "records": records, "selections": selection})
	if status != http.StatusOK || draft["revision"] != float64(2) {
		t.Fatalf("select model media record for placement: status=%d body=%v", status, draft)
	}

	profile := map[string]any{
		"snapshotId": snapshotID, "featureKey": featureKey, "buildVersion": "1.0.0",
		"schemaKind": "dataInputs", "fieldPath": "/cover", "buildDigest": build["digest"],
	}
	preparePath := "/api/v2/admin/media/" + mediaID + "/renditions/prepare"
	status, prepared, _ := env.request(http.MethodPost, preparePath, env.adminToken, profile)
	if status != http.StatusAccepted || prepared["status"] != "pending" {
		t.Fatalf("queue required model media rendition: status=%d body=%v", status, prepared)
	}
	stateQuery := url.Values{
		"snapshotId": {snapshotID}, "featureKey": {featureKey}, "buildVersion": {"1.0.0"},
		"schemaKind": {"dataInputs"}, "fieldPath": {"/cover"}, "buildDigest": {build["digest"].(string)},
	}
	statePath := "/api/v2/admin/media/" + mediaID + "/renditions/state?" + stateQuery.Encode()
	status, body, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": 2})
	if status != http.StatusConflict {
		t.Fatalf("publication must wait for required model media rendition: status=%d body=%v", status, body)
	}

	env.processor.failNext = true
	processed, err := env.media.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("run failed rendition job: processed=%t err=%v", processed, err)
	}
	status, state, _ := env.request(http.MethodGet, statePath, env.adminToken, nil)
	if status != http.StatusOK || state["status"] != "failed" {
		t.Fatalf("failed rendition state should be visible: status=%d body=%v", status, state)
	}
	status, retried, _ := env.request(http.MethodPost, preparePath, env.adminToken, profile)
	if status != http.StatusAccepted || retried["status"] != "pending" {
		t.Fatalf("retry model rendition job: status=%d body=%v", status, retried)
	}

	restartedWorker := media.NewServiceWithProcessor(media.NewRepository(env.pool), env.store, 20<<20, env.processor)
	configureMediaRenditionWorker(t, restartedWorker)
	processed, err = restartedWorker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("resume rendition after worker restart: processed=%t err=%v", processed, err)
	}
	status, state, _ = env.request(http.MethodGet, statePath, env.adminToken, nil)
	if status != http.StatusOK || state["status"] != "ready" {
		t.Fatalf("completed model rendition state: status=%d body=%v", status, state)
	}
	status, published, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": 2})
	if status != http.StatusOK || published["publishedRevision"] != float64(2) {
		t.Fatalf("publish model media record: status=%d body=%v", status, published)
	}

	placement := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/")
	content, ok := placement["content"].(map[string]any)
	if !ok {
		t.Fatalf("runtime placement omitted model content: %v", placement)
	}
	cover, ok := content["cover"].(map[string]any)
	if !ok {
		t.Fatalf("runtime placement omitted projected cover media: %v", placement)
	}
	if cover["kind"] != "image" || cover["id"] != mediaID || cover["originalUrl"] == nil || cover["srcSet"] == nil {
		t.Fatalf("runtime should return a hydrated responsive model image: %v", cover)
	}
	variants := cover["variants"].(map[string]any)
	card := variants["card"].(map[string]any)
	cardURL := card["url"].(string)
	if !strings.Contains(cover["srcSet"].(string), cardURL) {
		t.Fatalf("runtime srcSet does not reference the prepared model rendition: %v", cover)
	}

	request, _ := http.NewRequest(http.MethodGet, env.devServer.URL+cardURL, nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound || !strings.Contains(response.Header.Get("Location"), "/renditions/") {
		t.Fatalf("prepared model rendition should resolve from the public endpoint: status=%d location=%s", response.StatusCode, response.Header.Get("Location"))
	}
}

func modelMediaRenditionFixture(featureKey, version string, omitPlacement bool) (sitebuilder.DataModel, sitebuilder.SiteConfig) {
	entityID, fieldID := uuid.NewString(), uuid.NewString()
	model := sitebuilder.DataModel{
		FormatVersion: 1,
		Entities: []sitebuilder.DataModelEntity{{
			ID: entityID, Key: "media_asset", Name: "Медиа", Fields: []sitebuilder.DataModelField{{
				ID: fieldID, Key: "cover", Name: "Обложка", Type: "media",
			}},
		}},
	}
	placements := []sitebuilder.Placement{}
	if !omitPlacement {
		placements = append(placements, sitebuilder.Placement{
			ID: "media-placement", Build: sitebuilder.BuildRef{FeatureKey: featureKey, Version: version}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{{InputKey: "cover", EntityID: entityID, FieldID: fieldID}},
		})
	}
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		DataModel:     &model,
		Pages: []sitebuilder.Page{{
			ID: "home", Name: "Home", Kind: "page", Placements: placements,
		}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home"}}},
	}
	return model, config
}

func modelMediaRenditionManifest(featureKey, version string) map[string]any {
	return map[string]any{
		"formatVersion": 2, "key": featureKey, "name": featureKey, "version": version, "engineApi": "2",
		"runtime":   map[string]any{"abi": "static-module-v1", "entry": "entry.js"},
		"slots":     []string{"main"},
		"arguments": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"dataInputs": []any{map[string]any{
			"key": "cover", "kind": "scalar", "type": "media",
			"profile": map[string]any{
				"accept":     "raster-image",
				"renditions": []any{map[string]any{"name": "card", "width": 640, "height": 480, "fit": "cover"}},
			},
		}},
	}
}

func modelMediaRenditionConfigMap(t *testing.T, config sitebuilder.SiteConfig) map[string]any {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
