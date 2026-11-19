import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: { website: { allSnapshots: vi.fn() }, formsEngine: { schemas: vi.fn(), schema: vi.fn(), saveSchema: vi.fn() } },
  useAuth: vi.fn(),
  useConfirm: vi.fn(),
}));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));

import FormsPage from "../../../../../../frontend/admin/src/pages/forms/FormsPage";

const snapshot = {
  id: "22222222-2222-4222-8222-222222222222",
  site_id: "33333333-3333-4333-8333-333333333333",
  name: "Главный сайт",
  sequence: 5,
  revision: 8,
  feature_pins: {},
  state: "ready",
};
const schema = {
  id: "44444444-4444-4444-8444-444444444444",
  key: "contact_request",
  version: 3,
  revision: 7,
  fields: {
    attachment: {
      type: "file",
      required: false,
      max_files: 2,
      max_file_bytes: 5242880,
      allowed_content_types: ["application/pdf"],
    },
  },
};

function renderPage(path: string, level: 1 | 2) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } });
  client.setQueryData(["website", "snapshots", "all"], [snapshot]);
  client.setQueryData(["formsEngine", "schemas", snapshot.id], {
    pages: [{ items: [schema], next_cursor: null }],
    pageParams: [null],
  });
  mocks.useAuth.mockReturnValue({ principal: { group_access: { forms_schemas: level } } });
  const router = createMemoryRouter([{ path: "/forms", element: createElement(FormsPage) }], { initialEntries: [path] });
  return renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(RouterProvider, { router })));
}

describe("Forms schema editor", () => {
  beforeEach(() => {
    mocks.useConfirm.mockReturnValue(vi.fn(async () => true));
    mocks.api.website.allSnapshots.mockResolvedValue([snapshot]);
    mocks.api.formsEngine.schemas.mockResolvedValue({ items: [schema], next_cursor: null });
    mocks.api.formsEngine.schema.mockResolvedValue(schema);
    mocks.api.formsEngine.saveSchema.mockResolvedValue(schema);
  });

  it("lets Forms readers inspect the explicit file constraints without write controls", () => {
    const rendered = renderPage(`/forms?snapshot=${snapshot.id}&form=${schema.id}`, 1);

    expect(rendered).toContain("Схемы");
    expect(rendered).toContain("contact_request");
    expect(rendered).toContain("Разрешённые MIME-типы");
    expect(rendered).toContain("application/pdf");
    expect(rendered).not.toContain("Новая форма");
    expect(rendered).not.toContain("Сохранить");
  });

  it("lets Forms writers create an explicitly typed schema with generated IDs", () => {
    const rendered = renderPage(`/forms?snapshot=${snapshot.id}&form=new`, 2);

    expect(rendered).toContain("Ключ формы");
    expect(rendered).toContain("Идентификатор поля");
    expect(rendered).toContain("Электронная почта");
    expect(rendered).toContain("Добавить поле");
    expect(rendered).toContain("Идентификатор формы назначится автоматически.");
  });
});
