import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SiteConfig } from "@/types";

const mocks = vi.hoisted(() => ({
  useSiteWorkspace: vi.fn(),
}));

vi.mock("./SiteWorkspace", () => ({ useSiteWorkspace: mocks.useSiteWorkspace }));
vi.mock("./PageEditorPage", () => ({
  default: ({ pageId, embedded }: { pageId: string; embedded?: boolean }) =>
    createElement("div", { "data-page-editor": pageId, "data-embedded": String(Boolean(embedded)) }, "Инспектор страницы"),
}));

import RouterPage from "./RouterPage";

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
    ],
  },
};

describe("pages workspace", () => {
  beforeEach(() => {
    mocks.useSiteWorkspace.mockReturnValue({ config, update: vi.fn() });
  });

  it("renders the page list and selected page inspector in one screen", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const markup = renderToStaticMarkup(createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(MemoryRouter, null, createElement(RouterPage)),
    ));

    expect(markup).toContain("Страницы");
    expect(markup).toContain("Приоритет маршрутов");
    expect(markup).toContain("Без маршрута");
    expect(markup).toContain("data-page-editor=\"home\"");
    expect(markup).toContain("data-embedded=\"true\"");
    expect(markup).toContain("Инспектор страницы");
    expect(markup).not.toContain(">Канвас<");
  });
});
