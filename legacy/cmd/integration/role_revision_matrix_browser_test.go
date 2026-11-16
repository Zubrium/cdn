package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/pkg/environment"
)

// TestRoleRevisionMatrixBrowserAcceptance covers the agreed content and
// submission role controls using only synthetic format-2 builds. It requires a
// task-owned loopback database; newTestEnv verifies the exact DB name, loopback
// host, and zero other sessions before migrating or truncating anything.
func TestRoleRevisionMatrixBrowserAcceptance(t *testing.T) {
	node := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	module := strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browser := strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	missing := []string{}
	if node == "" {
		missing = append(missing, "PRERENDER_TEST_NODE")
	}
	if module == "" {
		missing = append(missing, "PRERENDER_TEST_MODULE")
	}
	if browser == "" {
		missing = append(missing, "PRERENDER_TEST_BROWSER")
	}
	if len(missing) > 0 {
		t.Skipf("Chromium role/revision acceptance skipped; set PRERENDER_TEST_NODE, PRERENDER_TEST_MODULE, and PRERENDER_TEST_BROWSER (missing: %s)", strings.Join(missing, ", "))
	}
	for label, path := range map[string]string{"Node.js": node, "Playwright module": module, "Chromium": browser} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("Chromium role/revision acceptance skipped; configured %s is unavailable at %q: %v", label, path, err)
		}
	}
	requireRoleRevisionUIBrowserDatabase(t)

	env := newTestEnv(t)
	const (
		formKey        = "qa-role-revision-ui-form"
		contentKey     = "qa-role-revision-ui-content"
		formVersion    = "1.0.0"
		contentVersion = "1.0.0"
	)

	formManifest := testFeatureManifestFor(formKey, formVersion)
	formManifest["form"] = map[string]any{"submissionSchema": objectSchema(map[string]any{
		"name": map[string]any{"type": "string", "title": "Name"},
	}, "name")}
	uploadFeatureBuild(t, env, formManifest, "export default function Feature() { return null; }")

	contentManifest := testFeatureManifestFor(contentKey, contentVersion)
	contentManifest["dataInputs"] = []any{map[string]any{
		"key": "entries", "kind": "collection", "fields": []any{
			map[string]any{"path": "/title", "type": "text"},
		},
	}}
	uploadFeatureBuild(t, env, contentManifest, "export default function Feature() { return null; }")

	entityID, titleFieldID := uuid.NewString(), uuid.NewString()
	config := map[string]any{
		"formatVersion": 3,
		"dataModel": map[string]any{
			"formatVersion": 1,
			"entities": []any{map[string]any{
				"id": entityID, "key": "article", "name": "Article",
				"fields": []any{map[string]any{
					"id": titleFieldID, "key": "title", "name": "Title", "type": "text",
				}},
			}},
			"relations": []any{},
		},
		"pages": []any{map[string]any{
			"id": "home", "name": "Home", "kind": "page",
			"placements": []any{
				map[string]any{
					"id": "submission-form", "build": map[string]any{"featureKey": formKey, "version": formVersion}, "slot": "main",
				},
				map[string]any{
					"id": "article-list", "build": map[string]any{"featureKey": contentKey, "version": contentVersion}, "slot": "main",
					"dataBindings": []any{map[string]any{
						"inputKey": "entries", "entityId": entityID,
						"fields": []any{map[string]any{"inputPath": "/title", "fieldId": titleFieldID}},
					}},
				},
			},
		}},
		"routeTree": map[string]any{
			"id": "site", "type": "site", "children": []any{map[string]any{
				"id": "home-route", "type": "path", "path": "/", "pageId": "home", "children": []any{},
			}},
		},
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, config, "Synthetic role revision browser acceptance")
	snapshotID := snapshot["id"].(string)
	contentURL := "/api/v2/admin/content/snapshots/" + snapshotID + "/model"

	// Create two published states and a newer draft. The browser restores the
	// first publication and must leave the second publication visible publicly.
	records := []any{map[string]any{
		"entityId": entityID, "sortOrder": 0, "values": map[string]any{"title": "Historical value"},
	}}
	status, firstDraft, _ := env.request(http.MethodPut, contentURL, env.adminToken, map[string]any{"revision": 0, "records": records, "selections": []any{}})
	if status != http.StatusOK {
		t.Fatalf("save first synthetic content draft: status=%d body=%v", status, firstDraft)
	}
	firstRevision := roleUIRevision(t, firstDraft["revision"])
	status, firstPublished, _ := env.request(http.MethodPost, contentURL+"/publish", env.adminToken, map[string]any{"revision": firstRevision})
	if status != http.StatusOK {
		t.Fatalf("publish first synthetic content revision: status=%d body=%v", status, firstPublished)
	}
	records = roleUICloneRecords(t, firstPublished["records"].([]any))
	roleUISetTitle(t, records, "Historical value", "Live value")
	status, secondDraft, _ := env.request(http.MethodPut, contentURL, env.adminToken, map[string]any{
		"revision": firstPublished["revision"], "records": records, "selections": firstPublished["selections"],
	})
	if status != http.StatusOK {
		t.Fatalf("save second synthetic content draft: status=%d body=%v", status, secondDraft)
	}
	status, secondPublished, _ := env.request(http.MethodPost, contentURL+"/publish", env.adminToken, map[string]any{"revision": secondDraft["revision"]})
	if status != http.StatusOK {
		t.Fatalf("publish second synthetic content revision: status=%d body=%v", status, secondPublished)
	}
	records = roleUICloneRecords(t, secondPublished["records"].([]any))
	roleUISetTitle(t, records, "Live value", "Unpublished candidate")
	status, currentDraft, _ := env.request(http.MethodPut, contentURL, env.adminToken, map[string]any{
		"revision": secondPublished["revision"], "records": records, "selections": secondPublished["selections"],
	})
	if status != http.StatusOK {
		t.Fatalf("save visible unpublished draft: status=%d body=%v", status, currentDraft)
	}
	contentRecordID := currentDraft["records"].([]any)[0].(map[string]any)["id"].(string)
	if roleUIRevision(t, currentDraft["publishedRevision"]) != 2 {
		t.Fatalf("synthetic workspace should have two published revisions: %v", currentDraft)
	}
	publicTitles := roleUIRuntimeTitles(t, env, "article-list", "entries")
	if len(publicTitles) != 1 || publicTitles[0] != "Live value" {
		t.Fatalf("runtime should expose the latest published value before restore: %v", publicTitles)
	}

	moderatorToken := roleUICreateToken(t, env, "moderator")
	staffToken := roleUICreateToken(t, env, "staff")
	userToken := roleUICreateToken(t, env, "user")
	env.forms.SetSnapshotSchemaProvider(env.snapshots)
	submission, err := env.forms.Submit(context.Background(), environment.Environment(string(env.devBranch)), formKey, json.RawMessage(`{"name":"Synthetic browser lead"}`))
	if err != nil {
		t.Fatalf("create synthetic submission for role visibility: %v", err)
	}

	browserScript := `import { pathToFileURL } from "node:url";
import { readFile } from "node:fs/promises";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, chromiumSandbox: true });
const failures = [];
const assert = (condition, message) => { if (!condition) failures.push(message); };
const baseURL = process.argv[1];
const publicURL = process.env.TEST_PUBLIC_URL;
const snapshotID = process.env.TEST_SNAPSHOT_ID;
const submissionID = process.env.TEST_SUBMISSION_ID;
const contentURL = baseURL + "/admin/content?snapshot=" + encodeURIComponent(snapshotID);
const submissionURL = baseURL + "/admin/submissions/" + encodeURIComponent(submissionID);
const workspaceAPI = "/api/v2/admin/content/snapshots/" + encodeURIComponent(snapshotID) + "/model";
const pageErrors = [];
try {
  const context = await browser.newContext({ acceptDownloads: true, viewport: { width: 1360, height: 900 } });
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", error => pageErrors.push(error.message));

  async function visitAs(token, url) {
    await page.goto(baseURL + "/admin/", { waitUntil: "domcontentloaded" });
    await page.evaluate(value => localStorage.setItem("we_admin_token", value), token);
    await page.goto(url, { waitUntil: "domcontentloaded" });
  }
  async function assertContentControls(role, token) {
    await visitAs(token, contentURL);
    await page.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor();
    await page.getByRole("button", { name: "История", exact: true }).waitFor();
    await page.getByRole("button", { name: "Опубликовать", exact: true }).waitFor();
    await page.getByRole("button", { name: "Unpublished candidate", exact: true }).waitFor();
    await page.locator(".data-table").count().then(count => { if (count) throw new Error(role + " unexpectedly rendered the submissions table on content page"); });
  }
  async function assertSubmissionControls(role, token, expected) {
    await visitAs(token, submissionURL);
    await page.getByRole("heading", { name: "Заявка", exact: true }).waitFor();
    await page.getByText("Synthetic browser lead", { exact: true }).waitFor();
    const work = await page.getByRole("button", { name: "В работе", exact: true }).count();
    const archive = await page.getByRole("button", { name: "В архив", exact: true }).count();
    const remove = await page.getByRole("button", { name: "Удалить", exact: true }).count();
    assert(work === Number(expected.manage), role + " status control count=" + work);
    assert(archive === Number(expected.manage), role + " archive control count=" + archive);
    assert(remove === Number(expected.delete), role + " delete control count=" + remove);
  }

  // Staff and user have the same content editor actions as site managers.
  await assertContentControls("user", process.env.TEST_USER_TOKEN);
  await assertSubmissionControls("user", process.env.TEST_USER_TOKEN, { manage: false, delete: false });
  await assertContentControls("staff", process.env.TEST_STAFF_TOKEN);
  await assertSubmissionControls("staff", process.env.TEST_STAFF_TOKEN, { manage: false, delete: false });
  await assertContentControls("moderator", process.env.TEST_MODERATOR_TOKEN);
  await assertSubmissionControls("moderator", process.env.TEST_MODERATOR_TOKEN, { manage: true, delete: false });
  await assertContentControls("admin", process.env.TEST_ADMIN_TOKEN);
  await assertSubmissionControls("admin", process.env.TEST_ADMIN_TOKEN, { manage: true, delete: true });

  // Restore publication 1 into a draft. The public projection remains the
  // current publication until the separate publish button is confirmed.
  await assertContentControls("user", process.env.TEST_USER_TOKEN);
  await page.getByRole("button", { name: "Unpublished candidate", exact: true }).click();
  await page.getByRole("textbox", { name: "Title", exact: true }).waitFor();
  await page.getByRole("button", { name: "История", exact: true }).click();
  const history = page.getByRole("dialog");
  await history.getByRole("heading", { name: "История публикаций", exact: true }).waitFor();
  const firstPublication = history.locator("li").filter({ hasText: "Публикация 1" });
  await firstPublication.getByRole("button", { name: "Восстановить", exact: true }).click();
  const restoreConfirm = page.getByRole("alertdialog");
  await restoreConfirm.getByText("Публичные страницы изменятся только после отдельной публикации.", { exact: false }).waitFor();
  await restoreConfirm.getByRole("button", { name: "Создать черновик", exact: true }).click();
  await page.getByText("Черновик 4 · опубликовано 2", { exact: true }).waitFor();
  let restoredWorkspace = await page.evaluate(async path => {
    const response = await fetch(path, { headers: { Authorization: "Bearer " + localStorage.getItem("we_admin_token") } });
    return { status: response.status, body: await response.json() };
  }, workspaceAPI);
  if (restoredWorkspace.status !== 200 || restoredWorkspace.body.revision !== 4 || restoredWorkspace.body.publishedRevision !== 2) {
    throw new Error("history restore should advance only the draft revision: " + JSON.stringify(restoredWorkspace));
  }
  const restoredRecord = restoredWorkspace.body.records.find(record => record.id === process.env.TEST_CONTENT_RECORD_ID);
  if (restoredRecord?.values?.title !== "Historical value") throw new Error("history restore did not create the historical draft: " + JSON.stringify(restoredWorkspace.body.records));
  if (await page.getByRole("textbox", { name: "Title", exact: true }).inputValue() !== "Historical value") throw new Error("history restore value was not rendered in the editor");
  const runtimeBeforeRestorePublish = await fetch(publicURL + "/api/v2/runtime?path=%2F").then(async response => ({ status: response.status, body: await response.json() }));
  const beforePlacement = runtimeBeforeRestorePublish.body.placements?.find(item => item.id === "article-list");
  const beforeTitles = beforePlacement?.content?.entries?.map(record => record.title) ?? [];
  if (runtimeBeforeRestorePublish.status !== 200 || beforeTitles.length !== 1 || beforeTitles[0] !== "Live value") {
    throw new Error("restoring history changed public output before publication: " + JSON.stringify(runtimeBeforeRestorePublish));
  }

  await page.getByRole("button", { name: "Опубликовать", exact: true }).click();
  const publishConfirm = page.getByRole("alertdialog");
  await publishConfirm.getByRole("heading", { name: "Опубликовать контент?", exact: true }).waitFor();
  await publishConfirm.getByRole("button", { name: "Опубликовать", exact: true }).click();
  await page.getByText("Черновик 4 · опубликовано 4", { exact: true }).waitFor();
  restoredWorkspace = await page.evaluate(async path => {
    const response = await fetch(path, { headers: { Authorization: "Bearer " + localStorage.getItem("we_admin_token") } });
    return { status: response.status, body: await response.json() };
  }, workspaceAPI);
  if (restoredWorkspace.status !== 200 || restoredWorkspace.body.publishedRevision !== 4) throw new Error("explicit publication after restore failed: " + JSON.stringify(restoredWorkspace));
  const publishedRuntime = await fetch(publicURL + "/api/v2/runtime?path=%2F").then(async response => ({ status: response.status, body: await response.json() }));
  if (publishedRuntime.status !== 200) throw new Error("public runtime request after restore publish failed: " + publishedRuntime.status);
  const composition = publishedRuntime.body;
  const placement = composition.placements.find(item => item.id === "article-list");
  const visibleTitles = placement?.content?.entries?.map(record => record.title) ?? [];
  if (visibleTitles.length !== 1 || visibleTitles[0] !== "Historical value") throw new Error("published runtime did not change after the explicit publish: " + JSON.stringify(visibleTitles));

  // Fail one autosave before sending it. The local value must remain, the
  // server must remain at revision 4, and the UI must offer an explicit retry.
  await visitAs(process.env.TEST_STAFF_TOKEN, contentURL);
  await page.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor();
  await page.getByRole("button", { name: "Historical value", exact: true }).click();
  const titleInput = page.getByRole("textbox", { name: "Title", exact: true });
  await titleInput.waitFor();
  const workspaceRoutePattern = "**/api/v2/admin/content/snapshots/" + snapshotID + "/model";
  async function readWorkspace(targetPage) {
    return targetPage.evaluate(async path => {
      const response = await fetch(path, { headers: { Authorization: "Bearer " + localStorage.getItem("we_admin_token") } });
      return { status: response.status, body: await response.json() };
    }, workspaceAPI);
  }
  const assertOnlyRecord = (workspace, expectedRevision, expectedTitle, phase) => {
    if (workspace.status !== 200 || workspace.body.revision !== expectedRevision || workspace.body.records.length !== 1) {
      throw new Error(phase + " must have one record at revision " + expectedRevision + ": " + JSON.stringify(workspace));
    }
    const onlyRecord = workspace.body.records[0];
    if (onlyRecord.id !== process.env.TEST_CONTENT_RECORD_ID || onlyRecord.values?.title !== expectedTitle) {
      throw new Error(phase + " changed record identity/value: " + JSON.stringify(workspace.body.records));
    }
  };

  let abortNextPut = true;
  let abortedRequest;
  const abortBeforeCommit = async route => {
    if (route.request().method() === "PUT" && abortNextPut) {
      abortNextPut = false;
      abortedRequest = route.request().postDataJSON();
      await route.abort("failed");
      return;
    }
    await route.continue();
  };
  await page.route(workspaceRoutePattern, abortBeforeCommit);
  await titleInput.fill("Pre-commit retry draft");
  await page.getByRole("button", { name: "Повторить сохранение", exact: true }).waitFor();
  if (abortedRequest?.revision !== 4 || abortedRequest.records[0]?.values?.title !== "Pre-commit retry draft") {
    throw new Error("browser did not send the expected revision-4 autosave before transport failure: " + JSON.stringify(abortedRequest));
  }
  if (await titleInput.inputValue() !== "Pre-commit retry draft") throw new Error("pre-commit transport failure discarded local input");
  const preCommitWorkspace = await readWorkspace(page);
  assertOnlyRecord(preCommitWorkspace, 4, "Historical value", "pre-commit transport failure");
  await page.unroute(workspaceRoutePattern, abortBeforeCommit);
  await page.getByRole("button", { name: "Повторить сохранение", exact: true }).click();
  await page.getByText("Черновик 5 · опубликовано 4", { exact: true }).waitFor();
  const afterPreCommitRetry = await readWorkspace(page);
  assertOnlyRecord(afterPreCommitRetry, 5, "Pre-commit retry draft", "pre-commit retry");

  // Commit the next autosave, then drop its successful response. A second
  // editor saves a newer value before retry, forcing a safe 409 rather than
  // allowing the stale local write to overwrite the server.
  let loseNextResponse = true;
  let resolveCommittedRequest;
  const committedRequest = new Promise(resolve => { resolveCommittedRequest = resolve; });
  const commitThenDropResponse = async route => {
    if (route.request().method() === "PUT" && loseNextResponse) {
      loseNextResponse = false;
      const response = await route.fetch();
      const body = await response.json();
      const result = { status: response.status(), body };
      await route.abort("connectionreset");
      resolveCommittedRequest(result);
      return;
    }
    await route.continue();
  };
  await page.route(workspaceRoutePattern, commitThenDropResponse);
  await titleInput.fill("Response-lost local draft");
  const committedButUnacknowledged = await committedRequest;
  if (committedButUnacknowledged.status !== 200 || committedButUnacknowledged.body.revision !== 6 || committedButUnacknowledged.body.records.length !== 1) {
    throw new Error("response-loss setup did not commit exactly one record as revision 6: " + JSON.stringify(committedButUnacknowledged));
  }
  await page.getByRole("button", { name: "Повторить сохранение", exact: true }).waitFor();
  if (await titleInput.inputValue() !== "Response-lost local draft") throw new Error("lost successful response discarded local input");
  const afterLostResponse = await readWorkspace(page);
  assertOnlyRecord(afterLostResponse, 6, "Response-lost local draft", "committed save with lost response");

  const competingPage = await context.newPage();
  competingPage.setDefaultTimeout(15000);
  competingPage.on("pageerror", error => pageErrors.push(error.message));
  await competingPage.goto(baseURL + "/admin/", { waitUntil: "domcontentloaded" });
  await competingPage.evaluate(token => localStorage.setItem("we_admin_token", token), process.env.TEST_STAFF_TOKEN);
  await competingPage.goto(contentURL, { waitUntil: "domcontentloaded" });
  await competingPage.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor();
  await competingPage.getByRole("button", { name: "Response-lost local draft", exact: true }).click();
  const competingTitleInput = competingPage.getByRole("textbox", { name: "Title", exact: true });
  await competingTitleInput.fill("Newer server draft after lost response");
  await competingPage.getByText("Черновик 7 · опубликовано 4", { exact: true }).waitFor();
  const newerServerWorkspace = await readWorkspace(competingPage);
  assertOnlyRecord(newerServerWorkspace, 7, "Newer server draft after lost response", "second-tab newer draft");

  // Retrying the ambiguous commit uses stale revision 5. It must return 409,
  // preserve this tab's local text, show both revisions, and avoid duplicates.
  const staleRetryResponse = page.waitForResponse(response =>
    new URL(response.url()).pathname === workspaceAPI && response.request().method() === "PUT" && response.status() === 409,
  );
  await page.getByRole("button", { name: "Повторить сохранение", exact: true }).click();
  const staleRetry = await staleRetryResponse;
  if (staleRetry.status() !== 409) throw new Error("retry after the lost response did not return a revision conflict");
  await page.getByText("Конфликт ревизии", { exact: true }).waitFor();
  const conflictAlert = page.getByRole("alert");
  await conflictAlert.getByText("Локальная ревизия: 5 · серверная ревизия: 7", { exact: true }).waitFor();
  const reloadButton = conflictAlert.getByRole("button", { name: "Загрузить сохранённую ревизию", exact: true });
  await reloadButton.waitFor();
  if (await titleInput.inputValue() !== "Response-lost local draft") throw new Error("stale retry replaced local input after the commit response was lost");
  const serverAfterRejectedRetry = await readWorkspace(page);
  assertOnlyRecord(serverAfterRejectedRetry, 7, "Newer server draft after lost response", "stale retry rejection");

  const downloadPromise = page.waitForEvent("download");
  await conflictAlert.getByRole("button", { name: "Скачать локальный черновик", exact: true }).click();
  const localDownload = await downloadPromise;
  if (localDownload.suggestedFilename() !== "content-draft-" + snapshotID + ".json") throw new Error("unexpected local draft filename: " + localDownload.suggestedFilename());
  const localPath = await localDownload.path();
  if (!localPath) throw new Error("browser did not produce a downloaded local draft");
  const downloadedDraft = JSON.parse(await readFile(localPath, "utf8"));
  const downloadedRecord = downloadedDraft.records.find(record => record.id === process.env.TEST_CONTENT_RECORD_ID);
  if (downloadedRecord?.values?.title !== "Response-lost local draft") throw new Error("download did not preserve the response-lost local value");

  await reloadButton.click();
  const reloadConfirm = page.getByRole("alertdialog");
  await reloadConfirm.getByRole("heading", { name: "Загрузить сохранённую ревизию?", exact: true }).waitFor();
  await reloadConfirm.getByRole("button", { name: "Отмена", exact: true }).click();
  if (await titleInput.inputValue() !== "Response-lost local draft") throw new Error("cancelling server reload discarded the local value after response loss");

  // In-app navigation must stop before unmounting the dirty editor. Cancelling
  // the prompt keeps the exact local value available for recovery.
  await page.getByRole("link", { name: "Главная", exact: true }).click();
  const leaveConfirm = page.getByRole("alertdialog");
  await leaveConfirm.getByRole("heading", { name: "Есть несохранённые изменения", exact: true }).waitFor();
  await leaveConfirm.getByRole("button", { name: "Остаться", exact: true }).click();
  await page.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor();
  if (await titleInput.inputValue() !== "Response-lost local draft") throw new Error("cancelling in-app navigation discarded the local value");

  // A real document reload must raise beforeunload. After accepting it, the
  // session recovery draft should reopen as a conflict and must not autosave
  // over the competing tab's newer revision.
  let recoveredPutCount = 0;
  page.on("request", request => {
    if (new URL(request.url()).pathname === workspaceAPI && request.method() === "PUT") recoveredPutCount++;
  });
  const beforeUnloadPromise = page.waitForEvent("dialog");
  const reloadPromise = page.reload({ waitUntil: "domcontentloaded" });
  const beforeUnload = await beforeUnloadPromise;
  if (beforeUnload.type() !== "beforeunload") throw new Error("dirty editor reload did not trigger a beforeunload dialog");
  await beforeUnload.accept();
  await reloadPromise;
  await page.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor();
  await page.getByRole("button", { name: "Response-lost local draft", exact: true }).click();
  const recoveredTitleInput = page.getByRole("textbox", { name: "Title", exact: true });
  await recoveredTitleInput.waitFor();
  if (await recoveredTitleInput.inputValue() !== "Response-lost local draft") throw new Error("document reload did not recover the local draft");
  await page.getByRole("alert").getByText("Локальная ревизия: 5 · серверная ревизия: 7", { exact: true }).waitFor();
  await page.waitForTimeout(1200);
  if (recoveredPutCount !== 0) throw new Error("recovered conflicting local draft was sent automatically after reload");
  const recoveredWorkspace = await readWorkspace(page);
  assertOnlyRecord(recoveredWorkspace, 7, "Newer server draft after lost response", "recovered local draft after reload");
  if (recoveredWorkspace.body.publishedRevision !== 4) throw new Error("recovering the local draft changed the published revision");
  const runtimeAfterRecovery = await fetch(publicURL + "/api/v2/runtime?path=%2F").then(async response => ({ status: response.status, body: await response.json() }));
  const recoveredPlacement = runtimeAfterRecovery.body.placements?.find(item => item.id === "article-list");
  const recoveredTitles = recoveredPlacement?.content?.entries?.map(record => record.title) ?? [];
  if (runtimeAfterRecovery.status !== 200 || recoveredTitles.length !== 1 || recoveredTitles[0] !== "Historical value") {
    throw new Error("recovering the local draft changed published runtime output: " + JSON.stringify(runtimeAfterRecovery));
  }

  const recoveredConflictAlert = page.getByRole("alert");
  const recoveredReloadButton = recoveredConflictAlert.getByRole("button", { name: "Загрузить сохранённую ревизию", exact: true });
  await recoveredReloadButton.click();
  const recoveredReloadConfirm = page.getByRole("alertdialog");
  await recoveredReloadConfirm.getByRole("button", { name: "Загрузить серверную версию", exact: true }).click();
  await page.waitForFunction(() => [...document.querySelectorAll("input")].some(input => input.value === "Newer server draft after lost response"));
  await page.getByText("Черновик 7 · опубликовано 4", { exact: true }).waitFor();
  const afterConfirmedReload = await readWorkspace(page);
  assertOnlyRecord(afterConfirmedReload, 7, "Newer server draft after lost response", "confirmed reload");
  await competingPage.close();

  if (pageErrors.length) throw new Error("role/revision browser raised page errors: " + pageErrors.join(" | "));
  console.log("ROLE_REVISION_MATRIX_BROWSER=PASS; roles=user,staff,moderator,admin; restore=draft-until-publish; precommit=retry-revision-5; response-lost=commit-once-retry-409-revisions-5-7; in-app-and-browser-reload=local-draft-recovered-without-autosave");
} finally {
  await browser.close();
}`
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", browserScript, env.server.URL)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browser,
		"TEST_ADMIN_TOKEN="+env.adminToken,
		"TEST_MODERATOR_TOKEN="+moderatorToken,
		"TEST_STAFF_TOKEN="+staffToken,
		"TEST_USER_TOKEN="+userToken,
		"TEST_PUBLIC_URL="+env.devServer.URL,
		"TEST_SNAPSHOT_ID="+snapshotID,
		"TEST_SUBMISSION_ID="+submission.ID.String(),
		"TEST_CONTENT_RECORD_ID="+contentRecordID,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic role/revision browser acceptance failed: %v\n%s", err, output)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			t.Log(line)
		}
	}
}

func requireRoleRevisionUIBrowserDatabase(t *testing.T) {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if rawURL == "" {
		t.Skip("role/revision browser acceptance skipped; set TEST_DATABASE_URL to a uniquely named loopback disposable website_engine_role_revision_ui_qa_<suffix> database")
	}
	parsed, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse role/revision UI disposable database URL: %v", err)
	}
	const prefix = "website_engine_role_revision_ui_qa_"
	if !strings.HasPrefix(parsed.Database, prefix) || strings.TrimPrefix(parsed.Database, prefix) == "" {
		t.Skipf("role/revision browser acceptance skipped; refusing database %q, expected a uniquely named disposable DB with prefix %q", parsed.Database, prefix)
	}
}

func roleUICreateToken(t *testing.T, env *testEnv, role string) string {
	t.Helper()
	status, body, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken, map[string]any{
		"name": "role revision browser " + role, "role": role,
	})
	if status != http.StatusCreated {
		t.Fatalf("create synthetic %s browser token: status=%d body=%v", role, status, body)
	}
	return body["token"].(string)
}

func roleUIRevision(t *testing.T, raw any) int64 {
	t.Helper()
	value, ok := raw.(float64)
	if !ok {
		t.Fatalf("expected numeric revision, got %T (%v)", raw, raw)
	}
	return int64(value)
}

func roleUICloneRecords(t *testing.T, records []any) []any {
	t.Helper()
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	var result []any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func roleUISetTitle(t *testing.T, records []any, current, next string) {
	t.Helper()
	for _, raw := range records {
		record := raw.(map[string]any)
		values := record["values"].(map[string]any)
		if values["title"] == current {
			values["title"] = next
			return
		}
	}
	t.Fatalf("could not find synthetic title %q in records: %v", current, records)
}

func roleUIRuntimeTitles(t *testing.T, env *testEnv, placementID, inputKey string) []string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, env.devServer.URL+"/api/v2/runtime?path=%2F", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("get synthetic public runtime: %v", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get synthetic public runtime: status=%d body=%s", response.StatusCode, raw)
	}
	var composition map[string]any
	if err := json.Unmarshal(raw, &composition); err != nil {
		t.Fatal(err)
	}
	placements, ok := composition["placements"].([]any)
	if !ok {
		t.Fatalf("runtime composition is missing placements: %v", composition)
	}
	for _, rawPlacement := range placements {
		placement, ok := rawPlacement.(map[string]any)
		if !ok || placement["id"] != placementID {
			continue
		}
		content, ok := placement["content"].(map[string]any)
		if !ok {
			t.Fatalf("runtime placement %s has no content object: %v", placementID, placement)
		}
		items, ok := content[inputKey].([]any)
		if !ok {
			t.Fatalf("runtime placement %s input %s is not a collection: %v", placementID, inputKey, content[inputKey])
		}
		titles := make([]string, 0, len(items))
		for _, rawItem := range items {
			item := rawItem.(map[string]any)
			title, _ := item["title"].(string)
			titles = append(titles, title)
		}
		return titles
	}
	t.Fatalf("runtime composition does not contain placement %q: %v", placementID, placements)
	return nil
}
