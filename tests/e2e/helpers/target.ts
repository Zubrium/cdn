import { expect } from "@playwright/test";

export function requiredEnv(name: string): string {
  const value = process.env[name];
  if (!value || !value.trim()) {
    throw new Error(`missing required E2E environment variable ${name}`);
  }
  return value;
}

export const adminURL = requiredEnv("E2E_ADMIN_URL").replace(/\/+$/, "");
export const publicURL = requiredEnv("E2E_PUBLIC_URL").replace(/\/+$/, "");
export const expectedBuildIdentity = requiredEnv("E2E_EXPECTED_BUILD_IDENTITY");
export const credential = requiredEnv("E2E_BOOTSTRAP_CREDENTIAL");
export const runId = requiredEnv("E2E_RUN_ID");

export interface RunState {
  runId: string;
  pageID: string;
  snapshotID: string;
  fileSetID: string;
  sourceFileID: string;
  featureKey: string;
  featureVersion: string;
  formSchemaID: string;
  locales: string[];
  routes: Array<{
    id: string;
    path: string;
    initial: Record<string, string>;
  }>;
}

export async function loadRunState(): Promise<RunState> {
  const path = requiredEnv("E2E_STATE_FILE");
  const file = await import("node:fs/promises").then((fs) => fs.readFile(path, "utf8"));
  const state = JSON.parse(file) as RunState;
  if (!state.pageID || !state.snapshotID || state.routes.length === 0 || state.locales.length === 0) {
    throw new Error("E2E run state is incomplete");
  }
  return state;
}

export interface AdminSession {
  headers: Record<string, string>;
  context: import("@playwright/test").BrowserContext;
  page: import("@playwright/test").Page;
}

export async function loginAdmin(browser: import("@playwright/test").Browser): Promise<AdminSession> {
  const context = await browser.newContext({
    baseURL: adminURL,
    extraHTTPHeaders: { Origin: adminURL },
  });
  const page = await context.newPage();
  await page.goto("/admin/login");
  await page.locator("#credential").fill(credential);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.waitForURL((url) => !url.pathname.endsWith("/login"), { timeout: 30_000 });

  const cookies = await context.cookies();
  const csrf = cookies.find((entry) => entry.name === "zubrium_admin_csrf");
  if (!csrf?.value) {
    throw new Error("admin login did not issue a CSRF cookie");
  }
  const headers = {
    Origin: adminURL,
    "X-CSRF-Token": csrf.value,
    Accept: "application/json",
  };
  await context.setExtraHTTPHeaders(headers);

  const health = await context.request.get(`${adminURL}/healthz`, {
    headers: { "Cache-Control": "no-store" },
  });
  expect(health.status(), "admin /healthz status").toBe(204);
  expect(health.headers()["x-zubrium-build-identity"], "admin build identity header").toBe(
    expectedBuildIdentity,
  );

  return { headers, context, page };
}

export function idempotencyKey(kind: string): string {
  return `e2e-${runId}-${kind}`.replace(/[^A-Za-z0-9._:-]/g, "-").slice(0, 128);
}
