package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const independentEditorFeatureKey = "qa-ui-bindings-browser"

type independentEditorBrowserResult struct {
	ModelSnapshotID string `json:"modelSnapshotId"`
	FinalSnapshotID string `json:"finalSnapshotId"`
}

// TestIndependentProductEditorBrowserAcceptance independently exercises the
// graphical ER editor and typed feature input mappings. Its feature and model
// are created locally for this test; it does not use Vitasil artifacts or data.
func TestIndependentProductEditorBrowserAcceptance(t *testing.T) {
	env := newTestEnv(t)
	manifest := testFeatureManifestFor(independentEditorFeatureKey, "1.0.0")
	manifest["name"] = "Independent Browser Binding Fixture"
	manifest["dataInputs"] = []any{map[string]any{
		"key": "items", "kind": "collection",
		"fields": []any{
			map[string]any{"path": "/title", "type": "text"},
			map[string]any{"path": "/quantity", "type": "integer"},
			map[string]any{"path": "/group/name", "type": "text"},
		},
	}}
	registered := uploadFeatureBuild(t, env, manifest, "export default function render() {}\n")
	if registered["key"] != independentEditorFeatureKey || registered["version"] != "1.0.0" {
		t.Fatalf("synthetic feature registry returned unexpected identity: %v", registered)
	}
	registeredManifest, ok := registered["manifest"].(map[string]any)
	if !ok {
		t.Fatalf("synthetic feature registration did not return its manifest object: %T", registered["manifest"])
	}
	registeredInputs, ok := registeredManifest["dataInputs"].([]any)
	if !ok || len(registeredInputs) != 1 {
		t.Fatalf("registered synthetic feature lost its typed dataInputs contract: %v", registeredManifest["dataInputs"])
	}
	root := saveAndDeploySnapshot(t, env, independentEditorRootConfig(), "Independent browser editor root")
	rootID := root["id"].(string)
	rootPayloadBefore := independentEditorSnapshotPayload(t, env, rootID)
	assertIndependentEditorBranchAssignments(t, env, rootID)
	assertIndependentEditorNoMedia(t, env)
	t.Log("synthetic format-2 ZIP registered in the integration fakeStore; no Vitasil assets, records, media, or S3-compatible storage are used")

	t.Run("browser", func(t *testing.T) {
		node, module, browser, missing := independentEditorBrowserDependencies()
		if missing != "" {
			t.Skipf("browser execution skipped because %s; API fixture and root snapshot setup above still ran", missing)
		}
		result := independentEditorRunBrowser(t, env, rootID, rootPayloadBefore, node, module, browser)
		if result.ModelSnapshotID == "" || result.FinalSnapshotID == "" {
			t.Fatalf("browser test did not report both saved snapshots: %+v", result)
		}

		modelSnapshotID, err := uuid.Parse(result.ModelSnapshotID)
		if err != nil {
			t.Fatalf("parse model snapshot id: %v", err)
		}
		finalSnapshotID, err := uuid.Parse(result.FinalSnapshotID)
		if err != nil {
			t.Fatalf("parse final snapshot id: %v", err)
		}
		rootUUID, err := uuid.Parse(rootID)
		if err != nil {
			t.Fatal(err)
		}
		modelSnapshot, err := env.snapshots.Snapshot(context.Background(), modelSnapshotID)
		if err != nil {
			t.Fatalf("read browser-created model snapshot: %v", err)
		}
		if modelSnapshot.Snapshot.ParentID == nil || *modelSnapshot.Snapshot.ParentID != rootUUID {
			t.Fatalf("ER editor save did not create a child of the original snapshot: %+v", modelSnapshot.Snapshot)
		}
		finalSnapshot, err := env.snapshots.Snapshot(context.Background(), finalSnapshotID)
		if err != nil {
			t.Fatalf("read browser-created bindings snapshot: %v", err)
		}
		if finalSnapshot.Snapshot.ParentID == nil || *finalSnapshot.Snapshot.ParentID != modelSnapshotID {
			t.Fatalf("valid binding save did not create an independent child of the model snapshot: %+v", finalSnapshot.Snapshot)
		}
		if !independentEditorEqualJSON(rootPayloadBefore, independentEditorSnapshotPayload(t, env, rootID)) {
			t.Fatal("browser structural edits changed the original root snapshot payload")
		}
		assertIndependentEditorBranchAssignments(t, env, rootID)
		assertIndependentEditorNoMedia(t, env)
	})
}

func independentEditorRootConfig() map[string]any {
	return map[string]any{
		"formatVersion": 3,
		"pages": []any{map[string]any{
			"id": "browser-home", "name": "Главная QA", "kind": "page", "placements": []any{},
		}},
		"routeTree": map[string]any{
			"id": "browser-root", "type": "site",
			"children": []any{map[string]any{
				"id": "browser-home-route", "type": "path", "path": "/", "pageId": "browser-home", "children": []any{},
			}},
		},
	}
}

func independentEditorBrowserDependencies() (node, module, browser, missing string) {
	node = strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	module = strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browser = strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	for _, dependency := range []struct{ name, path string }{
		{"PRERENDER_TEST_NODE", node}, {"PRERENDER_TEST_MODULE", module}, {"PRERENDER_TEST_BROWSER", browser},
	} {
		if dependency.path == "" {
			return node, module, browser, dependency.name + " is unset"
		}
		if _, err := os.Stat(dependency.path); err != nil {
			return node, module, browser, fmt.Sprintf("%s=%q is unavailable (%v)", dependency.name, dependency.path, err)
		}
	}
	return node, module, browser, ""
}

func independentEditorRunBrowser(t *testing.T, env *testEnv, rootID string, rootPayloadBefore map[string]any, node, module, browserPath string) independentEditorBrowserResult {
	t.Helper()
	outputDir := t.TempDir()
	baseURL := strings.TrimRight(env.server.URL, "/")
	rootPayloadRaw, err := json.Marshal(rootPayloadBefore)
	if err != nil {
		t.Fatalf("encode independent root payload baseline: %v", err)
	}
	adminScript := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.EDITOR_PLAYWRIGHT_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.EDITOR_PLAYWRIGHT_BROWSER, headless: true, chromiumSandbox: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
await context.addInitScript(({ token }) => localStorage.setItem("we_admin_token", token), { token: process.env.EDITOR_ADMIN_TOKEN });
const page = await context.newPage();
const pageErrors = [];
page.on("pageerror", error => pageErrors.push(error.message));
const base = process.env.EDITOR_ADMIN_BASE_URL;
const rootID = process.env.EDITOR_ROOT_SNAPSHOT_ID;
const screenshots = process.env.EDITOR_SCREENSHOT_DIR;
const metrics = [];
const stable = value => Array.isArray(value) ? value.map(stable) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, stable(value[key])])) : value;
const adminHeaders = { Authorization: "Bearer " + process.env.EDITOR_ADMIN_TOKEN };
const rootPayloadBaseline = JSON.parse(process.env.EDITOR_ROOT_PAYLOAD_BEFORE);
const readSnapshot = async id => {
  const response = await fetch(base + "/api/v2/admin/site/snapshots/" + encodeURIComponent(id), { headers: adminHeaders });
  if (!response.ok) throw new Error("snapshot detail request failed: " + response.status + " " + await response.text());
  return response.json();
};
const readBranches = async () => {
  const response = await fetch(base + "/api/v2/admin/branches", { headers: adminHeaders });
  if (!response.ok) throw new Error("branch assignment request failed: " + response.status + " " + await response.text());
  return response.json();
};
const branchSnapshot = async branchID => (await readBranches()).items?.find(item => item.id === branchID)?.snapshotId ?? null;
const assertSiteSeparation = async label => {
  const state = await page.evaluate(() => {
    const visible = element => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0;
    };
    const controls = [...document.querySelectorAll("button,input,select,[role=combobox],[role=button]")].filter(visible);
    const text = controls.map(element => [element.innerText || "", element.getAttribute("aria-label") || "", element.getAttribute("title") || ""].join(" ")).join("\n");
    return {
      branchAssignmentControls: controls.filter(element => /назначить ветке|снапшот ветки|выбрать ветку/i.test([element.innerText || "", element.getAttribute("aria-label") || "", element.getAttribute("title") || ""].join(" "))).length,
      uploadInputs: [...document.querySelectorAll('input[type="file"]')].filter(visible).length,
      uploadControls: controls.filter(element => /загрузить (файл|изображение|медиа)|upload (file|image)/i.test([element.innerText || "", element.getAttribute("aria-label") || "", element.getAttribute("title") || ""].join(" "))).length,
      recordEditors: document.querySelectorAll('[aria-label^="Записи сущности"], [data-model-record-editor], [data-model-content-editor]').length,
      publicationControls: controls.filter(element => /^(опубликовать|восстановить публикацию)$/i.test((element.innerText || element.getAttribute("aria-label") || "").trim())).length,
      visibleControlText: text,
    };
  });
  if (state.branchAssignmentControls || state.uploadInputs || state.uploadControls || state.recordEditors || state.publicationControls) {
    throw new Error(label + " contains a prohibited branch assignment, upload, or record-value control: " + JSON.stringify(state));
  }
  return state;
};
const assertResponsive = async label => {
  for (const width of [360, 390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    const metricsAtWidth = await page.evaluate(() => ({
      width: innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      activeTag: document.activeElement?.tagName ?? "",
      activeFocusVisible: Boolean(document.activeElement?.matches(":focus-visible")),
    }));
    if (metricsAtWidth.documentWidth > width + 1) throw new Error(label + " overflows at " + width + "px: " + JSON.stringify(metricsAtWidth));
    metrics.push({ screen: label, ...metricsAtWidth });
    if (width === 360 || width === 1280) await page.screenshot({ path: screenshots + "/" + label + "-" + width + ".png", fullPage: true });
  }
};
const tabTo = async locator => {
  await locator.scrollIntoViewIfNeeded();
  let focused = false;
  for (let index = 0; index < 160; index++) {
    await page.keyboard.press("Tab");
    focused = await locator.evaluate(element => document.activeElement === element);
    if (focused) break;
  }
  if (!focused) throw new Error("keyboard tab order did not reach " + await locator.innerText());
  const focus = await locator.evaluate(element => {
    const style = getComputedStyle(element);
    return { tag: element.tagName, label: element.getAttribute("aria-label") || element.innerText, focusVisible: element.matches(":focus-visible"), outlineStyle: style.outlineStyle, boxShadow: style.boxShadow };
  });
  if (!focus.focusVisible || (focus.outlineStyle === "none" && focus.boxShadow === "none")) throw new Error("keyboard focus is not visibly indicated: " + JSON.stringify(focus));
  metrics.push({ screen: "keyboard-focus", ...focus });
};
const assertFocused = async (locator, description) => {
  await locator.waitFor({ state: "visible" });
  const id = await locator.getAttribute("id");
  if (!id) throw new Error(description + " has no stable control ID for focus verification");
  try {
    await page.waitForFunction(controlID => document.activeElement?.id === controlID, id, { timeout: 5000 });
  } catch (error) {
    const activeState = await page.evaluate(() => ({
      activeControlID: document.activeElement instanceof HTMLElement ? document.activeElement.id : "",
      activeControlLabel: document.activeElement instanceof HTMLElement ? document.activeElement.getAttribute("aria-label") || document.activeElement.innerText || "" : "",
      openDialogs: [...document.querySelectorAll('[role="alertdialog"]')].filter(element => element instanceof HTMLElement && element.offsetParent !== null).map(element => element.innerText),
    }));
    throw new Error(description + " did not receive expected focus on #" + id + "; observed=" + JSON.stringify(activeState) + "; wait=" + String(error));
  }
  const focused = await locator.evaluate(element => document.activeElement === element);
  if (!focused) throw new Error(description + " did not receive focus");
  metrics.push({ screen: "focus-transition", controlID: id, description });
};
const confirmByClickAndWait = async label => {
  const dialog = page.getByRole("alertdialog");
  await dialog.getByRole("button", { name: label, exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
};
const confirmByKeyboard = async (trigger, dialogTitle, confirmLabel) => {
  await tabTo(trigger);
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("alertdialog", { name: dialogTitle, exact: true });
  await dialog.waitFor({ state: "visible" });
  const confirm = dialog.getByRole("button", { name: confirmLabel, exact: true });
  await tabTo(confirm);
  await page.keyboard.press("Enter");
  await dialog.waitFor({ state: "hidden" });
};
const selectByOptionText = async (select, predicate, description) => {
  const option = await select.locator("option").evaluateAll(options => options.map(item => ({ value: item.value, text: item.textContent?.trim() || "" }))).then(options => options.find(predicate));
  if (!option) {
    const choices = await select.locator("option").evaluateAll(options => options.map(item => ({ value: item.value, text: item.textContent?.trim() || "" })));
    throw new Error("no compatible option for " + description + "; choices=" + JSON.stringify(choices));
  }
  await select.selectOption(option.value);
  return option.text;
};
try {
  await page.goto(base + "/admin/site/data-model?snapshot=" + encodeURIComponent(rootID), { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Модель данных", exact: true }).waitFor({ timeout: 20000 });
  const modelControls = await assertSiteSeparation("/site/data-model");
  await assertResponsive("data-model-empty");

  const addEntity = page.getByRole("button", { name: "Добавить сущность", exact: true });
  await tabTo(addEntity);
  await page.keyboard.press("Enter");
  const entryCard = page.getByRole("article", { name: "Новая сущность" });
  const itemEntityName = entryCard.getByRole("textbox", { name: "Название сущности", exact: true });
  await assertFocused(itemEntityName, "new entity name after keyboard creation");
  await itemEntityName.fill("Позиция QA");
  const itemCard = page.getByRole("article", { name: "Позиция QA" });
  const addItemField = itemCard.getByRole("button", { name: "Добавить поле в сущность Позиция QA" });
  const itemFieldNames = itemCard.getByRole("textbox", { name: "Название поля", exact: true });
  const addItemFieldByKeyboard = async name => {
    const index = await itemFieldNames.count();
    await tabTo(addItemField);
    await page.keyboard.press("Enter");
    const input = itemFieldNames.nth(index);
    await assertFocused(input, "new field name after keyboard creation");
    await input.fill(name);
  };
  await addItemFieldByKeyboard("Название");
  await addItemFieldByKeyboard("Количество");
  await itemCard.getByLabel("Тип поля").nth(1).selectOption("integer");
  await confirmByClickAndWait("Изменить тип");

  await addItemFieldByKeyboard("Статус");
  await itemCard.getByLabel("Тип поля").nth(2).selectOption("enum");
  await confirmByClickAndWait("Изменить тип");
  await itemCard.getByLabel("Варианты поля Статус сущности Позиция QA", { exact: true }).fill("Новый\nОпубликован");

  await addItemFieldByKeyboard("Изображение");
  await itemCard.getByLabel("Тип поля").nth(3).selectOption("media");
  await confirmByClickAndWait("Изменить тип");

  await addItemFieldByKeyboard("Временное поле QA");
  const removeTemporaryField = itemCard.getByRole("button", { name: "Удалить поле Временное поле QA из сущности Позиция QA", exact: true });
  await confirmByKeyboard(removeTemporaryField, "Удалить поле?", "Удалить поле");
  await assertFocused(itemFieldNames.nth(3), "preceding field after confirmed field deletion");
  if (await itemFieldNames.count() !== 4) throw new Error("confirmed deletion did not remove only the temporary field");

  const addTemporaryEntity = page.getByRole("button", { name: "Добавить сущность", exact: true });
  await tabTo(addTemporaryEntity);
  await page.keyboard.press("Enter");
  const temporaryEntityCard = page.getByRole("article", { name: "Новая сущность" });
  const temporaryEntityName = temporaryEntityCard.getByRole("textbox", { name: "Название сущности", exact: true });
  await assertFocused(temporaryEntityName, "new entity name after keyboard creation");
  await temporaryEntityName.fill("Временная сущность QA");
  const temporaryEntity = page.getByRole("article", { name: "Временная сущность QA" });
  const removeTemporaryEntity = temporaryEntity.getByRole("button", { name: "Удалить сущность Временная сущность QA", exact: true });
  await confirmByKeyboard(removeTemporaryEntity, "Удалить сущность?", "Удалить сущность");
  await assertFocused(itemCard.getByRole("textbox", { name: "Название сущности", exact: true }), "surviving entity after confirmed entity deletion");
  if (await page.locator('section[aria-label="Сущности модели, список"]').getByRole("article").count() !== 1) throw new Error("confirmed deletion removed a fixture entity or retained the temporary entity");

  const addAnotherEntity = page.getByRole("button", { name: "Добавить сущность", exact: true });
  await addAnotherEntity.waitFor({ state: "visible", timeout: 10000 });
  await tabTo(addAnotherEntity);
  await page.keyboard.press("Enter");
  const secondEntityCard = page.getByRole("article", { name: "Новая сущность" });
  const groupEntityName = secondEntityCard.getByRole("textbox", { name: "Название сущности", exact: true });
  await assertFocused(groupEntityName, "new entity name after keyboard creation");
  await groupEntityName.fill("Группа QA");
  const groupCard = page.getByRole("article", { name: "Группа QA" });
  const addGroupField = groupCard.getByRole("button", { name: "Добавить поле в сущность Группа QA" });
  await tabTo(addGroupField);
  await page.keyboard.press("Enter");
  const groupFieldName = groupCard.getByRole("textbox", { name: "Название поля", exact: true });
  await assertFocused(groupFieldName, "new field name after keyboard creation");
  await groupFieldName.fill("Название");

  const newRelationForm = () => page.getByRole("group", { name: "Новая связь", exact: true });
  await newRelationForm().waitFor({ state: "visible" });
  await newRelationForm().getByLabel("От сущности").selectOption({ label: "Позиция QA" });
  await newRelationForm().getByLabel("К сущности").selectOption({ label: "Группа QA" });
  await newRelationForm().getByLabel("Тип связи").selectOption("one-to-one");
  await newRelationForm().getByLabel("Название связи").fill("Группа");
  const connect = newRelationForm().getByRole("button", { name: "Связать", exact: true });
  await connect.waitFor({ state: "visible" });
  if (await connect.isDisabled()) throw new Error("ER relation button remained disabled after selecting two entities");
  await connect.press("Enter");
  const relationList = page.getByRole("list", { name: "Связи модели" });
  const relationNames = relationList.getByRole("textbox", { name: "Название связи Позиция QA — Группа QA", exact: true });
  await relationNames.nth(0).waitFor();
  if (await relationNames.nth(0).inputValue() !== "Группа") throw new Error("ER editor did not add the one-to-one relation name");
  await assertFocused(relationNames.nth(0), "new relation name after keyboard creation");

  const duplicateName = newRelationForm().getByLabel("Название связи");
  await duplicateName.fill("Группа");
  const duplicateAlert = page.locator("#data-model-new-relation-error");
  await duplicateAlert.waitFor({ state: "visible" });
  if ((await duplicateAlert.innerText()).trim() !== "Такая связь уже есть. Измените название или направление.") throw new Error("duplicate relation validation showed an unexpected error");
  const duplicateAlertID = await duplicateAlert.getAttribute("id");
  for (const control of [
    newRelationForm().getByLabel("От сущности"),
    newRelationForm().getByLabel("К сущности"),
    duplicateName,
  ]) {
    if (await control.getAttribute("aria-invalid") !== "true" || await control.getAttribute("aria-describedby") !== duplicateAlertID) {
      throw new Error("duplicate relation error was not associated with all relevant controls");
    }
  }
  if (!await newRelationForm().getByRole("button", { name: "Связать", exact: true }).isDisabled()) throw new Error("duplicate relation remained savable");
  if (await relationList.getByRole("listitem").count() !== 1) throw new Error("duplicate relation validation added an extra relation");

  await duplicateName.fill("Группы");
  await newRelationForm().getByLabel("Тип связи").selectOption("one-to-many");
  await newRelationForm().getByRole("button", { name: "Связать", exact: true }).press("Enter");
  await relationNames.nth(1).waitFor();
  if (await relationNames.nth(1).inputValue() !== "Группы") throw new Error("ER editor did not add the one-to-many relation name");
  await assertFocused(relationNames.nth(1), "new relation name after keyboard creation");
  await newRelationForm().getByLabel("Тип связи").selectOption("many-to-many");
  await newRelationForm().getByLabel("Название связи").fill("Метки");
  await newRelationForm().getByRole("button", { name: "Связать", exact: true }).press("Enter");
  await relationNames.nth(2).waitFor();
  if (await relationNames.nth(2).inputValue() !== "Метки") throw new Error("ER editor did not add the many-to-many relation name");
  await assertFocused(relationNames.nth(2), "new relation name after keyboard creation");

  await newRelationForm().getByLabel("Название связи").fill("Удаляемая QA");
  await newRelationForm().getByRole("button", { name: "Связать", exact: true }).press("Enter");
  await relationNames.nth(3).waitFor();
  await assertFocused(relationNames.nth(3), "new relation name after keyboard creation");
  const removeTemporaryRelation = relationList.getByRole("button", { name: "Удалить связь Позиция QA — Удаляемая QA — Группа QA", exact: true });
  await confirmByKeyboard(removeTemporaryRelation, "Удалить связь?", "Удалить связь");
  await assertFocused(relationNames.nth(2), "preceding relation after confirmed relation deletion");
  if (await relationList.getByRole("listitem").count() !== 3) throw new Error("confirmed deletion did not remove only the temporary relation");

  const graphToggle = page.getByRole("button", { name: "ER-граф", exact: true });
  await graphToggle.press("Enter");
  const graph = page.locator('[aria-label="Граф связей модели данных"]');
  await graph.waitFor({ state: "visible" });
  const graphPaths = await graph.locator("svg path").count();
  if (graphPaths < 4) throw new Error("ER graph did not render the three created relation paths");
  const graphControls = await assertSiteSeparation("/site/data-model with ER graph");
  await assertResponsive("data-model-graph");
  await page.getByRole("button", { name: "Карточки модели", exact: true }).press("Enter");

  const modelSaveResponsePromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v2/admin/site/snapshots");
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const modelSaveResponse = await modelSaveResponsePromise;
  if (!modelSaveResponse.ok()) throw new Error("ER model save failed: " + modelSaveResponse.status() + " " + await modelSaveResponse.text());
  const modelSnapshot = await modelSaveResponse.json();
  const modelSnapshotID = modelSnapshot.id;
  if (!modelSnapshotID || modelSnapshotID === rootID) throw new Error("ER model save did not create a child snapshot");
  await page.waitForFunction(id => new URL(location.href).searchParams.get("snapshot") === id, modelSnapshotID);
  await page.getByRole("article", { name: "Позиция QA" }).getByRole("heading", { name: "Позиция QA", exact: true }).waitFor();
  const persistedModelDetail = await readSnapshot(modelSnapshotID);
  const modelParentID = persistedModelDetail.snapshot?.parentSnapshotId;
  const modelConfig = persistedModelDetail.payload?.config;
  const modelData = modelConfig?.dataModel;
  const itemEntity = modelData?.entities?.find(entity => entity.name === "Позиция QA");
  const groupEntity = modelData?.entities?.find(entity => entity.name === "Группа QA");
  const relations = modelData?.relations?.filter(relation => relation.fromEntityId === itemEntity?.id && relation.toEntityId === groupEntity?.id) ?? [];
  const relationTypes = new Map(relations.map(relation => [relation.name, relation.cardinality]));
  const expectedRelations = [["Группа", "one-to-one"], ["Группы", "one-to-many"], ["Метки", "many-to-many"]];
  const enumField = itemEntity?.fields?.find(field => field.name === "Статус");
  if (modelParentID !== rootID || !itemEntity?.fields?.some(field => field.name === "Название" && field.type === "text") || !itemEntity?.fields?.some(field => field.name === "Количество" && field.type === "integer") || enumField?.type !== "enum" || JSON.stringify(enumField?.options) !== JSON.stringify(["Новый", "Опубликован"]) || !itemEntity?.fields?.some(field => field.name === "Изображение" && field.type === "media") || !groupEntity?.fields?.some(field => field.name === "Название" && field.type === "text") || expectedRelations.some(([name, cardinality]) => relationTypes.get(name) !== cardinality) || relations.length !== 3) {
    throw new Error("saved model snapshot omitted the ER entity, field, relation, or parent link: " + JSON.stringify({ modelParentID, modelData }));
  }
  const rootAfterModel = await readSnapshot(rootID);
  if (JSON.stringify(stable(rootAfterModel.payload)) !== JSON.stringify(stable(rootPayloadBaseline))) throw new Error("original root payload changed while saving the model");
  if (await branchSnapshot(process.env.EDITOR_DEV_BRANCH_ID) !== rootID) throw new Error("saving model structure changed the development branch assignment");

  await page.getByRole("link", { name: "Канвас", exact: true }).click();
  const openHome = page.getByRole("button", { name: "Открыть страницу Главная QA", exact: true });
  await openHome.waitFor({ timeout: 20000 });
  await openHome.press("Enter");
  await page.getByRole("heading", { name: "Главная QA", exact: true }).waitFor();
  const buildSelect = page.getByLabel("Фича и версия сборки");
  try {
    await page.waitForFunction(version => [...document.querySelectorAll("select")].some(select => [...select.options].some(option => option.textContent?.trim().startsWith(version))), "v1.0.0", { timeout: 15000 });
  } catch {
    const [snapshotDetail, registryResponse] = await Promise.all([
      readSnapshot(modelSnapshotID),
      fetch(base + "/api/v2/admin/feature-builds", { headers: adminHeaders }),
    ]);
    const registry = registryResponse.ok ? await registryResponse.json() : { status: registryResponse.status, error: await registryResponse.text() };
    throw new Error("page editor feature registry did not load its build option: " + JSON.stringify({
      snapshotGet: { parentSnapshotId: snapshotDetail.snapshot?.parentSnapshotId, config: snapshotDetail.payload?.config },
      featureBuildRegistryStatus: registryResponse.status,
      registeredFeature: registry.items?.find(item => item.key === process.env.EDITOR_FEATURE_KEY) ?? null,
      registry,
      pageText: (await page.locator("body").innerText()).slice(-2400),
      pageErrors,
    }));
  }
  const buildOptions = await buildSelect.locator("option").evaluateAll(options => options.map(item => ({ value: item.value, text: item.textContent?.trim() || "" })));
  const syntheticBuildOption = buildOptions.find(option => option.text.startsWith("v1.0.0"));
  if (!syntheticBuildOption?.value) throw new Error("synthetic build option has no registry ID value: " + JSON.stringify(buildOptions));
  await buildSelect.selectOption(syntheticBuildOption.value);
  const selectedBuildID = await buildSelect.inputValue();
  const selectedBuildLabel = await buildSelect.locator("option:checked").textContent();
  if (selectedBuildID !== syntheticBuildOption.value) throw new Error("feature picker did not retain selected build ID: " + JSON.stringify({ selectedBuildID, syntheticBuildOption }));
  await page.getByRole("button", { name: "Добавить фичу", exact: true }).click();
  const entitySelect = page.getByLabel("Сущность модели");
  try {
    await entitySelect.waitFor({ timeout: 5000 });
  } catch {
    const [snapshotDetail, registryResponse] = await Promise.all([
      readSnapshot(modelSnapshotID),
      fetch(base + "/api/v2/admin/feature-builds", { headers: adminHeaders }),
    ]);
    const registry = registryResponse.ok ? await registryResponse.json() : { status: registryResponse.status, error: await registryResponse.text() };
    const registeredBuild = registry.items?.find(item => item.id === selectedBuildID) ?? null;
    const renderState = await page.evaluate(() => ({
      selectLabels: [...document.querySelectorAll("select")].map(element => ({ label: element.getAttribute("aria-label"), text: element.closest("label")?.innerText ?? "", options: [...element.options].map(option => ({ value: option.value, text: option.textContent?.trim() })) })),
      bodyTail: document.body.innerText.slice(-2400),
    }));
    throw new Error("adding the synthetic feature did not render its model binding editor: " + JSON.stringify({
      selectedBuildID,
      selectedBuildLabel,
      featureKey: registeredBuild?.key ?? null,
      registeredManifestDataInputs: registeredBuild?.manifest?.dataInputs ?? null,
      snapshotGet: { parentSnapshotId: snapshotDetail.snapshot?.parentSnapshotId, config: snapshotDetail.payload?.config },
      featureBuildRegistryStatus: registryResponse.status,
      renderState,
      pageErrors,
    }));
  }
  const noEntityError = page.locator("#data-input-items-entity-error");
  await noEntityError.waitFor({ timeout: 5000 });
  if (!(await noEntityError.innerText()).includes("Выберите сущность")) throw new Error("missing entity selection did not show inline validation");

  await selectByOptionText(entitySelect, option => option.text.startsWith("Группа QA ·"), "incompatible group entity");
  const quantitySelect = page.getByLabel("Поле модели для items → quantity", { exact: true });
  await quantitySelect.waitFor();
  await page.waitForFunction(() => document.querySelector("#data-input-items-mapping-1-error")?.textContent?.includes("Нет совместимых полей"));
  if (await quantitySelect.getAttribute("aria-invalid") !== "true") throw new Error("incompatible entity target was not marked invalid on its typed input field");
  const invalidConfigSavePromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v2/admin/site/snapshots");
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const invalidConfigSave = await invalidConfigSavePromise;
  if (invalidConfigSave.status() !== 400) throw new Error("invalid typed binding should be rejected atomically with HTTP 400, got " + invalidConfigSave.status() + " " + await invalidConfigSave.text());

  await selectByOptionText(entitySelect, option => option.text.startsWith("Позиция QA ·"), "compatible item entity");
  const titleSelect = page.getByLabel("Поле модели для items → title", { exact: true });
  const categoryNameSelect = page.getByLabel("Поле модели для items → group → name", { exact: true });
  await titleSelect.waitFor();
  await categoryNameSelect.waitFor();
  await selectByOptionText(titleSelect, option => option.value !== "", "title field");
  await selectByOptionText(quantitySelect, option => option.value !== "", "integer quantity field");
  const relationFieldLabel = await selectByOptionText(categoryNameSelect, option => option.text.includes("связь «Группа»"), "field over the typed one-to-one relation");
  const editorControls = await assertSiteSeparation("/site/page editor with feature bindings");
  await assertResponsive("page-editor-bindings");

  const modelParentBeforeBindings = await readSnapshot(modelSnapshotID);
  const modelParentPayloadBefore = JSON.stringify(stable(modelParentBeforeBindings.payload));
  const bindingSaveResponsePromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v2/admin/site/snapshots");
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const bindingSaveResponse = await bindingSaveResponsePromise;
  if (!bindingSaveResponse.ok()) throw new Error("valid typed binding save failed: " + bindingSaveResponse.status() + " " + await bindingSaveResponse.text());
  const finalSnapshot = await bindingSaveResponse.json();
  const finalSnapshotID = finalSnapshot.id;
  if (!finalSnapshotID || finalSnapshotID === modelSnapshotID) throw new Error("valid binding save did not create an independent child snapshot");
  await page.waitForFunction(id => new URL(location.href).searchParams.get("snapshot") === id, finalSnapshotID);

  const finalDetail = await readSnapshot(finalSnapshotID);
  const finalPlacement = finalDetail.payload?.config?.pages?.flatMap(page => page.placements ?? []).find(placement => placement.build?.featureKey === process.env.EDITOR_FEATURE_KEY);
  const finalBindings = finalPlacement?.dataBindings?.find(binding => binding.inputKey === "items");
  if (!finalPlacement || !finalBindings || finalBindings.entityId !== modelData.entities.find(entity => entity.name === "Позиция QA")?.id) throw new Error("saved binding child omitted the selected entity mapping: " + JSON.stringify(finalPlacement));
  if (finalBindings.fields?.length !== 3) throw new Error("saved binding child did not retain each typed field mapping: " + JSON.stringify(finalBindings));
  const nestedMapping = finalBindings.fields.find(mapping => mapping.inputPath === "/group/name");
  if (!nestedMapping || !nestedMapping.relationIds?.length) throw new Error("typed nested input did not save its directed ER relation path: " + JSON.stringify(finalBindings));
  const parentAfterBindings = await readSnapshot(modelSnapshotID);
  if (JSON.stringify(stable(parentAfterBindings.payload)) !== modelParentPayloadBefore) throw new Error("valid binding save mutated its parent snapshot payload");
  if (await branchSnapshot(process.env.EDITOR_DEV_BRANCH_ID) !== rootID) throw new Error("valid model or binding save changed the assigned branch snapshot");
  const prodBranch = await branchSnapshot(process.env.EDITOR_PROD_BRANCH_ID);
  if (prodBranch !== null) throw new Error("site editor changed the independent production branch assignment: " + prodBranch);

  const finalSiteControls = await assertSiteSeparation("saved /site page editor");
  if (pageErrors.length) throw new Error("browser page runtime errors: " + pageErrors.join(" | "));
  console.log("EDITOR_BROWSER_RESULT=" + JSON.stringify({ modelSnapshotId: modelSnapshotID, finalSnapshotId: finalSnapshotID, nestedRelationField: relationFieldLabel, metrics, separation: { modelControls, graphControls, editorControls, finalSiteControls }, pageErrors }));
} finally {
  await context.close();
  await browser.close();
}`
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", adminScript)
	command.Env = append(os.Environ(),
		"EDITOR_PLAYWRIGHT_MODULE="+module,
		"EDITOR_PLAYWRIGHT_BROWSER="+browserPath,
		"EDITOR_ADMIN_BASE_URL="+baseURL,
		"EDITOR_ADMIN_TOKEN="+env.adminToken,
		"EDITOR_ROOT_SNAPSHOT_ID="+rootID,
		"EDITOR_ROOT_PAYLOAD_BEFORE="+string(rootPayloadRaw),
		"EDITOR_DEV_BRANCH_ID="+string(env.devBranch),
		"EDITOR_PROD_BRANCH_ID="+string(env.prodBranch),
		"EDITOR_FEATURE_KEY="+independentEditorFeatureKey,
		"EDITOR_SCREENSHOT_DIR="+outputDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("independent graphical model editor browser acceptance failed: %v\n%s", err, output)
	}
	const resultPrefix = "EDITOR_BROWSER_RESULT="
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, resultPrefix) {
			continue
		}
		var result independentEditorBrowserResult
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, resultPrefix)), &result); err != nil {
			t.Fatalf("decode independent editor browser result: %v\n%s", err, output)
		}
		t.Logf("independent /site browser assertions passed; temporary screenshots in %s:\n%s", outputDir, output)
		return result
	}
	t.Fatalf("independent editor browser did not report acceptance results:\n%s", output)
	return independentEditorBrowserResult{}
}

func independentEditorSnapshotPayload(t *testing.T, env *testEnv, id string) map[string]any {
	t.Helper()
	status, detail, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshots/"+id, env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read independent editor snapshot %s: status=%d body=%v", id, status, detail)
	}
	payload, ok := detail["payload"].(map[string]any)
	if !ok {
		t.Fatalf("independent editor snapshot %s has unexpected payload %T: %v", id, detail["payload"], detail)
	}
	return payload
}

func assertIndependentEditorBranchAssignments(t *testing.T, env *testEnv, expectedDevelopment string) {
	t.Helper()
	development, err := env.snapshots.Branch(context.Background(), uuid.MustParse(string(env.devBranch)))
	if err != nil {
		t.Fatalf("read disposable development assignment: %v", err)
	}
	if development.SnapshotID == nil || development.SnapshotID.String() != expectedDevelopment {
		t.Fatalf("editor saves changed the development branch assignment: got %v want %s", development.SnapshotID, expectedDevelopment)
	}
	production, err := env.snapshots.Branch(context.Background(), uuid.MustParse(string(env.prodBranch)))
	if err != nil {
		t.Fatalf("read disposable production assignment: %v", err)
	}
	if production.SnapshotID != nil {
		t.Fatalf("editor saves assigned an unassigned production branch: %v", production.SnapshotID)
	}
}

func assertIndependentEditorNoMedia(t *testing.T, env *testEnv) {
	t.Helper()
	var mediaObjects int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM media_objects`).Scan(&mediaObjects); err != nil {
		t.Fatalf("count disposable media objects: %v", err)
	}
	if mediaObjects != 0 {
		t.Fatalf("UI-only editor acceptance unexpectedly created %d media records", mediaObjects)
	}
}

func independentEditorEqualJSON(left, right any) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}
