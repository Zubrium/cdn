package integration

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/environment"
)

func TestSubmissionsBrowserAcceptanceWithVitasilForm(t *testing.T) {
	requireSubmissionsUIDatabase(t)
	featureDir := os.Getenv("TEST_FEATURE_DIR")
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if featureDir == "" || node == "" || module == "" || browser == "" {
		t.Skip("set TEST_FEATURE_DIR and PRERENDER_TEST_NODE/MODULE/BROWSER for Vitasil submissions browser acceptance")
	}
	env := newTestEnv(t)
	archive, formVersion := readExternalFeatureArchive(t, featureDir, "form")
	formManifest := modelAcceptanceManifestFromArchive(t, archive)
	status, build, _ := env.rawRequest("POST", "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
	if status != 201 && status != 200 {
		t.Fatalf("register current Vitasil form@%s: status=%d body=%v", formVersion, status, build)
	}
	devConfig := modelAcceptanceBindManifestInputs(t, singlePageConfig("form", formVersion), []sitebuilder.Manifest{formManifest})
	devSnapshot := saveAndDeploySnapshot(t, env, devConfig, "Submissions browser development")

	// Give the second configured branch its own assigned snapshot so every
	// browser filter reads submissions that were validated against a real build.
	productionConfig := modelAcceptanceBindManifestInputs(t, singlePageConfig("form", formVersion), []sitebuilder.Manifest{formManifest})
	status, productionSnapshot, _ := env.request("POST", "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{
		"name":   "Submissions browser production",
		"config": productionConfig,
	})
	if status != 201 {
		t.Fatalf("create production-branch form snapshot: status=%d body=%v", status, productionSnapshot)
	}
	assignSnapshotToBranch(t, env, string(env.prodBranch), productionSnapshot["id"].(string))
	if devSnapshot["id"] == productionSnapshot["id"] {
		t.Fatal("branch fixtures must use independent snapshots")
	}

	moderatorToken := createSubmissionBrowserRole(t, env, "moderator")
	userToken := createSubmissionBrowserRole(t, env, "user")
	for role, token := range map[string]string{"moderator": moderatorToken, "user": userToken} {
		status, principal, _ := env.request("GET", "/api/v1/admin/me", token, nil)
		if status != 200 || principal["role"] != role {
			t.Fatalf("created %s submissions browser token authenticated as %v: status=%d", role, principal["role"], status)
		}
	}
	createSubmission := func(branch environment.Environment, name string, index int) string {
		t.Helper()
		status, body, _ := env.public(branch, "POST", "/api/v2/forms/form/submissions", map[string]any{
			"data": map[string]any{
				"name":           name,
				"email":          fmt.Sprintf("submission-%03d@example.test", index),
				"consent":        true,
				"consentVersion": "1",
			},
		})
		if status != 201 {
			t.Fatalf("submit Vitasil form on branch %s: status=%d body=%v", branch, status, body)
		}
		id, ok := body["id"].(string)
		if !ok || id == "" {
			t.Fatalf("form response has no submission ID: %v", body)
		}
		return id
	}

	ids := map[string]string{}
	ids["needle"] = createSubmission(env.devBranch, "Needle Ada", 0)
	ids["inProgress"] = createSubmission(env.devBranch, "Followup Contact", 1)
	ids["archived"] = createSubmission(env.devBranch, "Archived Contact", 2)
	ids["previousDay"] = createSubmission(env.devBranch, "Earlier Contact", 3)
	createSubmission(env.devBranch, "Archive From Browser", 4)
	for index := 5; index < 40; index++ {
		createSubmission(env.devBranch, fmt.Sprintf("Development Lead %02d", index), index)
	}
	ids["production"] = createSubmission(env.prodBranch, "Production Contact", 40)
	createSubmission(env.prodBranch, "Production Followup", 41)
	createSubmission(env.prodBranch, "Production Archive", 42)
	status, statusBody, _ := env.request("PATCH", "/api/v1/admin/submissions/"+ids["inProgress"]+"/status", env.adminToken, map[string]any{"status": "in_progress"})
	if status != 200 {
		t.Fatalf("prepare in-progress status filter: status=%d body=%v", status, statusBody)
	}
	status, archiveBody, _ := env.request("POST", "/api/v1/admin/submissions/"+ids["archived"]+"/archive", env.adminToken, nil)
	if status != 200 {
		t.Fatalf("prepare archived submissions filter: status=%d body=%v", status, archiveBody)
	}
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	if _, err := env.pool.Exec(context.Background(), `UPDATE form_submissions SET created_at=$2 WHERE id=$1::uuid`, ids["previousDay"], yesterday); err != nil {
		t.Fatalf("prepare date-range submission: %v", err)
	}
	var devCount, prodCount int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM form_submissions WHERE environment=$1`, string(env.devBranch)).Scan(&devCount); err != nil {
		t.Fatal(err)
	}
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM form_submissions WHERE environment=$1`, string(env.prodBranch)).Scan(&prodCount); err != nil {
		t.Fatal(err)
	}
	if devCount != 40 || prodCount != 3 {
		t.Fatalf("branch-scoped fixture count mismatch: development=%d production=%d", devCount, prodCount)
	}

	baseURL := strings.TrimRight(env.server.URL, "/")
	browserScript := `import { pathToFileURL } from "node:url";
import { readFile } from "node:fs/promises";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
const baseURL = process.argv[1];
const devBranch = process.argv[2];
const prodBranch = process.argv[3];
const needleID = process.argv[4];
const moderatorToken = process.argv[5];
const userToken = process.argv[6];
const today = process.argv[7];
const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1360, height: 900 } });
  page.setDefaultTimeout(10000);
  page.on("pageerror", error => errors.push(error.message));
  async function waitForRows(expected, timeout = 10000) {
    try {
      await page.waitForFunction(count => document.querySelectorAll(".data-table tbody tr").length === count, expected, { timeout });
    } catch (error) {
      const state = await page.evaluate(() => ({
        url: location.href,
        rows: document.querySelectorAll(".data-table tbody tr").length,
        body: document.body.innerText.slice(-1800),
      }));
      throw new Error("waiting for " + expected + " submission rows failed: " + JSON.stringify(state) + "; " + error.message);
    }
  }
  await page.addInitScript(token => { if (!localStorage.getItem("we_admin_token")) localStorage.setItem("we_admin_token", token); }, process.env.TEST_ADMIN_TOKEN);
  await page.goto(baseURL + "/admin/submissions", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Заявки", exact: true }).waitFor();
  await page.getByLabel("Форма").waitFor();
  await page.getByLabel("Ветка").waitFor();
  await page.getByLabel("Статус").waitFor();
  await page.getByLabel("Поле поиска").waitFor();
  await page.getByLabel("С", { exact: true }).waitFor();
  await page.getByLabel("По", { exact: true }).waitFor();

  // Pagination must continue the stable cursor without duplicating or skipping rows.
  const tableRows = page.locator(".data-table tbody tr");
  await tableRows.first().waitFor();
  const initialCount = await tableRows.count();
  if (initialCount !== 20) throw new Error("expected the configured first page of 20 submissions, got " + initialCount);
  const loadMore = page.getByRole("button", { name: "Загрузить ещё" });
  await loadMore.click();
  await waitForRows(40, 15000);
  await page.getByRole("button", { name: "Загрузить ещё" }).click();
  await waitForRows(42, 15000);
  if (await page.getByText("Все загруженные заявки показаны", { exact: true }).count() !== 1) throw new Error("pagination did not reach the end of the branch-aware list");

  // Keyboard operation and label associations for filters.
  const statusFilter = page.getByRole("combobox", { name: "Статус" });
  await statusFilter.focus();
  await page.keyboard.press("Space");
  const newStatusOption = page.getByRole("option", { name: "Новые", exact: true });
  await newStatusOption.waitFor();
  await newStatusOption.focus();
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => new URLSearchParams(location.search).get("status") === "new");
  await page.getByRole("button", { name: "Сбросить фильтры" }).click();

  // Branch and form filters retain only the selected branch's records.
  await page.getByRole("combobox", { name: "Ветка" }).click();
  await page.getByRole("option", { name: "Integration Production", exact: true }).click();
  await page.waitForFunction(expected => new URLSearchParams(location.search).get("branchId") === expected, prodBranch);
  await waitForRows(3);
  if (await page.locator(".data-table tbody tr").count() !== 3) throw new Error("branch filter included submissions from a different branch");
  await page.getByRole("combobox", { name: "Форма" }).click();
  await page.getByRole("option", { name: "Все формы", exact: true }).click();
  if (await page.locator(".data-table tbody tr").count() !== 3) throw new Error("all-forms selector returned an empty or cross-branch result");

  // Field search, status, date range and sort all compose and are reflected in the URL.
  await page.getByRole("button", { name: "Сбросить фильтры" }).click();
  await page.getByRole("combobox", { name: "Поле поиска" }).click();
  await page.getByRole("option", { name: "Имя", exact: true }).click();
  await page.getByRole("searchbox", { name: "Поиск заявок" }).fill("Needle");
  await page.waitForFunction(() => new URLSearchParams(location.search).get("q") === "Needle");
  await waitForRows(1);
  if (await page.locator(".data-table tbody tr").count() !== 1) throw new Error("search field filter did not isolate the matching submission");
  const row = page.locator(".data-table tbody tr").filter({ hasText: "Needle Ada" });
  await row.waitFor();
  await row.getByRole("link", { name: "Открыть", exact: true }).click();
  await page.getByRole("heading", { name: "Заявка", exact: true }).waitFor();
  if (!new URL(page.url()).pathname.endsWith("/" + needleID)) throw new Error("opening a submission lost its identity");

  let statusMutationReached = false;
  await page.route("**/api/v1/admin/submissions/*/status", async route => {
    statusMutationReached = true;
    await new Promise(resolve => setTimeout(resolve, 500));
    await route.continue();
  });
  const processed = page.getByRole("button", { name: "Обработана", exact: true });
  const statusResponse = page.waitForResponse(response =>
    new URL(response.url()).pathname === "/api/v1/admin/submissions/" + needleID + "/status" && response.request().method() === "PATCH",
    { timeout: 15000 },
  );
  await processed.click();
  await page.waitForFunction(() => [...document.querySelectorAll("button")].some(button => button.disabled && button.textContent?.includes("Обработана")), null, { timeout: 2000 });
  const statusSaved = await statusResponse;
  if (!statusSaved.ok()) throw new Error("status save failed: HTTP " + statusSaved.status());
  await page.getByText("Обработана", { exact: true }).waitFor({ timeout: 15000 });
  if (!statusMutationReached) throw new Error("status mutation did not reach the isolated API");
  await page.getByRole("button", { name: "К списку заявок" }).click();
  await page.getByRole("heading", { name: "Заявки", exact: true }).waitFor();
  const returnedFilters = new URL(page.url()).searchParams;
  if (returnedFilters.get("q") !== "Needle" || returnedFilters.get("field") !== "name") throw new Error("opening the submission detail lost its search filters: " + page.url());
  await page.goto(baseURL + "/admin/submissions", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Заявки", exact: true }).waitFor();
  await page.getByLabel("С", { exact: true }).fill(today);
  await page.getByLabel("По", { exact: true }).fill(today);
  await page.waitForFunction(() => new URLSearchParams(location.search).get("from") && new URLSearchParams(location.search).get("to"));
  await waitForRows(20);
  await page.getByRole("button", { name: "Загрузить ещё" }).click();
  await waitForRows(40);
  await page.getByRole("button", { name: "Загрузить ещё" }).click();
  await waitForRows(41);
  if (await page.locator(".data-table tbody tr").count() !== 41) throw new Error("date range did not exclude the previous-day submission");
  await page.getByRole("combobox", { name: "Сортировка" }).click();
  await page.getByRole("option", { name: "Сначала старые", exact: true }).click();
  await page.waitForFunction(() => new URLSearchParams(location.search).get("sort") === "asc");
  await page.getByRole("combobox", { name: "Статус" }).click();
  await page.getByRole("option", { name: "Обработанные", exact: true }).click();
  await page.waitForFunction(() => new URLSearchParams(location.search).get("status") === "processed");
  await waitForRows(1);
  if (await page.locator(".data-table tbody tr").count() !== 1) throw new Error("combined date/status filters returned the wrong results");
  await page.getByRole("button", { name: "Сбросить фильтры" }).click();

  // An empty result is recoverable with the visible reset action.
  await page.getByRole("searchbox", { name: "Поиск заявок" }).fill("no-such-submission-5a02");
  await page.getByText("Заявки не найдены", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Сбросить фильтры" }).click();
  await tableRows.first().waitFor();

  // Archived entries are hidden by default and become visible only when requested.
  await page.goto(baseURL + "/admin/submissions?form=form&q=Archived%20Contact&field=name", { waitUntil: "domcontentloaded" });
  await page.getByText("Заявки не найдены", { exact: true }).waitFor();
  await page.goto(baseURL + "/admin/submissions?form=form&q=Archived%20Contact&field=name&archived=1", { waitUntil: "domcontentloaded" });
  await page.locator(".data-table tbody tr").filter({ hasText: "Archived Contact" }).waitFor();
  if (await page.locator(".data-table tbody tr").count() !== 1) throw new Error("archived filter did not expose the archived submission");

  // Archive requires explicit confirmation and removes the entry from the default list.
  await page.goto(baseURL + "/admin/submissions?form=form&q=Archive%20From%20Browser&field=name", { waitUntil: "domcontentloaded" });
  const archiveRow = page.locator(".data-table tbody tr").filter({ hasText: "Archive From Browser" });
  await archiveRow.getByRole("link", { name: "Открыть", exact: true }).click();
  await page.getByRole("button", { name: "В архив", exact: true }).click();
  const confirmation = page.getByRole("alertdialog");
  await confirmation.getByText("Заявка исчезнет из списка и из экспорта", { exact: false }).waitFor();
  await confirmation.getByRole("button", { name: "В архив", exact: true }).click();
  await page.getByRole("heading", { name: "Заявки", exact: true }).waitFor();
  await page.getByText("Заявки не найдены", { exact: true }).waitFor();

  // Admin, moderator and user can list and export; hard deletion stays admin-only.
  async function verifyRole(token, role, canDelete) {
    await page.evaluate(value => localStorage.setItem("we_admin_token", value), token);
    await page.goto(baseURL + "/admin/submissions?form=form&branchId=" + prodBranch, { waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "Заявки", exact: true }).waitFor();
    const authenticated = await page.evaluate(async () => {
      const response = await fetch("/api/v1/admin/me", { headers: { Authorization: "Bearer " + localStorage.getItem("we_admin_token") } });
      return { status: response.status, body: await response.json() };
    });
    if (authenticated.status !== 200 || authenticated.body.role !== role) throw new Error(role + " browser session did not authenticate with its intended role: " + JSON.stringify(authenticated));
    await page.locator(".data-table tbody tr").first().waitFor();
    if (await page.locator(".data-table tbody tr").count() !== 3) throw new Error(role + " cannot read branch-scoped submissions");
    if (await page.getByRole("button", { name: "Экспорт CSV", exact: true }).count() !== 1) throw new Error(role + " cannot access CSV export");
    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("button", { name: "Экспорт CSV", exact: true }).click();
    const download = await downloadPromise;
    const csvPath = await download.path();
    const csv = await readFile(csvPath, "utf8");
    const csvLines = csv.trim().split(/\r?\n/);
    if (csvLines.length !== 4 || !csv.includes("Production Contact") || !csv.includes(prodBranch)) throw new Error(role + " CSV export does not match the branch-filtered list");
    await page.locator(".data-table tbody tr").first().getByRole("link", { name: "Открыть", exact: true }).click();
    await page.getByRole("heading", { name: "Заявка", exact: true }).waitFor();
    const deleteCount = await page.getByRole("button", { name: "Удалить", exact: true }).count();
    if (canDelete ? deleteCount !== 1 : deleteCount !== 0) throw new Error(role + " hard-delete control mismatch: count=" + deleteCount);
  }
  await verifyRole(process.env.TEST_ADMIN_TOKEN, "admin", true);
  await verifyRole(moderatorToken, "moderator", false);
  await verifyRole(userToken, "user", false);

  // User can operate the list at a mobile viewport with labelled, keyboardable filters.
  const mobile = await browser.newPage({ viewport: { width: 390, height: 844 } });
  mobile.setDefaultTimeout(10000);
  mobile.on("pageerror", error => errors.push(error.message));
  await mobile.addInitScript(token => localStorage.setItem("we_admin_token", token), userToken);
  await mobile.goto(baseURL + "/admin/submissions?form=form&branchId=" + devBranch + "&field=name&q=Needle", { waitUntil: "domcontentloaded" });
  await mobile.getByRole("heading", { name: "Заявки", exact: true }).waitFor();
  await mobile.getByRole("link").filter({ hasText: "Needle Ada" }).waitFor();
  if (await mobile.locator("table").isVisible()) throw new Error("desktop table remained visible at the mobile breakpoint");
  if (!(await mobile.getByRole("searchbox", { name: "Поиск заявок" }).isVisible())) throw new Error("mobile search control is not usable");

  // Verify initial loading, a recoverable network error, and retry without mutating data.
  const loadingPage = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  await loadingPage.addInitScript(token => localStorage.setItem("we_admin_token", token), userToken);
  await loadingPage.route("**/api/v1/admin/submissions*", async route => {
    if (new URL(route.request().url()).pathname !== "/api/v1/admin/submissions") return route.continue();
    await new Promise(resolve => setTimeout(resolve, 700));
    await route.continue();
  });
  await loadingPage.goto(baseURL + "/admin/submissions", { waitUntil: "domcontentloaded" });
  await loadingPage.getByRole("heading", { name: "Заявки", exact: true }).waitFor();
  await loadingPage.locator('[aria-hidden="true"] .animate-pulse').first().waitFor({ timeout: 1000 });
  await loadingPage.locator(".data-table tbody tr").first().waitFor();

  const errorPage = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  await errorPage.addInitScript(token => localStorage.setItem("we_admin_token", token), userToken);
  let failedAttempts = 0;
  await errorPage.route("**/api/v1/admin/submissions*", async route => {
    if (new URL(route.request().url()).pathname !== "/api/v1/admin/submissions") return route.continue();
    if (++failedAttempts <= 3) {
      return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "qa_unavailable", message: "Временный отказ изолированного тестового API" } }) });
    }
    await route.continue();
  });
  await errorPage.goto(baseURL + "/admin/submissions", { waitUntil: "domcontentloaded" });
  const errorAlert = errorPage.getByRole("alert");
  await errorAlert.getByText("Не удалось загрузить заявки", { exact: true }).waitFor({ timeout: 10000 });
  await errorAlert.getByRole("button", { name: "Повторить", exact: true }).click();
  await errorPage.locator(".data-table tbody tr").first().waitFor();
  if (errors.length) throw new Error("submissions browser raised runtime exceptions: " + errors.join(" | "));
  console.log("SUBMISSIONS_BROWSER=PASS; vitasilForm=" + process.env.TEST_FEATURE_ZIP + "; rows=43; roles=admin,moderator,user; branches=2");
} finally {
  await browser.close();
}`
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", browserScript,
		baseURL,
		string(env.devBranch),
		string(env.prodBranch),
		ids["needle"],
		moderatorToken,
		userToken,
		time.Now().UTC().Format("2006-01-02"),
	)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browser,
		"TEST_ADMIN_TOKEN="+env.adminToken,
		"TEST_FEATURE_ZIP=form-"+formVersion+".zip",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Vitasil form submissions browser acceptance failed: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Log(strings.TrimSpace(string(output)))
	}
}

func createSubmissionBrowserRole(t *testing.T, env *testEnv, role string) string {
	t.Helper()
	status, body, _ := env.request("POST", "/api/v1/admin/tokens", env.adminToken, map[string]any{"name": "submissions browser " + role, "role": role})
	if status != 201 {
		t.Fatalf("create %s submissions browser token: status=%d body=%v", role, status, body)
	}
	token, ok := body["token"].(string)
	if !ok || token == "" {
		t.Fatalf("create %s token response contains no credential: %v", role, body)
	}
	return token
}

func requireSubmissionsUIDatabase(t *testing.T) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("invalid TEST_DATABASE_URL: %v", err)
	}
	host := parsed.Hostname()
	parsedIP := net.ParseIP(host)
	if host != "localhost" && (parsedIP == nil || !parsedIP.IsLoopback()) {
		t.Fatalf("submissions UI integration requires a loopback disposable database, got %q", host)
	}
	database := strings.Trim(parsed.Path, "/")
	if !strings.HasPrefix(database, "website_engine_submissions_ui_") || strings.HasSuffix(database, "website_engine_submissions_ui_") {
		t.Skipf("submissions UI acceptance requires its own unique website_engine_submissions_ui_* disposable database; current database is %q", database)
	}
}
