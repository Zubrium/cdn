import { describe, expect, it } from "vitest";
import type { RouteTreeNode, SitePage } from "@/types";
import {
  filterPageEntries,
  groupPageEntries,
  pageListEntries,
  sortPageEntries,
} from "../../../../../../frontend/admin/src/pages/site/pages-list";

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
    { id: "price-file", type: "path", path: "/files/price.pdf", fileId: "11111111-1111-4111-8111-111111111111", fileDisposition: "attachment" },
  ],
};

describe("pages list", () => {
  it("keeps one entry per page with its primary and additional routes", () => {
    const entries = pageListEntries(tree, pages);
    const catalog = entries.find((entry) => entry.kind === "page" && entry.page.id === "catalog");

    expect(entries).toHaveLength(5);
    expect(catalog?.primaryRoute?.path).toBe("/catalog");
    expect(catalog?.routes.map((route) => route.path)).toEqual(["/catalog", "/products"]);
    expect(entries.find((entry) => entry.kind === "page" && entry.page.id === "draft")?.groupLabel).toBe("Без маршрута");
    expect(entries.find((entry) => entry.kind === "file")?.name).toBe("Публичный файл");
  });

  it("searches page names, routes, feature keys, and registered feature labels", () => {
    const entries = pageListEntries(tree, pages, new Map([["catalog", "Каталог продукции"]]));
    const withFileName = pageListEntries(tree, pages, new Map([["catalog", "Каталог продукции"]]), new Map([["11111111-1111-4111-8111-111111111111", "Прайс-лист"]]));

    expect(filterPageEntries(entries, "карточка").map((entry) => entry.name)).toEqual(["Карточка товара"]);
    expect(filterPageEntries(entries, "/products").map((entry) => entry.name)).toEqual(["Каталог"]);
    expect(filterPageEntries(entries, "catalog").map((entry) => entry.name)).toEqual(["Каталог", "Карточка товара"]);
    expect(filterPageEntries(entries, "продукции").map((entry) => entry.name)).toEqual(["Каталог"]);
    expect(filterPageEntries(withFileName, "прайс").map((entry) => entry.kind)).toEqual(["file"]);
  });

  it("sorts view entries without changing stored route priority", () => {
    const entries = pageListEntries(tree, pages);
    const original = entries.map((entry) => entry.name);
    const sorted = sortPageEntries(entries, "name");

    expect(sorted.map((entry) => entry.name)).toEqual(["Главная", "Карточка товара", "Каталог", "Публичный файл", "Черновик"]);
    expect(entries.map((entry) => entry.name)).toEqual(original);
  });

  it("groups by the first route section and places unrouted pages last", () => {
    const groups = groupPageEntries(pageListEntries(tree, pages));

    expect(groups.map((group) => group.label)).toEqual(["Главная", "/catalog", "/files", "Без маршрута"]);
    expect(groups.at(-1)?.entries.map((entry) => entry.name)).toEqual(["Черновик"]);
  });
});
