package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

func TestSnapshotProvenanceIsStoredShownAndNotExposedByRuntime(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	var actor uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM access_tokens WHERE name='bootstrap admin' AND revoked_at IS NULL`).Scan(&actor); err != nil {
		t.Fatalf("find test administrator: %v", err)
	}

	rootConfig := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Pages:         []sitebuilder.Page{{ID: "home", Name: "Главная", Kind: "page", Placements: []sitebuilder.Placement{}}},
		RouteTree:     sitebuilder.RouteTreeNode{ID: "root", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home", Children: []sitebuilder.RouteTreeNode{}}}},
	}
	root, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Пустой корень", Config: &rootConfig})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	if root.CreationSummary == nil || root.CreationSummary.Origin != "root" {
		t.Fatalf("root response omitted provenance: %#v", root.CreationSummary)
	}
	if root.CreatedByName == nil || *root.CreatedByName != "bootstrap admin" {
		t.Fatalf("root response omitted actor name: %#v", root.CreatedByName)
	}

	childConfig := rootConfig
	childConfig.Pages = append([]sitebuilder.Page(nil), rootConfig.Pages...)
	childConfig.Pages[0].Name = "Главная новая"
	child, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Потомок", ParentID: &root.ID, Config: &childConfig})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.CreationSummary == nil || child.CreationSummary.Origin != "child" || !containsString(child.CreationSummary.Changes, "pages_routes") {
		t.Fatalf("child response omitted structural summary: %#v", child.CreationSummary)
	}

	detail, err := env.snapshots.Snapshot(ctx, child.ID)
	if err != nil {
		t.Fatalf("read child detail: %v", err)
	}
	if detail.Snapshot.CreatedByName == nil || *detail.Snapshot.CreatedByName != "bootstrap admin" {
		t.Fatalf("detail omitted current actor name: %#v", detail.Snapshot.CreatedByName)
	}
	if detail.Snapshot.ParentSequence == nil || *detail.Snapshot.ParentSequence != root.Sequence || detail.Snapshot.ParentName == nil || *detail.Snapshot.ParentName != root.Name {
		t.Fatalf("detail omitted parent provenance: parent sequence=%v name=%v", detail.Snapshot.ParentSequence, detail.Snapshot.ParentName)
	}
	if detail.Snapshot.CreationSummary == nil || detail.Payload.CreationSummary != nil {
		t.Fatalf("detail must return provenance on snapshot metadata only: record=%#v payload=%#v", detail.Snapshot.CreationSummary, detail.Payload.CreationSummary)
	}

	list, err := env.snapshots.List(ctx, 20, nil)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(list) != 2 || list[0].CreationSummary == nil || list[0].CreatedByName == nil {
		t.Fatalf("snapshot list omitted provenance fields: %#v", list)
	}

	branchID, err := uuid.Parse(string(env.devBranch))
	if err != nil {
		t.Fatal(err)
	}
	branch, err := env.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.snapshots.AssignBranch(ctx, actor, branchID, child.ID, branch.Revision); err != nil {
		t.Fatalf("assign child for public runtime check: %v", err)
	}
	composition, err := env.snapshots.Runtime(ctx, env.devBranch, "/", nil)
	if err != nil {
		t.Fatalf("read runtime composition: %v", err)
	}
	runtimeJSON, err := json.Marshal(composition)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(runtimeJSON), "createdByName") || strings.Contains(string(runtimeJSON), "creationSummary") {
		t.Fatalf("public runtime composition leaked snapshot provenance: %s", runtimeJSON)
	}

	if _, err := env.pool.Exec(ctx, `UPDATE access_tokens SET revoked_at=now() WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	detail, err = env.snapshots.Snapshot(ctx, child.ID)
	if err != nil {
		t.Fatalf("read detail after actor revocation: %v", err)
	}
	if detail.Snapshot.CreatedByName != nil {
		t.Fatalf("revoked actor should be shown as unavailable: %#v", detail.Snapshot.CreatedByName)
	}
}

func TestSnapshotProvenanceEditorBrowser(t *testing.T) {
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		t.Skip("set PRERENDER_TEST_NODE/MODULE/BROWSER for snapshot provenance browser acceptance")
	}
	env := newTestEnv(t)
	ctx := context.Background()
	var actor uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM access_tokens WHERE name='bootstrap admin' AND revoked_at IS NULL`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	pageID := "home"
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Pages:         []sitebuilder.Page{{ID: pageID, Name: "Главная", Kind: "page", Placements: []sitebuilder.Placement{}}},
		RouteTree:     sitebuilder.RouteTreeNode{ID: "root", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: pageID, Children: []sitebuilder.RouteTreeNode{}}}},
	}
	root, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "UI root", Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	config.Pages[0].Name = "Главная изменённая"
	child, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "UI child", ParentID: &root.ID, Config: &config})
	if err != nil {
		t.Fatal(err)
	}

	const script = `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const pageErrors = [];
  page.on("pageerror", error => pageErrors.push(error.message));
  await page.addInitScript(token => localStorage.setItem("we_admin_token", token), process.env.TEST_ADMIN_TOKEN);
  await page.goto(process.env.TEST_TREE_URL, { waitUntil: "domcontentloaded" });
  const item = page.locator("li").filter({ hasText: "UI child" });
  await item.waitFor({ timeout: 15000 });
  const listText = await item.innerText();
  for (const expected of ["Инициатор: bootstrap admin", process.env.TEST_PARENT_LABEL, "Обновлены страницы и маршруты"]) {
    if (!listText.includes(expected)) throw new Error("snapshot list provenance is missing: " + expected + "; actual=" + listText);
  }
  await page.goto(process.env.TEST_PAGE_URL, { waitUntil: "domcontentloaded" });
  const region = page.getByRole("region", { name: "Сведения о снапшоте" });
  await region.waitFor({ timeout: 15000 });
  const text = await region.innerText();
  for (const expected of ["Инициатор: bootstrap admin", process.env.TEST_PARENT_LABEL, "Обновлены страницы и маршруты"]) {
    if (!text.includes(expected)) throw new Error("snapshot provenance is missing: " + expected + "; actual=" + text);
  }
  if (pageErrors.length) throw new Error("browser page errors: " + pageErrors.join(" | "));
  console.log(text);
} finally { await browser.close(); }`
	pageURL := env.server.URL + "/admin/site/pages?snapshot=" + child.ID.String()
	parentLabel := fmt.Sprintf("Родитель: #%d · %s", root.Sequence, root.Name)
	cmd := exec.Command(node, "--input-type=module", "-e", script)
	cmd.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browser,
		"TEST_ADMIN_TOKEN="+env.adminToken,
		"TEST_TREE_URL="+env.server.URL+"/admin/snapshots/tree",
		"TEST_PAGE_URL="+pageURL,
		"TEST_PARENT_LABEL="+parentLabel,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser did not show snapshot provenance: %v\n%s", err, output)
	}
	t.Logf("browser snapshot provenance:\n%s", output)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
