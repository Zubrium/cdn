//go:build website_acceptance
// +build website_acceptance

package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	"github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

type web001CountingStore struct {
	*memoryStore
	gets int
}

func (s *web001CountingStore) Get(ctx context.Context, key recorddata.RecordKey) (recorddata.Record, error) {
	s.gets++
	return s.memoryStore.Get(ctx, key)
}

func newWeb001Service(t *testing.T, store *web001CountingStore, level core.AccessLevel) *websiteapp.Service {
	t.Helper()
	service, err := websiteapp.NewService(websiteConfig(), store, &fakeAuthority{level: level, coordinatorLevel: level}, fakeSessions{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func webAcceptanceJSONEqual(left, right json.RawMessage) bool {
	var first, second any
	if json.Unmarshal(left, &first) != nil || json.Unmarshal(right, &second) != nil {
		return false
	}
	return reflect.DeepEqual(first, second)
}

func TestWebsiteScenarioSystemModelGrantCoverage(t *testing.T) {
	t.Run("WEB-001", func(t *testing.T) {
		store := &web001CountingStore{memoryStore: newMemoryStore()}
		writer := newWeb001Service(t, store, core.AccessLevelWrite)
		writeCtx, err := writer.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		created, err := writer.SaveContentModel(writeCtx, websiteapi.SaveContentModelRequest{
			ExpectedRevision: 0,
			DataModel:        json.RawMessage(`{"formatVersion":1,"entities":[]}`),
		})
		if err != nil {
			t.Fatalf("save system model: %v", err)
		}
		if !created.VerifyDigest() || created.ModelRevision != 1 {
			t.Fatalf("saved system model = %#v", created)
		}

		reader := newWeb001Service(t, store, core.AccessLevelRead)
		readCtx, err := reader.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		beforeReads := store.gets
		beforeRecords := len(store.records)
		first, err := reader.GetContentModel(readCtx)
		if err != nil {
			t.Fatalf("read system model with grant: %v", err)
		}
		second, err := reader.GetContentModel(readCtx)
		if err != nil {
			t.Fatalf("repeat system model read: %v", err)
		}
		if !reflect.DeepEqual(first, second) || !first.VerifyDigest() ||
			first.ModelRevision != created.ModelRevision || first.Digest != created.Digest ||
			!webAcceptanceJSONEqual(first.DataModel, created.DataModel) {
			t.Fatalf("system model is not stable: first=%#v second=%#v saved=%#v", first, second, created)
		}
		if store.gets-beforeReads != 2 || len(store.records) != beforeRecords {
			t.Fatalf("reads changed store state: gets=%d records=%d", store.gets-beforeReads, len(store.records))
		}

		deniedStore := &web001CountingStore{memoryStore: newMemoryStore()}
		denied := newWeb001Service(t, deniedStore, core.AccessLevelNone)
		deniedCtx, err := denied.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := denied.GetContentModel(deniedCtx); !errors.Is(err, websiteapi.ErrForbidden) {
			t.Fatalf("system model read without grant = %v, want forbidden", err)
		}
		if deniedStore.gets != 0 || len(deniedStore.records) != 0 {
			t.Fatalf("denied read touched website storage: gets=%d records=%d", deniedStore.gets, len(deniedStore.records))
		}

		modelType := reflect.TypeOf(websiteapi.ContentModel{})
		for _, forbidden := range []string{"Role", "Kind", "Records", "Publication", "Locale", "ScopeID"} {
			if _, exists := modelType.FieldByName(forbidden); exists {
				t.Fatalf("ContentModel must not expose %s", forbidden)
			}
		}
	})
}

func TestWebsiteScenarioModelCASHardeningCoverage(t *testing.T) {
	t.Run("WEB-005", func(t *testing.T) {
		store := newMemoryStore()
		service := newService(t, store, core.AccessLevelWrite, websiteConfig())
		ctx, err := service.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		page := createScenarioPage(t, service, ctx, "Catalog", "web005-cas-page-0001")
		parent, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		const entityID = "11111111-1111-4111-8111-111111111111"
		const fieldID = "22222222-2222-4222-8222-222222222222"
		model := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"product","name":"Product","cardinality":"many","fields":[{"id":"22222222-2222-4222-8222-222222222222","key":"title","name":"Title","type":"text"}]}]}`)
		current, err := service.SaveContentModel(ctx, websiteapi.SaveContentModelRequest{ExpectedRevision: 0, DataModel: model})
		if err != nil {
			t.Fatalf("save model: %v", err)
		}
		payload := scenarioPagePayload(t, page.ID, "Catalog", []any{map[string]any{
			"id": "catalog-1", "build": map[string]any{"featureKey": "catalog", "version": "1.2.0"},
			"dataBindings": []any{map[string]any{"inputKey": "items", "entityId": entityID, "fields": []any{map[string]any{"inputPath": "title", "fieldId": fieldID}}}},
		}})
		child, err := service.CreateSnapshot(ctx, scenarioSnapshotRequest(t, page.ID, parent.ID, parent.Revision, "Catalog child", "web005-cas-child-0001", payload))
		if err != nil {
			t.Fatalf("create bound snapshot: %v", err)
		}

		beforeRejected := len(store.records)
		if _, err := service.SaveContentModel(ctx, websiteapi.SaveContentModelRequest{ExpectedRevision: 0, DataModel: model}); !errors.Is(err, websiteapi.ErrConflict) {
			t.Fatalf("stale model revision = %v, want conflict", err)
		}
		invalidating := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"product","name":"Product","cardinality":"many","fields":[]}]}`)
		if _, err := service.SaveContentModel(ctx, websiteapi.SaveContentModelRequest{ExpectedRevision: current.ModelRevision, DataModel: invalidating}); !errors.Is(err, websiteapi.ErrConflict) {
			t.Fatalf("invalidating binding = %v, want conflict", err)
		}
		if len(store.records) != beforeRejected {
			t.Fatalf("rejected model writes changed records: before=%d after=%d", beforeRejected, len(store.records))
		}
		persisted, err := service.GetContentModel(ctx)
		if err != nil || persisted.ModelRevision != current.ModelRevision || persisted.Digest != current.Digest || !webAcceptanceJSONEqual(persisted.DataModel, model) {
			t.Fatalf("rejected model changes mutated system model: %#v, %v", persisted, err)
		}
		storedChild, err := service.GetSnapshot(ctx, child.ID)
		if err != nil || storedChild.Revision != child.Revision || string(storedChild.Payload.Config) != string(child.Payload.Config) {
			t.Fatalf("rejected model changes mutated snapshot: %#v, %v", storedChild, err)
		}
		for key, record := range store.records {
			if key.Namespace == "website/snapshots" && bytes.Contains(record.IndexData, []byte("model_revision")) {
				t.Fatalf("model revision leaked into snapshot namespace %s: %s", key.RecordKey, record.IndexData)
			}
		}

		projection, err := service.GetPagePlacementProjection(ctx, page.ID, child.ID)
		if err != nil {
			t.Fatalf("exact tuple projection: %v", err)
		}
		if !projection.VerifyDigest() || projection.PageID != page.ID || projection.PageSnapshotID != child.ID ||
			len(projection.Placements) != 1 || len(projection.Placements[0].Bindings) != 1 {
			t.Fatalf("page projection = %#v", projection)
		}
		if _, err := service.GetPagePlacementProjection(ctx, pageB, child.ID); !errors.Is(err, websiteapi.ErrNotFound) {
			t.Fatalf("mismatched page tuple = %v, want not found", err)
		}
		missingSnapshot := "99999999-9999-4999-8999-999999999999"
		if _, err := service.GetPagePlacementProjection(ctx, page.ID, missingSnapshot); !errors.Is(err, websiteapi.ErrNotFound) {
			t.Fatalf("mismatched snapshot tuple = %v, want not found", err)
		}
	})
}

func TestWebsiteScenarioDataModelCardinalityCoverage(t *testing.T) {
	t.Run("WEB-007", func(t *testing.T) {
		store := newMemoryStore()
		service := newService(t, store, core.AccessLevelWrite, websiteConfig())
		ctx, err := service.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		singleton := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"settings","name":"Settings","cardinality":"singleton","fields":[{"id":"22222222-2222-4222-8222-222222222222","key":"title","name":"Title","type":"text"}]}]}`)
		created, err := service.SaveContentModel(ctx, websiteapi.SaveContentModelRequest{ExpectedRevision: 0, DataModel: singleton})
		if err != nil {
			t.Fatalf("singleton model structural save: %v", err)
		}
		if !created.VerifyDigest() || created.ModelRevision != 1 || !webAcceptanceJSONEqual(created.DataModel, singleton) {
			t.Fatalf("singleton model projection = %#v", created)
		}
		for _, invalid := range []string{"one", "", "SINGLETON"} {
			rejected := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"settings","name":"Settings","cardinality":"` + invalid + `","fields":[]}]}`)
			if _, err := service.SaveContentModel(ctx, websiteapi.SaveContentModelRequest{ExpectedRevision: 0, DataModel: rejected}); !errors.Is(err, websiteapi.ErrInvalid) {
				t.Fatalf("cardinality %q = %v, want invalid", invalid, err)
			}
		}
		if len(store.records) != 1 {
			t.Fatalf("Website structural model writes created %d records, want only the system model", len(store.records))
		}
		for key := range store.records {
			if key.Namespace != "website/content-models" {
				t.Fatalf("Website model write escaped its namespace: %#v", key)
			}
		}
		// Content owns drafts, record IDs, publication and locale isolation. This
		// executable check fixes the Website-side boundary only: no record,
		// publication or locale scope is accepted in the system model DTO.
		modelType := reflect.TypeOf(websiteapi.ContentModel{})
		for _, forbidden := range []string{"Records", "RecordID", "Publication", "PublishedRevision", "Locale"} {
			if _, exists := modelType.FieldByName(forbidden); exists {
				t.Fatalf("ContentModel must not carry Content lifecycle field %s", forbidden)
			}
		}
	})
}

func TestWebsiteScenarioTemplateApplicationCoverage(t *testing.T) {
	t.Run("WEB-009", func(t *testing.T) {
		store := newMemoryStore()
		service := newService(t, store, core.AccessLevelWrite, websiteConfig())
		ctx, err := service.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		page, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{Name: "Original", IdempotencyKey: "web009-template-page-0001"})
		if err != nil {
			t.Fatal(err)
		}
		root, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		configuration := websiteapi.PageTemplateConfiguration{
			SchemaVersion: 1, FeaturePins: map[string]string{}, PlacementSlots: []websiteapi.PlacementSlot{},
			Payload: scenarioPagePayload(t, "", "Replacement", nil),
		}
		template, err := service.CreatePageTemplate(ctx, websiteapi.CreatePageTemplateRequest{
			Name: "Replacement", Description: "Structural replacement", Configuration: configuration,
			IdempotencyKey: "web009-template-create-0001",
		})
		if err != nil {
			t.Fatal(err)
		}
		apply := websiteapi.ApplyPageTemplateRequest{
			PageID: page.ID, TemplateID: template.ID, TemplateRevision: template.Revision,
			ExpectedSnapshotID: root.ID, ExpectedSnapshotRevision: root.Revision, ExpectedContentRevision: 0,
			Confirmed: true, IdempotencyKey: "web009-template-apply-0001",
		}

		beforeRejected := len(store.records)
		unconfirmed := apply
		unconfirmed.Confirmed = false
		unconfirmed.IdempotencyKey = "web009-template-unconfirmed-01"
		if _, err := service.ApplyPageTemplate(ctx, unconfirmed); !errors.Is(err, websiteapi.ErrInvalid) {
			t.Fatalf("unconfirmed replacement = %v, want invalid", err)
		}
		staleSnapshot := apply
		staleSnapshot.ExpectedSnapshotRevision = root.Revision + 1
		staleSnapshot.IdempotencyKey = "web009-template-stale-snap-001"
		if _, err := service.ApplyPageTemplate(ctx, staleSnapshot); !errors.Is(err, websiteapi.ErrConflict) {
			t.Fatalf("stale snapshot revision = %v, want conflict", err)
		}
		staleTemplate := apply
		staleTemplate.TemplateRevision = template.Revision + 1
		staleTemplate.IdempotencyKey = "web009-template-stale-tmpl-001"
		if _, err := service.ApplyPageTemplate(ctx, staleTemplate); !errors.Is(err, websiteapi.ErrConflict) {
			t.Fatalf("stale template revision = %v, want conflict", err)
		}
		if len(store.records) != beforeRejected {
			t.Fatalf("rejected template applications wrote records: before=%d after=%d", beforeRejected, len(store.records))
		}

		child, err := service.ApplyPageTemplate(ctx, apply)
		if err != nil {
			t.Fatalf("confirmed replacement: %v", err)
		}
		if child.PageID != page.ID || child.ParentID != root.ID || child.State != "provisioning" || child.Sequence != root.Sequence+1 {
			t.Fatalf("replacement child = %#v", child)
		}
		var metadata map[string]map[string]any
		if err := json.Unmarshal(child.Payload.Metadata, &metadata); err != nil ||
			metadata["template_application"]["selection_policy"] != "reset" ||
			metadata["template_application"]["template_revision"] != float64(template.Revision) {
			t.Fatalf("replacement provenance = %#v, %v", metadata, err)
		}
		parentAfter, err := service.GetSnapshot(ctx, root.ID)
		if err != nil || parentAfter.State != root.State || string(parentAfter.Payload.Config) != string(root.Payload.Config) || parentAfter.Revision != root.Revision {
			t.Fatalf("replacement mutated parent: %#v, %v", parentAfter, err)
		}
		replayed, err := service.ApplyPageTemplate(ctx, apply)
		if err != nil || replayed.ID != child.ID {
			t.Fatalf("replacement replay = %#v, %v", replayed, err)
		}

		incompatibleStore := newMemoryStore()
		incompatible := newService(t, incompatibleStore, core.AccessLevelWrite, websiteConfig())
		incompatibleCtx, err := incompatible.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		const entityID = "11111111-1111-4111-8111-111111111111"
		const fieldID = "22222222-2222-4222-8222-222222222222"
		validModel := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"11111111-1111-4111-8111-111111111111","key":"product","name":"Product","cardinality":"many","fields":[{"id":"22222222-2222-4222-8222-222222222222","key":"title","name":"Title","type":"text"}]}]}`)
		if _, err := incompatible.SaveContentModel(incompatibleCtx, websiteapi.SaveContentModelRequest{ExpectedRevision: 0, DataModel: validModel}); err != nil {
			t.Fatal(err)
		}
		boundPayload := scenarioPagePayload(t, "", "Bound replacement", []any{map[string]any{
			"id": "bound-1", "build": map[string]any{"featureKey": "catalog", "version": "1.2.0"},
			"dataBindings": []any{map[string]any{"inputKey": "items", "entityId": entityID, "fields": []any{map[string]any{"inputPath": "title", "fieldId": fieldID}}}},
		}})
		boundConfig := configuration
		boundConfig.FeaturePins = map[string]string{"catalog": "1.2.0"}
		boundConfig.Payload = boundPayload
		boundTemplate, err := incompatible.CreatePageTemplate(incompatibleCtx, websiteapi.CreatePageTemplateRequest{
			Name: "Bound", Description: "Invalidated replacement", Configuration: boundConfig,
			IdempotencyKey: "web009-bound-template-0001",
		})
		if err != nil {
			t.Fatal(err)
		}
		emptyModel := json.RawMessage(`{"formatVersion":1,"entities":[]}`)
		if _, err := incompatible.SaveContentModel(incompatibleCtx, websiteapi.SaveContentModelRequest{ExpectedRevision: 1, DataModel: emptyModel}); err != nil {
			t.Fatalf("model change after template lock: %v", err)
		}
		incompatiblePage, err := incompatible.CreatePage(incompatibleCtx, websiteapi.CreatePageRequest{Name: "Bound page", IdempotencyKey: "web009-bound-page-00001"})
		if err != nil {
			t.Fatal(err)
		}
		incompatibleRoot, err := incompatible.GetSnapshot(incompatibleCtx, incompatiblePage.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		beforeIncompatible := len(incompatibleStore.records)
		if _, err := incompatible.ApplyPageTemplate(incompatibleCtx, websiteapi.ApplyPageTemplateRequest{
			PageID: incompatiblePage.ID, TemplateID: boundTemplate.ID, TemplateRevision: boundTemplate.Revision,
			ExpectedSnapshotID: incompatibleRoot.ID, ExpectedSnapshotRevision: incompatibleRoot.Revision,
			Confirmed: true, IdempotencyKey: "web009-bound-apply-000001",
		}); !errors.Is(err, websiteapi.ErrConflict) {
			t.Fatalf("incompatible structural replacement = %v, want conflict", err)
		}
		if len(incompatibleStore.records) != beforeIncompatible {
			t.Fatalf("incompatible replacement wrote partial state: before=%d after=%d", beforeIncompatible, len(incompatibleStore.records))
		}
	})
}

func TestWebsiteScenarioStructuralBoundaryCoverage(t *testing.T) {
	t.Run("WEB-010", func(t *testing.T) {
		store := newMemoryStore()
		service := newService(t, store, core.AccessLevelWrite, websiteConfig())
		ctx, err := service.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		page, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{Name: "Boundary", IdempotencyKey: "web010-boundary-page-0001"})
		if err != nil {
			t.Fatal(err)
		}
		root, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		beforeInvalid := len(store.records)
		leakingConfig := json.RawMessage(`{"format_version":3,"route":"/public","url":"https://example.test","branch":"main","branch_assignment":{"route_id":"r1"},"dataModel":{"formatVersion":1,"entities":[]},"records":[{"id":"record-1"}],"page":{"id":"` + page.ID + `","name":"Boundary","placements":[]}}`)
		request := scenarioSnapshotRequest(t, page.ID, root.ID, root.Revision, "Invalid boundary", "web010-boundary-snap-0001", websiteapi.SnapshotPayload{Config: leakingConfig, Metadata: json.RawMessage(`{}`)})
		if _, err := service.CreateSnapshot(ctx, request); !errors.Is(err, websiteapi.ErrInvalid) {
			t.Fatalf("snapshot with Router/record fields = %v, want invalid", err)
		}
		if len(store.records) != beforeInvalid {
			t.Fatalf("invalid structural input wrote records: before=%d after=%d", beforeInvalid, len(store.records))
		}

		job, err := service.BuildSnapshot(ctx, websiteapi.BuildSnapshotRequest{
			SnapshotID: root.ID, Locale: "en-US", IdempotencyKey: "web010-build-key-000001",
		})
		if !errors.Is(err, websiteapi.ErrUnavailable) {
			t.Fatalf("build without durable workflow = %#v, %v, want unavailable", job, err)
		}
		if !reflect.DeepEqual(job, websiteapi.BuildJob{}) {
			t.Fatalf("unavailable build exposed partial result: %#v", job)
		}

		for _, model := range []any{
			websiteapi.CreateSnapshotRequest{}, websiteapi.SnapshotPayload{}, websiteapi.BuildSnapshotRequest{}, websiteapi.BuildJob{},
		} {
			valueType := reflect.TypeOf(model)
			for _, forbidden := range []string{"Route", "URL", "Branch", "BranchAssignment", "Records", "RecordValues", "FileSet", "Namespace", "Module"} {
				if _, exists := valueType.FieldByName(forbidden); exists {
					t.Fatalf("%s must not expose Router assignment field %s", valueType.Name(), forbidden)
				}
			}
		}
		for _, valueType := range []reflect.Type{
			reflect.TypeOf(websiteapi.CreateSnapshotRequest{}), reflect.TypeOf(websiteapi.BuildSnapshotRequest{}),
		} {
			field, exists := valueType.FieldByName("IdempotencyKey")
			if !exists || field.Tag.Get("json") != "-" {
				t.Fatalf("%s.IdempotencyKey must never be accepted in a JSON body", valueType.Name())
			}
		}
	})
}

func TestWebsiteScenarioDependencyResolverCoverage(t *testing.T) {
	t.Run("WEB-013", func(t *testing.T) {
		resolver, err := runtime.NewResolver([]runtime.Candidate{
			{FeatureID: "api", Version: "1.2.0", Digest: "sha256:api-120", APIVersion: "v1"},
			{FeatureID: "api", Version: "1.3.0", Digest: "sha256:api-130", APIVersion: "v1"},
			{FeatureID: "ui", Version: "2.0.0", Digest: "sha256:ui-200", APIVersion: "v1", Depends: []runtime.Dependency{{FeatureID: "api", Range: "^1.0.0"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		lock, err := resolver.Resolve([]runtime.Dependency{{FeatureID: "ui", Range: "^2.0.0"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(lock) != 2 || lock[0].FeatureID != "api" || lock[0].Version != "1.3.0" || lock[0].Digest != "sha256:api-130" || lock[0].APIVersion != "v1" ||
			lock[1].FeatureID != "ui" || lock[1].Version != "2.0.0" {
			t.Fatalf("resolved dependency lock = %#v", lock)
		}
		replayed, err := resolver.Resolve([]runtime.Dependency{{FeatureID: "ui", Range: "^2.0.0"}}, lock)
		if err != nil || !reflect.DeepEqual(lock, replayed) {
			t.Fatalf("locked dependency closure changed: lock=%#v replayed=%#v err=%v", lock, replayed, err)
		}

		ambiguous, err := runtime.NewResolver([]runtime.Candidate{
			{FeatureID: "api", Version: "1.3.0", Digest: "sha256:api-130", APIVersion: "v1"},
			{FeatureID: "api", Version: "1.3.0", Digest: "sha256:api-130-alt", APIVersion: "v1"},
		})
		if err == nil || !errors.Is(err, runtime.ErrDependency) {
			t.Fatalf("ambiguous catalog version was accepted: resolver=%#v err=%v", ambiguous, err)
		}

		cycleResolver, err := runtime.NewResolver([]runtime.Candidate{
			{FeatureID: "api", Version: "1.0.0", Digest: "sha256:a", APIVersion: "v1", Depends: []runtime.Dependency{{FeatureID: "ui", Range: "^1.0.0"}}},
			{FeatureID: "ui", Version: "1.0.0", Digest: "sha256:b", APIVersion: "v1", Depends: []runtime.Dependency{{FeatureID: "api", Range: "^1.0.0"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cycleResolver.Resolve([]runtime.Dependency{{FeatureID: "api", Range: "^1.0.0"}}, nil); !errors.Is(err, runtime.ErrCycle) {
			t.Fatalf("dependency cycle = %v, want cycle", err)
		}
		changed := []runtime.LockEntry{{FeatureID: "ui", Version: "2.0.0", Digest: "sha256:ui-changed", APIVersion: "v1"}}
		if _, err := resolver.Resolve([]runtime.Dependency{{FeatureID: "ui", Range: "^2.0.0"}}, changed); !errors.Is(err, runtime.ErrLock) {
			t.Fatalf("changed locked dependency = %v, want lock mismatch", err)
		}
	})
}

func TestWebsiteScenarioAdminContractCoverage(t *testing.T) {
	t.Run("WEB-017", func(t *testing.T) {
		root := websiteRepositoryRoot(t)
		openapi, err := os.ReadFile(filepath.Join(root, "spec", "presentation", "openapi", "website.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		handler, err := os.ReadFile(filepath.Join(root, "presentation", "admin", "sites", "handler.go"))
		if err != nil {
			t.Fatal(err)
		}
		contract := string(openapi)
		if !strings.Contains(contract, "openapi: 3.1.0") || !strings.Contains(contract, "/api/admin/snapshots:") {
			t.Fatal("Website OpenAPI contract is missing its flat 3.1 admin paths")
		}
		for _, versioned := range []string{"/api/v1", "/api/admin/v1"} {
			if strings.Contains(contract, versioned) || strings.Contains(string(handler), versioned) {
				t.Fatalf("versioned public route %s leaked into Website contract", versioned)
			}
		}
		for _, route := range []string{
			`POST "+apiPrefix+"/snapshots"`, `POST "+apiPrefix+"/pages"`, `POST "+apiPrefix+"/builds"`,
			`PUT "+apiPrefix+"/model"`, `DELETE "+apiPrefix+"/snapshots/{snapshotID}"`,
		} {
			if !strings.Contains(string(handler), route) {
				t.Fatalf("Admin adapter does not mount %s", route)
			}
		}

		for _, valueType := range []reflect.Type{
			reflect.TypeOf(websiteapi.CreateSnapshotRequest{}), reflect.TypeOf(websiteapi.ApplyPageTemplateRequest{}),
			reflect.TypeOf(websiteapi.BuildSnapshotRequest{}), reflect.TypeOf(websiteapi.SnapshotPayload{}),
		} {
			for _, forbidden := range []string{"Route", "URL", "Branch", "BranchAssignment", "FileSet", "Namespace", "ModuleID", "RecordValues"} {
				if _, exists := valueType.FieldByName(forbidden); exists {
					t.Fatalf("%s must not accept %s from Admin HTTP clients", valueType.Name(), forbidden)
				}
			}
		}

		store := newMemoryStore()
		service := newService(t, store, core.AccessLevelRead, websiteConfig())
		ctx, err := service.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		unknown := "99999999-9999-4999-8999-999999999999"
		if _, err := service.GetSnapshot(ctx, unknown); !errors.Is(err, websiteapi.ErrNotFound) {
			t.Fatalf("unknown snapshot read = %v, want typed not found without existence disclosure", err)
		}
	})
}
