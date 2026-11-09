import type { KeyboardEvent, RefObject } from "react";
import { ArrowDown, ArrowUp, Link2, Plus, Trash2 } from "lucide-react";
import type { RouteTreeNode } from "@/types";
import { describeRoutes } from "../../route-tree";
import { RouteMenuAction } from "./RouteMenuAction";
import { currentNode } from "./route-utils";
import type { ContextState } from "./route-types";

export function RouteContextMenu({
  menu,
  tree,
  panelRef,
  onKeyDown,
  onOpenPage,
  onAddChild,
  onEditRoute,
  onSelectParent,
  onChangeParent,
  onMovePriority,
  onDeleteBranch,
  onOpenOrphanPage,
  onCreateRouteForPage,
  onCreatePage,
  onAttachExistingPage,
}: {
  menu: ContextState | null;
  tree: RouteTreeNode;
  panelRef: RefObject<HTMLDivElement>;
  onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void;
  onOpenPage: (node: RouteTreeNode) => void;
  onAddChild: (routeId: string) => void;
  onEditRoute: (node: RouteTreeNode) => void;
  onSelectParent: (routeId: string) => void;
  onChangeParent: (sourceId: string, targetId: string | null) => void;
  onMovePriority: (routeId: string, direction: -1 | 1) => void;
  onDeleteBranch: (node: RouteTreeNode) => void;
  onOpenOrphanPage: (pageId: string) => void;
  onCreateRouteForPage: (pageId: string) => void;
  onCreatePage: () => void;
  onAttachExistingPage: () => void;
}) {
  if (!menu) return null;

  const selectedNode = currentNode(tree, menu.routeId);
  const descriptors = describeRoutes(tree);
  const routeDescriptor = descriptors.find((route) => route.id === menu.routeId);
  const parentId = routeDescriptor?.parentId;
  const siblingIds = (parentId ? currentNode(tree, parentId)?.children : tree.children)?.map((node) => node.id) ?? [];
  const siblingIndex = menu.routeId ? siblingIds.indexOf(menu.routeId) : -1;
  const canHaveChildren = selectedNode?.type !== "catchAll" && !(selectedNode?.type === "path" && selectedNode.path?.split("/").filter(Boolean).at(-1)?.startsWith("*"));

  return <div ref={panelRef} data-route-context-menu role="menu" aria-label={menu.routeId ? "Действия маршрута" : menu.pageId ? "Действия страницы" : "Действия с маршрутами"} onKeyDown={onKeyDown} className="absolute z-40 max-h-[calc(100%-1rem)] min-w-56 overflow-auto rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg" style={{ left: menu.x, top: menu.y }}>
    {menu.routeId ? <>
      <RouteMenuAction onClick={() => selectedNode && onOpenPage(selectedNode)}>Открыть страницу</RouteMenuAction>
      <RouteMenuAction onClick={() => onAddChild(menu.routeId!)} disabled={!canHaveChildren}><Plus className="size-4" />Добавить дочерний</RouteMenuAction>
      <RouteMenuAction onClick={() => selectedNode && onEditRoute(selectedNode)}>Изменить URL и страницу</RouteMenuAction>
      {selectedNode?.type !== "index" && <RouteMenuAction onClick={() => onSelectParent(menu.routeId!)}><Link2 className="size-4" />Выбрать родительский маршрут</RouteMenuAction>}
      {routeDescriptor?.parentId && <RouteMenuAction onClick={() => onChangeParent(menu.routeId!, null)}><Link2 className="size-4" />Отсоединить в корень</RouteMenuAction>}
      <div className="my-1 border-t border-border" />
      <RouteMenuAction disabled={siblingIndex <= 0} onClick={() => onMovePriority(menu.routeId!, -1)}><ArrowUp className="size-4" />Выше приоритетом</RouteMenuAction>
      <RouteMenuAction disabled={siblingIndex < 0 || siblingIndex === siblingIds.length - 1} onClick={() => onMovePriority(menu.routeId!, 1)}><ArrowDown className="size-4" />Ниже приоритетом</RouteMenuAction>
      <RouteMenuAction className="text-destructive" onClick={() => selectedNode && onDeleteBranch(selectedNode)}><Trash2 className="size-4" />Удалить ветвь</RouteMenuAction>
    </> : menu.pageId ? <>
      <RouteMenuAction onClick={() => onOpenOrphanPage(menu.pageId!)}>Редактировать страницу</RouteMenuAction>
      <RouteMenuAction onClick={() => onCreateRouteForPage(menu.pageId!)}><Plus className="size-4" />Создать для неё маршрут</RouteMenuAction>
    </> : <>
      <RouteMenuAction onClick={onCreatePage}><Plus className="size-4" />Новая самостоятельная страница</RouteMenuAction>
      <RouteMenuAction onClick={onAttachExistingPage}><Link2 className="size-4" />Добавить маршрут к существующей</RouteMenuAction>
    </>}
  </div>;
}
