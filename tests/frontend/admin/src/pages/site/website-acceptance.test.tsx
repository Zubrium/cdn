import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../../../../../frontend/admin/src/api";
import type { SiteConfig } from "../../../../../../frontend/admin/src/types";

const specification = readFileSync(
  new URL("../../../../../../spec/presentation/openapi/website.yaml", import.meta.url),
  "utf8",
);

function stubAdminWrite(fetchMock: ReturnType<typeof vi.fn>) {
  vi.stubGlobal("window", { location: { origin: "http://localhost" }, dispatchEvent: () => true });
  vi.stubGlobal("document", { cookie: `zubrium_admin_csrf=${"a".repeat(43)}` });
  vi.stubGlobal("fetch", fetchMock);
}

function snapshotResponse(id: string) {
  return new Response(JSON.stringify({
    id,
    page_id: "22222222-2222-4222-8222-222222222222",
    name: "Materialized",
    sequence: 2,
    revision: 1,
    feature_pins: {},
    state: "provisioning",
  }), { status: 202, headers: { "Content-Type": "application/json" } });
}

describe("Website contract acceptance", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("keeps the Website OpenAPI contract versioned in metadata only and flat in paths", () => {
    expect(specification).toContain("openapi: 3.1.0");
    expect(specification).toMatch(/^\s{2}version:/m);
    expect(specification).not.toMatch(/\/api\/v\d+\//);
    expect(specification).toContain("operationId: createSnapshot");
    expect(specification).toContain("required: [page_id, parent_id, name, expected_revision, expected_content_revision, payload]");
    expect(specification).toContain("additionalProperties: false");
    expect(specification).toContain("'409': { $ref: '#/components/responses/Error' }");
  });

  it("sends structural input without Router, model or record fields under flat write paths", async () => {
    const fetchMock = vi.fn(async () => snapshotResponse("33333333-3333-4333-8333-333333333333"));
    stubAdminWrite(fetchMock);

    await api.website.createSnapshot({
      page_id: "22222222-2222-4222-8222-222222222222",
      parent_id: "11111111-1111-4111-8111-111111111111",
      name: "Structural child",
      expected_revision: 1,
      expected_content_revision: 0,
      payload: {
        config: {
          formatVersion: 3,
          pages: [{ id: "22222222-2222-4222-8222-222222222222", name: "Home", kind: "page", placements: [] }],
          routeTree: { id: "root", type: "site", children: [] },
          dataModel: { formatVersion: 1, entities: [] },
        } as unknown as SiteConfig,
        metadata: { formatVersion: 1, siteName: "Example", pages: {} },
      },
    }, "website-acceptance:snapshot-0001");

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/admin/snapshots");
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("website-acceptance:snapshot-0001");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("a".repeat(43));
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
    const body = JSON.parse(String(init.body));
    expect(body.payload.config).toMatchObject({ format_version: 3, pages: [{ id: "22222222-2222-4222-8222-222222222222" }] });
    for (const forbidden of ["route", "routeTree", "dataModel", "records", "module_id", "scope"]) {
      expect(body.payload.config).not.toHaveProperty(forbidden);
    }
  });

  it("requires explicit confirmation and exact revisions when applying a template", async () => {
    const fetchMock = vi.fn(async () => snapshotResponse("44444444-4444-4444-8444-444444444444"));
    stubAdminWrite(fetchMock);

    await api.website.applyPageTemplate("22222222-2222-4222-8222-222222222222", {
      template_id: "55555555-5555-4555-8555-555555555555",
      template_revision: 3,
      expected_snapshot_id: "11111111-1111-4111-8111-111111111111",
      expected_snapshot_revision: 1,
      expected_content_revision: 2,
      confirmed: true,
    }, "website-acceptance:template-0001");

    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/admin/pages/22222222-2222-4222-8222-222222222222/template-applications");
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("website-acceptance:template-0001");
    expect(JSON.parse(String(init.body))).toEqual({
      template_id: "55555555-5555-4555-8555-555555555555",
      template_revision: 3,
      expected_snapshot_id: "11111111-1111-4111-8111-111111111111",
      expected_snapshot_revision: 1,
      expected_content_revision: 2,
      confirmed: true,
    });
  });

  it("uses the idempotent build contract and rejects unknown snapshot lifecycle states", async () => {
    const buildMock = vi.fn(async () => new Response(JSON.stringify({
      id: "build-1", snapshot_id: "snapshot-1", locale: "ru-RU", locale_catalog_revision: 4,
      input_digest: "a".repeat(64), state: "queued",
      created_at: "2026-11-22T10:00:00Z", updated_at: "2026-11-22T10:00:00Z",
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    stubAdminWrite(buildMock);
    await api.website.buildSnapshot("snapshot-1", "ru-RU", "website-acceptance:build-0001");

    const [input, init] = buildMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe("/api/admin/builds");
    expect(JSON.parse(String(init.body))).toEqual({ snapshot_id: "snapshot-1", locale: "ru-RU" });
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("website-acceptance:build-0001");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("a".repeat(43));

    stubAdminWrite(vi.fn(async () => new Response(JSON.stringify({
      id: "33333333-3333-4333-8333-333333333333",
      name: "Unknown", sequence: 1, revision: 1, feature_pins: {}, state: "draft",
    }), { status: 200, headers: { "Content-Type": "application/json" } })));
    await expect(api.website.snapshot("33333333-3333-4333-8333-333333333333"))
      .rejects.toMatchObject({ code: "invalid_response", status: 502 });
  });
});
