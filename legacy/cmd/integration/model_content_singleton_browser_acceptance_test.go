package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

// TestModelContentSingletonBrowserAcceptance checks the singleton model and
// content workflow in Chromium against the isolated integration environment.
func TestModelContentSingletonBrowserAcceptance(t *testing.T) {
	node := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	module := strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browser := strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	for name, path := range map[string]string{"PRERENDER_TEST_NODE": node, "PRERENDER_TEST_MODULE": module, "PRERENDER_TEST_BROWSER": browser} {
		if path == "" {
			t.Skipf("set %s for singleton browser acceptance", name)
		}
		if _, err := os.Stat(path); err != nil {
			t.Skipf("%s=%q is unavailable: %v", name, path, err)
		}
	}
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	ctx := context.Background()
	entityID, fieldID := uuid.NewString(), uuid.NewString()
	manifest := map[string]any{
		"formatVersion": 2, "key": "singleton-browser-acceptance", "name": "Singleton browser acceptance",
		"version": "1.0.0", "engineApi": "2",
		"runtime":   map[string]any{"abi": "static-module-v1", "entry": "entry.js"},
		"slots":     []string{"main"},
		"arguments": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"dataInputs": []any{
			map[string]any{"key": "record", "kind": "record", "fields": []any{map[string]any{"path": "/title", "type": "text"}}},
			map[string]any{"key": "scalar", "kind": "scalar", "type": "text"},
		},
	}
	modelAcceptanceUploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")
	model := sitebuilder.DataModel{
		FormatVersion: 1,
		Entities: []sitebuilder.DataModelEntity{{
			ID: entityID, Key: "site_settings", Name: "Настройки",
			Cardinality: sitebuilder.DataModelEntityCardinalityMany,
			Fields:      []sitebuilder.DataModelField{{ID: fieldID, Key: "title", Name: "Название", Type: "text"}},
		}},
	}
	config := sitebuilder.SiteConfig{
		FormatVersion: 2, DataModel: &model,
		Pages: []sitebuilder.Page{{ID: "home", Name: "Home", Kind: "page", Placements: []sitebuilder.Placement{{
			ID: "singleton-placement", Build: sitebuilder.BuildRef{FeatureKey: "singleton-browser-acceptance", Version: "1.0.0"}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{
				{InputKey: "record", EntityID: entityID, Fields: []sitebuilder.DataInputFieldBinding{{InputPath: "/title", FieldID: fieldID}}},
				{InputKey: "scalar", EntityID: entityID, FieldID: fieldID},
			},
		}}}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "root", Type: "index", PageID: "home"},
	}
	rawConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(rawConfig, &configMap); err != nil {
		t.Fatal(err)
	}
	root := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Singleton browser parent")
	rootID := root["id"].(string)
	branchID := uuid.MustParse(string(env.devBranch))
	branchBefore, err := env.snapshots.Branch(ctx, branchID)
	if err != nil || branchBefore.SnapshotID == nil || branchBefore.SnapshotID.String() != rootID {
		t.Fatalf("disposable branch should begin on the parent snapshot: branch=%+v err=%v", branchBefore, err)
	}
	const browserScript = `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.SINGLETON_PLAYWRIGHT_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.SINGLETON_PLAYWRIGHT_BROWSER, headless: true, args: ["--no-sandbox"] });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
await context.addInitScript(token => localStorage.setItem("we_admin_token", token), process.env.SINGLETON_ADMIN_TOKEN);
const page = await context.newPage();
const pageErrors = [];
page.on("pageerror", error => pageErrors.push(error.message));
const base = process.env.SINGLETON_ADMIN_BASE;
const runtimeBase = process.env.SINGLETON_RUNTIME_BASE;
const rootID = process.env.SINGLETON_ROOT_ID;
const entityID = process.env.SINGLETON_ENTITY_ID;
const branchID = process.env.SINGLETON_BRANCH_ID;
const headers = { Authorization: "Bearer " + process.env.SINGLETON_ADMIN_TOKEN };
const readJSON = async path => {
  const response = await fetch(base + path, { headers });
  if (!response.ok) throw new Error("admin request failed: " + response.status + " " + await response.text());
  return response.json();
};
const readRuntime = async () => {
  const response = await fetch(runtimeBase + "/api/v2/runtime?path=%2F");
  if (!response.ok) throw new Error("public runtime failed: " + response.status + " " + await response.text());
  const data = await response.json();
  const placement = data.placements?.find(item => item.id === "singleton-placement");
  if (!placement?.content) throw new Error("runtime omitted singleton placement");
  return placement.content;
};
const contentFor = async snapshotID => readJSON("/api/v2/admin/content/snapshots/" + encodeURIComponent(snapshotID) + "/model");
const branchList = async () => readJSON("/api/v2/admin/branches");
try {
  await page.goto(base + "/admin/site/data-model?snapshot=" + encodeURIComponent(rootID), { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Модель данных", exact: true }).waitFor({ timeout: 20000 });
  const cardinality = page.getByRole("combobox", { name: "Количество записей сущности Настройки", exact: true });
  await cardinality.waitFor({ timeout: 20000 });
  const tabTo = async locator => {
    await locator.scrollIntoViewIfNeeded();
    for (let index = 0; index < 160; index++) {
      await page.keyboard.press("Tab");
      if (await locator.evaluate(element => document.activeElement === element)) return;
    }
    throw new Error("keyboard navigation did not reach cardinality control");
  };
  await tabTo(cardinality);
  if (!(await cardinality.evaluate(element => element.matches(":focus-visible")))) throw new Error("cardinality control has no visible keyboard focus");
  // Chromium delegates the native select popup to macOS, where key presses
  // sent to the closed control do not consistently commit a new option.
  // Keyboard focus visibility is asserted above; use the browser select API
  // for the value transition and continue through the real confirmation UI.
  await cardinality.selectOption("singleton");
  const cardinalityDialog = page.getByRole("alertdialog");
  await cardinalityDialog.waitFor({ timeout: 10000 });
  await cardinalityDialog.getByRole("button", { name: "Изменить количество", exact: true }).click();
  if (await cardinality.inputValue() !== "singleton") throw new Error("keyboard did not select singleton cardinality");
  const savePromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v2/admin/site/snapshots");
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const saveResponse = await savePromise;
  if (!saveResponse.ok()) throw new Error("structural save failed: " + saveResponse.status());
  const child = await saveResponse.json();
  if (!child.id || child.id === rootID) throw new Error("structural save did not create a child snapshot");
  await page.waitForFunction(id => new URL(location.href).searchParams.get("snapshot") === id, child.id);
  const detail = await readJSON("/api/v2/admin/site/snapshots/" + encodeURIComponent(child.id));
  const entity = detail.payload?.config?.dataModel?.entities?.find(item => item.id === entityID);
  if (detail.snapshot?.parentSnapshotId !== rootID || entity?.cardinality !== "singleton") throw new Error("child omitted its parent or singleton change");
  let branches = await branchList();
  let branch = branches.items?.find(item => item.id === branchID);
  if (!branch || branch.snapshotId !== rootID) throw new Error("site editor changed the branch assignment");
  const assigned = await fetch(base + "/api/v2/admin/branches/" + encodeURIComponent(branchID) + "/snapshot", {
    method: "PUT", headers: { ...headers, "Content-Type": "application/json" },
    body: JSON.stringify({ snapshotId: child.id, revision: branch.revision, contentRevision: detail.snapshot.contentRevision }),
  });
  if (!assigned.ok) throw new Error("explicit disposable branch setup failed: " + assigned.status());
  const initial = await contentFor(child.id);
  const initialRecord = initial.records?.find(record => record.entityId === entityID);
  if (!initialRecord || initial.records.filter(record => record.entityId === entityID).length !== 1 ||
      Object.keys(initialRecord.values ?? {}).length || initial.publishedRevision !== 0) throw new Error("child did not seed one empty unpublished singleton record");
  const recordID = initialRecord.id;
  const initialRuntime = await readRuntime();
  if (Object.keys(initialRuntime.record ?? {}).length || (initialRuntime.scalar != null && initialRuntime.scalar !== "")) throw new Error("unpublished singleton leaked into runtime");

  await page.goto(base + "/admin/content?snapshot=" + encodeURIComponent(child.id), { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor({ timeout: 20000 });
  await page.getByText("единственная запись", { exact: false }).waitFor({ timeout: 20000 });
  const title = page.getByRole("textbox", { name: "Название", exact: true });
  await title.waitFor({ timeout: 20000 });
  if (await title.count() !== 1) throw new Error("singleton editor should display one form");
  const clearButton = page.getByRole("button", { name: "Очистить поля и связи", exact: true });
  await clearButton.waitFor({ timeout: 20000 });
  const controls = await page.evaluate(() => {
    const root = document.querySelector(".page-grid") ?? document.body;
    const visible = element => {
      const style = getComputedStyle(element), rect = element.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0;
    };
    const buttons = [...root.querySelectorAll("button")].filter(visible).map(button => (button.getAttribute("aria-label") || button.innerText || button.title || "").trim());
    const prohibited = buttons.filter(name => /^(добавить|дублировать|переместить|удалить запись)(\b|$)/i.test(name));
    const recordLists = [...root.querySelectorAll('[role="list"]')].filter(list => /Записи сущности/i.test(list.getAttribute("aria-label") || "")).length;
    return { prohibited, recordLists };
  });
  if (controls.prohibited.length || controls.recordLists) throw new Error("singleton editor exposed list/add/reorder/delete controls: " + JSON.stringify(controls));
  const contentPath = "/api/v2/admin/content/snapshots/" + encodeURIComponent(child.id) + "/model";

  const saveDraft = page.waitForResponse(response => response.request().method() === "PUT" && new URL(response.url()).pathname === contentPath);
  await title.fill("Browser singleton value");
  const saveDraftResponse = await saveDraft;
  if (!saveDraftResponse.ok()) throw new Error("autosave failed: " + saveDraftResponse.status());
  await page.getByText("Черновик сохранён", { exact: true }).waitFor({ timeout: 10000 });
  let draft = await contentFor(child.id);
  let record = draft.records?.find(item => item.entityId === entityID);
  if (!record || record.id !== recordID || record.values?.title !== "Browser singleton value" || draft.revision <= draft.publishedRevision) throw new Error("autosave lost ID or draft-only state");
  if (JSON.stringify(await readRuntime()) !== JSON.stringify(initialRuntime)) throw new Error("draft changed runtime before publish");

  await page.getByRole("button", { name: "Опубликовать", exact: true }).click();
  let dialog = page.getByRole("alertdialog");
  const publishPromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === contentPath + "/publish");
  await dialog.getByRole("button", { name: "Опубликовать", exact: true }).click();
  const publishResponse = await publishPromise;
  if (!publishResponse.ok()) throw new Error("publish failed: " + publishResponse.status());
  await dialog.waitFor({ state: "hidden", timeout: 10000 });
  let runtime = await readRuntime();
  if (runtime.record?.id !== recordID || runtime.record?.title !== "Browser singleton value" || runtime.scalar !== "Browser singleton value") throw new Error("explicit publish did not update runtime");

  await clearButton.click();
  dialog = page.getByRole("alertdialog");
  if (!(await dialog.innerText()).includes("Публичные страницы изменятся только после отдельной публикации")) throw new Error("clear confirmation omitted draft-only warning");
  const clearPromise = page.waitForResponse(response => response.request().method() === "PUT" && new URL(response.url()).pathname === contentPath);
  await dialog.getByRole("button", { name: "Очистить поля и связи", exact: true }).click();
  const clearResponse = await clearPromise;
  if (!clearResponse.ok()) throw new Error("confirmed clear save failed: " + clearResponse.status());
  await page.getByText("Черновик сохранён", { exact: true }).waitFor({ timeout: 10000 });
  draft = await contentFor(child.id);
  record = draft.records?.find(item => item.entityId === entityID);
  if (!record || record.id !== recordID || Object.keys(record.values ?? {}).length || draft.revision <= draft.publishedRevision) throw new Error("clear lost ID or changed publication state");
  runtime = await readRuntime();
  if (runtime.record?.id !== recordID || runtime.record?.title !== "Browser singleton value" || runtime.scalar !== "Browser singleton value") throw new Error("clear draft changed runtime before publish");

  await page.getByRole("button", { name: "Опубликовать", exact: true }).click();
  dialog = page.getByRole("alertdialog");
  const publishClearPromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === contentPath + "/publish");
  await dialog.getByRole("button", { name: "Опубликовать", exact: true }).click();
  const publishClearResponse = await publishClearPromise;
  if (!publishClearResponse.ok()) throw new Error("publish clear failed: " + publishClearResponse.status());
  await dialog.waitFor({ state: "hidden", timeout: 10000 });
  const final = await contentFor(child.id);
  record = final.records?.find(item => item.entityId === entityID);
  if (!record || record.id !== recordID || Object.keys(record.values ?? {}).length || final.revision !== final.publishedRevision) throw new Error("published clear lost ID or remained draft-only");
  runtime = await readRuntime();
  if (runtime.record?.id !== recordID || Object.hasOwn(runtime.record ?? {}, "title") || (runtime.scalar != null && runtime.scalar !== "")) throw new Error("published clear did not update runtime");
  branches = await branchList();
  branch = branches.items?.find(item => item.id === branchID);
  if (!branch || branch.snapshotId !== child.id) throw new Error("content operations changed branch assignment");
  if (pageErrors.length) throw new Error("browser errors: " + pageErrors.join(" | "));
  console.log("SINGLETON_RESULT=" + JSON.stringify({ childID: child.id, recordID, branchSnapshotID: branch.snapshotId }));
} finally {
  await context.close();
  await browser.close();
}`
	commandCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, node, "--input-type=module", "-e", browserScript)
	command.Env = append(os.Environ(),
		"SINGLETON_PLAYWRIGHT_MODULE="+module, "SINGLETON_PLAYWRIGHT_BROWSER="+browser,
		"SINGLETON_ADMIN_BASE="+strings.TrimRight(env.server.URL, "/"),
		"SINGLETON_RUNTIME_BASE="+strings.TrimRight(env.devServer.URL, "/"),
		"SINGLETON_ADMIN_TOKEN="+env.adminToken, "SINGLETON_ROOT_ID="+rootID,
		"SINGLETON_ENTITY_ID="+entityID, "SINGLETON_BRANCH_ID="+string(env.devBranch),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("singleton browser acceptance failed: %v\n%s", err, output)
	}
	const resultPrefix = "SINGLETON_RESULT="
	var result map[string]any
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, resultPrefix) {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, resultPrefix)), &result); err != nil {
				t.Fatalf("decode singleton browser result: %v\n%s", err, output)
			}
		}
	}
	childID, ok := result["childID"].(string)
	if !ok || childID == "" || result["recordID"] == "" || result["branchSnapshotID"] != childID {
		t.Fatalf("browser omitted child/record/branch results: %v\n%s", result, output)
	}
	childUUID, err := uuid.Parse(childID)
	if err != nil {
		t.Fatalf("parse child snapshot ID: %v", err)
	}
	recordID, ok := result["recordID"].(string)
	if !ok || recordID == "" {
		t.Fatalf("browser result omitted record ID: %v\n%s", result, output)
	}
	recordUUID, err := uuid.Parse(recordID)
	if err != nil {
		t.Fatalf("parse singleton record ID: %v", err)
	}
	childDetail, err := env.snapshots.Snapshot(ctx, childUUID)
	if err != nil {
		t.Fatalf("read child snapshot: %v", err)
	}
	if childDetail.Snapshot.ParentID == nil || childDetail.Snapshot.ParentID.String() != rootID {
		t.Fatalf("structural child lost parent link: %+v", childDetail.Snapshot)
	}
	finalWorkspace, err := env.snapshots.ModelContentWorkspace(ctx, childUUID)
	if err != nil {
		t.Fatalf("read final singleton workspace: %v", err)
	}
	if len(finalWorkspace.Records) != 1 || finalWorkspace.Records[0].ID != recordUUID.String() ||
		len(finalWorkspace.Records[0].Values) != 0 || finalWorkspace.Revision != finalWorkspace.PublishedRevision {
		t.Fatalf("final singleton state should retain ID and publish empty values: %+v", finalWorkspace)
	}
	branchAfter, err := env.snapshots.Branch(ctx, branchID)
	if err != nil || branchAfter.SnapshotID == nil || branchAfter.SnapshotID.String() != childID {
		t.Fatalf("content workflow changed branch assignment: branch=%+v err=%v", branchAfter, err)
	}
	modelContentSingletonBrowserAssertRuntime(t, env, recordID)
	t.Logf("singleton browser acceptance passed: parent=%s child=%s record=%s", rootID, childID, recordID)
}

func modelContentSingletonBrowserAssertRuntime(t *testing.T, env *testEnv, recordID string) {
	t.Helper()
	placement := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/")
	content, ok := placement["content"].(map[string]any)
	if !ok {
		t.Fatalf("runtime placement omitted content: %v", placement)
	}
	record, ok := content["record"].(map[string]any)
	if !ok || record["id"] != recordID || record["title"] != nil || (content["scalar"] != nil && content["scalar"] != "") {
		t.Fatalf("runtime did not reflect cleared singleton publication: %v", content)
	}
}
