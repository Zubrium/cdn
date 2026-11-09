package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	"websiteengine/pkg/environment"
	httptransport "websiteengine/presentation/http"
)

func TestBranchModeHidesLegacyGlobalFeatureAndContentAPIs(t *testing.T) {
	e := newTestEnv(t)
	build := uploadFeatureBuild(t, e, testFeatureManifestFor("branch-api-form", "1.0.0"), "export default function Form() {}")
	featureID := build["featureId"].(string)
	indexable := false
	if _, err := e.snapshots.RegisterBranches(context.Background(), []config.BranchConfig{{
		Name: "branch api test", HTTPAddr: ":39872", CORSOrigins: []string{}, Indexable: &indexable,
	}}); err != nil {
		t.Fatalf("register static branch: %v", err)
	}

	deps := e.routerDeps
	server := httptest.NewServer(httptransport.NewRouter(deps))
	t.Cleanup(server.Close)

	request := func(method, path string) (int, string) {
		t.Helper()
		var body io.Reader
		if method != http.MethodGet {
			body = bytes.NewBufferString(`{}`)
		}
		req, err := http.NewRequest(method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+e.adminToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		response, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(response)
	}

	for _, path := range []string{
		"/api/v2/admin/branches",
		"/api/v2/admin/site/snapshots",
		"/api/v2/admin/content/snapshots",
		"/api/v1/admin/features?type=form",
		"/api/v1/admin/features/" + featureID + "/versions",
	} {
		if status, body := request(http.MethodGet, path); status != http.StatusOK {
			t.Errorf("branch or required read-only API GET %s: got %d, want 200: %s", path, status, body)
		}
	}

	legacy := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/admin/features"},
		{http.MethodGet, "/api/v1/admin/features/" + featureID},
		{http.MethodPatch, "/api/v1/admin/features/" + featureID},
		{http.MethodPost, "/api/v1/admin/features/" + featureID + "/archive"},
		{http.MethodGet, "/api/v1/admin/features/" + featureID + "/draft"},
		{http.MethodPut, "/api/v1/admin/features/" + featureID + "/draft"},
		{http.MethodPost, "/api/v1/admin/features/" + featureID + "/publish"},
		{http.MethodPost, "/api/v1/admin/features/" + featureID + "/promote"},
		{http.MethodPost, "/api/v1/admin/features/" + featureID + "/rollback"},
		{http.MethodGet, "/api/v1/admin/features/" + featureID + "/entries"},
		{http.MethodPost, "/api/v1/admin/features/" + featureID + "/entries"},
		{http.MethodGet, "/api/v2/admin/features/" + featureID + "/settings"},
		{http.MethodPost, "/api/v2/admin/features/" + featureID + "/settings"},
		{http.MethodPut, "/api/v2/admin/features/" + featureID + "/entries/entry/move"},
		{http.MethodPut, "/api/v2/admin/features/" + featureID + "/entries/order"},
		{http.MethodPost, "/api/v2/admin/features/" + featureID + "/entries/import"},
		{http.MethodGet, "/api/v1/admin/entries/entry"},
		{http.MethodPatch, "/api/v1/admin/entries/entry/draft"},
		{http.MethodPost, "/api/v1/admin/entries/entry/publish"},
		{http.MethodPost, "/api/v1/admin/entries/entry/promote"},
		{http.MethodPost, "/api/v1/admin/entries/entry/rollback"},
		{http.MethodPost, "/api/v1/admin/entries/entry/unpublish"},
		{http.MethodPost, "/api/v1/admin/entries/entry/discard-draft"},
		{http.MethodPost, "/api/v1/admin/entries/entry/archive"},
		{http.MethodGet, "/api/v1/admin/entries/entry/versions"},
		{http.MethodGet, "/api/v1/admin/releases"},
		{http.MethodPost, "/api/v1/admin/releases"},
		{http.MethodPost, "/api/v1/admin/releases/release/activate"},
		{http.MethodPost, "/api/v1/admin/releases/release/rollback"},
		{http.MethodGet, "/api/v1/admin/site/environments"},
		{http.MethodGet, "/api/v2/admin/site/metadata"},
		{http.MethodPut, "/api/v2/admin/site/metadata"},
		{http.MethodGet, "/api/v2/admin/site/design-system"},
		{http.MethodPost, "/api/v2/admin/site/snapshots/snapshot/deploy"},
		{http.MethodGet, "/api/v2/admin/site/draft"},
		{http.MethodPut, "/api/v2/admin/site/draft"},
		{http.MethodPatch, "/api/v1/admin/site/environments/prod"},
		{http.MethodGet, "/api/v1/admin/release-sources"},
		{http.MethodPost, "/api/v1/admin/release-sources"},
		{http.MethodDelete, "/api/v1/admin/release-sources/source"},
		{http.MethodGet, "/api/v1/admin/release-sources/source/releases"},
		{http.MethodPost, "/api/v1/admin/release-sources/source/sync"},
	}
	for _, endpoint := range legacy {
		if status, _ := request(endpoint.method, endpoint.path); status < http.StatusBadRequest {
			t.Errorf("legacy global API %s %s unexpectedly succeeded: %d", endpoint.method, endpoint.path, status)
		}
	}

	// The persistent structural-draft API is removed in every router mode.
	legacyDeps := e.routerDeps
	legacyRouter := httptransport.NewRouter(legacyDeps)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		body := io.Reader(nil)
		if method != http.MethodGet {
			body = bytes.NewBufferString(`{}`)
		}
		req := httptest.NewRequest(method, "/api/v2/admin/site/draft", body)
		req.Header.Set("Authorization", "Bearer "+e.adminToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		legacyRouter.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Errorf("removed site-draft route %s unexpectedly returned %d: %s", method, response.Code, response.Body.String())
		}
	}
}

func TestStaticBranchRuntimeDoesNotSeedLegacyDrafts(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	indexable := false
	branches, err := e.snapshots.RegisterBranches(ctx, []config.BranchConfig{{Name: "clean branch", HTTPAddr: ":39871", CORSOrigins: []string{}, Indexable: &indexable}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg sitebuilder.SiteConfig
	if err := json.Unmarshal([]byte(`{"formatVersion":3,"pages":[{"id":"home","name":"Главная","kind":"page","placements":[]}],"routeTree":{"id":"site","type":"site","children":[{"id":"root","type":"path","path":"/","pageId":"home","children":[]}]}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Clean", Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.snapshots.AssignBranch(ctx, actor, branches[0].ID, snapshot.ID, branches[0].Revision); err != nil {
		t.Fatal(err)
	}
	var missingLegacyTables bool
	if err = e.pool.QueryRow(ctx, `SELECT to_regclass('public.site_draft_config') IS NULL AND to_regclass('public.site_metadata_state') IS NULL AND to_regclass('public.site_design_system_state') IS NULL`).Scan(&missingLegacyTables); err != nil {
		t.Fatal(err)
	}
	if !missingLegacyTables {
		t.Fatal("clean branch/snapshot schema must not contain persistent drafts or singleton metadata/design state")
	}
	var legacyPointerColumns int
	if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name IN ('features','feature_entries') AND column_name IN ('draft_version_id','dev_version_id','prod_version_id')`).Scan(&legacyPointerColumns); err != nil {
		t.Fatal(err)
	}
	if legacyPointerColumns != 0 {
		t.Fatalf("clean branch/snapshot schema must not contain global feature pointers, found %d columns", legacyPointerColumns)
	}
	var settingsBefore int
	if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM site_settings`).Scan(&settingsBefore); err != nil {
		t.Fatal(err)
	}
	composition, err := e.snapshots.Runtime(ctx, environment.Environment(branches[0].ID.String()), "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if composition.Metadata.Title != "" {
		t.Fatalf("unconfigured branch inherited legacy title: %q", composition.Metadata.Title)
	}
	var settingsAfter int
	if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM site_settings`).Scan(&settingsAfter); err != nil {
		t.Fatal(err)
	}
	if settingsBefore != settingsAfter {
		t.Fatalf("runtime changed site settings: before=%d after=%d", settingsBefore, settingsAfter)
	}
}
