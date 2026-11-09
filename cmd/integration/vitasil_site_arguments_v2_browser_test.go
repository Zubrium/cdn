package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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

// TestVitasilSiteArgumentsV2BrowserAcceptance exercises the site-editor
// arguments and typed data-input binding flow using current format-2 Vitasil
// ZIPs. A small format-2 fixture supplies an array argument because the
// current Vitasil manifests do not declare one. The fixture does not add or
// change any Vitasil feature behavior or ABI.
func TestVitasilSiteArgumentsV2BrowserAcceptance(t *testing.T) {
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	node := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	module := strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browser := strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	if featureDir == "" || node == "" || module == "" || browser == "" {
		t.Skip("set TEST_FEATURE_DIR and PRERENDER_TEST_NODE/MODULE/BROWSER for the Vitasil site arguments browser acceptance")
	}
	requireSiteArgumentsV2BrowserDatabase(t)

	env := newTestEnv(t)
	manifests := make(map[string]sitebuilder.Manifest, 2)
	for _, key := range []string{"catalog", "background"} {
		archive, version := readExternalFeatureArchive(t, featureDir, key)
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify current Vitasil %s ZIP: %v", key, err)
		}
		manifest := verified.Manifest
		if manifest.Key != key || manifest.Version != version || manifest.FormatVersion != 2 || manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
			t.Fatalf("current Vitasil %s ZIP has unexpected contract: key=%q version=%q format=%d ABI=%q", key, manifest.Key, manifest.Version, manifest.FormatVersion, manifest.RuntimeABI())
		}
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register current Vitasil %s@%s ZIP: status=%d body=%v", key, version, status, body)
		}
		if body["key"] != key || body["version"] != version {
			t.Fatalf("registered build identity differs from Vitasil ZIP manifest for %s@%s: %v", key, version, body)
		}
		manifests[key] = manifest
	}

	arrayManifest := testFeatureManifestFor("site-arguments-array-fixture", "1.0.0")
	arrayManifest["name"] = "Array arguments fixture"
	arrayManifest["arguments"] = objectSchema(map[string]any{
		"cards": map[string]any{
			"type":  "array",
			"title": "Карточки",
			"items": objectSchema(map[string]any{
				"label":   map[string]any{"type": "string", "title": "Подпись"},
				"enabled": map[string]any{"type": "boolean", "title": "Доступен"},
			}, "label", "enabled"),
		},
	}, "cards")
	arrayBuild := modelAcceptanceUploadFeatureBuild(t, env, arrayManifest, "export default function Feature() { return null; }")
	arrayVersion := "1.0.0"
	if arrayBuild["key"] != "site-arguments-array-fixture" || arrayBuild["version"] != arrayVersion {
		t.Fatalf("registered array-argument fixture has unexpected identity: %v", arrayBuild)
	}

	productPort := modelAcceptancePort(t, manifests["catalog"], "products")
	model, product, productFieldIDs, productFieldKeys := modelAcceptanceEntity("product", "Product", productPort, true)
	alternateFieldIDs := make(map[string]string, len(productPort.Fields))
	for _, declared := range productPort.Fields {
		var source sitebuilder.DataModelField
		for _, field := range product.Fields {
			if field.ID == productFieldIDs[declared.Path] {
				source = field
				break
			}
		}
		if source.ID == "" {
			t.Fatalf("Vitasil catalog field %q was not represented in the Product fixture", declared.Path)
		}
		alternate := source
		alternate.ID = uuid.NewString()
		alternate.Key = "mapped_" + source.Key
		alternate.Name = "Сопоставление " + source.Name
		alternate.Unique = false
		model.Entities[0].Fields = append(model.Entities[0].Fields, alternate)
		alternateFieldIDs[declared.Path] = alternate.ID
	}

	media := env.uploadMedia("site-arguments-background.png", minimalPNG(t))
	mediaID, ok := media["id"].(string)
	if !ok || mediaID == "" {
		t.Fatalf("isolated media upload did not return an ID: %v", media)
	}
	if _, err := uuid.Parse(mediaID); err != nil {
		t.Fatalf("isolated media object has a non-system ID %q: %v", mediaID, err)
	}

	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Features: []sitebuilder.BuildRef{
			{FeatureKey: "catalog", Version: manifests["catalog"].Version},
			{FeatureKey: "background", Version: manifests["background"].Version},
			{FeatureKey: "site-arguments-array-fixture", Version: arrayVersion},
		},
		DataModel: &model,
		Pages: []sitebuilder.Page{{
			ID: "vitasil-catalog", Name: "Vitasil Catalog QA", Kind: "collection-list",
			Placements: []sitebuilder.Placement{
				{ID: "vitasil-catalog-placement", Build: sitebuilder.BuildRef{FeatureKey: "catalog", Version: manifests["catalog"].Version}, Slot: "main", Arguments: map[string]sitebuilder.ArgumentBinding{
					"title":  {Source: "literal", Value: json.RawMessage(`"Начальный каталог"`)},
					"action": {Source: "literal", Value: json.RawMessage(`{"label":"Начальный текст","to":"/"}`)},
				}, DataBindings: []sitebuilder.DataInputBinding{{InputKey: "products", EntityID: product.ID, Fields: modelAcceptanceFieldBindings(productPort, productFieldIDs)}}},
				{ID: "vitasil-background-placement", Build: sitebuilder.BuildRef{FeatureKey: "background", Version: manifests["background"].Version}, Slot: "background"},
				{ID: "array-arguments-placement", Build: sitebuilder.BuildRef{FeatureKey: "site-arguments-array-fixture", Version: arrayVersion}, Slot: "main", Arguments: map[string]sitebuilder.ArgumentBinding{"cards": {Source: "literal", Value: json.RawMessage(`[]`)}}},
			},
		}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "catalog-route", Type: "path", Path: "/", PageID: "vitasil-catalog"}}},
	}
	configRaw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(configRaw, &configMap); err != nil {
		t.Fatal(err)
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Vitasil site arguments v2 browser acceptance")
	snapshotID, err := uuid.Parse(snapshot["id"].(string))
	if err != nil {
		t.Fatalf("parse isolated snapshot ID: %v", err)
	}
	branchID := uuid.MustParse(string(env.devBranch))
	branchBefore, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil || branchBefore.SnapshotID == nil || *branchBefore.SnapshotID != snapshotID {
		t.Fatalf("isolated branch does not point to the test snapshot before the browser flow: branch=%+v err=%v", branchBefore, err)
	}

	values := modelAcceptanceValues(productFieldKeys, map[string]any{
		"/slug": "rosehip", "/title": "Шиповник: отдельное значение контента", "/category": "Напитки",
		"/price": "100 ₽", "/imageSrc": mediaID, "/description": "Значение записи, не аргумент сайта",
	})
	if _, err := env.snapshots.SaveModelContentWorkspace(context.Background(), modelAcceptanceActor(t, env), snapshotID, sitebuilder.ModelContentDraftInput{
		Revision: 0,
		Records:  []sitebuilder.ModelContentRecord{{EntityID: product.ID, SortOrder: 0, Values: values}},
	}); err != nil {
		t.Fatalf("seed an isolated Product record for the /content boundary check: %v", err)
	}

	// The real Vitasil background argument requires image renditions. Keep its
	// normal preparation-and-polling UI path intact while a disposable worker
	// consumes only this test environment's rendition queue.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				_, _ = env.media.ProcessOne(workerCtx)
			}
		}
	}()
	defer func() {
		stopWorker()
		<-workerDone
	}()

	modelAcceptanceRunVitasilSiteArgumentsV2Browser(t, env, snapshotID.String(), mediaID, alternateFieldIDs, node, module, browser)

	childID := siteArgumentsV2BrowserSnapshotID(t, env, snapshotID)
	if childID == snapshotID {
		t.Fatal("saving structural site arguments or data-input mapping did not create a child snapshot")
	}
	childDetail, err := env.snapshots.Snapshot(context.Background(), childID)
	if err != nil {
		t.Fatalf("read child snapshot after browser save: %v", err)
	}
	if childDetail.Snapshot.ParentID == nil || *childDetail.Snapshot.ParentID != snapshotID {
		t.Fatalf("site editor save did not preserve snapshot ancestry: parent=%v expected=%s", childDetail.Snapshot.ParentID, snapshotID)
	}
	childContent, err := env.snapshots.ModelContentWorkspace(context.Background(), childID)
	if err != nil {
		t.Fatalf("read inherited content workspace on the structural child snapshot: %v", err)
	}
	if len(childContent.Records) != 1 || childContent.Records[0].Values[productFieldKeys["/title"]] != "Шиповник: отдельное значение контента" {
		t.Fatalf("structural site save did not preserve independent inherited content values: %+v", childContent.Records)
	}
	branchAfter, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil || branchAfter.SnapshotID == nil || *branchAfter.SnapshotID != snapshotID || branchAfter.Revision != branchBefore.Revision {
		t.Fatalf("site editor save changed branch assignment outside /snapshots/branches: before=%+v after=%+v err=%v", branchBefore, branchAfter, err)
	}
	t.Logf("browser acceptance used current Vitasil catalog@%s and background@%s ZIPs; child snapshot %s; branch assignment stayed on parent %s", manifests["catalog"].Version, manifests["background"].Version, childID, snapshotID)
}

func modelAcceptanceRunVitasilSiteArgumentsV2Browser(t *testing.T, env *testEnv, snapshotID, mediaID string, alternateFieldIDs map[string]string, node, module, browserPath string) {
	t.Helper()
	const browserScript = `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
const context = await browser.newContext({ viewport: { width: 1440, height: 1100 } });
await context.addInitScript(token => localStorage.setItem("we_admin_token", token), process.env.SITE_ARGUMENTS_ADMIN_TOKEN);
const page = await context.newPage();
const errors = [];
const uploadRequests = [];
page.on("pageerror", error => errors.push(error.message));
page.on("request", request => {
  const url = new URL(request.url());
  if (!["POST", "PUT"].includes(request.method())) return;
  if (url.pathname === "/api/v1/admin/media" || url.pathname.startsWith("/api/v2/admin/files/uploads")) uploadRequests.push(request.method() + " " + url.pathname);
});
const base = process.env.SITE_ARGUMENTS_ADMIN_BASE_URL;
const initialSnapshotID = process.env.SITE_ARGUMENTS_INITIAL_SNAPSHOT_ID;
const mediaID = process.env.SITE_ARGUMENTS_MEDIA_ID;
const alternateFieldIDs = JSON.parse(process.env.SITE_ARGUMENTS_ALTERNATE_FIELDS);
const state = async snapshotID => page.evaluate(async ({ token, id }) => {
  const response = await fetch("/api/v2/admin/site/snapshots/" + encodeURIComponent(id), { headers: { Authorization: "Bearer " + token } });
  if (!response.ok) throw new Error("snapshot detail failed: " + response.status);
  return response.json();
}, { token: process.env.SITE_ARGUMENTS_ADMIN_TOKEN, id: snapshotID });
try {
  await page.goto(base + "/admin/site/pages?snapshot=" + encodeURIComponent(initialSnapshotID), { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "Открыть страницу Vitasil Catalog QA", exact: true }).click();
  await page.getByRole("heading", { name: "Vitasil Catalog QA", exact: true }).waitFor({ timeout: 20000 });
  await page.getByRole("button", { name: "Каталог продукции", exact: true }).click();
  await page.getByRole("textbox", { name: /^Заголовок каталога/ }).fill("Каталог Vitasil из редактора сайта");
  await page.getByRole("textbox", { name: /^Кнопка внизу каталога · Надпись/ }).fill("Связаться");
  await page.getByRole("textbox", { name: /^Кнопка внизу каталога · Адрес/ }).fill("/contacts");

  await page.locator("#data-input-products-entity").selectOption({ label: "Product · product" });
  const mappingLabels = [
    "Поле модели для products → slug",
    "Поле модели для products → title",
    "Поле модели для products → category",
    "Поле модели для products → price",
    "Поле модели для products → imageSrc",
    "Поле модели для products → description",
  ];
  const mappedValues = [];
  for (let index = 0; index < mappingLabels.length; index += 1) {
    const select = page.getByLabel(mappingLabels[index], { exact: true });
    const mappedFieldID = alternateFieldIDs["/" + ["slug", "title", "category", "price", "imageSrc", "description"][index]];
    const options = await select.locator("option").all();
    let selectedOption;
    for (const candidate of options) {
      const label = (await candidate.innerText()).trim();
      if (label.startsWith("Product → Сопоставление ") && await candidate.getAttribute("value") === "|" + mappedFieldID) {
        selectedOption = candidate;
        break;
      }
    }
    if (!selectedOption) throw new Error("typed mapping option for " + mappingLabels[index] + " was missing; candidate text=" + await select.locator("option").allInnerTexts());
    await select.selectOption(await selectedOption.getAttribute("value"));
    mappedValues.push(await select.inputValue());
  }
  if (mappedValues.some((value, index) => value !== "|" + alternateFieldIDs["/" + ["slug", "title", "category", "price", "imageSrc", "description"][index]])) {
    throw new Error("typed data-input fields did not retain the requested compatible model fields: " + JSON.stringify(mappedValues));
  }

  await page.getByRole("button", { name: "Фон страницы", exact: true }).click();
  await page.getByRole("button", { name: "Изображение фона", exact: true }).click();
  const picker = page.getByRole("dialog");
  await picker.getByRole("heading", { name: "Выбрать медиа", exact: true }).waitFor({ timeout: 10000 });
  await picker.getByLabel("Поиск медиа", { exact: true }).fill("site-arguments-background.png");
  const existingFile = picker.getByRole("button").filter({ hasText: "site-arguments-background.png" }).first();
  await existingFile.waitFor({ timeout: 10000 });
  await existingFile.click();
  if (await picker.locator('input[type="file"]').count() !== 0) throw new Error("site media picker exposed a direct upload input");
  const libraryLink = picker.getByRole("link", { name: "Открыть медиатеку", exact: true });
  if (await libraryLink.getAttribute("href") !== "/admin/media" || await libraryLink.getAttribute("target") !== "_blank") throw new Error("media picker does not link to the separate media library");
  await picker.getByRole("button", { name: "Выбрать", exact: true }).click();
  await picker.waitFor({ state: "hidden", timeout: 30000 });

  await page.getByRole("button", { name: "Array arguments fixture", exact: true }).click();
  await page.getByRole("button", { name: "Добавить элемент в Карточки", exact: true }).click();
  await page.getByRole("textbox", { name: /^Карточки · элемент 1 · Подпись/ }).fill("Первый элемент");
  await page.getByLabel(/^Карточки · элемент 1 · Доступен/).selectOption("true");
  await page.getByRole("button", { name: "Добавить элемент в Карточки", exact: true }).click();
  await page.getByRole("textbox", { name: /^Карточки · элемент 2 · Подпись/ }).fill("Второй элемент");
  await page.getByLabel(/^Карточки · элемент 2 · Доступен/).selectOption("false");
  await page.getByRole("button", { name: "Переместить Карточки · элемент 2 выше", exact: true }).click();

  const siteBoundary = await page.evaluate(() => ({
    fileInputs: document.querySelectorAll('input[type="file"]').length,
    branchAssignments: [...document.querySelectorAll("button")].filter(button => /назначить ветке/i.test(button.textContent || "")).length,
    branchControls: [...document.querySelectorAll('[role="combobox"]')].filter(control => /ветк/i.test(control.getAttribute("aria-label") || "")).length,
    contentScreens: [...document.querySelectorAll("body *")].filter(element => element.children.length === 0 && /^(Записи размещений|Опубликовать|Добавить запись)$/.test((element.textContent || "").trim())).length,
    recordValues: [...document.querySelectorAll("input,textarea")].some(control => control.value === "Шиповник: отдельное значение контента"),
    mediaUploadLinks: [...document.querySelectorAll('a[href="/admin/media"]')].length,
  }));
  if (siteBoundary.fileInputs || siteBoundary.branchAssignments || siteBoundary.branchControls || siteBoundary.contentScreens || siteBoundary.recordValues) {
    throw new Error("/site exposed upload, content-editing, publication, or branch-assignment controls: " + JSON.stringify(siteBoundary));
  }

  const saveResponsePromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v2/admin/site/snapshots");
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const saveResponse = await saveResponsePromise;
  if (!saveResponse.ok()) throw new Error("saving site arguments or bindings failed: " + saveResponse.status() + " " + await saveResponse.text());
  const created = await saveResponse.json();
  const childSnapshotID = created.id;
  if (!childSnapshotID || childSnapshotID === initialSnapshotID) throw new Error("site structural save did not create a child snapshot: " + JSON.stringify(created));
  await page.waitForFunction(id => new URL(location.href).searchParams.get("snapshot") === id, childSnapshotID, { timeout: 20000 });

  let detail = await state(childSnapshotID);
  const parentDetail = await state(initialSnapshotID);
  const config = detail.payload?.config;
  const pageConfig = config?.pages?.find(item => item.id === "vitasil-catalog");
  const placements = pageConfig?.placements || [];
  const catalog = placements.find(item => item.build?.featureKey === "catalog");
  const background = placements.find(item => item.build?.featureKey === "background");
  const arrayFeature = placements.find(item => item.build?.featureKey === "site-arguments-array-fixture");
  const catalogArgs = catalog?.arguments || {};
  const cards = arrayFeature?.arguments?.cards?.value;
  const binding = catalog?.dataBindings?.find(item => item.inputKey === "products");
  const persistedMappings = Object.fromEntries((binding?.fields || []).map(item => [item.inputPath, item.fieldId]));
  const cardSentinel = Array.isArray(cards) && cards.length === 2 && cards[0]?.label === "Второй элемент" && cards[0]?.enabled === false && cards[1]?.label === "Первый элемент" && cards[1]?.enabled === true;
  const mappingMismatch = Object.entries(alternateFieldIDs).some(([path, fieldID]) => persistedMappings[path] !== fieldID) || Object.keys(persistedMappings).length !== Object.keys(alternateFieldIDs).length;
  if (catalogArgs.title?.value !== "Каталог Vitasil из редактора сайта" ||
      catalogArgs.action?.value?.label !== "Связаться" ||
      catalogArgs.action?.value?.to !== "/contacts" ||
      background?.arguments?.imageSrc?.value !== mediaID ||
      !cardSentinel || mappingMismatch) {
    throw new Error("saved site argument values or typed data-input mapping differ from browser edits: " + JSON.stringify({ catalogArgs, background: background?.arguments, cards, persistedMappings, alternateFieldIDs }));
  }
  if (parentDetail.payload?.config?.pages?.[0]?.placements?.some(item => item.arguments?.title?.value === "Каталог Vitasil из редактора сайта")) {
    throw new Error("site editor changed the parent snapshot instead of creating an independent child");
  }

  await page.reload({ waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "Открыть страницу Vitasil Catalog QA", exact: true }).click();
  await page.getByRole("button", { name: "Каталог продукции", exact: true }).click();
  if (await page.getByRole("textbox", { name: /^Заголовок каталога/ }).inputValue() !== "Каталог Vitasil из редактора сайта") throw new Error("nested Vitasil arguments did not reappear after reloading the saved child snapshot");
  for (const [label, expected] of [
    ["Кнопка внизу каталога · Надпись", "Связаться"],
    ["Кнопка внизу каталога · Адрес", "/contacts"],
  ]) {
    if (await page.getByRole("textbox", { name: new RegExp("^" + label) }).inputValue() !== expected) throw new Error("nested Vitasil argument " + label + " did not reappear after reload");
  }
  for (const [index, label] of ["slug", "title", "category", "price", "imageSrc", "description"].entries()) {
    const fieldID = alternateFieldIDs["/" + label];
    const select = page.getByLabel(mappingLabels[index], { exact: true });
    if (await select.inputValue() !== "|" + fieldID) throw new Error("typed data-input mapping did not reappear after reload for " + mappingLabels[index]);
  }
  await page.getByRole("button", { name: "Фон страницы", exact: true }).click();
  const restoredMediaControl = page.getByRole("button", { name: "Изображение фона", exact: true });
  if (!(await restoredMediaControl.innerText()).includes(mediaID)) throw new Error("selected existing media did not reappear after reloading the child snapshot");
  await page.getByRole("button", { name: "Array arguments fixture", exact: true }).click();
  if (await page.getByRole("textbox", { name: /^Карточки · элемент 1 · Подпись/ }).inputValue() !== "Второй элемент" ||
      await page.getByLabel(/^Карточки · элемент 1 · Доступен/).inputValue() !== "false" ||
      await page.getByRole("textbox", { name: /^Карточки · элемент 2 · Подпись/ }).inputValue() !== "Первый элемент") {
    throw new Error("nested array arguments or their order did not reappear after reload");
  }
  if (uploadRequests.length) throw new Error("/site sent a file upload request instead of selecting existing media: " + JSON.stringify(uploadRequests));

  await page.goto(base + "/admin/content?snapshot=" + encodeURIComponent(childSnapshotID), { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor({ timeout: 20000 });
  await page.getByRole("navigation", { name: "Сущности" }).getByRole("button", { name: /Product/ }).waitFor({ timeout: 20000 });
  const productRecord = page.getByRole("list", { name: "Записи сущности Product" }).getByRole("listitem").filter({ hasText: "Шиповник: отдельное значение контента" });
  await productRecord.waitFor({ timeout: 20000 });
  await productRecord.getByRole("button").first().click();
  await page.getByRole("textbox", { name: "Название", exact: true }).waitFor({ timeout: 10000 });
  const contentBoundary = await page.evaluate(() => ({
    recordForms: [...document.querySelectorAll("input")].some(input => input.value === "Шиповник: отдельное значение контента"),
    branchAssignments: [...document.querySelectorAll("button")].filter(button => /назначить ветке/i.test(button.textContent || "")).length,
    uploadInputs: document.querySelectorAll('input[type="file"]').length,
  }));
  if (!contentBoundary.recordForms || contentBoundary.branchAssignments || contentBoundary.uploadInputs) throw new Error("/content did not show the inherited Product record separately from site structure: " + JSON.stringify(contentBoundary));
  if (errors.length) throw new Error("browser page errors: " + errors.join(" | "));
  console.log(JSON.stringify({ childSnapshotID, siteBoundary, contentBoundary, uploadRequests, errors }));
} finally {
  await context.close();
  await browser.close();
}`
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", browserScript)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browserPath,
		"SITE_ARGUMENTS_ADMIN_BASE_URL="+strings.TrimRight(env.server.URL, "/"),
		"SITE_ARGUMENTS_ADMIN_TOKEN="+env.adminToken,
		"SITE_ARGUMENTS_INITIAL_SNAPSHOT_ID="+snapshotID,
		"SITE_ARGUMENTS_MEDIA_ID="+mediaID,
		"SITE_ARGUMENTS_ALTERNATE_FIELDS="+siteArgumentsV2JSON(t, alternateFieldIDs),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Vitasil site arguments and data-input browser acceptance failed: %v\n%s", err, output)
	}
	t.Logf("Vitasil site arguments browser metrics:\n%s", output)
}

func siteArgumentsV2JSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func siteArgumentsV2BrowserSnapshotID(t *testing.T, env *testEnv, parentID uuid.UUID) uuid.UUID {
	t.Helper()
	items, err := env.snapshots.List(context.Background(), 100, nil)
	if err != nil {
		t.Fatalf("list snapshots after site editor save: %v", err)
	}
	for _, item := range items {
		if item.ParentID != nil && *item.ParentID == parentID && item.Name == "Vitasil site arguments v2 browser acceptance · изменение" {
			return item.ID
		}
	}
	// The UI derives the child name from the parent title, which is a stable
	// user-facing contract; report available children if that contract changes.
	var children []string
	for _, item := range items {
		if item.ParentID != nil && *item.ParentID == parentID {
			children = append(children, item.Name+" ("+item.ID.String()+")")
		}
	}
	t.Fatalf("could not find the child snapshot created by the site arguments save; children=%v", children)
	return uuid.Nil
}

func requireSiteArgumentsV2BrowserDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	databaseName := ""
	if err == nil && parsed != nil {
		databaseName = strings.TrimPrefix(parsed.Path, "/")
	}
	if raw == "" || err != nil || parsed == nil || !strings.HasPrefix(databaseName, "website_engine_vitasil_site_arguments_browser_qa_") || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Skip("set TEST_DATABASE_URL to a local, uniquely named website_engine_vitasil_site_arguments_browser_qa_* disposable database; this acceptance truncates that database")
	}
	if parsed.Port() == "" {
		t.Fatalf("disposable Vitasil site-arguments database URL must specify its port: %s", parsed.Redacted())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to disposable Vitasil site-arguments database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var sessions int
	if err := connection.QueryRow(ctx, `SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&currentDatabase, &sessions); err != nil {
		t.Fatalf("verify disposable Vitasil site-arguments database: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("connected database %q differs from requested QA database %q", currentDatabase, databaseName)
	}
	if sessions != 0 {
		t.Fatalf("refusing integration setup: disposable database %q already has %d other active sessions", databaseName, sessions)
	}
	t.Logf("verified disposable database %s on port %s has no other active sessions before integration setup", currentDatabase, parsed.Port())
}
