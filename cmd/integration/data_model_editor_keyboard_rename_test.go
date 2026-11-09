package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/sitebuilder"
)

type dataModelKeyboardBrowserResult struct {
	InitialSnapshotID       string `json:"initialSnapshotId"`
	FinalSnapshotID         string `json:"finalSnapshotId"`
	EntityID                string `json:"entityId"`
	CategoryID              string `json:"categoryId"`
	TitleFieldID            string `json:"titleFieldId"`
	StatusFieldID           string `json:"statusFieldId"`
	RelationID              string `json:"relationId"`
	FocusAfterMove          string `json:"focusAfterMove"`
	FocusAfterAdd           string `json:"focusAfterAdd"`
	FocusAfterAddVisible    bool   `json:"focusAfterAddVisible"`
	FocusAfterDelete        string `json:"focusAfterDelete"`
	FocusAfterDeleteVisible bool   `json:"focusAfterDeleteVisible"`
}

// TestDataModelEditorKeyboardRenameReorderBrowserAcceptance verifies that
// keyboard editing preserves model IDs, saves field order, and restores focus
// when a relation action removes the active control.
func TestDataModelEditorKeyboardRenameReorderBrowserAcceptance(t *testing.T) {
	node, playwright, browser := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE")), strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE")), strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	for name, path := range map[string]string{
		"PRERENDER_TEST_NODE":    node,
		"PRERENDER_TEST_MODULE":  playwright,
		"PRERENDER_TEST_BROWSER": browser,
	} {
		if path == "" {
			t.Skipf("set %s for data-model editor browser acceptance", name)
		}
		if _, err := os.Stat(path); err != nil {
			t.Skipf("%s=%q is unavailable: %v", name, path, err)
		}
	}
	requireDataModelKeyboardDatabase(t)
	env := newTestEnv(t)
	ctx := context.Background()
	rootConfig := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Pages:         []sitebuilder.Page{{ID: "keyboard-home", Name: "Главная", Kind: "page", Placements: []sitebuilder.Placement{}}},
		RouteTree: sitebuilder.RouteTreeNode{
			ID: "keyboard-site", Type: "site",
			Children: []sitebuilder.RouteTreeNode{{ID: "keyboard-home-route", Type: "path", Path: "/", PageID: "keyboard-home"}},
		},
	}
	root, err := env.snapshots.CreateSnapshot(ctx, testActor(t, env), sitebuilder.CreateSnapshotInput{Name: "Data model keyboard root", Config: &rootConfig})
	if err != nil {
		t.Fatalf("create disposable keyboard editor root snapshot: %v", err)
	}
	branchesBefore, err := env.snapshots.Branches(ctx)
	if err != nil {
		t.Fatalf("read branch assignments before browser scenario: %v", err)
	}

	script := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.DM_PLAYWRIGHT_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.DM_PLAYWRIGHT_BROWSER, headless: true, chromiumSandbox: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
await context.addInitScript(token => localStorage.setItem("we_admin_token", token), process.env.DM_ADMIN_TOKEN);
const page = await context.newPage();
const pageErrors = [];
page.on("pageerror", error => pageErrors.push(error.message));
const base = process.env.DM_ADMIN_BASE_URL;
const initialSnapshotID = process.env.DM_ROOT_SNAPSHOT_ID;
const headers = { Authorization: "Bearer " + process.env.DM_ADMIN_TOKEN };
const tabTo = async locator => {
  if (await locator.evaluate(element => document.activeElement === element).catch(() => false)) return;
  for (let index = 0; index < 100; index++) {
    await page.keyboard.press("Tab");
    if (await locator.evaluate(element => document.activeElement === element)) return;
  }
  throw new Error("Tab order did not reach " + await locator.getAttribute("aria-label") + " / " + await locator.innerText().catch(() => "control"));
};
const typeByKeyboard = async (locator, value) => {
  await tabTo(locator);
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type(value);
};
const activateByKeyboard = async locator => {
  await tabTo(locator);
  await page.keyboard.press("Enter");
};
const saveSnapshot = async () => {
  const responsePromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v2/admin/site/snapshots");
  await activateByKeyboard(page.getByRole("button", { name: "Сохранить", exact: true }));
  const response = await responsePromise;
  if (!response.ok()) throw new Error("structural save failed: " + response.status() + " " + await response.text());
  const snapshot = await response.json();
  if (!snapshot.id) throw new Error("structural save did not return a child snapshot ID");
  await page.waitForFunction(id => new URL(location.href).searchParams.get("snapshot") === id, snapshot.id);
  return snapshot.id;
};
const readModel = async snapshotID => {
  const response = await fetch(base + "/api/v2/admin/site/snapshots/" + encodeURIComponent(snapshotID), { headers });
  if (!response.ok) throw new Error("snapshot detail failed: " + response.status + " " + await response.text());
  return (await response.json()).payload?.config?.dataModel ?? null;
};
try {
  await page.goto(base + "/admin/site/data-model?snapshot=" + encodeURIComponent(initialSnapshotID), { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Модель данных", exact: true }).waitFor({ timeout: 20000 });

  await activateByKeyboard(page.getByRole("button", { name: "Добавить сущность", exact: true }));
  const newArticle = page.getByRole("article", { name: "Новая сущность" });
  const articleName = newArticle.getByRole("textbox", { name: "Название сущности", exact: true });
  await articleName.waitFor();
  await typeByKeyboard(articleName, "Публикация QA");
  const article = page.getByRole("article", { name: "Публикация QA" });
  const addArticleField = article.getByRole("button", { name: "Добавить поле в сущность Публикация QA", exact: true });
  await activateByKeyboard(addArticleField);
  const articleFields = article.getByRole("textbox", { name: "Название поля", exact: true });
  const titleName = articleFields.nth(0);
  await typeByKeyboard(titleName, "Название");
  await activateByKeyboard(addArticleField);
  const statusName = articleFields.nth(1);
  await typeByKeyboard(statusName, "Статус");

  await activateByKeyboard(page.getByRole("button", { name: "Добавить сущность", exact: true }));
  const newCategory = page.getByRole("article", { name: "Новая сущность" });
  const categoryName = newCategory.getByRole("textbox", { name: "Название сущности", exact: true });
  await categoryName.waitFor();
  await typeByKeyboard(categoryName, "Категория QA");
  const category = page.getByRole("article", { name: "Категория QA" });
  const addCategoryField = category.getByRole("button", { name: "Добавить поле в сущность Категория QA", exact: true });
  await activateByKeyboard(addCategoryField);
  await typeByKeyboard(category.getByRole("textbox", { name: "Название поля", exact: true }), "Название");

  const initialSavedSnapshotID = await saveSnapshot();
  const initialModel = await readModel(initialSavedSnapshotID);
  const savedArticle = initialModel?.entities?.find(entity => entity.name === "Публикация QA");
  const savedCategory = initialModel?.entities?.find(entity => entity.name === "Категория QA");
  if (!savedArticle?.id || !savedCategory?.id || savedArticle.fields?.length !== 2 || savedCategory.fields?.length !== 1) throw new Error("initial model snapshot did not preserve both generated entities and fields");
  const savedTitle = savedArticle.fields.find(field => field.name === "Название");
  const savedStatus = savedArticle.fields.find(field => field.name === "Статус");
  if (!savedTitle?.id || !savedStatus?.id) throw new Error("initial fields did not receive stable system IDs");

  const currentArticle = page.getByRole("article", { name: "Публикация QA" });
  const currentArticleName = currentArticle.getByRole("textbox", { name: "Название сущности", exact: true });
  const articleControlID = await currentArticleName.getAttribute("id");
  await typeByKeyboard(currentArticleName, "Публикация с разделами QA");
  const renamedArticleName = page.locator("#" + articleControlID);
  if (await renamedArticleName.getAttribute("id") !== articleControlID) throw new Error("renaming an entity changed its stable control ID");
  const renamedArticle = page.getByRole("article", { name: "Публикация с разделами QA" });
  const renamedTitle = renamedArticle.getByRole("textbox", { name: "Название поля", exact: true }).nth(0);
  const titleControlID = await renamedTitle.getAttribute("id");
  await typeByKeyboard(renamedTitle, "Заголовок");
  if (await renamedTitle.getAttribute("id") !== titleControlID) throw new Error("renaming a field changed its stable control ID");
  const renamedStatus = renamedArticle.getByRole("textbox", { name: "Название поля", exact: true }).nth(1);
  await typeByKeyboard(renamedStatus, "Состояние");

  const moveStatusUp = renamedArticle.getByRole("button", { name: "Переместить поле Состояние выше", exact: true });
  await activateByKeyboard(moveStatusUp);
  await page.waitForFunction(id => document.activeElement instanceof HTMLElement && document.activeElement.id === id, "data-model-field-name-" + savedStatus.id);
  const focusAfterMove = await page.evaluate(() => {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement)) return { id: "", visible: false };
    const style = getComputedStyle(active);
    return { id: active.id, visible: active.matches(":focus-visible") || style.outlineStyle !== "none" || style.boxShadow !== "none" };
  });
  if (focusAfterMove.id !== "data-model-field-name-" + savedStatus.id || !focusAfterMove.visible) throw new Error("field reorder lost keyboard focus or its visible indicator: " + JSON.stringify(focusAfterMove));
  const movedStatusButton = renamedArticle.getByRole("button", { name: "Переместить поле Состояние выше", exact: true });
  if (!await movedStatusButton.isDisabled()) throw new Error("moved field was not reordered to the first position");

  const relationForm = page.getByRole("group", { name: "Новая связь", exact: true });
  const fromEntity = relationForm.getByLabel("От сущности");
  const toEntity = relationForm.getByLabel("К сущности");
  await fromEntity.selectOption({ label: "Публикация с разделами QA" });
  await toEntity.selectOption({ label: "Категория QA" });
  const newRelationName = relationForm.getByLabel("Название связи", { exact: true });
  await typeByKeyboard(newRelationName, "Содержит QA");
  const addRelation = relationForm.getByRole("button", { name: "Связать", exact: true });
  await activateByKeyboard(addRelation);
  const relationList = page.getByRole("list", { name: "Связи модели" });
  const relationName = relationList.getByRole("textbox", { name: "Название связи Публикация с разделами QA — Категория QA", exact: true });
  await relationName.waitFor();
  const focusAfterAdd = await page.evaluate(() => {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement)) return { id: "", visible: false };
    const rectangle = active.getBoundingClientRect();
    const style = getComputedStyle(active);
    const inViewport = rectangle.width > 0 && rectangle.height > 0 && rectangle.top >= 0 && rectangle.left >= 0 && rectangle.bottom <= window.innerHeight && rectangle.right <= window.innerWidth;
    const hasFocusIndicator = active.matches(":focus-visible") || style.outlineStyle !== "none" || style.boxShadow !== "none";
    return { id: active.id, visible: inViewport && hasFocusIndicator };
  });
  if (focusAfterAdd.id !== await relationName.getAttribute("id") || !focusAfterAdd.visible) throw new Error("adding a relation did not restore visible focus to its new name field: " + JSON.stringify(focusAfterAdd));
  const relationControlID = await relationName.getAttribute("id");
  await typeByKeyboard(relationName, "Состоит из QA");
  if (await relationName.getAttribute("id") !== relationControlID) throw new Error("renaming a relation changed its stable control ID");
  const removeRelation = relationList.getByRole("button", { name: "Удалить связь Публикация с разделами QA — Состоит из QA — Категория QA", exact: true });
  await activateByKeyboard(removeRelation);
  const confirmDialog = page.getByRole("alertdialog", { name: "Удалить связь?", exact: true });
  await confirmDialog.waitFor({ state: "visible" });
  await activateByKeyboard(confirmDialog.getByRole("button", { name: "Удалить связь", exact: true }));
  await confirmDialog.waitFor({ state: "hidden" });
  await page.waitForFunction(() => document.activeElement instanceof HTMLElement && document.activeElement.id === "data-model-new-relation-from");
  const focusAfterDelete = await page.evaluate(() => {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement)) return { id: "", visible: false };
    const rectangle = active.getBoundingClientRect();
    const style = getComputedStyle(active);
    const inViewport = rectangle.width > 0 && rectangle.height > 0 && rectangle.top >= 0 && rectangle.left >= 0 && rectangle.bottom <= window.innerHeight && rectangle.right <= window.innerWidth;
    const hasFocusIndicator = active.matches(":focus-visible") || style.outlineStyle !== "none" || style.boxShadow !== "none";
    return { id: active.id, visible: inViewport && hasFocusIndicator };
  });
  if (focusAfterDelete.id !== "data-model-new-relation-from" || !focusAfterDelete.visible) throw new Error("relation deletion did not restore visible focus to the relation form: " + JSON.stringify(focusAfterDelete));
  if (await relationList.getByRole("listitem").count() !== 0) throw new Error("confirmed relation deletion retained the relation");
  if (await renamedArticle.getByRole("textbox", { name: "Название поля", exact: true }).count() !== 2) throw new Error("relation deletion unexpectedly removed model fields");

  const finalSnapshotID = await saveSnapshot();
  const finalModel = await readModel(finalSnapshotID);
  const finalArticle = finalModel?.entities?.find(entity => entity.id === savedArticle.id);
  const finalCategory = finalModel?.entities?.find(entity => entity.id === savedCategory.id);
  if (finalModel?.entities?.length !== 2 || !finalArticle || !finalCategory) throw new Error("structural save dropped or replaced an existing entity");
  const finalFields = finalArticle.fields ?? [];
  if (finalFields.length !== 2 || finalFields[0]?.id !== savedStatus.id || finalFields[0]?.name !== "Состояние" || finalFields[1]?.id !== savedTitle.id || finalFields[1]?.name !== "Заголовок") {
    throw new Error("saved field order or stable field IDs differ from the keyboard edits: " + JSON.stringify(finalFields));
  }
  if (finalModel.relations?.length) throw new Error("deleted relation was still present after structural save");
  for (const width of [360, 390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    const documentWidth = await page.evaluate(() => document.documentElement.scrollWidth);
    if (documentWidth > width + 1) throw new Error(width + "px model editor overflow: documentWidth=" + documentWidth);
  }
  if (pageErrors.length) throw new Error("browser errors: " + pageErrors.join(" | "));
  console.log("DATA_MODEL_KEYBOARD_RESULT=" + JSON.stringify({
    initialSnapshotID: initialSavedSnapshotID,
    finalSnapshotID,
    entityID: savedArticle.id,
    categoryID: savedCategory.id,
    titleFieldID: savedTitle.id,
    statusFieldID: savedStatus.id,
    relationID: relationControlID.replace("data-model-relation-name-", ""),
    focusAfterMove: focusAfterMove.id,
    focusAfterAdd: focusAfterAdd.id,
    focusAfterAddVisible: focusAfterAdd.visible,
    focusAfterDelete: focusAfterDelete.id,
    focusAfterDeleteVisible: focusAfterDelete.visible,
  }));
  await context.close();
} finally { await browser.close(); }`

	commandCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, node, "--input-type=module", "-e", script)
	command.Env = append(os.Environ(),
		"DM_PLAYWRIGHT_MODULE="+playwright,
		"DM_PLAYWRIGHT_BROWSER="+browser,
		"DM_ADMIN_BASE_URL="+strings.TrimRight(env.server.URL, "/"),
		"DM_ADMIN_TOKEN="+env.adminToken,
		"DM_ROOT_SNAPSHOT_ID="+root.ID.String(),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("data-model keyboard browser acceptance failed: %v\n%s", err, output)
	}
	const resultPrefix = "DATA_MODEL_KEYBOARD_RESULT="
	resultAt := strings.LastIndex(string(output), resultPrefix)
	if resultAt < 0 {
		t.Fatalf("browser test did not return its persistence result: %s", output)
	}
	var result dataModelKeyboardBrowserResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output)[resultAt+len(resultPrefix):])), &result); err != nil {
		t.Fatalf("decode browser test result: %v\n%s", err, output)
	}
	initialSnapshotID, err := uuid.Parse(result.InitialSnapshotID)
	if err != nil {
		t.Fatalf("parse initial child snapshot ID: %v", err)
	}
	finalSnapshotID, err := uuid.Parse(result.FinalSnapshotID)
	if err != nil {
		t.Fatalf("parse final child snapshot ID: %v", err)
	}
	if result.InitialSnapshotID == result.FinalSnapshotID {
		t.Fatalf("second structural save must create another child snapshot: %+v", result)
	}
	entityID, err := uuid.Parse(result.EntityID)
	if err != nil {
		t.Fatalf("parse persisted entity ID: %v", err)
	}
	titleFieldID, err := uuid.Parse(result.TitleFieldID)
	if err != nil {
		t.Fatalf("parse persisted title field ID: %v", err)
	}
	statusFieldID, err := uuid.Parse(result.StatusFieldID)
	if err != nil {
		t.Fatalf("parse persisted status field ID: %v", err)
	}
	if result.FocusAfterMove != "data-model-field-name-"+statusFieldID.String() ||
		result.FocusAfterAdd == "" || !result.FocusAfterAddVisible ||
		result.FocusAfterDelete != "data-model-new-relation-from" || !result.FocusAfterDeleteVisible {
		t.Fatalf("keyboard focus recovery result is inconsistent: %+v", result)
	}
	initialSnapshot, err := env.snapshots.Snapshot(ctx, initialSnapshotID)
	if err != nil {
		t.Fatalf("read initial child snapshot: %v", err)
	}
	finalSnapshot, err := env.snapshots.Snapshot(ctx, finalSnapshotID)
	if err != nil {
		t.Fatalf("read final child snapshot: %v", err)
	}
	if initialSnapshot.Snapshot.ParentID == nil || *initialSnapshot.Snapshot.ParentID != root.ID || finalSnapshot.Snapshot.ParentID == nil || *finalSnapshot.Snapshot.ParentID != initialSnapshotID {
		t.Fatalf("keyboard structural saves did not preserve snapshot ancestry: initial=%+v final=%+v", initialSnapshot.Snapshot, finalSnapshot.Snapshot)
	}
	model := finalSnapshot.Payload.Config.DataModel
	if model == nil || len(model.Entities) != 2 || len(model.Relations) != 0 {
		t.Fatalf("saved keyboard model has unexpected entities or relations: %+v", model)
	}
	var article *sitebuilder.DataModelEntity
	for index := range model.Entities {
		if model.Entities[index].ID == entityID.String() {
			article = &model.Entities[index]
			break
		}
	}
	if article == nil || article.Name != "Публикация с разделами QA" || len(article.Fields) != 2 {
		t.Fatalf("renamed entity or its fields were lost: %+v", article)
	}
	if article.Fields[0].ID != statusFieldID.String() || article.Fields[0].Name != "Состояние" || article.Fields[1].ID != titleFieldID.String() || article.Fields[1].Name != "Заголовок" {
		t.Fatalf("persisted model does not retain renamed field IDs and order: %+v", article.Fields)
	}
	branchesAfter, err := env.snapshots.Branches(ctx)
	if err != nil {
		t.Fatalf("read branch assignments after browser scenario: %v", err)
	}
	if !sameDataModelKeyboardBranchAssignments(branchesBefore, branchesAfter) {
		t.Fatalf("site model editor changed branch assignments: before=%+v after=%+v", branchesBefore, branchesAfter)
	}
	t.Logf("data-model keyboard acceptance passed: root=%s first=%s final=%s entity=%s fields=%s,%s relation=%s focus-after-move=%s focus-after-add=%s add-visible=%t focus-after-delete=%s delete-visible=%t", root.ID, initialSnapshotID, finalSnapshotID, entityID, statusFieldID, titleFieldID, result.RelationID, result.FocusAfterMove, result.FocusAfterAdd, result.FocusAfterAddVisible, result.FocusAfterDelete, result.FocusAfterDeleteVisible)
}

func requireDataModelKeyboardDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	if raw == "" || err != nil {
		t.Skip("set TEST_DATABASE_URL to a unique local website_engine_data_model_keyboard_qa_* database")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(databaseName, "website_engine_data_model_keyboard_qa_") ||
		(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") ||
		parsed.Port() == "" {
		t.Skip("data-model keyboard acceptance requires a local website_engine_data_model_keyboard_qa_* database; refusing any other target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to disposable data-model keyboard database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var sessions int
	if err := connection.QueryRow(ctx, `SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&currentDatabase, &sessions); err != nil {
		t.Fatalf("verify disposable data-model keyboard database: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("connected database %q differs from requested QA database %q", currentDatabase, databaseName)
	}
	if sessions != 0 {
		t.Fatalf("refusing integration setup: database %q has %d active sessions", currentDatabase, sessions)
	}
	t.Logf("verified disposable database %s has no other sessions before integration setup", currentDatabase)
}

func sameDataModelKeyboardBranchAssignments(before, after []sitebuilder.BranchRecord) bool {
	if len(before) != len(after) {
		return false
	}
	state := make(map[uuid.UUID]*uuid.UUID, len(before))
	for _, branch := range before {
		state[branch.ID] = branch.SnapshotID
	}
	for _, branch := range after {
		previous, exists := state[branch.ID]
		if !exists || (previous == nil) != (branch.SnapshotID == nil) {
			return false
		}
		if previous != nil && *previous != *branch.SnapshotID {
			return false
		}
	}
	return true
}
