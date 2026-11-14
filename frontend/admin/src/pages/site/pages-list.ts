import type { RouteTreeNode, SitePage } from "@/types";
import { routeCapturesForPage, type RouteDescriptor } from "./route-tree";

export type PageListSort = "manual" | "name" | "route" | "kind";

export type PageListEntry = {
  page: SitePage;
  routes: RouteDescriptor[];
  primaryRoute?: RouteDescriptor;
  featureNames: string[];
  groupKey: string;
  groupLabel: string;
};

export type PageListGroup = {
  key: string;
  label: string;
  entries: PageListEntry[];
};

const pageKindLabels: Record<SitePage["kind"], string> = {
  page: "Обычная страница",
  "collection-list": "Список коллекции",
  detail: "Карточка записи",
};

function pathGroup(path: string | undefined): { key: string; label: string } {
  if (!path) return { key: "unrouted", label: "Без маршрута" };
  const segment = path.split("/").filter(Boolean)[0];
  if (!segment) return { key: "root", label: "Главная" };
  return { key: `segment:${segment}`, label: `/${segment}` };
}

export function pageKindLabel(kind: SitePage["kind"]): string {
  return pageKindLabels[kind];
}

export function pageListEntries(
  tree: RouteTreeNode,
  pages: SitePage[],
  featureNames: ReadonlyMap<string, string> = new Map(),
): PageListEntry[] {
  return pages.map((page) => {
    const routes = routeCapturesForPage(tree, page.id);
    const group = pathGroup(routes[0]?.path);
    return {
      page,
      routes,
      primaryRoute: routes[0],
      featureNames: page.placements.map((placement) => featureNames.get(placement.build.featureKey) ?? ""),
      groupKey: group.key,
      groupLabel: group.label,
    };
  });
}

export function filterPageEntries(entries: PageListEntry[], query: string): PageListEntry[] {
  const needle = query.trim().toLocaleLowerCase("ru");
  if (!needle) return entries;
  return entries.filter(({ page, routes, featureNames }) => {
    const routeText = routes.map((route) => route.path).join(" ");
    const featureText = `${page.placements.map((placement) => placement.build.featureKey).join(" ")} ${featureNames.join(" ")}`;
    return `${page.name} ${pageKindLabel(page.kind)} ${routeText} ${featureText}`
      .toLocaleLowerCase("ru")
      .includes(needle);
  });
}

export function sortPageEntries(entries: PageListEntry[], sort: PageListSort): PageListEntry[] {
  if (sort === "manual") return entries;
  return [...entries].sort((left, right) => {
    const byRoute = (left.primaryRoute?.path ?? "\uffff").localeCompare(
      right.primaryRoute?.path ?? "\uffff",
      "ru",
      { numeric: true },
    );
    if (sort === "route") return byRoute || left.page.name.localeCompare(right.page.name, "ru");
    if (sort === "kind") {
      const byKind = pageKindLabel(left.page.kind).localeCompare(pageKindLabel(right.page.kind), "ru");
      return byKind || byRoute;
    }
    return left.page.name.localeCompare(right.page.name, "ru", { numeric: true }) || byRoute;
  });
}

export function groupPageEntries(entries: PageListEntry[]): PageListGroup[] {
  const groups = new Map<string, PageListGroup>();
  for (const entry of entries) {
    const group = groups.get(entry.groupKey) ?? {
      key: entry.groupKey,
      label: entry.groupLabel,
      entries: [],
    };
    group.entries.push(entry);
    groups.set(entry.groupKey, group);
  }
  return [...groups.values()].sort((left, right) => {
    if (left.key === "unrouted") return 1;
    if (right.key === "unrouted") return -1;
    if (left.key === "root") return -1;
    if (right.key === "root") return 1;
    return left.label.localeCompare(right.label, "ru", { numeric: true });
  });
}

export function visiblePageEntries(
  tree: RouteTreeNode,
  pages: SitePage[],
  query: string,
  sort: PageListSort,
): PageListGroup[] {
  return groupPageEntries(sortPageEntries(filterPageEntries(pageListEntries(tree, pages), query), sort));
}
