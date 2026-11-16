package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"
)

func TestPublicFileRouteServesOnlyReadyPublicFiles(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	var actor uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM access_tokens WHERE name='bootstrap admin' AND revoked_at IS NULL`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	file, err := env.media.Upload(ctx, principal.Principal{TokenID: actor, Role: principal.RoleAdmin}, "price-list.pdf", []byte("%PDF-1.4\n% public price list\n"))
	if err != nil {
		t.Fatalf("upload public file: %v", err)
	}
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Pages: []sitebuilder.Page{
			{ID: "home", Name: "Главная", Kind: "page", Placements: []sitebuilder.Placement{}},
		},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{
			{ID: "home-route", Type: "path", Path: "/", PageID: "home"},
			{ID: "price-route", Type: "path", Path: "/files/price-list.pdf", FileID: file.ID.String(), FileDisposition: "attachment"},
		}},
	}
	snapshot, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "File route", Config: &config})
	if err != nil {
		t.Fatalf("create file-route snapshot: %v", err)
	}
	branchID, err := uuid.Parse(string(env.devBranch))
	if err != nil {
		t.Fatal(err)
	}
	branch, err := env.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.snapshots.AssignBranch(ctx, actor, branchID, snapshot.ID, branch.Revision); err != nil {
		t.Fatalf("assign file-route snapshot: %v", err)
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Get(env.devServer.URL + "/files/price-list.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("public file route status = %d, want 302", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "https://cdn.example.test/"+file.ObjectKey {
		t.Fatalf("public file route redirect = %q", location)
	}

	references, err := env.snapshots.References(ctx, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, reference := range references {
		if reference.Kind == "snapshot" && reference.ID == snapshot.ID.String() {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("file route reference missing from snapshot references: %#v", references)
	}

	if _, err := env.pool.Exec(ctx, `UPDATE media_objects SET visibility='private' WHERE id=$1`, file.ID); err != nil {
		t.Fatal(err)
	}
	privateResponse, err := client.Get(env.devServer.URL + "/files/price-list.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_ = privateResponse.Body.Close()
	if privateResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("private file route status = %d, want 404", privateResponse.StatusCode)
	}
}
