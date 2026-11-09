package integration

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

// This browser acceptance uses current Vitasil ZIPs and disposable Engine data.
// It exercises the graphical editor end to end, then verifies the exact saved
// theme that the public runtime consumes.
func TestVitasilDesignSystemGraphicalWorkflowCreatesIndependentSnapshot(t *testing.T) {
	dir := os.Getenv("TEST_FEATURE_DIR")
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if dir == "" || node == "" || module == "" || browser == "" {
		t.Skip("set TEST_FEATURE_DIR and PRERENDER_TEST_NODE/MODULE/BROWSER for Vitasil design-system browser acceptance")
	}
	env := newTestEnv(t)
	ctx := context.Background()
	keys := []string{"background", "catalog", "footer", "form", "header", "hero", "legal-documents", "main-data", "not-found"}
	features := make([]any, 0, len(keys))
	versions := make(map[string]string, len(keys))
	manifests := make([]sitebuilder.Manifest, 0, len(keys))
	for _, key := range keys {
		archive, version := readExternalFeatureArchive(t, dir, key)
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register %s@%s: status=%d body=%v", key, version, status, body)
		}
		versions[key] = version
		manifests = append(manifests, modelAcceptanceManifestFromArchive(t, archive))
		features = append(features, map[string]any{"featureKey": key, "version": version})
	}
	config := singlePageConfig("header", versions["header"])
	pageConfig := config["pages"].([]any)[0].(map[string]any)
	pageConfig["placements"].([]any)[0].(map[string]any)["slot"] = "header"
	config["features"] = features
	config = modelAcceptanceBindManifestInputs(t, config, manifests)
	parentJSON := saveAndDeploySnapshot(t, env, config, "Vitasil design parent")
	parentID, err := uuid.Parse(parentJSON["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	parentBranchID, err := uuid.Parse(string(env.devBranch))
	if err != nil {
		t.Fatal(err)
	}
	otherBranchID, err := uuid.Parse(string(env.prodBranch))
	if err != nil {
		t.Fatal(err)
	}

	childID, grandchildID := runVitasilDesignSystemGraphicalEditorBrowser(t, env, parentID)
	parentTheme, err := env.snapshots.SnapshotDesignWorkspace(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}
	childTheme, err := env.snapshots.SnapshotDesignWorkspace(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	grandchildTheme, err := env.snapshots.SnapshotDesignWorkspace(ctx, grandchildID)
	if err != nil {
		t.Fatal(err)
	}
	if len(parentTheme.DesignSystem.Groups) != 0 || len(parentTheme.DesignSystem.BaseTokens) != 0 || len(parentTheme.DesignSystem.Breakpoints) != 0 {
		t.Fatalf("the graphical child edit changed its parent theme: %+v", parentTheme.DesignSystem)
	}
	if len(childTheme.DesignSystem.Groups) != 1 || len(childTheme.DesignSystem.BaseTokens) != 1 || len(childTheme.DesignSystem.Breakpoints) != 3 {
		t.Fatalf("browser-created theme did not persist all three sorted breakpoints: %+v", childTheme.DesignSystem)
	}
	base := childTheme.DesignSystem.BaseTokens[0]
	if base.Name != "Focus" || childTheme.DesignSystem.FeatureValues["header/color-focus"].BaseTokenID != base.ID {
		t.Fatalf("Vitasil token did not retain its graphical base link: %+v %+v", base, childTheme.DesignSystem.FeatureValues)
	}
	for i, want := range []int{600, 1024, 1440} {
		if childTheme.DesignSystem.Breakpoints[i].MinWidthPx != want {
			t.Fatalf("saved breakpoint order = %+v, want %d at index %d", childTheme.DesignSystem.Breakpoints, want, i)
		}
	}
	if len(grandchildTheme.DesignSystem.Breakpoints) != 2 || grandchildTheme.DesignSystem.Breakpoints[0].MinWidthPx != 600 || grandchildTheme.DesignSystem.Breakpoints[1].MinWidthPx != 1024 {
		t.Fatalf("confirmed deletion did not create an independent grandchild theme: %+v", grandchildTheme.DesignSystem.Breakpoints)
	}
	if len(childTheme.DesignSystem.Breakpoints[2].BaseValues) != 1 || grandchildTheme.DesignSystem.Breakpoints[0].BaseValues[base.ID].Color.Hex != "#fedcba" {
		t.Fatalf("child values changed when its grandchild removed a breakpoint: child=%+v grandchild=%+v", childTheme.DesignSystem.Breakpoints, grandchildTheme.DesignSystem.Breakpoints)
	}
	parentBranch, err := env.snapshots.Branch(ctx, parentBranchID)
	if err != nil || parentBranch.SnapshotID == nil || *parentBranch.SnapshotID != parentID {
		t.Fatalf("site-editor save changed the already assigned branch: %+v %v", parentBranch, err)
	}
	otherBranch, err := env.snapshots.Branch(ctx, otherBranchID)
	if err != nil || otherBranch.SnapshotID != nil {
		t.Fatalf("site-editor save assigned a child to another branch: %+v %v", otherBranch, err)
	}
	// Assign only after proving the editor did not do so. The browser helper then
	// validates the published Vitasil header token at several viewport widths.
	otherBranch, err = env.snapshots.AssignBranch(ctx, testActor(t, env), otherBranch.ID, childID, otherBranch.Revision)
	if err != nil {
		t.Fatalf("explicitly assign browser-created child for public CSS verification: %v", err)
	}
	assertResponsiveDesignTokensInBrowser(t, env, otherBranch, base.ID, "--feature-header-color-focus", []designTokenBrowserCase{
		{Width: 500, Color: "rgb(18, 52, 86)"},
		{Width: 800, Color: "rgb(254, 220, 186)"},
		{Width: 1280, Color: "rgb(254, 220, 186)"},
		{Width: 1600, Color: "rgb(171, 205, 239)"},
	})
}

func runVitasilDesignSystemGraphicalEditorBrowser(t *testing.T, env *testEnv, parentID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	pageURL := env.server.URL + "/admin/site/design-system?snapshot=" + parentID.String()
	script := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 960 } });
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.addInitScript(token => localStorage.setItem("we_admin_token", token), process.env.TEST_ADMIN_TOKEN);
  await page.goto(process.argv[1], { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Дизайн-система", exact: true }).waitFor();
  const screenshotPrefix = "/tmp/website-engine-design-system-" + process.env.TEST_SNAPSHOT_ID;
  if (process.env.QA_SCREENSHOT === "1") await page.screenshot({ path: screenshotPrefix + "-desktop.png", fullPage: true });

  const rawEditors = await page.evaluate(() => ({ textareas: document.querySelectorAll("textarea").length, iframes: document.querySelectorAll("iframe").length, fileInputs: document.querySelectorAll('input[type="file"]').length }));
  if (rawEditors.textareas || rawEditors.iframes || rawEditors.fileInputs) throw new Error("design editor exposed code, live preview, or file upload: " + JSON.stringify(rawEditors));

  await page.getByText("Добавить группу или токен", { exact: true }).click();
  await page.getByRole("textbox", { name: "Название группы" }).fill("QA Brand");
  await page.getByRole("button", { name: "Группа", exact: true }).click();
  await page.getByRole("textbox", { name: "Название токена" }).fill("Focus");
  await page.getByRole("button", { name: "Токен", exact: true }).click();
  const baseColor = page.locator('input[type="color"]').first();
  await baseColor.fill("#123456");
  if (await baseColor.inputValue() !== "#123456") throw new Error("graphically edited base color was not retained in the control");

  await page.locator("#design-width").selectOption("");
  await page.getByRole("button", { name: "Шапка сайта", exact: true }).click();
  const featureSource = page.getByRole("combobox", { name: "Источник: Фокус", exact: true });
  const parentFocus = await page.locator("#feature-header-color-focus-color").inputValue();
  const parentFocusOpacity = await page.locator("#feature-header-color-focus-opacity").inputValue();
  await featureSource.selectOption({ label: "Связать: Focus" });
  await page.getByText("Связан с базовым", { exact: false }).waitFor();
  const resetFeature = page.getByRole("button", { name: "Сбросить Фокус", exact: true });
  if (await resetFeature.isDisabled()) throw new Error("the feature token reset control was disabled while linked");
  await resetFeature.click();
  await featureSource.selectOption({ label: "Связать: Focus" });
  await page.getByText("Связан с базовым", { exact: false }).waitFor();

  await page.getByRole("button", { name: "Базовые токены", exact: true }).click();
  await page.getByText("Размеры экрана", { exact: true }).click();
  const addBreakpoint = async (name, width) => {
    await page.locator("#breakpoint-name").fill(name);
    await page.locator("#breakpoint-width").fill(String(width));
    await page.getByRole("button", { name: "Добавить размер", exact: true }).click();
  };
  await addBreakpoint("Wide", 1440);
  await page.locator('input[type="color"]').first().fill("#abcdef");
  await addBreakpoint("Desktop", 1024);
  await addBreakpoint("Tablet", 600);
  await page.locator('input[type="color"]').first().fill("#fedcba");

  const sortedLabels = (await page.locator("#design-width option").allTextContents()).slice(1);
  const sortedExpected = ["Tablet · от 600 px", "Desktop · от 1024 px", "Wide · от 1440 px"];
  if (JSON.stringify(sortedLabels) !== JSON.stringify(sortedExpected)) throw new Error("breakpoints were not sorted by width: " + JSON.stringify(sortedLabels));
  await page.locator("#design-width").selectOption({ label: "Desktop · от 1024 px" });
  if (await page.locator('input[type="color"]').first().inputValue() !== "#fedcba") throw new Error("the unmodified intermediate width did not inherit the previous base value");

  await page.locator("#breakpoint-name").fill("Duplicate");
  await page.locator("#breakpoint-width").fill("600");
  await page.getByRole("button", { name: "Добавить размер", exact: true }).click();
  await page.getByText("Такой порог ширины уже существует.", { exact: true }).waitFor();
  if ((await page.locator("#design-width option").count()) !== 4) throw new Error("duplicate breakpoint width was inserted");

  await page.locator("#design-width").selectOption({ label: "Wide · от 1440 px" });
  const resetBase = page.getByRole("button", { name: "Сбросить Focus", exact: true });
  if (await resetBase.isDisabled()) throw new Error("responsive base override was not available to reset");
  await resetBase.click();
  if (await page.locator('input[type="color"]').first().inputValue() !== "#fedcba") throw new Error("reset did not restore the inherited responsive value");
  await page.locator('input[type="color"]').first().fill("#abcdef");
  await page.locator("#design-width").selectOption({ label: "Tablet · от 600 px" });

  await page.setViewportSize({ width: 360, height: 860 });
  const visual = await page.evaluate(() => {
    const unlabeled = [...document.querySelectorAll("input:not([type=hidden]),select,textarea")]
      .filter(element => element.getClientRects().length > 0 && !element.disabled)
      .filter(element => !element.getAttribute("aria-label") && !element.getAttribute("aria-labelledby") && !(element.labels?.length));
    return { viewport: innerWidth, pageWidth: document.documentElement.scrollWidth, unlabeled: unlabeled.map(element => element.outerHTML.slice(0, 100)) };
  });
  if (visual.pageWidth > visual.viewport) throw new Error("design editor overflows at 360px: " + JSON.stringify(visual));
  if (visual.unlabeled.length) throw new Error("visible controls have no accessible label: " + JSON.stringify(visual.unlabeled));
  if (process.env.QA_SCREENSHOT === "1") await page.screenshot({ path: screenshotPrefix + "-mobile.png", fullPage: true });
  await page.locator("#design-width").focus();
  await page.keyboard.press("Tab");
  const keyboardFocus = await page.evaluate(() => ({ label: document.activeElement?.getAttribute("aria-label"), focusVisible: document.activeElement?.matches(":focus-visible") }));
  if (keyboardFocus.label !== "Удалить размер экрана" || !keyboardFocus.focusVisible) throw new Error("breakpoint controls do not expose a visible keyboard focus: " + JSON.stringify(keyboardFocus));
  await page.keyboard.press("Enter");
  const keyboardDialog = page.getByRole("alertdialog");
  await keyboardDialog.waitFor();
  const keyboardCancel = keyboardDialog.getByRole("button", { name: "Отмена", exact: true });
  await keyboardCancel.waitFor();
  await page.keyboard.press("Tab");
  await page.keyboard.press("Shift+Tab");
  const cancelFocus = await page.evaluate(() => ({ label: document.activeElement?.textContent?.trim(), focusVisible: document.activeElement?.matches(":focus-visible") }));
  if (cancelFocus.label !== "Отмена" || !cancelFocus.focusVisible) throw new Error("delete confirmation does not keep its keyboard cancel action visibly focused: " + JSON.stringify(cancelFocus));
  await page.keyboard.press("Enter");
  await keyboardDialog.waitFor({ state: "hidden" });

  let rejectNextCreate = true;
  await page.route("**/api/v2/admin/site/snapshots", async route => {
    const request = route.request();
    if (rejectNextCreate && request.method() === "POST" && new URL(request.url()).pathname === "/api/v2/admin/site/snapshots") {
      rejectNextCreate = false;
      await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "revision_conflict", message: "Stale test revision" } }) });
      return;
    }
    await route.continue();
  });
  const rejectedWait = page.waitForResponse(response => new URL(response.url()).pathname === "/api/v2/admin/site/snapshots" && response.request().method() === "POST");
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const rejected = await rejectedWait;
  if (rejected.status() !== 409) throw new Error("conflict fixture did not reject first snapshot save");
  if (await page.locator('input[type="color"]').first().inputValue() !== "#fedcba") throw new Error("conflict discarded the user's responsive edit");
  if (await page.getByRole("button", { name: "Сохранить", exact: true }).isDisabled()) throw new Error("save retry was disabled after revision conflict");

  const savedWait = page.waitForResponse(response => new URL(response.url()).pathname === "/api/v2/admin/site/snapshots" && response.request().method() === "POST" && response.status() === 201);
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const saved = await savedWait;
  const child = await saved.json();
  if (!child.id) throw new Error("snapshot save response has no child id");
  await page.waitForFunction(childID => new URLSearchParams(location.search).get("snapshot") === childID, child.id);

  const parentDesignRoute = "**/api/v2/admin/site/snapshots/" + process.env.TEST_SNAPSHOT_ID + "/design-system";
  await page.route(parentDesignRoute, route => route.fulfill({
    status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "Parent theme unavailable in test" } }),
  }));
  await page.goto(process.argv[2] + child.id, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Дизайн-система", exact: true }).waitFor();
  await page.getByText("Не удалось загрузить значения родителя.", { exact: true }).waitFor();
  if (!(await baseColor.isEnabled())) throw new Error("failed parent comparison blocked editing the current theme");
  await page.unroute(parentDesignRoute);
  await page.getByRole("button", { name: "Повторить сравнение", exact: true }).click();
  await page.getByText("Новый относительно родителя", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Шапка сайта", exact: true }).click();
  await page.getByText("Изменено относительно родителя", { exact: true }).waitFor();
  await page.getByText("В родителе · " + parentFocus + " · непрозрачность " + parentFocusOpacity + "% · Из фичи", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Базовые токены", exact: true }).click();
  await page.locator("#design-width").selectOption({ label: "Wide · от 1440 px" });
  await page.getByRole("button", { name: "Удалить размер экрана", exact: true }).click();
  const deleteDialog = page.getByRole("alertdialog");
  await deleteDialog.getByRole("heading", { name: "Удалить размер «Wide»?", exact: true }).waitFor();
  await deleteDialog.getByText(/Будет удалено переопределений: 1/).waitFor();
  await deleteDialog.getByRole("button", { name: "Отмена", exact: true }).click();
  if ((await page.locator("#design-width option").allTextContents()).every(label => !label.startsWith("Wide ·"))) throw new Error("cancelling breakpoint deletion removed the breakpoint");
  await page.getByRole("button", { name: "Удалить размер экрана", exact: true }).click();
  const acceptedDialog = page.getByRole("alertdialog");
  await acceptedDialog.getByRole("button", { name: "Удалить размер", exact: true }).click();
  if ((await page.locator("#design-width option").allTextContents()).some(label => label.startsWith("Wide ·"))) throw new Error("confirmed breakpoint deletion left the breakpoint in the editor");
  const grandchildWait = page.waitForResponse(response => new URL(response.url()).pathname === "/api/v2/admin/site/snapshots" && response.request().method() === "POST" && response.status() === 201);
  await page.getByRole("button", { name: "Сохранить", exact: true }).click();
  const grandchild = await grandchildWait;
  const grandchildRecord = await grandchild.json();
  if (!grandchildRecord.id) throw new Error("grandchild save response has no id");
  await page.waitForFunction(id => new URLSearchParams(location.search).get("snapshot") === id, grandchildRecord.id);
  await page.locator("#design-width").selectOption({ label: "Desktop · от 1024 px" });
  await baseColor.fill("#aabbcc");
  await page.getByText("В родителе · #fedcba · непрозрачность 100%", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Шапка сайта", exact: true }).click();
  await page.getByText("В родителе · #fedcba · непрозрачность 100% · Связан с «Focus»", { exact: true }).waitFor();
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error("parent comparison overflows at 360px");
  await page.getByRole("button", { name: "Базовые токены", exact: true }).click();
  await page.getByRole("button", { name: "Сбросить Focus", exact: true }).click();
  await page.getByText("Изменено относительно родителя", { exact: true }).waitFor({ state: "hidden" });
  if (errors.length) throw new Error("design editor raised browser exceptions: " + errors.join(" | "));
  console.log("DESIGN_CHILD=" + child.id);
  console.log("DESIGN_GRANDCHILD=" + grandchildRecord.id);
  if (process.env.QA_SCREENSHOT === "1") console.log("DESIGN_SCREENSHOTS=" + screenshotPrefix);
} finally { await browser.close(); }`
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", script,
		pageURL,
		env.server.URL+"/admin/site/design-system?snapshot=",
	)
	command.Env = append(os.Environ(), "PLAYWRIGHT_TEST_MODULE="+module, "PLAYWRIGHT_TEST_BROWSER="+browser, "TEST_ADMIN_TOKEN="+env.adminToken, "TEST_SNAPSHOT_ID="+parentID.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("graphical design-system browser test failed: %v\n%s", err, output)
	}
	var childID, grandchildID uuid.UUID
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "DESIGN_CHILD=") {
			childID, err = uuid.Parse(strings.TrimPrefix(line, "DESIGN_CHILD="))
		} else if strings.HasPrefix(line, "DESIGN_GRANDCHILD=") {
			grandchildID, err = uuid.Parse(strings.TrimPrefix(line, "DESIGN_GRANDCHILD="))
		}
		if err != nil {
			t.Fatalf("parse browser-created snapshot identifier: %v\n%s", err, output)
		}
	}
	if childID == uuid.Nil || grandchildID == uuid.Nil {
		t.Fatalf("browser did not report both saved snapshots: %s", output)
	}
	t.Log(strings.TrimSpace(string(output)))
	return childID, grandchildID
}
