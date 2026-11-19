import { afterEach, describe, expect, it, vi } from "vitest";
import { api, getSyntheticDataMode, subscribeSyntheticDataMode } from "../../../../frontend/admin/src/api";
import type { FormFieldDefinition, MediaRenditionContext, SiteConfig } from "../../../../frontend/admin/src/types";

function stubAdminBrowser(fetchMock: ReturnType<typeof vi.fn>) {
  vi.stubGlobal("window", {
    location: { origin: "http://localhost" },
    localStorage: { getItem: () => null },
    dispatchEvent: () => true,
  });
  vi.stubGlobal("document", { cookie: `zubrium_admin_csrf=${"a".repeat(43)}` });
  vi.stubGlobal("fetch", fetchMock);
}

describe("Website pagination API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("loads every snapshot page without repeating or guessing cursors", async () => {
    const fetchMock = vi.fn()
      .mockImplementationOnce(async () => new Response(JSON.stringify({
        items: [{ id: "snapshot-1", site_id: "site-1", page_id: "home", name: "One", sequence: 1, revision: 1, feature_pins: {}, state: "ready" }],
        next_cursor: "cursor-2",
      }), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockImplementationOnce(async () => new Response(JSON.stringify({
        items: [{ id: "snapshot-2", site_id: "site-1", page_id: "home", name: "Two", sequence: 2, revision: 1, feature_pins: {}, state: "ready" }],
        next_cursor: null,
      }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      localStorage: { getItem: () => null },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("fetch", fetchMock);

    const snapshots = await api.website.allSnapshots();
    const [firstInput] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const [secondInput] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(new URL(firstInput, "http://localhost").searchParams.has("cursor")).toBe(false);
    expect(new URL(secondInput, "http://localhost").searchParams.get("cursor")).toBe("cursor-2");
    expect(snapshots.map((snapshot) => snapshot.id)).toEqual(["snapshot-1", "snapshot-2"]);
  });

  it("does not send picker accept metadata to the rendition endpoint", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ status: "ready" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      localStorage: { getItem: () => null },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("fetch", fetchMock);

    await api.media.prepareRenditions("media-id", {
      snapshotId: "snapshot-id",
      featureKey: "product-card",
      buildVersion: "2.1.0",
      schemaKind: "dataInputs",
      fieldPath: "/product/image",
      buildDigest: "sha256:build",
      accept: "raster-image",
    } as MediaRenditionContext);

    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(String(init.body))).not.toHaveProperty("accept");
  });
});

describe("Forms schema API contract", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("round-trips explicit field types and sends the optimistic revision", async () => {
    const fields: Record<string, FormFieldDefinition> = {
      name: { type: "text", required: true, min_length: 2, max_length: 80 },
      email: { type: "email", required: true },
      age: { type: "number", required: false, minimum: 18, maximum: 120 },
      consent: { type: "boolean", required: true },
      topic: { type: "select", required: false, options: ["Sales", "Support"] },
      attachment: { type: "file", required: false, max_files: 2, max_file_bytes: 5242880, allowed_content_types: ["application/pdf"] },
    };
    const schema = {
      id: "44444444-4444-4444-8444-444444444444",
      key: "contact_request",
      version: 2,
      revision: 8,
      fields,
    };
    const fetchMock = vi.fn(async () => new Response(JSON.stringify(schema), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    stubAdminBrowser(fetchMock);

    const saved = await api.formsEngine.saveSchema({
      snapshot_id: "22222222-2222-4222-8222-222222222222",
      schema_id: schema.id,
      key: schema.key,
      fields,
      expected_revision: 7,
    });

    expect(saved).toEqual(schema);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v2/admin/forms/schemas");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toMatchObject({ expected_revision: 7, schema_id: schema.id, snapshot_id: "22222222-2222-4222-8222-222222222222" });
  });

  it("rejects an invalid server schema instead of exposing malformed constraints", async () => {
    const invalid = {
      id: "44444444-4444-4444-8444-444444444444",
      key: "contact_request",
      version: 1,
      revision: 1,
      fields: { attachment: { type: "file", required: false, max_files: 1, max_file_bytes: 1024, allowed_content_types: ["IMAGE/SVG+XML"] } },
    };
    stubAdminBrowser(vi.fn(async () => new Response(JSON.stringify(invalid), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })));

    await expect(api.formsEngine.schema("22222222-2222-4222-8222-222222222222", invalid.id)).rejects.toMatchObject({ code: "invalid_response" });
  });
});

describe("development fixture response mode", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("announces the synthetic engine profile from the Core-backed admin session response", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ principal: {
      id: "principal-1", name: "Operator", active: true,
      can_manage_administrators: true, group_access: { features: 2 },
    } }), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "X-Zubrium-Data-Mode": "synthetic",
      },
    }));
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      localStorage: { getItem: () => null },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("fetch", fetchMock);
    const notify = vi.fn();
    const unsubscribe = subscribeSyntheticDataMode(notify);

    try {
      const principal = await api.auth.me();
      expect(principal.id).toBe("principal-1");
      expect(fetchMock).toHaveBeenCalledWith("/api/v2/admin/session", expect.any(Object));
      expect(getSyntheticDataMode()).toBe(true);
      expect(notify).toHaveBeenCalledTimes(1);
    } finally {
      unsubscribe();
    }
  });
});

describe("identity and browser session API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("exchanges a one-time credential for a cookie session without persisting it", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ principal: {
      id: "principal-1", name: "Operator", active: true,
      can_manage_administrators: true, group_access: { features: 2 },
    } }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    const getItem = vi.fn(() => null);
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      localStorage: { getItem },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("fetch", fetchMock);

    const principal = await api.auth.login("secret-credential");

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/v2/admin/session");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
    expect(JSON.parse(String(init.body))).toEqual({ credential: "secret-credential" });
    expect(principal.group_access.features).toBe(2);
    expect(getItem).not.toHaveBeenCalled();
  });

  it("sends CSRF proof on cookie-authenticated writes", async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("document", { cookie: `zubrium_admin_csrf=${"a".repeat(43)}` });
    vi.stubGlobal("fetch", fetchMock);

    await api.auth.logout();

    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe("DELETE");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
  });
});

describe("Coordinator jobs API", () => {
  afterEach(() => vi.unstubAllGlobals());

  function stubBrowser(fetchMock: ReturnType<typeof vi.fn>) {
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("fetch", fetchMock);
  }

  it("sends filters and the opaque continuation cursor", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      items: [],
      next_cursor: "opaque-cursor",
    }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    stubBrowser(fetchMock);

    const page = await api.coordinator.jobs.list({
      type: "archive.inspect/v1",
      state: "failed",
      limit: 50,
      before: "cursor+with/special=chars",
    });

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(input, "http://localhost");
    expect(url.pathname).toBe("/api/v2/coordinator/jobs");
    expect(url.searchParams.get("type")).toBe("archive.inspect/v1");
    expect(url.searchParams.get("state")).toBe("failed");
    expect(url.searchParams.get("limit")).toBe("50");
    expect(url.searchParams.get("before")).toBe("cursor+with/special=chars");
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
    expect(init.credentials).toBe("same-origin");
    expect(page.next_cursor).toBe("opaque-cursor");
  });

  it("reads retry permission and retries only through the explicit endpoint", async () => {
    const fetchMock = vi.fn()
      .mockImplementationOnce(async () => new Response(JSON.stringify({ can_retry: false }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }))
      .mockImplementationOnce(async () => new Response(null, { status: 204 }));
    stubBrowser(fetchMock);

    await expect(api.coordinator.jobs.permissions()).resolves.toEqual({ can_retry: false });
    await expect(api.coordinator.jobs.retry("job/id with space")).resolves.toBeUndefined();

    const [permissionsInput, permissionsInit] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const [retryInput, retryInit] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(permissionsInput).toBe("/api/v2/coordinator/jobs/permissions");
    expect(new Headers(permissionsInit.headers).get("Authorization")).toBeNull();
    expect(permissionsInit.credentials).toBe("same-origin");
    expect(retryInput).toBe("/api/v2/coordinator/jobs/job%2Fid%20with%20space/retry");
    expect(retryInit.method).toBe("POST");
    expect(retryInit.credentials).toBe("same-origin");
    expect(retryInit.body).toBeUndefined();
  });
});

describe("Core-backed file upload API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends upload intent to Loader with explicit access groups and no visibility flag", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      id: "upload-1", state: "uploading", partSize: 8, parts: [], expiresAt: "2026-11-18T12:00:00Z",
    }), { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("window", { location: { origin: "http://localhost" }, dispatchEvent: () => true });
    vi.stubGlobal("document", { cookie: `zubrium_admin_csrf=${"a".repeat(43)}` });
    vi.stubGlobal("fetch", fetchMock);

    await api.media.createUpload({
      name: "asset.bin", size: 7, sha256: "a".repeat(64),
      contentType: "application/octet-stream", groups: ["content_assets"],
    }, "upload-intent-0000000001");

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/v2/files/uploads");
    expect(JSON.parse(String(init.body))).toEqual({
      name: "asset.bin", size: 7, sha256: "a".repeat(64),
      contentType: "application/octet-stream", groups: ["content_assets"],
    });
    expect(JSON.parse(String(init.body))).not.toHaveProperty("visibility");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("upload-intent-0000000001");
  });
});

describe("Website feature catalog API", () => {
  afterEach(() => vi.unstubAllGlobals());

  function stubBrowser(fetchMock: ReturnType<typeof vi.fn>) {
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("document", { cookie: `zubrium_admin_csrf=${"a".repeat(43)}` });
    vi.stubGlobal("fetch", fetchMock);
  }

  function encodedManifest(key: string, name: string, version: string) {
    const manifest = JSON.stringify({
      formatVersion: 2,
      key,
      name,
      version,
      engineApi: "2",
      slots: ["main"],
      arguments: { type: "object", properties: {} },
      dataInputs: [],
    });
    return btoa(Array.from(new TextEncoder().encode(manifest), (byte) => String.fromCharCode(byte)).join(""));
  }

  it("loads every catalog page and decodes immutable manifest metadata", async () => {
    const fetchMock = vi.fn()
      .mockImplementationOnce(async () => new Response(JSON.stringify({
        items: [{
          feature_id: "feature-1",
          version: "1.2.0",
          manifest: encodedManifest("header", "Заголовок", "1.2.0"),
          file_set_id: "set-1",
          files: { "entry.js": "file-1" },
          digest: "a".repeat(64),
          artifact_size: 321,
          created_at: "2026-11-18T10:00:00Z",
          state: "ready",
        }],
        next_cursor: "cursor-2",
      }), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockImplementationOnce(async () => new Response(JSON.stringify({
        items: [{
          feature_id: "feature-2",
          version: "2.0.0",
          manifest: encodedManifest("footer", "Подвал", "2.0.0"),
          file_set_id: "set-2",
          files: { "entry.js": "file-2" },
          digest: "b".repeat(64),
          artifact_size: 456,
          created_at: "2026-11-18T11:00:00Z",
          state: "ready",
        }],
        next_cursor: null,
      }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubBrowser(fetchMock);

    const result = await api.website.featureBuilds();

    expect(result.items.map(({ key, name, version }) => [key, name, version])).toEqual([
      ["header", "Заголовок", "1.2.0"],
      ["footer", "Подвал", "2.0.0"],
    ]);
    expect(result.items[0]?.schemaVersionId).toBe("set-1");
    expect(result.items[0]?.manifest.key).toBe("header");
    const [firstInput, firstInit] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const [secondInput, secondInit] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(new URL(firstInput, "http://localhost").pathname).toBe("/api/v2/admin/sites/features");
    expect(new URL(firstInput, "http://localhost").searchParams.has("limit")).toBe(false);
    expect(new URL(secondInput, "http://localhost").searchParams.get("cursor")).toBe("cursor-2");
    expect(new Headers(firstInit.headers).get("Authorization")).toBeNull();
    expect(firstInit.credentials).toBe("same-origin");
    expect(new Headers(secondInit.headers).get("Authorization")).toBeNull();
  });

  it("begins an import from a Core file ID with cookie CSRF and idempotency", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      id: "job-1",
      source_file_id: "file-1",
      state: "queued",
      source_deleted: false,
      created_at: "2026-11-18T10:00:00Z",
      updated_at: "2026-11-18T10:00:00Z",
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    stubBrowser(fetchMock);

    await api.website.beginFeatureImport("file-1", "feature-import:request-0001", "c".repeat(64));

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/v2/admin/sites/feature-imports");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("feature-import:request-0001");
    expect(JSON.parse(String(init.body))).toEqual({ source_file_id: "file-1", expected_sha256: "c".repeat(64) });
  });

  it("reads import job state through its durable resource URL", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      id: "11111111-1111-4111-8111-111111111111",
      source_file_id: "file-1",
      state: "succeeded",
      source_deleted: true,
      created_at: "2026-11-18T10:00:00Z",
      updated_at: "2026-11-18T10:01:00Z",
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubBrowser(fetchMock);

    await api.website.featureImport("11111111-1111-4111-8111-111111111111");

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/v2/admin/sites/feature-imports/11111111-1111-4111-8111-111111111111");
    expect(init.method).toBe("GET");
    expect(init.credentials).toBe("same-origin");
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
  });
});

describe("Content workspace API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("adds the model field type to Website placement bindings", async () => {
    const snapshotID = "11111111-1111-4111-8111-111111111111";
    const fieldID = "22222222-2222-4222-8222-222222222222";
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      snapshot_id: snapshotID,
      site_id: "33333333-3333-4333-8333-333333333333",
      page_snapshot_id: snapshotID,
      page_id: "home",
      model_revision: 1,
      model_digest: "a".repeat(64),
      page_revision: 1,
      page_digest: "b".repeat(64),
      data_model: { formatVersion: 1, entities: [{
        id: "33333333-3333-4333-8333-333333333333", key: "article", name: "Article", cardinality: "many",
        fields: [{ id: fieldID, key: "image", name: "Image", type: "media" }],
      }] },
      revision: 0, published_revision: 0, records: [], selections: [],
      placements: [{ id: "placement-1", page_id: "page-1", page_name: "Home", feature_key: "hero", feature_name: "Hero", version: "1.0.0",
        inputs: [{ input_key: "article", kind: "record", entity_id: "33333333-3333-4333-8333-333333333333", fields: [{ input_path: "image", field_id: fieldID }] }],
      }],
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubAdminBrowser(fetchMock);

    const workspace = await api.contentEngine.workspace(snapshotID);

    expect(workspace.placements[0]?.inputs[0]?.fields[0]?.type).toBe("media");
  });

  it("preserves history cursors for the next request", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      items: [{
        id: "history-1", draft_revision: 4, content_revision: 2,
        created_at: "2026-11-18T10:00:00Z", record_count: 3,
      }],
      next_cursor: "opaque-history-cursor",
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("window", { location: { origin: "http://localhost" }, dispatchEvent: () => true });
    vi.stubGlobal("fetch", fetchMock);

    const page = await api.contentEngine.history("11111111-1111-4111-8111-111111111111", "before", 25);

    const [input] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(input, "http://localhost");
    expect(url.pathname).toBe("/api/v2/admin/content/snapshots/11111111-1111-4111-8111-111111111111/history");
    expect(url.searchParams.get("cursor")).toBe("before");
    expect(url.searchParams.get("limit")).toBe("25");
    expect(page.nextCursor).toBe("opaque-history-cursor");
    expect(page.items[0]?.contentRevision).toBe(2);
  });
});

describe("modular Website, Content, Forms and Router clients", () => {
  afterEach(() => vi.unstubAllGlobals());

  function stubV2(fetchMock: ReturnType<typeof vi.fn>) {
    vi.stubGlobal("window", { location: { origin: "http://localhost" }, dispatchEvent: () => true });
    vi.stubGlobal("document", { cookie: `zubrium_admin_csrf=${"a".repeat(43)}` });
    vi.stubGlobal("fetch", fetchMock);
  }

  it("lists Website snapshots through the typed, paginated v2 contract", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      items: [{ id: "22222222-2222-4222-8222-222222222222", site_id: "33333333-3333-4333-8333-333333333333", page_id: "home", name: "Homepage", sequence: 1, revision: 3, feature_pins: {}, state: "ready" }],
      next_cursor: "next-page",
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubV2(fetchMock);

    const page = await api.website.snapshots("33333333-3333-4333-8333-333333333333", "home", "first-page", 20);

    const [input] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(input, "http://localhost");
    expect(url.pathname).toBe("/api/v2/admin/sites/snapshots");
    expect(url.searchParams.get("site_id")).toBe("33333333-3333-4333-8333-333333333333");
    expect(url.searchParams.get("page_id")).toBe("home");
    expect(url.searchParams.get("cursor")).toBe("first-page");
    expect(url.searchParams.get("limit")).toBe("20");
    expect(page.next_cursor).toBe("next-page");
    expect(page.items[0]?.id).toBe("22222222-2222-4222-8222-222222222222");
  });

  it("preserves lifecycle state in the site editor snapshot choices", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      items: [{ id: "22222222-2222-4222-8222-222222222222", site_id: "33333333-3333-4333-8333-333333333333", page_id: "home", name: "Child", sequence: 2, revision: 1, feature_pins: {}, state: "provisioning" }],
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubV2(fetchMock);

    const page = await api.siteBuilder.contentSnapshots();

    expect(page.items[0]?.state).toBe("provisioning");
  });

  it("rejects snapshot lifecycle states outside the Website contract", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      id: "22222222-2222-4222-8222-222222222222",
      site_id: "33333333-3333-4333-8333-333333333333",
      name: "Homepage",
      sequence: 1,
      revision: 1,
      feature_pins: {},
      state: "draft",
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubV2(fetchMock);

    await expect(api.website.snapshot("22222222-2222-4222-8222-222222222222"))
      .rejects.toMatchObject({ code: "invalid_response", status: 502 });
  });

  it("creates a complete child snapshot idempotently and deletes by revision", async () => {
    const snapshotID = "22222222-2222-4222-8222-222222222222";
    const fetchMock = vi.fn()
      .mockImplementationOnce(async () => new Response(JSON.stringify({
      id: snapshotID, site_id: "33333333-3333-4333-8333-333333333333", page_id: "homepage", parent_id: "11111111-1111-4111-8111-111111111111",
        name: "Child", sequence: 2, revision: 1, feature_pins: {}, state: "provisioning",
      }), { status: 202, headers: { "Content-Type": "application/json" } }))
      .mockImplementationOnce(async () => new Response(null, { status: 204 }));
    stubV2(fetchMock);

    await api.website.createSnapshot({
      site_id: "33333333-3333-4333-8333-333333333333",
      page_id: "homepage",
      parent_id: "11111111-1111-4111-8111-111111111111",
      name: "Child",
      expected_revision: 4,
      expected_content_revision: 8,
      payload: {
        config: {
          formatVersion: 3,
          pages: [{ id: "homepage", name: "Homepage", kind: "page", placements: [] }],
          routeTree: { id: "root", type: "site", children: [] },
          dataModel: { formatVersion: 1, entities: [] },
        } as unknown as SiteConfig,
        metadata: { formatVersion: 1, siteName: "Example", pages: {} },
      },
    }, "create-snapshot:request-0001");
    await api.website.deleteSnapshot(snapshotID, 7);

    const [createInput, createInit] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(createInput).toBe("/api/v2/admin/sites/snapshots");
    expect(createInit.method).toBe("POST");
    const createBody = JSON.parse(String(createInit.body));
    expect(createBody).toMatchObject({
      page_id: "homepage",
      expected_revision: 4,
      expected_content_revision: 8,
      payload: { config: { format_version: 3, pages: [{ id: "homepage" }] } },
    });
    expect(createBody.payload.config).not.toHaveProperty("routeTree");
    expect(createBody.payload.config).not.toHaveProperty("dataModel");
    expect(createBody.payload).not.toHaveProperty("design_system");
    expect(new Headers(createInit.headers).get("Idempotency-Key")).toBe("create-snapshot:request-0001");
    expect(new Headers(createInit.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
    const [deleteInput, deleteInit] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(deleteInput).toBe(`/api/v2/admin/sites/snapshots/${snapshotID}`);
    expect(deleteInit.method).toBe("DELETE");
    expect(new Headers(deleteInit.headers).get("If-Match")).toBe('"7"');
    expect(new Headers(deleteInit.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
  });

  it("uses the Website build contract with request idempotency and cookie CSRF", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      id: "build-1", snapshot_id: "snapshot-1", state: "queued",
      created_at: "2026-11-18T10:00:00Z", updated_at: "2026-11-18T10:00:00Z",
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    stubV2(fetchMock);

    await api.website.buildSnapshot("snapshot-1", "website-build:request-0001");

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/v2/admin/sites/builds");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ snapshot_id: "snapshot-1" });
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("website-build:request-0001");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
    expect(init.credentials).toBe("same-origin");
  });

  it("uses Content and Forms snapshot contracts with revision guards", async () => {
    const fetchMock = vi.fn()
      .mockImplementationOnce(async () => new Response(JSON.stringify({
        snapshot_id: "snapshot-1",
        site_id: "site-1",
        page_snapshot_id: "snapshot-1",
        page_id: "home",
        model_revision: 1,
        model_digest: "a".repeat(64),
        page_revision: 1,
        page_digest: "b".repeat(64),
        data_model: null,
        revision: 3, published_revision: 2, records: [], selections: [], placements: [],
      }), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockImplementationOnce(async () => new Response(null, { status: 204 }));
    stubV2(fetchMock);

    await api.contentEngine.saveWorkspaceDraft("snapshot-1", 2, [
      { clientKey: "draft-row-1", entityId: "model-1", sortOrder: 0, values: { title: "Draft" } },
    ], [], "content-save:request-0001");
    await api.formsEngine.deleteSubmission("snapshot-1", "submission-1", 7);

    const [contentInput, contentInit] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(contentInput).toBe("/api/v2/admin/content/snapshots/snapshot-1/workspace");
    expect(contentInit.method).toBe("PUT");
    expect(JSON.parse(String(contentInit.body))).toEqual({
      expected_revision: 2,
      records: [{ client_key: "draft-row-1", entity_id: "model-1", sort_order: 0, values: { title: "Draft" } }],
      selections: [],
    });
    expect(new Headers(contentInit.headers).get("Idempotency-Key")).toBe("content-save:request-0001");
    expect(new Headers(contentInit.headers).get("X-CSRF-Token")).toBe("a".repeat(43));

    const [formsInput, formsInit] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    const formsURL = new URL(formsInput, "http://localhost");
    expect(formsURL.pathname).toBe("/api/v2/admin/forms/submissions/submission-1");
    expect(formsURL.searchParams.get("snapshot_id")).toBe("snapshot-1");
    expect(formsInit.method).toBe("DELETE");
    expect(new Headers(formsInit.headers).get("If-Match")).toBe('"r7"');
    expect(new Headers(formsInit.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
  });

  it("does not send Forms mutations with a zero revision", async () => {
    const fetchMock = vi.fn();
    stubV2(fetchMock);

    await expect(api.formsEngine.deleteSubmission("snapshot-1", "submission-1", 0))
      .rejects.toMatchObject({ code: "invalid_revision", status: 400 });
    await expect(api.formsEngine.setSubmissionStatus("snapshot-1", "submission-1", "read", 0))
      .rejects.toMatchObject({ code: "invalid_revision", status: 400 });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("downloads a complete Forms CSV using the selected list filters", async () => {
    const fetchMock = vi.fn(async () => new Response("id,form_id,schema_version,state,created_at,values_json\n", {
      status: 200,
      headers: {
        "Content-Type": "text/csv; charset=utf-8",
        "Content-Disposition": 'attachment; filename="forms-snapshot-1-submissions.csv"',
      },
    }));
    stubV2(fetchMock);

    const result = await api.formsEngine.exportSubmissions("snapshot-1", { form_id: "form-1", state: "new" });
    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(input, "http://localhost");
    expect(url.pathname).toBe("/api/v2/admin/forms/submissions/export");
    expect(url.searchParams.get("snapshot_id")).toBe("snapshot-1");
    expect(url.searchParams.get("form_id")).toBe("form-1");
    expect(url.searchParams.get("state")).toBe("new");
    expect(new Headers(init.headers).get("Accept")).toBe("text/csv");
    expect(init.credentials).toBe("same-origin");
    expect(result.filename).toBe("forms-snapshot-1-submissions.csv");
    expect(result.blob.type).toBe("text/csv;charset=utf-8");
  });

  it("rejects non-CSV Forms export responses", async () => {
    const fetchMock = vi.fn(async () => new Response("{}", {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    stubV2(fetchMock);

    await expect(api.formsEngine.exportSubmissions("snapshot-1"))
      .rejects.toMatchObject({ code: "invalid_response", status: 502 });
  });

  it("creates and revises Router routes through idempotency and ETag contracts", async () => {
    const routeID = "01938f7a-68af-7c10-a51d-78b6ca125301";
    const route = {
      id: routeID, branch_id: "main", host: "example.test", path: "/uploads/file",
      match: "exact", target: { kind: "core-file", file_id: "01938f7a-68af-7c10-a51d-78b6ca125302" }, state: "active",
      revision: 4, created_at: "2026-11-18T10:00:00Z", updated_at: "2026-11-18T10:00:00Z",
    } as const;
    const fetchMock = vi.fn()
      .mockImplementationOnce(async () => new Response(JSON.stringify(route), { status: 201, headers: { "Content-Type": "application/json" } }))
      .mockImplementationOnce(async () => new Response(JSON.stringify({ ...route, revision: 5 }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubV2(fetchMock);

    await api.router.routes.create({ host: route.host, path: route.path, match: route.match, target: route.target }, "router-route:request-0001");
    await api.router.routes.replaceTarget(routeID, { kind: "core-file", file_id: "01938f7a-68af-7c10-a51d-78b6ca125303" }, 4);

    const [createInput, createInit] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(createInput).toBe("/api/v2/admin/router/routes");
    expect(new Headers(createInit.headers).get("Idempotency-Key")).toBe("router-route:request-0001");
    expect(JSON.parse(String(createInit.body))).toEqual({ host: route.host, path: route.path, match: route.match, target: route.target });
    expect(new Headers(createInit.headers).get("X-CSRF-Token")).toBe("a".repeat(43));

    const [replaceInput, replaceInit] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(replaceInput).toBe(`/api/v2/admin/router/routes/${routeID}/target`);
    expect(new Headers(replaceInit.headers).get("If-Match")).toBe('"4"');
    expect(JSON.parse(String(replaceInit.body))).toEqual({ target: { kind: "core-file", file_id: "01938f7a-68af-7c10-a51d-78b6ca125303" } });
  });

  it("rejects malformed Router routes before UI code consumes them", async () => {
    const route = {
      id: "01938f7a-68af-7c10-a51d-78b6ca125301", branch_id: "main", host: "example.test", path: "/uploads/file",
      match: "exact", target: { kind: "core-file", file_id: "01938f7a-68af-7c10-a51d-78b6ca125302" }, state: "active",
      revision: 4, created_at: "2026-11-18T10:00:00Z", updated_at: "2026-11-18T10:00:00Z",
    };
    const cases = [
      { name: "route ID", value: { ...route, id: "route-id" } },
      { name: "unknown match mode", value: { ...route, match: "regex" } },
      { name: "prefix file route", value: { ...route, match: "prefix" } },
      { name: "file ID", value: { ...route, target: { kind: "core-file", file_id: "file-id" } } },
      { name: "invalid upload FileSet ID", value: { ...route, target: {
        kind: "registered-operation", operation_id: "public.form-upload",
        operation_config: { enabled: true, access_group: "uploads", max_bytes: 1024, allowed_content_types: ["image/png"], file_set_id: "set-id" },
      } } },
    ];
    for (const testCase of cases) {
      const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [testCase.value] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
      stubAdminBrowser(fetchMock);

      await expect(api.router.routes.list(), testCase.name).rejects.toMatchObject({ status: 502, code: "invalid_response" });
    }
  });

  it("accepts a Loader upload route with an optional FileSet search index", async () => {
    const route = {
      id: "01938f7a-68af-7c10-a51d-78b6ca125301", branch_id: "main", host: "example.test", path: "/upload",
      match: "exact", target: { kind: "registered-operation", operation_id: "public.form-upload", operation_config: {
        enabled: true, access_group: "uploads", file_set_id: "01938f7a-68af-7c10-a51d-78b6ca125399",
        max_bytes: 1024, allowed_content_types: ["image/png", "application/pdf"],
      } }, state: "active", revision: 4,
      created_at: "2026-11-18T10:00:00Z", updated_at: "2026-11-18T10:00:00Z",
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [route], next_cursor: null }), {
      status: 200, headers: { "Content-Type": "application/json" },
    }));
    stubAdminBrowser(fetchMock);

    const page = await api.router.routes.list();

    expect(page.items[0]?.target).toEqual(route.target);
    expect(page.next_cursor).toBeUndefined();
  });

  it("reads the compiled Router operation limits and MIME allowlist", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [{
      id: "public.form-upload", contract_version: 1, methods: ["POST"], max_request_bytes: 1048576,
      allowed_content_types: ["image/png", "application/pdf"],
    }] }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubAdminBrowser(fetchMock);

    const operations = await api.router.operations.list();
    const [input] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];

    expect(input).toBe("/api/v2/admin/router/operations");
    expect(operations.items[0]).toEqual({
      id: "public.form-upload", contract_version: 1, methods: ["POST"], max_request_bytes: 1048576,
      allowed_content_types: ["image/png", "application/pdf"],
    });
  });

  it("rejects unsafe or malformed Router operation descriptors", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [{
      id: "public.form-upload", contract_version: 1, methods: ["POST"], max_request_bytes: 1048576,
      allowed_content_types: ["text/html"],
    }] }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubAdminBrowser(fetchMock);

    await expect(api.router.operations.list()).rejects.toMatchObject({ status: 502, code: "invalid_response" });
  });

  it("lists FileSets using the Loader pagination contract for optional upload indexing", async () => {
    const fileSet = {
      id: "01938f7a-68af-7c10-a51d-78b6ca125399",
      namespace: "router/uploads",
      name: "Public uploads",
      managementGroup: "router_uploads",
      revision: 3,
      createdAt: "2026-11-18T10:00:00Z",
      updatedAt: "2026-11-18T10:00:00Z",
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      items: [fileSet], nextCursor: "next-set-cursor",
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubAdminBrowser(fetchMock);

    const page = await api.media.fileSets({ query: "uploads", limit: 25, cursor: "previous-cursor" });
    const [input] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(input, "http://localhost");

    expect(url.pathname).toBe("/api/v2/files/sets");
    expect(url.searchParams.get("q")).toBe("uploads");
    expect(url.searchParams.get("limit")).toBe("25");
    expect(url.searchParams.get("cursor")).toBe("previous-cursor");
    expect(page.items).toEqual([fileSet]);
    expect(page.next_cursor).toBe("next-set-cursor");
  });

  it("rejects malformed FileSet metadata before it can be used as upload configuration", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      items: [{ id: "set-id", namespace: "router/uploads", name: "Uploads", managementGroup: "router_uploads", revision: 1,
        createdAt: "2026-11-18T10:00:00Z", updatedAt: "2026-11-18T10:00:00Z" }],
      nextCursor: null,
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    stubAdminBrowser(fetchMock);

    await expect(api.media.fileSets()).rejects.toMatchObject({ status: 502, code: "invalid_response" });
  });
});
