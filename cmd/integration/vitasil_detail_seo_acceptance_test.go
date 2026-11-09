package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
	branchconfig "websiteengine/pkg/config"
	"websiteengine/pkg/environment"
	httptransport "websiteengine/presentation/http"
)

// TestVitasilDetailSEOPublishedProductFieldsAcceptance verifies that detail
// metadata is projected from the same published Product record consumed by
// the Vitasil detail feature. Draft-only SEO values must never reach public HTML.
func TestVitasilDetailSEOPublishedProductFieldsAcceptance(t *testing.T) {
	featureDir := strings.TrimSpace(getenv("TEST_FEATURE_DIR"))
	if featureDir == "" {
		t.Skip("set TEST_FEATURE_DIR to the current Vitasil feature ZIP directory")
	}
	if !externalFeatureArchiveExists(t, featureDir, "catalog-detail") {
		t.Skip("current Vitasil intentionally has no product detail page; generic detail-route behavior is tested separately")
	}
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)

	archive, version := readExternalFeatureArchive(t, featureDir, "catalog-detail")
	verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
	if err != nil {
		t.Fatalf("verify current Vitasil catalog-detail ZIP: %v", err)
	}
	if verified.Manifest.Key != "catalog-detail" || verified.Manifest.FormatVersion != 2 || verified.Manifest.Version != version {
		t.Fatalf("unexpected Vitasil detail manifest: key=%q format=%d version=%q", verified.Manifest.Key, verified.Manifest.FormatVersion, verified.Manifest.Version)
	}
	status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, strings.NewReader(string(archive)), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register current Vitasil catalog-detail ZIP: status=%d body=%v", status, body)
	}

	port := modelAcceptancePort(t, verified.Manifest, "product")
	model, entity, fieldIDs, fieldKeys := modelAcceptanceEntity("product", "Product", port, true)
	image := env.uploadMedia("product-og-image.png", minimalPNG(t))
	imageID := image["id"].(string)
	if _, err := uuid.Parse(imageID); err != nil {
		t.Fatalf("uploaded media did not receive a system ID: %v", err)
	}
	binding := sitebuilder.DataInputBinding{
		InputKey: "product",
		EntityID: entity.ID,
		Fields:   modelAcceptanceFieldBindings(port, fieldIDs),
		Lookup:   &sitebuilder.DataInputLookupBinding{Argument: "slug", FieldID: fieldIDs["/slug"]},
		DetailSEO: &sitebuilder.DetailSEOFieldBindings{
			TitleFieldID:       fieldIDs["/title"],
			DescriptionFieldID: fieldIDs["/description"],
			OGImageFieldID:     fieldIDs["/imageSrc"],
		},
	}
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		DataModel:     &model,
		Pages: []sitebuilder.Page{{ID: "product", Name: "Товар", Kind: "detail", Placements: []sitebuilder.Placement{{
			ID: "product-detail", Build: sitebuilder.BuildRef{FeatureKey: "catalog-detail", Version: version}, Slot: "main",
			Arguments:    map[string]sitebuilder.ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}},
			DataBindings: []sitebuilder.DataInputBinding{binding},
		}}}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{{
			ID: "product-detail-route", Type: "path", Path: "/catalog/:slug", PageID: "product",
		}}},
	}
	configRaw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(configRaw, &configMap); err != nil {
		t.Fatal(err)
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Vitasil published detail SEO")
	snapshotID, err := uuid.Parse(snapshot["id"].(string))
	if err != nil {
		t.Fatalf("parse isolated snapshot ID: %v", err)
	}
	branchesBefore, err := env.snapshots.Branches(context.Background())
	if err != nil {
		t.Fatalf("read isolated branches before indexability setup: %v", err)
	}
	indexable, nonIndexable := true, false
	branchesAfter, err := env.snapshots.RegisterBranches(context.Background(), []branchconfig.BranchConfig{
		{Name: "Integration Development", HTTPAddr: ":18081", CORSOrigins: []string{"https://site.example"}, Indexable: &indexable},
		{Name: "Integration Production", HTTPAddr: ":18082", CORSOrigins: []string{"https://site.example"}, Indexable: &nonIndexable},
	})
	if err != nil {
		t.Fatalf("set indexable policy in disposable branch configuration: %v", err)
	}
	branchState := func(branches []sitebuilder.BranchRecord) map[uuid.UUID]*uuid.UUID {
		state := make(map[uuid.UUID]*uuid.UUID, len(branches))
		for _, branch := range branches {
			var assignment *uuid.UUID
			if branch.SnapshotID != nil {
				id := *branch.SnapshotID
				assignment = &id
			}
			state[branch.ID] = assignment
		}
		return state
	}
	assignmentsBefore, assignmentsAfter := branchState(branchesBefore), branchState(branchesAfter)
	if len(assignmentsBefore) != len(assignmentsAfter) {
		t.Fatalf("indexability setup changed the configured branch set: before=%v after=%v", assignmentsBefore, assignmentsAfter)
	}
	for id, before := range assignmentsBefore {
		after, exists := assignmentsAfter[id]
		if !exists || (before == nil) != (after == nil) || (before != nil && *before != *after) {
			t.Fatalf("indexability setup changed branch assignment %s: before=%v after=%v", id, before, after)
		}
	}
	indexing := make(map[environment.Environment]bool, len(env.routerDeps.BranchIndexable))
	for branch, enabled := range env.routerDeps.BranchIndexable {
		indexing[branch] = enabled
	}
	indexing[env.devBranch] = true // Isolated indexable-origin policy for cache and canonical assertions.
	deps := env.routerDeps
	deps.BranchIndexable = indexing
	publicServer := httptest.NewServer(httptransport.NewPublicRouter(deps, env.devBranch))
	defer publicServer.Close()

	values := modelAcceptanceValues(fieldKeys, map[string]any{
		"/slug":        "rosehip",
		"/title":       "Опубликованный заголовок товара",
		"/description": "Опубликованное описание товара",
		"/imageSrc":    imageID,
	})
	workspace, err := env.snapshots.ModelContentWorkspace(context.Background(), snapshotID)
	if err != nil {
		t.Fatalf("read empty model content workspace: %v", err)
	}
	draft, err := env.snapshots.SaveModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, sitebuilder.ModelContentDraftInput{
		Revision: workspace.Revision,
		Records:  []sitebuilder.ModelContentRecord{{EntityID: entity.ID, SortOrder: 0, Values: values}},
	})
	if err != nil {
		t.Fatalf("save Vitasil detail SEO draft: %v", err)
	}
	if draft.PublishedRevision == draft.Revision {
		t.Fatalf("saving a draft implicitly published its SEO values: %+v", draft)
	}

	publicURL := publicServer.URL + "/catalog/rosehip"
	getHTML := func() (int, string, http.Header) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, publicURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := publicServer.Client().Do(request)
		if err != nil {
			t.Fatalf("request Vitasil detail page: %v", err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read Vitasil detail HTML: %v", err)
		}
		return response.StatusCode, string(data), response.Header.Clone()
	}
	status, draftHTML, _ := getHTML()
	if status != http.StatusNotFound || strings.Contains(draftHTML, "Опубликованный заголовок товара") || strings.Contains(draftHTML, "Опубликованное описание товара") || strings.Contains(draftHTML, imageID) {
		t.Fatalf("public detail page exposed draft-only SEO or media: status=%d html=%s", status, draftHTML)
	}

	if _, err := env.snapshots.PublishModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, draft.Revision); !errors.Is(err, sitebuilder.ErrConflict) {
		t.Fatalf("publication must wait for required media renditions, got %v", err)
	}
	processed, err := env.media.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("process required Vitasil detail image rendition: processed=%t err=%v", processed, err)
	}
	if _, err := env.snapshots.PublishModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, draft.Revision); err != nil {
		t.Fatalf("publish Vitasil detail SEO record: %v", err)
	}
	status, publishedHTML, headers := getHTML()
	if status != http.StatusOK {
		t.Fatalf("published detail route status=%d, want 200: %s", status, publishedHTML)
	}
	headStart, headEnd := strings.Index(publishedHTML, "<head>"), strings.Index(publishedHTML, "</head>")
	if headStart < 0 || headEnd <= headStart {
		t.Fatalf("public HTML does not contain a head section: %s", publishedHTML)
	}
	head := publishedHTML[headStart:headEnd]
	for _, expected := range []string{
		"<title>Опубликованный заголовок товара</title>",
		"name=\"description\" content=\"Опубликованное описание товара\"",
		"property=\"og:title\" content=\"Опубликованный заголовок товара\"",
		"property=\"og:description\" content=\"Опубликованное описание товара\"",
		"property=\"og:image\"",
		imageID,
	} {
		if !strings.Contains(head, expected) {
			t.Fatalf("published detail HTML head is missing %q: %s", expected, head)
		}
	}
	if headers.Get("ETag") == "" {
		t.Fatalf("published detail HTML must carry the current render validator: headers=%v", headers)
	}

	values[fieldKeys["/title"]], values[fieldKeys["/description"]] = "Черновое SEO-обновление", "Неопубликованное описание"
	updatedDraft, err := env.snapshots.SaveModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, sitebuilder.ModelContentDraftInput{
		Revision: draft.Revision,
		Records:  []sitebuilder.ModelContentRecord{{ID: draft.Records[0].ID, EntityID: entity.ID, SortOrder: 0, Values: values}},
	})
	if err != nil {
		t.Fatalf("save un-published SEO update: %v", err)
	}
	status, stillPublishedHTML, stillPublishedHeaders := getHTML()
	if status != http.StatusOK || !strings.Contains(stillPublishedHTML, "Опубликованный заголовок товара") || strings.Contains(stillPublishedHTML, "Черновое SEO-обновление") {
		t.Fatalf("public route did not retain the published SEO revision: status=%d html=%s", status, stillPublishedHTML)
	}
	if stillPublishedHeaders.Get("ETag") != headers.Get("ETag") {
		t.Fatalf("a draft changed the public detail render validator: before=%q after=%q", headers.Get("ETag"), stillPublishedHeaders.Get("ETag"))
	}
	if _, err := env.snapshots.PublishModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, updatedDraft.Revision); err != nil {
		t.Fatalf("publish updated detail SEO fields: %v", err)
	}
	status, updatedPublishedHTML, updatedHeaders := getHTML()
	if status != http.StatusOK || !strings.Contains(updatedPublishedHTML, "<title>Черновое SEO-обновление</title>") || strings.Contains(updatedPublishedHTML, "Опубликованный заголовок товара") {
		t.Fatalf("publishing SEO changes did not update the public head: status=%d html=%s", status, updatedPublishedHTML)
	}
	if updatedHeaders.Get("ETag") == "" || updatedHeaders.Get("ETag") == headers.Get("ETag") {
		t.Fatalf("published SEO change did not invalidate the public render identity: before=%q after=%q", headers.Get("ETag"), updatedHeaders.Get("ETag"))
	}
	t.Logf("Vitasil detail SEO passed: snapshot=%s build=catalog-detail@%s media=%s publishedRevision=%d", snapshotID, version, imageID, updatedDraft.Revision)
}

func getenv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
