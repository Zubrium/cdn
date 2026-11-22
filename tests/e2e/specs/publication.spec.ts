import { expect, test } from "@playwright/test";
import {
  adminWrite,
  getFileContent,
  getRoute,
  getWorkspace,
  localeManifest,
  sleep,
  waitFor,
} from "../helpers/api";
import { loadRunState, loginAdmin, publicURL } from "../helpers/target";

interface Manifest {
  format?: string;
  document?: { file_id?: string };
}

test("E2E-PUB-001 publishes one locale across every configured route target", async ({ browser }) => {
  const state = await loadRunState();
  const session = await loginAdmin(browser);
  const publicContext = await browser.newContext();

  const initial = await Promise.all(
    state.routes.map(async (route) => {
      const view = await getRoute(session, route.id);
      return {
        route,
        en: localeManifest(view.body, "en-US"),
        de: localeManifest(view.body, "de-DE"),
      };
    }),
  );
  for (const entry of initial) {
    expect(entry.en, `initial en-US artifact on ${entry.route.path}`).toBe(
      entry.route.initial["en-US"],
    );
    expect(entry.de, `initial de-DE artifact on ${entry.route.path}`).toBe(
      entry.route.initial["de-DE"],
    );
  }

  const workspace = await getWorkspace(session, state.snapshotID, "en-US");
  const publish = await adminWrite<{ published_revision?: number }>(
    session.context.request,
    session.headers,
    "POST",
    `/api/admin/content/snapshots/${encodeURIComponent(state.snapshotID)}/workspace/publish?locale=en-US`,
    {
      body: {
        expected_revision: workspace.revision,
        expected_selection_revision: workspace.selection_revision,
      },
      extraHeaders: { "Idempotency-Key": `e2e-${state.runId}-pub-en` },
    },
  );
  expect(publish.status, "content publication status").toBe(200);

  const activated = await waitFor(
    "en-US fan-out activation",
    async () =>
      Promise.all(
        state.routes.map(async (route) => {
          const view = await getRoute(session, route.id);
          return { path: route.path, manifest: localeManifest(view.body, "en-US") };
        }),
      ),
    (routes) => routes.every((route) => route.manifest !== routePathInitial(route.path)),
    180_000,
  );

  for (const route of activated) {
    const original = initial.find((entry) => entry.route.path === route.path)!;
    expect(route.manifest, `published en-US artifact on ${route.path}`).not.toBe(original.en);
  }

  const unchanged = await Promise.all(
    state.routes.map(async (route) => {
      const view = await getRoute(session, route.id);
      return localeManifest(view.body, "de-DE");
    }),
  );
  unchanged.forEach((manifest, index) => {
    expect(manifest, `de-DE target on ${state.routes[index].path}`).toBe(
      state.routes[index].initial["de-DE"],
    );
  });

  for (const route of state.routes) {
    const view = await getRoute(session, route.id);
    const enManifestID = localeManifest(view.body, "en-US");
    const deManifestID = localeManifest(view.body, "de-DE");
    expect(enManifestID, "locale-specific en-US manifest identity").not.toBe(deManifestID);

    const enManifest = await getFileContent(session, enManifestID);
    expect(enManifest.status, "en-US manifest read").toBe(200);
    expect((enManifest.body as Manifest).format, "artifact format").toBe("static-page-artifact/v1");
    const documentID = (enManifest.body as Manifest).document?.file_id;
    expect(documentID, "manifest document file id").toBeTruthy();
    const document = await getFileContent(session, documentID!);
    expect(document.status, "en-US document read").toBe(200);
    expect(document.text, "en-US locale marker in artifact document").toContain('lang="en-US"');
    expect(document.text, "rendered locale marker in artifact document").toContain("data-rendered=\"en-US\"");

    const en = await publicContext.request.get(`${publicURL}${route.path}`, {
      headers: { "Accept-Language": "en-US" },
    });
    expect(en.status(), `public en-US GET ${route.path}`).toBe(200);
    expect(await en.text(), `public en-US locale ${route.path}`).toContain('lang="en-US"');

    const de = await publicContext.request.get(`${publicURL}${route.path}`, {
      headers: { "Accept-Language": "de-DE" },
    });
    expect(de.status(), `public de-DE GET ${route.path}`).toBe(200);
    expect(await de.text(), `public de-DE locale ${route.path}`).toContain('lang="de-DE"');
  }

  const afterPublish = await getWorkspace(session, state.snapshotID, "en-US");
  const staleDraft = await adminWrite(
    session.context.request,
    session.headers,
    "PUT",
    `/api/admin/content/snapshots/${encodeURIComponent(state.snapshotID)}/workspace?locale=en-US`,
    {
      body: {
        expected_revision: afterPublish.revision,
        expected_selection_revision: afterPublish.selection_revision,
        records: [],
        selections: [],
      },
      extraHeaders: { "Idempotency-Key": `e2e-${state.runId}-stale-draft` },
    },
  );
  expect(staleDraft.status, "unpublished draft save status").toBe(200);
  await sleep(3_000);
  for (const route of state.routes) {
    const view = await getRoute(session, route.id);
    expect(localeManifest(view.body, "en-US"), `stale output must stay inactive on ${route.path}`).toBe(
      activated.find((entry) => entry.path === route.path)!.manifest,
    );
  }

  function routePathInitial(path: string): string {
    return initial.find((entry) => entry.route.path === path)!.en;
  }
});
