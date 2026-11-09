import { describe, expect, it } from "vitest";
import type { SiteConfig } from "@/types";
import { staticPageRouteIds } from "../route-tree";
import { setSystemPageAssignment, systemPageCandidates } from "./SystemPagesDialog";

const config: SiteConfig = {
  formatVersion: 3,
  pages: [
    { id: "missing", name: "Нет маршрута", kind: "page", placements: [] },
    { id: "error", name: "Страница ошибки", kind: "page", placements: [] },
    { id: "detail", name: "Динамическая", kind: "page", placements: [] },
  ],
  routeTree: { id: "root", type: "site", children: [
    { id: "error-route", type: "path", path: "/error", pageId: "error" },
    { id: "dynamic-route", type: "param", name: "slug", pageId: "detail" },
  ] },
  systemPages: { "404": "missing", "403": "detail" },
};

describe("system page configuration", () => {
  it("offers only existing pages with static routes", () => {
    expect(systemPageCandidates(config.pages, staticPageRouteIds(config.routeTree)).map((page) => page.id)).toEqual(["error"]);
  });

  it("selects a page and removes assignments without mutating unrelated mappings or the snapshot", () => {
    const selected = setSystemPageAssignment(config, "404", "error");
    expect(selected.systemPages).toEqual({ "404": "error", "403": "detail" });
    expect(config.systemPages).toEqual({ "404": "missing", "403": "detail" });
    const removed = setSystemPageAssignment(selected, "404", "");
    expect(removed.systemPages).toEqual({ "403": "detail" });
    expect(setSystemPageAssignment(removed, "403", "").systemPages).toEqual({});
    expect(removed.pages).toBe(config.pages);
    expect(removed.routeTree).toBe(config.routeTree);
  });
});
