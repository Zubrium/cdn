import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, CornerDownRight, FilePlus2, Home, Pencil, Plus, Search, Settings2, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useDocumentTitle } from "@/components/common/page";
import type { RouteTreeNode, SitePage } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";
import PageEditorPage from "./PageEditorPage";
import { SystemPagesDialog } from "./components/SystemPagesDialog";
import { RouteEditorDialog, RouteParentDialog } from "./components/routes/RouteDialogs";
import type { RouteDialogState } from "./components/routes/route-types";
import {
  addRouteNode,
  describeRoutes,
  moveRoutePriority,
  removeRouteBranch,
  reparentRoute,
  updateRouteNode,
} from "./route-tree";
import { isDescendant } from "./components/routes/route-utils";
import {
  filterPageEntries,
  groupPageEntries,
  pageKindLabel,
  pageListEntries,
  sortPageEntries,
  type PageListSort,
} from "./pages-list";

export default function RouterPage() {
  useDocumentTitle("Страницы");
  const { config, update } = useSiteWorkspace();
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<PageListSort>("manual");
  const [selectedPageId, setSelectedPageId] = useState("");
  const [dialog, setDialog] = useState<RouteDialogState | null>(null);
  const [reparentRouteId, setReparentRouteId] = useState<string | null>(null);
  const [systemPagesOpen, setSystemPagesOpen] = useState(false);
  const inspectorRef = useRef<HTMLDivElement>(null);
  const featureBuilds = useQuery({ queryKey: ["site", "feature-builds"], queryFn: ({ signal }) => api.siteBuilder.featureBuilds(signal) });
  const descriptors = useMemo(() => describeRoutes(config.routeTree), [config.routeTree]);

  const entries = useMemo(() => {
    const names = new Map((featureBuilds.data?.items ?? []).map((build) => [build.key, build.name]));
    return pageListEntries(config.routeTree, config.pages, names);
  }, [config.pages, config.routeTree, featureBuilds.data]);
  const groups = useMemo(
    () => groupPageEntries(sortPageEntries(filterPageEntries(entries, query), sort)),
    [entries, query, sort],
  );
  const selectedPage =
    config.pages.find((page) => page.id === selectedPageId) ??
    entries[0]?.page;

  useEffect(() => {
    if (selectedPageId && !config.pages.some((page) => page.id === selectedPageId)) {
      setSelectedPageId("");
    }
  }, [config.pages, selectedPageId]);

  const applyTree = (tree: RouteTreeNode) => update((current) => ({ ...current, routeTree: tree, formatVersion: 3 }));

  const selectPage = (pageId: string) => {
    setSelectedPageId(pageId);
    if (window.matchMedia("(max-width: 1023px)").matches) {
      window.requestAnimationFrame(() => inspectorRef.current?.scrollIntoView({ block: "start", behavior: "smooth" }));
    }
  };

  const addPage = () => setDialog({ mode: "root" });
  const addRoute = (page: SitePage) => setDialog({ mode: "root", existingPageId: page.id });
  const editRoute = (routeId: string) => setDialog({ mode: "edit", routeId });

  const applyRouteEdit = (node: RouteTreeNode, _parentId: null, page?: SitePage) => {
    update((current) => ({
      ...current,
      pages: page ? [...current.pages, page] : current.pages,
      routeTree: updateRouteNode(current.routeTree, node.id, () => node),
      formatVersion: 3,
    }));
    setDialog(null);
    if (page) selectPage(page.id);
  };

  const createRoute = (node: RouteTreeNode, parentId: string | null, page?: SitePage) => {
    update((current) => ({
      ...current,
      ...(page ? { pages: [...current.pages, page] } : {}),
      routeTree: addRouteNode(current.routeTree, parentId, node),
      formatVersion: 3,
    }));
    setDialog(null);
    if (page) selectPage(page.id);
  };

  const deleteRoute = (routeId: string) => {
    const routes = describeRoutes(config.routeTree);
    const route = routes.find((item) => item.id === routeId);
    if (!route) return;
    const branchIds = new Set(routes
      .filter((item) => item.id === routeId || isDescendant(config.routeTree, routeId, item.id))
      .map((item) => item.id));
    const remaining = routes.filter((item) => !branchIds.has(item.id));
    if (!remaining.some((item) => item.node.pageId)) {
      toast.error("Нельзя удалить последний маршрут сайта.");
      return;
    }
    const message = branchIds.size > 1
      ? `Удалить маршрут ${route.path} и ${branchIds.size - 1} дочерних? Страницы останутся в списке без этих маршрутов.`
      : `Удалить маршрут ${route.path}? Страница останется в списке.`;
    if (!window.confirm(message)) return;
    applyTree(removeRouteBranch(config.routeTree, routeId));
  };

  const moveRoute = (routeId: string, offset: -1 | 1) => {
    applyTree(moveRoutePriority(config.routeTree, routeId, offset));
  };

  return <div className="h-full min-h-0 overflow-y-auto bg-background">
    <div className="mx-auto grid min-h-full max-w-[1600px] gap-4 p-4 sm:p-6 lg:grid-cols-[minmax(19rem,0.75fr)_minmax(0,2fr)] lg:items-start">
      <section className="min-w-0 rounded-xl border border-border bg-card" aria-labelledby="pages-list-heading">
        <div className="border-b border-border p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h1 id="pages-list-heading" className="text-xl font-semibold tracking-tight">Страницы</h1>
              <p className="mt-1 text-sm text-muted-foreground">Маршруты, состав фич и общие настройки сайта.</p>
            </div>
            <div className="flex gap-1">
              <Button type="button" variant="outline" size="icon" className="size-11" aria-label="Настроить системные страницы" onClick={() => setSystemPagesOpen(true)}><Settings2 /></Button>
              <Button type="button" size="icon" className="size-11" aria-label="Добавить страницу" onClick={addPage}><Plus /></Button>
            </div>
          </div>
          <div className="mt-4 grid gap-2">
            <label className="relative block">
              <span className="sr-only">Поиск страниц</span>
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input type="search" className="min-h-11 pl-9" value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="Название, URL или фича" />
            </label>
            <label>
              <span className="sr-only">Сортировка страниц</span>
              <select className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={sort} onChange={(event) => setSort(event.currentTarget.value as PageListSort)}>
                <option value="manual">Приоритет маршрутов</option>
                <option value="name">По названию</option>
                <option value="route">По маршруту</option>
                <option value="kind">По типу</option>
              </select>
            </label>
          </div>
        </div>

        <div className="max-h-[calc(100dvh-15rem)] overflow-y-auto p-2 lg:sticky lg:top-4">
          {groups.length === 0 ? <div className="p-6 text-center">
            <p className="text-sm font-medium">{entries.length ? "Ничего не найдено" : "На сайте пока нет страниц"}</p>
            <p className="mt-1 text-sm text-muted-foreground">{entries.length ? "Измените поисковый запрос." : "Создайте первую страницу и её маршрут."}</p>
            {!entries.length && <Button type="button" className="mt-4 min-h-11" onClick={addPage}><FilePlus2 />Создать страницу</Button>}
          </div> : groups.map((group) => <section key={group.key} className="mb-3 last:mb-0" aria-labelledby={`page-group-${group.key}`}>
            <div className="flex items-center justify-between px-2 py-2">
              <h2 id={`page-group-${group.key}`} className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{group.label}</h2>
              <span className="text-xs text-muted-foreground">{group.entries.length}</span>
            </div>
            <ol className="space-y-1">
              {group.entries.map(({ page, routes, primaryRoute }) => {
                const selected = selectedPage?.id === page.id;
                return <li key={page.id} className={`rounded-lg border transition-colors ${selected ? "border-primary bg-primary/5" : "border-transparent hover:border-border hover:bg-muted/30"}`}>
                  <button type="button" className="flex w-full min-w-0 items-start gap-3 rounded-t-lg px-3 py-3 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-current={selected ? "page" : undefined} onClick={() => selectPage(page.id)}>
                    <span className={`mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-md ${selected ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground"}`}>{primaryRoute?.path === "/" ? <Home className="size-4" /> : <FilePlus2 className="size-4" />}</span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{page.name}</span>
                      <span className="mt-0.5 block truncate font-mono text-xs text-muted-foreground">{primaryRoute?.path ?? "Без маршрута"}</span>
                      <span className="mt-1 block text-xs text-muted-foreground">{pageKindLabel(page.kind)} · {page.placements.length} {page.placements.length === 1 ? "фича" : "фич"}</span>
                    </span>
                  </button>
                  <div className="flex flex-wrap gap-1 px-3 pb-3 pl-[3.75rem]">
                    {routes.map((route) => {
                      const descriptor = descriptors.find((item) => item.id === route.id);
                      const siblings = descriptor
                        ? descriptors.filter((item) => item.parentId === descriptor.parentId && item.depth === descriptor.depth)
                        : [];
                      const priorityIndex = siblings.findIndex((item) => item.id === route.id);
                      return <span key={route.id} className="flex max-w-full flex-wrap items-center rounded-md border border-border bg-background">
                      <button type="button" className="min-h-11 w-full max-w-full justify-start truncate px-3 text-left text-xs font-medium hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:w-auto" title={`Изменить ${route.path}`} onClick={() => editRoute(route.id)}><Pencil className="mr-1 inline size-3.5" />{route.path}</button>
                      <button type="button" disabled={priorityIndex <= 0} className="flex size-11 items-center justify-center border-l border-border/60 text-muted-foreground hover:bg-accent disabled:opacity-35" aria-label={`Поднять маршрут ${route.path} в приоритете`} onClick={() => moveRoute(route.id, -1)}><ArrowUp className="size-4" /></button>
                      <button type="button" disabled={priorityIndex < 0 || priorityIndex === siblings.length - 1} className="flex size-11 items-center justify-center border-l border-border/60 text-muted-foreground hover:bg-accent disabled:opacity-35" aria-label={`Опустить маршрут ${route.path} в приоритете`} onClick={() => moveRoute(route.id, 1)}><ArrowDown className="size-4" /></button>
                      <button type="button" className="flex size-11 items-center justify-center border-l border-border/60 text-muted-foreground hover:bg-accent" aria-label={`Добавить дочерний маршрут к ${route.path}`} onClick={() => setDialog({ mode: "child", parentId: route.id })}><CornerDownRight className="size-4" /></button>
                      <button type="button" className="flex size-11 items-center justify-center border-l border-border/60 text-muted-foreground hover:bg-destructive/10 hover:text-destructive" aria-label={`Удалить маршрут ${route.path}`} onClick={() => deleteRoute(route.id)}><Trash2 className="size-4" /></button>
                    </span>;
                    })}
                    <button type="button" className="inline-flex min-h-11 items-center gap-1 rounded-md border border-dashed border-border px-3 text-xs text-muted-foreground hover:border-primary hover:text-foreground" onClick={() => addRoute(page)}><Plus className="size-4" />Маршрут</button>
                  </div>
                </li>;
              })}
            </ol>
          </section>)}
        </div>
      </section>

      <div id="page-inspector" ref={inspectorRef} className="min-w-0 scroll-mt-4">
        {selectedPage ? <PageEditorPage
          pageId={selectedPage.id}
          embedded
          onDeleted={() => {
            setSelectedPageId("");
            window.requestAnimationFrame(() => window.scrollTo({ top: 0, behavior: "smooth" }));
          }}
          onRouteEdit={(routeId) => {
            const descriptorId = routeId ?? describeRoutes(config.routeTree).find((route) => route.node.pageId === selectedPage.id)?.id;
            if (!descriptorId) addRoute(selectedPage);
            else editRoute(descriptorId);
          }}
          onReparentRoute={setReparentRouteId}
        /> : <div className="rounded-xl border border-dashed border-border bg-card p-8 text-center text-sm text-muted-foreground">Выберите страницу слева или создайте новую.</div>}
      </div>
    </div>

    <RouteEditorDialog key={`${dialog?.mode ?? "closed"}-${dialog?.routeId ?? dialog?.parentId ?? dialog?.existingPageId ?? "root"}`} state={dialog} pages={config.pages} tree={config.routeTree} onClose={() => setDialog(null)} onApply={(route, parentId, page) => {
      if (dialog?.mode === "edit") applyRouteEdit(route, null, page);
      else createRoute(route, parentId ?? null, page);
    }} />
    <RouteParentDialog routeId={reparentRouteId} tree={config.routeTree} onClose={() => setReparentRouteId(null)} onApply={(routeId, parentId) => {
      const result = reparentRoute(config.routeTree, routeId, parentId);
      if (!result.tree) {
        toast.error(result.error ?? "Не удалось переместить маршрут");
        return;
      }
      applyTree(result.tree);
      setReparentRouteId(null);
    }} />
    <SystemPagesDialog open={systemPagesOpen} onOpenChange={setSystemPagesOpen} config={config} update={update} />
  </div>;
}
