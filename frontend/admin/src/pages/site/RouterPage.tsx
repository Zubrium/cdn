import { useMemo, useState } from "react";
import { ChevronRight, FilePlus2, FileUp, Plus, Search, Settings2 } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api";
import { useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { RouteTreeNode, SitePage } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";
import { SystemPagesDialog } from "./components/SystemPagesDialog";
import { siteContextQuery } from "./site-query";
import { RouteEditorDialog } from "./components/routes/RouteDialogs";
import { FileRouteDialog, type NewFileRoute } from "./components/routes/FileRouteDialog";
import type { RouteDialogState } from "./components/routes/route-types";
import { addRouteNode, describeRoutes } from "./route-tree";
import {
  filterPageEntries,
  groupPageEntries,
  pageListEntries,
  sortPageEntries,
  type PageListSort,
} from "./pages-list";

export default function RouterPage() {
  useDocumentTitle("Страницы");
  const { config, update } = useSiteWorkspace();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<PageListSort>("manual");
  const [routeDialog, setRouteDialog] = useState<RouteDialogState | null>(null);
  const [fileDialogOpen, setFileDialogOpen] = useState(false);
  const [systemPagesOpen, setSystemPagesOpen] = useState(false);
  const snapshotId = searchParams.get("snapshot") ?? "";
  const pageID = searchParams.get("page_id") ?? "";
  const suffix = siteContextQuery(snapshotId, pageID);
  const featureBuilds = useQuery({ queryKey: ["site", "feature-builds"], queryFn: ({ signal }) => api.website.featureBuilds(signal) });
  const media = useQuery({ queryKey: ["media", "public-file-routes"], queryFn: ({ signal }) => api.media.list({ status: "ready", limit: 200 }, signal) });
  const groups = useMemo(() => {
    const featureNames = new Map((featureBuilds.data?.items ?? []).map((build) => [build.key, build.name]));
    const fileNames = new Map((media.data?.items ?? []).map((item) => [item.id, item.original_filename]));
    return groupPageEntries(sortPageEntries(filterPageEntries(pageListEntries(config.routeTree, config.pages, featureNames, fileNames), query), sort));
  }, [config.pages, config.routeTree, featureBuilds.data, media.data, query, sort]);
  const entryCount = groups.reduce((count, group) => count + group.entries.length, 0);
  const existingPaths = describeRoutes(config.routeTree).map((route) => route.path);

  const createPageRoute = (page: SitePage | undefined, node: RouteTreeNode) => {
    update((current) => ({
      ...current,
      pages: page ? [...current.pages, page] : current.pages,
      routeTree: addRouteNode(current.routeTree, null, node),
      formatVersion: 3,
    }));
    setRouteDialog(null);
    if (page) navigate(`/site/pages/${page.id}${suffix}`);
  };

  const createFileRoute = ({ path, file }: NewFileRoute) => {
    const id = crypto.randomUUID();
    update((current) => ({
      ...current,
      routeTree: addRouteNode(current.routeTree, null, {
        id,
        type: "path",
        path,
        fileId: file.id,
        children: [],
      }),
      formatVersion: 3,
    }));
    setFileDialogOpen(false);
    navigate(`/site/pages/files/${id}${suffix}`);
  };

  return <div className="h-full min-h-0 overflow-y-auto bg-background">
    <div className="mx-auto w-full max-w-5xl px-4 py-5 sm:px-7 sm:py-7">
      <header className="flex flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
        <div><h1 className="text-2xl font-semibold tracking-tight">Страницы</h1><p className="mt-1 text-sm text-muted-foreground">Страницы и файловые маршруты выбранного снапшота.</p></div>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" className="min-h-11" onClick={() => setFileDialogOpen(true)}><FileUp />Новый файл</Button>
          <Button type="button" className="min-h-11" onClick={() => setRouteDialog({ mode: "root" })}><Plus />Новая страница</Button>
        </div>
      </header>

      <div className="flex flex-col gap-2 border-b border-border py-4 sm:flex-row">
        <label className="relative min-w-0 flex-1">
          <span className="sr-only">Поиск страниц и файлов</span>
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" className="min-h-11 pl-9" value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="Название, URL или фича" />
        </label>
        <label className="sm:w-56"><span className="sr-only">Сортировка</span><select className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={sort} onChange={(event) => setSort(event.currentTarget.value as PageListSort)}><option value="manual">Приоритет маршрутов</option><option value="name">По названию</option><option value="route">По маршруту</option><option value="kind">По типу</option></select></label>
        <Button type="button" variant="outline" size="icon" className="size-11 shrink-0" aria-label="Системные страницы" onClick={() => setSystemPagesOpen(true)}><Settings2 /></Button>
      </div>

      {entryCount === 0 ? <section className="py-16 text-center">
        <h2 className="font-medium">{query ? "Ничего не найдено" : "Здесь пока ничего нет"}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{query ? "Измените поисковый запрос." : "Создайте страницу или файловый маршрут."}</p>
      </section> : <div className="divide-y divide-border">
        {groups.map((group) => <section key={group.key} className="py-5" aria-labelledby={`page-group-${group.key}`}>
          <div className="mb-2 flex items-center justify-between"><h2 id={`page-group-${group.key}`} className="text-xs font-medium text-muted-foreground">{group.label}</h2><span className="text-xs text-muted-foreground">{group.entries.length}</span></div>
          <ul className="divide-y divide-border/70">
            {group.entries.map((entry) => {
              const href = entry.kind === "page"
                ? `/site/pages/${entry.page.id}${suffix}`
                : `/site/pages/files/${entry.route.id}${suffix}`;
              return <li key={entry.kind === "page" ? entry.page.id : entry.route.id}>
                <Link to={href} className="group flex min-h-16 min-w-0 items-center gap-3 px-1 py-3 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">{entry.kind === "file" ? <FilePlus2 className="size-4" /> : <span className="text-sm font-semibold">{entry.name.slice(0, 1).toLocaleUpperCase("ru")}</span>}</span>
                  <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{entry.name}</span><span className="mt-0.5 block truncate font-mono text-xs text-muted-foreground">{entry.primaryRoute?.path ?? "Без маршрута"}</span><span className="mt-0.5 block truncate text-xs text-muted-foreground">{entry.kind === "page" ? `${entry.kindLabel} · ${entry.page.placements.length} ${entry.page.placements.length === 1 ? "фича" : "фич"}${entry.routes.length > 1 ? ` · маршрутов ${entry.routes.length}` : ""}` : "Файл"}</span></span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
                </Link>
              </li>;
            })}
          </ul>
        </section>)}
      </div>}
    </div>

    <RouteEditorDialog state={routeDialog} pages={config.pages} tree={config.routeTree} onClose={() => setRouteDialog(null)} onApply={(node, _parentId, page) => createPageRoute(page, node)} />
    <FileRouteDialog open={fileDialogOpen} existingPaths={existingPaths} onClose={() => setFileDialogOpen(false)} onCreate={createFileRoute} />
    <SystemPagesDialog open={systemPagesOpen} onOpenChange={setSystemPagesOpen} config={config} update={update} />
  </div>;
}
