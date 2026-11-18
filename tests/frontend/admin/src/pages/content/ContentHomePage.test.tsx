import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmProvider } from "../../../../../../frontend/admin/src/components/common/confirm";

const mocks = vi.hoisted(() => ({
  useAuth: vi.fn(),
  api: { website: { snapshots: vi.fn() }, contentEngine: { models: vi.fn(), records: vi.fn() } },
}));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/domains/media/MediaPickerDialog", () => ({ MediaPickerDialog: () => null }));

import ContentHomePage from "../../../../../../frontend/admin/src/pages/content/ContentHomePage";

const snapshot = { id: "snapshot-1", site_id: "site-1", name: "Homepage", sequence: 1, revision: 2, feature_pins: {}, state: "draft" };
const model = {
  id: "model-1", key: "products", name: "Товары", cardinality: "many" as const, revision: 3,
  fields: [{ id: "field-1", key: "title", name: "Название", type: "text", required: false }],
};
const record = {
  id: "record-1", model_id: "model-1", values: { title: "Молоко" }, revision: 4,
  published_revision: 3, updated_at: "2026-11-18T10:00:00Z",
};

function renderPage(level: 1 | 2) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["website", "snapshots"], { pages: [{ items: [snapshot], next_cursor: null }], pageParams: [null] });
  client.setQueryData(["contentEngine", "models", "snapshot-1"], { pages: [{ items: [model], next_cursor: null }], pageParams: [null] });
  client.setQueryData(["contentEngine", "records", "snapshot-1", "model-1"], { pages: [{ items: [record], next_cursor: null }], pageParams: [null] });
  mocks.useAuth.mockReturnValue({ principal: { group_access: { content: level } } });
  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client },
    createElement(ConfirmProvider, null,
      createElement(MemoryRouter, { initialEntries: ["/content?snapshot=snapshot-1"] }, createElement(ContentHomePage))),
  ));
}

describe("Content Engine workspace", () => {
  beforeEach(() => {
    mocks.api.website.snapshots.mockResolvedValue({ items: [snapshot], next_cursor: null });
    mocks.api.contentEngine.models.mockResolvedValue({ items: [model], next_cursor: null });
    mocks.api.contentEngine.records.mockResolvedValue({ items: [record], next_cursor: null });
  });

  it("renders snapshot-scoped models and records from the engine API", () => {
    const rendered = renderPage(1);
    expect(rendered).toContain("Контент");
    expect(rendered).toContain("Товары");
    expect(rendered).toContain("Молоко");
    expect(rendered).toContain("Черновик");
  });

  it("shows write controls only to a subject with level two", () => {
    expect(renderPage(1)).not.toContain("Новая запись");
    expect(renderPage(2)).toContain("Новая запись");
  });
});
