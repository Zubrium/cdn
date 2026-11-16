package integration

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	"websiteengine/pkg/environment"
)

func TestSnapshotMetadataRendersAndChangesOnlyAfterBranchAssignment(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, env)

	indexable := true
	branches, err := env.snapshots.RegisterBranches(ctx, []config.BranchConfig{{
		Name: "metadata QA", HTTPAddr: ":38181", CORSOrigins: []string{}, Indexable: &indexable,
	}})
	if err != nil || len(branches) != 1 {
		t.Fatalf("register static branch: branches=%+v err=%v", branches, err)
	}
	branch := branches[0]
	branchEnv := environment.Environment(branch.ID.String())

	pageTitle := "Главная страница"
	metadata := sitebuilder.SiteMetadata{
		FormatVersion:   1,
		SiteName:        "VitaSil",
		Description:     "Качество продукции",
		CanonicalDomain: "https://vitasil.example",
		Pages: map[string]sitebuilder.SiteMetadataOverride{
			"home": {SiteName: &pageTitle},
		},
	}
	configRaw := []byte(`{"formatVersion":3,"pages":[{"id":"home","name":"Главная","kind":"page","placements":[]}],"routeTree":{"id":"site","type":"site","children":[{"id":"home-route","type":"path","path":"/","pageId":"home","children":[]}]}}`)
	config, err := sitebuilder.ParseSiteConfig(configRaw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{
		Name: "SEO snapshot", Config: &config, Metadata: &metadata,
	})
	if err != nil {
		t.Fatalf("create SEO snapshot: %v", err)
	}
	branch, err = env.snapshots.AssignBranch(ctx, actor, branch.ID, snapshot.ID, branch.Revision)
	if err != nil {
		t.Fatalf("assign SEO snapshot to static branch: %v", err)
	}

	composition, err := env.snapshots.Runtime(ctx, branchEnv, "/", url.Values{})
	if err != nil || composition.Metadata.Title != pageTitle || composition.Metadata.Description != metadata.Description {
		t.Fatalf("runtime metadata does not match the assigned snapshot: %#v err=%v", composition.Metadata, err)
	}
	server := seoPublicServer(t, env, branch, nil)
	status, html, _ := seoGet(t, server, "/")
	if status != 200 || !strings.Contains(html, "<title>Главная страница</title>") || !strings.Contains(html, `content="Качество продукции"`) {
		t.Fatalf("public HTML metadata is missing: status=%d html=%s", status, html)
	}
	if !strings.Contains(html, `rel="canonical" href="https://vitasil.example/"`) {
		t.Fatalf("public HTML must use canonical metadata from the snapshot: %s", html)
	}

	updated := metadata
	updated.Description = "Новое описание в дочернем снапшоте"
	child, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{
		Name: "SEO metadata update", ParentID: &snapshot.ID, Metadata: &updated,
	})
	if err != nil {
		t.Fatalf("create metadata child snapshot: %v", err)
	}
	composition, err = env.snapshots.Runtime(ctx, branchEnv, "/", url.Values{})
	if err != nil || composition.Snapshot.ID != snapshot.ID || composition.Metadata.Description != metadata.Description {
		t.Fatalf("unassigned child must not affect the branch: %#v err=%v", composition, err)
	}
	branch, err = env.snapshots.AssignBranch(ctx, actor, branch.ID, child.ID, branch.Revision)
	if err != nil {
		t.Fatalf("assign updated SEO snapshot: %v", err)
	}
	composition, err = env.snapshots.Runtime(ctx, branchEnv, "/", url.Values{})
	if err != nil || composition.Snapshot.ID != child.ID || composition.Metadata.Description != updated.Description {
		t.Fatalf("metadata must update after explicit branch assignment: %#v err=%v", composition, err)
	}
	status, html, _ = seoGet(t, server, "/")
	if status != 200 || !strings.Contains(html, `content="Новое описание в дочернем снапшоте"`) {
		t.Fatalf("public HTML did not refresh after branch assignment: status=%d html=%s", status, html)
	}

	parentDetail, err := env.snapshots.Snapshot(ctx, snapshot.ID)
	if err != nil || parentDetail.Payload.Metadata.Description != metadata.Description {
		t.Fatalf("parent metadata must remain unchanged: %#v err=%v", parentDetail.Payload.Metadata, err)
	}
}
