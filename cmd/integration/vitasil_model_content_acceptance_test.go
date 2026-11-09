package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

func TestVitasilAllCurrentDataModelBuildsAccepted(t *testing.T) {
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	if featureDir == "" {
		t.Skip("set TEST_FEATURE_DIR to the isolated current Vitasil feature ZIP directory")
	}
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	keys := []string{"background", "catalog", "catalog-detail", "footer", "form", "header", "hero", "main-data", "not-found"}
	for _, key := range keys {
		archive := modelAcceptanceReadArchive(t, featureDir, key)
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify current Vitasil %s ZIP: %v", key, err)
		}
		if verified.Manifest.Key != key || verified.Manifest.FormatVersion != 2 || verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule || verified.Manifest.DataInputs == nil {
			t.Fatalf("unexpected Vitasil %s data model contract: key=%q format=%d ABI=%q dataInputs=%#v", key, verified.Manifest.Key, verified.Manifest.FormatVersion, verified.Manifest.RuntimeABI(), verified.Manifest.DataInputs)
		}
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register current Vitasil %s ZIP: status=%d body=%v", key, status, body)
		}
		if body["key"] != key || body["version"] != verified.Manifest.Version {
			t.Fatalf("Engine registered a different Vitasil build for %s: %v", key, body)
		}
		t.Logf("registered current Vitasil archive %s@%s", key, verified.Manifest.Version)
	}
}

// This acceptance registers the current Vitasil catalog, detail, and homepage
// ZIPs and verifies that all three placements consume the same Product records.
// The homepage slider is intentionally limited to an ordered subset.
func TestVitasilSharedProductModelContentAcceptance(t *testing.T) {
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	if featureDir == "" {
		t.Skip("set TEST_FEATURE_DIR to the isolated current Vitasil feature ZIP directory")
	}
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)

	manifests := map[string]sitebuilder.Manifest{}
	for _, key := range []string{"catalog", "catalog-detail", "main-data"} {
		archive := modelAcceptanceReadArchive(t, featureDir, key)
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify current Vitasil %s ZIP: %v", key, err)
		}
		if verified.Manifest.Key != key || verified.Manifest.FormatVersion != 2 || verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
			t.Fatalf("unexpected Vitasil %s manifest contract: key=%q format=%d ABI=%q", key, verified.Manifest.Key, verified.Manifest.FormatVersion, verified.Manifest.RuntimeABI())
		}
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register current Vitasil %s ZIP: status=%d body=%v", key, status, body)
		}
		manifests[key] = verified.Manifest
		t.Logf("registered current Vitasil archive %s@%s", key, verified.Manifest.Version)
	}

	productPort := modelAcceptancePort(t, manifests["catalog"], "products")
	model, productEntity, productPathIDs, productPathKeys := modelAcceptanceEntity("product", "Товар", productPort, true)
	entityByInput := map[string]sitebuilder.DataModelEntity{
		"product": productEntity,
	}
	fieldIDsByInput := map[string]map[string]string{"product": productPathIDs}
	fieldKeysByInput := map[string]map[string]string{"product": productPathKeys}
	for _, key := range []string{"catalog-detail", "main-data"} {
		portKey := "product"
		if key == "main-data" {
			portKey = "products"
		}
		port := modelAcceptancePort(t, manifests[key], portKey)
		modelAcceptanceAssertSameOutputShape(t, productPort, port, key+"."+portKey)
		entityByInput[key+"."+portKey] = productEntity
		fieldIDsByInput[key+"."+portKey] = productPathIDs
		fieldKeysByInput[key+"."+portKey] = productPathKeys
	}
	for _, portKey := range []string{"info", "advantages", "slider"} {
		port := modelAcceptancePort(t, manifests["main-data"], portKey)
		entityName := map[string]string{"info": "Информация главной", "advantages": "Преимущества", "slider": "Слайдер"}[portKey]
		_, entity, ids, keys := modelAcceptanceEntity("main_data_"+portKey, entityName, port, false)
		model.Entities = append(model.Entities, entity)
		entityByInput["main-data."+portKey] = entity
		fieldIDsByInput["main-data."+portKey] = ids
		fieldKeysByInput["main-data."+portKey] = keys
	}

	mainBindings := make([]sitebuilder.DataInputBinding, 0, len(manifests["main-data"].DataInputs))
	for _, port := range manifests["main-data"].DataInputs {
		inputKey := "main-data." + port.Key
		entity := entityByInput[inputKey]
		fields := modelAcceptanceFieldBindings(port, fieldIDsByInput[inputKey])
		mainBindings = append(mainBindings, sitebuilder.DataInputBinding{InputKey: port.Key, EntityID: entity.ID, Fields: fields})
	}
	catalogBinding := sitebuilder.DataInputBinding{
		InputKey: "products", EntityID: productEntity.ID,
		Fields: modelAcceptanceFieldBindings(productPort, productPathIDs),
	}
	detailPort := modelAcceptancePort(t, manifests["catalog-detail"], "product")
	detailBinding := sitebuilder.DataInputBinding{
		InputKey: "product", EntityID: productEntity.ID,
		Fields: modelAcceptanceFieldBindings(detailPort, productPathIDs),
		Lookup: &sitebuilder.DataInputLookupBinding{Argument: "slug", FieldID: productPathIDs["/slug"]},
	}

	catalogArguments := map[string]sitebuilder.ArgumentBinding{
		"title":      {Source: "literal", Value: json.RawMessage(`"Каталог"`)},
		"action":     {Source: "literal", Value: json.RawMessage(`{"label":"Задать вопрос","to":"/contacts"}`)},
	}
	detailArguments := map[string]sitebuilder.ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}}
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		DataModel:     &model,
		Pages: []sitebuilder.Page{
			{ID: "home", Name: "Главная", Kind: "page", Placements: []sitebuilder.Placement{{
				ID: "home-data", Build: sitebuilder.BuildRef{FeatureKey: "main-data", Version: manifests["main-data"].Version}, Slot: "main", DataBindings: mainBindings,
			}}},
			{ID: "catalog", Name: "Каталог", Kind: "collection-list", Placements: []sitebuilder.Placement{{
				ID: "catalog-products", Build: sitebuilder.BuildRef{FeatureKey: "catalog", Version: manifests["catalog"].Version}, Slot: "main", Arguments: catalogArguments,
				DataBindings: []sitebuilder.DataInputBinding{catalogBinding},
				DetailLink:   &sitebuilder.DetailLink{RouteID: "product-detail-route", Field: "slug", PathTemplate: "/catalog/{slug}"},
			}}},
			{ID: "product", Name: "Товар", Kind: "detail", Placements: []sitebuilder.Placement{{
				ID: "product-detail", Build: sitebuilder.BuildRef{FeatureKey: "catalog-detail", Version: manifests["catalog-detail"].Version}, Slot: "main", Arguments: detailArguments,
				DataBindings: []sitebuilder.DataInputBinding{detailBinding},
			}}},
		},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{
			{ID: "home-route", Type: "path", Path: "/", PageID: "home"},
			{ID: "catalog-route", Type: "path", Path: "/catalog", PageID: "catalog"},
			{ID: "product-detail-route", Type: "path", Path: "/catalog/:slug", PageID: "product"},
		}},
	}

	configRaw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(configRaw, &configMap); err != nil {
		t.Fatal(err)
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Vitasil shared Product model")
	snapshotID := snapshot["id"].(string)
	base := "/api/v2/admin/content/snapshots/" + snapshotID + "/model"

	userStatus, userBody, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken, map[string]any{"name": "Vitasil model editor", "role": "user"})
	if userStatus != http.StatusCreated {
		t.Fatalf("create Vitasil model editor token: status=%d body=%v", userStatus, userBody)
	}
	userToken := userBody["token"].(string)

	productRecord := func(order int, slug, title string) sitebuilder.ModelContentRecord {
		values := map[string]any{
			productPathKeys["/slug"]: slug, productPathKeys["/title"]: title,
			productPathKeys["/category"]: "Напитки", productPathKeys["/price"]: "100 ₽",
			productPathKeys["/description"]: title + " описание",
		}
		return sitebuilder.ModelContentRecord{EntityID: productEntity.ID, SortOrder: order, Values: values}
	}
	infoEntity := entityByInput["main-data.info"]
	sliderEntity := entityByInput["main-data.slider"]
	advantagesEntity := entityByInput["main-data.advantages"]
	initialRecords := []sitebuilder.ModelContentRecord{
		productRecord(0, "pear", "Груша"), productRecord(1, "apple", "Яблоко"), productRecord(2, "plum", "Слива"),
		{EntityID: infoEntity.ID, SortOrder: 0, Values: modelAcceptanceValues(fieldKeysByInput["main-data.info"], map[string]any{"/title": "Натуральная забота"})},
		{EntityID: sliderEntity.ID, SortOrder: 0, Values: modelAcceptanceValues(fieldKeysByInput["main-data.slider"], map[string]any{
			"/eyebrow": "Ассортимент", "/title": "Популярные продукты", "/previousLabel": "Назад", "/nextLabel": "Вперёд",
			"/itemsLabel": "Позиции", "/catalogActionLabel": "Весь каталог", "/catalogActionTo": "/catalog",
		})},
		{EntityID: advantagesEntity.ID, SortOrder: 0, Values: modelAcceptanceValues(fieldKeysByInput["main-data.advantages"], map[string]any{
			"/icon": "quality", "/title": "Качество", "/description": "Натуральные ингредиенты",
		})},
	}
	status, firstDraft, _ := env.request(http.MethodPut, base, userToken, sitebuilder.ModelContentDraftInput{Revision: 0, Records: initialRecords})
	if status != http.StatusOK || firstDraft["revision"] != float64(1) {
		t.Fatalf("save Vitasil model records: status=%d body=%v", status, firstDraft)
	}

	workspaceRecords, ok := firstDraft["records"].([]any)
	if !ok {
		t.Fatalf("model API response omitted records: %v", firstDraft)
	}
	var savedRecords []sitebuilder.ModelContentRecord
	productIDsBySlug := map[string]string{}
	var infoID, sliderID string
	for _, raw := range workspaceRecords {
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var record sitebuilder.ModelContentRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			t.Fatal(err)
		}
		savedRecords = append(savedRecords, record)
		switch record.EntityID {
		case productEntity.ID:
			productIDsBySlug[fmt.Sprint(record.Values[productPathKeys["/slug"]])] = record.ID
		case infoEntity.ID:
			infoID = record.ID
		case sliderEntity.ID:
			sliderID = record.ID
		}
	}
	if len(productIDsBySlug) != 3 || infoID == "" || sliderID == "" {
		t.Fatalf("model content response omitted shared Product or main-data record IDs: %v", firstDraft["records"])
	}
	selections := []sitebuilder.ModelContentSelection{
		{PlacementID: "home-data", InputKey: "info", Mode: "ids", RecordIDs: []string{infoID}},
		{PlacementID: "home-data", InputKey: "slider", Mode: "ids", RecordIDs: []string{sliderID}},
		{PlacementID: "home-data", InputKey: "products", Mode: "ids", RecordIDs: []string{productIDsBySlug["plum"], productIDsBySlug["pear"]}},
	}
	status, selectedDraft, _ := env.request(http.MethodPut, base, userToken, sitebuilder.ModelContentDraftInput{Revision: 1, Records: savedRecords, Selections: selections})
	if status != http.StatusOK || selectedDraft["revision"] != float64(2) {
		t.Fatalf("save Vitasil slider selection: status=%d body=%v", status, selectedDraft)
	}
	status, published, _ := env.request(http.MethodPost, base+"/publish", env.adminToken, map[string]any{"revision": 2})
	if status != http.StatusOK || published["publishedRevision"] != float64(2) {
		t.Fatalf("publish Vitasil model records: status=%d body=%v", status, published)
	}

	home := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/")
	homeProducts := modelAcceptanceContentList(t, home, "products")
	modelAcceptanceAssertTitles(t, homeProducts, []string{"Слива", "Груша"})
	catalog := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/catalog")
	catalogProducts := modelAcceptanceContentList(t, catalog, "products")
	modelAcceptanceAssertTitles(t, catalogProducts, []string{"Груша", "Яблоко", "Слива"})
	detail := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/catalog/apple")
	detailProduct := modelAcceptanceContentObject(t, detail, "product")
	if detailProduct["slug"] != "apple" || detailProduct["title"] != "Яблоко" {
		t.Fatalf("Vitasil detail route did not resolve the shared Product model record: %v", detailProduct)
	}
	if productIDsBySlug["pear"] == productIDsBySlug["apple"] || productIDsBySlug["apple"] == productIDsBySlug["plum"] {
		t.Fatal("Vitasil catalog records must retain independent generated identities")
	}
	modelAcceptanceRunAdminBrowser(t, env, snapshotID)
	t.Log("Vitasil catalog, detail, and homepage slider use one published Product entity; catalog lists all three while slider uses its ordered two-record selection")
}

func modelAcceptanceRunAdminBrowser(t *testing.T, env *testEnv, snapshotID string) {
	t.Helper()
	node := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	module := strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browser := strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	if node == "" || module == "" || browser == "" {
		t.Log("admin browser acceptance not run: set PRERENDER_TEST_NODE, PRERENDER_TEST_MODULE, and PRERENDER_TEST_BROWSER")
		return
	}
	outputDir := strings.TrimSpace(os.Getenv("ADMIN_UI_TEST_OUTPUT_DIR"))
	if outputDir == "" {
		outputDir = t.TempDir()
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatalf("create disposable admin screenshot directory: %v", err)
	}
	baseURL := strings.TrimRight(env.server.URL, "/")
	adminScript := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
await context.addInitScript(({ token }) => localStorage.setItem("we_admin_token", token), { token: process.env.MODEL_ADMIN_TOKEN });
const page = await context.newPage();
const pageErrors = [];
page.on("pageerror", error => pageErrors.push(error.message));
const base = process.env.MODEL_ADMIN_BASE_URL;
const snapshot = encodeURIComponent(process.env.MODEL_ADMIN_SNAPSHOT_ID);
const screenshots = process.env.MODEL_ADMIN_SCREENSHOT_DIR;
const metrics = [];
try {
  await page.goto(base + "/admin/site/data-model?snapshot=" + snapshot, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Модель данных", exact: true }).waitFor({ timeout: 20000 });
	  await page.getByRole("heading", { name: "Товар", exact: true }).waitFor({ timeout: 20000 });
  await page.getByRole("button", { name: "ER-граф", exact: true }).click();
	  await page.locator('[aria-label="Граф связей модели данных"]').waitFor({ state: "visible" });
  for (const width of [360, 390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    const current = await page.evaluate(() => ({
      width: innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      graphWidth: document.querySelector('[aria-label="Граф связей модели данных"]')?.clientWidth,
      graphScrollWidth: document.querySelector('[aria-label="Граф связей модели данных"]')?.scrollWidth,
      branchControls: [...document.querySelectorAll('[role="combobox"]')].filter(el => /ветк/i.test(el.getAttribute('aria-label') || '')).length,
      branchAssignments: [...document.querySelectorAll('button')].filter(el => /назначить ветке/i.test(el.textContent || '')).length,
    }));
    if (current.documentWidth > width + 1) throw new Error(width + "px model editor document overflow: " + JSON.stringify(current));
    if (current.branchControls || current.branchAssignments) throw new Error("branch assignment leaked into /site: " + JSON.stringify(current));
    metrics.push({ screen: "model", ...current });
    if (width === 1280 || width === 360) await page.screenshot({ path: screenshots + "/data-model-" + width + ".png", fullPage: true });
  }

  await page.getByRole("button", { name: "Добавить сущность", exact: true }).click();
  const newEntityCard = page.getByRole("article", { name: "Новая сущность" });
  await newEntityCard.getByLabel("Название сущности").fill("Категория QA");
  const categoryCard = page.getByRole("article", { name: "Категория QA" });
  await categoryCard.getByRole("button", { name: "Добавить поле в сущность Категория QA" }).click();
  await categoryCard.getByLabel("Название поля").fill("Название");
  await page.getByLabel(/^От сущности/).selectOption({ label: "Товар" });
  await page.getByLabel(/^К сущности/).selectOption({ label: "Категория QA" });
  await page.getByLabel(/^Тип связи/).selectOption("one-to-many");
  await page.getByLabel(/^Название связи/).fill("Категория");
  await page.getByRole("button", { name: "Связать", exact: true }).click();
  const snapshotResponsePromise = page.waitForResponse(response =>
    response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v2/admin/site/snapshots");
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const snapshotResponse = await snapshotResponsePromise;
  if (!snapshotResponse.ok()) throw new Error("model structure save failed: " + await snapshotResponse.text());
  const createdSnapshot = await snapshotResponse.json();
  const contentSnapshotID = createdSnapshot.id;
  if (!contentSnapshotID || contentSnapshotID === process.env.MODEL_ADMIN_SNAPSHOT_ID) throw new Error("model structure save did not create a child snapshot");
  await page.waitForFunction(id => new URL(location.href).searchParams.get("snapshot") === id, contentSnapshotID);
  await categoryCard.getByRole("heading", { name: "Категория QA", exact: true }).waitFor();

  const persistedModel = await page.evaluate(async ({ token, snapshotID }) => {
    const response = await fetch("/api/v2/admin/site/snapshots/" + snapshotID, { headers: { Authorization: "Bearer " + token } });
    if (!response.ok) throw new Error("saved model snapshot request failed: " + response.status);
    const detail = await response.json();
    return detail.payload?.config?.dataModel ?? null;
  }, { token: process.env.MODEL_ADMIN_TOKEN, snapshotID: contentSnapshotID });
  if (!persistedModel?.entities?.some(entity => entity.name === "Категория QA") || !persistedModel?.relations?.some(relation => relation.name === "Категория")) {
    throw new Error("saved child snapshot omitted the entity or relation created in the ER editor");
  }

  await page.goto(base + "/admin/content?snapshot=" + encodeURIComponent(contentSnapshotID), { waitUntil: "domcontentloaded" });
  await page.getByText("Контент снапшота", { exact: true }).waitFor({ timeout: 20000 });
  const entityNavigation = page.getByRole("navigation", { name: "Сущности" });
  const productEntity = entityNavigation.getByRole("button", { name: /Товар/ });
  await productEntity.waitFor({ timeout: 20000 });
  const categoryEntity = entityNavigation.getByRole("button", { name: /Категория QA/ });
  await categoryEntity.waitFor({ timeout: 20000 });
  await page.getByText("Записи размещений", { exact: true }).waitFor({ timeout: 20000 });

  await categoryEntity.click();
  await page.getByRole("button", { name: "Добавить", exact: true }).click();
  await page.getByRole("textbox", { name: "Название", exact: true }).fill("Напитки");
  const modelContentPath = "/api/v2/admin/content/snapshots/" + contentSnapshotID + "/model";
  const categorySavePromise = page.waitForResponse(response => response.request().method() === "PUT" && new URL(response.url()).pathname === modelContentPath);
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const categorySave = await categorySavePromise;
  if (!categorySave.ok()) throw new Error("category record draft save failed: " + await categorySave.text());

  await productEntity.click();
  const productRecords = page.getByRole("list", { name: "Записи сущности Товар" });
  const firstProductRow = productRecords.getByRole("listitem").first();
  await firstProductRow.waitFor({ timeout: 20000 });
  const firstProductLabel = (await firstProductRow.getByRole("button").first().innerText()).trim();
  if (!firstProductLabel.includes("Груша")) throw new Error("content list did not prefer the human product title over its technical slug: " + firstProductLabel);
  await firstProductRow.getByRole("button").first().click();
  const categoryRelation = page.getByRole("group", { name: /Категория/ });
  await categoryRelation.getByLabel("Напитки", { exact: true }).check();
  await page.getByRole("textbox", { name: "Название", exact: true }).fill("Груша QA");
  const productSavePromise = page.waitForResponse(response => response.request().method() === "PUT" && new URL(response.url()).pathname === modelContentPath);
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const productSave = await productSavePromise;
  if (!productSave.ok()) throw new Error("product record and relation draft save failed: " + await productSave.text());
  await page.getByRole("button", { name: "Опубликовать", exact: true }).click();
  const publishPromise = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === modelContentPath + "/publish");
  await page.getByRole("alertdialog").getByRole("button", { name: "Опубликовать", exact: true }).click();
  const publishResponse = await publishPromise;
  if (!publishResponse.ok()) throw new Error("model content publication failed: " + await publishResponse.text());
  await page.getByRole("alertdialog").waitFor({ state: "hidden", timeout: 5000 });
  const publishedModel = await page.evaluate(async ({ token, snapshotID }) => {
    const headers = { Authorization: "Bearer " + token };
    const [snapshotResponse, contentResponse] = await Promise.all([
      fetch("/api/v2/admin/site/snapshots/" + snapshotID, { headers }),
      fetch("/api/v2/admin/content/snapshots/" + snapshotID + "/model", { headers }),
    ]);
    if (!snapshotResponse.ok || !contentResponse.ok) throw new Error("could not read published snapshot model/content state");
    const [detail, content] = await Promise.all([snapshotResponse.json(), contentResponse.json()]);
    const model = detail.payload?.config?.dataModel;
    const productEntity = model?.entities?.find(entity => entity.name === "Товар");
    const categoryEntity = model?.entities?.find(entity => entity.name === "Категория QA");
    const titleField = productEntity?.fields?.find(field => field.name === "Название");
    const categoryNameField = categoryEntity?.fields?.find(field => field.name === "Название");
    const relation = model?.relations?.find(item => item.name === "Категория" && item.fromEntityId === productEntity?.id && item.toEntityId === categoryEntity?.id);
    const category = content.records?.find(record => record.entityId === categoryEntity?.id && record.values?.[categoryNameField?.key] === "Напитки");
    const pear = content.records?.find(record => record.entityId === productEntity?.id && record.values?.[titleField?.key] === "Груша QA");
    return {
      revision: content.revision,
      publishedRevision: content.publishedRevision,
      categoryFound: Boolean(category),
      productFound: Boolean(pear),
      relationTargets: relation && pear ? pear.relations?.[relation.id] ?? [] : [],
      categoryID: category?.id ?? null,
    };
  }, { token: process.env.MODEL_ADMIN_TOKEN, snapshotID: contentSnapshotID });
  if (publishedModel.revision !== publishedModel.publishedRevision || !publishedModel.categoryFound || !publishedModel.productFound || !publishedModel.categoryID || !publishedModel.relationTargets.includes(publishedModel.categoryID)) {
    throw new Error("published model values or relationship were not persisted atomically: " + JSON.stringify(publishedModel));
  }

  const productTitle = page.getByRole("textbox", { name: "Название", exact: true });
  await productTitle.fill("Груша autosave QA");
  const autosavePromise = page.waitForResponse(response => response.request().method() === "PUT" && new URL(response.url()).pathname === modelContentPath);
  const autosaveResponse = await autosavePromise;
  if (!autosaveResponse.ok()) throw new Error("model content autosave failed: " + await autosaveResponse.text());
  await page.getByText("Черновик сохранён", { exact: true }).waitFor({ timeout: 5000 });
  const autosavedRevision = await page.evaluate(async ({ token, snapshotID }) => {
    const response = await fetch("/api/v2/admin/content/snapshots/" + snapshotID + "/model", { headers: { Authorization: "Bearer " + token } });
    if (!response.ok) throw new Error("autosaved model workspace request failed: " + response.status);
    const content = await response.json();
    const modelResponse = await fetch("/api/v2/admin/site/snapshots/" + snapshotID, { headers: { Authorization: "Bearer " + token } });
    if (!modelResponse.ok) throw new Error("autosaved snapshot request failed: " + modelResponse.status);
    const model = (await modelResponse.json()).payload?.config?.dataModel;
    const product = model?.entities?.find(entity => entity.name === "Товар");
    const titleField = product?.fields?.find(field => field.name === "Название");
    const record = content.records?.find(item => item.entityId === product?.id && item.values?.[titleField?.key] === "Груша autosave QA");
    if (!record || content.revision <= content.publishedRevision) throw new Error("autosave did not preserve a draft-only model revision: " + JSON.stringify({ revision: content.revision, publishedRevision: content.publishedRevision, found: Boolean(record) }));
    return content.revision;
  }, { token: process.env.MODEL_ADMIN_TOKEN, snapshotID: contentSnapshotID });
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Контент сайта", exact: true }).waitFor({ timeout: 20000 });
  await page.getByRole("list", { name: "Записи сущности Товар" }).getByRole("listitem").filter({ hasText: "Груша autosave QA" }).waitFor({ timeout: 20000 });
  await page.getByRole("list", { name: "Записи сущности Товар" }).getByRole("listitem").filter({ hasText: "Груша autosave QA" }).getByRole("button").first().click();
  if (await page.getByRole("textbox", { name: "Название", exact: true }).inputValue() !== "Груша autosave QA") throw new Error("autosaved model content was lost after reloading the editor");
  console.log(JSON.stringify({ autosavedRevision, publishedRevision: publishedModel.publishedRevision }));

  for (const width of [360, 390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    const current = await page.evaluate(() => ({
      width: innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      branchControls: [...document.querySelectorAll('[role="combobox"]')].filter(el => /ветк/i.test(el.getAttribute('aria-label') || '')).length,
      featureEditors: [...document.querySelectorAll('input,textarea,select')].filter(el => /content\.schema|схема фич/i.test((el.getAttribute('aria-label') || '') + " " + (el.getAttribute('name') || ''))).length,
    }));
    if (current.documentWidth > width + 1) throw new Error(width + "px content editor document overflow: " + JSON.stringify(current));
    if (current.branchControls || current.featureEditors) throw new Error("/content contains a branch control or feature-schema editor: " + JSON.stringify(current));
    metrics.push({ screen: "content", ...current });
    if (width === 360 || width === 1280) await page.screenshot({ path: screenshots + "/model-content-" + width + ".png", fullPage: true });
  }

  await page.goto(base + "/admin/snapshots/branches", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Ветки", exact: true }).waitFor({ timeout: 20000 });
  await page.getByRole("combobox", { name: /Снапшот ветки/ }).first().waitFor();
  await page.getByRole("button", { name: "Назначить", exact: true }).first().waitFor();
  const assignedSnapshotID = await page.evaluate(async ({ token, branchID }) => {
    const response = await fetch("/api/v2/admin/branches", { headers: { Authorization: "Bearer " + token } });
    if (!response.ok) throw new Error("branch assignments request failed: " + response.status);
    const result = await response.json();
    return result.items?.find(item => item.id === branchID)?.snapshotId ?? null;
  }, { token: process.env.MODEL_ADMIN_TOKEN, branchID: process.env.MODEL_ADMIN_BRANCH_ID });
  if (assignedSnapshotID !== process.env.MODEL_ADMIN_SNAPSHOT_ID) throw new Error("saving model structure changed the branch assignment");
  await page.screenshot({ path: screenshots + "/branch-assignments.png", fullPage: true });
  if (pageErrors.length) throw new Error("browser runtime errors: " + pageErrors.join(" | "));
  console.log(JSON.stringify({ metrics, pageErrors }));
} finally {
  await context.close();
  await browser.close();
}`
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", adminScript)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browser,
		"MODEL_ADMIN_BASE_URL="+baseURL,
		"MODEL_ADMIN_TOKEN="+env.adminToken,
		"MODEL_ADMIN_SNAPSHOT_ID="+snapshotID,
		"MODEL_ADMIN_BRANCH_ID="+string(env.devBranch),
		"MODEL_ADMIN_SCREENSHOT_DIR="+outputDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("model editor browser acceptance failed: %v\n%s", err, output)
	}
	t.Logf("model editor browser metrics and screenshot directory %s:\n%s", outputDir, output)
}

func modelAcceptanceReadArchive(t *testing.T, directory, key string) []byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read isolated feature ZIP directory: %v", err)
	}
	var matches [][]byte
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		archive, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatalf("read feature ZIP %q: %v", entry.Name(), err)
		}
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify feature ZIP %q: %v", entry.Name(), err)
		}
		if verified.Manifest.Key == key {
			matches = append(matches, archive)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one isolated %s ZIP, found %d in %s", key, len(matches), directory)
	}
	return matches[0]
}

func modelAcceptancePort(t *testing.T, manifest sitebuilder.Manifest, key string) sitebuilder.DataInputPort {
	t.Helper()
	for _, port := range manifest.DataInputs {
		if port.Key == key {
			return port
		}
	}
	t.Fatalf("Vitasil %s manifest is missing data input %q", manifest.Key, key)
	return sitebuilder.DataInputPort{}
}

func modelAcceptanceEntity(key, name string, port sitebuilder.DataInputPort, uniqueSlug bool) (sitebuilder.DataModel, sitebuilder.DataModelEntity, map[string]string, map[string]string) {
	entity := sitebuilder.DataModelEntity{ID: uuid.NewString(), Key: key, Name: name, Fields: make([]sitebuilder.DataModelField, 0, len(port.Fields))}
	pathIDs, pathKeys := map[string]string{}, map[string]string{}
	for index, output := range port.Fields {
		fieldKey := fmt.Sprintf("field_%d", index)
		field := sitebuilder.DataModelField{ID: uuid.NewString(), Key: fieldKey, Name: modelAcceptanceFieldName(output.Path), Type: output.Type, Options: append([]string(nil), output.Values...)}
		if uniqueSlug && output.Path == "/slug" {
			field.Unique = true
		}
		entity.Fields = append(entity.Fields, field)
		pathIDs[output.Path], pathKeys[output.Path] = field.ID, field.Key
	}
	return sitebuilder.DataModel{FormatVersion: 1, Entities: []sitebuilder.DataModelEntity{entity}}, entity, pathIDs, pathKeys
}

func modelAcceptanceFieldName(path string) string {
	names := map[string]string{
		"/slug": "Слаг", "/title": "Название", "/category": "Категория", "/price": "Цена",
		"/imageSrc": "Изображение", "/description": "Описание", "/icon": "Значок",
		"/eyebrow": "Надзаголовок", "/previousLabel": "Подпись «назад»", "/nextLabel": "Подпись «вперёд»",
		"/itemsLabel": "Подпись количества", "/catalogActionLabel": "Текст ссылки на каталог", "/catalogActionTo": "Ссылка на каталог",
	}
	if name, ok := names[path]; ok {
		return name
	}
	return strings.Trim(path, "/")
}

func modelAcceptanceFieldBindings(port sitebuilder.DataInputPort, ids map[string]string) []sitebuilder.DataInputFieldBinding {
	bindings := make([]sitebuilder.DataInputFieldBinding, 0, len(port.Fields))
	for _, field := range port.Fields {
		bindings = append(bindings, sitebuilder.DataInputFieldBinding{InputPath: field.Path, FieldID: ids[field.Path]})
	}
	return bindings
}

func modelAcceptanceAssertSameOutputShape(t *testing.T, first, other sitebuilder.DataInputPort, label string) {
	t.Helper()
	if len(first.Fields) != len(other.Fields) {
		t.Fatalf("Vitasil input %s is not compatible with shared Product: %+v", label, other)
	}
	for index := range first.Fields {
		if first.Fields[index].Path != other.Fields[index].Path || first.Fields[index].Type != other.Fields[index].Type {
			t.Fatalf("Vitasil input %s field %d differs from shared Product contract: %+v != %+v", label, index, first.Fields[index], other.Fields[index])
		}
	}
}

func modelAcceptanceValues(keys map[string]string, byPath map[string]any) map[string]any {
	values := make(map[string]any, len(byPath))
	for path, value := range byPath {
		values[keys[path]] = value
	}
	return values
}

func modelAcceptanceContentObject(t *testing.T, placement map[string]any, key string) map[string]any {
	t.Helper()
	content, ok := placement["content"].(map[string]any)
	if !ok {
		t.Fatalf("runtime placement has no content: %v", placement)
	}
	object, ok := content[key].(map[string]any)
	if !ok {
		t.Fatalf("runtime content %q is not an object: %v", key, content[key])
	}
	return object
}

func modelAcceptanceContentList(t *testing.T, placement map[string]any, key string) []any {
	t.Helper()
	content, ok := placement["content"].(map[string]any)
	if !ok {
		t.Fatalf("runtime placement has no content: %v", placement)
	}
	items, ok := content[key].([]any)
	if !ok {
		t.Fatalf("runtime content %q is not a collection: %v", key, content[key])
	}
	return items
}

func modelAcceptanceAssertTitles(t *testing.T, items []any, expected []string) {
	t.Helper()
	if len(items) != len(expected) {
		t.Fatalf("runtime products count = %d, expected %d: %v", len(items), len(expected), items)
	}
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || item["title"] != expected[index] {
			t.Fatalf("runtime product[%d] = %v, expected title %q", index, raw, expected[index])
		}
	}
}
