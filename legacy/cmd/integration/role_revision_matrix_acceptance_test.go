package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/pkg/environment"
)

type roleRevisionCase struct {
	role      string
	token     string
	site      bool
	manageSub bool
	admin     bool
}

// TestRoleRevisionMatrixAcceptance uses only generated feature builds, records,
// files and submissions in the disposable integration environment. The shared
// newTestEnv safety guard requires a uniquely named loopback QA database with
// no other active sessions before it migrates or truncates the schema.
func TestRoleRevisionMatrixAcceptance(t *testing.T) {
	requireRoleRevisionMatrixDatabase(t)
	env := newTestEnv(t)
	roles := roleRevisionTokens(t, env)

	const formKey = "qa-role-revision-form"
	manifest := testFeatureManifestFor(formKey, "1.0.0")
	manifest["form"] = map[string]any{"submissionSchema": objectSchema(map[string]any{
		"name": map[string]any{"type": "string"},
	}, "name")}
	uploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")

	entityID, titleFieldID := uuid.NewString(), uuid.NewString()
	config := singlePageConfig(formKey, "1.0.0")
	config["dataModel"] = map[string]any{
		"formatVersion": 1,
		"entities": []any{map[string]any{
			"id": entityID, "key": "article", "name": "Article",
			"fields": []any{map[string]any{
				"id": titleFieldID, "key": "title", "name": "Title", "type": "text",
			}},
		}},
		"relations": []any{},
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, config, "Role and revision acceptance")
	snapshotID := snapshot["id"].(string)
	contentURL := "/api/v2/admin/content/snapshots/" + snapshotID + "/model"
	branchID := string(env.devBranch)
	branchURL := "/api/v2/admin/branches/" + branchID + "/snapshot"

	// Anonymous requests are denied before handler validation or writes.
	assertRoleRevisionRequest(t, env, "anonymous content read", "anonymous", http.MethodGet, contentURL, "", nil, http.StatusUnauthorized)
	assertRoleRevisionRequest(t, env, "anonymous content save", "anonymous", http.MethodPut, contentURL, "", map[string]any{"revision": 0, "records": []any{}, "selections": []any{}}, http.StatusUnauthorized)
	assertRoleRevisionRequest(t, env, "anonymous structure read", "anonymous", http.MethodGet, "/api/v2/admin/site/snapshots", "", nil, http.StatusUnauthorized)
	assertRoleRevisionRequest(t, env, "anonymous branch assignment", "anonymous", http.MethodPut, branchURL, "", map[string]any{"snapshotId": snapshotID, "revision": 0, "contentRevision": 0}, http.StatusUnauthorized)
	assertRoleRevisionRequest(t, env, "anonymous submissions read", "anonymous", http.MethodGet, "/api/v1/admin/submissions", "", nil, http.StatusUnauthorized)
	assertRoleRevisionRequest(t, env, "anonymous media read", "anonymous", http.MethodGet, "/api/v1/admin/media", "", nil, http.StatusUnauthorized)

	assertRoleRevisionRequest(t, env, "anonymous content publish", "anonymous", http.MethodPost, contentURL+"/publish", "", map[string]any{"revision": 0}, http.StatusUnauthorized)
	initialBranch, err := env.snapshots.Branch(context.Background(), uuid.MustParse(branchID))
	if err != nil || initialBranch.SnapshotID == nil || initialBranch.SnapshotID.String() != snapshotID {
		t.Fatalf("anonymous branch assignment changed the assignment: branch=%+v err=%v", initialBranch, err)
	}
	workspace := roleMatrixWorkspace(t, env, contentURL, env.adminToken)
	if got := roleMatrixInt(t, workspace["revision"]); got != 0 {
		t.Fatalf("anonymous content writes changed workspace revision: %d", got)
	}

	roleMatrixStructureAndBranchChecks(t, env, roles, snapshotID, branchID)
	roleMatrixFeatureBuildChecks(t, env, roles)

	// All four roles can edit, save, publish, read history and restore content.
	for _, role := range roles {
		current := roleMatrixWorkspace(t, env, contentURL, role.token)
		records := cloneModelContentJSONArray(t, current["records"].([]any))
		records = append(records, map[string]any{
			"entityId": entityID, "sortOrder": len(records), "values": map[string]any{"title": "Created by " + role.role},
		})
		status, saved, _ := env.request(http.MethodPut, contentURL, role.token, map[string]any{
			"revision": current["revision"], "records": records, "selections": current["selections"],
		})
		assertRoleRevisionStatus(t, "content save", role.role, status, http.StatusOK)
		status, published, _ := env.request(http.MethodPost, contentURL+"/publish", role.token, map[string]any{"revision": saved["revision"]})
		assertRoleRevisionStatus(t, "content publish", role.role, status, http.StatusOK)
		if roleMatrixInt(t, published["publishedRevision"]) != roleMatrixInt(t, saved["revision"]) {
			t.Fatalf("%s published revision does not match the saved revision: saved=%v published=%v", role.role, saved, published)
		}
		status, history, _ := env.request(http.MethodGet, contentURL+"/history", role.token, nil)
		assertRoleRevisionStatus(t, "content history", role.role, status, http.StatusOK)
		items, ok := history["items"].([]any)
		if !ok || len(items) == 0 {
			t.Fatalf("%s did not receive publication history: %v", role.role, history)
		}
		publicationID := items[0].(map[string]any)["id"].(string)
		current = roleMatrixWorkspace(t, env, contentURL, role.token)
		status, restored, _ := env.request(http.MethodPost, contentURL+"/history/"+publicationID+"/restore", role.token,
			map[string]any{"revision": current["revision"]})
		assertRoleRevisionStatus(t, "content history restore", role.role, status, http.StatusOK)
		if roleMatrixInt(t, restored["publishedRevision"]) != roleMatrixInt(t, current["publishedRevision"]) {
			t.Fatalf("%s restore implicitly published content: before=%v after=%v", role.role, current, restored)
		}
	}

	roleMatrixRevisionConflictChecks(t, env, contentURL)
	roleMatrixMediaChecks(t, env, roles)
	roleMatrixSubmissionChecks(t, env, roles, formKey)
	roleMatrixStaleStructuralForkCheck(t, env, roles, snapshotID, contentURL, entityID)
}

func requireRoleRevisionMatrixDatabase(t *testing.T) {
	t.Helper()
	rawURL := os.Getenv("TEST_DATABASE_URL")
	if rawURL == "" {
		return // newTestEnv will skip without configuring a database.
	}
	parsed, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse role/revision disposable database URL: %v", err)
	}
	const prefix = "website_engine_role_revision_qa_"
	if !strings.HasPrefix(parsed.Database, prefix) || strings.TrimPrefix(parsed.Database, prefix) == "" {
		t.Skipf("role/revision acceptance requires a uniquely named disposable database with prefix %q; refusing to migrate or truncate %q", prefix, parsed.Database)
	}
}

func roleRevisionTokens(t *testing.T, env *testEnv) []roleRevisionCase {
	t.Helper()
	roles := []roleRevisionCase{{role: "admin", token: env.adminToken, site: true, manageSub: true, admin: true}}
	for _, role := range []struct {
		name      string
		site      bool
		manageSub bool
	}{
		{name: "moderator", site: true, manageSub: true},
		{name: "staff"},
		{name: "user"},
	} {
		status, body, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken, map[string]any{
			"name": "role-revision-" + role.name, "role": role.name,
		})
		if status != http.StatusCreated {
			t.Fatalf("create synthetic %s token: status=%d body=%v", role.name, status, body)
		}
		roles = append(roles, roleRevisionCase{
			role: role.name, token: body["token"].(string), site: role.site,
			manageSub: role.manageSub, admin: false,
		})
	}
	return roles
}

func roleMatrixStructureAndBranchChecks(t *testing.T, env *testEnv, roles []roleRevisionCase, parentID, branchID string) {
	t.Helper()
	managerSnapshots := map[string]string{}
	for _, role := range roles {
		for _, path := range []string{"/api/v2/admin/site/snapshots", "/api/v2/admin/branches", "/api/v2/admin/feature-builds"} {
			status, body, _ := env.request(http.MethodGet, path, role.token, nil)
			want := http.StatusForbidden
			if role.site {
				want = http.StatusOK
			}
			assertRoleRevisionStatus(t, "site structure read", role.role, status, want)
			if status != want {
				t.Fatalf("site structure read body: %v", body)
			}
		}
	}

	initialCount := roleMatrixSnapshotCount(t, env)
	status, anonymousBody, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", "", map[string]any{
		"name": "Anonymous structure attempt", "parentSnapshotId": parentID, "expectedParentContentRevision": 0,
	})
	assertRoleRevisionStatus(t, "anonymous snapshot structure change", "anonymous", status, http.StatusUnauthorized)
	if got := roleMatrixSnapshotCount(t, env); got != initialCount {
		t.Fatalf("anonymous snapshot request had side effects: before=%d after=%d body=%v", initialCount, got, anonymousBody)
	}
	for _, role := range roles {
		status, body, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", role.token, map[string]any{
			"name": "Role matrix child " + role.role, "parentSnapshotId": parentID, "expectedParentContentRevision": 0,
		})
		want := http.StatusForbidden
		if role.site {
			want = http.StatusCreated
		}
		assertRoleRevisionStatus(t, "snapshot structure change", role.role, status, want)
		if status == http.StatusCreated {
			managerSnapshots[role.role] = body["id"].(string)
		}
		if role.site && status != http.StatusCreated {
			t.Fatalf("site manager snapshot creation body: %v", body)
		}
	}
	if got, want := roleMatrixSnapshotCount(t, env), initialCount+len(managerSnapshots); got != want {
		t.Fatalf("forbidden snapshot creates had side effects: count=%d, want=%d", got, want)
	}

	branchUUID, err := uuid.Parse(branchID)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		candidate := managerSnapshots[role.role]
		if candidate == "" {
			candidate = parentID
		}
		branch, err := env.snapshots.Branch(context.Background(), branchUUID)
		if err != nil {
			t.Fatal(err)
		}
		snapshotStatus, candidateInfo, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshots/"+candidate, env.adminToken, nil)
		if snapshotStatus != http.StatusOK {
			t.Fatalf("get branch assignment candidate: status=%d body=%v", snapshotStatus, candidateInfo)
		}
		candidateRevision := candidateInfo["snapshot"].(map[string]any)["contentRevision"]
		status, body, _ := env.request(http.MethodPut, "/api/v2/admin/branches/"+branchID+"/snapshot", role.token, map[string]any{
			"snapshotId": candidate, "revision": branch.Revision, "contentRevision": candidateRevision,
		})
		want := http.StatusForbidden
		if role.site {
			want = http.StatusOK
		}
		assertRoleRevisionStatus(t, "branch snapshot assignment", role.role, status, want)
		if status != want {
			t.Fatalf("branch assignment response for %s: %v", role.role, body)
		}
		updated, err := env.snapshots.Branch(context.Background(), branchUUID)
		if err != nil {
			t.Fatal(err)
		}
		if !role.site && (updated.Revision != branch.Revision || !sameOptionalUUID(updated.SnapshotID, branch.SnapshotID)) {
			t.Fatalf("forbidden %s branch assignment had side effects: before=%+v after=%+v", role.role, branch, updated)
		}
	}
}

func roleMatrixFeatureBuildChecks(t *testing.T, env *testEnv, roles []roleRevisionCase) {
	t.Helper()
	beforeAnonymous := roleMatrixBuildCount(t, env)
	anonymousArchive := featureArtifact(t, testFeatureManifestFor("qa-role-build-anonymous", "1.0.0"), "export default function Feature() { return null; }")
	status, anonymousBody, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", "", bytes.NewReader(anonymousArchive), "application/zip")
	assertRoleRevisionStatus(t, "anonymous feature build registration", "anonymous", status, http.StatusUnauthorized)
	if afterAnonymous := roleMatrixBuildCount(t, env); afterAnonymous != beforeAnonymous {
		t.Fatalf("anonymous feature build request had side effects: before=%d after=%d body=%v", beforeAnonymous, afterAnonymous, anonymousBody)
	}
	for _, role := range roles {
		key := "qa-role-build-" + role.role
		before := roleMatrixBuildCount(t, env)
		archive := featureArtifact(t, testFeatureManifestFor(key, "1.0.0"), "export default function Feature() { return null; }")
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", role.token, bytes.NewReader(archive), "application/zip")
		want := http.StatusForbidden
		if role.site && (status == http.StatusOK || status == http.StatusCreated) {
			want = status
		}
		assertRoleRevisionStatus(t, "feature build registration", role.role, status, want)
		if role.site && status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("site manager feature build registration failed: role=%s status=%d body=%v", role.role, status, body)
		}
		after := roleMatrixBuildCount(t, env)
		if role.site && after != before+1 {
			t.Fatalf("allowed feature build was not registered exactly once: role=%s before=%d after=%d", role.role, before, after)
		}
		if !role.site && after != before {
			t.Fatalf("forbidden feature build registration had side effects: role=%s before=%d after=%d", role.role, before, after)
		}
	}
}

func roleMatrixRevisionConflictChecks(t *testing.T, env *testEnv, contentURL string) {
	t.Helper()
	workspace := roleMatrixWorkspace(t, env, contentURL, env.adminToken)
	staleRevision := roleMatrixInt(t, workspace["revision"])
	records := cloneModelContentJSONArray(t, workspace["records"].([]any))
	if len(records) == 0 {
		t.Fatal("role matrix content fixture has no saved records")
	}
	firstID := records[0].(map[string]any)["id"].(string)
	serverRecords := cloneModelContentJSONArray(t, records)
	modelContentRecordByID(t, serverRecords, firstID)["values"].(map[string]any)["title"] = "Server revision"
	status, saved, _ := env.request(http.MethodPut, contentURL, env.adminToken, map[string]any{
		"revision": staleRevision, "records": serverRecords, "selections": workspace["selections"],
	})
	if status != http.StatusOK {
		t.Fatalf("advance server draft before stale-write check: status=%d body=%v", status, saved)
	}

	localRecords := cloneModelContentJSONArray(t, records)
	modelContentRecordByID(t, localRecords, firstID)["values"].(map[string]any)["title"] = "Local draft retained"
	status, conflict, _ := env.request(http.MethodPut, contentURL, env.adminToken, map[string]any{
		"revision": staleRevision, "records": localRecords, "selections": workspace["selections"],
	})
	assertRoleRevisionStatus(t, "stale content save", "admin", status, http.StatusConflict)
	if modelContentRecordByID(t, localRecords, firstID)["values"].(map[string]any)["title"] != "Local draft retained" {
		t.Fatalf("stale-save rejection mutated the caller's local draft: %v", localRecords)
	}
	current := roleMatrixWorkspace(t, env, contentURL, env.adminToken)
	currentRecord := modelContentRecordByID(t, current["records"].([]any), firstID)
	if currentRecord["values"].(map[string]any)["title"] != "Server revision" {
		t.Fatalf("stale save partially replaced server state: body=%v current=%v", conflict, current)
	}

	// Safe retry: explicitly rebase the retained local edit onto the latest
	// server workspace revision; the request never silently retries stale data.
	rebased := cloneModelContentJSONArray(t, current["records"].([]any))
	modelContentRecordByID(t, rebased, firstID)["values"].(map[string]any)["title"] = "Local draft retained"
	status, retried, _ := env.request(http.MethodPut, contentURL, env.adminToken, map[string]any{
		"revision": current["revision"], "records": rebased, "selections": current["selections"],
	})
	if status != http.StatusOK || modelContentRecordByID(t, retried["records"].([]any), firstID)["values"].(map[string]any)["title"] != "Local draft retained" {
		t.Fatalf("explicit safe retry did not preserve the local edit: status=%d body=%v", status, retried)
	}

	publishedBefore := roleMatrixInt(t, retried["publishedRevision"])
	status, publishConflict, _ := env.request(http.MethodPost, contentURL+"/publish", env.adminToken, map[string]any{
		"revision": roleMatrixInt(t, retried["revision"]) - 1,
	})
	assertRoleRevisionStatus(t, "stale content publish", "admin", status, http.StatusConflict)
	current = roleMatrixWorkspace(t, env, contentURL, env.adminToken)
	if roleMatrixInt(t, current["publishedRevision"]) != publishedBefore {
		t.Fatalf("stale publish partially changed published revision: conflict=%v current=%v", publishConflict, current)
	}

	historyStatus, history, _ := env.request(http.MethodGet, contentURL+"/history", env.adminToken, nil)
	if historyStatus != http.StatusOK || len(history["items"].([]any)) == 0 {
		t.Fatalf("load history for stale restore: status=%d body=%v", historyStatus, history)
	}
	publicationID := history["items"].([]any)[0].(map[string]any)["id"].(string)
	staleRestoreRevision := roleMatrixInt(t, current["revision"])
	newer := cloneModelContentJSONArray(t, current["records"].([]any))
	modelContentRecordByID(t, newer, firstID)["values"].(map[string]any)["title"] = "Newer server draft"
	status, newerDraft, _ := env.request(http.MethodPut, contentURL, env.adminToken, map[string]any{
		"revision": staleRestoreRevision, "records": newer, "selections": current["selections"],
	})
	if status != http.StatusOK {
		t.Fatalf("save newer draft before stale restore: status=%d body=%v", status, newerDraft)
	}
	status, restoreConflict, _ := env.request(http.MethodPost, contentURL+"/history/"+publicationID+"/restore", env.adminToken,
		map[string]any{"revision": staleRestoreRevision})
	assertRoleRevisionStatus(t, "stale history restore", "admin", status, http.StatusConflict)
	current = roleMatrixWorkspace(t, env, contentURL, env.adminToken)
	if got := modelContentRecordByID(t, current["records"].([]any), firstID)["values"].(map[string]any)["title"]; got != "Newer server draft" {
		t.Fatalf("stale restore partially replaced server draft: conflict=%v current=%v", restoreConflict, current)
	}
}

func roleMatrixMediaChecks(t *testing.T, env *testEnv, roles []roleRevisionCase) {
	t.Helper()
	imageBytes := minimalPNG(t)
	beforeAnonymous := roleMatrixMediaCount(t, env)
	var anonymousBuffer bytes.Buffer
	anonymousWriter := multipart.NewWriter(&anonymousBuffer)
	anonymousPart, err := anonymousWriter.CreateFormFile("file", "anonymous.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anonymousPart.Write(imageBytes); err != nil {
		t.Fatal(err)
	}
	if err := anonymousWriter.Close(); err != nil {
		t.Fatal(err)
	}
	status, anonymousBody, _ := env.rawRequest(http.MethodPost, "/api/v1/admin/media", "", &anonymousBuffer, anonymousWriter.FormDataContentType())
	assertRoleRevisionStatus(t, "anonymous media upload", "anonymous", status, http.StatusUnauthorized)
	if afterAnonymous := roleMatrixMediaCount(t, env); afterAnonymous != beforeAnonymous {
		t.Fatalf("anonymous media upload had side effects: before=%d after=%d body=%v", beforeAnonymous, afterAnonymous, anonymousBody)
	}
	mediaIDs := map[string]string{}
	for _, role := range roles {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		part, err := writer.CreateFormFile("file", "role-"+role.role+".png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		status, uploaded, _ := env.rawRequest(http.MethodPost, "/api/v1/admin/media", role.token, &buffer, writer.FormDataContentType())
		if status != http.StatusCreated {
			t.Fatalf("%s media upload: status=%d body=%v", role.role, status, uploaded)
		}
		mediaID := uploaded["id"].(string)
		mediaIDs[role.role] = mediaID
		status, listed, _ := env.request(http.MethodGet, "/api/v1/admin/media", role.token, nil)
		if status != http.StatusOK || len(listed["items"].([]any)) == 0 {
			t.Fatalf("%s media gallery/media picker listing: status=%d body=%v", role.role, status, listed)
		}
		status, _, _ = env.request(http.MethodGet, "/api/v1/admin/media/"+mediaID, role.token, nil)
		assertRoleRevisionStatus(t, "media picker item read", role.role, status, http.StatusOK)
		status, renamed, _ := env.request(http.MethodPatch, "/api/v1/admin/media/"+mediaID, role.token,
			map[string]any{"filename": "selected-" + role.role + ".png"})
		if status != http.StatusOK || renamed["original_filename"] != "selected-"+role.role+".png" {
			t.Fatalf("%s media rename: status=%d body=%v", role.role, status, renamed)
		}
	}

	adminMediaID := mediaIDs["admin"]
	for _, role := range roles {
		if role.admin {
			continue
		}
		status, body, _ := env.request(http.MethodDelete, "/api/v1/admin/media/"+adminMediaID, role.token, nil)
		assertRoleRevisionStatus(t, "permanent media delete", role.role, status, http.StatusForbidden)
		checkStatus, stored, _ := env.request(http.MethodGet, "/api/v1/admin/media/"+adminMediaID, env.adminToken, nil)
		if checkStatus != http.StatusOK || stored["status"] == "deleted" {
			t.Fatalf("forbidden media delete had side effects: role=%s response=%v stored=%v", role.role, body, stored)
		}
	}
	status, _, _ = env.request(http.MethodDelete, "/api/v1/admin/media/"+adminMediaID, env.adminToken, nil)
	if status != http.StatusNoContent {
		t.Fatalf("admin media delete: status=%d", status)
	}
	// The media API keeps a tombstone so administrators can inspect the deleted
	// row; public redirects must reject that same non-ready object.
	status, deleted, _ := env.request(http.MethodGet, "/api/v1/admin/media/"+adminMediaID, env.adminToken, nil)
	if status != http.StatusOK || deleted["status"] != "deleted" {
		t.Fatalf("admin media metadata should retain a deleted tombstone: status=%d body=%v", status, deleted)
	}
	redirectRequest, err := http.NewRequest(http.MethodGet, env.server.URL+"/media/"+adminMediaID, nil)
	if err != nil {
		t.Fatal(err)
	}
	redirectClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	redirectResponse, err := redirectClient.Do(redirectRequest)
	if err != nil {
		t.Fatalf("request public redirect for deleted media: %v", err)
	}
	redirectResponse.Body.Close()
	if redirectResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted media must not be publicly redirected: status=%d location=%q", redirectResponse.StatusCode, redirectResponse.Header.Get("Location"))
	}
}

func roleMatrixSubmissionChecks(t *testing.T, env *testEnv, roles []roleRevisionCase, formKey string) {
	t.Helper()
	ids := map[string]string{}
	for _, role := range roles {
		submission, err := env.forms.Submit(context.Background(), environment.Environment(string(env.devBranch)), formKey,
			json.RawMessage(`{"name":"Synthetic `+role.role+` submission"}`))
		if err != nil {
			t.Fatalf("create synthetic %s submission: %v", role.role, err)
		}
		ids[role.role] = submission.ID.String()
	}
	beforeStatus, beforeArchived := roleMatrixSubmissionState(t, env, ids["admin"])
	status, _, _ := env.request(http.MethodPatch, "/api/v1/admin/submissions/"+ids["admin"]+"/status", "", map[string]any{"status": "processed"})
	assertRoleRevisionStatus(t, "anonymous submission status change", "anonymous", status, http.StatusUnauthorized)
	status, _, _ = env.request(http.MethodPost, "/api/v1/admin/submissions/"+ids["admin"]+"/archive", "", nil)
	assertRoleRevisionStatus(t, "anonymous submission archive", "anonymous", status, http.StatusUnauthorized)
	afterStatus, afterArchived := roleMatrixSubmissionState(t, env, ids["admin"])
	if beforeStatus != afterStatus || beforeArchived != afterArchived {
		t.Fatalf("anonymous submission operations had side effects: before=%q/%v after=%q/%v", beforeStatus, beforeArchived, afterStatus, afterArchived)
	}

	for _, role := range roles {
		for _, path := range []string{"/api/v1/admin/submissions", "/api/v1/admin/submissions/export", "/api/v1/admin/submissions/" + ids[role.role]} {
			status, body, _ := env.request(http.MethodGet, path, role.token, nil)
			assertRoleRevisionStatus(t, "submission view/export", role.role, status, http.StatusOK)
			if status != http.StatusOK {
				t.Fatalf("submission read body for %s: %v", role.role, body)
			}
		}
	}

	for _, role := range roles {
		id := ids[role.role]
		if role.manageSub {
			status, body, _ := env.request(http.MethodPatch, "/api/v1/admin/submissions/"+id+"/status", role.token, map[string]any{"status": "processed"})
			if status != http.StatusOK {
				t.Fatalf("%s submission status change: status=%d body=%v", role.role, status, body)
			}
			status, body, _ = env.request(http.MethodPost, "/api/v1/admin/submissions/"+id+"/archive", role.token, nil)
			if status != http.StatusOK {
				t.Fatalf("%s submission archive: status=%d body=%v", role.role, status, body)
			}
			continue
		}
		beforeStatus, beforeArchived := roleMatrixSubmissionState(t, env, id)
		status, body, _ := env.request(http.MethodPatch, "/api/v1/admin/submissions/"+id+"/status", role.token, map[string]any{"status": "processed"})
		assertRoleRevisionStatus(t, "submission status change", role.role, status, http.StatusForbidden)
		if body == nil {
			t.Fatalf("submission status denial returned no response body for %s", role.role)
		}
		status, body, _ = env.request(http.MethodPost, "/api/v1/admin/submissions/"+id+"/archive", role.token, nil)
		assertRoleRevisionStatus(t, "submission archive", role.role, status, http.StatusForbidden)
		if body == nil {
			t.Fatalf("submission archive denial returned no response body for %s", role.role)
		}
		afterStatus, afterArchived := roleMatrixSubmissionState(t, env, id)
		if beforeStatus != afterStatus || beforeArchived != afterArchived {
			t.Fatalf("forbidden submission action had side effects for %s: before=%q/%v after=%q/%v", role.role, beforeStatus, beforeArchived, afterStatus, afterArchived)
		}
	}

	for _, role := range roles {
		id := ids[role.role]
		if role.admin {
			continue
		}
		status, _, _ := env.request(http.MethodDelete, "/api/v1/admin/submissions/"+id, role.token, nil)
		assertRoleRevisionStatus(t, "permanent submission delete", role.role, status, http.StatusForbidden)
		if !roleMatrixSubmissionExists(t, env, id) {
			t.Fatalf("forbidden submission delete had side effects for %s", role.role)
		}
	}
	adminID := ids["admin"]
	status, _, _ = env.request(http.MethodDelete, "/api/v1/admin/submissions/"+adminID, env.adminToken, nil)
	if status != http.StatusNoContent {
		t.Fatalf("admin submission delete: status=%d", status)
	}
	if roleMatrixSubmissionExists(t, env, adminID) {
		t.Fatal("admin submission delete left the row active")
	}
}

func roleMatrixStaleStructuralForkCheck(t *testing.T, env *testEnv, roles []roleRevisionCase, snapshotID, contentURL, entityID string) {
	t.Helper()
	manager := roles[1] // moderator
	detailURL := "/api/v2/admin/site/snapshots/" + snapshotID
	status, opened, _ := env.request(http.MethodGet, detailURL, manager.token, nil)
	if status != http.StatusOK {
		t.Fatalf("read snapshot structure before editing model: status=%d body=%v", status, opened)
	}
	openedRevision := opened["snapshot"].(map[string]any)["contentRevision"]

	workspace := roleMatrixWorkspace(t, env, contentURL, roles[3].token)
	records := cloneModelContentJSONArray(t, workspace["records"].([]any))
	records = append(records, map[string]any{
		"entityId": entityID, "sortOrder": len(records), "values": map[string]any{"title": "Concurrent content revision"},
	})
	status, saved, _ := env.request(http.MethodPut, contentURL, roles[3].token, map[string]any{
		"revision": workspace["revision"], "records": records, "selections": workspace["selections"],
	})
	if status != http.StatusOK {
		t.Fatalf("save concurrent content revision: status=%d body=%v", status, saved)
	}
	status, published, _ := env.request(http.MethodPost, contentURL+"/publish", roles[3].token, map[string]any{"revision": saved["revision"]})
	if status != http.StatusOK {
		t.Fatalf("publish concurrent content revision: status=%d body=%v", status, published)
	}

	currentStatus, current, _ := env.request(http.MethodGet, detailURL, manager.token, nil)
	if currentStatus != http.StatusOK {
		t.Fatalf("reload structural parent after content publication: status=%d body=%v", currentStatus, current)
	}
	updatedContentRevision := current["snapshot"].(map[string]any)["contentRevision"]
	if roleMatrixInt(t, updatedContentRevision) <= roleMatrixInt(t, openedRevision) {
		t.Fatalf("concurrent publication did not advance the parent snapshot revision: opened=%v current=%v", openedRevision, updatedContentRevision)
	}
	config := roleMatrixCloneObject(t, current["payload"].(map[string]any)["config"])
	roleMatrixAddModelField(t, config)
	beforeCount := roleMatrixSnapshotCount(t, env)
	status, conflict, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", manager.token, map[string]any{
		"name": "Stale model structure", "parentSnapshotId": snapshotID,
		"expectedParentContentRevision": openedRevision, "config": config,
	})
	assertRoleRevisionStatus(t, "stale model structure fork", manager.role, status, http.StatusConflict)
	if got := roleMatrixSnapshotCount(t, env); got != beforeCount {
		t.Fatalf("stale structural fork left a partial snapshot: before=%d after=%d response=%v", beforeCount, got, conflict)
	}

	status, child, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", manager.token, map[string]any{
		"name": "Model structure child", "parentSnapshotId": snapshotID,
		"expectedParentContentRevision": updatedContentRevision, "config": config,
	})
	if status != http.StatusCreated {
		t.Fatalf("retry model structure fork against the current parent revision: status=%d body=%v", status, child)
	}
	childStatus, childDetail, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshots/"+child["id"].(string), manager.token, nil)
	if childStatus != http.StatusOK {
		t.Fatalf("read model structure child: status=%d body=%v", childStatus, childDetail)
	}
	parentStatus, parentDetail, _ := env.request(http.MethodGet, detailURL, manager.token, nil)
	if parentStatus != http.StatusOK {
		t.Fatalf("read model structure parent: status=%d body=%v", parentStatus, parentDetail)
	}
	childFields := roleMatrixEntityFields(t, childDetail, entityID)
	parentFields := roleMatrixEntityFields(t, parentDetail, entityID)
	if len(childFields) != len(parentFields)+1 {
		t.Fatalf("model change was not isolated in the child snapshot: parent fields=%d child fields=%d", len(parentFields), len(childFields))
	}
}

func roleMatrixAddModelField(t *testing.T, config map[string]any) {
	t.Helper()
	model, ok := config["dataModel"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot config is missing its data model: %v", config)
	}
	entities, ok := model["entities"].([]any)
	if !ok || len(entities) == 0 {
		t.Fatalf("snapshot data model is missing entities: %v", model)
	}
	entity, ok := entities[0].(map[string]any)
	if !ok {
		t.Fatalf("snapshot data model entity has unexpected shape: %T", entities[0])
	}
	fields, ok := entity["fields"].([]any)
	if !ok {
		t.Fatalf("snapshot entity fields have unexpected shape: %T", entity["fields"])
	}
	entity["fields"] = append(fields, map[string]any{
		"id": uuid.NewString(), "key": "summary", "name": "Summary", "type": "longText",
	})
}

func roleMatrixEntityFields(t *testing.T, detail map[string]any, entityID string) []any {
	t.Helper()
	config := detail["payload"].(map[string]any)["config"].(map[string]any)
	model := config["dataModel"].(map[string]any)
	for _, rawEntity := range model["entities"].([]any) {
		entity := rawEntity.(map[string]any)
		if entity["id"] == entityID {
			return entity["fields"].([]any)
		}
	}
	t.Fatalf("snapshot model omitted entity %s: %v", entityID, model)
	return nil
}

func roleMatrixWorkspace(t *testing.T, env *testEnv, path, token string) map[string]any {
	t.Helper()
	status, workspace, _ := env.request(http.MethodGet, path, token, nil)
	if status != http.StatusOK {
		t.Fatalf("read model content workspace: status=%d body=%v", status, workspace)
	}
	return workspace
}

func roleMatrixInt(t *testing.T, raw any) int64 {
	t.Helper()
	value, ok := raw.(float64)
	if !ok {
		t.Fatalf("expected JSON integer, got %T (%v)", raw, raw)
	}
	return int64(value)
}

func roleMatrixCloneObject(t *testing.T, raw any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func roleMatrixSnapshotCount(t *testing.T, env *testEnv) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM site_snapshots`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func roleMatrixBuildCount(t *testing.T, env *testEnv) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM feature_builds`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func roleMatrixMediaCount(t *testing.T, env *testEnv) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM media_objects`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func roleMatrixSubmissionState(t *testing.T, env *testEnv, id string) (string, bool) {
	t.Helper()
	var status string
	var archived bool
	if err := env.pool.QueryRow(context.Background(), `SELECT status, archived_at IS NOT NULL FROM form_submissions WHERE id=$1`, id).Scan(&status, &archived); err != nil {
		t.Fatal(err)
	}
	return status, archived
}

func roleMatrixSubmissionExists(t *testing.T, env *testEnv, id string) bool {
	t.Helper()
	var exists bool
	if err := env.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM form_submissions WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func assertRoleRevisionStatus(t *testing.T, action, role string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s for %s: status=%d, want=%d", action, role, got, want)
	}
}

func assertRoleRevisionRequest(t *testing.T, env *testEnv, action, role, method, path, token string, body any, want int) {
	t.Helper()
	status, response, _ := env.request(method, path, token, body)
	if status != want {
		t.Fatalf("%s for %s: status=%d, want=%d, body=%v", action, role, status, want, response)
	}
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
