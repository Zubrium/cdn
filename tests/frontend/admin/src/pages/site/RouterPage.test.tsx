import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SiteConfig } from "@/types";

const mocks = vi.hoisted(() => ({
  useSiteWorkspace: vi.fn(),
}));

vi.mock("../../../../../../frontend/admin/src/pages/site/SiteWorkspace", () => ({ useSiteWorkspace: mocks.useSiteWorkspace }));

import RouterPage from "../../../../../../frontend/admin/src/pages/site/RouterPage";

const config: SiteConfig = {
  formatVersion: 3,
  pages: [
    { id: "home", name: "Главная", kind: "page", placements: [] },
    { id: "catalog", name: "Каталог", kind: "collection-list", placements: [] },
    { id: "draft", name: "Черновик", kind: "page", placements: [] },
  ],
  routeTree: {
    id: "site",
    type: "site",
    children: [
      { id: "home-route", type: "path", path: "/", pageId: "home" },
      { id: "catalog-route", type: "path", path: "/catalog", pageId: "catalog" },
      { id: "file-route", type: "path", path: "/price.pdf", fileId: "11111111-1111-4111-8111-111111111111" },
    ],
  },
};

describe("pages workspace", () => {
  beforeEach(() => {
    mocks.useSiteWorkspace.mockReturnValue({ config, update: vi.fn() });
  });

  it("renders only navigation entries for pages and file routes", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const markup = renderToStaticMarkup(createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(MemoryRouter, null, createElement(RouterPage)),
    ));

    expect(markup).toContain("Страницы");
    expect(markup).toContain("Приоритет маршрутов");
    expect(markup).toContain("Без маршрута");
    expect(markup).toContain('href="/site/pages/home"');
    expect(markup).toContain('href="/site/pages/catalog"');
    expect(markup).toContain('href="/site/pages/files/file-route"');
    expect(markup).toContain("Публичный файл");
    expect(markup).not.toContain("Инспектор страницы");
    expect(markup).not.toContain(">Канвас<");
  });
});
