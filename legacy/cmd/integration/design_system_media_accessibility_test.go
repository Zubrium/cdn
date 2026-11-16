package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"websiteengine/features/sitebuilder"
	httptransport "websiteengine/presentation/http"
	hfiles "websiteengine/presentation/http/handlers/files"
	hmedia "websiteengine/presentation/http/handlers/media"
)

// The graphical token/breakpoint test has separate coverage. This scenario
// verifies the library boundary with real Vitasil files and the public runtime.
func TestVitasilDesignMediaPickerPersistsPublicAssets(t *testing.T) {
	archiveDir := os.Getenv("TEST_FEATURE_DIR")
	assetDir := os.Getenv("TEST_VITASIL_ASSET_DIR")
	if archiveDir == "" || assetDir == "" || os.Getenv("PRERENDER_TEST_NODE") == "" || os.Getenv("PRERENDER_TEST_MODULE") == "" || os.Getenv("PRERENDER_TEST_BROWSER") == "" {
		t.Skip("set TEST_FEATURE_DIR, TEST_VITASIL_ASSET_DIR and PRERENDER_TEST_NODE/MODULE/BROWSER for real design media acceptance")
	}
	requireIsolatedFilesMediaDatabase(t)
	requireDisposableFilesMediaStore(t)
	database, err := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || !strings.Contains(database.Database, "_design_") || !strings.HasPrefix(os.Getenv("TEST_FILES_S3_BUCKET"), "files-media-qa-design-") {
		t.Fatal("use a disposable database named website_engine_files_media_qa_*_design_* and a files-media-qa-design-* bucket")
	}
	fontPath := filepath.Join(assetDir, "fonts", "alumni-sans-sc-latin-wght-normal.woff2")
	logoPath := filepath.Join(assetDir, "issit_logo.svg")
	fontBytes, err := os.ReadFile(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	logoBytes, err := os.ReadFile(logoPath)
	if err != nil {
		t.Fatal(err)
	}

	env := newTestEnv(t)
	harness := newFileMediaHarness(t, env)
	ctx := context.Background()
	admin := harness.service.AdminOwner()
	privateFont := uploadFileMedia(t, harness, admin, "design-qa-private.woff2", "private", fontBytes)
	archive, version := readExternalFeatureArchive(t, archiveDir, "header")
	verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
	if err != nil {
		t.Fatalf("verify current Vitasil header archive: %v", err)
	}
	status, imported, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register Vitasil header@%s: %d %v", version, status, imported)
	}
	headerPort := modelAcceptancePort(t, verified.Manifest, "header")
	navigationPort := modelAcceptancePort(t, verified.Manifest, "navigation")
	model, headerEntity, headerFieldIDs, headerFieldKeys := modelAcceptanceEntity("site_header", "Site header", headerPort, false)
	_, navigationEntity, navigationFieldIDs, navigationFieldKeys := modelAcceptanceEntity("navigation_link", "Navigation link", navigationPort, false)
	model.Entities = append(model.Entities, navigationEntity)
	configRaw, err := json.Marshal(singlePageConfig("header", version))
	if err != nil {
		t.Fatal(err)
	}
	var config sitebuilder.SiteConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		t.Fatal(err)
	}
	config.DataModel = &model
	config.Pages[0].Placements[0].Slot = "header"
	config.Pages[0].Placements[0].DataBindings = []sitebuilder.DataInputBinding{
		{InputKey: headerPort.Key, EntityID: headerEntity.ID, Fields: modelAcceptanceFieldBindings(headerPort, headerFieldIDs)},
		{InputKey: navigationPort.Key, EntityID: navigationEntity.ID, Fields: modelAcceptanceFieldBindings(navigationPort, navigationFieldIDs)},
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(configBytes, &configMap); err != nil {
		t.Fatal(err)
	}
	parentJSON := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Vitasil design media parent")
	parentID, err := uuid.Parse(parentJSON["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	// The real header needs navigation data to mount. Leave logoSrc unset so
	// the rendered image proves that the runtime supplies the theme logo.
	content, err := env.snapshots.SaveModelContentWorkspace(ctx, modelAcceptanceActor(t, env), parentID, sitebuilder.ModelContentDraftInput{
		Revision: 0,
		Records: []sitebuilder.ModelContentRecord{
			{EntityID: headerEntity.ID, Values: modelAcceptanceValues(headerFieldKeys, map[string]any{
				"/brand": "Vitasil", "/brandTo": "/", "/navigationLabel": "Основная навигация",
			})},
			{EntityID: navigationEntity.ID, SortOrder: 0, Values: modelAcceptanceValues(navigationFieldKeys, map[string]any{
				"/label": "Каталог", "/to": "/catalog",
			})},
		},
	})
	if err != nil {
		t.Fatalf("save model-backed Vitasil header data: %v", err)
	}
	if len(content.Records) != 2 {
		t.Fatalf("header media fixture should contain header and navigation records: %+v", content.Records)
	}
	recordIDs := make(map[string]string, len(content.Records))
	for _, record := range content.Records {
		recordIDs[record.EntityID] = record.ID
	}
	selected, err := env.snapshots.SaveModelContentWorkspace(ctx, modelAcceptanceActor(t, env), parentID, sitebuilder.ModelContentDraftInput{
		Revision: content.Revision,
		Records:  content.Records,
		Selections: []sitebuilder.ModelContentSelection{
			{PlacementID: config.Pages[0].Placements[0].ID, InputKey: headerPort.Key, Mode: "ids", RecordIDs: []string{recordIDs[headerEntity.ID]}},
			{PlacementID: config.Pages[0].Placements[0].ID, InputKey: navigationPort.Key, Mode: "ids", RecordIDs: []string{recordIDs[navigationEntity.ID]}},
		},
	})
	if err != nil {
		t.Fatalf("select published header and navigation data for the Vitasil placement: %v", err)
	}
	if _, err := env.snapshots.PublishModelContentWorkspace(ctx, modelAcceptanceActor(t, env), parentID, selected.Revision); err != nil {
		t.Fatalf("publish model-backed Vitasil header data: %v", err)
	}
	designURL := harness.server.URL + "/admin/site/design-system?snapshot=" + parentID.String()
	output := runDesignMediaBrowser(t, designMediaEditorScript,
		[]string{"TEST_ADMIN_TOKEN=" + env.adminToken}, harness.server.URL+"/admin/media", designURL, fontPath, logoPath)
	var childID uuid.UUID
	for _, line := range strings.Split(output, "\n") {
		if id, found := strings.CutPrefix(line, "DESIGN_MEDIA_CHILD="); found {
			childID, err = uuid.Parse(id)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if childID == uuid.Nil {
		t.Fatalf("browser did not report a saved child: %s", output)
	}

	parent, err := env.snapshots.SnapshotDesignWorkspace(ctx, parentID)
	if err != nil || len(parent.DesignSystem.Fonts) != 0 || parent.DesignSystem.BrandLogoID != "" {
		t.Fatalf("media changes reached the parent: %+v %v", parent, err)
	}
	child, err := env.snapshots.SnapshotDesignWorkspace(ctx, childID)
	if err != nil || child.Snapshot.ParentID == nil || *child.Snapshot.ParentID != parentID || len(child.DesignSystem.Fonts) != 1 {
		t.Fatalf("selected media did not create an independent child: %+v %v", child, err)
	}
	font := child.DesignSystem.Fonts[0]
	if font.Family != "QA Alumni Sans" || font.Style != "normal" || font.Weight != "400" {
		t.Fatalf("font selection and graphical weight were not persisted: %+v", font)
	}
	for _, asset := range []struct{ id, name, mime string }{
		{font.MediaID, "design-qa-public.woff2", "font/woff2"},
		{child.DesignSystem.BrandLogoID, "design-qa-logo.svg", "image/svg+xml"},
	} {
		id, err := uuid.Parse(asset.id)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := harness.service.Get(ctx, admin, id)
		if err != nil || stored.Name != asset.name || stored.ContentType != asset.mime || stored.Visibility != "public" {
			t.Fatalf("child did not store the selected public file ID: %+v %v", stored, err)
		}
	}

	// Existing synthetic tests cover private logos and images as fonts. These
	// checks use genuine WOFF2 bytes to cover the other directions.
	for _, attack := range []struct {
		name, diagnostic string
		mutate           func(*sitebuilder.DesignSystem)
	}{
		{"private font", "must be public", func(theme *sitebuilder.DesignSystem) { theme.Fonts[0].MediaID = privateFont.ID.String() }},
		{"font as logo", "must be an image", func(theme *sitebuilder.DesignSystem) { theme.BrandLogoID = font.MediaID }},
	} {
		theme := child.DesignSystem
		theme.Fonts = append([]sitebuilder.DesignFont(nil), theme.Fonts...)
		attack.mutate(&theme)
		status, body, _ := fileMediaRequest(t, harness.server, http.MethodPost, "/api/v2/admin/site/snapshots/"+childID.String()+"/design-system", env.adminToken, map[string]any{
			"name": attack.name, "designSystem": theme,
		})
		if status != http.StatusBadRequest || !strings.Contains(string(body), attack.diagnostic) {
			t.Fatalf("invalid design asset %s: status=%d body=%s", attack.name, status, body)
		}
	}
	var rejectedChildren int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM site_snapshots WHERE parent_snapshot_id=$1`, childID).Scan(&rejectedChildren); err != nil || rejectedChildren != 0 {
		t.Fatalf("rejected design changes produced descendants: %d %v", rejectedChildren, err)
	}
	preview, err := env.snapshots.Branch(ctx, uuid.MustParse(string(env.devBranch)))
	if err != nil || preview.SnapshotID == nil || *preview.SnapshotID != parentID {
		t.Fatalf("saving assets changed the assigned branch: %+v %v", preview, err)
	}
	branch, err := env.snapshots.Branch(ctx, uuid.MustParse(string(env.prodBranch)))
	if err != nil || branch.SnapshotID != nil {
		t.Fatalf("saving assets assigned another branch: %+v %v", branch, err)
	}
	branch, err = env.snapshots.AssignBranch(ctx, testActor(t, env), branch.ID, childID, branch.Revision, &child.Snapshot.ContentRevision)
	if err != nil {
		t.Fatal(err)
	}

	deps := env.routerDeps
	media := hmedia.NewHandler(env.media, deps.Log, 20<<20)
	if err := media.SetRedirectCacheTTL(time.Hour); err != nil {
		t.Fatal(err)
	}
	media.SetFileHandler(hfiles.NewHandler(harness.service, deps.Log, 1<<20))
	deps.Media = media
	public := httptest.NewServer(httptransport.NewPublicRouter(deps, env.prodBranch))
	t.Cleanup(public.Close)
	status, raw, _ := fileMediaRequest(t, public, http.MethodGet, "/api/v2/runtime?path=/", "", nil)
	var runtime sitebuilder.RuntimeComposition
	if err := json.Unmarshal(raw, &runtime); err != nil || status != http.StatusOK || runtime.Snapshot.ID != childID {
		t.Fatalf("public composition does not use assigned child: %d %s %v", status, raw, err)
	}
	fontURL := "/api/v2/system-assets/" + font.MediaID
	logoURL := "/api/v2/system-assets/" + child.DesignSystem.BrandLogoID
	if runtime.SystemAssets.LogoURL != logoURL {
		t.Fatalf("runtime logo URL = %q, expected %q", runtime.SystemAssets.LogoURL, logoURL)
	}
	status, css, _ := fileMediaRequest(t, public, http.MethodGet, runtime.ThemeStyleURL, "", nil)
	if status != http.StatusOK || !strings.Contains(string(css), `@font-face{font-family:"QA Alumni Sans";font-style:normal;font-weight:400;`) || !strings.Contains(string(css), `url("`+fontURL+`") format("woff2")`) {
		t.Fatalf("public theme did not use the selected font: %d %s", status, css)
	}
	for _, asset := range []struct {
		path, mime string
		data       []byte
	}{{fontURL, "font/woff2", fontBytes}, {logoURL, "image/svg+xml", logoBytes}} {
		status, body, headers := fileMediaRequest(t, public, http.MethodGet, asset.path, "", nil)
		if status != http.StatusOK || headers.Get("Content-Type") != asset.mime || !bytes.Equal(body, asset.data) {
			t.Fatalf("public design asset %s mismatch: status=%d MIME=%q bytes=%d", asset.path, status, headers.Get("Content-Type"), len(body))
		}
	}
	status, _, _ = fileMediaRequest(t, public, http.MethodGet, "/api/v2/system-assets/"+privateFont.ID.String(), "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("public system-assets path exposed private WOFF2: %d", status)
	}
	runDesignMediaBrowser(t, designMediaRuntimeScript, nil, public.URL+"/", fontURL, logoURL)
	t.Logf("real Vitasil header@%s, public WOFF2 and SVG retained by child and served through branch %s", version, branch.ID)
}

func runDesignMediaBrowser(t *testing.T, script string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	command := exec.CommandContext(ctx, node, append([]string{"--input-type=module", "-e", script}, args...)...)
	command.Env = append(append(os.Environ(), "PLAYWRIGHT_TEST_MODULE="+module, "PLAYWRIGHT_TEST_BROWSER="+browser), env...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("design media browser acceptance failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	return string(output)
}

const designMediaBrowserInit = `
import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true });
`

const designMediaEditorScript = designMediaBrowserInit + `
import { readFile } from "node:fs/promises";
import { join } from "node:path";
const [libraryURL, designURL, fontPath, logoPath] = process.argv.slice(1);
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 960 }, colorScheme: "light", reducedMotion: "reduce" });
  page.setDefaultTimeout(10000);
  const errors = [];
  const misplacedUploads = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("request", request => {
    if (request.method() !== "GET" && new URL(request.url()).pathname.includes("/uploads") && !page.url().endsWith("/admin/media")) misplacedUploads.push(request.url());
  });
  await page.addInitScript(token => localStorage.setItem("we_admin_token", token), process.env.TEST_ADMIN_TOKEN);
  await page.goto(libraryURL, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Файлы", exact: true }).waitFor();
  await page.getByRole("button", { name: "Загрузить", exact: true }).click();
  const upload = page.getByRole("dialog", { name: "Загрузка медиа" });
  await upload.locator("#upload-visibility").click();
  await page.getByRole("option", { name: "Публичный — доступен по ссылке", exact: true }).click();
  await upload.getByLabel("Выбрать файлы").setInputFiles([
    { name: "design-qa-public.woff2", mimeType: "font/woff2", buffer: await readFile(fontPath) },
    { name: "design-qa-logo.svg", mimeType: "image/svg+xml", buffer: await readFile(logoPath) },
  ]);
  await upload.getByText("Готово: 2 · Ошибок: 0", { exact: true }).waitFor({ timeout: 30000 });
  await upload.getByRole("button", { name: "Закрыть", exact: true }).first().click();
  await page.goto(designURL, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Дизайн-система", exact: true }).waitFor();
  await page.getByRole("button", { name: "Шрифты и бренд", exact: true }).click();
  if (await page.locator('input[type="file"]').count()) throw new Error("file upload appeared in the design editor");

  async function keyboardReach(control) {
    for (let tab = 0; tab < 12; tab++) {
      await page.keyboard.press("Tab");
      if (await control.evaluate(element => element === document.activeElement)) {
        if (!await control.evaluate(element => element.matches(":focus-visible"))) throw new Error("keyboard focus has no visible treatment");
        return;
      }
    }
    throw new Error("named control was not reachable by Tab: " + await control.ariaSnapshot());
  }
  async function chooseFromLibrary(dialogName, fileName, excluded) {
    const picker = page.getByRole("dialog", { name: dialogName });
    const tile = picker.getByRole("button", { name: new RegExp(fileName.replace(".", "\\.")) });
    await tile.waitFor();
    for (const name of excluded) {
      if (await picker.getByText(name, { exact: true }).count()) throw new Error("ineligible resource appeared in picker: " + name);
    }
    if (await picker.locator('input[type="file"]').count()) throw new Error("design picker exposed an upload input");
    const link = picker.getByRole("link", { name: "Открыть медиатеку" }).first();
    if (await link.getAttribute("href") !== "/admin/media" || await link.getAttribute("target") !== "_blank") throw new Error("picker does not preserve the editor while opening the library");
    await picker.getByRole("textbox", { name: "Поиск медиа" }).focus();
    await keyboardReach(tile);
    await page.keyboard.press("Enter");
    if (await tile.getAttribute("aria-pressed") !== "true") throw new Error("keyboard selection did not expose its selected state");
    await keyboardReach(picker.getByRole("button", { name: "Выбрать", exact: true }));
    await page.keyboard.press("Enter");
    await picker.waitFor({ state: "hidden" });
  }

  await page.setViewportSize({ width: 360, height: 860 });
  const family = page.getByRole("textbox", { name: "Название семейства шрифта" });
  await family.fill("QA Alumni Sans");
  await family.focus();
  await keyboardReach(page.getByRole("button", { name: "Добавить WOFF2" }));
  await page.keyboard.press("Enter");
  await chooseFromLibrary("Выбрать файл шрифта", "design-qa-public.woff2", ["design-qa-private.woff2", "design-qa-logo.svg"]);
  await page.getByRole("combobox", { name: "Начертание QA Alumni Sans" }).selectOption("400");
  await page.getByRole("combobox", { name: "Стиль QA Alumni Sans" }).waitFor();
  await page.getByRole("figure", { name: "Образец шрифта QA Alumni Sans" }).waitFor();
  await page.waitForFunction(() => [...document.fonts].some(face => face.family.startsWith("design-font-") && face.weight === "400" && face.status === "loaded"));
  await page.getByRole("button", { name: "Выбрать изображение" }).focus();
  await page.keyboard.press("Enter");
  await chooseFromLibrary("Выбрать логотип", "design-qa-logo.svg", ["design-qa-public.woff2", "design-qa-private.woff2"]);
  await page.getByRole("img", { name: "Логотип сайта" }).waitFor();
  await page.waitForFunction(() => {
    const logo = document.querySelector('img[alt="Логотип сайта"]');
    return logo?.complete && logo.naturalWidth > 0;
  });

  for (const [width, scheme] of [[360, "dark"], [390, "light"], [1280, "light"]]) {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForFunction(scheme => document.documentElement.classList.contains("dark") === (scheme === "dark"), scheme);
    const region = page.getByRole("region", { name: "Шрифты и бренд", exact: true });
    const accessibility = await region.ariaSnapshot();
    for (const name of ["Название семейства шрифта", "Начертание QA Alumni Sans", "Стиль QA Alumni Sans", "Удалить шрифт QA Alumni Sans", "Логотип сайта"]) {
      if (!accessibility.includes(name)) throw new Error("accessible name absent at " + width + "px: " + name);
    }
    const layout = await region.evaluate(element => ({ width: innerWidth, documentWidth: document.documentElement.scrollWidth, overflow: element.scrollWidth > element.clientWidth }));
    if (layout.documentWidth > layout.width || layout.overflow) throw new Error("design media section overflows at " + width + "px: " + JSON.stringify(layout));
    if (process.env.DESIGN_MEDIA_QA_SCREENSHOTS) await page.screenshot({ path: join(process.env.DESIGN_MEDIA_QA_SCREENSHOTS, "design-media-" + width + "-" + scheme + ".png"), fullPage: true });
  }
  const [saved] = await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === "/api/v2/admin/site/snapshots" && response.request().method() === "POST"),
    page.getByRole("button", { name: "Сохранить", exact: true }).click(),
  ]);
  if (saved.status() !== 201) throw new Error("save of selected resources failed: " + saved.status() + " " + await saved.text());
  const child = await saved.json();
  await page.waitForFunction(id => new URLSearchParams(location.search).get("snapshot") === id, child.id);
  if (misplacedUploads.length) throw new Error("design edit triggered file uploads: " + misplacedUploads.join(", "));
  if (errors.length) throw new Error("admin browser errors: " + errors.join("; "));
  console.log("verified public media upload in library, keyboard-only picker selections, named controls, WOFF2 sample, logo rendering, and responsive 360/390/1280px layouts");
  console.log("DESIGN_MEDIA_CHILD=" + child.id);
} finally { await browser.close(); }
`

const designMediaRuntimeScript = designMediaBrowserInit + `
const [siteURL, fontURL, logoURL] = process.argv.slice(1);
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  page.setDefaultTimeout(10000);
  const browserErrors = [];
  page.on("pageerror", error => browserErrors.push(error.message));
  page.on("console", message => { if (message.type() === "error") browserErrors.push(message.text()); });
  await page.goto(siteURL, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(logoURL => {
    const expected = new URL(logoURL, location.href).href;
    return [...document.images].some(image => image.currentSrc === expected && image.complete && image.naturalWidth > 0);
  }, logoURL).catch(async error => {
    const state = await page.evaluate(() => ({
      content: document.body.innerText,
      images: [...document.images].map(image => ({ src: image.currentSrc, complete: image.complete, width: image.naturalWidth })),
    }));
    throw new Error("Vitasil header did not load the assigned logo: " + JSON.stringify({ state, browserErrors, cause: error.message }));
  });
  const cssFace = await page.evaluate(() => [...document.styleSheets].filter(sheet => sheet.href?.includes("/site-themes/snapshots/")).flatMap(sheet => [...sheet.cssRules]).find(rule => rule instanceof CSSFontFaceRule && rule.style.getPropertyValue("font-family").includes("QA Alumni Sans"))?.cssText);
  if (!cssFace?.includes(fontURL)) throw new Error("runtime @font-face is absent or points to the wrong file: " + cssFace);
  const [response, loaded] = await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === fontURL),
    page.evaluate(async () => (await document.fonts.load('400 24px "QA Alumni Sans"', "Aa 123")).some(face => face.status === "loaded")),
  ]);
  if (!loaded || response.status() !== 200 || response.headers()["content-type"] !== "font/woff2") throw new Error("browser did not decode the published WOFF2");
  console.log("verified Vitasil header logo and Chromium-decoded @font-face through the assigned branch");
} finally { await browser.close(); }
`
