package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"sync"
	"testing"
	"websiteengine/features/auth"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
)

func TestConfiguredBranchesPublishForkAndProtectTree(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	indexable := false
	defs := []config.BranchConfig{
		{Name: "web preview", HTTPAddr: ":18081", CORSOrigins: []string{}, Indexable: &indexable},
		{Name: "app live", HTTPAddr: ":18082", CORSOrigins: []string{}, Indexable: &indexable},
	}
	branches, err := e.snapshots.RegisterBranches(ctx, defs)
	if err != nil || len(branches) != 2 {
		t.Fatalf("register branches: %+v %v", branches, err)
	}
	branchNamed := func(items []sitebuilder.BranchRecord, name string) sitebuilder.BranchRecord {
		t.Helper()
		for _, item := range items {
			if item.Name == name {
				return item
			}
		}
		t.Fatalf("configured branch %q is missing: %+v", name, items)
		return sitebuilder.BranchRecord{}
	}
	preview := branchNamed(branches, defs[0].Name)
	live := branchNamed(branches, defs[1].Name)
	var cfg sitebuilder.SiteConfig
	if err := json.Unmarshal([]byte(`{"formatVersion":3,"pages":[{"id":"home","name":"Home","kind":"page","placements":[]}],"routeTree":{"id":"root","type":"site","children":[{"id":"home-route","type":"path","path":"/","pageId":"home","children":[]}]}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	root, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Root", Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	preview, err = e.snapshots.AssignBranch(ctx, actor, preview.ID, root.ID, preview.Revision)
	if err != nil {
		t.Fatal(err)
	}
	live, err = e.snapshots.AssignBranch(ctx, actor, live.ID, root.ID, live.Revision)
	if err != nil {
		t.Fatal(err)
	}
	child, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Child", ParentID: &root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID == nil || *child.ParentID != root.ID {
		t.Fatalf("child snapshot has wrong parent: %+v", child)
	}
	preview, err = e.snapshots.AssignBranch(ctx, actor, preview.ID, child.ID, preview.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if live.SnapshotID == nil || *live.SnapshotID != root.ID {
		t.Fatalf("assigning one branch changed another branch: %+v", live)
	}
	if err := e.snapshots.DeleteSnapshot(ctx, root.ID); !errors.Is(err, sitebuilder.ErrConflict) {
		t.Fatalf("assigned parent of a branch snapshot was not protected: %v", err)
	}
	if err := e.snapshots.DeleteSnapshot(ctx, child.ID); !errors.Is(err, sitebuilder.ErrConflict) {
		t.Fatalf("branch-assigned snapshot was not protected: %v", err)
	}

	restarted, err := e.snapshots.RegisterBranches(ctx, defs)
	restartedPreview := branchNamed(restarted, defs[0].Name)
	if err != nil || restartedPreview.ID != preview.ID || restartedPreview.SnapshotID == nil || *restartedPreview.SnapshotID != child.ID {
		t.Fatalf("restarting registration lost branch identity or assignment: %+v %v", restarted, err)
	}
	if err := e.snapshots.RenameBranch(ctx, defs[0].Name, "renamed web"); err != nil {
		t.Fatal(err)
	}
	defs[0].Name = "renamed web"
	again, err := e.snapshots.RegisterBranches(ctx, defs)
	if err != nil {
		t.Fatal(err)
	}
	renamedPreview := branchNamed(again, defs[0].Name)
	if renamedPreview.ID != preview.ID || renamedPreview.SnapshotID == nil || *renamedPreview.SnapshotID != child.ID {
		t.Fatalf("rename changed branch identity or assignment: %+v %v", again, err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.snapshots.AssignBranch(ctx, actor, preview.ID, root.ID, renamedPreview.Revision)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, sitebuilder.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("racing branch assignments: %d succeeded, %d conflicted", ok, conflicts)
	}

	removed, err := e.snapshots.Branch(ctx, live.ID)
	if err != nil || removed.SnapshotID == nil {
		t.Fatalf("read assignment before branch removal: %+v %v", removed, err)
	}
	active, err := e.snapshots.RegisterBranches(ctx, []config.BranchConfig{defs[0]})
	if err != nil || len(active) != 1 {
		t.Fatalf("remove a configured branch: %+v %v", active, err)
	}
	if active[0].ID != preview.ID || active[0].Name != defs[0].Name {
		t.Fatalf("active branch set after config change is wrong: %+v", active)
	}
	if _, err := e.snapshots.Branch(ctx, removed.ID); !errors.Is(err, sitebuilder.ErrNotFound) {
		t.Fatalf("removed branch remained active: %v", err)
	}
	restored, err := e.snapshots.RegisterBranches(ctx, defs)
	if err != nil {
		t.Fatal(err)
	}
	restoredBranch := branchNamed(restored, removed.Name)
	if restoredBranch.ID != removed.ID || restoredBranch.SnapshotID == nil || *restoredBranch.SnapshotID != *removed.SnapshotID {
		t.Fatalf("re-adding branch did not restore identity and assignment: before=%+v after=%+v", removed, restoredBranch)
	}
}

func TestBranchManagementHTTPRequiresManagerAndRevision(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	indexable := false
	branches, err := e.snapshots.RegisterBranches(ctx, []config.BranchConfig{{Name: "api preview", HTTPAddr: ":18083", CORSOrigins: []string{}, Indexable: &indexable}})
	if err != nil || len(branches) != 1 {
		t.Fatalf("register branch: %+v %v", branches, err)
	}
	var cfg sitebuilder.SiteConfig
	if err := json.Unmarshal([]byte(`{"formatVersion":3,"pages":[{"id":"home","name":"Главная","kind":"page","placements":[]}],"routeTree":{"id":"site","type":"site","children":[{"id":"root","type":"path","path":"/","pageId":"home","children":[]}]}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "HTTP branch target", Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewService(auth.NewRepository(e.pool)).Create(ctx, auth.Principal{TokenID: actor, Role: auth.RoleAdmin}, "content-only", string(auth.RoleUser), nil)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, token string, payload any) (int, []byte) {
		t.Helper()
		var body io.Reader
		if payload != nil {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(encoded)
		}
		req, err := http.NewRequest(method, e.server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := e.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, data
	}
	path := "/api/v2/admin/branches"
	if status, _ := do(http.MethodGet, path, "", nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated branch list status=%d", status)
	}
	if status, _ := do(http.MethodPut, path+"/"+branches[0].ID.String()+"/snapshot", user.Secret, map[string]any{"snapshotId": snapshot.ID, "revision": branches[0].Revision, "contentRevision": snapshot.ContentRevision}); status != http.StatusForbidden {
		t.Fatalf("user branch assignment status=%d", status)
	}
	if status, _ := do(http.MethodPut, path+"/"+branches[0].ID.String()+"/snapshot", e.adminToken, map[string]any{"snapshotId": snapshot.ID, "revision": branches[0].Revision - 1, "contentRevision": snapshot.ContentRevision}); status != http.StatusConflict {
		t.Fatalf("stale branch assignment status=%d", status)
	}
	if status, _ := do(http.MethodPut, path+"/"+branches[0].ID.String()+"/snapshot", e.adminToken, map[string]any{"snapshotId": snapshot.ID, "revision": branches[0].Revision}); status != http.StatusBadRequest {
		t.Fatalf("assignment without reviewed content revision status=%d", status)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE site_snapshots SET content_revision=content_revision+1 WHERE id=$1`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if status, _ := do(http.MethodPut, path+"/"+branches[0].ID.String()+"/snapshot", e.adminToken, map[string]any{"snapshotId": snapshot.ID, "revision": branches[0].Revision, "contentRevision": snapshot.ContentRevision}); status != http.StatusConflict {
		t.Fatalf("stale reviewed content assignment status=%d", status)
	}
	status, raw := do(http.MethodGet, path, e.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("manager branch list status=%d body=%s", status, raw)
	}
	var listed struct {
		Items []sitebuilder.BranchRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil || len(listed.Items) != 1 || listed.Items[0].SnapshotID != nil || listed.Items[0].Revision != branches[0].Revision {
		t.Fatalf("stale request changed branch state: %+v %v", listed, err)
	}
	status, raw = do(http.MethodPut, path+"/"+branches[0].ID.String()+"/snapshot", e.adminToken, map[string]any{"snapshotId": snapshot.ID, "revision": branches[0].Revision, "contentRevision": snapshot.ContentRevision + 1})
	var assigned sitebuilder.BranchRecord
	if status != http.StatusOK || json.Unmarshal(raw, &assigned) != nil || assigned.SnapshotID == nil || *assigned.SnapshotID != snapshot.ID || assigned.Revision != branches[0].Revision+1 {
		t.Fatalf("manager assignment status=%d record=%s", status, raw)
	}
	if status, _ := do(http.MethodPut, path+"/"+branches[0].ID.String()+"/snapshot", e.adminToken, map[string]any{"snapshotId": snapshot.ID, "revision": branches[0].Revision, "contentRevision": snapshot.ContentRevision + 1}); status != http.StatusConflict {
		t.Fatalf("repeated stale assignment status=%d", status)
	}
}

func TestSnapshotDeletionReparentsCopiesWithoutChangingPayload(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	entityID, fieldID := uuid.NewString(), uuid.NewString()
	model := sitebuilder.DataModel{FormatVersion: 1, Entities: []sitebuilder.DataModelEntity{{
		ID: entityID, Key: "item", Name: "Item", Fields: []sitebuilder.DataModelField{{ID: fieldID, Key: "title", Name: "Title", Type: "text"}},
	}}}
	cfg := sitebuilder.SiteConfig{
		FormatVersion: 3,
		DataModel:     &model,
		Pages:         []sitebuilder.Page{{ID: "home", Name: "Home", Kind: "page", Placements: []sitebuilder.Placement{}}},
		RouteTree:     sitebuilder.RouteTreeNode{ID: "root", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home", Children: []sitebuilder.RouteTreeNode{}}}},
	}
	root, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "A", Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	rootWorkspace, err := e.snapshots.SaveModelContentWorkspace(ctx, actor, root.ID, sitebuilder.ModelContentDraftInput{
		Revision: 0, Records: []sitebuilder.ModelContentRecord{{EntityID: entityID, SortOrder: 0, Values: map[string]any{"title": "root published"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rootWorkspace, err = e.snapshots.PublishModelContentWorkspace(ctx, actor, root.ID, rootWorkspace.Revision)
	if err != nil {
		t.Fatal(err)
	}
	rootDraft := cloneModelContentRecords(rootWorkspace.Records)
	rootDraft[0].Values["title"] = "root draft"
	rootWorkspace, err = e.snapshots.SaveModelContentWorkspace(ctx, actor, root.ID, sitebuilder.ModelContentDraftInput{Revision: rootWorkspace.Revision, Records: rootDraft})
	if err != nil {
		t.Fatal(err)
	}
	middle, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "B", ParentID: &root.ID})
	if err != nil {
		t.Fatal(err)
	}
	child, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "C", ParentID: &middle.ID})
	if err != nil {
		t.Fatal(err)
	}
	childWorkspace, err := e.snapshots.ModelContentWorkspace(ctx, child.ID)
	if err != nil || childWorkspace.Revision != rootWorkspace.Revision || childWorkspace.PublishedRevision != rootWorkspace.PublishedRevision || len(childWorkspace.Records) != 1 || childWorkspace.Records[0].Values["title"] != "root draft" {
		t.Fatalf("child must inherit published and draft model content: workspace=%+v err=%v", childWorkspace, err)
	}
	childDraft := cloneModelContentRecords(childWorkspace.Records)
	childDraft[0].Values["title"] = "child published"
	childWorkspace, err = e.snapshots.SaveModelContentWorkspace(ctx, actor, child.ID, sitebuilder.ModelContentDraftInput{Revision: childWorkspace.Revision, Records: childDraft})
	if err != nil {
		t.Fatal(err)
	}
	childWorkspace, err = e.snapshots.PublishModelContentWorkspace(ctx, actor, child.ID, childWorkspace.Revision)
	if err != nil {
		t.Fatal(err)
	}
	childDraft = cloneModelContentRecords(childWorkspace.Records)
	childDraft[0].Values["title"] = "child newer draft"
	childWorkspace, err = e.snapshots.SaveModelContentWorkspace(ctx, actor, child.ID, sitebuilder.ModelContentDraftInput{Revision: childWorkspace.Revision, Records: childDraft})
	if err != nil {
		t.Fatal(err)
	}
	publishedBeforeDelete := snapshotPublishedTitle(t, e, child.ID)
	if publishedBeforeDelete != "child published" {
		t.Fatalf("unexpected pre-delete publication: got %q want %q", publishedBeforeDelete, "child published")
	}
	childRevisionBeforeDelete, childPublishedRevisionBeforeDelete := childWorkspace.Revision, childWorkspace.PublishedRevision
	if err := e.snapshots.DeleteSnapshot(ctx, middle.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := e.snapshots.Snapshot(ctx, child.ID)
	if err != nil || detail.Snapshot.ParentID == nil || *detail.Snapshot.ParentID != root.ID || detail.Snapshot.Digest != child.Digest {
		t.Fatalf("reparent failed: %v %v", detail, err)
	}
	assertReparentedModelContent(t, e, child.ID, childRevisionBeforeDelete, childPublishedRevisionBeforeDelete, "child published", "child newer draft")
	if err := e.snapshots.DeleteSnapshot(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	detail, err = e.snapshots.Snapshot(ctx, child.ID)
	if err != nil || detail.Snapshot.ParentID != nil || detail.Snapshot.Digest != child.Digest {
		t.Fatal("root deletion changed child structure")
	}
	assertReparentedModelContent(t, e, child.ID, childRevisionBeforeDelete, childPublishedRevisionBeforeDelete, "child published", "child newer draft")
}

func snapshotPublishedTitle(t *testing.T, env *testEnv, snapshotID uuid.UUID) string {
	t.Helper()
	var raw []byte
	if err := env.pool.QueryRow(context.Background(), `SELECT published_records FROM site_snapshot_model_workspaces WHERE snapshot_id=$1`, snapshotID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var records []sitebuilder.ModelContentRecord
	if err := json.Unmarshal(raw, &records); err != nil || len(records) != 1 {
		t.Fatalf("decode published model records: %v, %v", records, err)
	}
	title, _ := records[0].Values["title"].(string)
	return title
}

func assertReparentedModelContent(t *testing.T, env *testEnv, snapshotID uuid.UUID, expectedRevision, expectedPublishedRevision int64, expectedPublished, expectedDraft string) {
	t.Helper()
	workspace, err := env.snapshots.ModelContentWorkspace(context.Background(), snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Revision != expectedRevision || workspace.PublishedRevision != expectedPublishedRevision || len(workspace.Records) != 1 {
		t.Fatalf("reparent changed model-content revisions/records: %+v", workspace)
	}
	if got := snapshotPublishedTitle(t, env, snapshotID); got != expectedPublished {
		t.Fatalf("reparent changed published model content: got %q want %q", got, expectedPublished)
	}
	if got, _ := workspace.Records[0].Values["title"].(string); got != expectedDraft {
		t.Fatalf("reparent changed model draft: got %q want %q", got, expectedDraft)
	}
}

func cloneModelContentRecords(records []sitebuilder.ModelContentRecord) []sitebuilder.ModelContentRecord {
	cloned := make([]sitebuilder.ModelContentRecord, len(records))
	for index, record := range records {
		cloned[index] = record
		cloned[index].Values = make(map[string]any, len(record.Values))
		for key, value := range record.Values {
			cloned[index].Values[key] = value
		}
		cloned[index].Relations = make(map[string][]string, len(record.Relations))
		for relationID, targets := range record.Relations {
			cloned[index].Relations[relationID] = append([]string(nil), targets...)
		}
	}
	return cloned
}

func TestSnapshotAssignmentRacesDeletion(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	indexable := false
	branches, err := e.snapshots.RegisterBranches(ctx, []config.BranchConfig{{
		Name: "assignment race", HTTPAddr: ":18091", CORSOrigins: []string{}, Indexable: &indexable,
	}})
	if err != nil || len(branches) != 1 {
		t.Fatalf("register branch: %v %v", branches, err)
	}
	raw := json.RawMessage(`{"formatVersion":3,"pages":[{"id":"p","name":"Home","kind":"page","placements":[]}],"routeTree":{"id":"r","type":"site","children":[{"id":"h","type":"path","path":"/","pageId":"p"}]}}`)
	cfg, err := sitebuilder.ParseSiteConfig(raw)
	if err != nil {
		t.Fatal(err)
	}

	// The tree advisory lock linearizes each pair: either assignment commits
	// first and deletion sees a protected snapshot, or deletion commits first
	// and assignment finds no snapshot. No mixed terminal state is valid.
	for i := 0; i < 8; i++ {
		candidate, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "race candidate", Config: &cfg})
		if err != nil {
			t.Fatal(err)
		}
		branch, err := e.snapshots.Branch(ctx, branches[0].ID)
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var deleteErr, assignErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			deleteErr = e.snapshots.DeleteSnapshot(ctx, candidate.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, assignErr = e.snapshots.AssignBranch(ctx, actor, branch.ID, candidate.ID, branch.Revision)
		}()
		close(start)
		wg.Wait()

		current, err := e.snapshots.Branch(ctx, branch.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, snapshotErr := e.snapshots.Snapshot(ctx, candidate.ID)
		switch {
		case assignErr == nil:
			if !errors.Is(deleteErr, sitebuilder.ErrConflict) || snapshotErr != nil || current.SnapshotID == nil || *current.SnapshotID != candidate.ID {
				t.Fatalf("assignment first must protect candidate: assign=%v delete=%v snapshot=%v branch=%+v", assignErr, deleteErr, snapshotErr, current)
			}
		case deleteErr == nil && errors.Is(assignErr, sitebuilder.ErrNotFound):
			if !errors.Is(snapshotErr, sitebuilder.ErrNotFound) || current.Revision != branch.Revision {
				t.Fatalf("deletion first must leave the previous assignment: assign=%v delete=%v snapshot=%v before=%+v after=%+v", assignErr, deleteErr, snapshotErr, branch, current)
			}
			if branch.SnapshotID == nil && current.SnapshotID != nil || branch.SnapshotID != nil && (current.SnapshotID == nil || *current.SnapshotID != *branch.SnapshotID) {
				t.Fatalf("deletion first changed the previous assignment: before=%+v after=%+v", branch, current)
			}
		default:
			t.Fatalf("unexpected assignment/deletion outcome: assign=%v delete=%v", assignErr, deleteErr)
		}
	}
}
