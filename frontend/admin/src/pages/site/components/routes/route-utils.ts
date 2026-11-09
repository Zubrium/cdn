import type { RouteTreeNode, SitePage } from "@/types";
import { describeRoutes } from "../../route-tree";
import type { RoutePosition } from "./route-types";

const NODE_WIDTH = 286;
const NODE_HEIGHT = 106;
const COLUMN_GAP = 370;
const ROW_GAP = 158;

export function collectPositions(tree: RouteTreeNode): RoutePosition[] {
  const positions: RoutePosition[] = [];
  const descriptions = new Map(describeRoutes(tree).map((route) => [route.id, route]));
  let row = 0;
  const visit = (node: RouteTreeNode, depth: number, parentId: string | undefined): number => {
    const description = descriptions.get(node.id);
    const children = node.children ?? [];
    const childRows = children.map((child) => visit(child, depth + 1, node.id));
    const nodeRow = childRows.length ? childRows.reduce((sum, value) => sum + value, 0) / childRows.length : row++;
    positions.push({
      node,
      path: description?.path ?? "/",
      captures: description?.captures ?? [],
      x: 40 + depth * COLUMN_GAP,
      y: 48 + nodeRow * ROW_GAP,
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      parentId,
    });
    return nodeRow;
  };
  const roots = tree.type === "site" ? tree.children ?? [] : [tree];
  roots.forEach((root) => visit(root, 0, undefined));
  return positions;
}

export function currentNode(tree: RouteTreeNode, id?: string): RouteTreeNode | undefined {
  if (!id) return undefined;
  return describeRoutes(tree).find((route) => route.id === id)?.node;
}

export function pageName(pages: SitePage[], id?: string) {
  return pages.find((page) => page.id === id)?.name;
}

export function displayNodeType(node: RouteTreeNode) {
  switch (node.type) {
    case "path": return "Корневой адрес";
    case "index": return "Главная";
    case "static": return "Статический сегмент";
    case "param": return "Параметр пути";
    case "regex": return "Regex-сегмент";
    case "catchAll": return "Catch-all";
    case "site": return "Сайт";
  }
}

export function readRouteSegment(node: RouteTreeNode): string {
  if (node.type === "path") return node.path ?? "/";
  if (node.type === "index") return "/";
  if (node.type === "static") return node.value ?? "";
  if (node.type === "param") return `:${node.name ?? "slug"}`;
  if (node.type === "regex") return `~${node.pattern ?? ""}`;
  if (node.type === "catchAll") return `*${node.name ?? "path"}`;
  return "";
}

export function readRootPath(node: RouteTreeNode): string {
  if (node.type === "path") return node.path ?? "/";
  if (node.type === "index") return "/";
  if (node.type === "static") return `/${node.value ?? ""}`;
  if (node.type === "param") return `/:${node.name ?? "slug"}`;
  if (node.type === "regex") return `/~${node.pattern ?? ""}`;
  if (node.type === "catchAll") return `/*${node.name ?? "path"}`;
  return "/";
}

export function parseAbsolutePath(value: string): { path?: string; error?: string } {
  const path = value.trim();
  if (path === "/") return { path };
  if (!path.startsWith("/") || path.endsWith("/") || path.length > 500 || path.includes("#")) {
    return { error: "Введите абсолютный путь без завершающего слеша, query и fragment." };
  }
  const parts = path.slice(1).split("/");
  if (parts.some((part) => !part)) return { error: "Путь не может содержать пустые сегменты." };
  for (const [index, part] of parts.entries()) {
    if (part.includes("?") && !part.startsWith("~")) return { error: "Символ ? допустим только внутри regex-сегмента." };
    if ((part === "." || part === "..") && !part.startsWith("~")) return { error: "Сегменты . и .. нельзя использовать в маршруте." };
    if (part.startsWith(":")) {
      if (!/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(part.slice(1))) return { error: `Некорректный параметр «${part}».` };
    } else if (part.startsWith("*")) {
      if (index !== parts.length - 1 || !/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(part.slice(1))) return { error: "Catch-all должен быть последним сегментом и иметь имя: *path." };
    } else if (part.startsWith("~")) {
      if (!part.slice(1)) return { error: "Regex-сегмент не может быть пустым." };
    } else if (part.includes("/")) return { error: "Статический сегмент не может содержать /." };
  }
  return { path };
}

export function childNodeFromSegment(input: string, id: string, pageId?: string, children: RouteTreeNode[] = []): { node?: RouteTreeNode; error?: string } {
  const segment = input.trim();
  if (!segment || segment.includes("/") || segment.includes("#") || (segment.includes("?") && !segment.startsWith("~"))) return { error: "Введите ровно один URL-сегмент." };
  if (segment === "." || segment === "..") return { error: "Сегменты . и .. нельзя использовать в маршруте." };
  const common = { id, ...(pageId ? { pageId } : {}), children };
  if (segment.startsWith(":")) {
    const name = segment.slice(1);
    return /^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(name) ? { node: { ...common, type: "param", name } } : { error: "Имя параметра должно начинаться с буквы и содержать только буквы, цифры и _." };
  }
  if (segment.startsWith("*")) {
    const name = segment.slice(1);
    if (!/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(name)) return { error: "Catch-all должен иметь имя, например *path." };
    if (children.length) return { error: "Catch-all не может содержать дочерние маршруты." };
    return { node: { ...common, type: "catchAll", name } };
  }
  if (segment.startsWith("~")) return segment.length > 1 ? { node: { ...common, type: "regex", pattern: segment.slice(1) } } : { error: "Regex-сегмент не может быть пустым." };
  return { node: { ...common, type: "static", value: segment } };
}

export function suggestedName(path: string) {
  if (path === "/") return "Главная";
  const part = path.split("/").filter(Boolean).at(-1) ?? "Новая страница";
  const value = part.replace(/^[:*~]/, "").replace(/\(\?<[^>]+>/g, "").replace(/[()?+*]/g, "").replaceAll("[", "").replaceAll("]", "");
  return value ? value.charAt(0).toLocaleUpperCase("ru-RU") + value.slice(1) : "Новая страница";
}

export function comparableRootPath(path: string) {
  if (path === "/") return "/";
  return path.split("/").map((segment) => {
    if (segment.startsWith(":")) return `{${segment.slice(1)}}`;
    if (segment.startsWith("*")) return `{${segment.slice(1)}}`;
    if (segment.startsWith("~")) return `{${Array.from(segment.matchAll(/\(\?<([A-Za-z][A-Za-z0-9_]*)>/g), (match) => match[1])[0] ?? "regex"}}`;
    return segment;
  }).join("/");
}

export function defaultRootPath(tree: RouteTreeNode) {
  const existing = new Set(describeRoutes(tree).map((route) => route.path));
  let suffix = 1;
  let candidate = "/new-page";
  while (existing.has(candidate)) candidate = `/new-page-${++suffix}`;
  return candidate;
}

export function isDescendant(tree: RouteTreeNode, ancestorId: string, id: string): boolean {
  const ancestor = currentNode(tree, ancestorId);
  const contains = (node: RouteTreeNode): boolean => node.id === id || (node.children ?? []).some(contains);
  return Boolean(ancestor?.children?.some(contains));
}
