import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: {
    media: { fileSets: vi.fn() },
    principals: { groups: vi.fn() },
    router: { operations: { list: vi.fn() }, routes: { list: vi.fn() } },
    formsEngine: { schemas: vi.fn() },
  },
  useAuth: vi.fn(),
  useConfirm: vi.fn(),
}));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));
vi.mock("@/domains/media/MediaPickerDialog", () => ({ MediaPickerDialog: () => null }));

import RouterRoutesPage from "../../../../../../frontend/admin/src/pages/router/RouterRoutesPage";

const route = {
  id: "route-1",
  branch_id: "main",
  host: "files.example.test",
  path: "/uploads/consent",
  match: "exact",
  target: { kind: "registered-operation", operation_id: "public.form-session", operation_config: {
    enabled: true, form_id: "44444444-4444-4444-8444-444444444444", schema_version: 3,
  } },
  state: "active",
  revision: 4,
  created_at: "2026-11-18T10:00:00Z",
  updated_at: "2026-11-18T10:00:00Z",
};

const laterRoute = {
  ...route,
  id: "route-2",
  path: "/assets/app.js",
  target: { kind: "static-site-artifact", file_id: "01938f7a-68af-7c10-a51d-78b6ca125302" },
};

function markup(level: 1 | 2) {
  mocks.useAuth.mockReturnValue({ principal: { group_access: { router_routes: level } } });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["router", "routes", ""], {
    pages: [{ items: [route], next_cursor: "next-routes" }, { items: [laterRoute], next_cursor: null }],
    pageParams: [null, "next-routes"],
  });
  client.setQueryData(["access", "groups"], [{ key: "uploads", active: true, level: 2 }]);
  client.setQueryData(["router", "operations"], { items: [{
    id: "public.form-upload", contract_version: 1, methods: ["POST"], max_request_bytes: 4194304,
    allowed_content_types: ["image/png", "application/pdf"],
  }] });
  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client },
    createElement(MemoryRouter, null, createElement(RouterRoutesPage)),
  ));
}

describe("Router route settings", () => {
  beforeEach(() => {
    mocks.useAuth.mockReturnValue({ principal: { group_access: { router_routes: 1 } } });
    mocks.useConfirm.mockReturnValue(vi.fn(async () => true));
    mocks.api.principals.groups.mockResolvedValue([{ key: "uploads", active: true, level: 2 }]);
    mocks.api.router.operations.list.mockResolvedValue({ items: [{
      id: "public.form-upload", contract_version: 1, methods: ["POST"], max_request_bytes: 4194304,
      allowed_content_types: ["image/png", "application/pdf"],
    }] });
    mocks.api.router.routes.list.mockResolvedValue({ items: [route], next_cursor: null });
    mocks.api.formsEngine.schemas.mockResolvedValue({ items: [], next_cursor: null });
  });

  it("shows explicit public routes to readers without exposing write actions", () => {
    const rendered = markup(1);
    expect(rendered).toContain("Публичные маршруты");
    expect(rendered).toContain("files.example.test/uploads/consent");
    expect(rendered).toContain("files.example.test/assets/app.js");
    expect(rendered).toContain("Сессия формы · GET/POST/DELETE");
    expect(rendered).toContain("форма 44444444-4444-4444-8444-444444444444 · схема v3");
    expect(rendered).not.toContain("ID снапшота");
    expect(rendered).not.toContain("Новый маршрут");
    expect(rendered).not.toContain("Изменить цель");
  });

  it("shows route creation to operators with level two", () => {
    const rendered = markup(2);
    expect(rendered).toContain("Новый маршрут");
  });
});
