import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: {
    formsEngine: { schemas: vi.fn(), schema: vi.fn(), saveSchema: vi.fn() },
    router: { operations: { list: vi.fn() }, routes: { list: vi.fn() } },
  },
  useAuth: vi.fn(),
  useConfirm: vi.fn(),
}));

vi.mock("@/api", () => ({ api: mocks.api, isApiError: (error: unknown) => error instanceof Error && "status" in error }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));

import FormsPage from "../../../../../../frontend/admin/src/pages/forms/FormsPage";
import FormDetailPage from "../../../../../../frontend/admin/src/pages/forms/FormDetailPage";
import { schemaDetailQueryKey, schemaQueryKey } from "../../../../../../frontend/admin/src/pages/forms/forms-query";

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

function renderRoutes(path: string, access: Record<string, 1 | 2>) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } });
  client.setQueryData(schemaQueryKey, {
    pages: [{ items: [schema], next_cursor: null }],
    pageParams: [null],
  });
  client.setQueryData(schemaDetailQueryKey(schema.id), schema);
  mocks.useAuth.mockReturnValue({ principal: { group_access: access } });
  const router = createMemoryRouter([
    { path: "/forms", element: createElement(FormsPage) },
    { path: "/forms/new", element: createElement(FormDetailPage, { creating: true }) },
    { path: "/forms/:formId", element: createElement(FormDetailPage) },
  ], { initialEntries: [path] });
  return renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(RouterProvider, { router })));
}

describe("global Forms nested pages", () => {
  beforeEach(() => {
    mocks.useConfirm.mockReturnValue(vi.fn(async () => true));
    mocks.api.formsEngine.schemas.mockResolvedValue({ items: [schema], next_cursor: null });
    mocks.api.formsEngine.schema.mockResolvedValue(schema);
    mocks.api.formsEngine.saveSchema.mockResolvedValue(schema);
    mocks.api.router.operations.list.mockResolvedValue({ items: [] });
    mocks.api.router.routes.list.mockResolvedValue({ items: [], next_cursor: null });
  });

  it("shows a list of unlimited global forms on /forms", () => {
    const rendered = renderRoutes("/forms", { forms_schemas: 2 });

    expect(rendered).toContain("Выберите форму");
    expect(rendered).toContain("Количество форм не ограничено");
    expect(rendered).toContain("contact_request");
    expect(rendered).toContain("Новая форма");
    expect(rendered).not.toContain("Идентификатор поля");
    expect(rendered).not.toContain("Сохранить");
    expect(rendered).not.toContain("Снапшот");
  });

  it("opens the editor of a specific schema on /forms/:formId", () => {
    const rendered = renderRoutes(`/forms/${schema.id}`, { forms_schemas: 1 });

    expect(rendered).toContain("contact_request");
    expect(rendered).toContain("Версия 3");
    expect(rendered).toContain("Разрешённые MIME-типы");
    expect(rendered).toContain("application/pdf");
    expect(rendered).not.toContain("Сохранить");
    expect(rendered).not.toContain("Снапшот");
  });

  it("opens a separate new-form page on /forms/new", () => {
    const rendered = renderRoutes("/forms/new", { forms_schemas: 2 });

    expect(rendered).toContain("Новая форма");
    expect(rendered).toContain("Ключ формы");
    expect(rendered).toContain("Идентификатор поля");
    expect(rendered).toContain("Электронная почта");
    expect(rendered).toContain("Добавить поле");
    expect(rendered).toContain("Идентификатор формы назначится автоматически.");
    expect(rendered).not.toContain("Снапшот");
  });

  it("explains pinned Router bindings and publishes only with both write grants", () => {
    const rendered = renderRoutes(`/forms/${schema.id}`, { forms_schemas: 2, router_routes: 2 });

    expect(rendered).toContain("создаст новую версию");
    expect(rendered).toContain("Router");
    expect(rendered).toContain("обновляется вручную");
    expect(rendered).toContain("Опубликовать форму");
    expect(rendered).toContain("/forms/contact_request");
  });
});
