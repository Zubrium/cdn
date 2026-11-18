import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowLeft, ArrowUp, ChevronRight, GripVertical, Pencil, Plus, Trash2 } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/api";
import { useConfirm } from "@/components/common/confirm";
import { useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { SortableItem, SortableList } from "@/components/ui/sortable-list";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import type { SiteMetadataOverride, SitePage, SitePlacement } from "@/types";
import { groupFeatureBuilds } from "./feature-builds";
import { selectedFeatures } from "./feature-selection";
import { useSiteWorkspace } from "./SiteWorkspace";
import { addRouteNode, describeRoutes, removePageReferences, routeCapturesForPage, updateRouteNode } from "./route-tree";
import { RouteEditorDialog } from "./components/routes/RouteDialogs";
import type { RouteDialogState } from "./components/routes/route-types";

type PageTab = "features" | "general" | "seo";

export default function PageDetailPage() {
  const { pageId = "" } = useParams();
  const { config, metadata, updateMetadata, update } = useSiteWorkspace();
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const page = config.pages.find((item) => item.id === pageId);
  useDocumentTitle(page?.name ?? "Страница");
  const snapshotId = searchParams.get("snapshot") ?? "";
  const listPath = `/site/pages${snapshotId ? `?snapshot=${encodeURIComponent(snapshotId)}` : ""}`;
  const requestedTab = searchParams.get("tab");
  const tab: PageTab = requestedTab === "general" || requestedTab === "seo" ? requestedTab : "features";
  const pageRoutes = useMemo(() => routeCapturesForPage(config.routeTree, pageId), [config.routeTree, pageId]);
  const [routeDialog, setRouteDialog] = useState<RouteDialogState | null>(null);
  const [seoImagePicker, setSeoImagePicker] = useState(false);
  const [newBuildId, setNewBuildId] = useState("");
  const buildsQuery = useQuery({ queryKey: ["site", "feature-builds"], queryFn: ({ signal }) => api.website.featureBuilds(signal) });
  const selected = selectedFeatures(config);
  const availableBuilds = (buildsQuery.data?.items ?? []).filter((build) => !selected.has(build.key) || selected.get(build.key) === build.version);
  const buildGroups = groupFeatureBuilds(availableBuilds);

  useEffect(() => {
    setNewBuildId("");
  }, [pageId]);

  if (!page) {
    return <div className="flex h-full items-center justify-center p-6 text-center">
      <div>
        <h1 className="text-xl font-semibold">Страница не найдена</h1>
        <p className="mt-2 text-sm text-muted-foreground">Возможно, она была удалена из рабочей копии.</p>
        <Button asChild variant="outline" className="mt-5 min-h-11"><Link to={listPath}><ArrowLeft />К страницам</Link></Button>
      </div>
    </div>;
  }

  const changeTab = (next: PageTab) => {
    const params = new URLSearchParams(searchParams);
    if (next === "features") params.delete("tab");
    else params.set("tab", next);
    setSearchParams(params, { replace: true });
  };
  const changePage = (patch: Partial<SitePage>) => update((current) => ({
    ...current,
    pages: current.pages.map((item) => item.id === page.id ? { ...item, ...patch } : item),
  }));
  const pageMetadata = metadata.pages?.[page.id] ?? {};
  const changeMetadata = (key: keyof SiteMetadataOverride, value?: string | boolean) => updateMetadata((current) => {
    const pages = { ...(current.pages ?? {}) };
    const next = { ...(pages[page.id] ?? {}) };
    if (value === undefined || value === "") delete next[key];
    else Object.assign(next, { [key]: value });
    if (Object.keys(next).length) pages[page.id] = next;
    else delete pages[page.id];
    return { ...current, pages };
  });
  const addFeature = () => {
    const build = availableBuilds.find((item) => item.id === newBuildId);
    if (!build) {
      toast.error("Выберите фичу из реестра.");
      return;
    }
    const placement: SitePlacement = {
      id: crypto.randomUUID(),
      build: { featureKey: build.key, version: build.version },
      slot: build.manifest.slots[0] ?? "main",
      arguments: {},
    };
    changePage({ placements: [...page.placements, placement] });
    setNewBuildId("");
    const params = new URLSearchParams(searchParams);
    navigate(`/site/pages/${page.id}/features/${placement.id}${params.size ? `?${params}` : ""}`);
  };
  const moveFeature = (index: number, direction: -1 | 1) => {
    const target = index + direction;
    if (target < 0 || target >= page.placements.length) return;
    const placements = [...page.placements];
    [placements[index], placements[target]] = [placements[target]!, placements[index]!];
    changePage({ placements });
  };
  const reorderFeatures = (orderedIDs: string[]) => {
    const byID = new Map(page.placements.map((placement) => [placement.id, placement]));
    const placements = orderedIDs.flatMap((id) => {
      const placement = byID.get(id);
      return placement ? [placement] : [];
    });
    if (placements.length === page.placements.length) changePage({ placements });
  };
  const deletePage = async () => {
    if (config.pages.length <= 1) {
      toast.error("В конфигурации должна остаться хотя бы одна страница.");
      return;
    }
    if (Object.values(config.systemPages ?? {}).includes(page.id)) {
      toast.error("Сначала снимите назначение системной страницы.");
      return;
    }
    const routeIds = new Set(pageRoutes.map((route) => route.id));
    const linked = config.pages.some((item) => item.id !== page.id && item.placements.some((placement) => placement.detailLink && routeIds.has(placement.detailLink.routeId)));
    if (linked) {
      toast.error("Сначала уберите ссылки коллекций на маршруты этой страницы.");
      return;
    }
    const nextTree = removePageReferences(config.routeTree, page.id);
    if (!describeRoutes(nextTree).some((route) => route.node.pageId)) {
      toast.error("Нельзя удалить последнюю страницу, на которую ведёт маршрут.");
      return;
    }
    if (!(await confirm({ title: "Удалить страницу?", description: `«${page.name}» будет удалена из рабочей копии. Маршруты останутся без страницы.`, confirmLabel: "Удалить страницу", destructive: true }))) return;
    update((current) => ({ ...current, pages: current.pages.filter((item) => item.id !== page.id), routeTree: nextTree }));
    navigate(listPath);
  };

  return <div className="h-full overflow-y-auto bg-background">
    <div className="mx-auto w-full max-w-5xl px-4 py-5 sm:px-7 sm:py-7">
      <Link to={listPath} className="inline-flex min-h-11 items-center gap-2 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft className="size-4" />Все страницы</Link>

      <header className="mt-3 border-b border-border pb-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <p className="text-xs text-muted-foreground">Страница</p>
            <input
              className="mt-1 block w-full min-w-0 rounded-sm border-0 bg-transparent p-0 text-2xl font-semibold tracking-tight outline-none focus-visible:ring-2 focus-visible:ring-ring"
              aria-label="Название страницы"
              value={page.name}
              onChange={(event) => changePage({ name: event.currentTarget.value })}
            />
          </div>
          <Button type="button" variant="ghost" className="min-h-11 text-destructive" onClick={() => void deletePage()}><Trash2 />Удалить страницу</Button>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-2">
          {pageRoutes.map((route) => <button key={route.id} type="button" className="inline-flex min-h-11 items-center gap-1.5 rounded-md border border-border px-3 font-mono text-xs hover:bg-accent" onClick={() => setRouteDialog({ mode: "edit", routeId: route.id })}><Pencil className="size-3.5" />{route.path}</button>)}
          <Button type="button" variant="outline" className="min-h-11" onClick={() => setRouteDialog({ mode: "root", existingPageId: page.id })}><Plus />Добавить маршрут</Button>
        </div>
      </header>

      <nav aria-label="Настройки страницы" className="flex gap-1 border-b border-border py-3">
        {([["features", "Фичи"], ["general", "Параметры"], ["seo", "SEO"]] as const).map(([value, label]) => <button key={value} type="button" aria-current={tab === value ? "page" : undefined} className={`min-h-11 rounded-md px-3 text-sm font-medium ${tab === value ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-muted"}`} onClick={() => changeTab(value)}>{label}</button>)}
      </nav>

      {tab === "features" && <section aria-labelledby="page-features-title" className="py-6">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div><h2 id="page-features-title" className="text-lg font-semibold">Фичи страницы</h2><p className="mt-1 text-sm text-muted-foreground">Откройте фичу, чтобы изменить её данные и аргументы.</p></div>
          <div className="flex min-w-0 flex-col gap-2 sm:flex-row">
            <label><span className="sr-only">Фича</span><select className="h-11 min-w-56 rounded-md border border-input bg-background px-3 text-sm" value={newBuildId} onChange={(event) => setNewBuildId(event.currentTarget.value)}><option value="">{buildsQuery.isLoading ? "Загружаем…" : "Добавить фичу"}</option>{buildGroups.map((group) => <optgroup key={group.key} label={group.name}>{group.versions.map((build) => <option key={build.id} value={build.id}>Версия {build.version}</option>)}</optgroup>)}</select></label>
            <Button type="button" className="min-h-11" disabled={!newBuildId} onClick={addFeature}><Plus />Добавить</Button>
          </div>
        </div>
        {page.placements.length === 0 ? <div className="mt-5 border-y border-dashed border-border py-10 text-center text-sm text-muted-foreground">На странице пока нет фич.</div> : <SortableList
          items={page.placements.map((placement) => placement.id)}
          onReorder={reorderFeatures}
          getItemLabel={(id) => {
            const placement = page.placements.find((item) => item.id === id);
            if (!placement) return "фича";
            const itemBuild = buildsQuery.data?.items.find((item) => item.key === placement.build.featureKey && item.version === placement.build.version);
            return `${itemBuild?.name ?? "Неизвестная фича"}, версия ${placement.build.version}`;
          }}
        >
          <ol className="mt-4 divide-y divide-border border-y border-border" aria-label="Порядок фич на странице">
            {page.placements.map((placement, index) => {
              const build = availableBuilds.find((item) => item.key === placement.build.featureKey && item.version === placement.build.version) ?? buildsQuery.data?.items.find((item) => item.key === placement.build.featureKey && item.version === placement.build.version);
              const query = new URLSearchParams(searchParams);
              return <SortableItem key={placement.id} id={placement.id}>{(handle) => <div className="flex min-w-0 items-center gap-1 py-2 sm:gap-2">
                <Button
                  ref={handle.setActivatorNodeRef}
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-10 shrink-0 touch-none cursor-grab text-muted-foreground active:cursor-grabbing"
                  aria-label={`Перетащить ${build?.name ?? "фичу"}`}
                  {...handle.attributes}
                  {...handle.listeners}
                ><GripVertical /></Button>
                <Link className="flex min-h-14 min-w-0 flex-1 items-center gap-3 rounded-md px-2 hover:bg-muted/60" to={`/site/pages/${page.id}/features/${placement.id}${query.size ? `?${query}` : ""}`}>
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-sm font-medium">{index + 1}</span>
                  <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{build?.name ?? "Неизвестная фича"}</span><span className="mt-0.5 block text-xs text-muted-foreground">Версия {placement.build.version} · {placement.slot}</span></span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                </Link>
                <Button type="button" size="icon" variant="ghost" className="size-11" aria-label={`Поднять ${build?.name ?? "фичу"}`} disabled={index === 0} onClick={() => moveFeature(index, -1)}><ArrowUp /></Button>
                <Button type="button" size="icon" variant="ghost" className="size-11" aria-label={`Опустить ${build?.name ?? "фичу"}`} disabled={index === page.placements.length - 1} onClick={() => moveFeature(index, 1)}><ArrowDown /></Button>
              </div>}</SortableItem>;
            })}
          </ol>
        </SortableList>}
      </section>}

      {tab === "general" && <section className="space-y-5 py-6">
        <div><h2 className="text-lg font-semibold">Параметры страницы</h2><p className="mt-1 text-sm text-muted-foreground">Название видно редакторам. Тип используется при настройке связей и detail-маршрутов.</p></div>
        <div className="grid max-w-2xl gap-4 sm:grid-cols-[minmax(0,1fr)_14rem]">
          <div className="space-y-1.5"><Label htmlFor="detail-page-name">Название</Label><Input id="detail-page-name" className="min-h-11" value={page.name} onChange={(event) => changePage({ name: event.currentTarget.value })} /></div>
          <label className="space-y-1.5 text-sm"><span className="font-medium">Тип страницы</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={page.kind} onChange={(event) => changePage({ kind: event.currentTarget.value as SitePage["kind"] })}><option value="page">Обычная страница</option><option value="collection-list">Список коллекции</option><option value="detail">Запись по маршруту</option></select></label>
        </div>
      </section>}

      {tab === "seo" && <section className="space-y-5 py-6">
        <div><h2 className="text-lg font-semibold">Поисковое представление</h2><p className="mt-1 text-sm text-muted-foreground">Пустые поля наследуют общие настройки сайта.</p></div>
        <div className="grid max-w-2xl gap-4">
          {([["siteName", "Заголовок страницы"], ["description", "Описание"], ["ogTitle", "Заголовок ссылки"], ["ogDescription", "Описание ссылки"]] as const).map(([key, label]) => <div key={key} className="space-y-1.5"><Label htmlFor={`page-seo-${key}`}>{label}</Label><Input id={`page-seo-${key}`} className="min-h-11" value={pageMetadata[key] ?? ""} onChange={(event) => changeMetadata(key, event.currentTarget.value)} /></div>)}
          <div><Button type="button" variant="outline" className="min-h-11" onClick={() => setSeoImagePicker(true)}>{pageMetadata.ogImageMediaId ? "Заменить изображение ссылки" : "Выбрать изображение ссылки"}</Button>{pageMetadata.ogImageMediaId && <Button type="button" variant="ghost" className="ml-2 min-h-11" onClick={() => changeMetadata("ogImageMediaId")}>Убрать</Button>}</div>
          <label className="flex min-h-11 items-center gap-2 text-sm"><input type="checkbox" className="size-4 accent-primary" checked={pageMetadata.noIndex ?? metadata.noIndex ?? false} onChange={(event) => changeMetadata("noIndex", event.currentTarget.checked)} />Запретить индексацию</label>
          <Button type="button" variant="outline" className="min-h-11 justify-self-start" onClick={() => updateMetadata((current) => { const pages = { ...(current.pages ?? {}) }; delete pages[page.id]; return { ...current, pages }; })}>Наследовать общие настройки</Button>
        </div>
      </section>}
    </div>

    <RouteEditorDialog key={`${routeDialog?.mode ?? "closed"}-${routeDialog?.routeId ?? routeDialog?.existingPageId ?? "root"}`} state={routeDialog} pages={config.pages} tree={config.routeTree} onClose={() => setRouteDialog(null)} onApply={(node, _parentId, newPage) => {
      update((current) => ({
        ...current,
        pages: newPage ? [...current.pages, newPage] : current.pages,
        routeTree: routeDialog?.mode === "edit"
          ? updateRouteNode(current.routeTree, node.id, () => node)
          : addRouteNode(current.routeTree, null, node),
        formatVersion: 3,
      }));
      setRouteDialog(null);
    }} />
    <MediaPickerDialog open={seoImagePicker} onClose={() => setSeoImagePicker(false)} accept="image" title="Изображение страницы для ссылки" initialSelected={pageMetadata.ogImageMediaId ? [pageMetadata.ogImageMediaId] : []} onSelect={(items) => {
      if (items[0]) changeMetadata("ogImageMediaId", items[0].id);
      setSeoImagePicker(false);
    }} />
  </div>;
}
