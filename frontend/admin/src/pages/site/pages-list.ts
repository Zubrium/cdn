import type { RouteTreeNode, SitePage } from "@/types";
import { describeRoutes, routeCapturesForPage, type RouteDescriptor } from "./route-tree";

export type PageListSort = "manual" | "name" | "route" | "kind";

type PageListEntryBase = {
  name: string;
  kindLabel: string;
  routes: RouteDescriptor[];
  primaryRoute?: RouteDescriptor;
  searchText: string;
  groupKey: string;
  groupLabel: string;
};

export type PageListEntry =
  | (PageListEntryBase & {
      kind: "page";
      page: SitePage;
    })
  | (PageListEntryBase & {
      kind: "file";
      route: RouteDescriptor;
      kindLabel: "Файл";
    });

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
  fileNames: ReadonlyMap<string, string> = new Map(),
): PageListEntry[] {
  const pageEntries: PageListEntry[] = pages.map((page) => {
    const routes = routeCapturesForPage(tree, page.id);
    const group = pathGroup(routes[0]?.path);
    const features = page.placements.map((placement) => featureNames.get(placement.build.featureKey) ?? "");
    return {
      kind: "page",
      page,
      name: page.name,
      kindLabel: pageKindLabel(page.kind),
      routes,
      primaryRoute: routes[0],
      searchText: `${page.name} ${pageKindLabel(page.kind)} ${routes.map((route) => route.path).join(" ")} ${page.placements.map((placement) => placement.build.featureKey).join(" ")} ${features.join(" ")}`,
      groupKey: group.key,
      groupLabel: group.label,
    };
  });
  const fileEntries: PageListEntry[] = describeRoutes(tree)
    .filter((route) => Boolean(route.node.fileId) && !route.node.pageId)
    .map((route) => {
      const group = pathGroup(route.path);
      const name = fileNames.get(route.node.fileId!) ?? "Публичный файл";
      return {
        kind: "file",
        route,
        name,
        kindLabel: "Файл",
        routes: [route],
        primaryRoute: route,
        searchText: `${name} файл публичный файл ${route.path}`,
        groupKey: group.key,
        groupLabel: group.label,
      };
    });
  return [...pageEntries, ...fileEntries];
}

export function filterPageEntries(entries: PageListEntry[], query: string): PageListEntry[] {
  const needle = query.trim().toLocaleLowerCase("ru");
  if (!needle) return entries;
  return entries.filter((entry) => entry.searchText.toLocaleLowerCase("ru").includes(needle));
}

export function sortPageEntries(entries: PageListEntry[], sort: PageListSort): PageListEntry[] {
  if (sort === "manual") return entries;
  return [...entries].sort((left, right) => {
    const byRoute = (left.primaryRoute?.path ?? "\uffff").localeCompare(
      right.primaryRoute?.path ?? "\uffff",
      "ru",
      { numeric: true },
    );
    if (sort === "route") return byRoute || left.name.localeCompare(right.name, "ru");
    if (sort === "kind") {
      const byKind = left.kindLabel.localeCompare(right.kindLabel, "ru");
      return byKind || byRoute;
    }
    return left.name.localeCompare(right.name, "ru", { numeric: true }) || byRoute;
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
