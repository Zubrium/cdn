import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ api: { website: { branches: vi.fn() } }, useAuth: vi.fn() }));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));

import SettingsPage from "../../../../../../frontend/admin/src/pages/settings/SettingsPage";

const branches = {
  items: [{
    id: "11111111-1111-4111-8111-111111111111",
    name: "preview",
    site_id: "33333333-3333-4333-8333-333333333333",
    listen_address: "127.0.0.1:8081",
    cors_origins: ["https://preview.example.test"],
    indexable: false,
    snapshot_id: null,
    revision: 1,
    enabled: true,
    updated_at: "2026-11-18T10:00:00Z",
  }],
};

function renderPage(principal: unknown = { group_access: { features: 1 } }) {
  mocks.useAuth.mockReturnValue({ principal });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["website", "branches"], branches);
  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client },
    createElement(MemoryRouter, null, createElement(SettingsPage)),
  ));
}

describe("static runtime settings", () => {
  beforeEach(() => {
    mocks.api.website.branches.mockResolvedValue(branches);
  });

  it("shows configured branch policy without offering runtime configuration writes", () => {
    const rendered = renderPage();

    expect(rendered).toContain("preview");
    expect(rendered).toContain("127.0.0.1:8081");
    expect(rendered).toContain("https://preview.example.test");
    expect(rendered).toContain("Индексация");
    expect(rendered).toContain("Закрыта");
    expect(rendered).toContain("Снапшот не назначен");
    expect(rendered).not.toContain("Сохранить");
    expect(rendered).not.toContain("dev");
    expect(rendered).not.toContain("prod");
    expect(rendered).not.toContain("Назначения снапшотов");
  });

  it("offers branch assignment only to a user with feature group write access", () => {
    const rendered = renderPage({ group_access: { features: 2 } });

    expect(rendered).toContain("Назначения снапшотов");
  });
});
