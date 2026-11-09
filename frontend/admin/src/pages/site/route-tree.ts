import type { RouteTreeNode, SitePage } from "@/types";

export interface RouteDescriptor {
  id: string;
  node: RouteTreeNode;
  path: string;
  captures: string[];
  parentId?: string;
  depth: number;
}

function routeRoots(tree: RouteTreeNode): RouteTreeNode[] {
  return tree.type === "site" ? tree.children ?? [] : [tree];
}

function joinPath(base: string, segment: string): string {
  if (segment === "/") return "/";
  const prefix = base === "/" ? "" : base;
  return `${prefix}/${segment.replace(/^\/+/, "")}`;
}

function regexCaptureNames(pattern: string): string[] {
  return Array.from(pattern.matchAll(/\(\?<([A-Za-z][A-Za-z0-9_]*)>/g), (match) => match[1]);
}

function rootPathCaptures(path: string): string[] {
  return path.split("/").filter(Boolean).flatMap((segment) => {
    if (segment.startsWith(":" ) || segment.startsWith("*")) return [segment.slice(1)];
    if (segment.startsWith("~")) return regexCaptureNames(segment.slice(1));
    return [];
  });
}

function displayRootPath(path: string): string {
  if (path === "/") return "/";
  return path.split("/").map((segment) => {
    if (segment.startsWith(":")) return `{${segment.slice(1)}}`;
    if (segment.startsWith("*")) return `{${segment.slice(1)}}`;
    if (segment.startsWith("~")) return `{${regexCaptureNames(segment.slice(1))[0] ?? "regex"}}`;
    return segment;
  }).join("/");
}

function nodeSegment(node: RouteTreeNode): { path: string; captures: string[] } {
  switch (node.type) {
    case "path":
      return { path: displayRootPath(node.path ?? "/"), captures: rootPathCaptures(node.path ?? "/") };
    case "index":
      return { path: "/", captures: [] };
    case "static":
      return { path: `/${node.value ?? ""}`, captures: [] };
    case "param":
      return { path: `/{${node.name ?? "param"}}`, captures: node.name ? [node.name] : [] };
    case "regex": {
      const names = regexCaptureNames(node.pattern ?? "");
      return { path: `/{${names[0] ?? "regex"}}`, captures: names };
    }
    case "catchAll":
      return { path: `/{${node.name ?? "path"}}`, captures: node.name ? [node.name] : [] };
    case "site":
      return { path: "", captures: [] };
  }
}

export function describeRoutes(tree: RouteTreeNode): RouteDescriptor[] {
  const result: RouteDescriptor[] = [];
  const visit = (node: RouteTreeNode, parentPath: string, inherited: string[], parentId: string | undefined, depth: number) => {
    const own = nodeSegment(node);
    const path = node.type === "site" ? parentPath : node.type === "path" ? own.path : joinPath(parentPath, own.path);
    const captures = [...inherited, ...own.captures];
    if (node.type !== "site") result.push({ id: node.id, node, path: path || "/", captures, parentId, depth });
    for (const child of node.children ?? []) visit(child, path, captures, node.type === "site" ? undefined : node.id, node.type === "site" ? 0 : depth + 1);
  };
  for (const root of routeRoots(tree)) visit(root, "", [], undefined, 0);
  return result;
}

// Keep this aligned with the server's staticPagePaths: a system page cannot
// be rendered using an invented parameter or a dynamic-only route.
export function staticPageRouteIds(tree: RouteTreeNode): Set<string> {
  const pageIds = new Set<string>();
  const visit = (node: RouteTreeNode, inheritedDynamic: boolean) => {
    let dynamic = inheritedDynamic;
    if (node.type === "path") dynamic = /[:~*]/.test(node.path ?? "");
    else if (!["site", "index", "static"].includes(node.type)) dynamic = true;
    if (!dynamic && node.pageId) pageIds.add(node.pageId);
    for (const child of node.children ?? []) visit(child, dynamic);
  };
  visit(tree, false);
  return pageIds;
}

export function routeCapturesForPage(tree: RouteTreeNode, pageId: string): RouteDescriptor[] {
  return describeRoutes(tree).filter((route) => route.node.pageId === pageId);
}

export function detailRoutes(tree: RouteTreeNode, pages: SitePage[]): RouteDescriptor[] {
  const detailPageIds = new Set(pages.filter((page) => page.kind === "detail").map((page) => page.id));
  return describeRoutes(tree).filter((route) => route.node.pageId && detailPageIds.has(route.node.pageId));
}

export function updateRouteNode(
  tree: RouteTreeNode,
  id: string,
  update: (node: RouteTreeNode) => RouteTreeNode,
): RouteTreeNode {
  if (tree.id === id) return update(tree);
  return {
    ...tree,
    children: (tree.children ?? []).map((child) => updateRouteNode(child, id, update)),
  };
}

export function addRouteNode(tree: RouteTreeNode, parentId: string | null, node: RouteTreeNode): RouteTreeNode {
  if (tree.type !== "site") {
    if (parentId === null) return { id: "site", type: "site", children: [tree, node] };
    const site: RouteTreeNode = { id: "site", type: "site", children: [tree] };
    return updateRouteNode(site, parentId, (parent) => ({ ...parent, children: [...(parent.children ?? []), node] }));
  }
  if (parentId === null) return { ...tree, children: [...(tree.children ?? []), node] };
  return updateRouteNode(tree, parentId, (parent) => ({ ...parent, children: [...(parent.children ?? []), node] }));
}

export function removeRouteBranch(tree: RouteTreeNode, id: string): RouteTreeNode {
  const remove = (node: RouteTreeNode): RouteTreeNode | null => {
    if (node.id === id) return null;
    const children = (node.children ?? []).map(remove).filter((child): child is RouteTreeNode => child !== null);
    if (node.type !== "site" && !node.pageId && children.length === 0) return null;
    return { ...node, children };
  };
  return remove(tree) ?? { id: "site", type: "site", children: [] };
}

export function removePageReferences(tree: RouteTreeNode, pageId: string): RouteTreeNode {
  const clear = (node: RouteTreeNode): RouteTreeNode => {
    const children = (node.children ?? []).map(clear).filter((child) => child.type === "site" || Boolean(child.pageId) || (child.children ?? []).length > 0);
    return { ...node, ...(node.pageId === pageId ? { pageId: undefined } : {}), children };
  };
  return clear(tree);
}

export function moveRoutePriority(tree: RouteTreeNode, id: string, offset: -1 | 1): RouteTreeNode {
  const reorder = (children: RouteTreeNode[]) => {
    const from = children.findIndex((child) => child.id === id);
    const to = from + offset;
    if (from < 0 || to < 0 || to >= children.length) return children;
    const next = [...children];
    [next[from], next[to]] = [next[to], next[from]];
    return next;
  };
  if (tree.type === "site" && (tree.children ?? []).some((child) => child.id === id)) {
    return { ...tree, children: reorder(tree.children ?? []) };
  }
  return updateRouteNode(tree, tree.id, (node) => node).children?.some((child) => child.id === id)
    ? { ...tree, children: reorder(tree.children ?? []) }
    : updateChildrenWithPriority(tree, id, offset, reorder);
}

function updateChildrenWithPriority(
  tree: RouteTreeNode,
  id: string,
  offset: -1 | 1,
  reorder: (children: RouteTreeNode[]) => RouteTreeNode[],
): RouteTreeNode {
  return {
    ...tree,
    children: reorder(tree.children ?? []).map((child) => updateChildrenWithPriority(child, id, offset, reorder)),
  };
}

function findRoute(tree: RouteTreeNode, id: string): RouteTreeNode | undefined {
  if (tree.id === id) return tree;
  for (const child of tree.children ?? []) {
    const found = findRoute(child, id);
    if (found) return found;
  }
  return undefined;
}

function containsRoute(tree: RouteTreeNode, id: string): boolean {
  return tree.id === id || (tree.children ?? []).some((child) => containsRoute(child, id));
}

function endsWithCatchAll(node: RouteTreeNode): boolean {
  if (node.type === "catchAll") return true;
  return node.type === "path" && (node.path ?? "").split("/").filter(Boolean).at(-1)?.startsWith("*") === true;
}

function removeAndTake(nodes: RouteTreeNode[], id: string): { nodes: RouteTreeNode[]; moved?: RouteTreeNode } {
  const index = nodes.findIndex((node) => node.id === id);
  if (index >= 0) return { nodes: nodes.filter((_, nodeIndex) => nodeIndex !== index), moved: nodes[index] };
  let moved: RouteTreeNode | undefined;
  const next = nodes.map((node) => {
    const result = removeAndTake(node.children ?? [], id);
    if (result.moved) moved = result.moved;
    return result.moved ? { ...node, children: result.nodes } : node;
  });
  return { nodes: next, moved };
}

function pruneEmptyRoutes(nodes: RouteTreeNode[]): RouteTreeNode[] {
  return nodes
    .map((node) => ({ ...node, children: pruneEmptyRoutes(node.children ?? []) }))
    .filter((node) => node.type === "site" || Boolean(node.pageId) || (node.children ?? []).length > 0);
}

function pathToSegments(path: string, route: RouteTreeNode): RouteTreeNode | null {
  if (path === "/") return null;
  const parts = path.split("/").filter(Boolean);
  let current: RouteTreeNode | undefined;
  let first: RouteTreeNode | undefined;
  parts.forEach((part, index) => {
    const last = index === parts.length - 1;
    let segment: RouteTreeNode;
    if (part.startsWith(":")) segment = { id: last ? route.id : `route-${crypto.randomUUID().slice(0, 8)}`, type: "param", name: part.slice(1), ...(last ? { pageId: route.pageId } : {}), children: [] };
    else if (part.startsWith("*")) segment = { id: last ? route.id : `route-${crypto.randomUUID().slice(0, 8)}`, type: "catchAll", name: part.slice(1), ...(last ? { pageId: route.pageId } : {}), children: [] };
    else if (part.startsWith("~")) segment = { id: last ? route.id : `route-${crypto.randomUUID().slice(0, 8)}`, type: "regex", pattern: part.slice(1), ...(last ? { pageId: route.pageId } : {}), children: [] };
    else segment = { id: last ? route.id : `route-${crypto.randomUUID().slice(0, 8)}`, type: "static", value: part, ...(last ? { pageId: route.pageId } : {}), children: [] };
    if (!first) first = segment;
    if (current) current.children = [segment];
    current = segment;
  });
  if (!first || !current) return null;
  current.children = route.children ?? [];
  return first;
}

function asRootRoute(node: RouteTreeNode): RouteTreeNode | null {
  if (node.type === "path") return node;
  if (node.type === "index") return { ...node, type: "path", path: "/" };
  const segment = nodeSegment(node).path;
  const token = segment.replace(/^\//, "");
  const path = token.startsWith("{") && token.endsWith("}")
    ? node.type === "regex" ? `/~${node.pattern ?? ""}` : node.type === "catchAll" ? `/*${node.name ?? "path"}` : `/:${node.name ?? "param"}`
    : `/${token}`;
  return { id: node.id, type: "path", path, ...(node.pageId ? { pageId: node.pageId } : {}), children: node.children ?? [] };
}

export function reparentRoute(tree: RouteTreeNode, sourceId: string, targetId: string | null): { tree?: RouteTreeNode; error?: string } {
  const source = findRoute(tree, sourceId);
  if (!source || source.type === "site") return { error: "Маршрут не найден" };
  if (targetId && containsRoute(source, targetId)) return { error: "Нельзя вложить маршрут в самого себя" };
  const target = targetId ? findRoute(tree, targetId) : undefined;
  if (targetId && !target) return { error: "Родительский маршрут не найден" };
  if (target && endsWithCatchAll(target)) return { error: "Catch-all маршрут не может содержать дочерние узлы" };
  if (source.type === "index" && target) return { error: "Корневой маршрут / нельзя вложить в другой маршрут" };

  const roots = routeRoots(tree);
  const removed = removeAndTake(roots, sourceId);
  if (!removed.moved) return { error: "Не удалось переместить маршрут" };
  const remainingRoots = pruneEmptyRoutes(removed.nodes);
  let moved = removed.moved;
  if (target) {
    if (moved.type === "path") {
      const segments = pathToSegments(moved.path ?? "/", moved);
      if (!segments) return { error: "Маршрут / нельзя вложить в другой маршрут" };
      moved = segments;
    }
    if (moved.type === "site" || moved.type === "index") return { error: "Этот узел нельзя вложить" };
    const interim: RouteTreeNode = { id: "site", type: "site", children: remainingRoots };
    const next = updateRouteNode(interim, targetId!, (parent) => ({ ...parent, children: [...(parent.children ?? []), moved] }));
    return { tree: { ...tree, children: next.children ?? [] } };
  }

  const root = asRootRoute(moved);
  if (!root) return { error: "Не удалось сделать маршрут самостоятельным" };
  return { tree: { id: "site", type: "site", children: [...remainingRoots, root] } };
}

export function routePathForNode(tree: RouteTreeNode, id: string): string | undefined {
  return describeRoutes(tree).find((route) => route.id === id)?.path;
}

export function pageForRoute(tree: RouteTreeNode, id: string, pages: SitePage[]): SitePage | undefined {
  const pageId = findRoute(tree, id)?.pageId;
  return pages.find((page) => page.id === pageId);
}

export function routePages(tree: RouteTreeNode, pages: SitePage[]): Array<{ page: SitePage; routes: RouteDescriptor[] }> {
  return pages.map((page) => ({ page, routes: routeCapturesForPage(tree, page.id) }));
}

export function createPageFromRoute(node: RouteTreeNode, path: string): SitePage {
  const raw = node.type === "path" ? node.path ?? path : path;
  const lastPart = raw.split("/").filter(Boolean).at(-1);
  const readable = lastPart?.startsWith(":") ? `Страница: ${lastPart.slice(1)}` : lastPart?.startsWith("*") ? `Страница: ${lastPart.slice(1)}` : lastPart?.startsWith("~") ? "Страница по шаблону" : lastPart ? decodeURIComponent(lastPart) : "Главная";
  const baseId = `page-${crypto.randomUUID().slice(0, 8)}`;
  return { id: baseId, name: readable, kind: "page", placements: [] };
}
