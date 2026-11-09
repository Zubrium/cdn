import type { RouteTreeNode, SitePage } from "@/types";

export type RoutePosition = {
  node: RouteTreeNode;
  path: string;
  captures: string[];
  x: number;
  y: number;
  width: number;
  height: number;
  parentId?: string;
};

export type OrphanPosition = {
  page: SitePage;
  x: number;
  y: number;
  width: number;
  height: number;
};

export type RouteDialogState = {
  mode: "root" | "child" | "edit";
  parentId?: string;
  routeId?: string;
  existingPageId?: string;
  chooseExisting?: boolean;
};

export type ContextState = {
  x: number;
  y: number;
  routeId?: string;
  pageId?: string;
};

export type PanState = {
  pointerId: number;
  clientX: number;
  clientY: number;
  originX: number;
  originY: number;
};

export type ConnectionState = {
  pointerId: number;
  sourceId: string;
  clientX: number;
  clientY: number;
};
