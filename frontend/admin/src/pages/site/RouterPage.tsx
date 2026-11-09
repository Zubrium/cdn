import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from "react";
import { AlertTriangle, ArrowUp, FilePlus2, GitBranch, Home, Maximize2, MoreHorizontal, Plus, ZoomIn, ZoomOut } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/common/confirm";
import { ContextHelp } from "@/components/common/page";
import type { RouteTreeNode, SitePage } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";
import PageEditorPage from "./PageEditorPage";
import { SystemPagesDialog } from "./components/SystemPagesDialog";
import { addRouteNode, createPageFromRoute, describeRoutes, moveRoutePriority, removeRouteBranch, reparentRoute, updateRouteNode } from "./route-tree";
import type { ConnectionState, ContextState, OrphanPosition, PanState, RouteDialogState } from "./components/routes/route-types";
import { RouteEditorDialog, RouteParentDialog } from "./components/routes/RouteDialogs";
import { RouteContextMenu } from "./components/routes/RouteContextMenu";
import { collectPositions, displayNodeType, isDescendant, pageName } from "./components/routes/route-utils";


const NODE_WIDTH = 286;
const NODE_HEIGHT = 106;
export default function RouterPage() {
  const { config, update, dirty, saving, conflict, revision, reload, setPageEditorOpen } = useSiteWorkspace();
  const confirm = useConfirm();
  const [editingPageId, setEditingPageId] = useState<string | null>(null);
  const [selectedRouteId, setSelectedRouteId] = useState<string | null>(null);
  const [dialog, setDialog] = useState<RouteDialogState | null>(null);
  const [reparentRouteId, setReparentRouteId] = useState<string | null>(null);
  const [systemPagesOpen, setSystemPagesOpen] = useState(false);

  useLayoutEffect(() => {
    setPageEditorOpen(Boolean(editingPageId));
  }, [editingPageId, setPageEditorOpen]);
  const [menu, setMenu] = useState<ContextState | null>(null);
  const [scale, setScale] = useState(1);
  const [pan, setPan] = useState({ x: 32, y: 40 });
  const [panGesture, setPanGesture] = useState<PanState | null>(null);
  const [connection, setConnection] = useState<ConnectionState | null>(null);
  const canvasRef = useRef<HTMLDivElement>(null);
  const menuPanelRef = useRef<HTMLDivElement>(null);
  const menuTriggerRef = useRef<HTMLButtonElement | null>(null);
  const hasFit = useRef(false);
  const manualView = useRef(false);
  const root = config.routeTree;
  const positions = useMemo(() => collectPositions(root), [root]);
  const routeIds = useMemo(() => new Set(positions.map((position) => position.node.pageId).filter((id): id is string => Boolean(id))), [positions]);
  const orphanPages = useMemo(() => config.pages.filter((page) => !routeIds.has(page.id)), [config.pages, routeIds]);
  const maxRouteY = positions.reduce((max, position) => Math.max(max, position.y + position.height), 120);
  const orphanPositions: OrphanPosition[] = orphanPages.map((page, index) => ({ page, x: 40 + index * (NODE_WIDTH + 28), y: maxRouteY + 92, width: NODE_WIDTH, height: NODE_HEIGHT }));
  const canvasWidth = Math.max(760, ...positions.map((position) => position.x + position.width + 120), ...orphanPositions.map((position) => position.x + position.width + 80));
  const canvasHeight = Math.max(540, maxRouteY + (orphanPages.length ? 260 : 120));
  const contentLeft = Math.min(0, ...positions.map((position) => position.x), ...orphanPositions.map((position) => position.x));
  const contentTop = Math.min(0, ...positions.map((position) => position.y), ...orphanPositions.map((position) => position.y));
  const contentRight = Math.max(1, ...positions.map((position) => position.x + position.width), ...orphanPositions.map((position) => position.x + position.width));
  const contentBottom = Math.max(1, ...positions.map((position) => position.y + position.height), ...orphanPositions.map((position) => position.y + position.height));
  const fitContentWidth = Math.max(200, contentRight - contentLeft + 80);
  const fitContentHeight = Math.max(200, contentBottom - contentTop + 160);
  const positionById = useMemo(() => new Map(positions.map((position) => [position.node.id, position])), [positions]);
  const descriptors = useMemo(() => describeRoutes(root), [root]);

  const fitCanvas = useCallback(() => {
    const viewport = canvasRef.current;
    if (!viewport) return;
    const bounds = viewport.getBoundingClientRect();
    const availableWidth = Math.max(200, bounds.width - 100);
    const availableHeight = Math.max(200, bounds.height - 150);
    const minimumScale = bounds.width <= 640 ? 0.62 : 0.22;
    const nextScale = Math.max(minimumScale, Math.min(1.1, availableWidth / fitContentWidth, availableHeight / fitContentHeight));
    manualView.current = false;
    setScale(nextScale);
    setPan({
      x: Math.max(24, (bounds.width - (contentRight - contentLeft) * nextScale) / 2 - contentLeft * nextScale),
      y: Math.max(86, 90 + (availableHeight - fitContentHeight * nextScale) / 2 - contentTop * nextScale),
    });
  }, [fitContentWidth, fitContentHeight, contentLeft, contentTop, contentRight]);

  useLayoutEffect(() => {
    if (!hasFit.current && positions.length) {
      fitCanvas();
      hasFit.current = true;
    }
  }, [fitCanvas, positions.length]);

  useEffect(() => {
    const viewport = canvasRef.current;
    if (!viewport || editingPageId || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      if (!manualView.current) fitCanvas();
    });
    observer.observe(viewport);
    return () => observer.disconnect();
  }, [editingPageId, fitCanvas]);

  useEffect(() => {
    if (!menu) return;
    const focusFrame = window.requestAnimationFrame(() => {
      menuPanelRef.current?.querySelector<HTMLButtonElement>('[role="menuitem"]:not(:disabled)')?.focus();
    });
    const close = (event: Event) => {
      if (event.type === "keydown" && (event as KeyboardEvent).key === "Escape") {
        event.preventDefault();
        setMenu(null);
        window.requestAnimationFrame(() => menuTriggerRef.current?.focus());
      }
      if (event.type === "pointerdown" && (!(event.target instanceof Element) || !event.target.closest("[data-route-context-menu]"))) setMenu(null);
    };
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", close);
    return () => { window.cancelAnimationFrame(focusFrame); document.removeEventListener("pointerdown", close); document.removeEventListener("keydown", close); };
  }, [menu]);

  const applyTree = (tree: RouteTreeNode) => update((current) => ({ ...current, routeTree: tree, formatVersion: 3 }));
  const makeNewRoute = (node: RouteTreeNode, parentId: string | null, page?: SitePage) => {
    if (page) update((current) => ({ ...current, pages: [...current.pages, page], routeTree: addRouteNode(current.routeTree, parentId, node), formatVersion: 3 }));
    else applyTree(addRouteNode(root, parentId, node));
    setSelectedRouteId(node.id);
    if (page) setEditingPageId(page.id);
    setDialog(null);
  };

  const applyRouteEdit = (node: RouteTreeNode, _parentId: null, page?: SitePage) => {
    update((current) => ({
      ...current,
      pages: page ? [...current.pages, page] : current.pages,
      routeTree: updateRouteNode(current.routeTree, node.id, () => node),
      formatVersion: 3,
    }));
    setDialog(null);
    setMenu(null);
    if (page) setEditingPageId(page.id);
  };

  const openPageForRoute = (node: RouteTreeNode) => {
    let pageId = node.pageId;
    if (!pageId) {
      const descriptor = descriptors.find((route) => route.id === node.id);
      const page = createPageFromRoute(node, descriptor?.path ?? "/");
      pageId = page.id;
      update((current) => ({
        ...current,
        pages: [...current.pages, page],
        routeTree: updateRouteNode(current.routeTree, node.id, (currentNode) => ({ ...currentNode, pageId: page.id })),
        formatVersion: 3,
      }));
    }
    setSelectedRouteId(node.id);
    setMenu(null);
    setEditingPageId(pageId);
  };

  const openMenuAt = (clientX: number, clientY: number, routeId?: string, pageId?: string) => {
    const bounds = canvasRef.current?.getBoundingClientRect();
    if (!bounds) return;
    const maxX = Math.max(8, bounds.width - 250);
    const maxY = Math.max(8, bounds.height - 280);
    setMenu({ x: Math.max(8, Math.min(clientX - bounds.left, maxX)), y: Math.max(8, Math.min(clientY - bounds.top, maxY)), routeId, pageId });
  };
  const contextMenu = (event: ReactMouseEvent, routeId?: string, pageId?: string) => {
    event.preventDefault();
    event.stopPropagation();
    const target = event.target instanceof Element ? event.target.closest("button") : null;
    menuTriggerRef.current = target instanceof HTMLButtonElement ? target : null;
    openMenuAt(event.clientX, event.clientY, routeId, pageId);
  };

  const openChildDialog = (parentId: string) => { setDialog({ mode: "child", parentId }); setMenu(null); };
  const openRootDialog = (existingPageId?: string, chooseExisting = false) => { setDialog({ mode: "root", existingPageId, chooseExisting }); setMenu(null); };
  const editRouteDialog = (node: RouteTreeNode) => { setDialog({ mode: "edit", routeId: node.id }); setMenu(null); };

  const deleteBranch = (node: RouteTreeNode) => {
    const descendants = positions.filter((position) => position.node.id === node.id || isDescendant(root, node.id, position.node.id)).length;
    const removedIds = new Set(positions.filter((position) => position.node.id === node.id || isDescendant(root, node.id, position.node.id)).map((position) => position.node.id));
    const remainsRouted = positions.some((position) => !removedIds.has(position.node.id) && Boolean(position.node.pageId));
    if (!remainsRouted) { toast.error("Нельзя удалить последний маршрут сайта."); return; }
    if (!window.confirm(descendants > 1 ? `Удалить маршрут «${descriptors.find((route) => route.id === node.id)?.path}» и ${descendants - 1} дочерних маршрутов? Страницы останутся в черновиках без маршрута.` : `Удалить маршрут «${descriptors.find((route) => route.id === node.id)?.path}»? Страница останется в черновиках без маршрута.`)) return;
    applyTree(removeRouteBranch(root, node.id));
    setSelectedRouteId(null);
    setMenu(null);
  };

  const changeParent = (sourceId: string, targetId: string | null) => {
    const result = reparentRoute(root, sourceId, targetId);
    if (!result.tree) { toast.error(result.error ?? "Не удалось соединить маршруты"); return false; }
    applyTree(result.tree);
    setMenu(null);
    return true;
  };

  const refreshAfterConflict = async () => {
    if (dirty && !(await confirm({
      title: "Заменить текущие изменения?",
      description: "Загрузка актуальной версии заменит текущую локальную рабочую копию редактора.",
      confirmLabel: "Загрузить актуальную версию",
      cancelLabel: "Остаться в редакторе",
    }))) return;
    await reload();
  };

  const menuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const items = [...(menuPanelRef.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [])];
    if (!items.length) return;
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Home" || event.key === "End") {
      event.preventDefault();
      const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
      items[next]?.focus();
    }
  };

  const openPageForOrphan = (pageId: string) => { setEditingPageId(pageId); setMenu(null); };

  const onCanvasPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    if ((event.target as HTMLElement).closest("button, [data-route-id], input, [role=menu]")) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    setPanGesture({ pointerId: event.pointerId, clientX: event.clientX, clientY: event.clientY, originX: pan.x, originY: pan.y });
    setMenu(null);
  };
  const onCanvasPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (panGesture?.pointerId === event.pointerId) {
      manualView.current = true;
      setPan({ x: panGesture.originX + event.clientX - panGesture.clientX, y: panGesture.originY + event.clientY - panGesture.clientY });
    }
    if (connection?.pointerId === event.pointerId) setConnection({ ...connection, clientX: event.clientX, clientY: event.clientY });
  };
  const onCanvasPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (panGesture?.pointerId === event.pointerId) setPanGesture(null);
    if (connection?.pointerId === event.pointerId) {
      const hit = document.elementFromPoint(event.clientX, event.clientY)?.closest<HTMLElement>("[data-route-id]");
      const targetId = hit?.dataset.routeId ?? null;
      if (targetId === connection.sourceId) setConnection(null);
      else { setConnection(null); changeParent(connection.sourceId, targetId); }
    }
  };
  const onCanvasWheel = (event: ReactWheelEvent<HTMLDivElement>) => {
    event.preventDefault();
    manualView.current = true;
    const direction = event.deltaY < 0 ? 1 : -1;
    setScale((current) => Math.max(0.22, Math.min(1.5, current + direction * 0.06)));
  };

  const zoomBy = (amount: number) => {
    manualView.current = true;
    setScale((current) => Math.max(0.22, Math.min(1.5, current + amount)));
  };

  if (editingPageId) return <PageEditorPage pageId={editingPageId} onBack={() => setEditingPageId(null)} />;

  const source = connection ? positionById.get(connection.sourceId) : undefined;
  const connectionPoint = connection && canvasRef.current ? (() => {
    const bounds = canvasRef.current!.getBoundingClientRect();
    return { x: (connection.clientX - bounds.left - pan.x) / scale, y: (connection.clientY - bounds.top - pan.y) / scale };
  })() : null;
  const pathRootCount = root.children?.length ?? 0;

  return <div className="relative h-full min-h-0 overflow-hidden bg-background" onContextMenu={(event) => { if (!(event.target as HTMLElement).closest("[data-route-id], [data-route-context-menu], button, input, [role=dialog]")) contextMenu(event); }}>
    <div role="region" aria-label="Карта маршрутов сайта" className="absolute inset-0" data-route-canvas ref={canvasRef} onPointerDown={onCanvasPointerDown} onPointerMove={onCanvasPointerMove} onPointerUp={onCanvasPointerUp} onPointerCancel={onCanvasPointerUp} onWheel={onCanvasWheel} style={{ cursor: panGesture ? "grabbing" : connection ? "crosshair" : "grab", touchAction: "none" }}>
      <div aria-hidden="true" className="pointer-events-none absolute inset-0 opacity-65" style={{ backgroundImage: "radial-gradient(hsl(var(--border)) .75px, transparent .75px)", backgroundSize: "24px 24px" }} />
      <div className="absolute left-3 right-3 top-3 z-20 flex flex-wrap items-center justify-between gap-2 sm:left-5 sm:right-5 sm:top-5" onContextMenu={(event) => event.stopPropagation()}>
        <div className="flex min-w-0 items-center gap-2 rounded-lg border border-border bg-card/95 px-3 py-2 shadow-sm backdrop-blur-sm">
          <GitBranch className="size-4 shrink-0 text-primary" />
          <div className="min-w-0"><h1 className="truncate text-sm font-semibold">Страницы и маршруты</h1><p className="hidden text-xs text-muted-foreground sm:block">{pathRootCount} корневых маршрута · {positions.length} узлов · ревизия {revision}</p></div>
          <span role="status" aria-live="polite" className={`ml-1 flex shrink-0 items-center gap-1 rounded-full px-2 py-1 text-xs ${dirty ? "bg-warning/10 text-warning" : "bg-success/10 text-success"}`}><span aria-hidden="true" className={`size-1.5 rounded-full ${dirty ? "bg-warning" : "bg-success"}`} />{dirty ? "Изменено" : "Сохранено"}</span>
          {conflict && <span role="status" className="hidden items-center gap-1 text-xs text-destructive md:flex"><AlertTriangle className="size-3.5" />Черновик изменился</span>}
        </div>
        <div role="toolbar" aria-label="Инструменты маршрутов" className="flex items-center gap-1 rounded-lg border border-border bg-card/95 p-1 shadow-sm backdrop-blur-sm">
          <Button type="button" size="sm" variant="ghost" className="min-h-11 min-w-11 px-2 sm:px-3" onClick={() => setSystemPagesOpen(true)} aria-label="Настроить системные страницы"><AlertTriangle /><span className="hidden sm:inline">Системные страницы</span></Button>
          <Button type="button" size="sm" variant="ghost" className="min-h-11 min-w-11 px-2 sm:px-3" onClick={() => openRootDialog()} aria-label="Добавить корневой маршрут"><Plus /><span className="hidden sm:inline">Корневой маршрут</span></Button>
          <span className="mx-1 h-5 border-l border-border" />
          <Button type="button" size="icon" variant="ghost" className="size-11" aria-label="Уменьшить масштаб" onClick={() => zoomBy(-0.1)}><ZoomOut /></Button>
          <span className="w-10 text-center text-xs tabular-nums text-muted-foreground">{Math.round(scale * 100)}%</span>
          <Button type="button" size="icon" variant="ghost" className="size-11" aria-label="Увеличить масштаб" onClick={() => zoomBy(0.1)}><ZoomIn /></Button>
          <Button type="button" size="icon" variant="ghost" className="size-11" aria-label="Вписать маршруты в окно" onClick={fitCanvas}><Maximize2 /></Button>
          {conflict && <Button type="button" size="icon" variant="outline" aria-label="Загрузить актуальную версию" disabled={saving} onClick={() => void refreshAfterConflict()}><ArrowUp /></Button>}
        </div>
      </div>

      <div className="absolute bottom-4 left-4 z-10 sm:bottom-5 sm:left-5">
        <ContextHelp
          label="Подсказка по маршрутам"
          side="top"
          buttonClassName="size-10 rounded-xl border border-input bg-card/95 text-foreground shadow-sm backdrop-blur-sm hover:bg-accent"
        >
            Откройте «Действия» у маршрута, чтобы изменить его. Перетащите узел или выберите родителя в меню; действия доступны с клавиатуры.
        </ContextHelp>
      </div>

      <div className="absolute left-0 top-0 origin-top-left" style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`, width: canvasWidth, height: canvasHeight }}>
        <svg className="pointer-events-none absolute inset-0 overflow-visible" width={canvasWidth} height={canvasHeight} aria-hidden="true">
          <defs><marker id="route-arrow" markerWidth="9" markerHeight="9" refX="8" refY="4.5" orient="auto"><path d="M 0 0 L 9 4.5 L 0 9 z" fill="hsl(var(--primary))" /></marker></defs>
          {positions.flatMap((parent) => (parent.node.children ?? []).map((child) => {
            const target = positionById.get(child.id);
            if (!target) return null;
            const startX = parent.x + parent.width;
            const startY = parent.y + parent.height / 2;
            const endX = target.x;
            const endY = target.y + target.height / 2;
            const control = Math.max(44, (endX - startX) * 0.45);
            return <path key={`${parent.node.id}-${child.id}`} d={`M ${startX} ${startY} C ${startX + control} ${startY}, ${endX - control} ${endY}, ${endX} ${endY}`} fill="none" stroke="hsl(var(--primary) / .48)" strokeWidth="2" markerEnd="url(#route-arrow)" />;
          }))}
          {connection && source && connectionPoint && <path d={`M ${source.x + source.width} ${source.y + source.height / 2} C ${source.x + source.width + 70} ${source.y + source.height / 2}, ${connectionPoint.x - 60} ${connectionPoint.y}, ${connectionPoint.x} ${connectionPoint.y}`} fill="none" stroke="hsl(var(--primary))" strokeWidth="2.5" strokeDasharray="7 6" />}
        </svg>

        {positions.map((position) => {
          const node = position.node;
          const label = pageName(config.pages, node.pageId);
          const childCount = node.children?.length ?? 0;
          return <div key={node.id} data-route-id={node.id} onContextMenu={(event) => contextMenu(event, node.id)} className="absolute" style={{ left: position.x, top: position.y, width: position.width, height: position.height }}>
              <button type="button" onFocus={() => setSelectedRouteId(node.id)} onClick={() => openPageForRoute(node)} aria-label={`Открыть страницу ${label ?? position.path}`} className={`group flex h-full w-full flex-col justify-between rounded-lg border bg-card px-3.5 py-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selectedRouteId === node.id ? "border-primary ring-2 ring-primary/20" : "border-border hover:border-primary/60"}`}>
              <span className="flex min-w-0 items-center gap-2">
                <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">{position.path === "/" ? <Home className="size-4" /> : <GitBranch className="size-4" />}</span>
                <code className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">{position.path}</code>
                {scale >= 0.75 && <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{displayNodeType(node)}</span>}
              </span>
              <span className="flex items-end justify-between gap-2 pl-9">
                <span className="min-w-0 truncate text-sm text-muted-foreground">{label ?? "Страница не назначена"}</span>
                <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{config.pages.find((page) => page.id === node.pageId)?.placements.length ?? 0} фич</span>
              </span>
              {childCount > 0 && <span className="absolute -bottom-5 left-9 text-[10px] text-muted-foreground">{childCount} дочерн. {childCount === 1 ? "маршрут" : "маршрута"}</span>}
            </button>
            <button type="button" aria-label={`Переместить маршрут ${position.path}: перетащите на другого узла или активируйте для выбора родителя`} title="Перетащите к родителю или активируйте, чтобы выбрать родителя" onClick={(event) => { if (event.detail === 0) setReparentRouteId(node.id); }} onPointerDown={(event) => { if (event.button !== 0) return; event.stopPropagation(); const canvas = event.currentTarget.closest("[data-route-canvas]"); if (canvas instanceof HTMLDivElement) canvas.setPointerCapture(event.pointerId); setConnection({ pointerId: event.pointerId, sourceId: node.id, clientX: event.clientX, clientY: event.clientY }); }} className="absolute top-1/2 z-10 flex -translate-y-1/2 items-center justify-center rounded-full bg-transparent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" style={{ right: -30 / scale, width: 44 / scale, height: 44 / scale }}><span aria-hidden="true" className="flex items-center justify-center rounded-full border-2 border-card bg-primary text-primary-foreground shadow-sm" style={{ width: 12 / scale, height: 12 / scale }} /></button>
            <Button type="button" size="icon-sm" variant="ghost" aria-haspopup="menu" aria-expanded={menu?.routeId === node.id} aria-label={`Действия маршрута ${position.path}`} className="absolute right-1 top-1 z-20 bg-card/90" style={{ width: 44 / scale, height: 44 / scale }} onClick={(event) => { menuTriggerRef.current = event.currentTarget; const bounds = event.currentTarget.getBoundingClientRect(); openMenuAt(bounds.left, bounds.bottom, node.id); }} onContextMenu={(event) => contextMenu(event, node.id)}><MoreHorizontal aria-hidden="true" style={{ width: 20 / scale, height: 20 / scale }} /></Button>
          </div>;
        })}

        {orphanPositions.map(({ page, x, y, width, height }) => <div key={`page-${page.id}`} className="absolute" style={{ left: x, top: y, width, height }} onContextMenu={(event) => contextMenu(event, undefined, page.id)}>
          <button type="button" onClick={() => openPageForOrphan(page.id)} className="flex h-full w-full flex-col justify-between rounded-lg border border-dashed border-warning/50 bg-card/90 px-3.5 py-3 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <span className="flex items-center gap-2"><span className="flex size-7 items-center justify-center rounded-md bg-warning/10 text-warning"><FilePlus2 className="size-4" /></span><span className="text-xs text-warning">Без маршрута</span></span>
            <span className="truncate pl-9 text-sm font-medium">{page.name}</span>
          </button>
          <Button type="button" size="icon-sm" variant="ghost" aria-haspopup="menu" aria-expanded={menu?.pageId === page.id} aria-label={`Действия страницы ${page.name}`} className="absolute right-1 top-1 size-7 bg-card/90" onClick={(event) => { menuTriggerRef.current = event.currentTarget; const bounds = event.currentTarget.getBoundingClientRect(); openMenuAt(bounds.left, bounds.bottom, undefined, page.id); }} onContextMenu={(event) => contextMenu(event, undefined, page.id)}><MoreHorizontal aria-hidden="true" /></Button>
        </div>)}
      </div>
    </div>

    <RouteContextMenu
      menu={menu}
      tree={root}
      panelRef={menuPanelRef}
      onKeyDown={menuKeyDown}
      onOpenPage={openPageForRoute}
      onAddChild={openChildDialog}
      onEditRoute={editRouteDialog}
      onSelectParent={(routeId) => { setReparentRouteId(routeId); setMenu(null); }}
      onChangeParent={changeParent}
      onMovePriority={(routeId, direction) => { applyTree(moveRoutePriority(root, routeId, direction)); setMenu(null); }}
      onDeleteBranch={deleteBranch}
      onOpenOrphanPage={openPageForOrphan}
      onCreateRouteForPage={openRootDialog}
      onCreatePage={() => openRootDialog()}
      onAttachExistingPage={() => openRootDialog(undefined, true)}
    />

    <RouteEditorDialog key={`${dialog?.mode ?? "closed"}-${dialog?.routeId ?? dialog?.parentId ?? "root"}`} state={dialog} pages={config.pages} tree={root} onClose={() => setDialog(null)} onApply={(route, parentId, page) => {
      if (dialog?.mode === "edit") applyRouteEdit(route, null, page);
      else makeNewRoute(route, parentId ?? null, page);
    }} />
    <RouteParentDialog routeId={reparentRouteId} tree={root} onClose={() => setReparentRouteId(null)} onApply={(sourceId, parentId) => { if (changeParent(sourceId, parentId)) setReparentRouteId(null); }} />
    <SystemPagesDialog open={systemPagesOpen} onOpenChange={setSystemPagesOpen} config={config} update={update} />
  </div>;
}
