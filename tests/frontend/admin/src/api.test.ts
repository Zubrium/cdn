import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../../../frontend/admin/src/api";
import type { MediaRenditionContext } from "../../../../frontend/admin/src/types";

describe("branch snapshot API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends branchId for the snapshot history endpoint", async () => {
    const branchId = "2f3b91b2-cf9c-4d5e-b473-41aec5c15280";
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ items: [], nextCursor: null }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      localStorage: { getItem: () => null },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("fetch", fetchMock);

    await api.siteBuilder.snapshotEvents(branchId, "cursor-id");

    const [input] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(input, "http://localhost");
    expect(url.pathname).toBe("/api/v2/admin/site/snapshot-events");
    expect(url.searchParams.get("branchId")).toBe(branchId);
    expect(url.searchParams.get("before")).toBe("cursor-id");
    expect(url.searchParams.has("environment")).toBe(false);
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

describe("token pagination API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends the continuation cursor and returns the next page", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ items: [], next_cursor: "next-page" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      localStorage: { getItem: () => null },
      dispatchEvent: () => true,
    });
    vi.stubGlobal("fetch", fetchMock);

    const page = await api.tokens.list("cursor-value");

    const [input] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(input, "http://localhost");
    expect(url.pathname).toBe("/api/v1/admin/tokens");
    expect(url.searchParams.get("cursor")).toBe("cursor-value");
    expect(page.next_cursor).toBe("next-page");
  });
});

describe("Coordinator jobs API", () => {
  afterEach(() => vi.unstubAllGlobals());

  function stubBrowser(fetchMock: ReturnType<typeof vi.fn>) {
    vi.stubGlobal("window", {
      location: { origin: "http://localhost" },
      localStorage: { getItem: () => "coordinator-test-token" },
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
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer coordinator-test-token");
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
    expect(new Headers(permissionsInit.headers).get("Authorization")).toBe("Bearer coordinator-test-token");
    expect(retryInput).toBe("/api/v2/coordinator/jobs/job%2Fid%20with%20space/retry");
    expect(retryInit.method).toBe("POST");
    expect(retryInit.body).toBeUndefined();
  });
});
