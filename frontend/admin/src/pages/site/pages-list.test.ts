import { describe, expect, it } from "vitest";
import type { RouteTreeNode, SitePage } from "@/types";
import {
  filterPageEntries,
  groupPageEntries,
  pageListEntries,
  sortPageEntries,
} from "./pages-list";

const pages: SitePage[] = [
  {
    id: "home",
    name: "Главная",
    kind: "page",
    placements: [],
  },
  {
    id: "catalog",
    name: "Каталог",
    kind: "collection-list",
    placements: [{ id: "catalog-main", build: { featureKey: "catalog", version: "2.0.0" }, slot: "main" }],
  },
  {
    id: "product",
    name: "Карточка товара",
    kind: "detail",
    placements: [],
  },
  {
    id: "draft",
    name: "Черновик",
    kind: "page",
    placements: [],
  },
];

const tree: RouteTreeNode = {
  id: "site",
  type: "site",
  children: [
    { id: "root", type: "path", path: "/", pageId: "home" },
    {
      id: "catalog-root",
      type: "path",
      path: "/catalog",
      pageId: "catalog",
      children: [
        { id: "catalog-slug", type: "param", name: "slug", pageId: "product" },
      ],
    },
    { id: "catalog-alias", type: "path", path: "/products", pageId: "catalog" },
  ],
};

describe("pages list", () => {
  it("keeps one entry per page with its primary and additional routes", () => {
    const entries = pageListEntries(tree, pages);
    const catalog = entries.find((entry) => entry.page.id === "catalog");

    expect(entries).toHaveLength(4);
    expect(catalog?.primaryRoute?.path).toBe("/catalog");
    expect(catalog?.routes.map((route) => route.path)).toEqual(["/catalog", "/products"]);
    expect(entries.find((entry) => entry.page.id === "draft")?.groupLabel).toBe("Без маршрута");
  });

  it("searches page names, routes, feature keys, and registered feature labels", () => {
    const entries = pageListEntries(tree, pages, new Map([["catalog", "Каталог продукции"]]));

    expect(filterPageEntries(entries, "карточка").map((entry) => entry.page.id)).toEqual(["product"]);
    expect(filterPageEntries(entries, "/products").map((entry) => entry.page.id)).toEqual(["catalog"]);
    expect(filterPageEntries(entries, "catalog").map((entry) => entry.page.id)).toEqual(["catalog", "product"]);
    expect(filterPageEntries(entries, "продукции").map((entry) => entry.page.id)).toEqual(["catalog"]);
  });

  it("sorts view entries without changing stored route priority", () => {
    const entries = pageListEntries(tree, pages);
    const original = entries.map((entry) => entry.page.id);
    const sorted = sortPageEntries(entries, "name");

    expect(sorted.map((entry) => entry.page.name)).toEqual(["Главная", "Карточка товара", "Каталог", "Черновик"]);
    expect(entries.map((entry) => entry.page.id)).toEqual(original);
  });

  it("groups by the first route section and places unrouted pages last", () => {
    const groups = groupPageEntries(pageListEntries(tree, pages));

    expect(groups.map((group) => group.label)).toEqual(["Главная", "/catalog", "Без маршрута"]);
    expect(groups.at(-1)?.entries.map((entry) => entry.page.id)).toEqual(["draft"]);
  });
});
