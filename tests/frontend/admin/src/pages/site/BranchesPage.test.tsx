import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: { website: { branches: vi.fn(), allSnapshots: vi.fn(), branchEvents: vi.fn(), snapshot: vi.fn(), assignBranch: vi.fn() } },
  getSyntheticDataMode: vi.fn(() => true),
  subscribeSyntheticDataMode: vi.fn(() => () => undefined),
}));

vi.mock("@/api", () => ({
  api: mocks.api,
  getSyntheticDataMode: mocks.getSyntheticDataMode,
  subscribeSyntheticDataMode: mocks.subscribeSyntheticDataMode,
}));
vi.mock("@/components/common/confirm", () => ({ useConfirm: () => vi.fn() }));

import BranchesPage from "../../../../../../frontend/admin/src/pages/site/BranchesPage";

const siteID = "33333333-3333-4333-8333-333333333333";
const snapshot = {
  id: "22222222-2222-4222-8222-222222222222", site_id: siteID, name: "Homepage", sequence: 12,
  revision: 9, feature_pins: {}, state: "ready",
};
const branch = {
  id: "11111111-1111-4111-8111-111111111111", name: "preview", site_id: siteID,
  listen_address: "127.0.0.1:8081", cors_origins: [], indexable: false,
  snapshot_id: snapshot.id, revision: 4, enabled: true, updated_at: "2026-11-18T10:00:00Z",
};

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["website", "branches"], { items: [branch], next_cursor: null });
  client.setQueryData(["website", "snapshots", "all"], [snapshot]);
  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client },
    createElement(MemoryRouter, { initialEntries: ["/snapshots/branches"] }, createElement(BranchesPage)),
  ));
}

describe("branch assignment preview", () => {
  beforeEach(() => {
    mocks.api.website.branches.mockResolvedValue({ items: [branch], next_cursor: null });
    mocks.api.website.allSnapshots.mockResolvedValue([snapshot]);
    mocks.api.website.branchEvents.mockResolvedValue({ items: [], next_cursor: null });
  });

  it("does not imply branch assignments are loaded or provide live writes", () => {
    const rendered = renderPage();
    expect(rendered).toContain("Назначения недоступны через Admin API");
    expect(rendered).toContain("нет endpoint для чтения или изменения назначений");
    expect(rendered).not.toContain("Назначить");
  });
});
