import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmProvider } from "../../../../../../frontend/admin/src/components/common/confirm";

const mocks = vi.hoisted(() => ({
  useAuth: vi.fn(),
  api: { website: { allSnapshots: vi.fn() }, contentEngine: { workspace: vi.fn() } },
}));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/domains/media/MediaPickerDialog", () => ({ MediaPickerDialog: () => null }));

import ContentHomePage from "../../../../../../frontend/admin/src/pages/content/ContentHomePage";

const snapshotID = "11111111-1111-4111-8111-111111111111";
const siteID = "22222222-2222-4222-8222-222222222222";
const entityID = "33333333-3333-4333-8333-333333333333";
const fieldID = "44444444-4444-4444-8444-444444444444";
const recordID = "55555555-5555-4555-8555-555555555555";

const snapshot = {
  id: snapshotID,
  site_id: siteID,
  name: "Homepage",
  sequence: 1,
  revision: 2,
  feature_pins: {},
  state: "ready" as "ready" | "provisioning" | "failed",
};

const workspace = {
  snapshot: { id: snapshotID },
  dataModel: {
    formatVersion: 1,
    entities: [{
      id: entityID,
      key: "products",
      name: "Товары",
      cardinality: "many",
      fields: [{ id: fieldID, key: "title", name: "Название", type: "text" }],
    }],
  },
  revision: 4,
  publishedRevision: 3,
  records: [{ id: recordID, entityId: entityID, sortOrder: 0, values: { title: "Молоко" } }],
  selections: [],
  placements: [],
};

function renderPage(contentLevel: 1 | 2, selectedSnapshot = snapshot) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["website", "snapshots", "content", "", ""], [selectedSnapshot]);
  client.setQueryData(["content", "model-workspace", snapshotID], workspace);
  mocks.useAuth.mockReturnValue({ principal: { group_access: { content: contentLevel } } });
  const router = createMemoryRouter([{ path: "*", element: createElement(ContentHomePage) }], { initialEntries: [`/content?snapshot=${snapshotID}`] });

  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client },
    createElement(ConfirmProvider, null,
      createElement(RouterProvider, { router })),
  ));
}

describe("Content workspace page", () => {
  beforeEach(() => {
    mocks.api.website.allSnapshots.mockResolvedValue([snapshot]);
    mocks.api.contentEngine.workspace.mockResolvedValue(workspace);
  });

  it("renders the selected snapshot's model and records from the Content workspace", () => {
    const rendered = renderPage(1);

    expect(rendered).toContain("Контент");
    expect(rendered).toContain("Товары");
    expect(rendered).toContain("Молоко");
    expect(rendered).toContain("Черновик 4 · опубликовано 3");
  });

  it("shows publishing controls only with write access to the content group", () => {
    expect(renderPage(1)).not.toContain("Опубликовать");
    expect(renderPage(2)).toContain("Опубликовать");
  });

  it("does not load or expose content while its snapshot is provisioning", () => {
    const rendered = renderPage(2, { ...snapshot, state: "provisioning" });

    expect(rendered).toContain("Снапшот готовится");
    expect(rendered).not.toContain("Молоко");
    expect(rendered).not.toContain("Опубликовать");
  });
});
