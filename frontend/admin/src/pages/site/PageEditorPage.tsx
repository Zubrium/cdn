import { removePlacement, selectedFeatures } from "./feature-selection";
import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle, ArrowDown, ArrowLeft, ArrowUp, Plus, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ErrorState } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import type { FeatureBuild, SiteMetadataOverride, SitePage, SitePlacement } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";
import { describeRoutes, detailRoutes as getDetailRoutes, removePageReferences, routeCapturesForPage } from "./route-tree";
import { groupFeatureBuilds } from "./feature-builds";
import { PlacementEditor } from "./components/page-editor/PlacementEditor";
import { buildFor } from "./components/page-editor/schema";

export { addArgumentArrayItem, ArgumentField, moveArgumentArrayItem, removeArgumentArrayItem, updateArgumentArrayItem } from "./components/page-editor/FeatureArgumentsEditor";
export { DetailSEOFieldsEditor, updateDetailSEOField } from "./components/page-editor/StructuralBindingsEditor";
export type { SchemaField } from "./components/page-editor/schema";

export default function PageEditorPage({ pageId, onBack }: { pageId: string; onBack: () => void }) {
  const { config, metadata, updateMetadata, update, dirty, saving, conflict, localSaveError, revision, reload, save } = useSiteWorkspace();
  const [searchParams] = useSearchParams();
  const snapshotId = searchParams.get("snapshot") ?? "";
  const confirm = useConfirm();
  const [registryBuilds, setBuilds] = useState<FeatureBuild[]>([]);
  const [buildsLoading, setBuildsLoading] = useState(true);
  const selected = selectedFeatures(config);
  const builds = registryBuilds.filter((build) => !selected.has(build.key) || selected.get(build.key) === build.version);
  const buildGroups = groupFeatureBuilds(builds);
  const [selectedPlacementId, setSelectedPlacementId] = useState("");
  const [tab, setTab] = useState<"features" | "general" | "seo">("features");
  const [seoImagePicker, setSeoImagePicker] = useState(false);
  const [newBuildId, setNewBuildId] = useState("");
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    setTab("features");
  }, [pageId]);
  useEffect(() => {
    let active = true;
    api.siteBuilder.featureBuilds()
      .then((result) => { if (active) setBuilds(result.items); })
      .catch((reason: unknown) => { if (active) setError(reason); })
      .finally(() => { if (active) setBuildsLoading(false); });
    return () => { active = false; };
  }, []);
  const page = config.pages.find((item) => item.id === pageId);
  useEffect(() => {
    setSelectedPlacementId((current) => page?.placements.some((placement) => placement.id === current) ? current : page?.placements[0]?.id ?? "");
  }, [page?.id, page?.placements]);
  const pageRoutes = useMemo(() => routeCapturesForPage(config.routeTree, pageId), [config.routeTree, pageId]);
  const detailRouteChoices = useMemo(() => getDetailRoutes(config.routeTree, config.pages), [config.routeTree, config.pages]);
  const changePage = (id: string, patch: Partial<SitePage>) => update((current) => ({ ...current, pages: current.pages.map((item) => item.id === id ? { ...item, ...patch } : item) }));
  const pageMetadata = metadata.pages?.[pageId] ?? {};
  const changeMetadata = (key: keyof SiteMetadataOverride, value?: string | boolean) => updateMetadata((current) => {
    const pages = { ...(current.pages ?? {}) };
    const next = { ...(pages[pageId] ?? {}) };
    if (value === undefined || value === "") delete next[key];
    else Object.assign(next, { [key]: value });
    if (Object.keys(next).length) pages[pageId] = next;
    else delete pages[pageId];
    return { ...current, pages };
  });
  const removePage = (id: string) => {
    if (config.pages.length <= 1) { toast.error("В конфигурации должна остаться хотя бы одна страница."); return; }
    if (Object.values(config.systemPages ?? {}).includes(id)) { toast.error("Сначала снимите назначение этой страницы в настройках системных страниц."); return; }
    const routeIds = new Set(pageRoutes.map((route) => route.id));
    const linked = config.pages.some((item) => item.id !== id && item.placements.some((placement) => placement.detailLink && routeIds.has(placement.detailLink.routeId)));
    if (linked) { toast.error("Сначала уберите ссылки коллекций на маршруты этой страницы."); return; }
    const nextRouteTree = removePageReferences(config.routeTree, id);
    if (!describeRoutes(nextRouteTree).some((route) => route.node.pageId)) { toast.error("Нельзя удалить последнюю страницу, на которую ведёт маршрут."); return; }
    const nextConfig = { ...config, pages: config.pages.filter((item) => item.id !== id), routeTree: nextRouteTree };
    if (!window.confirm(`Удалить страницу «${page?.name ?? ""}»? Маршруты останутся и станут самостоятельными без страницы.`)) return;
    update(nextConfig);
    onBack();
  };
  const addPlacement = () => {
    if (!page) return;
    const build = builds.find((item) => item.id === newBuildId);
    if (!build) { toast.error("Выберите сборку фичи из реестра."); return; }
    const next: SitePlacement = { id: `placement-${crypto.randomUUID().slice(0, 8)}`, build: { featureKey: build.key, version: build.version }, slot: build.manifest.slots[0] ?? "main", arguments: {} };
    changePage(page.id, { placements: [...page.placements, next] });
    setSelectedPlacementId(next.id);
    setNewBuildId("");
  };

  const retryBuilds = async () => {
    setBuildsLoading(true);
    setError(null);
    try {
      const result = await api.siteBuilder.featureBuilds();
      setBuilds(result.items);
    } catch (reason) {
      setError(reason);
    } finally {
      setBuildsLoading(false);
    }
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

  if (error) return <ErrorState error={error} title="Не удалось загрузить сборки фич" onRetry={() => void retryBuilds()} />;
  if (!page) return <div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 p-6 text-center"><p className="text-sm text-muted-foreground">Эта страница уже удалена или недоступна.</p><Button type="button" variant="outline" className="min-h-11" onClick={onBack}><ArrowLeft />К маршрутам</Button></div>;
  return <div className="h-full min-h-0 overflow-y-auto bg-background px-4 py-4 sm:px-7 sm:py-6">
    <div className="mx-auto max-w-5xl space-y-5">
      <div className="sticky top-0 z-20 -mx-4 -mt-4 flex flex-wrap items-center justify-between gap-3 border-b border-border bg-background/95 px-4 py-3 backdrop-blur-sm sm:-mx-7 sm:-mt-6 sm:px-7 sm:py-4">
        <div className="flex min-w-0 items-start gap-3">
          <Button type="button" size="icon" variant="outline" className="size-11 shrink-0" aria-label="Вернуться к маршрутам" onClick={onBack}><ArrowLeft /></Button>
          <div className="min-w-0"><p className="text-xs text-muted-foreground">Страницы и маршруты · ревизия {revision}</p><h1 className="truncate text-2xl font-semibold tracking-tight">{page.name}</h1><p className="mt-1 text-sm text-muted-foreground">Соберите страницу из фич и настройте аргументы их размещения.</p></div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <span role="status" aria-live="polite" className={`flex items-center gap-1.5 text-xs ${dirty ? "text-warning" : "text-success"}`}><span aria-hidden="true" className={`size-1.5 rounded-full ${dirty ? "bg-warning" : "bg-success"}`} />{dirty ? "Есть изменения" : "Сохранено"}</span>
          <Button type="button" size="sm" className="min-h-11" disabled={!dirty || saving} onClick={() => void save()}>{saving ? "Сохраняем…" : "Сохранить"}</Button>
          {localSaveError && <span role="alert" className="max-w-64 text-xs text-destructive">Локальная копия недоступна или изменена в другой вкладке.</span>}
          {conflict && <><span role="status" className="hidden items-center gap-1 text-xs text-destructive sm:flex"><AlertTriangle className="size-3.5" />Конфликт ревизий</span><Button type="button" size="sm" className="min-h-11" variant="outline" disabled={saving} onClick={() => void refreshAfterConflict()}><RotateCcw />Загрузить актуальную версию</Button></>}
          <Button type="button" size="sm" variant="outline" className="min-h-11 text-destructive" onClick={() => removePage(page.id)}><Trash2 />Удалить</Button>
        </div>
      </div>

      <Card><CardHeader className="pb-3"><CardTitle className="text-base">Адреса страницы</CardTitle><CardDescription>Несколько маршрутов могут открывать одну и ту же страницу.</CardDescription></CardHeader><CardContent className="flex flex-wrap gap-2">{pageRoutes.length ? pageRoutes.map((route) => <code key={route.id} className="rounded-md border border-border bg-muted/40 px-2.5 py-1.5 text-sm">{route.path}</code>) : <p className="text-sm text-warning">У страницы пока нет маршрута. Вернитесь на канвас и создайте для неё корневой путь.</p>}</CardContent></Card>

      <Tabs value={tab} onValueChange={(value) => setTab(value as typeof tab)} className="min-w-0">
        <TabsList aria-label="Разделы страницы" className="h-auto w-full justify-start overflow-x-auto">
          <TabsTrigger value="features" className="min-h-11 px-3">Фичи</TabsTrigger>
          <TabsTrigger value="general" className="min-h-11 px-3">Общие настройки</TabsTrigger>
          <TabsTrigger value="seo" className="min-h-11 px-3">SEO</TabsTrigger>
        </TabsList>

        <TabsContent value="features">
          <Card>
            <CardHeader className="space-y-3">
              <div><CardTitle className="text-base">Фичи страницы</CardTitle><CardDescription>Выберите фичу в списке, чтобы настроить её справа. Версии сгруппированы под одной фичей; у уже включённой фичи используется версия снапшота.</CardDescription></div>
              <div className="flex flex-wrap gap-2">
                <label className="min-w-0 flex-1"><span className="sr-only">Фича и версия сборки</span><select className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={newBuildId} disabled={buildsLoading || buildGroups.length === 0} onChange={(event) => setNewBuildId(event.currentTarget.value)}><option value="">{buildsLoading ? "Загружаем реестр сборок…" : buildGroups.length ? "Выберите фичу из реестра" : "В реестре пока нет сборок"}</option>{buildGroups.map((group) => <optgroup key={group.key} label={`${group.name} · ${group.key}`}>{group.versions.map((build) => <option key={build.id} value={build.id}>v{build.version}{!selected.has(group.key) && build.id === group.latest.id ? " · последняя по SemVer" : ""}</option>)}</optgroup>)}</select></label>
                <Button type="button" size="sm" variant="outline" className="min-h-11" disabled={!newBuildId} onClick={addPlacement}><Plus />Добавить фичу</Button>
              </div>
              {!buildsLoading && buildGroups.length === 0 && <p className="text-sm text-muted-foreground">Загрузите ZIP фичи в <Link className="underline underline-offset-4" to={`/snapshots/registry${snapshotId ? `?snapshot=${encodeURIComponent(snapshotId)}` : ""}`}>реестр сборок</Link>, чтобы добавить её к странице.</p>}
            </CardHeader>
            <CardContent>
              <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(12rem,1fr)_minmax(0,2fr)]">
                <div className="min-w-0 space-y-2">
                  {page.placements.length === 0 ? <p className="rounded-md border border-dashed border-border p-4 text-sm text-muted-foreground">Добавьте первую фичу из реестра.</p> : page.placements.map((placement, index) => <div key={placement.id} className="flex min-w-0 items-center gap-1 rounded-md border border-border p-1">
                    <Button type="button" variant={selectedPlacementId === placement.id ? "secondary" : "ghost"} className="min-h-11 min-w-0 flex-1 justify-start" aria-pressed={selectedPlacementId === placement.id} onClick={() => setSelectedPlacementId(placement.id)}><span className="truncate">{buildFor(builds, placement)?.name ?? placement.build.featureKey}</span></Button>
                    <Button type="button" variant="ghost" size="icon" className="size-11 shrink-0" aria-label={`Поднять фичу ${buildFor(builds, placement)?.name ?? placement.build.featureKey}`} disabled={index === 0} onClick={() => { const placements = [...page.placements]; [placements[index - 1], placements[index]] = [placements[index], placements[index - 1]]; changePage(page.id, { placements }); }}><ArrowUp /></Button>
                    <Button type="button" variant="ghost" size="icon" className="size-11 shrink-0" aria-label={`Опустить фичу ${buildFor(builds, placement)?.name ?? placement.build.featureKey}`} disabled={index === page.placements.length - 1} onClick={() => { const placements = [...page.placements]; [placements[index], placements[index + 1]] = [placements[index + 1], placements[index]]; changePage(page.id, { placements }); }}><ArrowDown /></Button>
                  </div>)}
                </div>
                <div className="min-w-0" aria-live="polite">
                  {page.placements.filter((item) => item.id === selectedPlacementId).map((placement) => <PlacementEditor key={placement.id} page={page} placement={placement} builds={builds} config={config} snapshotId={snapshotId} buildsLoading={buildsLoading} onChange={(next) => changePage(page.id, { placements: page.placements.map((item) => item.id === placement.id ? next : item) })} onRemove={() => {
                    if (!window.confirm("Удалить это размещение из локальной копии? Модель записей останется независимой от набора размещений, а родительский снапшот не изменится.")) return;
                    update(removePlacement(config, page.id, placement.id));
                    setSelectedPlacementId("");
                  }} />)}
                  {!page.placements.some((item) => item.id === selectedPlacementId) && <p className="rounded-md border border-dashed border-border p-4 text-sm text-muted-foreground">{page.placements.length ? "Выберите фичу, чтобы настроить её размещение." : "Настройки размещения появятся здесь после добавления фичи."}</p>}
                </div>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="general">
          <Card><CardHeader><CardTitle className="text-base">Параметры страницы</CardTitle><CardDescription>Название видно редакторам; тип используется при настройке коллекций и detail-фич.</CardDescription></CardHeader><CardContent className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_13rem]"><div className="space-y-1.5"><Label htmlFor="page-name">Название страницы</Label><Input className="min-h-11" id="page-name" value={page.name} onChange={(event) => changePage(page.id, { name: event.currentTarget.value })} /></div><label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Тип страницы</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={page.kind} onChange={(event) => changePage(page.id, { kind: event.currentTarget.value as SitePage["kind"] })}><option value="page">Обычная страница</option><option value="collection-list">Список коллекции</option><option value="detail">Карточка записи</option></select></label></CardContent></Card>
        </TabsContent>

        <TabsContent value="seo">
          <Card><CardHeader><CardTitle className="text-base">Поисковое представление страницы</CardTitle><CardDescription>Пустые поля наследуют общие настройки сайта.</CardDescription></CardHeader><CardContent className="grid gap-3 sm:max-w-2xl">{([ ["siteName", "Заголовок страницы"], ["description", "Описание страницы"], ["ogTitle", "Заголовок ссылки"], ["ogDescription", "Описание ссылки"] ] as const).map(([key, label]) => <div className="space-y-1.5" key={key}><Label htmlFor={`page-seo-${key}`}>{label}</Label><Input className="min-h-11" id={`page-seo-${key}`} value={pageMetadata[key] ?? ""} onChange={(event) => changeMetadata(key, event.currentTarget.value)} /></div>)}<div className="flex flex-wrap items-center gap-2"><span id="page-og-image-label" className="text-sm">Изображение для ссылки</span><Button type="button" variant="outline" aria-labelledby="page-og-image-label" className="min-h-11" onClick={() => setSeoImagePicker(true)}>{pageMetadata.ogImageMediaId ? "Заменить изображение" : "Выбрать изображение"}</Button>{pageMetadata.ogImageMediaId && <Button type="button" variant="ghost" className="min-h-11" aria-label="Убрать изображение для ссылки" onClick={() => changeMetadata("ogImageMediaId")}>Убрать</Button>}</div><label className="flex min-h-11 items-center gap-2"><input type="checkbox" className="size-4 accent-primary" checked={pageMetadata.noIndex ?? metadata.noIndex ?? false} onChange={(event) => changeMetadata("noIndex", event.currentTarget.checked)} />Запретить индексацию этой страницы</label><Button type="button" variant="outline" className="min-h-11 justify-self-start" onClick={() => updateMetadata((current) => { const pages = { ...(current.pages ?? {}) }; delete pages[pageId]; return { ...current, pages }; })}>Наследовать общие настройки</Button></CardContent></Card>
        </TabsContent>
      </Tabs>

      <Card><CardContent className="p-4 text-sm text-muted-foreground">Доступные маршруты для path-аргументов: {detailRouteChoices.map((route) => route.path).join(", ") || "маршрутов с параметрами пока нет"}.</CardContent></Card>
      <MediaPickerDialog open={seoImagePicker} onClose={() => setSeoImagePicker(false)} accept="image" title="Изображение страницы для ссылки" onSelect={(items) => { if (items[0]) changeMetadata("ogImageMediaId", items[0].id); setSeoImagePicker(false); }} />
    </div>
  </div>;
}
