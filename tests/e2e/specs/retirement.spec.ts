import { expect, test } from "@playwright/test";
import { adminGet, adminWrite, getRoute, waitFor } from "../helpers/api";
import { adminURL, loadRunState, loginAdmin, publicURL } from "../helpers/target";

interface PageView {
  id: string;
  lifecycle_state: string;
  latest_snapshot_id: string;
  retirement_job_id?: string;
}

interface RetirementRoute {
  route_id: string;
  state: string;
  error_code?: string;
}

interface RetirementView {
  id: string;
  page_id: string;
  state: string;
  routes: RetirementRoute[];
}

test("E2E-RET-001 retires all route targets without deleting page-owned data", async ({ browser }) => {
  const state = await loadRunState();
  const session = await loginAdmin(browser);
  const publicContext = await browser.newContext();

  const before = await adminGet<PageView>(
    session.context.request,
    session.headers,
    `/api/admin/pages/${encodeURIComponent(state.pageID)}`,
  );
  expect(before.status, "page read before retirement").toBe(200);
  expect(before.body.id, "page identity before retirement").toBe(state.pageID);
  const ifMatch = before.headers.etag;
  expect(ifMatch, "page ETag for If-Match").toMatch(/^"[1-9][0-9]*"$/);

  const key = `e2e-${state.runId}-retire-${state.pageID}`.slice(0, 128);
  const first = await adminWrite<RetirementView>(
    session.context.request,
    session.headers,
    "DELETE",
    `/api/admin/pages/${encodeURIComponent(state.pageID)}`,
    { extraHeaders: { "If-Match": ifMatch!, "Idempotency-Key": key } },
  );
  expect(first.status, "retirement accepted status").toBe(202);
  expect(first.body.page_id, "retirement page identity").toBe(state.pageID);
  expect(first.body.id, "retirement job identity").toBeTruthy();
  expect(["queued", "running", "succeeded"], "retirement initial state").toContain(first.body.state);
  for (const route of state.routes) {
    const entry = first.body.routes.find((item) => item.route_id === route.id);
    expect(entry, `retirement covers route ${route.id}`).toBeTruthy();
    expect(["pending", "removed", "retryable_failed"], `retirement route state ${route.id}`).toContain(
      entry!.state,
    );
  }

  const repeated = await adminWrite<RetirementView>(
    session.context.request,
    session.headers,
    "DELETE",
    `/api/admin/pages/${encodeURIComponent(state.pageID)}`,
    { extraHeaders: { "If-Match": ifMatch!, "Idempotency-Key": key } },
  );
  expect(repeated.status, "retirement replay status").toBe(202);
  expect(repeated.body.id, "retirement replay is idempotent").toBe(first.body.id);

  const reuse = await adminWrite<unknown>(
    session.context.request,
    session.headers,
    "DELETE",
    `/api/admin/pages/${encodeURIComponent(state.pageID)}`,
    {
      extraHeaders: {
        "If-Match": ifMatch!,
        "Idempotency-Key": `e2e-${state.runId}-retire-reuse-${state.pageID}`.slice(0, 128),
      },
    },
  );
  expect([404, 409, 410], "retirement with a new idempotency key must be refused").toContain(
    reuse.status,
  );

  if (first.headers.location) {
    const location = new URL(first.headers.location, adminURL);
    await waitFor(
      "retirement workflow",
      async () =>
        adminGet<RetirementView>(
          session.context.request,
          session.headers,
          `${location.pathname}${location.search}`,
        ),
      (response) => response.status === 200 && response.body.state === "succeeded",
      180_000,
    );
  } else {
    await waitFor(
      "page lifecycle after retirement",
      async () =>
        adminGet<PageView>(
          session.context.request,
          session.headers,
          `/api/admin/pages/${encodeURIComponent(state.pageID)}`,
        ),
      (response) => response.body.lifecycle_state === "retired",
      180_000,
    );
  }

  for (const route of state.routes) {
    for (const locale of state.locales) {
      await waitFor(
        `withdrawal ${route.path}/${locale}`,
        async () =>
          publicContext.request.get(`${publicURL}${route.path}`, {
            headers: { "Accept-Language": locale },
          }),
        async (response) => {
          if (response.status() === 200) {
            const body = await response.text();
            return !body.includes('static-page-artifact') && !body.includes('data-build="');
          }
          return true;
        },
        180_000,
      );
    }
    const withdrawn = await waitFor(
      `route target withdrawal ${route.id}`,
      async () => getRoute(session, route.id),
      (response) => response.body.target.kind !== "static-site-artifact",
      180_000,
    );
    expect(withdrawn.body.target.kind, `route ${route.path} target after retirement`).not.toBe(
      "static-site-artifact",
    );
  }

  const page = await adminGet<PageView>(
    session.context.request,
    session.headers,
    `/api/admin/pages/${encodeURIComponent(state.pageID)}`,
  );
  expect(page.status, "page retained after retirement").toBe(200);
  expect(page.body.id, "page ID not reused").toBe(state.pageID);
  expect(page.body.latest_snapshot_id, "snapshot retained after retirement").toBe(
    before.body.latest_snapshot_id,
  );

  const snapshot = await adminGet<unknown>(
    session.context.request,
    session.headers,
    `/api/admin/snapshots/${encodeURIComponent(state.snapshotID)}`,
  );
  expect(snapshot.status, "snapshot retained after retirement").toBe(200);

  for (const locale of state.locales) {
    const workspace = await adminGet<unknown>(
      session.context.request,
      session.headers,
      `/api/admin/content/snapshots/${encodeURIComponent(state.snapshotID)}/workspace?locale=${encodeURIComponent(locale)}`,
    );
    expect(workspace.status, `content workspace retained for ${locale}`).toBe(200);
  }

  const form = await adminGet<unknown>(
    session.context.request,
    session.headers,
    `/api/admin/forms/schemas/${encodeURIComponent(state.formSchemaID)}`,
  );
  expect(form.status, "form schema retained after retirement").toBe(200);

  const file = await session.context.request.get(
    `${adminURL}/api/files/objects/${encodeURIComponent(state.sourceFileID)}`,
    { headers: session.headers },
  );
  expect(file.status(), "source file retained after retirement").toBe(200);
});
