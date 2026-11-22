import { expect, type APIRequestContext } from "@playwright/test";
import { adminURL, type AdminSession } from "./target";

export interface ApiResponse<T> {
  status: number;
  headers: Record<string, string>;
  body: T;
}

function assertJson(contentType: string | undefined): void {
  expect(contentType ?? "", "JSON response content type").toContain("application/json");
}

export async function adminGet<T>(
  request: APIRequestContext,
  headers: Record<string, string>,
  path: string,
): Promise<ApiResponse<T>> {
  const response = await request.get(`${adminURL}${path}`, { headers });
  const contentType = response.headers()["content-type"];
  assertJson(contentType);
  const body = (await response.json()) as T;
  return { status: response.status(), headers: response.headers(), body };
}

export async function adminWrite<T>(
  request: APIRequestContext,
  headers: Record<string, string>,
  method: "POST" | "PUT" | "DELETE",
  path: string,
  options: { body?: unknown; extraHeaders?: Record<string, string> } = {},
): Promise<ApiResponse<T>> {
  const response = await request.fetch(`${adminURL}${path}`, {
    method,
    headers: { ...headers, ...(options.extraHeaders ?? {}) },
    data: options.body,
  });
  const text = await response.text();
  let body: T;
  try {
    body = JSON.parse(text) as T;
  } catch {
    body = text as unknown as T;
  }
  return { status: response.status(), headers: response.headers(), body };
}

export async function getFileContent(
  session: AdminSession,
  fileID: string,
): Promise<{ status: number; body: unknown; text: string }> {
  const response = await session.context.request.get(
    `${adminURL}/api/files/objects/${encodeURIComponent(fileID)}/content`,
    { headers: session.headers },
  );
  const text = await response.text();
  let body: unknown = text;
  try {
    body = JSON.parse(text) as unknown;
  } catch {
    // Non-JSON artifact bodies are returned verbatim.
  }
  return { status: response.status(), body, text };
}

interface RouteTarget {
  kind: string;
  default_locale?: string;
  locale_targets?: Array<{ locale: string; artifact_manifest_file_id: string }>;
}

interface RouteView {
  id: string;
  target: RouteTarget;
}

export async function getRoute(
  session: AdminSession,
  routeID: string,
): Promise<ApiResponse<RouteView>> {
  const response = await adminGet<RouteView>(
    session.context.request,
    session.headers,
    `/api/admin/router/routes/${encodeURIComponent(routeID)}`,
  );
  expect(response.status, `route ${routeID} status`).toBe(200);
  expect(response.body.id, `route ${routeID} identity`).toBe(routeID);
  return response;
}

export function localeManifest(route: RouteView, locale: string): string {
  const target = route.target;
  expect(target.kind, `route ${route.id} target kind`).toBe("static-site-artifact");
  const found = target.locale_targets?.find((entry) => entry.locale === locale);
  expect(found, `route ${route.id} locale ${locale}`).toBeTruthy();
  return found!.artifact_manifest_file_id;
}

export interface WorkspaceView {
  revision: number;
  selection_revision: number;
  published_revision: number;
}

export async function getWorkspace(
  session: AdminSession,
  snapshotID: string,
  locale: string,
): Promise<WorkspaceView> {
  const response = await adminGet<WorkspaceView>(
    session.context.request,
    session.headers,
    `/api/admin/content/snapshots/${encodeURIComponent(snapshotID)}/workspace?locale=${encodeURIComponent(locale)}`,
  );
  expect(response.status, `workspace ${locale} status`).toBe(200);
  return response.body;
}

export async function waitFor<T>(
  label: string,
  operation: () => Promise<T>,
  predicate: (value: T) => boolean | Promise<boolean>,
  timeoutMs = 150_000,
  intervalMs = 1_000,
): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let last: T | undefined;
  while (Date.now() < deadline) {
    last = await operation();
    if (await predicate(last)) {
      return last;
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(`${label} timed out after ${timeoutMs}ms; last value: ${JSON.stringify(last)}`);
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
