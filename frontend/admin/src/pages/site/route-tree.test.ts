import { describe, expect, it } from "vitest";
import type { RouteTreeNode } from "@/types";
import { addRouteNode, describeRoutes, moveRoutePriority, removeRouteBranch, reparentRoute, routeCapturesForPage, staticPageRouteIds } from "./route-tree";

const tree: RouteTreeNode = {
  id: "site",
  type: "site",
  children: [
    {
      id: "catalog",
      type: "path",
      path: "/catalog",
      pageId: "catalog-page",
      children: [
        {
          id: "products",
          type: "static",
          value: "products",
          children: [{ id: "product", type: "param", name: "slug", pageId: "product-page" }],
        },
      ],
    },
    { id: "about", type: "path", path: "/about", pageId: "about-page" },
  ],
};

describe("route tree editor", () => {
  it("identifies system page candidates without dynamic or inherited dynamic paths", () => {
    const dynamicTree: RouteTreeNode = { ...tree, children: [...tree.children!, { id: "regex", type: "regex", pattern: "foo.*", pageId: "regex-page", children: [{ id: "under-regex", type: "static", value: "info", pageId: "under-regex-page" }] }] };
    expect([...staticPageRouteIds(dynamicTree)].sort()).toEqual(["about-page", "catalog-page"]);
  });
  it("describes full URLs, captures and shared pages", () => {
    expect(describeRoutes(tree).map(({ id, path }) => [id, path])).toEqual([
      ["catalog", "/catalog"],
      ["products", "/catalog/products"],
      ["product", "/catalog/products/{slug}"],
      ["about", "/about"],
    ]);
    expect(routeCapturesForPage(tree, "product-page")[0]?.captures).toEqual(["slug"]);
  });

  it("adds a root and child in their respective positions", () => {
    const root = addRouteNode(tree, null, { id: "contact", type: "path", path: "/contact", pageId: "contact-page" });
    const child = addRouteNode(root, "catalog", { id: "category", type: "static", value: "category", pageId: "category-page" });
    expect(describeRoutes(child).map((route) => route.path)).toContain("/contact");
    expect(describeRoutes(child).map((route) => route.path)).toContain("/catalog/category");
  });

  it("moves a route to the root and prunes empty structural parents", () => {
    const result = reparentRoute(tree, "product", null);
    expect(result.error).toBeUndefined();
    expect(describeRoutes(result.tree!).map((route) => route.path)).toEqual([
      "/catalog",
      "/about",
      "/{slug}",
    ]);
    expect(result.tree?.children?.[0]?.children).toEqual([]);
  });

  it("moves route branches below another node and rejects cycles", () => {
    const moved = reparentRoute(tree, "product", "about");
    expect(moved.error).toBeUndefined();
    expect(describeRoutes(moved.tree!).find((route) => route.id === "product")?.path).toBe("/about/{slug}");
    expect(reparentRoute(tree, "catalog", "product").error).toContain("самого себя");
  });

  it("preserves priority order and removes empty parent chains", () => {
    const reordered = moveRoutePriority(tree, "about", -1);
    expect(reordered.children?.map((node) => node.id)).toEqual(["about", "catalog"]);

    const siblings = addRouteNode(tree, "catalog", { id: "alternate", type: "static", value: "alternate", pageId: "alternate-page" });
    const nestedReorder = moveRoutePriority(siblings, "alternate", -1);
    expect(nestedReorder.children?.[0]?.children?.map((node) => node.id)).toEqual(["alternate", "products"]);

    const removed = removeRouteBranch(tree, "product");
    expect(describeRoutes(removed).map((route) => route.id)).toEqual(["catalog", "about"]);
    expect(removed.children?.[0]?.children).toEqual([]);
  });
});
