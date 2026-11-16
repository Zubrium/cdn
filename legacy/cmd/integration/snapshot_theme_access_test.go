package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"websiteengine/features/sitebuilder"
)

func TestPublicSnapshotThemeCSSRequiresCurrentBranchAssignment(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	featureKey := "theme-css-guard"
	uploadFeatureBuild(t, e, testFeatureManifestFor(featureKey, "1.0.0"), "export default function Feature() {}")

	configJSON, err := json.Marshal(singlePageConfig(featureKey, "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := sitebuilder.ParseSiteConfig(configJSON)
	if err != nil {
		t.Fatal(err)
	}
	parentTheme := sitebuilder.NewDesignSystem()
	parent, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{
		Name: "Assigned theme parent", Config: &config, DesignSystem: &parentTheme,
	})
	if err != nil {
		t.Fatalf("create parent snapshot: %v", err)
	}

	branchID := uuid.MustParse(e.devBranch.String())
	branch, err := e.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.snapshots.AssignBranch(ctx, actor, branchID, parent.ID, branch.Revision, &parent.ContentRevision); err != nil {
		t.Fatalf("assign parent snapshot: %v", err)
	}
	assertSnapshotThemeCSS := func(server *httptest.Server, snapshotID uuid.UUID, wantStatus int, wantToken string) {
		t.Helper()
		status, body, headers := fileMediaRequest(t, server, http.MethodGet, "/api/v2/site-themes/snapshots/"+snapshotID.String(), "", nil)
		if status != wantStatus {
			t.Fatalf("theme CSS for branch=%s snapshot=%s: status=%d, want %d, body=%s", server.URL, snapshotID, status, wantStatus, body)
		}
		if wantStatus != http.StatusOK {
			if headers.Get("Cache-Control") != "no-store" {
				t.Fatalf("denied theme CSS should not be cached: Cache-Control=%q", headers.Get("Cache-Control"))
			}
			if wantToken != "" && strings.Contains(string(body), wantToken) {
				t.Fatalf("denied theme CSS leaked token value %q", wantToken)
			}
			return
		}
		if wantToken != "" && !strings.Contains(string(body), wantToken) {
			t.Fatalf("assigned theme CSS missing token %q: %s", wantToken, body)
		}
	}

	assertSnapshotThemeCSS(e.devServer, parent.ID, http.StatusOK, "")
	assertSnapshotThemeCSS(e.prodServer, parent.ID, http.StatusNotFound, "")

	childTheme := sitebuilder.NewDesignSystem()
	groupID, tokenID := uuid.NewString(), uuid.NewString()
	childTheme.Groups = []sitebuilder.TokenGroup{{ID: groupID, Name: "Unpublished"}}
	childTheme.BaseTokens = []sitebuilder.BaseToken{{
		ID: tokenID, Name: "Private Accent", GroupID: groupID, Kind: "color",
		Value: sitebuilder.TokenValue{Color: &sitebuilder.TokenColor{Hex: "#c0ffee", Opacity: 100}},
	}}
	child, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{
		Name: "Unassigned theme child", ParentID: &parent.ID, DesignSystem: &childTheme,
	})
	if err != nil {
		t.Fatalf("create unassigned child: %v", err)
	}
	assertSnapshotThemeCSS(e.devServer, child.ID, http.StatusNotFound, "rgb(192 255 238 / 100%)")
	assertSnapshotThemeCSS(e.prodServer, child.ID, http.StatusNotFound, "rgb(192 255 238 / 100%)")
	previewJSON, err := json.Marshal(childTheme)
	if err != nil {
		t.Fatal(err)
	}
	previewToken, err := e.snapshots.CreateDesignPreview(ctx, previewJSON)
	if err != nil {
		t.Fatalf("create temporary design preview: %v", err)
	}
	status, previewCSS, _ := fileMediaRequest(t, e.prodServer, http.MethodGet, "/api/v2/site-themes/previews/"+previewToken, "", nil)
	if status != http.StatusOK || !strings.Contains(string(previewCSS), "rgb(192 255 238 / 100%)") {
		t.Fatalf("opaque preview should remain independent of branch assignment: status=%d css=%s", status, previewCSS)
	}

	branch, err = e.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.snapshots.AssignBranch(ctx, actor, branchID, child.ID, branch.Revision, &child.ContentRevision); err != nil {
		t.Fatalf("assign child snapshot: %v", err)
	}
	assertSnapshotThemeCSS(e.devServer, child.ID, http.StatusOK, "rgb(192 255 238 / 100%)")
	assertSnapshotThemeCSS(e.prodServer, child.ID, http.StatusNotFound, "rgb(192 255 238 / 100%)")

	branch, err = e.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.snapshots.AssignBranch(ctx, actor, branchID, parent.ID, branch.Revision, &parent.ContentRevision); err != nil {
		t.Fatalf("restore parent assignment: %v", err)
	}
	assertSnapshotThemeCSS(e.devServer, child.ID, http.StatusNotFound, "rgb(192 255 238 / 100%)")
	assertSnapshotThemeCSS(e.devServer, parent.ID, http.StatusOK, "")

	status, body, _ := fileMediaRequest(t, e.devServer, http.MethodGet, "/api/v2/runtime?path=/", "", nil)
	var runtime sitebuilder.RuntimeComposition
	if err := json.Unmarshal(body, &runtime); err != nil || status != http.StatusOK || runtime.Snapshot.ID != parent.ID {
		t.Fatalf("runtime should follow restored branch assignment: status=%d snapshot=%s err=%v body=%s", status, runtime.Snapshot.ID, err, body)
	}
}
