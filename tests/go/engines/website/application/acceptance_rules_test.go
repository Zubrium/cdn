//go:build website_acceptance
// +build website_acceptance

package application_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

func TestWebsiteScenarioPageRootCreation(t *testing.T) {
	t.Run("WEB-002", func(t *testing.T) {
		store, service, ctx := newWebsiteScenarioService(t)
		first := createScenarioPage(t, service, ctx, "Home", "scenario-page-root-home-001")
		second := createScenarioPage(t, service, ctx, "About", "scenario-page-root-about-001")

		if !scenarioUUID(first.ID) || !scenarioUUID(first.LatestSnapshotID) || first.ID == second.ID || first.LatestSnapshotID == second.LatestSnapshotID {
			t.Fatalf("page/root identities must be distinct server-generated UUIDs: first=%#v second=%#v", first, second)
		}
		for _, page := range []websiteapi.Page{first, second} {
			items, err := service.ListSnapshots(ctx, page.ID, "", 10)
			if err != nil {
				t.Fatalf("list roots for %s: %v", page.ID, err)
			}
			if len(items.Items) != 1 || items.Items[0].ID != page.LatestSnapshotID || items.Items[0].State != "ready" || items.Items[0].ParentID != "" || items.Items[0].Sequence != 1 {
				t.Fatalf("page %s must have one ready root: %#v", page.ID, items.Items)
			}
		}
		requestType := reflect.TypeOf(websiteapi.CreatePageRequest{})
		for _, field := range []string{"ID", "PageID", "SnapshotID", "RootSnapshotID"} {
			if _, exists := requestType.FieldByName(field); exists {
				t.Fatalf("CreatePageRequest must not let callers select %s", field)
			}
		}
		if len(store.records) != 8 {
			t.Fatalf("two page creations must persist exactly page/root/tree/receipt records: %d", len(store.records))
		}
	})

	t.Run("WEB-003", func(t *testing.T) {
		store, service, ctx := newWebsiteScenarioService(t)
		first := createScenarioPage(t, service, ctx, "Home", "scenario-page-tree-home-001")
		second := createScenarioPage(t, service, ctx, "About", "scenario-page-tree-about-001")
		root, err := service.GetSnapshot(ctx, first.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}

		childRequest := scenarioSnapshotRequest(t, first.ID, root.ID, root.Revision, "Home child", "scenario-child-tree-home-001", scenarioPagePayload(t, first.ID, "Home child", nil))
		child, err := service.CreateSnapshot(ctx, childRequest)
		if err != nil {
			t.Fatal(err)
		}
		if child.PageID != first.ID || child.ParentID != root.ID || child.State != "provisioning" {
			t.Fatalf("child must stay in the exact page tree: %#v", child)
		}

		before := len(store.records)
		crossPage := childRequest
		crossPage.PageID = second.ID
		crossPage.IdempotencyKey = "scenario-child-cross-page-001"
		if _, err := service.CreateSnapshot(ctx, crossPage); err == nil {
			t.Fatal("cross-page parent was accepted")
		}
		directRoot := childRequest
		directRoot.ParentID = ""
		directRoot.IdempotencyKey = "scenario-child-direct-root-001"
		if _, err := service.CreateSnapshot(ctx, directRoot); !errors.Is(err, websiteapi.ErrInvalid) {
			t.Fatalf("direct root = %v, want invalid", err)
		}
		if len(store.records) != before {
			t.Fatalf("rejected parent/root requests changed Website records: before=%d after=%d", before, len(store.records))
		}
	})
}

func TestWebsiteScenarioSnapshotCAS(t *testing.T) {
	t.Run("WEB-004", func(t *testing.T) {
		store, service, ctx := newWebsiteScenarioService(t)
		page := createScenarioPage(t, service, ctx, "Home", "scenario-snapshot-cas-page-001")
		parent, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		parentConfig := append([]byte(nil), parent.Payload.Config...)
		request := scenarioSnapshotRequest(t, page.ID, parent.ID, parent.Revision, "Changed structure", "scenario-snapshot-cas-current-001", scenarioPagePayload(t, page.ID, "Changed structure", []any{map[string]any{"id": "hero-1", "build": map[string]any{"featureKey": "hero", "version": "1.2.0"}}}))
		if _, err := service.CreateSnapshot(ctx, request); err != nil {
			t.Fatalf("current structural save: %v", err)
		}

		before := len(store.records)
		stale := request
		stale.IdempotencyKey = "scenario-snapshot-cas-stale-001"
		if _, err := service.CreateSnapshot(ctx, stale); !errors.Is(err, websiteapi.ErrConflict) {
			t.Fatalf("stale structural save = %v, want conflict", err)
		}
		if len(store.records) != before {
			t.Fatalf("stale structural save wrote records: before=%d after=%d", before, len(store.records))
		}
		storedParent, err := service.GetSnapshot(ctx, parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if storedParent.Revision != parent.Revision || !bytes.Equal(storedParent.Payload.Config, parentConfig) || storedParent.ParentID != "" {
			t.Fatalf("stale structural save changed immutable parent: %#v", storedParent)
		}
		var config map[string]json.RawMessage
		if err := json.Unmarshal(storedParent.Payload.Config, &config); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"route", "url", "branch", "dataModel", "records"} {
			if _, exists := config[forbidden]; exists {
				t.Fatalf("snapshot payload must not carry %q after structural save", forbidden)
			}
		}
	})
}

func TestWebsiteScenarioSiteEditorBindings(t *testing.T) {
	t.Run("WEB-006", func(t *testing.T) {
		store, service, ctx := newWebsiteScenarioService(t)
		page := createScenarioPage(t, service, ctx, "Catalog", "scenario-bindings-page-001")
		parent, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		const entityID = "11111111-1111-4111-8111-111111111111"
		const fieldID = "22222222-2222-4222-8222-222222222222"
		model := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"product","name":"Product","cardinality":"many","fields":[{"id":"22222222-2222-4222-8222-222222222222","key":"title","name":"Title","type":"text"}]}]}`)
		if _, err := service.SaveContentModel(ctx, websiteapi.SaveContentModelRequest{ExpectedRevision: 0, DataModel: model}); err != nil {
			t.Fatalf("save direct-binding model: %v", err)
		}
		validPayload := scenarioPagePayload(t, page.ID, "Catalog", []any{map[string]any{
			"id": "catalog-1", "build": map[string]any{"featureKey": "catalog", "version": "1.2.0"},
			"arguments":    map[string]any{"label": "Products"},
			"dataBindings": []any{map[string]any{"inputKey": "items", "entityId": entityID, "fields": []any{map[string]any{"inputPath": "title", "fieldId": fieldID}}}},
		}})
		valid := scenarioSnapshotRequest(t, page.ID, parent.ID, parent.Revision, "Catalog", "scenario-bindings-valid-001", validPayload)
		if _, err := service.CreateSnapshot(ctx, valid); err != nil {
			t.Fatalf("valid direct data binding: %v", err)
		}

		invalidPage := createScenarioPage(t, service, ctx, "Invalid catalog", "scenario-bindings-invalid-page-001")
		invalidParent, err := service.GetSnapshot(ctx, invalidPage.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		invalidPayload := scenarioPagePayload(t, invalidPage.ID, "Invalid catalog", []any{map[string]any{
			"id": "catalog-2", "build": map[string]any{"featureKey": "catalog", "version": "1.2.0"},
			"dataBindings": []any{map[string]any{
				"inputKey": "items", "entityId": entityID, "fields": []any{map[string]any{"inputPath": "title", "fieldId": fieldID}},
				"relationIds": []string{"retired-relation"},
			}},
		}})
		before := len(store.records)
		invalid := scenarioSnapshotRequest(t, invalidPage.ID, invalidParent.ID, invalidParent.Revision, "Invalid catalog", "scenario-bindings-invalid-001", invalidPayload)
		if _, err := service.CreateSnapshot(ctx, invalid); !errors.Is(err, websiteapi.ErrInvalid) {
			t.Fatalf("relation traversal binding = %v, want invalid", err)
		}
		if len(store.records) != before {
			t.Fatalf("invalid data binding created partial Website records: before=%d after=%d", before, len(store.records))
		}
	})
}

func TestWebsiteScenarioPageDeletion(t *testing.T) {
	t.Run("WEB-022", func(t *testing.T) {
		// The executable durable-flow check lives in the disposable main
		// acceptance suite; this contract guard keeps the Website facade
		// honest: no retirement state or operation may disappear silently.
		apiType := reflect.TypeOf((*websiteapi.API)(nil)).Elem()
		if _, exists := apiType.MethodByName("RetirePage"); !exists {
			t.Fatal("Website API must expose the typed RetirePage operation")
		}
		pageType := reflect.TypeOf(websiteapi.Page{})
		for _, field := range []string{"Revision", "LifecycleState", "RetirementJobID"} {
			if _, exists := pageType.FieldByName(field); !exists {
				t.Fatalf("Website Page must expose %s for durable retirement", field)
			}
		}
	})
}

func TestWebsiteScenarioLocaleProjection(t *testing.T) {
	t.Run("WEB-005", func(t *testing.T) {
		t.Skip("gap: Website projection has no locale/catalog-revision input or authoritative locale Content fixture")
	})
}

func TestWebsiteScenarioDataModelCardinality(t *testing.T) {
	t.Run("WEB-007", func(t *testing.T) {
		t.Skip("gap: singleton draft/publication lifecycle belongs to the unavailable Content authority fixture")
	})
}

func TestWebsiteScenarioTemplateMaterialization(t *testing.T) {
	t.Run("WEB-008", func(t *testing.T) {
		store, service, ctx := newWebsiteScenarioService(t)
		configuration := websiteapi.PageTemplateConfiguration{
			SchemaVersion:  1,
			FeaturePins:    map[string]string{},
			PlacementSlots: []websiteapi.PlacementSlot{},
			Payload:        scenarioPagePayload(t, "", "Template title", nil),
		}
		template, err := service.CreatePageTemplate(ctx, websiteapi.CreatePageTemplateRequest{Name: "Landing", Description: "Initial template", Configuration: configuration, IdempotencyKey: "scenario-template-create-001"})
		if err != nil {
			t.Fatal(err)
		}
		page := createScenarioPageFromTemplate(t, service, ctx, "Materialized page", template.ID, template.Revision, "scenario-template-page-001")
		root, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		rootConfig := append([]byte(nil), root.Payload.Config...)

		configuration.Payload = scenarioPagePayload(t, "", "Updated template", nil)
		updated, err := service.UpdatePageTemplate(ctx, websiteapi.UpdatePageTemplateRequest{TemplateID: template.ID, ExpectedRevision: template.Revision, Name: "Landing v2", Description: "Updated template", Configuration: configuration})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Revision != template.Revision+1 || updated.Digest == template.Digest {
			t.Fatalf("template CAS update = %#v, initial=%#v", updated, template)
		}
		unchanged, err := service.GetSnapshot(ctx, root.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(unchanged.Payload.Config, rootConfig) {
			t.Fatalf("template update mutated materialized page: before=%s after=%s", rootConfig, unchanged.Payload.Config)
		}

		invalidConfig := configuration
		invalidConfig.Payload.Config = json.RawMessage(`{"format_version":3,"route":"/must-not-enter-template","page":{"name":"Invalid","placements":[]}}`)
		before := len(store.records)
		if _, err := service.CreatePageTemplate(ctx, websiteapi.CreatePageTemplateRequest{Name: "Invalid", Description: "Must fail", Configuration: invalidConfig, IdempotencyKey: "scenario-template-invalid-001"}); !errors.Is(err, websiteapi.ErrInvalid) {
			t.Fatalf("template with route contract field = %v, want invalid", err)
		}
		if len(store.records) != before {
			t.Fatalf("invalid template wrote records: before=%d after=%d", before, len(store.records))
		}
		selected, err := service.GetPageTemplate(ctx, template.ID)
		if err != nil || selected.ID != template.ID || selected.Revision != updated.Revision {
			t.Fatalf("invalid template changed selected template: %#v, %v", selected, err)
		}
	})
}

func TestWebsiteScenarioFeatureImportPreflight(t *testing.T) {
	t.Run("WEB-011", func(t *testing.T) {
		limits := runtime.DefaultArchiveLimits()
		for _, test := range []struct {
			name string
			data []byte
		}{
			{name: "malformed", data: []byte("not a zip")},
			{name: "windows traversal", data: scenarioArchive(t, []scenarioArchiveEntry{{name: "manifest.json", data: scenarioManifest(t, "static-lifecycle")}, {name: "assets\\escape.js", data: []byte("export {}")}})},
			{name: "symlink", data: scenarioArchive(t, []scenarioArchiveEntry{{name: "manifest.json", data: scenarioManifest(t, "static-lifecycle")}, {name: "link.js", data: []byte("index.js"), mode: os.ModeSymlink | 0o777}})},
			{name: "retired ABI", data: scenarioArchive(t, []scenarioArchiveEntry{{name: "manifest.json", data: scenarioManifest(t, "static-lifecycle-v2")}, {name: "index.js", data: []byte("export {}")}})},
		} {
			t.Run(test.name, func(t *testing.T) {
				files, manifest, err := runtime.PreflightArchive(test.data, limits)
				if err == nil {
					t.Fatal("unsafe archive was accepted")
				}
				if files != nil || !reflect.DeepEqual(manifest, runtime.Manifest{}) {
					t.Fatalf("rejected archive exposed partial output: files=%#v manifest=%#v", files, manifest)
				}
			})
		}
		valid := scenarioArchive(t, []scenarioArchiveEntry{{name: "manifest.json", data: scenarioManifest(t, "static-lifecycle")}, {name: "index.js", data: []byte("export {}")}})
		files, manifest, err := runtime.PreflightArchive(valid, limits)
		if err != nil || len(files) != 2 || manifest.Runtime.ABI != runtime.ABI {
			t.Fatalf("valid preflight = files=%#v manifest=%#v err=%v", files, manifest, err)
		}
	})
}

func TestWebsiteScenarioArtifactValidation(t *testing.T) {
	t.Run("WEB-014", func(t *testing.T) {
		valid := scenarioArtifact(t, map[string]any{
			"format":   "static-page-artifact/v1",
			"document": map[string]any{"file_id": "11111111-1111-4111-8111-111111111111", "content_type": "text/html; charset=utf-8", "sha256": strings.Repeat("a", 64), "size": 32},
			"assets":   map[string]any{"assets/app.js": map[string]any{"file_id": "22222222-2222-4222-8222-222222222222", "content_type": "text/javascript; charset=utf-8", "sha256": strings.Repeat("b", 64), "size": 16, "cache_class": "immutable"}},
		})
		first, err := runtime.ValidateArtifact(valid, 4, 1024)
		if err != nil {
			t.Fatalf("valid artifact: %v", err)
		}
		second, err := runtime.ValidateArtifact(valid, 4, 1024)
		if err != nil || !reflect.DeepEqual(first, second) {
			t.Fatalf("artifact validation must be deterministic: first=%#v second=%#v err=%v", first, second, err)
		}
		if err := runtime.VerifyArtifactFiles(first, map[string]runtime.CoreFileMetadata{
			first.Document.FileID:                {ContentType: first.Document.ContentType, SHA256: first.Document.SHA256, Size: first.Document.Size},
			first.Assets["assets/app.js"].FileID: {ContentType: first.Assets["assets/app.js"].ContentType, SHA256: first.Assets["assets/app.js"].SHA256, Size: first.Assets["assets/app.js"].Size},
		}); err != nil {
			t.Fatalf("trusted Core metadata: %v", err)
		}
		for _, payload := range [][]byte{
			scenarioArtifact(t, map[string]any{"format": "static-page-artifact/v1", "route": "/visitor-selected", "document": map[string]any{"file_id": "11111111-1111-4111-8111-111111111111", "content_type": "text/html", "sha256": strings.Repeat("a", 64), "size": 32}, "assets": map[string]any{}}),
			scenarioArtifact(t, map[string]any{"format": "static-page-artifact/v1", "document": map[string]any{"file_id": "11111111-1111-4111-8111-111111111111", "content_type": "text/plain", "sha256": strings.Repeat("a", 64), "size": 32}, "assets": map[string]any{}}),
			scenarioArtifact(t, map[string]any{"format": "static-page-artifact/v1", "document": map[string]any{"file_id": "11111111-1111-4111-8111-111111111111", "content_type": "text/html", "sha256": strings.Repeat("a", 64), "size": 32}, "assets": map[string]any{"../escape.js": map[string]any{"file_id": "22222222-2222-4222-8222-222222222222", "content_type": "text/javascript", "sha256": strings.Repeat("b", 64), "size": 16}}}),
		} {
			if _, err := runtime.ValidateArtifact(payload, 4, 1024); err == nil {
				t.Fatalf("invalid artifact was accepted: %s", payload)
			}
		}
	})
}

func newWebsiteScenarioService(t *testing.T) (*memoryStore, interface {
	CreatePage(context.Context, websiteapi.CreatePageRequest) (websiteapi.Page, error)
	CreateSnapshot(context.Context, websiteapi.CreateSnapshotRequest) (websiteapi.Snapshot, error)
	GetSnapshot(context.Context, string) (websiteapi.Snapshot, error)
	ListSnapshots(context.Context, string, string, int) (websiteapi.SnapshotPage, error)
	SaveContentModel(context.Context, websiteapi.SaveContentModelRequest) (websiteapi.ContentModel, error)
	CreatePageTemplate(context.Context, websiteapi.CreatePageTemplateRequest) (websiteapi.PageTemplate, error)
	UpdatePageTemplate(context.Context, websiteapi.UpdatePageTemplateRequest) (websiteapi.PageTemplate, error)
	GetPageTemplate(context.Context, string) (websiteapi.PageTemplate, error)
}, context.Context) {
	t.Helper()
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	return store, service, ctx
}

func createScenarioPage(t *testing.T, service interface {
	CreatePage(context.Context, websiteapi.CreatePageRequest) (websiteapi.Page, error)
}, ctx context.Context, name, idempotencyKey string) websiteapi.Page {
	t.Helper()
	page, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{Name: name, IdempotencyKey: idempotencyKey})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return page
}

func createScenarioPageFromTemplate(t *testing.T, service interface {
	CreatePage(context.Context, websiteapi.CreatePageRequest) (websiteapi.Page, error)
}, ctx context.Context, name, templateID string, templateRevision int64, idempotencyKey string) websiteapi.Page {
	t.Helper()
	page, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{Name: name, TemplateID: templateID, TemplateRevision: templateRevision, IdempotencyKey: idempotencyKey})
	if err != nil {
		t.Fatalf("create template page: %v", err)
	}
	return page
}

func scenarioSnapshotRequest(t *testing.T, pageID, parentID string, revision int64, name, idempotencyKey string, payload websiteapi.SnapshotPayload) websiteapi.CreateSnapshotRequest {
	t.Helper()
	return websiteapi.CreateSnapshotRequest{PageID: pageID, ParentID: parentID, ExpectedRevision: revision, ExpectedContentRevision: 0, Name: name, IdempotencyKey: idempotencyKey, Payload: payload}
}

func scenarioPagePayload(t *testing.T, pageID, name string, placements []any) websiteapi.SnapshotPayload {
	t.Helper()
	config, err := json.Marshal(map[string]any{"format_version": 3, "page": map[string]any{"id": pageID, "name": name, "placements": placements}})
	if err != nil {
		t.Fatal(err)
	}
	return websiteapi.SnapshotPayload{Config: config, Metadata: json.RawMessage(`{}`)}
}

func scenarioUUID(value string) bool {
	if len(value) != 36 || value[14] != '4' || value[19] != '8' && value[19] != '9' && value[19] != 'a' && value[19] != 'b' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

type scenarioArchiveEntry struct {
	name string
	data []byte
	mode os.FileMode
}

func scenarioArchive(t *testing.T, entries []scenarioArchiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		handle, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := handle.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func scenarioManifest(t *testing.T, abi string) []byte {
	t.Helper()
	manifest, err := json.Marshal(map[string]any{
		"formatVersion": 2,
		"key":           "hero",
		"name":          "Hero",
		"version":       "1.0.0",
		"engineApi":     "v1",
		"slots":         []string{"main"},
		"arguments":     map[string]any{},
		"dataInputs":    []any{},
		"runtime": map[string]any{
			"abi": abi, "entry": "index.js", "styles": []string{}, "sharedStyles": []string{}, "origins": []string{"https://api.example.test"}, "sdk": []string{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func scenarioArtifact(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	withoutDigest, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(withoutDigest)
	payload["digest"] = "sha256:" + hex.EncodeToString(digest[:])
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
