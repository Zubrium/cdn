import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";

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
});
